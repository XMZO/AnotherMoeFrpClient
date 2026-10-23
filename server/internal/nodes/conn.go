package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"moefrp/server/internal/store"
	"moefrp/shared/agentproto"
	"moefrp/shared/apierr"
)

// agentConn 是一个在线 agent 的连接。
type agentConn struct {
	nodeID      string
	ws          *websocket.Conn
	ip          string
	connectedAt time.Time
	hello       agentproto.Hello

	seq     atomic.Int64
	mu      sync.Mutex
	pending map[string]chan agentproto.Message
}

// liveState 是节点最近一次上报，只放内存。
type liveState struct {
	report         agentproto.Report
	at             time.Time
	rxRate, txRate float64
	touchedAt      time.Time // 最后一次写 last_seen_at
}

func (c *agentConn) send(ctx context.Context, m agentproto.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

// Online 判断节点是否在线。
func (s *Service) Online(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[id] != nil
}

func (s *Service) disconnect(id, reason string) {
	s.mu.Lock()
	c := s.conns[id]
	delete(s.conns, id)
	s.mu.Unlock()
	if c != nil {
		c.ws.Close(websocket.StatusPolicyViolation, reason)
	}
}

// Call 向节点发请求并等待响应。节点不在线时返回 node_offline，节点报错时返回 node_failed。
func (s *Service) Call(ctx context.Context, id, method string, req any, out any, timeout time.Duration) error {
	s.mu.Lock()
	c := s.conns[id]
	s.mu.Unlock()
	if c == nil {
		return apierr.New(apierr.NodeOffline)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	mid := fmt.Sprintf("p%d", c.seq.Add(1))
	ch := make(chan agentproto.Message, 1)
	c.mu.Lock()
	c.pending[mid] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, mid)
		c.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.send(ctx, agentproto.Message{ID: mid, Type: agentproto.TypeRequest, Method: method, Data: raw}); err != nil {
		return apierr.Newf(apierr.NodeOffline, "发送到节点失败：%v", err)
	}
	select {
	case <-ctx.Done():
		return apierr.Newf(apierr.NodeFailed, "节点没有在 %s 内响应", timeout)
	case m, ok := <-ch:
		if !ok {
			return apierr.Newf(apierr.NodeOffline, "节点在执行过程中断开了")
		}
		if m.Error != nil {
			return apierr.Newf(apierr.NodeFailed, "%s", m.Error.Message)
		}
		if out != nil && len(m.Data) > 0 {
			return json.Unmarshal(m.Data, out)
		}
		return nil
	}
}

// Serve 处理一个已认证的 agent 连接，直到连接断开。
func (s *Service) Serve(ctx context.Context, ws *websocket.Conn, node store.Node, ip string) {
	ws.SetReadLimit(8 << 20)
	c := &agentConn{nodeID: node.ID, ws: ws, ip: ip, connectedAt: s.now(), pending: map[string]chan agentproto.Message{}}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		s.mu.Lock()
		if s.conns[node.ID] == c {
			delete(s.conns, node.ID)
		}
		s.mu.Unlock()
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		_ = store.TouchNode(context.Background(), s.st.DB(), node.ID, s.now().Unix())
		ws.CloseNow()
	}()

	greeted := false
	for {
		// agent 每 10 秒上报一次；超过 OfflineAfter 收不到任何消息就断开
		rctx, rcancel := context.WithTimeout(ctx, agentproto.OfflineAfter*time.Second)
		_, b, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			if greeted {
				s.log.Info("node disconnected", "node", node.ID, "err", err)
			}
			return
		}
		var m agentproto.Message
		if err := json.Unmarshal(b, &m); err != nil {
			s.log.Warn("node: bad message", "node", node.ID, "err", err)
			continue
		}
		switch {
		case m.Type == agentproto.TypeRequest && m.Method == agentproto.MethodHello:
			if err := s.handleHello(ctx, c, node, m); err != nil {
				s.log.Warn("node hello failed", "node", node.ID, "err", err)
				return
			}
			greeted = true
		case !greeted:
			return // 第一条必须是 hello
		case m.Type == agentproto.TypeEvent && m.Method == agentproto.MethodReport:
			var r agentproto.Report
			if json.Unmarshal(m.Data, &r) == nil {
				s.recordReport(node.ID, r)
			}
		case m.Type == agentproto.TypeResponse:
			c.mu.Lock()
			ch := c.pending[m.ID]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		}
	}
}

func (s *Service) handleHello(ctx context.Context, c *agentConn, node store.Node, m agentproto.Message) error {
	var h agentproto.Hello
	if err := json.Unmarshal(m.Data, &h); err != nil {
		return err
	}
	c.hello = h
	now := s.now()
	info, _ := json.Marshal(h)
	if err := store.SetNodeSeen(ctx, s.st.DB(), node.ID, string(info), c.ip, now.Unix()); err != nil {
		return err
	}
	if err := store.SetNodeApplied(ctx, s.st.DB(), node.ID, h.AppliedRev, node.ApplyError); err != nil {
		return err
	}
	// 新连接顶掉旧连接
	s.mu.Lock()
	old := s.conns[node.ID]
	s.conns[node.ID] = c
	s.mu.Unlock()
	if old != nil && old != c {
		old.ws.Close(websocket.StatusPolicyViolation, "replaced by a new connection")
	}
	ack, _ := json.Marshal(agentproto.HelloAck{NodeID: node.ID, Name: node.Name})
	if err := c.send(ctx, agentproto.Message{ID: m.ID, Type: agentproto.TypeResponse, Method: m.Method, Data: ack}); err != nil {
		return err
	}
	s.log.Info("node connected", "node", node.ID, "name", node.Name, "version", h.AgentVersion, "ip", c.ip)

	if h.Upgrade != nil {
		s.finishUpgrade(node, h.Upgrade)
	} else {
		// 升级后重连时一定带结果；没带说明结果丢了（例如升级包没生效）
		_ = store.FinishRunningUpgrade(ctx, s.st.DB(), node.ID, "failed", "", "节点重新连接时没有报告升级结果", now.Unix())
	}
	go s.afterHello(node.ID, h)
	return nil
}

func (s *Service) finishUpgrade(node store.Node, r *agentproto.UpgradeResult) {
	status, out := "done", fmt.Sprintf("%s → %s 升级成功", r.From, r.To)
	if !r.OK {
		status, out = "failed", fmt.Sprintf("%s → %s 升级失败", r.From, r.To)
		if r.RolledBack {
			out += "，已回滚到 " + r.From
		}
	}
	ctx := context.Background()
	_ = store.FinishRunningUpgrade(ctx, s.st.DB(), node.ID, status, out, r.Error, s.now().Unix())
}

// afterHello：节点上线后执行待卸载，或者补发没同步的配置。
func (s *Service) afterHello(id string, h agentproto.Hello) {
	ctx := context.Background()
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return
	}
	if n.PendingUninstall {
		if err := s.uninstallNow(ctx, id); err != nil {
			s.log.Error("pending uninstall failed", "node", id, "err", err)
		}
		return
	}
	// 节点已经拒绝过的版本（apply_error 不为空）不自动重发：每次重发都会让 frps 重启再回滚。
	// 管理员保存新配置时 apply_error 会被清空
	if n.FrpsConfig != "" && n.ConfigRev != h.AppliedRev && n.ApplyError == "" {
		res, err := s.push(ctx, id, n.ConfigRev, n.FrpsConfig)
		switch {
		case err != nil:
			s.log.Warn("push config failed", "node", id, "err", err)
		case !res.Applied:
			s.log.Warn("node rejected config", "node", id, "rev", n.ConfigRev, "err", res.Error)
		}
	}
}

func (s *Service) recordReport(id string, r agentproto.Report) {
	now := s.now()
	s.mu.Lock()
	l := s.live[id]
	if l == nil {
		l = &liveState{}
		s.live[id] = l
	}
	if !l.at.IsZero() {
		if dt := now.Sub(l.at).Seconds(); dt > 0 && r.NetRx >= l.report.NetRx && r.NetTx >= l.report.NetTx {
			l.rxRate = float64(r.NetRx-l.report.NetRx) / dt
			l.txRate = float64(r.NetTx-l.report.NetTx) / dt
		}
	}
	l.report, l.at = r, now
	touch := now.Sub(l.touchedAt) > time.Minute
	if touch {
		l.touchedAt = now
	}
	s.mu.Unlock()
	if touch {
		if err := store.TouchNode(context.Background(), s.st.DB(), id, now.Unix()); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("touch node", "node", id, "err", err)
		}
	}
}
