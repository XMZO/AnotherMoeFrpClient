package httpapi

import (
	"bufio"
	"encoding/json"
	"moefrp/server/internal/admin"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"moefrp/server/internal/config"
	"moefrp/server/internal/ratelimit"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	var h api.HealthResponse
	e.do(req{method: "GET", path: api.RouteHealthz}).expect(http.StatusOK).decode(&h)
	if h.Status != "ok" || h.Version != "test" {
		t.Fatalf("healthz 不对：%+v", h)
	}
	e.st.Close()
	e.do(req{method: "GET", path: api.RouteHealthz}).expect(http.StatusServiceUnavailable)
}

func TestClientConfig(t *testing.T) {
	e := newEnv(t)
	var c api.ClientConfigResponse
	// 版本过低的客户端也要能拿到 client/config，否则它不知道去哪里下载新版
	e.do(req{method: "GET", path: api.RouteClientConfig, header: map[string]string{api.HeaderClientVersion: "1.0.0"}}).
		expect(http.StatusOK).decode(&c)
	if c.LatestVersion != "3.1.0" || c.MinVersion != "3.0.0" || c.DownloadURL == "" || c.Announcement != "测试公告" ||
		len(c.ImageSources) != 1 || c.ImageSources[0].Weight != 5 {
		t.Fatalf("client/config 不对：%+v", c)
	}
}

func TestClientVersionCheck(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	for _, v := range []string{"2.9.9", "3.0.0-beta.1", "garbage"} {
		er := e.do(req{method: "GET", path: api.RouteMe, token: tok, header: map[string]string{api.HeaderClientVersion: v}}).
			apiErr(apierr.ClientOutdated)
		if er.Details[apierr.DetailDownloadURL] != e.cfg.Client.DownloadURL || er.Details[apierr.DetailLatestVersion] != "3.1.0" ||
			er.Details[apierr.DetailMinVersion] != "3.0.0" {
			t.Fatalf("%s：426 应带上 latestVersion、minVersion、downloadUrl：%+v", v, er.Details)
		}
	}
	// 登录同样检查：过低的客户端不该拿到令牌
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "alice", Password: "password-alice"},
		header: map[string]string{api.HeaderClientVersion: "2.0.0"}}).apiErr(apierr.ClientOutdated)
	for _, v := range []string{"3.0.0", "v3.0.1", "3.1.0", "4.0.0"} {
		e.do(req{method: "GET", path: api.RouteMe, token: tok, header: map[string]string{api.HeaderClientVersion: v}}).expect(http.StatusOK)
	}
	// 不带版本头的请求（curl、监控）不检查
	e.do(req{method: "GET", path: api.RouteMe, token: tok, header: map[string]string{api.HeaderClientVersion: ""}}).expect(http.StatusOK)

	// min_version 为空时完全不检查
	e2 := newEnv(t, withConfig(func(c *config.Config) { c.Client.MinVersion = "" }))
	e2.do(req{method: "GET", path: api.RouteClientConfig, header: map[string]string{api.HeaderClientVersion: "0.0.1"}}).expect(http.StatusOK)
	e2.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: "x", Password: "y"},
		header: map[string]string{api.HeaderClientVersion: "0.0.1"}}).apiErr(apierr.InvalidCredentials)
}

// 旧客户端：get_challenge 返回 200 和假挑战码，其余旧路径返回 426 和下载地址（PLAN 7.12）。
func TestLegacyUpgradePrompt(t *testing.T) {
	e := newEnv(t)
	r := e.do(req{method: "POST", path: "/api/login/get_challenge", body: `{"nickname":"alice"}`}).expect(http.StatusOK)
	var ch api.LegacyChallengeResponse
	r.decode(&ch)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(ch.Challenge) {
		t.Fatalf("挑战码应是 64 位十六进制：%s", r.Body.String())
	}
	want := "请下载新版客户端：" + e.cfg.Client.DownloadURL
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/login"},
		{"POST", "/api/register"},
		{"POST", "/api/session/check"},
		{"GET", "/api/configs"},
		{"POST", "/api/share/use"},
		{"GET", "/api/login/get_challenge"},
	} {
		r := e.do(req{method: c.method, path: c.path, body: `{}`, header: map[string]string{api.HeaderClientVersion: ""}}).expect(http.StatusUpgradeRequired)
		var le api.LegacyErrorResponse
		r.decode(&le)
		if le.Error != want {
			t.Fatalf("%s %s：error 应为 %q：%s", c.method, c.path, want, r.Body.String())
		}
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	e.do(req{method: "GET", path: "/api/v1/nope", token: tok}).apiErr(apierr.NotFound)
	e.do(req{method: "GET", path: "/nope"}).apiErr(apierr.NotFound)
	e.do(req{method: "PUT", path: api.RouteShares, token: tok}).apiErr(apierr.MethodNotAllowed)
	r := e.do(req{method: "GET", path: api.RouteLogin})
	r.apiErr(apierr.MethodNotAllowed)
	if r.Header().Get("Allow") != "POST" {
		t.Fatalf("Allow 头应为 POST：%v", r.Header())
	}
	e.do(req{method: "PATCH", path: api.ProfilePath("x"), token: tok}).apiErr(apierr.MethodNotAllowed)
}

func TestBodyLimit(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	big := `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: big}).apiErr(apierr.PayloadTooLarge)

	// 没有 Content-Length（分块传输）时由 MaxBytesReader 兜住
	hr := httptest.NewRequest("POST", api.RouteProfiles, strings.NewReader(big))
	hr.ContentLength = -1
	hr.Header.Set(api.HeaderAuthorization, api.BearerPrefix+tok)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, hr)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("分块上传超限应返回 413：%d %s", rec.Code, rec.Body.String())
	}
}

func TestRateLimits(t *testing.T) {
	e := newEnv(t, withLimits(DefaultLimits))
	login := func(ip string) resp {
		return e.do(req{method: "POST", path: api.RouteLogin, ip: ip, body: api.LoginRequest{Nickname: "nobody", Password: "x"}})
	}
	// 登录：每个 IP 每分钟 10 次（这里用不存在的昵称，失败锁定会更早触发，所以每次换昵称）
	for i := 0; i < 10; i++ {
		e.do(req{method: "POST", path: api.RouteLogin, ip: "198.51.100.1",
			body: api.LoginRequest{Nickname: "n" + string(rune('a'+i)) + "xx", Password: "x"}}).apiErr(apierr.InvalidCredentials)
	}
	r := login("198.51.100.1")
	er := r.apiErr(apierr.RateLimited)
	if er.Details[apierr.DetailRetryAfter] != float64(6) || r.Header().Get("Retry-After") != "6" {
		t.Fatalf("应等 6 秒：%+v %s", er.Details, r.Header().Get("Retry-After"))
	}
	login("198.51.100.2").apiErr(apierr.InvalidCredentials) // 不同 IP 不受影响
	e.clock.Add(6 * time.Second)
	e.do(req{method: "POST", path: api.RouteLogin, ip: "198.51.100.1", body: api.LoginRequest{Nickname: "zzz", Password: "x"}}).
		apiErr(apierr.InvalidCredentials)

	// 注册：每个 IP 每小时 5 次
	for i := 0; i < 5; i++ {
		e.do(req{method: "POST", path: api.RouteRegister, ip: "198.51.100.3", body: api.RegisterRequest{Nickname: "ab"}}).apiErr(apierr.ValidationFailed)
	}
	e.do(req{method: "POST", path: api.RouteRegister, ip: "198.51.100.3", body: api.RegisterRequest{Nickname: "ab"}}).apiErr(apierr.RateLimited)

	// 重置密码：每个 IP 每 10 分钟 5 次
	for i := 0; i < 5; i++ {
		e.do(req{method: "POST", path: api.RouteResetPassword, ip: "198.51.100.4", body: api.ResetPasswordRequest{}}).apiErr(apierr.ValidationFailed)
	}
	er = e.do(req{method: "POST", path: api.RouteResetPassword, ip: "198.51.100.4", body: api.ResetPasswordRequest{}}).apiErr(apierr.RateLimited)
	if er.Details[apierr.DetailRetryAfter] != float64(120) {
		t.Fatalf("应等 120 秒：%+v", er.Details)
	}
}

// IPv6 按 /64 限流：同一个 /64 里换地址不能绕过。
func TestRateLimitIPv6Prefix(t *testing.T) {
	e := newEnv(t, withLimits(Limits{Login: ratelimit.Rule{Limit: 2, Window: time.Minute}}))
	try := func(ip, nick string) resp {
		return e.do(req{method: "POST", path: api.RouteLogin, ip: ip, body: api.LoginRequest{Nickname: nick, Password: "x"}})
	}
	try("2001:db8:1:2::1", "u1").apiErr(apierr.InvalidCredentials)
	try("2001:db8:1:2::2", "u2").apiErr(apierr.InvalidCredentials)
	try("2001:db8:1:2:ffff::9", "u3").apiErr(apierr.RateLimited)
	try("2001:db8:1:3::1", "u4").apiErr(apierr.InvalidCredentials) // 另一个 /64
	if ipKey("198.51.100.1") != "198.51.100.1" || ipKey("2001:db8:1:2::1") != "2001:db8:1:2::/64" {
		t.Fatal("ipKey 不对")
	}
}

func TestRateLimitPerUser(t *testing.T) {
	e := newEnv(t, withLimits(Limits{Authenticated: ratelimit.Rule{Limit: 3, Window: time.Minute}}))
	alice := e.user("alice")
	bob := e.user("bob")
	for i := 0; i < 3; i++ {
		e.me(alice).expect(http.StatusOK)
	}
	e.me(alice).apiErr(apierr.RateLimited)
	// 按用户计数：同一个 IP 上的另一个用户不受影响
	e.me(bob).expect(http.StatusOK)
	// 默认规则是每个用户每分钟 120 次
	if DefaultLimits.Authenticated != (ratelimit.Rule{Limit: 120, Window: time.Minute}) {
		t.Fatal("默认规则与 PLAN 7.8 不一致")
	}
}

// 只有 trusted_proxies 里的地址发来的 X-Forwarded-For 才被信任（PLAN 7.8）。
func TestClientIP(t *testing.T) {
	e := newEnv(t, withConfig(func(c *config.Config) {
		c.TrustedProxies = []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8"}
	}))
	cases := []struct {
		remote, xff, want string
	}{
		{"198.51.100.7:1234", "", "198.51.100.7"},
		{"198.51.100.7:1234", "1.2.3.4", "198.51.100.7"}, // 不可信的对端：忽略伪造的 XFF
		{"127.0.0.1:1234", "203.0.113.5", "203.0.113.5"},
		{"127.0.0.1:1234", "6.6.6.6, 203.0.113.5", "203.0.113.5"},  // 最左边的是客户端自己填的，不可信
		{"127.0.0.1:1234", "203.0.113.5, 10.1.2.3", "203.0.113.5"}, // 跳过多层可信代理
		{"127.0.0.1:1234", "10.1.2.3, 10.2.3.4", "10.1.2.3"},       // 全是可信代理时取最左边
		{"127.0.0.1:1234", "garbage, 203.0.113.5", "203.0.113.5"},
		{"127.0.0.1:1234", "203.0.113.5, garbage", "127.0.0.1"}, // 最近一跳就不合法：用对端地址
		{"[::1]:1234", "2001:db8::1", "2001:db8::1"},
		{"[::ffff:127.0.0.1]:1234", "203.0.113.9", "203.0.113.9"}, // IPv4 映射地址
	}
	for _, c := range cases {
		hr := httptest.NewRequest("GET", "/healthz", nil)
		hr.RemoteAddr = c.remote
		if c.xff != "" {
			hr.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := e.srv.clientIP(hr); got != c.want {
			t.Errorf("remote=%s xff=%q：得到 %s，期望 %s", c.remote, c.xff, got, c.want)
		}
	}

	// 端到端：审计里记录的是真实 IP
	e.register("alice", "correct-horse")
	e.do(req{method: "POST", path: api.RouteLogin, ip: "127.0.0.1", header: map[string]string{"X-Forwarded-For": "203.0.113.77"},
		body: api.LoginRequest{Nickname: "alice", Password: "correct-horse"}}).expect(http.StatusOK)
	if !strings.Contains(e.logs.String(), `"ip":"203.0.113.77"`) {
		t.Fatal("访问日志应记录 XFF 里的真实 IP")
	}
}

func TestPanicRecovery(t *testing.T) {
	e := newEnv(t)
	h := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }),
		e.srv.requestID, e.srv.realIP, e.srv.recoverer, e.srv.bodyLimit, e.srv.accessLog)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Fatalf("panic 应返回 500 internal：%d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatal("不能把 panic 内容透给客户端")
	}
	logs := e.logs.String()
	if !strings.Contains(logs, `"msg":"panic"`) || !strings.Contains(logs, `"status":500`) {
		t.Fatalf("应记录 panic 和 500 的访问日志：%s", logs)
	}
}

// 访问日志：方法、路径、状态码、耗时、用户 ID、IP；任何时候都不记录令牌和密码（PLAN 7.4、7.9）。
func TestAccessLogNoSecrets(t *testing.T) {
	e := newEnv(t)
	u := e.register("alice", "Sup3r-Secret-PW")
	tok := e.login("alice", "Sup3r-Secret-PW")
	e.me(tok).expect(http.StatusOK)
	e.do(req{method: "POST", path: api.RouteChangePassword, token: tok,
		body: api.ChangePasswordRequest{OldPassword: "Sup3r-Secret-PW", NewPassword: "An0ther-Secret-PW"}}).expect(http.StatusNoContent)
	code, _, _ := e.admin.ResetCode(t.Context(), admin.Actor{}, "alice")
	e.do(req{method: "POST", path: api.RouteResetPassword,
		body: api.ResetPasswordRequest{Nickname: "alice", Code: code, NewPassword: "Th1rd-Secret-PW"}}).expect(http.StatusNoContent)
	e.do(req{method: "GET", path: api.RouteMe, token: tok + "x"}).apiErr(apierr.Unauthorized)

	logs := e.logs.String()
	for _, secret := range []string{tok, tok[4:], "Sup3r-Secret-PW", "An0ther-Secret-PW", "Th1rd-Secret-PW", code, "Bearer"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("日志里出现了机密 %q", secret)
		}
	}
	assertNoSecretInDB(t, e, tok, "Sup3r-Secret-PW", "An0ther-Secret-PW", "Th1rd-Secret-PW", code)

	// 访问日志字段齐全，me 请求带上了用户 ID
	found := false
	sc := bufio.NewScanner(strings.NewReader(logs))
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) != nil || line["msg"] != "http" {
			continue
		}
		for _, k := range []string{"method", "path", "status", "duration_ms", "user_id", "ip", "request_id"} {
			if _, ok := line[k]; !ok {
				t.Fatalf("访问日志缺少 %s：%s", k, sc.Text())
			}
		}
		if line["path"] == api.RouteMe && line["user_id"] == u.ID && line["status"] == float64(200) {
			found = true
		}
	}
	if !found {
		t.Fatal("没有找到带用户 ID 的 /me 访问日志")
	}
}

func TestRequestIDHeader(t *testing.T) {
	e := newEnv(t)
	a := e.do(req{method: "GET", path: api.RouteHealthz}).Header().Get(api.HeaderRequestID)
	b := e.do(req{method: "GET", path: api.RouteHealthz}).Header().Get(api.HeaderRequestID)
	if len(a) != 16 || a == b {
		t.Fatalf("每个请求应有不同的 X-Request-Id：%q %q", a, b)
	}
}

func TestErrorResponseShape(t *testing.T) {
	e := newEnv(t)
	r := e.do(req{method: "GET", path: api.RouteMe})
	if ct := r.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %s", ct)
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("API 响应应禁止缓存")
	}
	var raw map[string]map[string]any
	r.decode(&raw)
	if raw["error"]["code"] != "unauthorized" || raw["error"]["message"] == "" {
		t.Fatalf("错误格式应为 {error:{code,message}}：%s", r.Body.String())
	}
}
