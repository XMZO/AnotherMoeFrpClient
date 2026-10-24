package httpapi

// 客户端实时通道和后台"客户端"页（docs/client-live.md）：
//   - GET  /api/v1/client/live：客户端的 WebSocket；
//   - 面板这边的变化（配置、官方节点、密钥、客户端版本）马上推给客户端；
//   - 后台：在线设备、推送通知、远程停止 / 重启隧道、让设备下线、客户端版本和公告（改了立即推送）。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/coder/websocket"

	"moefrp/server/internal/config"
	"moefrp/server/internal/live"
	"moefrp/server/internal/nodes"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

// settingClientConfig 是后台改过的客户端配置（JSON）。没有时用配置文件里的。
const settingClientConfig = "client_config"

// clientSettings 是后台可以改的客户端配置。
type clientSettings struct {
	LatestVersion string `json:"latestVersion"`
	MinVersion    string `json:"minVersion"`
	DownloadURL   string `json:"downloadUrl"`
	SHA256        string `json:"sha256"`
	ReleaseNotes  string `json:"releaseNotes"`
	Announcement  string `json:"announcement"`
}

// clientConfigState 缓存当前生效的客户端配置。
type clientConfigState struct {
	mu       sync.RWMutex
	override *clientSettings // nil 表示用配置文件
	minVer   *api.Version
}

func (s *Server) initLive(ctx context.Context) error {
	s.live = live.New(s.log, s.now)
	s.nodes.SetHooks(nodesHooks(s))
	v, ok, err := store.GetSetting(ctx, s.st.DB(), settingClientConfig)
	if err != nil {
		return err
	}
	var o *clientSettings
	if ok && v != "" {
		o = &clientSettings{}
		if err := json.Unmarshal([]byte(v), o); err != nil {
			s.log.Warn("bad client_config setting, using config file", "err", err)
			o = nil
		}
	}
	return s.setClientOverride(o)
}

func nodesHooks(s *Server) nodes.Hooks {
	return nodes.Hooks{
		OfficialChanged: func() { s.live.Broadcast(api.LiveMessage{Type: api.LiveOfficial}) },
		TicketsInvalid: func(userID string) {
			m := api.LiveMessage{Type: api.LiveTickets}
			if userID == "" {
				s.live.Broadcast(m)
			} else {
				s.live.ToUser(userID, "", m)
			}
		},
	}
}

// setClientOverride 换上后台的客户端配置（nil 表示回到配置文件）。
func (s *Server) setClientOverride(o *clientSettings) error {
	c := s.cfg.Client
	if o != nil {
		c.LatestVersion, c.MinVersion, c.DownloadURL = o.LatestVersion, o.MinVersion, o.DownloadURL
		c.SHA256, c.ReleaseNotes, c.Announcement = o.SHA256, o.ReleaseNotes, o.Announcement
	}
	var mv *api.Version
	if c.MinVersion != "" {
		v, err := api.ParseVersion(c.MinVersion)
		if err != nil {
			return err
		}
		mv = &v
	}
	s.clientCfgState.mu.Lock()
	s.clientCfgState.override = o
	s.clientCfgState.minVer = mv
	s.clientCfgState.mu.Unlock()
	return nil
}

// clientCfg 是当前生效的客户端配置。
func (s *Server) clientCfg() config.Client {
	s.clientCfgState.mu.RLock()
	o := s.clientCfgState.override
	s.clientCfgState.mu.RUnlock()
	c := s.cfg.Client
	if o != nil {
		c.LatestVersion, c.MinVersion, c.DownloadURL = o.LatestVersion, o.MinVersion, o.DownloadURL
		c.SHA256, c.ReleaseNotes, c.Announcement = o.SHA256, o.ReleaseNotes, o.Announcement
	}
	return c
}

func (s *Server) clientMinVersion() *api.Version {
	s.clientCfgState.mu.RLock()
	defer s.clientCfgState.mu.RUnlock()
	return s.clientCfgState.minVer
}

// ---- 客户端 ----

// GET /api/v1/client/live：实时通道。
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept 已经写了错误响应
	}
	c := &live.Client{
		SessionID: p.Session.ID, UserID: p.User.ID, Nickname: p.User.Nickname, Role: p.User.Role,
		DeviceID: p.Session.DeviceID, IP: s.clientIP(r),
	}
	// 连接活得比请求久：不能用请求的 context（访问日志等中间件在请求返回后就结束了）
	s.live.Serve(context.WithoutCancel(r.Context()), ws, c)
}

// liveSyncAfter：请求成功后告诉这个用户的其他设备马上同步（云端配置、订阅）。
func (s *Server) liveSyncAfter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status < 300 {
			p := principalFrom(r)
			s.live.ToUser(p.User.ID, p.Session.ID, api.LiveMessage{Type: api.LiveSync})
		}
	})
}

// liveSubscribersAfter：分享被修改、重新发布、撤销后，订阅了它的用户马上同步。
func (s *Server) liveSubscribersAfter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 300 {
			return
		}
		ids, err := store.ListShareSubscriberIDs(r.Context(), s.st.DB(), r.PathValue("id"))
		if err != nil {
			return
		}
		for _, id := range ids {
			s.live.ToUser(id, "", api.LiveMessage{Type: api.LiveSync})
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// sessionsChanged 在会话被吊销、用户停用或改角色后调用：官方节点的授权表重算，
// 实时通道里已经无效的会话马上收到原因并断开（客户端按被动下线处理）。
func (s *Server) sessionsChanged() {
	s.nodes.AccessChanged()
	go s.live.Recheck(context.Background(), func(ctx context.Context, sessionID string) *api.LiveEndedBody {
		err := s.auth.CheckClientSession(ctx, sessionID)
		if err == nil {
			return nil
		}
		var ae *apierr.Error
		if errors.As(err, &ae) {
			return &api.LiveEndedBody{Code: string(ae.Code), Message: ae.Message}
		}
		return &api.LiveEndedBody{Code: string(apierr.Unauthorized), Message: err.Error()}
	})
}

// ---- 管理后台 ----

func (s *Server) adminLiveRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	p := adminAPIPrefix
	mux.Handle("GET "+p+"/clients", authed(s.handleAdminClients))
	mux.Handle("POST "+p+"/clients/notice", authed(s.handleAdminClientNotice))
	mux.Handle("POST "+p+"/clients/tunnel", authed(s.handleAdminClientTunnel))
	mux.Handle("POST "+p+"/clients/kick", authed(s.handleAdminClientKick))
	mux.Handle("GET "+p+"/client-config", authed(s.handleAdminClientConfig))
	mux.Handle("PUT "+p+"/client-config", authed(s.handleAdminSetClientConfig))
	mux.Handle("DELETE "+p+"/client-config", authed(s.handleAdminResetClientConfig))
}

// GET /api/v1/admin/clients：在线的客户端。
func (s *Server) handleAdminClients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"clients": s.live.List(), "latestVersion": s.clientCfg().LatestVersion})
}

type clientNoticeRequest struct {
	Target live.Target        `json:"target"`
	Notice api.LiveNoticeBody `json:"notice"`
}

const (
	maxNoticeTitle   = 80
	maxNoticeContent = 2000
	maxNoticeLink    = 500
	maxReason        = 200
)

// POST /api/v1/admin/clients/notice：给选中的客户端推送通知。
func (s *Server) handleAdminClientNotice(w http.ResponseWriter, r *http.Request) {
	var req clientNoticeRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	n := &req.Notice
	n.Title, n.Content = strings.TrimSpace(n.Title), strings.TrimSpace(n.Content)
	n.LinkText, n.LinkURL = strings.TrimSpace(n.LinkText), strings.TrimSpace(n.LinkURL)
	var errs validate.Errors
	switch {
	case n.Title == "":
		addErr(&errs, "title", validate.ReasonRequired, "标题不能为空")
	case utf8.RuneCountInString(n.Title) > maxNoticeTitle:
		addErr(&errs, "title", validate.ReasonInvalid, "标题最多 80 个字")
	}
	if utf8.RuneCountInString(n.Content) > maxNoticeContent {
		addErr(&errs, "content", validate.ReasonInvalid, "内容最多 2000 个字")
	}
	if n.Level == "" {
		n.Level = api.NoticeInfo
	}
	if !oneOf(n.Level, api.NoticeInfo, api.NoticeSuccess, api.NoticeWarning, api.NoticeError) {
		addErr(&errs, "level", validate.ReasonInvalid, "级别只能是 info / success / warning / error")
	}
	if n.Display == "" {
		n.Display = api.NoticeToast
	}
	if !oneOf(n.Display, api.NoticeToast, api.NoticeDialog) {
		addErr(&errs, "display", validate.ReasonInvalid, "显示方式只能是 toast / dialog")
	}
	if n.LinkURL != "" {
		u, err := url.Parse(n.LinkURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(n.LinkURL) > maxNoticeLink {
			addErr(&errs, "linkUrl", validate.ReasonInvalid, "链接要以 http:// 或 https:// 开头")
		}
		if n.LinkText == "" {
			n.LinkText = "打开链接"
		}
	} else {
		n.LinkText = ""
	}
	if req.Target.Empty() {
		addErr(&errs, "target", validate.ReasonRequired, "选一下发给谁")
	}
	if len(errs) > 0 {
		s.writeError(w, r, apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, errs))
		return
	}
	p := principalFrom(r)
	n.ID = newNoticeID()
	n.From = p.User.Nickname
	sent := s.live.Send(req.Target, api.LiveMessage{Type: api.LiveNotice, Notice: n})
	s.auditClient(r, store.AuditClientNotice, "", map[string]any{"title": n.Title, "target": req.Target, "sent": sent})
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent})
}

type clientTunnelRequest struct {
	Target live.Target `json:"target"`
	Action string      `json:"action"`
	Reason string      `json:"reason"`
}

// POST /api/v1/admin/clients/tunnel：远程停止 / 重启选中客户端的隧道。
func (s *Server) handleAdminClientTunnel(w http.ResponseWriter, r *http.Request) {
	var req clientTunnelRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	var errs validate.Errors
	if !oneOf(req.Action, api.TunnelStop, api.TunnelRestart) {
		addErr(&errs, "action", validate.ReasonInvalid, "动作只能是 stop 或 restart")
	}
	if utf8.RuneCountInString(req.Reason) > maxReason {
		addErr(&errs, "reason", validate.ReasonInvalid, "原因最多 200 个字")
	}
	if req.Target.Empty() {
		addErr(&errs, "target", validate.ReasonRequired, "选一下发给谁")
	}
	if len(errs) > 0 {
		s.writeError(w, r, apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, errs))
		return
	}
	cmd := &api.LiveTunnelCommand{Action: req.Action, Reason: req.Reason, From: principalFrom(r).User.Nickname}
	sent := s.live.Send(req.Target, api.LiveMessage{Type: api.LiveTunnel, Tunnel: cmd})
	s.auditClient(r, store.AuditClientTunnel, "", map[string]any{"action": req.Action, "reason": req.Reason, "target": req.Target, "sent": sent})
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent})
}

type clientKickRequest struct {
	Sessions []string `json:"sessions"`
}

// POST /api/v1/admin/clients/kick：让选中的设备下线（吊销这些会话，客户端马上回到未登录）。
func (s *Server) handleAdminClientKick(w http.ResponseWriter, r *http.Request) {
	var req clientKickRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if len(req.Sessions) == 0 || len(req.Sessions) > 500 {
		s.writeError(w, r, apierr.New(apierr.ValidationFailed).With(apierr.DetailFields,
			validate.Errors{{Field: "sessions", Reason: validate.ReasonInvalid, Message: "选 1–500 台设备"}}))
		return
	}
	n := 0
	for _, id := range req.Sessions {
		ok, err := store.RevokeSession(r.Context(), s.st.DB(), id, store.RevokeAdmin, s.now().Unix())
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if ok {
			n++
		}
	}
	s.auditClient(r, store.AuditClientKick, "", map[string]any{"sessions": req.Sessions, "revoked": n})
	s.sessionsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

// GET /api/v1/admin/client-config：当前生效的客户端配置，以及是不是后台改过的。
func (s *Server) handleAdminClientConfig(w http.ResponseWriter, r *http.Request) {
	c := s.clientCfg()
	s.clientCfgState.mu.RLock()
	fromPanel := s.clientCfgState.override != nil
	s.clientCfgState.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"config": clientSettings{LatestVersion: c.LatestVersion, MinVersion: c.MinVersion, DownloadURL: c.DownloadURL,
			SHA256: c.SHA256, ReleaseNotes: c.ReleaseNotes, Announcement: c.Announcement},
		"fromPanel": fromPanel,
		"online":    s.live.Count(),
	})
}

// PUT /api/v1/admin/client-config：修改并立即推给所有在线客户端（最低版本提高后，低于它的客户端马上停用账号功能）。
func (s *Server) handleAdminSetClientConfig(w http.ResponseWriter, r *http.Request) {
	var c clientSettings
	if err := decodeJSON(r, &c); err != nil {
		s.writeError(w, r, err)
		return
	}
	c.LatestVersion, c.MinVersion = strings.TrimSpace(c.LatestVersion), strings.TrimSpace(c.MinVersion)
	c.DownloadURL, c.SHA256 = strings.TrimSpace(c.DownloadURL), strings.ToLower(strings.TrimSpace(c.SHA256))
	c.ReleaseNotes, c.Announcement = strings.TrimSpace(c.ReleaseNotes), strings.TrimSpace(c.Announcement)
	var errs validate.Errors
	var latest, min *api.Version
	if c.LatestVersion != "" {
		if v, err := api.ParseVersion(c.LatestVersion); err != nil {
			addErr(&errs, "latestVersion", validate.ReasonInvalid, "版本号格式不对，例如 3.1.0")
		} else {
			latest = &v
		}
	}
	if c.MinVersion != "" {
		if v, err := api.ParseVersion(c.MinVersion); err != nil {
			addErr(&errs, "minVersion", validate.ReasonInvalid, "版本号格式不对，例如 3.0.0")
		} else {
			min = &v
		}
	}
	if latest != nil && min != nil && min.Compare(*latest) > 0 {
		addErr(&errs, "minVersion", validate.ReasonInvalid, "最低版本不能高于最新版本")
	}
	if min != nil && latest == nil {
		addErr(&errs, "latestVersion", validate.ReasonRequired, "设了最低版本就要填最新版本（客户端要知道去下载哪个）")
	}
	if c.DownloadURL != "" {
		if u, err := url.Parse(c.DownloadURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			addErr(&errs, "downloadUrl", validate.ReasonInvalid, "下载地址要以 http:// 或 https:// 开头")
		}
	}
	if c.SHA256 != "" && !isHex(c.SHA256, 64) {
		addErr(&errs, "sha256", validate.ReasonInvalid, "SHA-256 是 64 位十六进制")
	}
	if utf8.RuneCountInString(c.ReleaseNotes) > 4000 {
		addErr(&errs, "releaseNotes", validate.ReasonInvalid, "更新说明最多 4000 个字")
	}
	if utf8.RuneCountInString(c.Announcement) > 2000 {
		addErr(&errs, "announcement", validate.ReasonInvalid, "公告最多 2000 个字")
	}
	if len(errs) > 0 {
		s.writeError(w, r, apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, errs))
		return
	}
	b, _ := json.Marshal(c)
	if err := store.SetSetting(r.Context(), s.st.DB(), settingClientConfig, string(b)); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.setClientOverride(&c); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.auditClient(r, store.AuditClientConfig, "", map[string]any{"latestVersion": c.LatestVersion, "minVersion": c.MinVersion})
	s.pushClientConfig()
	s.handleAdminClientConfig(w, r)
}

// DELETE /api/v1/admin/client-config：回到配置文件里的设置。
func (s *Server) handleAdminResetClientConfig(w http.ResponseWriter, r *http.Request) {
	if err := store.SetSetting(r.Context(), s.st.DB(), settingClientConfig, ""); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.setClientOverride(nil); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.auditClient(r, store.AuditClientConfig, "", map[string]any{"reset": true})
	s.pushClientConfig()
	s.handleAdminClientConfig(w, r)
}

func (s *Server) pushClientConfig() {
	cc := s.clientCfg().ClientConfig()
	s.live.Broadcast(api.LiveMessage{Type: api.LiveConfig, Config: &cc})
}

func (s *Server) auditClient(r *http.Request, action, target string, detail map[string]any) {
	a := actorOf(r)
	detail["via"] = "web"
	if err := store.InsertAudit(r.Context(), s.st.DB(), store.AuditEntry{At: s.now().Unix(), ActorID: a.UserID, Action: action,
		Target: target, IP: a.IP, Detail: detail}); err != nil {
		s.log.Warn("audit failed", "action", action, "err", err)
	}
}

func oneOf(v string, opts ...string) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func addErr(errs *validate.Errors, field, reason, msg string) {
	*errs = append(*errs, validate.FieldError{Field: field, Reason: reason, Message: msg})
}

func newNoticeID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
