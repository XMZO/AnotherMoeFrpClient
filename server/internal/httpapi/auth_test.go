package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"moefrp/server/internal/admin"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/config"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// ---- 注册 ----

func TestRegister(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	var out api.RegisterResponse
	e.do(req{method: "POST", path: api.RouteRegister, body: api.RegisterRequest{Nickname: "Alice_1", Password: "correct-horse", InviteCode: "  " + strings.ToLower(code) + " "}}).
		expect(http.StatusCreated).decode(&out)
	if out.User.Nickname != "Alice_1" || out.User.Role != model.RoleUser || out.User.ID == "" {
		t.Fatalf("注册结果不对：%+v", out.User)
	}
	inv, _ := store.GetInvite(context.Background(), e.st.DB(), code)
	if inv.UsedBy != out.User.ID || inv.UsedAt == nil {
		t.Fatalf("邀请码应被该用户占用：%+v", inv)
	}
	// 注册后能登录
	e.login("alice_1", "correct-horse")
}

func TestRegisterValidation(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name  string
		body  any
		code  apierr.Code
		field string
	}{
		{"短昵称", api.RegisterRequest{Nickname: "ab", Password: "12345678", InviteCode: "FRPT-AAAA-AAAA"}, apierr.ValidationFailed, "nickname"},
		{"非法字符", api.RegisterRequest{Nickname: "a-b-c", Password: "12345678", InviteCode: "FRPT-AAAA-AAAA"}, apierr.ValidationFailed, "nickname"},
		{"保留词", api.RegisterRequest{Nickname: "Admin", Password: "12345678", InviteCode: "FRPT-AAAA-AAAA"}, apierr.ValidationFailed, "nickname"},
		{"短密码", api.RegisterRequest{Nickname: "alice", Password: "1234567", InviteCode: "FRPT-AAAA-AAAA"}, apierr.ValidationFailed, "password"},
		{"长密码", api.RegisterRequest{Nickname: "alice", Password: strings.Repeat("a", 129), InviteCode: "FRPT-AAAA-AAAA"}, apierr.ValidationFailed, "password"},
		{"邀请码格式", api.RegisterRequest{Nickname: "alice", Password: "12345678", InviteCode: "ABCD"}, apierr.InviteInvalid, "inviteCode"},
		{"邀请码校验位", api.RegisterRequest{Nickname: "alice", Password: "12345678", InviteCode: "FRPT-AAAA-AAAB"}, apierr.InviteInvalid, "inviteCode"},
		{"未知字段", `{"nickname":"alice","password":"12345678","inviteCode":"x","invite_code":"y"}`, apierr.ValidationFailed, "invite_code"},
		{"类型错误", `{"nickname":123}`, apierr.ValidationFailed, "nickname"},
		{"空请求体", "", apierr.ValidationFailed, ""},
		{"非法 JSON", `{"nickname":`, apierr.ValidationFailed, ""},
		{"多个 JSON", `{} {}`, apierr.ValidationFailed, ""},
	}
	for _, tc := range cases {
		er := e.do(req{method: "POST", path: api.RouteRegister, body: tc.body}).apiErr(tc.code)
		if tc.field != "" && !hasField(er, tc.field) {
			t.Errorf("%s：details.fields 应包含 %s：%+v", tc.name, tc.field, er.Details)
		}
	}
}

func TestRegisterInviteStates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := func(nick, code string) resp {
		return e.do(req{method: "POST", path: api.RouteRegister, body: api.RegisterRequest{Nickname: nick, Password: "12345678", InviteCode: code}})
	}
	// 格式正确但库里没有
	reg("alice", validate.NewInviteCode()).apiErr(apierr.InviteInvalid)
	// 已撤销
	revoked := e.invite()
	if err := e.admin.RevokeInvite(ctx, admin.Actor{}, revoked); err != nil {
		t.Fatal(err)
	}
	reg("alice", revoked).apiErr(apierr.InviteInvalid)
	// 已使用
	used := e.invite()
	reg("alice", used).expect(http.StatusCreated)
	reg("bob", used).apiErr(apierr.InviteUsed)
	// 昵称已被注册（不区分大小写）；邀请码不能因此被消耗
	fresh := e.invite()
	reg("ALICE", fresh).apiErr(apierr.NicknameTaken)
	reg("carol", fresh).expect(http.StatusCreated)
	// 没有有效邀请码时，不能借注册探测昵称是否存在
	reg("alice", validate.NewInviteCode()).apiErr(apierr.InviteInvalid)
}

// 并发注册抢同一个邀请码：只能有一个成功（修掉旧版的重复使用问题）。
func TestConcurrentRegisterSameInvite(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	const n = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = map[int]int{}
		bodies  []string
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := e.do(req{method: "POST", path: api.RouteRegister, body: api.RegisterRequest{
				Nickname: fmt.Sprintf("racer_%02d", i), Password: "12345678", InviteCode: code,
			}})
			mu.Lock()
			results[r.Code]++
			if r.Code != http.StatusCreated && r.Code != http.StatusConflict {
				bodies = append(bodies, r.Body.String())
			}
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()
	if results[http.StatusCreated] != 1 || results[http.StatusConflict] != n-1 {
		t.Fatalf("应恰好 1 个成功、%d 个 409：%v %v", n-1, results, bodies)
	}
	users, _ := store.ListUsers(context.Background(), e.st.DB())
	if len(users) != 1 {
		t.Fatalf("只应创建 1 个用户，实际 %d 个", len(users))
	}
	inv, _ := store.GetInvite(context.Background(), e.st.DB(), code)
	if inv.UsedBy != users[0].ID {
		t.Fatal("邀请码应属于唯一成功的用户")
	}
}

// 并发注册同一个昵称（各自用不同的邀请码）：只有一个成功，失败的不消耗邀请码。
func TestConcurrentRegisterSameNickname(t *testing.T) {
	e := newEnv(t)
	const n = 10
	codes := make([]string, n)
	for i := range codes {
		codes[i] = e.invite()
	}
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = e.do(req{method: "POST", path: api.RouteRegister, body: api.RegisterRequest{
				Nickname: "samename", Password: "12345678", InviteCode: codes[i],
			}}).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			ok++
		} else if s != http.StatusConflict {
			t.Fatalf("意外的状态码 %d", s)
		}
	}
	if ok != 1 {
		t.Fatalf("应恰好 1 个成功：%v", statuses)
	}
	unused, _ := store.ListInvites(context.Background(), e.st.DB(), true)
	if len(unused) != n-1 {
		t.Fatalf("失败的注册不应消耗邀请码：剩 %d 个", len(unused))
	}
}

// ---- 登录 ----

func TestLogin(t *testing.T) {
	e := newEnv(t)
	u := e.register("alice", "correct-horse")
	var out api.LoginResponse
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: " ALICE ", Password: "correct-horse", DeviceName: "家里的电脑"}}).
		expect(http.StatusOK).decode(&out)
	if !strings.HasPrefix(out.Token, "mfs_") || out.User.ID != u.ID {
		t.Fatalf("登录结果不对：%+v", out)
	}
	if want := e.clock.Now().Add(30 * 24 * time.Hour); !out.ExpiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", out.ExpiresAt, want)
	}

	// 库里只存 sha256(token)，不存令牌本身
	var hash []byte
	var installID, device string
	if err := e.st.DB().QueryRow(`SELECT token_hash, install_id, device_name FROM sessions`).Scan(&hash, &installID, &device); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(out.Token))
	if !bytes.Equal(hash, sum[:]) || installID != "install-test" || device != "家里的电脑" {
		t.Fatalf("会话记录不对：install=%s device=%s", installID, device)
	}
	assertNoSecretInDB(t, e, out.Token)

	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "wrong-password"}}).apiErr(apierr.InvalidCredentials)
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "nobody", Password: "whatever"}}).apiErr(apierr.InvalidCredentials)
	er := e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "", Password: ""}}).apiErr(apierr.ValidationFailed)
	if !hasField(er, "nickname") || !hasField(er, "password") {
		t.Fatalf("应列出缺失的字段：%+v", er.Details)
	}
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "x", DeviceName: "a\nb"}}).apiErr(apierr.ValidationFailed)
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "x"},
		header: map[string]string{api.HeaderInstallID: "bad id!"}}).apiErr(apierr.ValidationFailed)
}

// assertNoSecretInDB 扫描所有表的文本列，确保令牌明文没有落库。
func assertNoSecretInDB(t *testing.T, e *env, secrets ...string) {
	t.Helper()
	for _, q := range []string{
		`SELECT coalesce(detail,'') || coalesce(target,'') FROM audit_log`,
		`SELECT id || install_id || coalesce(device_name,'') FROM sessions`,
		`SELECT password_hash FROM users`,
	} {
		rows, err := e.st.DB().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			rows.Scan(&s)
			for _, sec := range secrets {
				if strings.Contains(s, sec) {
					t.Fatalf("数据库里出现了明文机密（%s）", q)
				}
			}
		}
		rows.Close()
	}
}

func TestLoginDisabledAccount(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	if _, err := e.admin.SetDisabled(context.Background(), admin.Actor{}, "alice", true); err != nil {
		t.Fatal(err)
	}
	e.me(tok).apiErr(apierr.SessionExpired) // 停用时吊销了会话
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "password-alice"}}).apiErr(apierr.AccountDisabled)
	// 密码错误时不透露账号已停用
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "wrong"}}).apiErr(apierr.InvalidCredentials)
	if _, err := e.admin.SetDisabled(context.Background(), admin.Actor{}, "alice", false); err != nil {
		t.Fatal(err)
	}
	e.login("alice", "password-alice")
}

// 按昵称失败锁定：5 次后锁 15 分钟，再失败翻倍；不存在的昵称同样会锁（PLAN 7.4）。
func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	e.register("alice", "correct-horse")
	bad := api.LoginRequest{Nickname: "alice", Password: "wrong"}
	for i := 0; i < 4; i++ {
		e.do(req{method: "POST", path: api.RouteLogin, body: bad}).apiErr(apierr.InvalidCredentials)
	}
	r := e.do(req{method: "POST", path: api.RouteLogin, body: bad})
	er := r.apiErr(apierr.AccountLocked)
	if er.Details[apierr.DetailRetryAfter] != float64(15*60) || r.Header().Get("Retry-After") != "900" {
		t.Fatalf("第 5 次失败应锁定 900 秒：%+v %s", er.Details, r.Header().Get("Retry-After"))
	}
	// 锁定期间正确的密码也不行
	good := api.LoginRequest{Nickname: "ALICE", Password: "correct-horse"}
	e.do(req{method: "POST", path: api.RouteLogin, body: good}).apiErr(apierr.AccountLocked)
	// 15 分钟后解锁，再失败一次直接锁 30 分钟
	e.clock.Add(15 * time.Minute)
	er = e.do(req{method: "POST", path: api.RouteLogin, body: bad}).apiErr(apierr.AccountLocked)
	if er.Details[apierr.DetailRetryAfter] != float64(30*60) {
		t.Fatalf("应翻倍到 30 分钟：%+v", er.Details)
	}
	e.clock.Add(30 * time.Minute)
	e.login("alice", "correct-horse") // 成功后清零
	for i := 0; i < 4; i++ {
		e.do(req{method: "POST", path: api.RouteLogin, body: bad}).apiErr(apierr.InvalidCredentials)
	}

	// 不存在的昵称：行为与存在的完全一样
	ghost := api.LoginRequest{Nickname: "ghost", Password: "x"}
	for i := 0; i < 4; i++ {
		e.do(req{method: "POST", path: api.RouteLogin, body: ghost}).apiErr(apierr.InvalidCredentials)
	}
	e.do(req{method: "POST", path: api.RouteLogin, body: ghost}).apiErr(apierr.AccountLocked)

	// 审计：失败有记录；不存在的昵称不写进 target（用户可能把密码误填进昵称框）
	rows, _ := store.ListAudit(context.Background(), e.st.DB(), store.AuditLoginFailed, 100)
	var known, unknown int
	for _, r := range rows {
		if r.Target == "alice" && r.ActorID != "" {
			known++
		}
		if strings.Contains(r.Detail, "unknown_user") {
			unknown++
			if r.Target != "" || strings.Contains(r.Detail, "ghost") {
				t.Fatalf("不存在的昵称不应出现在审计里：%+v", r)
			}
		}
	}
	if known == 0 || unknown != 5 {
		t.Fatalf("登录失败的审计记录不对：known=%d unknown=%d", known, unknown)
	}
}

// 用户不存在时也做一次假哈希：耗时应与密码错误同一量级。
// 用默认参数（64MB）让差异足够明显；只做粗略比较，避免在慢机器上误报。
func TestLoginUnknownUserDoesDummyHash(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	e := newEnv(t, withParams(auth.DefaultParams))
	e.register("alice", "correct-horse")
	measure := func(nick string) time.Duration {
		start := time.Now()
		e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: nick, Password: "wrong-password"}}).apiErr(apierr.InvalidCredentials)
		return time.Since(start)
	}
	known := min(measure("alice"), measure("alice"))
	unknown := min(measure("nobody1"), measure("nobody2"))
	if unknown < known/3 {
		t.Fatalf("不存在的用户耗时 %v，明显短于密码错误的 %v，假哈希没有生效", unknown, known)
	}
}

// ---- 单设备模式 ----

func TestSingleSessionReplaced(t *testing.T) {
	e := newEnv(t) // 默认 single_session = true
	e.register("alice", "correct-horse")
	a := e.login("alice", "correct-horse")
	e.me(a).expect(http.StatusOK)
	b := e.login("alice", "correct-horse")
	e.me(a).apiErr(apierr.SessionReplaced)
	e.do(req{method: "GET", path: api.RouteState, token: a}).apiErr(apierr.SessionReplaced)
	e.me(b).expect(http.StatusOK)

	rows, _ := store.ListAudit(context.Background(), e.st.DB(), store.AuditLogin, 10)
	if len(rows) != 2 || !strings.Contains(rows[0].Detail, `"replaced":1`) {
		t.Fatalf("第二次登录的审计应记录挤掉了 1 个会话：%+v", rows)
	}
}

func TestMultiSessionWhenDisabled(t *testing.T) {
	e := newEnv(t, withConfig(func(c *config.Config) { c.SingleSession = false }))
	e.register("alice", "correct-horse")
	a := e.login("alice", "correct-horse")
	b := e.login("alice", "correct-horse")
	e.me(a).expect(http.StatusOK)
	e.me(b).expect(http.StatusOK)
}

// ---- 会话 ----

func TestAuthHeader(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	e.do(req{method: "GET", path: api.RouteMe}).apiErr(apierr.Unauthorized)
	e.do(req{method: "GET", path: api.RouteMe, header: map[string]string{"Authorization": tok}}).apiErr(apierr.Unauthorized)
	e.do(req{method: "GET", path: api.RouteMe, header: map[string]string{"Authorization": "Basic abc"}}).apiErr(apierr.Unauthorized)
	e.do(req{method: "GET", path: api.RouteMe, token: "not-a-token"}).apiErr(apierr.Unauthorized)
	forged, _ := auth.NewToken()
	e.do(req{method: "GET", path: api.RouteMe, token: forged}).apiErr(apierr.Unauthorized)
	e.do(req{method: "GET", path: api.RouteMe, header: map[string]string{"Authorization": "bearer " + tok}}).expect(http.StatusOK)

	var me api.MeResponse
	e.me(tok).expect(http.StatusOK).decode(&me)
	if me.User.Nickname != "alice" || me.User.Role != "user" {
		t.Fatalf("me 不对：%+v", me)
	}
}

// 滑动过期：30 天内没有使用才失效；last_seen_at 最多每 10 分钟写一次库。
func TestSlidingExpiry(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	lastSeen := func() int64 {
		var v int64
		e.st.DB().QueryRow(`SELECT last_seen_at FROM sessions`).Scan(&v)
		return v
	}
	t0 := lastSeen()

	e.clock.Add(5 * time.Minute)
	e.me(tok).expect(http.StatusOK)
	if lastSeen() != t0 {
		t.Fatal("10 分钟内不应写库")
	}
	e.clock.Add(6 * time.Minute)
	e.me(tok).expect(http.StatusOK)
	if lastSeen() != e.clock.Now().Unix() {
		t.Fatal("超过 10 分钟应更新 last_seen_at")
	}
	// 每 29 天用一次，可以一直用下去
	for i := 0; i < 3; i++ {
		e.clock.Add(29 * 24 * time.Hour)
		e.me(tok).expect(http.StatusOK)
	}
	// 31 天不用就过期
	e.clock.Add(31 * 24 * time.Hour)
	e.me(tok).apiErr(apierr.SessionExpired)
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	e.do(req{method: "POST", path: api.RouteLogout, token: tok}).expect(http.StatusNoContent)
	e.me(tok).apiErr(apierr.Unauthorized)
	e.do(req{method: "POST", path: api.RouteLogout, token: tok}).apiErr(apierr.Unauthorized)
}

func TestAdminKick(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	n, err := e.admin.Kick(context.Background(), admin.Actor{}, "alice")
	if err != nil || n != 1 {
		t.Fatalf("应吊销 1 个会话：%d %v", n, err)
	}
	e.me(tok).apiErr(apierr.SessionExpired)
}

// ---- 改密码 ----

func TestChangePassword(t *testing.T) {
	e := newEnv(t, withConfig(func(c *config.Config) { c.SingleSession = false }))
	e.register("alice", "old-password")
	cur := e.login("alice", "old-password")
	other := e.login("alice", "old-password")
	path := api.RouteChangePassword

	e.do(req{method: "POST", path: path, token: cur, body: api.ChangePasswordRequest{OldPassword: "wrong", NewPassword: "new-password"}}).
		apiErr(apierr.InvalidCredentials)
	er := e.do(req{method: "POST", path: path, token: cur, body: api.ChangePasswordRequest{OldPassword: "", NewPassword: "short"}}).
		apiErr(apierr.ValidationFailed)
	if !hasField(er, "oldPassword") || !hasField(er, "newPassword") {
		t.Fatalf("应列出两个字段：%+v", er.Details)
	}
	e.do(req{method: "POST", path: path, body: api.ChangePasswordRequest{OldPassword: "old-password", NewPassword: "new-password"}}).
		apiErr(apierr.Unauthorized)

	e.do(req{method: "POST", path: path, token: cur, body: api.ChangePasswordRequest{OldPassword: "old-password", NewPassword: "new-password"}}).
		expect(http.StatusNoContent)
	e.me(cur).expect(http.StatusOK)           // 当前会话保留
	e.me(other).apiErr(apierr.SessionExpired) // 其他会话被吊销
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "old-password"}}).
		apiErr(apierr.InvalidCredentials)
	e.login("alice", "new-password")
	assertNoSecretInDB(t, e, "old-password", "new-password")
}

func TestChangePasswordLockout(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	for i := 0; i < 4; i++ {
		e.do(req{method: "POST", path: api.RouteChangePassword, token: tok, body: api.ChangePasswordRequest{OldPassword: "wrong", NewPassword: "new-password"}}).
			apiErr(apierr.InvalidCredentials)
	}
	e.do(req{method: "POST", path: api.RouteChangePassword, token: tok, body: api.ChangePasswordRequest{OldPassword: "wrong", NewPassword: "new-password"}}).
		apiErr(apierr.AccountLocked)
}

// ---- 重置密码 ----

func TestResetPassword(t *testing.T) {
	e := newEnv(t, withConfig(func(c *config.Config) { c.SingleSession = false }))
	ctx := context.Background()
	e.register("alice", "forgotten-pw")
	e.register("bob", "bob-password")
	s1 := e.login("alice", "forgotten-pw")
	s2 := e.login("alice", "forgotten-pw")

	code, exp, err := e.admin.ResetCode(ctx, admin.Actor{}, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(e.clock.Now().Add(24 * time.Hour)) {
		t.Fatalf("重置码应 24 小时有效：%v", exp)
	}
	reset := func(nick, c, pw string) resp {
		return e.do(req{method: "POST", path: api.RouteResetPassword, body: api.ResetPasswordRequest{Nickname: nick, Code: c, NewPassword: pw}})
	}
	reset("bob", code, "new-password-1").apiErr(apierr.ResetCodeInvalid) // 昵称不匹配
	reset("alice", "RST-AAAAA-AAAAA-AAAAA", "new-password-1").apiErr(apierr.ResetCodeInvalid)
	reset("alice", code, "short").apiErr(apierr.ValidationFailed)
	reset("", "", "new-password-1").apiErr(apierr.ValidationFailed)

	reset("ALICE", " "+strings.ToLower(code)+" ", "new-password-1").expect(http.StatusNoContent)
	e.me(s1).apiErr(apierr.SessionExpired)
	e.me(s2).apiErr(apierr.SessionExpired)
	e.login("alice", "new-password-1")
	reset("alice", code, "new-password-2").apiErr(apierr.ResetCodeInvalid) // 一次性

	// 过期
	code2, _, _ := e.admin.ResetCode(ctx, admin.Actor{}, "alice")
	e.clock.Add(24*time.Hour + time.Second)
	reset("alice", code2, "new-password-3").apiErr(apierr.ResetCodeInvalid)

	// 新码作废旧码
	c3, _, _ := e.admin.ResetCode(ctx, admin.Actor{}, "alice")
	c4, _, _ := e.admin.ResetCode(ctx, admin.Actor{}, "alice")
	reset("alice", c3, "new-password-4").apiErr(apierr.ResetCodeInvalid)
	reset("alice", c4, "new-password-4").expect(http.StatusNoContent)

	assertNoSecretInDB(t, e, code, code2, c3, c4)
	if _, _, err := e.admin.ResetCode(ctx, admin.Actor{}, "nobody"); err == nil {
		t.Fatal("不存在的用户应报错")
	}
}

// 重置密码清除失败锁定。
func TestResetPasswordClearsLockout(t *testing.T) {
	e := newEnv(t)
	e.register("alice", "forgotten-pw")
	for i := 0; i < 5; i++ {
		e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "guess"}})
	}
	code, _, _ := e.admin.ResetCode(context.Background(), admin.Actor{}, "alice")
	e.do(req{method: "POST", path: api.RouteResetPassword, body: api.ResetPasswordRequest{Nickname: "alice", Code: code, NewPassword: "brand-new-pw"}}).
		expect(http.StatusNoContent)
	e.login("alice", "brand-new-pw")
}

// ---- 旧 argon2 哈希 ----

// 以下哈希由 Python argon2-cffi 25.1.0 现场生成（脚本见 internal/auth/testdata），
// 参数与旧 server.py 一致；密码是测试密码，不来自旧 users.db。
const (
	legacyPassword  = "legacy-Passw0rd!"
	legacyHash      = "$argon2id$v=19$m=65536,t=3,p=2$8CXQOgjZR2VGrLN7x7Xsgg$VDdwfb4n404r6/bg+pjNpjPKmtW/1nz8BSvjWHY5UC4"
	cffiDefaultHash = "$argon2id$v=19$m=65536,t=3,p=4$SGTcDaPZMjXr8nckvE5lvg$pkCdHZzDAzVwB2a7VgaHduXi/M8wHu36dnUywQnmrEg"
)

// insertLegacyUser 模拟从旧库导入的用户：密码哈希原样保留。
func insertLegacyUser(t *testing.T, e *env, nick, hash string) {
	t.Helper()
	now := e.clock.Now().Unix()
	err := store.InsertUser(context.Background(), e.st.DB(), store.User{
		ID: "3f2b1c9e-0000-4000-8000-000000000001", Nickname: nick, PasswordHash: hash,
		Role: model.RoleVIP, Status: model.StatusActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func storedHash(t *testing.T, e *env, nick string) string {
	t.Helper()
	u, err := store.GetUserByNickname(context.Background(), e.st.DB(), nick)
	if err != nil {
		t.Fatal(err)
	}
	return u.PasswordHash
}

// 用旧 Python 服务端生成的 argon2 哈希能登录成功（Phase 1 验收标准）；
// 参数与当前一致，不需要重新哈希。
func TestLegacyHashLogin(t *testing.T) {
	e := newEnv(t, withParams(auth.DefaultParams))
	insertLegacyUser(t, e, "OldUser", legacyHash)
	var out api.LoginResponse
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "olduser", Password: legacyPassword}}).
		expect(http.StatusOK).decode(&out)
	if out.User.Role != model.RoleVIP || out.User.Nickname != "OldUser" {
		t.Fatalf("角色和昵称应原样保留：%+v", out.User)
	}
	if storedHash(t, e, "OldUser") != legacyHash {
		t.Fatal("参数相同时不应重新哈希")
	}
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "olduser", Password: "wrong"}}).
		apiErr(apierr.InvalidCredentials)
}

// 旧哈希的参数与当前参数不同时，登录后自动用当前参数重新哈希。
func TestLegacyHashRehash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hash   string
		params auth.Params
		prefix string
	}{
		{"cffi 默认参数 p=4 → 当前参数", cffiDefaultHash, auth.DefaultParams, "$argon2id$v=19$m=65536,t=3,p=2$"},
		{"旧参数 → 调整后的参数", legacyHash, testParams, "$argon2id$v=19$m=1024,t=1,p=1$"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, withParams(tc.params))
			insertLegacyUser(t, e, "olduser", tc.hash)
			e.login("olduser", legacyPassword)
			h := storedHash(t, e, "olduser")
			if h == tc.hash || !strings.HasPrefix(h, tc.prefix) {
				t.Fatalf("应重新哈希为 %s...，实际 %s", tc.prefix, h)
			}
			e.login("olduser", legacyPassword) // 新哈希同样能登录
			if storedHash(t, e, "olduser") != h {
				t.Fatal("已是当前参数，不应再次重新哈希")
			}
			rows, _ := store.ListAudit(context.Background(), e.st.DB(), store.AuditLogin, 10)
			if len(rows) != 2 || !strings.Contains(rows[1].Detail, `"rehashed":true`) {
				t.Fatalf("审计应记录重新哈希：%+v", rows)
			}
		})
	}
}
