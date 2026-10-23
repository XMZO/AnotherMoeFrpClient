package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"moefrp/server/internal/admin"
	"moefrp/server/internal/auth"
	"moefrp/server/internal/config"
	"moefrp/server/internal/profile"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// testParams 是测试用的轻量 argon2 参数。旧哈希兼容用例单独使用 DefaultParams。
var testParams = auth.Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}

// noLimits 关掉限流，限流本身在专门的用例里测。
var noLimits = Limits{}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// syncBuffer 收集日志，供"日志里不能有令牌和密码"的检查使用。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type env struct {
	t     *testing.T
	cfg   config.Config
	st    *store.Store
	auth  *auth.Service
	admin *admin.Admin
	srv   *Server
	clock *fakeClock
	logs  *syncBuffer
}

type envOpt func(*envOptions)

type envOptions struct {
	cfg    config.Config
	params auth.Params
	limits Limits
}

func withConfig(f func(*config.Config)) envOpt { return func(o *envOptions) { f(&o.cfg) } }
func withParams(p auth.Params) envOpt          { return func(o *envOptions) { o.params = p } }
func withLimits(l Limits) envOpt               { return func(o *envOptions) { o.limits = l } }

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	o := envOptions{cfg: config.Default(), params: testParams, limits: noLimits}
	o.cfg.Client = config.Client{
		LatestVersion: "3.1.0",
		MinVersion:    "3.0.0",
		DownloadURL:   "https://example.com/MoeFrp-3.1.0-windows-amd64.exe",
		Announcement:  "测试公告",
		ImageSources:  []config.ImageSource{{URL: "https://img.example.com/random", Weight: 5}},
	}
	for _, f := range opts {
		f(&o)
	}
	o.cfg.DBPath = filepath.Join(t.TempDir(), "moefrp.db")

	st, err := store.Open(o.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	clock := &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := auth.New(st, auth.Options{
		SingleSession: o.cfg.SingleSession,
		SessionTTL:    o.cfg.SessionTTL.D(),
		Params:        o.params,
		Now:           clock.Now,
		Logger:        log,
	})
	if err != nil {
		t.Fatal(err)
	}
	limits := o.limits
	srv, err := New(Deps{
		Config:   o.cfg,
		Store:    st,
		Auth:     a,
		Profiles: profile.New(st, clock.Now),
		Logger:   log,
		Version:  "test",
		Limits:   &limits,
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, cfg: o.cfg, st: st, auth: a, admin: admin.New(st, a, clock.Now), srv: srv, clock: clock, logs: logs}
}

// req 描述一个请求。
type req struct {
	method string
	path   string
	body   any // string 原样发送，其余 JSON 编码
	token  string
	ip     string // RemoteAddr 的 IP，默认 192.0.2.1
	header map[string]string
}

type resp struct {
	*httptest.ResponseRecorder
	t *testing.T
}

func (e *env) do(r req) resp {
	e.t.Helper()
	var body io.Reader
	switch b := r.body.(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	case []byte:
		body = bytes.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		body = bytes.NewReader(buf)
	}
	hr := httptest.NewRequest(r.method, r.path, body)
	ip := r.ip
	if ip == "" {
		ip = "192.0.2.1"
	}
	if strings.Contains(ip, ":") && !strings.HasPrefix(ip, "[") {
		hr.RemoteAddr = "[" + ip + "]:40000"
	} else {
		hr.RemoteAddr = ip + ":40000"
	}
	hr.Header.Set(api.HeaderClientVersion, "3.0.0")
	hr.Header.Set(api.HeaderInstallID, "install-test")
	if r.token != "" {
		hr.Header.Set(api.HeaderAuthorization, api.BearerPrefix+r.token)
	}
	for k, v := range r.header {
		if v == "" {
			hr.Header.Del(k)
		} else {
			hr.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, hr)
	return resp{ResponseRecorder: rec, t: e.t}
}

func (r resp) decode(v any) {
	r.t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		r.t.Fatalf("响应不是合法 JSON：%v\n%s", err, r.Body.String())
	}
}

// expect 检查状态码，返回响应自身方便链式调用。
func (r resp) expect(status int) resp {
	r.t.Helper()
	if r.Code != status {
		r.t.Fatalf("状态码 %d，期望 %d：%s", r.Code, status, r.Body.String())
	}
	return r
}

// apiErr 检查状态码和错误码，返回解析出的错误。
func (r resp) apiErr(code apierr.Code) *apierr.Error {
	r.t.Helper()
	r.expect(code.Status())
	var er apierr.Response
	r.decode(&er)
	if er.Error == nil || er.Error.Code != code {
		r.t.Fatalf("错误码不对，期望 %s：%s", code, r.Body.String())
	}
	return er.Error
}

// fields 取出 validation_failed 的 details.fields。
func fields(e *apierr.Error) []map[string]any {
	raw, _ := json.Marshal(e.Details[apierr.DetailFields])
	var out []map[string]any
	json.Unmarshal(raw, &out)
	return out
}

func hasField(e *apierr.Error, field string) bool {
	for _, f := range fields(e) {
		if f["field"] == field {
			return true
		}
	}
	return false
}

// ---- 常用操作 ----

func (e *env) invite() string {
	e.t.Helper()
	codes, err := e.admin.CreateInvites(context.Background(), admin.Actor{}, 1, "test")
	if err != nil {
		e.t.Fatal(err)
	}
	return codes[0]
}

func (e *env) register(nick, pw string) api.UserInfo {
	e.t.Helper()
	var out api.RegisterResponse
	e.do(req{method: "POST", path: api.RouteRegister, body: api.RegisterRequest{Nickname: nick, Password: pw, InviteCode: e.invite()}}).
		expect(http.StatusCreated).decode(&out)
	return out.User
}

func (e *env) login(nick, pw string) string {
	e.t.Helper()
	var out api.LoginResponse
	e.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: nick, Password: pw, DeviceName: "测试机"}}).
		expect(http.StatusOK).decode(&out)
	return out.Token
}

// user 注册并登录一个用户，返回令牌。
func (e *env) user(nick string) string {
	e.t.Helper()
	e.register(nick, "password-"+nick)
	return e.login(nick, "password-"+nick)
}

func (e *env) me(token string) resp {
	return e.do(req{method: "GET", path: api.RouteMe, token: token})
}
