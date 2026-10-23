// Package httpapi 是服务端的 HTTP 接口：路由、中间件、处理函数（PLAN 7.5–7.9）。
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/config"
	"moefrp/server/internal/legacy"
	"moefrp/server/internal/profile"
	"moefrp/server/internal/ratelimit"
	"moefrp/server/internal/share"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// Limits 是各接口的限流规则（PLAN 7.8）。
type Limits struct {
	Login         ratelimit.Rule // 每个 IP
	Register      ratelimit.Rule // 每个 IP
	ResetPassword ratelimit.Rule // 每个 IP
	Authenticated ratelimit.Rule // 每个用户，其他需要登录的接口
	SharePreview  ratelimit.Rule // 每个用户，分享预览
}

// DefaultLimits 按 PLAN 7.8。
var DefaultLimits = Limits{
	Login:         ratelimit.Rule{Limit: 10, Window: time.Minute},
	Register:      ratelimit.Rule{Limit: 5, Window: time.Hour},
	ResetPassword: ratelimit.Rule{Limit: 5, Window: 10 * time.Minute},
	Authenticated: ratelimit.Rule{Limit: 120, Window: time.Minute},
	SharePreview:  ratelimit.Rule{Limit: 30, Window: time.Minute},
}

// Deps 是创建 Server 需要的依赖。
type Deps struct {
	Config   config.Config
	Store    *store.Store
	Auth     *auth.Service
	Profiles *profile.Service
	Shares   *share.Service // nil 时自动创建
	Logger   *slog.Logger
	Version  string           // 服务端版本，显示在 /healthz
	Limits   *Limits          // nil 时用 DefaultLimits
	Now      func() time.Time // nil 时用 time.Now
}

// Server 实现 http.Handler。
type Server struct {
	cfg      config.Config
	st       *store.Store
	auth     *auth.Service
	profiles *profile.Service
	shares   *share.Service
	log      *slog.Logger
	version  string
	now      func() time.Time

	trustedProxies []netip.Prefix
	minVersion     *api.Version

	limLogin, limRegister, limReset, limUser, limPreview *ratelimit.Limiter

	mux     *http.ServeMux
	handler http.Handler
}

// New 创建 Server 并注册路由。
func New(d Deps) (*Server, error) {
	if d.Store == nil || d.Auth == nil || d.Profiles == nil {
		return nil, errors.New("httpapi: missing dependency")
	}
	prefixes, err := d.Config.TrustedPrefixes()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:            d.Config,
		st:             d.Store,
		auth:           d.Auth,
		profiles:       d.Profiles,
		shares:         d.Shares,
		log:            d.Logger,
		version:        d.Version,
		now:            d.Now,
		trustedProxies: prefixes,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.shares == nil {
		s.shares = share.New(d.Store, s.now)
	}
	if v := d.Config.Client.MinVersion; v != "" {
		mv, err := api.ParseVersion(v)
		if err != nil {
			return nil, err
		}
		s.minVersion = &mv
	}
	limits := DefaultLimits
	if d.Limits != nil {
		limits = *d.Limits
	}
	s.limLogin = ratelimit.New(limits.Login)
	s.limRegister = ratelimit.New(limits.Register)
	s.limReset = ratelimit.New(limits.ResetPassword)
	s.limUser = ratelimit.New(limits.Authenticated)
	s.limPreview = ratelimit.New(limits.SharePreview)

	s.routes()
	// 中间件顺序（PLAN 7.5）：请求 ID → 真实 IP → panic 恢复 → 请求体大小限制 → 访问日志
	// → 客户端版本检查 → 认证 → 限流。认证和限流按路由挂在 routes() 里。
	s.handler = chain(s.mux, s.requestID, s.realIP, s.recoverer, s.bodyLimit, s.accessLog, s.versionCheck)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) routes() {
	mux := http.NewServeMux()
	s.mux = mux

	public := func(h http.HandlerFunc, l *ratelimit.Limiter) http.Handler {
		return chain(h, s.limit(l, byIP))
	}
	authed := func(h http.HandlerFunc) http.Handler {
		return chain(h, s.requireAuth, s.limit(s.limUser, byUser))
	}

	mux.HandleFunc("GET "+api.RouteHealthz, s.handleHealth)
	mux.HandleFunc("GET "+api.RouteClientConfig, s.handleClientConfig)

	mux.Handle("POST "+api.RouteRegister, public(s.handleRegister, s.limRegister))
	mux.Handle("POST "+api.RouteLogin, public(s.handleLogin, s.limLogin))
	mux.Handle("POST "+api.RouteResetPassword, public(s.handleResetPassword, s.limReset))
	mux.Handle("POST "+api.RouteLogout, authed(s.handleLogout))
	mux.Handle("POST "+api.RouteChangePassword, authed(s.handleChangePassword))

	mux.Handle("GET "+api.RouteMe, authed(s.handleMe))
	mux.Handle("GET "+api.RouteState, authed(s.handleState))

	mux.Handle("POST "+api.RouteProfiles, authed(s.handleCreateProfile))
	mux.Handle("GET "+api.RouteProfile, authed(s.handleGetProfile))
	mux.Handle("PUT "+api.RouteProfile, authed(s.handleUpdateProfile))
	mux.Handle("DELETE "+api.RouteProfile, authed(s.handleDeleteProfile))

	mux.Handle("POST "+api.RouteShares, authed(s.handleCreateShare))
	mux.Handle("GET "+api.RouteShares, authed(s.handleListShares))
	mux.Handle("PATCH "+api.RouteShare, authed(s.handleUpdateShare))
	mux.Handle("DELETE "+api.RouteShare, authed(s.handleRevokeShare))
	mux.Handle("POST "+api.RouteShareRepublish, authed(s.handleRepublishShare))
	mux.Handle("GET "+api.RouteSharePreview, chain(http.HandlerFunc(s.handleSharePreview),
		s.requireAuth, s.limit(s.limUser, byUser), s.limit(s.limPreview, byUser)))

	mux.Handle("POST "+api.RouteSubscriptions, authed(s.handleSubscribe))
	mux.Handle("PUT "+api.RouteSubscription, authed(s.handleUpdateSubscription))
	mux.Handle("DELETE "+api.RouteSubscription, authed(s.handleUnsubscribe))
	mux.Handle("GET "+api.RouteSubscriptionRuntime, authed(s.handleSubscriptionRuntime))

	// /api/v1 下其他路径：404 或 405，统一 JSON 格式
	mux.HandleFunc(api.Prefix+"/", s.handleV1Fallback)
	// 旧客户端：get_challenge 返回假挑战码，其余 426（PLAN 7.12）
	mux.Handle(api.LegacyPrefix, legacy.Handler(s.cfg.Client.DownloadURL))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, apierr.New(apierr.NotFound))
	})
}

var probeMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// handleV1Fallback 区分"路径不存在"（404）和"路径存在但方法不对"（405）。
func (s *Server) handleV1Fallback(w http.ResponseWriter, r *http.Request) {
	var allowed []string
	for _, m := range probeMethods {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := s.mux.Handler(probe); pattern != "" && pattern != api.Prefix+"/" {
			allowed = append(allowed, m)
		}
	}
	if len(allowed) > 0 {
		for _, m := range allowed {
			w.Header().Add("Allow", m)
		}
		s.writeError(w, r, apierr.New(apierr.MethodNotAllowed))
		return
	}
	s.writeError(w, r, apierr.New(apierr.NotFound))
}

// Sweep 清理限流器里过期的 key，由 serve 定期调用。
func (s *Server) Sweep() {
	now := s.now()
	for _, l := range []*ratelimit.Limiter{s.limLogin, s.limRegister, s.limReset, s.limUser, s.limPreview} {
		l.Sweep(now)
	}
}

// RunMaintenance 每隔 interval 清理一次限流器、锁定表、过期会话和重置码，直到 ctx 结束。
func (s *Server) RunMaintenance(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep()
			if err := s.auth.Cleanup(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("maintenance", "err", err)
			}
		}
	}
}
