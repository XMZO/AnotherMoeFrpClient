package httpapi

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"moefrp/server/internal/config"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// adminReq 发一个管理后台请求：带 CSRF 头和 cookie。
func (e *env) adminReq(method, path, cookie string, body any) resp {
	e.t.Helper()
	h := map[string]string{adminCSRFHeader: "1"}
	if cookie != "" {
		h["Cookie"] = adminCookie + "=" + cookie
	}
	return e.do(req{method: method, path: adminAPIPrefix + path, body: body, header: h})
}

// adminCookieFrom 取出响应里设置的管理后台 cookie。
func adminCookieFrom(t *testing.T, r resp) *http.Cookie {
	t.Helper()
	for _, c := range r.Result().Cookies() {
		if c.Name == adminCookie {
			return c
		}
	}
	t.Fatalf("响应里没有 %s cookie：%v", adminCookie, r.Header())
	return nil
}

// setupAdmin 用初始化码创建第一个管理员，返回 cookie 值。
func (e *env) setupAdmin(nick string) string {
	e.t.Helper()
	code := e.srv.SetupCode()
	if code == "" {
		e.t.Fatal("空库启动时应生成初始化码")
	}
	r := e.adminReq("POST", "/setup", "", adminSetupRequest{Code: strings.ToLower(code), Nickname: nick, Password: "password-" + nick}).expect(http.StatusOK)
	return adminCookieFrom(e.t, r).Value
}

func TestAdminSetupFlow(t *testing.T) {
	e := newEnv(t)
	code := e.srv.SetupCode()
	if !strings.Contains(e.logs.String(), code) {
		t.Fatal("初始化码应写进启动日志")
	}

	var st map[string]bool
	e.adminReq("GET", "/setup", "", nil).expect(http.StatusOK).decode(&st)
	if !st["required"] {
		t.Fatalf("空库应要求初始化：%v", st)
	}
	// 初始化码不对
	e.adminReq("POST", "/setup", "", adminSetupRequest{Code: "AAAA-BBBB-CCCC", Nickname: "boss", Password: "password-boss"}).apiErr(apierr.Forbidden)
	// 密码不合格
	e.adminReq("POST", "/setup", "", adminSetupRequest{Code: code, Nickname: "boss", Password: "short"}).apiErr(apierr.ValidationFailed)

	r := e.adminReq("POST", "/setup", "", adminSetupRequest{Code: code, Nickname: "boss", Password: "password-boss"}).expect(http.StatusOK)
	c := adminCookieFrom(t, r)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure {
		t.Fatalf("cookie 属性不对（HTTP 下不带 Secure）：%+v", c)
	}
	var me adminMe
	e.adminReq("GET", "/me", c.Value, nil).expect(http.StatusOK).decode(&me)
	if me.Nickname != "boss" {
		t.Fatalf("me: %+v", me)
	}

	// 用过一次就作废，也不再要求初始化
	if e.srv.SetupCode() != "" {
		t.Fatal("初始化码用过后应作废")
	}
	e.adminReq("GET", "/setup", "", nil).expect(http.StatusOK).decode(&st)
	if st["required"] {
		t.Fatal("已有管理员后不应再要求初始化")
	}
	e.adminReq("POST", "/setup", "", adminSetupRequest{Code: code, Nickname: "evil", Password: "password-evil"}).apiErr(apierr.Forbidden)

	// 管理员也能登录桌面客户端
	e.login("boss", "password-boss")
}

func TestAdminSetupClosedWhenUsersExist(t *testing.T) {
	e := newEnv(t)
	e.user("alice")
	// 重新创建 Server：库里已有用户，不生成初始化码
	srv, err := New(Deps{Config: e.cfg, Store: e.st, Auth: e.auth, Profiles: e.srv.profiles, Logger: e.srv.log, Limits: &noLimits, Now: e.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if srv.SetupCode() != "" {
		t.Fatal("已有用户时不应生成初始化码")
	}
}

func TestAdminRecoveryMode(t *testing.T) {
	e := newEnv(t, withConfig(func(c *config.Config) {
		c.AdminRecovery = true
		c.AdminSetupCode = "my-fixed-setup-code"
	}))
	e.user("alice")
	if strings.Contains(e.logs.String(), "my-fixed-setup-code") {
		t.Fatal("配置里给定的初始化码不应写进日志")
	}
	var st map[string]bool
	e.adminReq("GET", "/setup", "", nil).expect(http.StatusOK).decode(&st)
	if !st["required"] || !st["recovery"] {
		t.Fatalf("恢复模式下即使有用户也开放：%v", st)
	}
	cookie := e.setupAdmin("rescue")
	e.adminReq("GET", "/overview", cookie, nil).expect(http.StatusOK)
}

func TestAdminLoginAndGuards(t *testing.T) {
	e := newEnv(t)
	boss := e.setupAdmin("boss")
	e.user("alice")

	// 普通用户密码正确也进不了后台
	e.adminReq("POST", "/login", "", adminLoginRequest{Nickname: "alice", Password: "password-alice"}).apiErr(apierr.Forbidden)
	e.adminReq("POST", "/login", "", adminLoginRequest{Nickname: "boss", Password: "wrong-password"}).apiErr(apierr.InvalidCredentials)

	// 没有 CSRF 头：拒绝，即使 cookie 有效
	e.do(req{method: "GET", path: adminAPIPrefix + "/overview", header: map[string]string{"Cookie": adminCookie + "=" + boss}}).apiErr(apierr.Forbidden)
	e.do(req{method: "POST", path: adminAPIPrefix + "/login", body: adminLoginRequest{Nickname: "boss", Password: "password-boss"}}).apiErr(apierr.Forbidden)
	// 没有 cookie
	e.adminReq("GET", "/overview", "", nil).apiErr(apierr.Unauthorized)

	// 客户端令牌和后台 cookie 互不通用
	clientTok := e.login("boss", "password-boss")
	e.adminReq("GET", "/overview", clientTok, nil).apiErr(apierr.Unauthorized)
	e.me(boss).apiErr(apierr.Unauthorized)

	// 单设备模式下：登录后台不会把客户端挤下线，反之亦然
	r := e.adminReq("POST", "/login", "", adminLoginRequest{Nickname: "boss", Password: "password-boss"}).expect(http.StatusOK)
	boss2 := adminCookieFrom(t, r).Value
	e.me(clientTok).expect(http.StatusOK)
	e.adminReq("GET", "/me", boss, nil).apiErr(apierr.SessionReplaced) // 同类型的旧会话被替换
	e.login("boss", "password-boss")
	e.adminReq("GET", "/me", boss2, nil).expect(http.StatusOK)

	// 退出后 cookie 失效
	r = e.adminReq("POST", "/logout", boss2, nil).expect(http.StatusNoContent)
	if c := adminCookieFrom(t, r); c.MaxAge >= 0 {
		t.Fatalf("退出应清除 cookie：%+v", c)
	}
	e.adminReq("GET", "/me", boss2, nil).expect(http.StatusUnauthorized)
}

func TestAdminSecureCookieBehindProxy(t *testing.T) {
	e := newEnv(t, withConfig(func(c *config.Config) { c.TrustedProxies = []string{"10.0.0.0/8"} }))
	code := e.srv.SetupCode()
	r := e.do(req{method: "POST", path: adminAPIPrefix + "/setup", ip: "10.0.0.2",
		body:   adminSetupRequest{Code: code, Nickname: "boss", Password: "password-boss"},
		header: map[string]string{adminCSRFHeader: "1", "X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.5"}}).expect(http.StatusOK)
	if !adminCookieFrom(t, r).Secure {
		t.Fatal("经可信代理的 HTTPS 请求应设置 Secure cookie")
	}
}

func TestAdminInvitesAndUsers(t *testing.T) {
	e := newEnv(t)
	boss := e.setupAdmin("boss")

	var created struct{ Codes []string }
	e.adminReq("POST", "/invites", boss, createInvitesRequest{Count: 3, Note: "群友"}).expect(http.StatusCreated).decode(&created)
	if len(created.Codes) != 3 {
		t.Fatalf("codes: %v", created.Codes)
	}
	e.adminReq("POST", "/invites", boss, createInvitesRequest{Count: 500}).apiErr(apierr.ValidationFailed)
	e.adminReq("DELETE", "/invites/"+created.Codes[2], boss, nil).expect(http.StatusNoContent)

	// 用生成的邀请码注册
	e.do(req{method: "POST", path: api.RouteRegister, body: api.RegisterRequest{Nickname: "alice", Password: "password-alice", InviteCode: created.Codes[0]}}).expect(http.StatusCreated)
	aliceTok := e.login("alice", "password-alice")

	var inv struct{ Invites []map[string]any }
	e.adminReq("GET", "/invites?status=unused", boss, nil).expect(http.StatusOK).decode(&inv)
	if len(inv.Invites) != 1 || inv.Invites[0]["code"] != created.Codes[1] || inv.Invites[0]["note"] != "群友" {
		t.Fatalf("unused: %v", inv.Invites)
	}
	e.adminReq("GET", "/invites?status=used", boss, nil).expect(http.StatusOK).decode(&inv)
	if len(inv.Invites) != 1 || inv.Invites[0]["usedBy"] != "alice" {
		t.Fatalf("used: %v", inv.Invites)
	}

	var users struct {
		Users []map[string]any
		Me    string
	}
	e.adminReq("GET", "/users", boss, nil).expect(http.StatusOK).decode(&users)
	if len(users.Users) != 2 || users.Me != "boss" {
		t.Fatalf("users: %+v", users)
	}

	// 改角色、停用、启用
	adm := "admin"
	e.adminReq("PATCH", "/users/alice", boss, updateUserRequest{Role: &adm}).expect(http.StatusNoContent)
	vip := "vip"
	e.adminReq("PATCH", "/users/alice", boss, updateUserRequest{Role: &vip}).apiErr(apierr.ValidationFailed) // 已经没有 VIP 了
	yes, no := true, false
	e.adminReq("PATCH", "/users/alice", boss, updateUserRequest{Disabled: &yes}).expect(http.StatusNoContent)
	e.me(aliceTok).expect(http.StatusUnauthorized)
	e.adminReq("PATCH", "/users/alice", boss, updateUserRequest{Disabled: &no}).expect(http.StatusNoContent)
	aliceTok = e.login("alice", "password-alice")

	// 不能改自己
	user := "user"
	e.adminReq("PATCH", "/users/boss", boss, updateUserRequest{Role: &user}).apiErr(apierr.Forbidden)
	e.adminReq("PATCH", "/users/boss", boss, updateUserRequest{Disabled: &yes}).apiErr(apierr.Forbidden)
	e.adminReq("PATCH", "/users/nobody", boss, updateUserRequest{Role: &user}).apiErr(apierr.NotFound)

	// 踢下线
	var kicked map[string]int
	e.adminReq("POST", "/users/alice/kick", boss, nil).expect(http.StatusOK).decode(&kicked)
	if kicked["revokedSessions"] != 1 {
		t.Fatalf("kick: %v", kicked)
	}
	e.me(aliceTok).expect(http.StatusUnauthorized)

	// 重置码
	var rc map[string]string
	e.adminReq("POST", "/users/alice/reset-code", boss, nil).expect(http.StatusOK).decode(&rc)
	e.do(req{method: "POST", path: api.RouteResetPassword, body: api.ResetPasswordRequest{Nickname: "alice", Code: rc["code"], NewPassword: "new-password-1"}}).expect(http.StatusNoContent)
	e.login("alice", "new-password-1")

	// 把 alice 提成管理员后再降级：她的后台会话立即失效
	admin := "admin"
	e.adminReq("PATCH", "/users/alice", boss, updateUserRequest{Role: &admin}).expect(http.StatusNoContent)
	r := e.adminReq("POST", "/login", "", adminLoginRequest{Nickname: "alice", Password: "new-password-1"}).expect(http.StatusOK)
	aliceWeb := adminCookieFrom(t, r).Value
	e.adminReq("PATCH", "/users/alice", boss, updateUserRequest{Role: &user}).expect(http.StatusNoContent)
	e.adminReq("GET", "/me", aliceWeb, nil).expect(http.StatusUnauthorized)

	// 审计日志能按前缀过滤，带操作者
	var audit struct{ Entries []map[string]any }
	e.adminReq("GET", "/audit?action=admin.&limit=500", boss, nil).expect(http.StatusOK).decode(&audit)
	if len(audit.Entries) == 0 {
		t.Fatal("应有管理操作记录")
	}
	for _, en := range audit.Entries {
		if !strings.HasPrefix(en["action"].(string), "admin.") {
			t.Fatalf("前缀过滤失效：%v", en)
		}
	}

	// 概览
	var ov struct {
		Stats         map[string]int
		ServerVersion string
		Client        api.ClientConfigResponse
	}
	e.adminReq("GET", "/overview", boss, nil).expect(http.StatusOK).decode(&ov)
	if ov.Stats["users"] != 2 || ov.Stats["admins"] != 1 || ov.Client.LatestVersion != "3.1.0" || ov.ServerVersion != "test" {
		t.Fatalf("overview: %+v", ov)
	}
}

func TestAdminBackupDownload(t *testing.T) {
	e := newEnv(t)
	boss := e.setupAdmin("boss")
	r := e.adminReq("GET", "/backup", boss, nil).expect(http.StatusOK)
	if !bytes.HasPrefix(r.Body.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatal("备份应是 SQLite 文件")
	}
	if cd := r.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition: %q", cd)
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("备份不能被缓存")
	}
	// 没登录不能下载
	e.adminReq("GET", "/backup", "", nil).apiErr(apierr.Unauthorized)
}

func TestAdminUI(t *testing.T) {
	e := newEnv(t)
	r := e.do(req{method: "GET", path: "/admin"})
	if r.Code != http.StatusMovedPermanently || r.Header().Get("Location") != "/admin/" {
		t.Fatalf("/admin 应跳转到 /admin/：%d %v", r.Code, r.Header())
	}
	r = e.do(req{method: "GET", path: "/admin/"}).expect(http.StatusOK)
	if !strings.Contains(r.Body.String(), `<script src="app.js"`) {
		t.Fatal("首页内容不对")
	}
	csp := r.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP: %q", csp)
	}
	if r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("缺少 nosniff")
	}
	for _, f := range []string{"app.js", "app.css", "favicon.svg"} {
		e.do(req{method: "GET", path: "/admin/" + f}).expect(http.StatusOK)
	}
	if r := e.do(req{method: "GET", path: "/admin/../go.mod"}); r.Code == http.StatusOK {
		t.Fatal("不能读到 static 目录以外的文件")
	}
	e.do(req{method: "GET", path: "/admin/nope.js"}).apiErr(apierr.NotFound)
	e.do(req{method: "POST", path: "/admin/"}).apiErr(apierr.NotFound)
}
