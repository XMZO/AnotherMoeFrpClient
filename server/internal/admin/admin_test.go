package admin

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

func setup(t *testing.T) (*Admin, *store.Store, *auth.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(st, auth.Options{SingleSession: false, Params: auth.Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}})
	if err != nil {
		t.Fatal(err)
	}
	return New(st, a, nil), st, a
}

func register(t *testing.T, ad *Admin, a *auth.Service, nick string) {
	t.Helper()
	codes, err := ad.CreateInvites(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register(context.Background(), nick, "password-"+nick, codes[0], auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
}

func TestInvites(t *testing.T) {
	ad, st, _ := setup(t)
	ctx := context.Background()
	codes, err := ad.CreateInvites(ctx, 5, "给 测试")
	if err != nil || len(codes) != 5 {
		t.Fatalf("应生成 5 个：%v %v", codes, err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if err := validate.InviteCode(c); err != nil {
			t.Fatalf("生成的邀请码不合法：%s", c)
		}
		seen[c] = true
	}
	if len(seen) != 5 {
		t.Fatal("邀请码重复")
	}
	for _, n := range []int{0, -1, MaxInvitesPerCall + 1} {
		if _, err := ad.CreateInvites(ctx, n, ""); err == nil {
			t.Errorf("n=%d 应报错", n)
		}
	}

	if err := ad.RevokeInvite(ctx, " "+strings.ToLower(codes[0])+" "); err != nil {
		t.Fatal(err)
	}
	if err := ad.RevokeInvite(ctx, codes[0]); err == nil {
		t.Fatal("重复撤销应报错")
	}
	if err := ad.RevokeInvite(ctx, "FRPT-AAAA-AAAA"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("不存在的邀请码应报错：%v", err)
	}

	all, _ := ad.ListInvites(ctx, false)
	unused, _ := ad.ListInvites(ctx, true)
	if len(all) != 5 || len(unused) != 4 || all[0].Note != "给 测试" {
		t.Fatalf("列表不对：all=%d unused=%d", len(all), len(unused))
	}
	audit, _ := store.ListAudit(ctx, st.DB(), "", 10)
	if len(audit) != 2 || audit[0].Action != store.AuditAdminInviteRevoke || audit[1].Action != store.AuditAdminInviteCreate ||
		!strings.Contains(audit[1].Detail, `"via":"cli"`) {
		t.Fatalf("审计不对：%+v", audit)
	}
}

func TestUsers(t *testing.T) {
	ad, st, a := setup(t)
	ctx := context.Background()
	register(t, ad, a, "alice")
	register(t, ad, a, "bob")
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}

	users, err := ad.ListUsers(ctx)
	if err != nil || len(users) != 2 || users[0].Nickname != "alice" || users[0].ActiveSessions != 2 || users[0].LastSeen == 0 ||
		users[1].ActiveSessions != 0 || users[1].LastSeen != 0 {
		t.Fatalf("用户列表不对：%+v %v", users, err)
	}

	if err := ad.SetRole(ctx, "ALICE", model.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := ad.SetRole(ctx, "alice", "root"); err == nil {
		t.Fatal("非法角色应报错")
	}
	if err := ad.SetRole(ctx, "nobody", model.RoleVIP); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("不存在的用户：%v", err)
	}
	u, _ := store.GetUserByNickname(ctx, st.DB(), "alice")
	if u.Role != model.RoleAdmin || u.StateVersion != 1 {
		t.Fatalf("改角色后：%+v", u)
	}

	n, err := ad.Kick(ctx, "alice")
	if err != nil || n != 2 {
		t.Fatalf("应吊销 2 个会话：%d %v", n, err)
	}
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	n, err = ad.Disable(ctx, "alice")
	if err != nil || n != 1 {
		t.Fatalf("停用应吊销 1 个会话：%d %v", n, err)
	}
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err == nil {
		t.Fatal("停用后不能登录")
	}
	if err := ad.Enable(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err != nil {
		t.Fatalf("启用后应能登录：%v", err)
	}

	code, exp, err := ad.ResetCode(ctx, "bob")
	if err != nil || !strings.HasPrefix(code, "RST-") || time.Until(exp) < 23*time.Hour {
		t.Fatalf("重置码不对：%s %v %v", code, exp, err)
	}
	if err := a.ResetPassword(ctx, "bob", code, "brand-new-password", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ad.ResetCode(ctx, "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("不存在的用户：%v", err)
	}

	// 每个管理操作都有审计
	want := map[string]bool{
		store.AuditAdminSetRole: false, store.AuditAdminKick: false, store.AuditAdminDisable: false,
		store.AuditAdminEnable: false, store.AuditAdminResetCode: false,
	}
	rows, _ := store.ListAudit(ctx, st.DB(), "", 100)
	for _, r := range rows {
		if _, ok := want[r.Action]; ok {
			want[r.Action] = true
			if r.Target == "" || !strings.Contains(r.Detail, `"via":"cli"`) {
				t.Errorf("审计记录不完整：%+v", r)
			}
			if strings.Contains(r.Detail, code) {
				t.Fatal("重置码不能写进审计")
			}
		}
	}
	for action, ok := range want {
		if !ok {
			t.Errorf("缺少审计 %s", action)
		}
	}
}

// 已使用的邀请码在用户被删除后（used_by 变成 NULL）也不能再用。
func TestInviteNotReusableAfterUserDeleted(t *testing.T) {
	ad, st, a := setup(t)
	ctx := context.Background()
	codes, _ := ad.CreateInvites(ctx, 1, "")
	u, err := a.Register(ctx, "alice", "password-alice", codes[0], auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, u.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	inv, _ := store.GetInvite(ctx, st.DB(), codes[0])
	if inv.UsedBy != "" || inv.UsedAt == nil {
		t.Fatalf("前提：used_by 已被置空、used_at 仍在：%+v", inv)
	}
	if _, err := a.Register(ctx, "bob", "password-bob", codes[0], auth.ClientMeta{}); err == nil {
		t.Fatal("邀请码不应能再次使用")
	}
}
