package apiclient

import (
	"context"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// DialLive 连接面板的实时通道（docs/client-live.md），代理设置和其他请求一样。未登录时返回 unauthorized。
// 面板拒绝时（会话失效、客户端版本过低）返回和普通请求一样的 *apierr.Error。
func (c *Client) DialLive(ctx context.Context) (*websocket.Conn, error) {
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()
	return c.dialWS(ctx, api.RouteClientLive, token)
}

// DialGuestLive 用游客令牌连游客的实时通道（联机房间）。
func (c *Client) DialGuestLive(ctx context.Context, token string) (*websocket.Conn, error) {
	return c.dialWS(ctx, api.RouteRoomGuestLive, token)
}

func (c *Client) dialWS(ctx context.Context, route, token string) (*websocket.Conn, error) {
	c.mu.RLock()
	mode, custom := c.mode, c.custom
	c.mu.RUnlock()
	if token == "" {
		return nil, apierr.New(apierr.Unauthorized)
	}
	u := *c.base
	u.Scheme = map[string]string{"https": "wss", "http": "ws"}[u.Scheme]
	u.Path = strings.TrimRight(u.Path, "/") + route
	h := http.Header{}
	h.Set(api.HeaderClientVersion, c.version)
	h.Set(api.HeaderInstallID, c.installID)
	h.Set(api.HeaderDeviceID, c.deviceID)
	h.Set(api.HeaderAuthorization, api.BearerPrefix+token)
	// 连接建立后一直开着：http.Client 不能设 Timeout（握手超时由 ctx 控制）
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFunc(mode, custom)
	ws, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: h, HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		if resp != nil && resp.StatusCode >= 400 {
			// 握手被拒绝：响应体已被 websocket 库读走，按状态码给出和普通请求一致的错误
			return nil, rejected(resp.StatusCode)
		}
		return nil, apperr.Wrap(apperr.ServerUnreachable, err)
	}
	ws.SetReadLimit(1 << 20)
	return ws, nil
}

func rejected(status int) *apierr.Error {
	switch status {
	case http.StatusUnauthorized:
		return apierr.New(apierr.Unauthorized)
	case http.StatusUpgradeRequired:
		return apierr.New(apierr.ClientOutdated)
	case http.StatusForbidden:
		return apierr.New(apierr.AccountDisabled)
	}
	return apperr.New(apperr.ServerUnreachable, "实时通道被拒绝：HTTP "+http.StatusText(status))
}
