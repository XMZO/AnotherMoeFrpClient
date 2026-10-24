// Package link 是 agent 到面板的 WebSocket 连接：注册 hello、周期上报、处理面板的请求，断线自动重连。
package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/agentproto"
)

// Handler 处理面板发来的请求，返回的值作为响应数据。返回 *agentproto.Error 时原样回给面板。
type Handler func(ctx context.Context, method string, data json.RawMessage) (any, error)

// Options 是连接参数。
type Options struct {
	Server  string // https://frp.example.com
	Token   string
	Hello   func() agentproto.Hello
	Report  func() agentproto.Report
	Handle  Handler
	OnReady func(agentproto.HelloAck) // hello 被接受后调用（每次连上都会调用）
	Log     *slog.Logger
}

// ErrRejected 表示面板拒绝了令牌（节点被删除或令牌被重置）。
var ErrRejected = errors.New("面板拒绝了这台节点的令牌")

// Run 一直保持连接直到 ctx 结束。令牌被拒绝时降低重试频率，但不退出：
// 退出会被 systemd 重新拉起，没有意义；frps 继续按最后的配置运行。
func Run(ctx context.Context, o Options) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := session(ctx, o)
		if ctx.Err() != nil {
			return
		}
		wait := backoff
		switch {
		case errors.Is(err, ErrRejected):
			o.Log.Error("面板拒绝了令牌：节点可能已在面板上删除或重置了令牌。frps 继续运行，1 小时后重试；要重新注册请执行 install --force")
			wait = time.Hour
		default:
			o.Log.Warn("与面板的连接断开", "err", err, "retry_in", wait.Round(time.Second))
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second // 连上过一段时间再断的，从头开始退避
		} else if backoff < time.Minute {
			backoff *= 2
		}
		wait += time.Duration(rand.Int64N(int64(wait/4) + 1)) // 抖动，避免所有节点同时重连
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func wsURL(server string) string {
	if rest, ok := strings.CutPrefix(server, "https://"); ok {
		return "wss://" + rest + agentproto.RouteWS
	}
	return "ws://" + strings.TrimPrefix(server, "http://") + agentproto.RouteWS
}

type conn struct {
	ws      *websocket.Conn
	seq     atomic.Int64
	mu      sync.Mutex
	pending map[string]chan agentproto.Message
}

func (c *conn) send(ctx context.Context, m agentproto.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *conn) call(ctx context.Context, method string, data any, out any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("a%d", c.seq.Add(1))
	ch := make(chan agentproto.Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(ctx, agentproto.Message{ID: id, Type: agentproto.TypeRequest, Method: method, Data: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Data, out)
		}
		return nil
	}
}

// dialClient 是连面板用的 HTTP 客户端：TLS 握手放宽到 30 秒（默认 10 秒），丢包严重的跨境线路也能连上。
var dialClient = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSHandshakeTimeout = 30 * time.Second
	return &http.Client{Transport: t}
}()

func session(ctx context.Context, o Options) error {
	dctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, wsURL(o.Server), &websocket.DialOptions{
		HTTPClient:   dialClient,
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + o.Token}},
		Subprotocols: []string{agentproto.Subprotocol},
	})
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return ErrRejected
		}
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(8 << 20)

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	c := &conn{ws: ws, pending: map[string]chan agentproto.Message{}}
	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(ctx, o) }()

	var ack agentproto.HelloAck
	hctx, hcancel := context.WithTimeout(ctx, 45*time.Second)
	err = c.call(hctx, agentproto.MethodHello, o.Hello(), &ack)
	hcancel()
	if err != nil {
		return fmt.Errorf("hello：%w", err)
	}
	o.Log.Info("已连接面板", "server", o.Server, "node", ack.Name)
	if o.OnReady != nil {
		o.OnReady(ack)
	}

	report := func() error {
		raw, _ := json.Marshal(o.Report())
		return c.send(ctx, agentproto.Message{Type: agentproto.TypeEvent, Method: agentproto.MethodReport, Data: raw})
	}
	if err := report(); err != nil {
		return err
	}
	// 心跳单独跑，等回应时不耽误上报。面板一直没响应（例如中间的反代断了但 TCP 没断）时断开重连
	pingErr := make(chan error, 1)
	go func() { pingErr <- pingLoop(ctx, ws) }()

	tick := time.NewTicker(agentproto.ReportInterval * time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-readErr:
			return err
		case err := <-pingErr:
			return err
		case <-ctx.Done():
			ws.Close(websocket.StatusGoingAway, "agent stopping")
			return ctx.Err()
		case <-tick.C:
			if err := report(); err != nil {
				return err
			}
		}
	}
}

// pingLoop 每 PingInterval 秒 ping 一次，PingTimeout 秒内没有回应就返回错误。ctx 结束时返回 nil。
func pingLoop(ctx context.Context, ws *websocket.Conn) error {
	t := time.NewTicker(agentproto.PingInterval * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, agentproto.PingTimeout*time.Second)
		err := ws.Ping(pctx)
		cancel()
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("ping：%w", err)
		}
	}
}

func (c *conn) readLoop(ctx context.Context, o Options) error {
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		var m agentproto.Message
		if err := json.Unmarshal(b, &m); err != nil {
			o.Log.Warn("面板发来的消息无法解析", "err", err)
			continue
		}
		switch m.Type {
		case agentproto.TypeResponse:
			c.mu.Lock()
			ch := c.pending[m.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case agentproto.TypeRequest:
			go c.serve(ctx, o, m)
		}
	}
}

func (c *conn) serve(ctx context.Context, o Options, m agentproto.Message) {
	res := agentproto.Message{ID: m.ID, Type: agentproto.TypeResponse, Method: m.Method}
	v, err := func() (v any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		return o.Handle(ctx, m.Method, m.Data)
	}()
	if err != nil {
		var pe *agentproto.Error
		if !errors.As(err, &pe) {
			pe = &agentproto.Error{Code: agentproto.ErrFailed, Message: err.Error()}
		}
		res.Error = pe
	} else if v != nil {
		res.Data, err = json.Marshal(v)
		if err != nil {
			res.Error = &agentproto.Error{Code: agentproto.ErrFailed, Message: err.Error()}
		}
	}
	if err := c.send(ctx, res); err != nil {
		o.Log.Warn("回复面板失败", "method", m.Method, "err", err)
	}
}
