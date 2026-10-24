package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// goneState 是刚断开的节点：宽限期内重连上的话，管理后台不显示离线，上线时间也接着算。
type goneState struct {
	at          time.Time
	connectedAt time.Time
	hello       agentproto.Hello
}

// graceLocked 返回还在宽限期内的断开记录。调用方持有 s.mu。
func (s *Service) graceLocked(id string) *goneState {
	g := s.gone[id]
	if g == nil || s.now().Sub(g.at) >= agentproto.OfflineGrace*time.Second {
		return nil
	}
	return g
}

// dropsLocked 返回最近 24 小时的断线次数，顺便清掉更早的记录。调用方持有 s.mu。
func (s *Service) dropsLocked(id string) int {
	cut := s.now().Add(-24 * time.Hour)
	d := s.drops[id]
	i := 0
	for i < len(d) && d[i].Before(cut) {
		i++
	}
	if i > 0 {
		d = d[i:]
		if len(d) == 0 {
			delete(s.drops, id)
		} else {
			s.drops[id] = d
		}
	}
	return len(d)
}

// liveState 是节点最近一次上报，只放内存。
type liveState struct {
	report         agentproto.Report
	at             time.Time
	rxRate, txRate float64
	conn           *agentConn // 上一份 report 来自哪条连接：换了连接（重连、升级）就重新开始算速率
	touchedAt      time.Time  // 最后一次写 last_seen_at
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

// Online 判断节点是否在线（刚断开、还在宽限期内的也算，见 OfflineGrace）。
func (s *Service) Online(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[id] != nil || s.graceLocked(id) != nil
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
	greeted := false // 收到 hello 之后才算上线过，断开时才记入断线
	defer func() {
		s.mu.Lock()
		if s.conns[node.ID] == c {
			delete(s.conns, node.ID)
			delete(s.pushed, node.ID)
			if greeted {
				s.gone[node.ID] = &goneState{at: s.now(), connectedAt: c.connectedAt, hello: c.hello}
				s.drops[node.ID] = append(s.drops[node.ID], s.now())
				s.offlineLater()
			}
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

	for {
		// agent 每 ReportInterval 秒上报一次；超过 OfflineAfter 收不到任何消息就断开
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
				s.recordReport(node.ID, c, r)
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
	// 机器指纹：第一次上线时记下；之后对不上说明令牌被拷到了别的机器上（或者重装了系统），拒绝
	if h.MachineID != "" {
		switch node.MachineID {
		case "":
			if err := store.SetNodeMachineID(ctx, s.st.DB(), node.ID, h.MachineID); err != nil {
				return err
			}
		case h.MachineID:
		default:
			_ = c.send(ctx, agentproto.Message{ID: m.ID, Type: agentproto.TypeResponse, Method: m.Method, Error: &agentproto.Error{
				Code:    agentproto.ErrMachineMismatch,
				Message: "这个节点的令牌是在另一台机器上注册的（机器指纹不同）。重装了系统或换了机器的话，请在面板上重置令牌，再用新的安装命令加 --force 重新安装",
			}})
			s.log.Warn("node machine id mismatch", "node", node.ID, "ip", c.ip)
			return errors.New("machine id mismatch")
		}
	}
	info, _ := json.Marshal(h)
	if err := store.SetNodeSeen(ctx, s.st.DB(), node.ID, string(info), c.ip, now.Unix()); err != nil {
		return err
	}
	if err := store.SetNodeApplied(ctx, s.st.DB(), node.ID, h.AppliedRev, node.ApplyError); err != nil {
		return err
	}
	// 新连接顶掉旧连接；宽限期内重连上的，上线时间接着算
	s.mu.Lock()
	old := s.conns[node.ID]
	if g := s.graceLocked(node.ID); g != nil && old == nil {
		c.connectedAt = g.connectedAt
	}
	delete(s.gone, node.ID)
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
	s.officialChanged()
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
	// 先下发存储租约（节点要用它解开、加密本地存储），再推授权表（frps 的准入插件要用），最后补发配置
	if err := s.pushLease(ctx, id, true); err != nil {
		s.log.Warn("push store lease failed", "node", id, "err", err)
	}
	if err := s.syncAccess(ctx, id, true); err != nil {
		s.log.Warn("sync node access failed", "node", id, "err", err)
	}
	// 旧 agent（不支持结构化配置）不补发：它照旧用自己的 TOML，升级后再下发。
	// 节点已经拒绝过的版本（apply_error 不为空）不自动重发：每次重发都会让 frps 重启再回滚。
	// 管理员保存新配置时 apply_error 会被清空
	if hasFeature(&h, agentproto.FeatureSettings) && n.ConfigRev != h.AppliedRev && n.ApplyError == "" {
		res, err := s.push(ctx, id, n.ConfigRev, settingsOf(n))
		switch {
		case err != nil:
			s.log.Warn("push config failed", "node", id, "err", err)
		case !res.Applied:
			s.log.Warn("node rejected config", "node", id, "rev", n.ConfigRev, "err", res.Error)
		}
	}
}

// minRateInterval：两次采样至少隔这么久才算速率。旧版 agent 没有采样时间，只能用面板收到的时间差，
// 网络抖动时两次上报会挤在一起到达，时间差太小就跳过，保留上一次的速率。
const minRateInterval = 1.5

func (s *Service) recordReport(id string, c *agentConn, r agentproto.Report) {
	now := s.now()
	s.mu.Lock()
	l := s.live[id]
	if l == nil {
		l = &liveState{}
		s.live[id] = l
	}
	if l.conn != c {
		// 新连接（重连、升级）：上一份 report 不能当基准，速率先归零，下一次再算
		l.rxRate, l.txRate = 0, 0
	} else if !l.at.IsZero() && r.NetRx >= l.report.NetRx && r.NetTx >= l.report.NetTx {
		dt := now.Sub(l.at).Seconds()
		if r.At > 0 && l.report.At > 0 {
			dt = float64(r.At-l.report.At) / 1000 // agent 的采样时间差，不受网络抖动影响
		}
		if dt >= minRateInterval {
			l.rxRate = float64(r.NetRx-l.report.NetRx) / dt
			l.txRate = float64(r.NetTx-l.report.NetTx) / dt
		}
	}
	l.report, l.at, l.conn = r, now, c
	// 出口公网地址是 agent 连上之后才探测出来的（也可能变）：和 hello 里的不一样就更新节点信息，访问地址跟着变
	var info []byte
	if len(r.PublicIPs) > 0 && !slices.Equal(r.PublicIPs, c.hello.PublicIPs) {
		c.hello.PublicIPs = slices.Clone(r.PublicIPs)
		info, _ = json.Marshal(c.hello)
	}
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
	if info != nil {
		if err := store.SetNodeInfo(context.Background(), s.st.DB(), id, string(info)); err != nil {
			s.log.Warn("save node public ips", "node", id, "err", err)
			return
		}
		s.log.Info("node public ips changed", "node", id, "ips", r.PublicIPs)
		s.officialChanged() // 官方节点的访问地址可能变了，推给客户端
	}
}
