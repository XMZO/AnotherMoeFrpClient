package httpapi

// 后台"设置"页（docs/server-ops.md"后台设置"一节）：面板运行中就能改的配置，存在数据库里，改了立即生效，
// 优先于配置文件；每一组都可以"恢复为配置文件"。部署相关、改了必须重启的（监听地址、数据库、受信任代理、
// 主密钥、agent 来源）只读显示，注明在哪里改。
//
//   - 客户端：版本、最低版本、下载地址、更新说明、公告、图片面板的图源（settings.client_config，见 live.go）；
//   - 账号与登录：单设备模式、客户端登录的有效期（settings.panel_auth）；
//   - 节点：写进安装命令的面板地址、添加节点时默认的下载代理（settings.panel_nodes）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"moefrp/server/internal/nodes"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

const (
	settingPanelAuth  = "panel_auth"
	settingPanelNodes = "panel_nodes"

	maxImageSources = 20
)

// authSettings 是"账号与登录"一组。
type authSettings struct {
	SingleSession   bool `json:"singleSession"`
	SessionTTLHours int  `json:"sessionTtlHours"`
}

// nodeSettings 是"节点"一组。
type nodeSettings struct {
	PublicURL   string `json:"publicUrl"`
	AgentMirror string `json:"agentMirror"`
}

// panelState 是数据库里的设置（nil 表示这一组用配置文件）。
type panelState struct {
	mu    sync.RWMutex
	auth  *authSettings
	nodes *nodeSettings
}

func loadSetting[T any](ctx context.Context, s *Server, key string) (*T, error) {
	v, ok, err := store.GetSetting(ctx, s.st.DB(), key)
	if err != nil || !ok || v == "" {
		return nil, err
	}
	var out T
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		s.log.Warn("bad panel setting, using config file", "key", key, "err", err)
		return nil, nil
	}
	return &out, nil
}

func (s *Server) initPanelSettings(ctx context.Context) error {
	a, err := loadSetting[authSettings](ctx, s, settingPanelAuth)
	if err != nil {
		return err
	}
	n, err := loadSetting[nodeSettings](ctx, s, settingPanelNodes)
	if err != nil {
		return err
	}
	s.panel.auth, s.panel.nodes = a, n
	s.applyAuthSettings()
	return nil
}

// authCfg 是当前生效的"账号与登录"。
func (s *Server) authCfg() authSettings {
	s.panel.mu.RLock()
	defer s.panel.mu.RUnlock()
	if s.panel.auth != nil {
		return *s.panel.auth
	}
	ttl := s.cfg.SessionTTL.D()
	if ttl == 0 {
		ttl = 30 * 24 * time.Hour
	}
	return authSettings{SingleSession: s.cfg.SingleSession, SessionTTLHours: int(ttl / time.Hour)}
}

func (s *Server) applyAuthSettings() {
	a := s.authCfg()
	s.auth.SetSessionPolicy(a.SingleSession, time.Duration(a.SessionTTLHours)*time.Hour)
}

// nodeCfg 是当前生效的"节点"。
func (s *Server) nodeCfg() nodeSettings {
	s.panel.mu.RLock()
	defer s.panel.mu.RUnlock()
	if s.panel.nodes != nil {
		return *s.panel.nodes
	}
	return nodeSettings{PublicURL: s.cfg.PublicURL, AgentMirror: s.cfg.AgentMirror}
}

// ---- 接口 ----

func (s *Server) adminPanelRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	p := adminAPIPrefix
	mux.Handle("GET "+p+"/panel-settings", authed(s.handlePanelSettings))
	mux.Handle("PUT "+p+"/panel-settings/auth", authed(s.handleSetPanelAuth))
	mux.Handle("DELETE "+p+"/panel-settings/auth", authed(s.handleResetPanelAuth))
	mux.Handle("PUT "+p+"/panel-settings/nodes", authed(s.handleSetPanelNodes))
	mux.Handle("DELETE "+p+"/panel-settings/nodes", authed(s.handleResetPanelNodes))
}

// GET /api/v1/admin/panel-settings：全部设置和来源，以及只读的部署信息。
func (s *Server) handlePanelSettings(w http.ResponseWriter, r *http.Request) {
	c := s.clientCfg()
	s.clientCfgState.mu.RLock()
	clientFromPanel := s.clientCfgState.override != nil
	s.clientCfgState.mu.RUnlock()
	s.panel.mu.RLock()
	authFromPanel, nodesFromPanel := s.panel.auth != nil, s.panel.nodes != nil
	s.panel.mu.RUnlock()
	agentSrc := "没有配置（不能安装和升级节点）"
	switch {
	case s.cfg.AgentDistDir != "":
		agentSrc = "本地目录 " + s.cfg.AgentDistDir
	case s.cfg.AgentRepo != "":
		agentSrc = "GitHub " + s.cfg.AgentRepo
	}
	secret, _ := s.nodes.Secret(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"client": map[string]any{"config": clientSettingsOf(c), "fromPanel": clientFromPanel},
		"auth":   map[string]any{"config": s.authCfg(), "fromPanel": authFromPanel},
		"nodes":  map[string]any{"config": s.nodeCfg(), "fromPanel": nodesFromPanel},
		"online": s.live.Count(),
		"deploy": map[string]any{
			"version": s.version, "listen": s.cfg.Listen, "dbPath": s.cfg.DBPath, "trustedProxies": s.cfg.TrustedProxies,
			"logLevel": s.cfg.LogLevel, "agentSource": agentSrc, "secretKey": secret.Protection,
		},
	})
}

// PUT /api/v1/admin/panel-settings/auth
func (s *Server) handleSetPanelAuth(w http.ResponseWriter, r *http.Request) {
	var a authSettings
	if err := decodeJSON(r, &a); err != nil {
		s.writeError(w, r, err)
		return
	}
	if a.SessionTTLHours < 1 || a.SessionTTLHours > 24*365 {
		s.writeError(w, r, fieldErr("sessionTtlHours", "登录有效期要在 1 小时到 365 天之间"))
		return
	}
	if err := s.savePanel(r, settingPanelAuth, &a); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.panel.mu.Lock()
	s.panel.auth = &a
	s.panel.mu.Unlock()
	s.applyAuthSettings()
	s.auditClient(r, store.AuditPanelSettings, "auth", map[string]any{"singleSession": a.SingleSession, "sessionTtlHours": a.SessionTTLHours})
	s.handlePanelSettings(w, r)
}

// DELETE /api/v1/admin/panel-settings/auth：恢复为配置文件。
func (s *Server) handleResetPanelAuth(w http.ResponseWriter, r *http.Request) {
	if err := store.SetSetting(r.Context(), s.st.DB(), settingPanelAuth, ""); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.panel.mu.Lock()
	s.panel.auth = nil
	s.panel.mu.Unlock()
	s.applyAuthSettings()
	s.auditClient(r, store.AuditPanelSettings, "auth", map[string]any{"reset": true})
	s.handlePanelSettings(w, r)
}

// PUT /api/v1/admin/panel-settings/nodes
func (s *Server) handleSetPanelNodes(w http.ResponseWriter, r *http.Request) {
	var n nodeSettings
	if err := decodeJSON(r, &n); err != nil {
		s.writeError(w, r, err)
		return
	}
	n.PublicURL = strings.TrimRight(strings.TrimSpace(n.PublicURL), "/")
	if n.PublicURL != "" {
		if u, err := url.Parse(n.PublicURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || strings.ContainsAny(n.PublicURL, "'\"` $\\") {
			s.writeError(w, r, fieldErr("publicUrl", "面板地址要类似 https://frp.example.com（不带路径）"))
			return
		}
	}
	m, err := nodes.NormalizeMirror(n.AgentMirror)
	if err != nil {
		s.writeError(w, r, fieldErr("agentMirror", "下载代理："+err.Error()))
		return
	}
	n.AgentMirror = m
	if err := s.savePanel(r, settingPanelNodes, &n); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.panel.mu.Lock()
	s.panel.nodes = &n
	s.panel.mu.Unlock()
	s.auditClient(r, store.AuditPanelSettings, "nodes", map[string]any{"publicUrl": n.PublicURL, "agentMirror": n.AgentMirror})
	s.handlePanelSettings(w, r)
}

// DELETE /api/v1/admin/panel-settings/nodes：恢复为配置文件。
func (s *Server) handleResetPanelNodes(w http.ResponseWriter, r *http.Request) {
	if err := store.SetSetting(r.Context(), s.st.DB(), settingPanelNodes, ""); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.panel.mu.Lock()
	s.panel.nodes = nil
	s.panel.mu.Unlock()
	s.auditClient(r, store.AuditPanelSettings, "nodes", map[string]any{"reset": true})
	s.handlePanelSettings(w, r)
}

func (s *Server) savePanel(r *http.Request, key string, v any) error {
	b, _ := json.Marshal(v)
	return store.SetSetting(r.Context(), s.st.DB(), key, string(b))
}

func fieldErr(field, msg string) error {
	return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{Field: field, Reason: validate.ReasonInvalid, Message: msg}})
}

// ---- 图源 ----

// imageSourceSetting 是图片面板的一个图源（后台可编辑，下发给客户端，PLAN 6.9）。
type imageSourceSetting struct {
	URL      string `json:"url"`
	Weight   int    `json:"weight"`
	IsAPI    bool   `json:"isApi"`    // true：地址返回 JSON，按 JSONPath 取图片地址；false：直接返回图片
	JSONPath string `json:"jsonPath"` // 例如 data.0.urls.regular
}

func validateImageSources(list []imageSourceSetting, errs *validate.Errors) {
	if len(list) > maxImageSources {
		addErr(errs, "imageSources", validate.ReasonInvalid, fmt.Sprintf("图源最多 %d 个", maxImageSources))
		return
	}
	for i := range list {
		src := &list[i]
		src.URL, src.JSONPath = strings.TrimSpace(src.URL), strings.TrimSpace(src.JSONPath)
		f := fmt.Sprintf("imageSources[%d]", i)
		if u, err := url.Parse(src.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(src.URL) > 1000 {
			addErr(errs, f+".url", validate.ReasonInvalid, fmt.Sprintf("第 %d 个图源的地址要以 http:// 或 https:// 开头", i+1))
		}
		if src.Weight < 1 || src.Weight > 100 {
			addErr(errs, f+".weight", validate.ReasonInvalid, fmt.Sprintf("第 %d 个图源的权重要在 1 到 100 之间", i+1))
		}
		if src.IsAPI && src.JSONPath == "" {
			addErr(errs, f+".jsonPath", validate.ReasonRequired, fmt.Sprintf("第 %d 个图源是 JSON 接口，要填图片地址在 JSON 里的路径，例如 data.0.url", i+1))
		}
		if !src.IsAPI {
			src.JSONPath = ""
		}
	}
}

func imageSourcesToAPI(list []imageSourceSetting) []api.ImageSource {
	out := make([]api.ImageSource, 0, len(list))
	for _, x := range list {
		out = append(out, api.ImageSource{URL: x.URL, Weight: x.Weight, IsAPI: x.IsAPI, JSONPath: x.JSONPath})
	}
	return out
}
