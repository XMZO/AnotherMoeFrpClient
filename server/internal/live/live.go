// Package live 管理客户端的实时通道（docs/client-live.md）：每个登录的客户端会话一条 WebSocket。
//
// 面板这边的变化（配置、官方节点、限额、密钥、客户端版本）通过它马上推给客户端，管理员的远程操作（通知、
// 停止 / 重启隧道、踢下线）也走它；客户端上报的隧道状态显示在后台"客户端"页。
//
// 通道只是加速：断开时客户端照常轮询，所以这里推送失败（对方断线、发送队列满）一律丢弃，不重试。
package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
)

const (
	pingInterval = 25 * time.Second
	pingTimeout  = 20 * time.Second
	sendQueue    = 32
	writeTimeout = 10 * time.Second
)

// Client 是一个在线的客户端会话。
type Client struct {
	SessionID   string
	UserID      string
	Nickname    string
	Role        string
	DeviceID    string
	IP          string
	ConnectedAt time.Time

	mu     sync.Mutex
	hello  api.LiveHelloBody
	status api.LiveTunnelInfo
	seenAt time.Time // 最近一次收到状态

	send   chan api.LiveMessage
	cancel context.CancelFunc
}

// Info 是后台"客户端"页的一行。
type Info struct {
	SessionID   string             `json:"sessionId"`
	UserID      string             `json:"userId"`
	Nickname    string             `json:"nickname"`
	Role        string             `json:"role"`
	DeviceID    string             `json:"deviceId"`
	IP          string             `json:"ip"`
	ConnectedAt time.Time          `json:"connectedAt"`
	Version     string             `json:"version"`
	OS          string             `json:"os"`
	Hostname    string             `json:"hostname"`
	Tunnel      api.LiveTunnelInfo `json:"tunnel"`
}

func (c *Client) info() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Info{
		SessionID: c.SessionID, UserID: c.UserID, Nickname: c.Nickname, Role: c.Role, DeviceID: c.DeviceID, IP: c.IP,
		ConnectedAt: c.ConnectedAt, Version: c.hello.Version, OS: c.hello.OS, Hostname: c.hello.Hostname, Tunnel: c.status,
	}
}

// Hub 是所有在线客户端。
type Hub struct {
	log *slog.Logger
	now func() time.Time

	mu      sync.Mutex
	clients map[string]*Client // 按会话 ID；同一会话新连接顶掉旧的
}

// New 创建 Hub。
func New(log *slog.Logger, now func() time.Time) *Hub {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Hub{log: log, now: now, clients: map[string]*Client{}}
}

// Serve 处理一条已经通过认证的连接，直到断开。
func (h *Hub) Serve(ctx context.Context, ws *websocket.Conn, c *Client) {
	ws.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.cancel = cancel
	c.send = make(chan api.LiveMessage, sendQueue)
	c.ConnectedAt = h.now()

	h.mu.Lock()
	if old := h.clients[c.SessionID]; old != nil {
		old.cancel()
	}
	h.clients[c.SessionID] = c
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.clients[c.SessionID] == c {
			delete(h.clients, c.SessionID)
		}
		h.mu.Unlock()
		ws.Close(websocket.StatusNormalClosure, "")
	}()

	c.send <- api.LiveMessage{Type: api.LiveWelcome, ServerTime: h.now().Unix()}

	go h.readLoop(ctx, cancel, ws, c)
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.send:
			b, _ := json.Marshal(m)
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := ws.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				return
			}
			if m.Type == api.LiveEnded {
				return // 会话结束：说完原因就断开
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, pingTimeout)
			err := ws.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *Hub) readLoop(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn, c *Client) {
	defer cancel()
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m api.LiveMessage
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		c.mu.Lock()
		switch m.Type {
		case api.LiveHello:
			if m.Hello != nil {
				c.hello = api.LiveHelloBody{Version: clip(m.Hello.Version, 40), OS: clip(m.Hello.OS, 80), Hostname: clip(m.Hello.Hostname, 80)}
			}
		case api.LiveStatus:
			if m.Status != nil {
				st := *m.Status
				st.State, st.ProfileKind, st.ProfileName = clip(st.State, 20), clip(st.ProfileKind, 20), clip(st.ProfileName, 80)
				st.NodeName, st.OfficialID, st.Error = clip(st.NodeName, 80), clip(st.OfficialID, 64), clip(st.Error, 300)
				c.status = st
				c.seenAt = h.now()
			}
		}
		c.mu.Unlock()
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// List 返回所有在线客户端，按连接时间从新到旧。
func (h *Hub) List() []Info {
	h.mu.Lock()
	cs := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	out := make([]Info, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.info())
	}
	slices.SortFunc(out, func(a, b Info) int { return b.ConnectedAt.Compare(a.ConnectedAt) })
	return out
}

// Target 选中一部分在线客户端。各条件之间是"或"；全空表示一个都不选，All 表示全部。
type Target struct {
	All      bool     `json:"all,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Users    []string `json:"users,omitempty"`    // 用户 ID
	Sessions []string `json:"sessions,omitempty"` // 会话 ID
}

// Empty 表示没有选任何客户端。
func (t Target) Empty() bool {
	return !t.All && len(t.Roles) == 0 && len(t.Users) == 0 && len(t.Sessions) == 0
}

func (t Target) match(c *Client) bool {
	return t.All || slices.Contains(t.Roles, c.Role) || slices.Contains(t.Users, c.UserID) || slices.Contains(t.Sessions, c.SessionID)
}

// Send 给选中的客户端发一条消息，返回发出去的数量。
func (h *Hub) Send(t Target, m api.LiveMessage) int {
	return h.sendWhere(t.match, m)
}

// Broadcast 发给所有在线客户端。
func (h *Hub) Broadcast(m api.LiveMessage) int { return h.Send(Target{All: true}, m) }

// ToUser 发给一个用户的所有在线会话，except 是要跳过的会话（发起变化的那台设备自己已经知道了）。
func (h *Hub) ToUser(userID, except string, m api.LiveMessage) int {
	return h.sendWhere(func(c *Client) bool { return c.UserID == userID && c.SessionID != except }, m)
}

func (h *Hub) sendWhere(match func(*Client) bool, m api.LiveMessage) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.clients {
		if !match(c) {
			continue
		}
		select {
		case c.send <- m:
			n++
		default:
			h.log.Warn("live: send queue full, dropping", "session", c.SessionID, "type", m.Type)
		}
	}
	return n
}

// Recheck 检查每个在线会话是否还有效（被踢、改密码、账号停用之后调用），无效的告诉它原因并断开。
// valid 返回 nil 表示有效。
func (h *Hub) Recheck(ctx context.Context, valid func(ctx context.Context, sessionID string) *api.LiveEndedBody) {
	h.mu.Lock()
	cs := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	for _, c := range cs {
		if why := valid(ctx, c.SessionID); why != nil {
			select {
			case c.send <- api.LiveMessage{Type: api.LiveEnded, Ended: why}:
			default:
				c.cancel()
			}
		}
	}
}

// Count 是在线客户端数。
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
