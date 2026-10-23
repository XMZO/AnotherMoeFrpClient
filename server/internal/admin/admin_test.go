package admin

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
	"moefrp/shared/apierr"
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
	a, err := auth.New(st, auth.Options{SingleSession: true, Params: auth.Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}})
	if err != nil {
		t.Fatal(err)
	}
	return New(st, a, nil), st, a
}

var root = Actor{UserID: "", IP: "192.0.2.9"}

func register(t *testing.T, ad *Admin, a *auth.Service, nick string) store.User {
	t.Helper()
	codes, err := ad.CreateInvites(context.Background(), root, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.Register(context.Background(), nick, "password-"+nick, codes[0], auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestInvites(t *testing.T) {
	ad, _, a := setup(t)
	ctx := context.Background()
	codes, err := ad.CreateInvites(ctx, root, 5, "给 测试")
	if err != nil || len(codes) != 5 {
		t.Fatalf("应生成 5 个：%v %v", codes, err)
	}
	for _, c := range codes {
		if err := validate.InviteCode(c); err != nil {
			t.Fatalf("生成的邀请码不合法：%s", c)
		}
	}
	if _, err := ad.CreateInvites(ctx, root, 0, ""); apierr.CodeOf(err) != apierr.ValidationFailed {
		t.Fatalf("数量 0 应报错：%v", err)
	}
	if _, err := a.Register(ctx, "alice", "password-alice", codes[0], auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := ad.RevokeInvite(ctx, root, codes[1]); err != nil {
		t.Fatal(err)
	}
	if err := ad.RevokeInvite(ctx, root, codes[0]); apierr.CodeOf(err) != apierr.InviteUsed {
		t.Fatalf("已使用的不能撤销：%v", err)
	}
	if err := ad.RevokeInvite(ctx, root, "FRPT-AAAA-AAAA"); apierr.CodeOf(err) != apierr.NotFound {
		t.Fatalf("不存在：%v", err)
	}
	count := func(status string) int {
		l, err := ad.ListInvites(ctx, status)
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}
	if count("all") != 5 || count("unused") != 3 || count("used") != 1 || count("revoked") != 1 {
		t.Fatalf("all=%d unused=%d used=%d revoked=%d", count("all"), count("unused"), count("used"), count("revoked"))
	}
	used, _ := ad.ListInvites(ctx, "used")
	if used[0].UsedBy != "alice" || used[0].Note != "给 测试" {
		t.Fatalf("used: %+v", used[0])
	}
	if _, err := ad.ListInvites(ctx, "weird"); apierr.CodeOf(err) != apierr.ValidationFailed {
		t.Fatal("非法 status 应报错")
	}
}

func TestUsersAndSelfProtection(t *testing.T) {
	ad, _, a := setup(t)
	ctx := context.Background()
	boss := register(t, ad, a, "boss")
	_ = register(t, ad, a, "alice")
	me := Actor{UserID: boss.ID, IP: "198.51.100.1"}
	if err := ad.SetRole(ctx, root, "boss", model.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	// 不能改自己、停用自己
	if err := ad.SetRole(ctx, me, "boss", model.RoleUser); apierr.CodeOf(err) != apierr.Forbidden {
		t.Fatalf("不能降低自己的角色：%v", err)
	}
	if _, err := ad.SetDisabled(ctx, me, "boss", true); apierr.CodeOf(err) != apierr.Forbidden {
		t.Fatalf("不能停用自己：%v", err)
	}
	if err := ad.SetRole(ctx, me, "alice", "god"); apierr.CodeOf(err) != apierr.ValidationFailed {
		t.Fatal("非法角色")
	}
	if err := ad.SetRole(ctx, me, "nobody", model.RoleVIP); apierr.CodeOf(err) != apierr.NotFound {
		t.Fatal("不存在的用户")
	}

	// alice 登录客户端和（成为管理员后）管理后台
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := ad.SetRole(ctx, me, "alice", model.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	web, err := a.LoginAdmin(ctx, "alice", "password-alice", auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// 降为 vip：管理后台会话立即失效，客户端会话保留
	if err := ad.SetRole(ctx, me, "alice", model.RoleVIP); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AuthenticateAdmin(ctx, web.Token); err == nil {
		t.Fatal("降级后管理后台会话应失效")
	}
	users, err := ad.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var alice User
	for _, u := range users {
		if u.Nickname == "alice" {
			alice = u
		}
	}
	if alice.Role != model.RoleVIP || alice.ActiveSessions != 1 || alice.LastSeen == nil {
		t.Fatalf("alice: %+v", alice)
	}

	// 踢下线只吊销客户端会话
	n, err := ad.Kick(ctx, me, "alice")
	if err != nil || n != 1 {
		t.Fatalf("kick: %d %v", n, err)
	}
	// 停用 → 不能登录；启用 → 恢复
	if _, err := ad.SetDisabled(ctx, me, "alice", true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); apierr.CodeOf(err) != apierr.AccountDisabled {
		t.Fatalf("停用后不能登录：%v", err)
	}
	if _, err := ad.SetDisabled(ctx, me, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "alice", "password-alice", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}

	// 重置码
	code, exp, err := ad.ResetCode(ctx, me, "alice")
	if err != nil || code == "" || exp.IsZero() {
		t.Fatalf("reset code: %v", err)
	}
	if err := a.ResetPassword(ctx, "alice", code, "new-password-1", auth.ClientMeta{}); err != nil {
		t.Fatal(err)
	}

	// 审计日志带操作者昵称和 IP
	logs, err := ad.ListAudit(ctx, "admin.", 50)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range logs {
		if e.Action == store.AuditAdminKick && e.Actor == "boss" && e.IP == "198.51.100.1" && e.Target == "alice" && e.Detail["via"] == "web" {
			found = true
		}
		if e.Action[:6] != "admin." {
			t.Fatalf("前缀过滤失效：%s", e.Action)
		}
	}
	if !found {
		t.Fatalf("没找到踢下线的审计记录：%+v", logs)
	}

	o, err := ad.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Users != 2 || o.Admins != 1 || o.VIPs != 1 || o.InvitesUsed != 2 || o.OnlineUsers != 0 || o.ActiveToday != 1 { // 重置密码吊销了 alice 的会话
		t.Fatalf("overview: %+v", o)
	}
}

func TestBackup(t *testing.T) {
	ad, _, a := setup(t)
	register(t, ad, a, "alice")
	var buf bytes.Buffer
	n, err := ad.Backup(context.Background(), root, &buf)
	if err != nil || n < 1024 || !bytes.HasPrefix(buf.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("backup: n=%d err=%v", n, err)
	}
}

func TestInviteNotReusableAfterUserDeleted(t *testing.T) {
	ad, st, a := setup(t)
	ctx := context.Background()
	codes, _ := ad.CreateInvites(ctx, root, 1, "")
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
	_, err = a.Register(ctx, "bob", "password-bob", codes[0], auth.ClientMeta{})
	if !errors.Is(err, apierr.New(apierr.InviteUsed)) {
		t.Fatalf("用户删除后邀请码也不能再用：%v", err)
	}
}
