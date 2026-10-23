package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"moefrp/server/internal/store"
	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

func newService(t *testing.T, single bool) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, Options{SingleSession: single, Params: testParams})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func mustRegister(t *testing.T, s *Service, st *store.Store, nick, pw string) {
	t.Helper()
	code := validate.NewInviteCode()
	if err := store.InsertInvite(context.Background(), st.DB(), code, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(context.Background(), nick, pw, code, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
}

func codeOf(err error) apierr.Code { return apierr.CodeOf(err) }

// 校验密码之后、发令牌之前，密码被重置：不能再发令牌（否则重置密码吊销会话后，
// 用旧密码换来的会话会插在它之后，被盗的账号重置密码也没用）。
func TestLoginRacesWithReset(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t, false)
	mustRegister(t, s, st, "alice", "old-password")
	code, _, err := s.IssueResetCode(ctx, "alice", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.afterVerify = func() {
		s.afterVerify = nil
		if err := s.ResetPassword(ctx, "alice", code, "new-password", ClientMeta{}); err != nil {
			t.Errorf("reset: %v", err)
		}
	}
	_, err = s.Login(ctx, "alice", "old-password", ClientMeta{})
	if codeOf(err) != apierr.InvalidCredentials {
		t.Fatalf("密码在校验后被重置，登录应失败：%v", err)
	}
	if n, _ := store.CountActiveSessions(ctx, st.DB(), mustUserID(t, st, "alice"), time.Now().Unix()); n != 0 {
		t.Fatalf("不应留下会话，实际 %d 个", n)
	}
	if _, err := s.Login(ctx, "alice", "new-password", ClientMeta{}); err != nil {
		t.Fatalf("新密码应能登录：%v", err)
	}
}

// 同理：校验后账号被停用，不发令牌。
func TestLoginRacesWithDisable(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t, false)
	mustRegister(t, s, st, "alice", "password-1")
	s.afterVerify = func() {
		s.afterVerify = nil
		store.SetUserStatus(ctx, st.DB(), mustUserID(t, st, "alice"), "disabled", 1)
	}
	if _, err := s.Login(ctx, "alice", "password-1", ClientMeta{}); codeOf(err) != apierr.AccountDisabled {
		t.Fatalf("应返回 account_disabled：%v", err)
	}
}

// 改密码：校验旧密码之后，另一台设备重置了密码（吊销了全部会话）。
// 这个已被吊销、但请求还在途中的会话不能把新密码覆盖掉。
func TestChangePasswordRacesWithReset(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t, false)
	mustRegister(t, s, st, "alice", "old-password")
	res, err := s.Login(ctx, "alice", "old-password", ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(ctx, res.Token)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := s.IssueResetCode(ctx, "alice", "", "")
	s.afterVerify = func() {
		s.afterVerify = nil
		if err := s.ResetPassword(ctx, "alice", code, "reset-password", ClientMeta{}); err != nil {
			t.Errorf("reset: %v", err)
		}
	}
	err = s.ChangePassword(ctx, p, "old-password", "attacker-password", ClientMeta{})
	if codeOf(err) != apierr.SessionExpired {
		t.Fatalf("会话已被吊销，改密码应失败：%v", err)
	}
	if _, err := s.Login(ctx, "alice", "reset-password", ClientMeta{}); err != nil {
		t.Fatalf("重置后的密码应仍然有效：%v", err)
	}
}

// 改密码：校验旧密码之后，密码在别处已被改过（会话没被吊销的情况，例如另一个改密码请求保留了它自己的会话）。
func TestChangePasswordStaleHash(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t, false)
	mustRegister(t, s, st, "alice", "old-password")
	res, _ := s.Login(ctx, "alice", "old-password", ClientMeta{})
	p, _ := s.Authenticate(ctx, res.Token)
	s.afterVerify = func() {
		s.afterVerify = nil
		h, _ := HashPassword("changed-elsewhere", testParams)
		store.UpdatePasswordHash(ctx, st.DB(), p.User.ID, h, 1)
	}
	if err := s.ChangePassword(ctx, p, "old-password", "second-password", ClientMeta{}); codeOf(err) != apierr.InvalidCredentials {
		t.Fatalf("哈希已变化，应拒绝：%v", err)
	}
	if _, err := s.Login(ctx, "alice", "changed-elsewhere", ClientMeta{}); err != nil {
		t.Fatalf("别处改的密码不应被覆盖：%v", err)
	}
}

// 并发的错误登录不能绕过失败上限：最多真正校验 MaxFailures 次，锁定时长也不会被并发叠加。
func TestConcurrentLoginLockout(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t, false)
	mustRegister(t, s, st, "alice", "correct-horse")
	const n = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]apierr.Code, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Login(ctx, "alice", "wrong-password", ClientMeta{})
			codes[i] = codeOf(err)
		}(i)
	}
	close(start)
	wg.Wait()
	counts := map[apierr.Code]int{}
	for _, c := range codes {
		counts[c]++
	}
	for c := range counts {
		if c != apierr.InvalidCredentials && c != apierr.AccountLocked && c != apierr.RateLimited {
			t.Fatalf("意外的错误码：%v", counts)
		}
	}
	rows, _ := store.ListAudit(ctx, st.DB(), store.AuditLoginFailed, 1000)
	if len(rows) != DefaultLockout.MaxFailures {
		t.Fatalf("应恰好真正校验 %d 次，实际 %d 次：%v", DefaultLockout.MaxFailures, len(rows), counts)
	}
	if d, locked := s.lockout.Check("alice", time.Now()); !locked || d > 15*time.Minute || d < 14*time.Minute {
		t.Fatalf("应锁定约 15 分钟（不被并发翻倍）：%v %v", d, locked)
	}
	if _, err := s.Login(ctx, "alice", "correct-horse", ClientMeta{}); codeOf(err) != apierr.AccountLocked {
		t.Fatalf("锁定期间正确的密码也应被拒绝：%v", err)
	}
}

// 不符合昵称格式的输入不进锁定表（否则任意长度的昵称能把内存撑爆），但仍然是 invalid_credentials。
func TestLoginMalformedNicknameNotTracked(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, false)
	for i := 0; i < 20; i++ {
		_, err := s.Login(ctx, strings.Repeat("x", 1000)+string(rune('a'+i)), "pw", ClientMeta{})
		if codeOf(err) != apierr.InvalidCredentials {
			t.Fatalf("应返回 invalid_credentials：%v", err)
		}
	}
	for _, nick := range []string{"a-b", "中文", "ab", "has space"} {
		if _, err := s.Login(ctx, nick, "pw", ClientMeta{}); codeOf(err) != apierr.InvalidCredentials {
			t.Fatalf("%q：%v", nick, err)
		}
	}
	if s.lockout.Len() != 0 {
		t.Fatalf("锁定表里不应有记录，实际 %d 条", s.lockout.Len())
	}
}

// 旧库里可能有现在的保留词（旧版只保留了 all），这些账号必须还能登录。
func TestLoginReservedButLegacyNickname(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t, false)
	h, _ := HashPassword("admin-password", testParams)
	if err := store.InsertUser(ctx, st.DB(), store.User{ID: "legacy-admin", Nickname: "admin", PasswordHash: h, Role: "admin", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "Admin", "admin-password", ClientMeta{}); err != nil {
		t.Fatalf("旧账号 admin 应能登录：%v", err)
	}
}

func TestLockoutCapacity(t *testing.T) {
	p := DefaultLockout
	p.MaxEntries = 10
	l := NewLockout(p)
	now := time.Now()
	for i := 0; i < 5; i++ {
		l.Fail("victim", now) // 锁定
	}
	for i := 0; i < 100; i++ {
		l.Fail("user"+string(rune('a'+i%26))+string(rune('a'+i/26)), now)
	}
	if l.Len() > 11 {
		t.Fatalf("记录数应被限制在上限附近，实际 %d", l.Len())
	}
	if _, locked := l.Check("victim", now); !locked {
		t.Fatal("刷满表不能解除已有的锁定")
	}
}

func TestAttemptLifecycle(t *testing.T) {
	l := NewLockout(DefaultLockout)
	now := time.Now()
	var atts []*Attempt
	for i := 0; i < 5; i++ {
		a, d, _ := l.Begin("bob", now)
		if d != Allowed {
			t.Fatalf("第 %d 个预占应成功", i+1)
		}
		atts = append(atts, a)
	}
	if _, d, _ := l.Begin("bob", now); d != Busy {
		t.Fatal("已有 5 个进行中，第 6 个应返回 Busy")
	}
	atts[0].Cancel()
	atts[0].Cancel() // 重复调用无害
	a, d, _ := l.Begin("bob", now)
	if d != Allowed {
		t.Fatal("Cancel 后应释放名额")
	}
	a.Succeed()
	a.Fail(now) // Succeed 之后再 Fail 是空操作
	for _, x := range atts[1:] {
		x.Cancel()
	}
	l.Sweep(now.Add(time.Hour))
	if l.Len() != 0 {
		t.Fatalf("全部结束后应能清理，剩 %d", l.Len())
	}
	if _, err := mustNotPanic(func() { var nilAtt *Attempt; nilAtt.Cancel(); nilAtt.Succeed() }); err != nil {
		t.Fatal(err)
	}
}

func mustNotPanic(f func()) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("panic")
		}
	}()
	f()
	return true, nil
}

func mustUserID(t *testing.T, st *store.Store, nick string) string {
	t.Helper()
	u, err := store.GetUserByNickname(context.Background(), st.DB(), nick)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
