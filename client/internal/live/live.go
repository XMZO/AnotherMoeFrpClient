// Package live 维持客户端和面板之间的实时通道（docs/client-live.md）。
//
// 登录后一直连着：面板有变化（云端配置、官方节点、限额、密钥、客户端版本）时马上收到，不用等下一次轮询；
// 管理员的通知、远程停止 / 重启隧道、让设备下线也从这里来。客户端把自己的隧道状态报给面板，
// 后台"客户端"页据此显示在线设备。
//
// 通道只是加速：断开期间照常靠轮询同步，所以断线、被拒绝都只记日志，按退避重连。
package live

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/client/internal/apiclient"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// Handlers 是收到各类消息时的回调，在通道的 goroutine 上调用，不要阻塞太久。没设的忽略。
type Handlers struct {
	Sync     func()                         // 云端配置、订阅变了
	Official func()                         // 官方节点列表、限额、访问地址变了
	Tickets  func(node string)              // 登录票据作废了（node 为空表示全部）
	Config   func(api.ClientConfigResponse) // 客户端配置变了（版本、最低版本、公告）
	Notice   func(api.LiveNoticeBody)       // 管理员推送的通知
	Tunnel   func(api.LiveTunnelCommand)    // 管理员远程控制隧道
	Ended    func(api.LiveEndedBody)        // 会话被面板结束了（被踢、改密码、账号停用）
	Online   func(connected bool)           // 通道连上 / 断开
	// Vnet 是联机房间的消息，VnetPacket 是中继过来的数据包（from 是发送方的座位号，payload 不能留着用）。
	Vnet       func(api.LiveVnetBody)
	VnetPacket func(from int, payload []byte)
	Log        func(format string, args ...any) // 调试日志
}

const (
	minBackoff   = time.Second
	maxBackoff   = 30 * time.Second
	idleRecheck  = 3 * time.Second // 没登录时多久看一次令牌
	dialTimeout  = 20 * time.Second
	writeTimeout = 10 * time.Second
	// 被拒绝（会话失效、版本过低）时不要一直重试：等令牌换了或者很久以后再试
	rejectedWait = 5 * time.Minute
)

// Client 是实时通道。
type Client struct {
	api   *apiclient.Client
	hello api.LiveHelloBody
	h     Handlers

	mu       sync.Mutex
	status   *api.LiveTunnelInfo
	conn     *websocket.Conn
	cancel   context.CancelFunc
	token    string // 当前连接用的令牌
	wake     chan struct{}
	statusCh chan struct{}
}

// New 创建实时通道，Run 之后开始工作。
func New(c *apiclient.Client, hello api.LiveHelloBody, h Handlers) *Client {
	return &Client{api: c, hello: hello, h: h, wake: make(chan struct{}, 1), statusCh: make(chan struct{}, 1)}
}

func (c *Client) logf(format string, args ...any) {
	if c.h.Log != nil {
		c.h.Log(format, args...)
	}
}

// Wake 在登录、退出登录、换代理后调用：令牌变了就断开旧连接，马上按新令牌重连。
func (c *Client) Wake() {
	c.mu.Lock()
	if c.cancel != nil && c.token != c.api.Token() {
		c.cancel()
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Reconnect 断开当前连接马上重连（例如代理设置变了）。
func (c *Client) Reconnect() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.Wake()
}

// SetStatus 更新要上报的隧道状态；连着的话马上发出去（连续变化只发最后一个）。
func (c *Client) SetStatus(st api.LiveTunnelInfo) {
	c.mu.Lock()
	c.status = &st
	c.mu.Unlock()
	select {
	case c.statusCh <- struct{}{}:
	default:
	}
}

// Run 一直运行到 stop 关闭。
func (c *Client) Run(stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stop
		cancel()
	}()
	backoff := minBackoff
	for ctx.Err() == nil {
		token := c.api.Token()
		if token == "" {
			c.sleep(ctx, idleRecheck)
			continue
		}
		start := time.Now()
		err := c.session(ctx, token)
		if ctx.Err() != nil {
			return
		}
		var ae *apierr.Error
		if errors.As(err, &ae) && (ae.Code == apierr.Unauthorized || ae.Code == apierr.ClientOutdated || ae.Code == apierr.AccountDisabled) {
			// 普通请求会处理下线、版本过低；这里等令牌换了（Wake）再说
			c.logf("live: rejected: %v", err)
			c.sleep(ctx, rejectedWait)
			backoff = minBackoff
			continue
		}
		if time.Since(start) > time.Minute {
			backoff = minBackoff // 连了一阵子才断的：很快重连
		}
		if err != nil {
			c.logf("live: disconnected: %v (retry in %s)", err, backoff)
		}
		c.sleep(ctx, backoff+time.Duration(rand.Int64N(int64(backoff/2)+1)))
		backoff = min(backoff*2, maxBackoff)
	}
}

// sleep 等 d，或者被 Wake 叫醒。
func (c *Client) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-c.wake:
	}
}

// session 建立一条连接，直到断开。
func (c *Client) session(parent context.Context, token string) error {
	dctx, dcancel := context.WithTimeout(parent, dialTimeout)
	ws, err := c.api.DialLive(dctx)
	dcancel()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	c.mu.Lock()
	c.conn, c.cancel, c.token = ws, cancel, token
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn, c.cancel, c.token = nil, nil, ""
		c.mu.Unlock()
		ws.CloseNow()
		if c.h.Online != nil {
			c.h.Online(false)
		}
	}()
	if c.h.Online != nil {
		c.h.Online(true)
	}
	hello := c.hello
	if err := c.write(ctx, ws, api.LiveMessage{Type: api.LiveHello, Hello: &hello}); err != nil {
		return err
	}
	c.sendStatus(ctx, ws)

	// 写：状态有变化就发
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.statusCh:
				if err := c.sendStatus(ctx, ws); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		typ, b, err := ws.Read(ctx)
		if err != nil {
			if ctx.Err() != nil && parent.Err() == nil {
				return nil // 主动断开（令牌变了、换代理）
			}
			return err
		}
		if typ == websocket.MessageBinary {
			if len(b) > 1 && c.h.VnetPacket != nil {
				c.h.VnetPacket(int(b[0]), b[1:])
			}
			continue
		}
		var m api.LiveMessage
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		c.dispatch(m)
		if m.Type == api.LiveEnded {
			return nil
		}
	}
}

// SendVnet 发一条联机房间消息；通道没连上时返回错误（连上后由 Online 回调重新进房间）。
func (c *Client) SendVnet(b api.LiveVnetBody) error {
	c.mu.Lock()
	ws := c.conn
	c.mu.Unlock()
	if ws == nil {
		return errNotConnected
	}
	return c.write(context.Background(), ws, api.LiveMessage{Type: api.LiveVnet, Vnet: &b})
}

// SendPacket 经面板中继一个数据包给房间里座位号为 to 的成员。
func (c *Client) SendPacket(to int, payload []byte) error {
	c.mu.Lock()
	ws := c.conn
	c.mu.Unlock()
	if ws == nil {
		return errNotConnected
	}
	b := make([]byte, 1+len(payload))
	b[0] = byte(to)
	copy(b[1:], payload)
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return ws.Write(ctx, websocket.MessageBinary, b)
}

var errNotConnected = errors.New("live channel not connected")

func (c *Client) sendStatus(ctx context.Context, ws *websocket.Conn) error {
	c.mu.Lock()
	st := c.status
	c.mu.Unlock()
	if st == nil {
		return nil
	}
	return c.write(ctx, ws, api.LiveMessage{Type: api.LiveStatus, Status: st})
}

func (c *Client) write(ctx context.Context, ws *websocket.Conn, m api.LiveMessage) error {
	b, _ := json.Marshal(m)
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, b)
}

func (c *Client) dispatch(m api.LiveMessage) {
	switch m.Type {
	case api.LiveSync:
		if c.h.Sync != nil {
			c.h.Sync()
		}
	case api.LiveOfficial:
		if c.h.Official != nil {
			c.h.Official()
		}
	case api.LiveTickets:
		if c.h.Tickets != nil {
			c.h.Tickets(m.Node)
		}
	case api.LiveConfig:
		if c.h.Config != nil && m.Config != nil {
			c.h.Config(*m.Config)
		}
	case api.LiveNotice:
		if c.h.Notice != nil && m.Notice != nil {
			c.h.Notice(*m.Notice)
		}
	case api.LiveTunnel:
		if c.h.Tunnel != nil && m.Tunnel != nil {
			c.h.Tunnel(*m.Tunnel)
		}
	case api.LiveEnded:
		if c.h.Ended != nil && m.Ended != nil {
			c.h.Ended(*m.Ended)
		}
	case api.LiveVnet:
		if c.h.Vnet != nil && m.Vnet != nil {
			c.h.Vnet(*m.Vnet)
		}
	}
}
