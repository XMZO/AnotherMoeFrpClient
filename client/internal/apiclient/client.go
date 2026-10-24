// Package apiclient 是客户端访问 moefrp-server 的 HTTP 客户端（PLAN 5.3、7.5）。
//
//   - 请求和响应都用 shared/api 的结构体；
//   - 每个请求都带 X-Client-Version 和 X-Install-Id；
//   - 出错时一律返回 *apierr.Error：服务端的错误原样保留 code（调用方按 code 处理，
//     不能"凡是 401 就下线"，例如 invalid_credentials 也是 401）；连不上服务端时是
//     apperr.ServerUnreachable，界面据此显示"离线"。
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// DefaultTimeout 是单个请求的超时。
const DefaultTimeout = 15 * time.Second

// maxResponse 是响应体的读取上限（服务端请求体上限是 256KB，state 最大约 50 份配置）。
const maxResponse = 16 << 20

// Client 是并发安全的 API 客户端。
type Client struct {
	base      *url.URL
	version   string
	installID string

	mu     sync.RWMutex
	token  string
	hc     *http.Client
	mode   string
	custom string
}

// New 创建客户端。base 是服务端根地址，例如 https://frp.example.com。
func New(base, version, installID, proxyMode string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("apiclient: invalid base url %q", base)
	}
	c := &Client{base: u, version: version, installID: installID}
	c.SetProxyMode(proxyMode)
	return c, nil
}

// BaseURL 返回服务端根地址。
func (c *Client) BaseURL() string { return c.base.String() }

// SetProxyMode 切换代理模式（跟随系统 / 不使用代理），之后的请求生效。
func (c *Client) SetProxyMode(mode string) { c.SetProxy(mode, "") }

// SetProxy 切换代理：mode 为 system / none / custom，custom 时用 customURL。之后的请求生效。
func (c *Client) SetProxy(mode, customURL string) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFunc(mode, customURL)
	tr.ResponseHeaderTimeout = DefaultTimeout
	c.mu.Lock()
	old := c.hc
	c.hc = &http.Client{Transport: tr, Timeout: DefaultTimeout}
	c.mode, c.custom = mode, customURL
	c.mu.Unlock()
	if old != nil {
		old.CloseIdleConnections()
	}
}

// NewHTTPClient 返回一个和 API 客户端用同样代理设置的 http.Client（图片面板用），超时由调用方指定。
func (c *Client) NewHTTPClient(timeout time.Duration) *http.Client {
	c.mu.RLock()
	mode, custom := c.mode, c.custom
	c.mu.RUnlock()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFunc(mode, custom)
	return &http.Client{Transport: tr, Timeout: timeout}
}

// SetToken 设置会话令牌，空串表示未登录。
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

// Token 返回当前令牌。
func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// do 发出请求。in 非 nil 时编码为 JSON 请求体；out 非 nil 且状态码为 2xx（204 除外）时解码响应。
// 返回响应头和状态码；出错时返回 *apierr.Error。
func (c *Client) do(ctx context.Context, method, path string, hdr http.Header, in, out any) (http.Header, int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, 0, apperr.Wrap(apperr.InvalidArg, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return nil, 0, apperr.Wrap(apperr.InvalidArg, err)
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(api.HeaderClientVersion, c.version)
	req.Header.Set(api.HeaderInstallID, c.installID)
	c.mu.RLock()
	token, hc := c.token, c.hc
	c.mu.RUnlock()
	if token != "" {
		req.Header.Set(api.HeaderAuthorization, api.BearerPrefix+token)
	}

	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, apperr.Wrap(apperr.ServerUnreachable, ctx.Err())
		}
		return nil, 0, apperr.Wrap(apperr.ServerUnreachable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, 0, apperr.Wrap(apperr.ServerUnreachable, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotModified, resp.StatusCode == http.StatusNoContent:
		return resp.Header, resp.StatusCode, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				return nil, resp.StatusCode, apperr.New(apperr.BadResponse, fmt.Sprintf("%s %s: %v", method, path, err))
			}
		}
		return resp.Header, resp.StatusCode, nil
	}
	return resp.Header, resp.StatusCode, decodeError(resp.StatusCode, data)
}

// decodeError 把错误响应转换成 *apierr.Error。不是我们的错误格式时（例如网关返回的 HTML）
// 按状态码归类：502/503/504 视为连不上服务端。
func decodeError(status int, data []byte) *apierr.Error {
	var r apierr.Response
	if err := json.Unmarshal(data, &r); err == nil && r.Error != nil && r.Error.Code != "" {
		return r.Error
	}
	msg := fmt.Sprintf("http %d: %s", status, truncate(string(data), 200))
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return apperr.New(apperr.ServerUnreachable, msg)
	case http.StatusTooManyRequests:
		return apierr.Newf(apierr.RateLimited, "%s", msg)
	}
	return apperr.New(apperr.BadResponse, msg)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- 错误分类 ----

// IsOffline 判断错误是不是"连不上服务端"。
func IsOffline(err error) bool {
	return apierr.CodeOf(err) == apperr.ServerUnreachable
}

// SessionEnded 判断错误是否意味着当前会话已经失效，需要下线：
// unauthorized、session_expired、session_replaced、account_disabled。
// invalid_credentials（昵称或密码错误，也是 401）不算。
func SessionEnded(err error) bool {
	switch apierr.CodeOf(err) {
	case apierr.Unauthorized, apierr.SessionExpired, apierr.SessionReplaced, apierr.AccountDisabled:
		return true
	}
	return false
}

// IsOutdated 判断是不是 426 client_outdated。
func IsOutdated(err error) bool { return apierr.CodeOf(err) == apierr.ClientOutdated }

// ConflictCurrent 从 409 revision_conflict 的 details.current 里取出云端当前版本。
func ConflictCurrent(err error) (model.Profile, bool) {
	e, ok := apierr.As(err)
	if !ok || e.Code != apierr.RevisionConflict || e.Details == nil {
		return model.Profile{}, false
	}
	raw, ok := e.Details[apierr.DetailCurrent]
	if !ok || raw == nil {
		return model.Profile{}, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return model.Profile{}, false
	}
	var p model.Profile
	if json.Unmarshal(b, &p) != nil || p.ID == "" {
		return model.Profile{}, false
	}
	p.Normalize()
	return p, true
}

// ---- 公开接口 ----

// ClientConfig：GET /api/v1/client/config。
func (c *Client) ClientConfig(ctx context.Context) (api.ClientConfigResponse, error) {
	var out api.ClientConfigResponse
	_, _, err := c.do(ctx, http.MethodGet, api.RouteClientConfig, nil, nil, &out)
	return out, err
}

// ---- 认证 ----

// Register：POST /api/v1/auth/register。注册不会自动登录。
func (c *Client) Register(ctx context.Context, req api.RegisterRequest) (api.RegisterResponse, error) {
	var out api.RegisterResponse
	_, _, err := c.do(ctx, http.MethodPost, api.RouteRegister, nil, req, &out)
	return out, err
}

// Login：POST /api/v1/auth/login。成功后不会自动 SetToken，由调用方决定。
func (c *Client) Login(ctx context.Context, req api.LoginRequest) (api.LoginResponse, error) {
	var out api.LoginResponse
	_, _, err := c.do(ctx, http.MethodPost, api.RouteLogin, nil, req, &out)
	return out, err
}

// Logout：POST /api/v1/auth/logout，吊销当前会话。
func (c *Client) Logout(ctx context.Context) error {
	_, _, err := c.do(ctx, http.MethodPost, api.RouteLogout, nil, nil, nil)
	return err
}

// ChangePassword：POST /api/v1/auth/change-password。旧密码错误时返回 invalid_credentials（401，不代表会话失效）。
func (c *Client) ChangePassword(ctx context.Context, req api.ChangePasswordRequest) error {
	_, _, err := c.do(ctx, http.MethodPost, api.RouteChangePassword, nil, req, nil)
	return err
}

// ResetPassword：POST /api/v1/auth/reset-password。
func (c *Client) ResetPassword(ctx context.Context, req api.ResetPasswordRequest) error {
	_, _, err := c.do(ctx, http.MethodPost, api.RouteResetPassword, nil, req, nil)
	return err
}

// Me：GET /api/v1/me。
func (c *Client) Me(ctx context.Context) (api.MeResponse, error) {
	var out api.MeResponse
	_, _, err := c.do(ctx, http.MethodGet, api.RouteMe, nil, nil, &out)
	return out, err
}

// ---- 云端状态与配置 ----

// State：GET /api/v1/state。stateVersion >= 0 时带 If-None-Match，没有变化返回 notModified=true。
func (c *Client) State(ctx context.Context, stateVersion int64) (st api.StateResponse, notModified bool, err error) {
	var hdr http.Header
	if stateVersion >= 0 {
		hdr = http.Header{api.HeaderIfNoneMatch: {api.StateETag(stateVersion)}}
	}
	_, code, err := c.do(ctx, http.MethodGet, api.RouteState, hdr, nil, &st)
	if err != nil {
		return st, false, err
	}
	if code == http.StatusNotModified {
		return st, true, nil
	}
	for i := range st.Profiles {
		st.Profiles[i].Normalize()
	}
	return st, false, nil
}

// OfficialNodes：GET /api/v1/official-nodes。etag 不为空时带 If-None-Match，没有变化返回 notModified=true。
// 返回新的 ETag。
func (c *Client) OfficialNodes(ctx context.Context, etag string) (nodes []api.OfficialNode, newETag string, notModified bool, err error) {
	var hdr http.Header
	if etag != "" {
		hdr = http.Header{api.HeaderIfNoneMatch: {etag}}
	}
	var out api.OfficialNodesResponse
	h, code, err := c.do(ctx, http.MethodGet, api.RouteOfficialNodes, hdr, nil, &out)
	if err != nil {
		return nil, "", false, err
	}
	if code == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if out.Nodes == nil {
		out.Nodes = []api.OfficialNode{}
	}
	return out.Nodes, h.Get("ETag"), false, nil
}

// CreateProfile：POST /api/v1/profiles。
func (c *Client) CreateProfile(ctx context.Context, req api.CreateProfileRequest) (model.Profile, error) {
	var out model.Profile
	_, _, err := c.do(ctx, http.MethodPost, api.RouteProfiles, nil, req, &out)
	out.Normalize()
	return out, err
}

// UpdateProfile：PUT /api/v1/profiles/{id}。版本冲突时返回 409 revision_conflict，用 ConflictCurrent 取云端版本。
func (c *Client) UpdateProfile(ctx context.Context, id string, req api.UpdateProfileRequest) (model.Profile, error) {
	req.Nodes, req.Proxies, req.Visitors = nonNil(req.Nodes), nonNil(req.Proxies), nonNil(req.Visitors)
	var out model.Profile
	_, _, err := c.do(ctx, http.MethodPut, api.ProfilePath(id), nil, req, &out)
	out.Normalize()
	return out, err
}

// DeleteProfile：DELETE /api/v1/profiles/{id}。
func (c *Client) DeleteProfile(ctx context.Context, id string) error {
	_, _, err := c.do(ctx, http.MethodDelete, api.ProfilePath(id), nil, nil, nil)
	return err
}

// 服务端要求 PUT 的数组字段必须出现且不为 null。
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// DeviceName 返回登录时上报的设备名（计算机名，最多 50 个字符）。
func DeviceName(hostname string) string {
	r := []rune(strings.TrimSpace(hostname))
	if len(r) > 50 {
		r = r[:50]
	}
	return string(r)
}
