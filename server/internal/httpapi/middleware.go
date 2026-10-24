package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/ratelimit"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// reqInfo 在请求开始时放进 context，后面的中间件往里面填内容，访问日志最后读取。
type reqInfo struct {
	RequestID string
	IP        string
	UserID    string
	principal *auth.Principal
}

type ctxKey struct{}

func infoFrom(ctx context.Context) *reqInfo {
	if v, ok := ctx.Value(ctxKey{}).(*reqInfo); ok {
		return v
	}
	return &reqInfo{}
}

// principalFrom 返回认证中间件放进来的当前用户。只在需要登录的路由里调用。
func principalFrom(r *http.Request) auth.Principal {
	if p := infoFrom(r.Context()).principal; p != nil {
		return *p
	}
	panic("httpapi: principal missing; route is not wrapped with requireAuth")
}

type middleware func(http.Handler) http.Handler

func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// 1. 请求 ID：每个请求生成一个，写进响应头 X-Request-Id 和访问日志。
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [8]byte
		_, _ = rand.Read(b[:])
		info := &reqInfo{RequestID: hex.EncodeToString(b[:])}
		w.Header().Set(api.HeaderRequestID, info.RequestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, info)))
	})
}

// 2. 真实 IP：只有 trusted_proxies 里的对端发来的 X-Forwarded-For 才被信任（PLAN 7.8）。
func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		infoFrom(r.Context()).IP = s.clientIP(r)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) clientIP(r *http.Request) string {
	peer, ok := parseIP(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !s.trusted(peer) {
		return peer.String()
	}
	// 从右往左找第一个不是可信代理的地址：最右边的是离我们最近的一跳。
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseIP(strings.TrimSpace(hops[i]))
		if !ok {
			break // 格式不对的值不可信，停在上一个可信代理认定的地址
		}
		client = a
		if !s.trusted(a) {
			break
		}
	}
	return client.String()
}

func (s *Server) trusted(a netip.Addr) bool {
	for _, p := range s.trustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ipKey 是按 IP 限流时用的 key。IPv6 按 /64 计：一个用户通常分到整个 /64，
// 换地址几乎没有成本，按单个地址限流形同虚设。
func ipKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() {
		return ip
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}

// parseIP 解析 "1.2.3.4"、"1.2.3.4:5"、"[::1]:5"、"::1"。
func parseIP(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

// 3. panic 恢复：记录堆栈，返回 500 internal。
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &statusWriter{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				s.log.ErrorContext(r.Context(), "panic",
					"request_id", infoFrom(r.Context()).RequestID, "path", r.URL.Path,
					"panic", p, "stack", string(debug.Stack()))
				if tw.status == 0 { // 还没写响应头时才能补一个 500
					writeJSON(tw, http.StatusInternalServerError, apierr.Response{Error: apierr.New(apierr.Internal)})
				}
			}
		}()
		next.ServeHTTP(tw, r)
	})
}

// 4. 请求体大小限制（PLAN 7.7：256KB）。
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBodyBytes {
			s.writeError(w, r, apierr.New(apierr.PayloadTooLarge).With("limit", MaxBodyBytes))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// statusWriter 记录状态码和响应字节数。
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// 5. 访问日志：方法、路径、状态码、耗时、用户 ID、IP（PLAN 7.9）。
// 不记录查询串、请求体和 Authorization 头。
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		sw := &statusWriter{ResponseWriter: w}
		panicked := true
		defer func() {
			info := infoFrom(r.Context())
			status := sw.status
			if status == 0 {
				status = http.StatusOK
				if panicked {
					status = http.StatusInternalServerError
				}
			}
			s.log.InfoContext(r.Context(), "http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"duration_ms", float64(s.now().Sub(start).Microseconds())/1000,
				"bytes", sw.bytes,
				"user_id", info.UserID,
				"ip", info.IP,
				"request_id", info.RequestID,
			)
		}()
		next.ServeHTTP(sw, r)
		panicked = false
	})
}

// 6. 客户端版本检查：X-Client-Version 低于 client.min_version 时返回 426 client_outdated（PLAN 6.12）。
//
// 只检查 /api/v1 下的接口，/api/v1/client/config 除外（客户端靠它得知新版本）。
// 没有带这个头的请求不检查（curl、监控等）；带了但无法解析的按过低处理。
func (s *Server) versionCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 管理后台的接口不受客户端最低版本限制
		if mv := s.clientMinVersion(); mv != nil && strings.HasPrefix(r.URL.Path, api.Prefix+"/") && r.URL.Path != api.RouteClientConfig &&
			!strings.HasPrefix(r.URL.Path, adminAPIPrefix+"/") {
			if v := r.Header.Get(api.HeaderClientVersion); v != "" {
				cv, err := api.ParseVersion(v)
				if err != nil || cv.Compare(*mv) < 0 {
					s.writeError(w, r, s.outdatedError())
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) outdatedError() *apierr.Error {
	c := s.clientCfg()
	return apierr.New(apierr.ClientOutdated).
		With(apierr.DetailLatestVersion, c.LatestVersion).
		With(apierr.DetailMinVersion, c.MinVersion).
		With(apierr.DetailDownloadURL, c.DownloadURL)
}

// 7. 认证：校验 Bearer 令牌，把用户放进 context。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get(api.HeaderAuthorization)
		token, ok := strings.CutPrefix(h, api.BearerPrefix)
		if !ok {
			// 也接受小写的 bearer
			if len(h) > len(api.BearerPrefix) && strings.EqualFold(h[:len(api.BearerPrefix)], api.BearerPrefix) {
				token, ok = h[len(api.BearerPrefix):], true
			}
		}
		if !ok || token == "" {
			s.writeError(w, r, apierr.New(apierr.Unauthorized))
			return
		}
		p, err := s.auth.Authenticate(r.Context(), strings.TrimSpace(token))
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		info := infoFrom(r.Context())
		info.UserID = p.User.ID
		info.principal = &p
		next.ServeHTTP(w, r)
	})
}

// 8. 限流：按 IP 或按用户。超限时返回 429 rate_limited 和 retryAfter（PLAN 7.8）。
type limitKey int

const (
	byIP limitKey = iota
	byUser
	bySession // 按客户端会话（同一用户的不同设备分开算）
)

func (s *Server) limit(l *ratelimit.Limiter, by limitKey) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := infoFrom(r.Context())
			key := ipKey(info.IP)
			if by == byUser && info.UserID != "" {
				key = info.UserID
			}
			if by == bySession && info.principal != nil {
				key = "s:" + info.principal.Session.ID
			}
			if ok, wait := l.Allow(key, s.now()); !ok {
				secs := int64((wait + time.Second - 1) / time.Second)
				s.writeError(w, r, apierr.New(apierr.RateLimited).With(apierr.DetailRetryAfter, secs))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
