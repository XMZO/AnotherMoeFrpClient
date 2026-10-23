package httpapi

// 管理后台（/admin）的接口：网页用 HttpOnly cookie 登录，只有 admin 角色可以进。
//
//   - cookie：moefrp_admin，HttpOnly、SameSite=Strict，HTTPS 下带 Secure，12 小时滑动过期；
//     会话类型是 admin_web，和桌面客户端的会话互不挤占（单设备模式只在同类型之间生效）。
//   - CSRF：所有 /api/v1/admin/ 请求都必须带 X-MoeFrp-Admin: 1。跨站请求带不了自定义头
//     （没有 CORS），再加上 SameSite=Strict，cookie 不会被别的网站利用。
//   - 初始化：库里还没有任何用户时，启动时生成一个初始化码（写进日志），在网页上用它创建第一个管理员；
//     admin_recovery = true 时即使已有用户也开放（管理员密码丢失时用）。

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"moefrp/server/internal/admin"
	"moefrp/server/internal/auth"
	"moefrp/server/internal/webadmin"
	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

const (
	adminCookie     = "moefrp_admin"
	adminCSRFHeader = "X-MoeFrp-Admin"
	adminAPIPrefix  = "/api/v1/admin"
)

// ---- 初始化码 ----

// initSetup 在启动时决定是否开放初始化页面。
func (s *Server) initSetup(ctx context.Context) error {
	var users int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		return err
	}
	if users > 0 && !s.cfg.AdminRecovery {
		return nil
	}
	code := s.cfg.AdminSetupCode
	if code == "" {
		code = strings.Join([]string{
			validate.RandomString(validate.InviteCharset, 4),
			validate.RandomString(validate.InviteCharset, 4),
			validate.RandomString(validate.InviteCharset, 4),
		}, "-")
		mode := "首次使用"
		if users > 0 {
			mode = "管理员恢复模式（admin_recovery = true，用完记得关掉）"
		}
		// 初始化码只在没有用户（或恢复模式）时有效，用一次就作废。打印出来是唯一的取用方式。
		s.log.Warn(fmt.Sprintf("%s：打开 https://<你的域名>/admin/ ，用初始化码 %s 创建管理员账号", mode, code))
	}
	s.setupMu.Lock()
	s.setupCode = code
	s.setupMu.Unlock()
	return nil
}

// SetupCode 返回当前有效的初始化码（测试用），没有时为空串。
func (s *Server) SetupCode() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupCode
}

func (s *Server) setupOpen(ctx context.Context) (bool, error) {
	if s.SetupCode() == "" {
		return false, nil
	}
	if s.cfg.AdminRecovery {
		return true, nil
	}
	var users int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		return false, err
	}
	return users == 0, nil
}

// ---- 中间件 ----

// adminCSRF：所有管理接口都要带 X-MoeFrp-Admin: 1。
func (s *Server) adminCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(adminCSRFHeader) != "1" {
			s.writeError(w, r, apierr.Newf(apierr.Forbidden, "缺少 %s 请求头", adminCSRFHeader))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin：cookie 里的会话必须是 admin_web，用户仍是管理员。
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminCookie)
		if err != nil || c.Value == "" {
			s.writeError(w, r, apierr.New(apierr.Unauthorized))
			return
		}
		p, err := s.auth.AuthenticateAdmin(r.Context(), c.Value)
		if err != nil {
			s.clearAdminCookie(w, r)
			s.writeError(w, r, err)
			return
		}
		info := infoFrom(r.Context())
		info.UserID = p.User.ID
		info.principal = &p
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if peer, ok := parseIP(r.RemoteAddr); ok && s.trusted(peer) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

func (s *Server) setAdminCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearAdminCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
}

func actorOf(r *http.Request) admin.Actor {
	return admin.Actor{UserID: principalFrom(r).User.ID, IP: infoFrom(r.Context()).IP}
}

// ---- 路由 ----

func (s *Server) adminRoutes(mux *http.ServeMux) {
	authed := func(h http.HandlerFunc) http.Handler {
		return chain(h, s.adminCSRF, s.requireAdmin, s.limit(s.limUser, byUser))
	}
	mux.Handle("GET "+adminAPIPrefix+"/setup", chain(http.HandlerFunc(s.handleAdminSetupState), s.adminCSRF))
	mux.Handle("POST "+adminAPIPrefix+"/setup", chain(http.HandlerFunc(s.handleAdminSetup), s.adminCSRF, s.limit(s.limReset, byIP)))
	mux.Handle("POST "+adminAPIPrefix+"/login", chain(http.HandlerFunc(s.handleAdminLogin), s.adminCSRF, s.limit(s.limLogin, byIP)))
	mux.Handle("POST "+adminAPIPrefix+"/logout", authed(s.handleAdminLogout))
	mux.Handle("GET "+adminAPIPrefix+"/me", authed(s.handleAdminMe))
	mux.Handle("GET "+adminAPIPrefix+"/overview", authed(s.handleAdminOverview))
	mux.Handle("GET "+adminAPIPrefix+"/invites", authed(s.handleAdminInvites))
	mux.Handle("POST "+adminAPIPrefix+"/invites", authed(s.handleAdminCreateInvites))
	mux.Handle("DELETE "+adminAPIPrefix+"/invites/{code}", authed(s.handleAdminRevokeInvite))
	mux.Handle("GET "+adminAPIPrefix+"/users", authed(s.handleAdminUsers))
	mux.Handle("PATCH "+adminAPIPrefix+"/users/{nickname}", authed(s.handleAdminUpdateUser))
	mux.Handle("POST "+adminAPIPrefix+"/users/{nickname}/kick", authed(s.handleAdminKick))
	mux.Handle("POST "+adminAPIPrefix+"/users/{nickname}/reset-code", authed(s.handleAdminResetCode))
	mux.Handle("GET "+adminAPIPrefix+"/audit", authed(s.handleAdminAudit))
	mux.Handle("GET "+adminAPIPrefix+"/backup", authed(s.handleAdminBackup))
}

// ---- 请求与响应 ----

type adminLoginRequest struct {
	Nickname string `json:"nickname"`
	Password string `json:"password"`
}

type adminSetupRequest struct {
	Code     string `json:"code"`
	Nickname string `json:"nickname"`
	Password string `json:"password"`
}

type adminMe struct {
	Nickname  string    `json:"nickname"`
	ExpiresAt time.Time `json:"expiresAt"`
	Version   string    `json:"version"`
}

type createInvitesRequest struct {
	Count int    `json:"count"`
	Note  string `json:"note"`
}

type updateUserRequest struct {
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

// ---- 处理函数 ----

// GET /api/v1/admin/setup
func (s *Server) handleAdminSetupState(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"required": open, "recovery": open && s.cfg.AdminRecovery})
}

// POST /api/v1/admin/setup：用初始化码创建管理员并直接登录。
func (s *Server) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	var req adminSetupRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	open, err := s.setupOpen(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	code := s.SetupCode()
	given := strings.ToUpper(strings.TrimSpace(req.Code))
	if !open || subtle.ConstantTimeCompare([]byte(given), []byte(strings.ToUpper(code))) != 1 {
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "初始化码不正确或已经初始化过了"))
		return
	}
	meta := auth.ClientMeta{IP: infoFrom(r.Context()).IP, DeviceName: "管理后台"}
	u, err := s.auth.CreateAdmin(r.Context(), req.Nickname, req.Password, !s.cfg.AdminRecovery, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.setupMu.Lock()
	s.setupCode = "" // 用一次就作废
	s.setupMu.Unlock()
	s.log.Info("admin created via setup page", "nickname", u.Nickname)
	res, err := s.auth.LoginAdmin(r.Context(), req.Nickname, req.Password, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.setAdminCookie(w, r, res.Token, time.Unix(res.Session.ExpiresAt, 0))
	writeJSON(w, http.StatusOK, adminMe{Nickname: res.User.Nickname, ExpiresAt: time.Unix(res.Session.ExpiresAt, 0).UTC(), Version: s.version})
}

// POST /api/v1/admin/login
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req adminLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.auth.LoginAdmin(r.Context(), req.Nickname, req.Password, auth.ClientMeta{IP: infoFrom(r.Context()).IP, DeviceName: "管理后台"})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	infoFrom(r.Context()).UserID = res.User.ID
	s.setAdminCookie(w, r, res.Token, time.Unix(res.Session.ExpiresAt, 0))
	writeJSON(w, http.StatusOK, adminMe{Nickname: res.User.Nickname, ExpiresAt: time.Unix(res.Session.ExpiresAt, 0).UTC(), Version: s.version})
}

// POST /api/v1/admin/logout
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), principalFrom(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.clearAdminCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/admin/me
func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	writeJSON(w, http.StatusOK, adminMe{Nickname: p.User.Nickname, ExpiresAt: time.Unix(p.Session.ExpiresAt, 0).UTC(), Version: s.version})
}

// GET /api/v1/admin/overview：统计数字 + 下发给客户端的版本信息（只读，改配置文件）。
func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.admin.Overview(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats":         o,
		"serverVersion": s.version,
		"client":        s.cfg.Client.ClientConfig(),
		"singleSession": s.cfg.SingleSession,
	})
}

// GET /api/v1/admin/invites?status=unused|used|revoked|all
func (s *Server) handleAdminInvites(w http.ResponseWriter, r *http.Request) {
	list, err := s.admin.ListInvites(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list})
}

// POST /api/v1/admin/invites
func (s *Server) handleAdminCreateInvites(w http.ResponseWriter, r *http.Request) {
	var req createInvitesRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	codes, err := s.admin.CreateInvites(r.Context(), actorOf(r), req.Count, req.Note)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"codes": codes})
}

// DELETE /api/v1/admin/invites/{code}
func (s *Server) handleAdminRevokeInvite(w http.ResponseWriter, r *http.Request) {
	if err := s.admin.RevokeInvite(r.Context(), actorOf(r), r.PathValue("code")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/admin/users
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.admin.ListUsers(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": list, "me": principalFrom(r).User.Nickname})
}

// PATCH /api/v1/admin/users/{nickname}：{role} 或 {disabled}
func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req updateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	nick, by := r.PathValue("nickname"), actorOf(r)
	if req.Role != nil {
		if err := s.admin.SetRole(r.Context(), by, nick, *req.Role); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	if req.Disabled != nil {
		if _, err := s.admin.SetDisabled(r.Context(), by, nick, *req.Disabled); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/admin/users/{nickname}/kick
func (s *Server) handleAdminKick(w http.ResponseWriter, r *http.Request) {
	n, err := s.admin.Kick(r.Context(), actorOf(r), r.PathValue("nickname"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revokedSessions": n})
}

// POST /api/v1/admin/users/{nickname}/reset-code：明文重置码只返回这一次。
func (s *Server) handleAdminResetCode(w http.ResponseWriter, r *http.Request) {
	code, exp, err := s.admin.ResetCode(r.Context(), actorOf(r), r.PathValue("nickname"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expiresAt": exp.UTC()})
}

// GET /api/v1/admin/audit?action=admin.&limit=200
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.admin.ListAudit(r.Context(), r.URL.Query().Get("action"), limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
}

// GET /api/v1/admin/backup：下载一份数据库备份（含密码哈希和节点 token，妥善保管）。
func (s *Server) handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	lw := &lazyHeaders{w: w, set: func(h http.Header) {
		h.Set("Content-Type", "application/vnd.sqlite3")
		h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="moefrp-%s.db"`, s.now().Format("20060102-150405")))
		h.Set("Cache-Control", "no-store")
	}}
	if _, err := s.admin.Backup(r.Context(), actorOf(r), lw); err != nil {
		if !lw.started {
			s.writeError(w, r, err)
			return
		}
		s.log.ErrorContext(r.Context(), "backup stream", "err", err)
	}
}

// lazyHeaders 在第一次写入时才设置下载用的响应头，出错时还能改回 JSON 错误响应。
type lazyHeaders struct {
	w       http.ResponseWriter
	set     func(http.Header)
	started bool
}

func (l *lazyHeaders) Write(p []byte) (int, error) {
	if !l.started {
		l.started = true
		l.set(l.w.Header())
	}
	return l.w.Write(p)
}

// ---- 页面 ----

// adminCSP：页面只加载自己的脚本和样式，不允许内联脚本，也不允许被嵌进别的页面。
const adminCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// adminUI 提供 /admin/ 下的静态页面。页面用 hash 路由（#/users），服务端只需要几个固定文件。
func (s *Server) adminUI() http.Handler {
	files := http.FileServerFS(webadmin.FS())
	return http.StripPrefix("/admin", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", adminCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		switch r.URL.Path {
		case "/", "/app.js", "/app.css", "/favicon.svg":
			files.ServeHTTP(w, r)
		default:
			s.writeError(w, r, apierr.New(apierr.NotFound))
		}
	}))
}
