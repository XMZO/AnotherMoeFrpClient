package services

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/model"
	"moefrp/shared/quota"
)

// logBatchInterval 是日志事件的批量推送间隔（PLAN 6.3.6）。
const logBatchInterval = 200 * time.Millisecond

// TunnelService：启停隧道、状态、测速、日志。
type TunnelService struct {
	eng      *engine.Engine
	sink     *engine.LogSink
	profiles *store.ProfileStore
	settings *store.SettingsStore

	mu       sync.Mutex
	emit     Emitter
	official []api.OfficialNode // 面板下发的官方节点（含 frps token，不直接给前端）
	tickets  TicketPool         // 官方节点的登录票据，见 SetTicketPool
	pending  []engine.LogLine
	onStatus []func(engine.Status)
	stop     chan struct{}
	once     sync.Once
	// saveDialog 弹出"另存为"对话框，返回用户选的路径，取消时返回空串。
	saveDialog func(defaultName string) (string, error)
}

// TunnelControl 是给 main 用的控制接口。单独成一个类型，是因为 TunnelService 的所有导出方法
// 都会被绑定给前端，这些方法不应该让前端调用。
type TunnelControl struct{ t *TunnelService }

// NewTunnelService 返回绑定给前端的服务和给 main 用的控制接口。
func NewTunnelService(eng *engine.Engine, sink *engine.LogSink, profiles *store.ProfileStore, settings *store.SettingsStore) (*TunnelService, *TunnelControl) {
	t := &TunnelService{eng: eng, sink: sink, profiles: profiles, settings: settings, stop: make(chan struct{})}
	sink.Subscribe(func(l engine.LogLine) {
		t.mu.Lock()
		t.pending = append(t.pending, l)
		if len(t.pending) > engine.DefaultLogLines { // 前端长时间收不到（例如还没启动），只留最新的
			t.pending = t.pending[len(t.pending)-engine.DefaultLogLines:]
		}
		t.mu.Unlock()
	})
	eng.SetHandlers(t.handleStatus, t.handleProxies)
	return t, &TunnelControl{t: t}
}

// Attach 在 Wails 应用启动之后调用：开始推送事件。
func (c *TunnelControl) Attach(emit Emitter) {
	t := c.t
	t.mu.Lock()
	t.emit = emit
	t.mu.Unlock()
	t.once.Do(func() { go t.logLoop() })
	t.handleStatus(t.eng.Status())
}

// Detach 停止推送（退出时）。
func (c *TunnelControl) Detach() {
	t := c.t
	t.mu.Lock()
	t.emit = nil
	t.mu.Unlock()
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
}

// OnStatus 注册额外的状态回调（托盘菜单用）。
func (c *TunnelControl) OnStatus(fn func(engine.Status)) {
	t := c.t
	t.mu.Lock()
	t.onStatus = append(t.onStatus, fn)
	t.mu.Unlock()
}

// SetSaveDialog 注入"另存为"对话框。
func (c *TunnelControl) SetSaveDialog(fn func(defaultName string) (string, error)) {
	c.t.mu.Lock()
	c.t.saveDialog = fn
	c.t.mu.Unlock()
}

func (t *TunnelService) handleStatus(s engine.Status) {
	t.mu.Lock()
	emit, hooks := t.emit, slices.Clone(t.onStatus)
	t.mu.Unlock()
	if emit != nil {
		emit(EventTunnelState, s)
	}
	for _, fn := range hooks {
		fn(s)
	}
}

func (t *TunnelService) handleProxies(ps []engine.ProxyStatus) {
	t.mu.Lock()
	emit := t.emit
	t.mu.Unlock()
	if emit != nil {
		emit(EventTunnelProxy, ps)
	}
}

func (t *TunnelService) logLoop() {
	tick := time.NewTicker(logBatchInterval)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
		}
		t.mu.Lock()
		batch, emit := t.pending, t.emit
		if emit != nil {
			t.pending = nil
		}
		t.mu.Unlock()
		if emit != nil && len(batch) > 0 {
			emit(EventTunnelLogs, batch)
		}
	}
}

// TicketPool 是官方节点登录票据的预取池（client/internal/tickets.Pool）。
type TicketPool interface {
	Clear()
	Retain(nodeIDs []string)
}

// SetTicketPool 设置票据池：退出登录时清空，官方节点列表变化时只保留还能用的节点的票据。
func (c *TunnelControl) SetTicketPool(p TicketPool) {
	c.t.mu.Lock()
	c.t.tickets = p
	c.t.mu.Unlock()
}

// OfficialNodeView 是给前端看的官方节点：不含地址、端口和登录用的东西，对用户是黑盒。ID 带 official- 前缀，和运行栏里的节点 ID 一致。
type OfficialNodeView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Online      bool        `json:"online"`
	Quota       quota.Quota `json:"quota"`
}

// OfficialNodes 返回面板下发给当前账号的官方节点。没登录或面板没有官方节点时为空。
// 订阅来的配置不能用官方节点（节点由分享决定），自己的云端配置和本地配置都可以用。
func (t *TunnelService) OfficialNodes() []OfficialNodeView {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]OfficialNodeView, 0, len(t.official))
	for _, o := range t.official {
		out = append(out, OfficialNodeView{ID: api.OfficialNodePrefix + o.ID, Name: o.Name, Description: o.Description,
			Online: o.Online, Quota: o.Quota})
	}
	return out
}

// SetOfficial 换上新的官方节点列表（cloudsync 拉到变化时、启动时从缓存、退出登录时传 nil）。
// 正在用官方节点运行时：节点的连接参数变了就重新连接，节点不能用了就停止。
func (c *TunnelControl) SetOfficial(list []api.OfficialNode) {
	t := c.t
	nodes := make([]model.Node, 0, len(list))
	ids := make([]string, 0, len(list))
	for _, o := range list {
		nodes = append(nodes, o.ToNode())
		ids = append(ids, o.ID)
	}
	t.mu.Lock()
	t.official = slices.Clone(list)
	emit, pool := t.emit, t.tickets
	t.mu.Unlock()
	if pool != nil {
		if list == nil {
			pool.Clear() // 退出登录、换账号
		} else {
			pool.Retain(ids)
		}
	}
	t.eng.SetExtraNodes(nodes)
	if emit != nil {
		emit(EventOfficial, t.OfficialNodes())
	}
	pid, nid, ok := t.eng.RunningProfile()
	if !ok || !strings.HasPrefix(nid, api.OfficialNodePrefix) {
		return
	}
	if cp, ok := t.profiles.Get(pid); ok {
		if _, err := t.eng.Update(cp.Profile); err != nil {
			t.sink.Appendf("warn", "official node is no longer available, stopping: %v", err)
			_ = t.eng.Stop()
		}
	}
}

// Start 用指定配置的指定节点启动隧道。已有隧道在运行时会先停掉再启动（状态先变为 stopping）。
func (t *TunnelService) Start(profileID, nodeID string) error {
	c, ok := t.profiles.Get(profileID)
	if !ok {
		return apperr.New(apperr.ProfileNotFound, profileID)
	}
	if c.IsSubscription() && strings.HasPrefix(nodeID, api.OfficialNodePrefix) {
		return apperr.New(apperr.InvalidArg, "subscribed profiles cannot use official nodes")
	}
	if c.IsSubscription() && c.Sub != nil && (c.Sub.Summary.Revoked || c.Sub.Summary.Expired) {
		return apperr.New(apperr.SubscriptionInactive, profileID)
	}
	if nodeID == "" && c.Sub != nil {
		nodeID = c.Sub.DefaultNodeID
	}
	if err := t.eng.Start(c.Profile, nodeID); err != nil {
		return apperr.Normalize(err)
	}
	_, _ = t.settings.Update(func(s *store.Settings) error {
		s.LastProfileID = profileID
		if s.LastNodeIDs == nil {
			s.LastNodeIDs = map[string]string{}
		}
		s.LastNodeIDs[profileID] = nodeID
		return nil
	})
	return nil
}

// StartLast 启动上次的配置（托盘菜单）。
func (t *TunnelService) StartLast() error {
	s := t.settings.Get()
	if s.LastProfileID == "" {
		return apperr.New(apperr.ProfileNotFound, "no last profile")
	}
	return t.Start(s.LastProfileID, s.LastNodeIDs[s.LastProfileID])
}

// Stop 发起停止，立即返回；状态通过 tunnel:state 推送（stopping → idle）。
func (t *TunnelService) Stop() error {
	return apperr.Normalize(t.eng.Stop())
}

// State 返回当前状态。
func (t *TunnelService) State() engine.Status {
	return t.eng.Status()
}

// ProxyStatuses 返回最近一次轮询到的代理状态（只读缓存，不会阻塞）。
func (t *TunnelService) ProxyStatuses() []engine.ProxyStatus {
	return t.eng.ProxyStatuses()
}

// PingNodes 并发测一份配置里所有节点的 TCP 延迟（超时 2 秒）。
func (t *TunnelService) PingNodes(profileID string) ([]engine.PingResult, error) {
	c, ok := t.profiles.Get(profileID)
	if !ok {
		return nil, apperr.New(apperr.ProfileNotFound, profileID)
	}
	nodes := slices.Clone(c.Profile.Nodes)
	if !c.IsSubscription() {
		t.mu.Lock()
		for _, o := range t.official {
			nodes = append(nodes, o.ToNode())
		}
		t.mu.Unlock()
	}
	return engine.PingNodes(context.Background(), nodes, engine.DefaultProbeTimeout), nil
}

// PingNode 测一个还没保存的节点（编辑节点时的"测试连接"）。
func (t *TunnelService) PingNode(n model.Node) engine.PingResult {
	return engine.Probe(context.Background(), n, engine.DefaultProbeTimeout)
}

// Logs 返回缓冲里的全部日志（前端首次打开时用）。之后的日志通过 tunnel:logs 推送，按 seq 去重。
func (t *TunnelService) Logs() []engine.LogLine {
	return t.sink.Lines()
}

// ClearLogs 清空日志缓冲。
func (t *TunnelService) ClearLogs() {
	t.sink.Clear()
}

// ExportLogs 把缓冲里的日志写到指定文件（UTF-8，CRLF 换行，方便记事本打开）。
func (t *TunnelService) ExportLogs(path string) error {
	if strings.TrimSpace(path) == "" {
		return apperr.New(apperr.InvalidArg, "empty path")
	}
	var b strings.Builder
	for _, l := range t.sink.Lines() {
		b.WriteString(l.Text)
		b.WriteString("\r\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return apperr.Wrap(apperr.ExportFailed, err)
	}
	return nil
}

// ExportLogsDialog 弹出"另存为"对话框并导出，返回保存的路径；用户取消时返回空串。
func (t *TunnelService) ExportLogsDialog() (string, error) {
	t.mu.Lock()
	dialog := t.saveDialog
	t.mu.Unlock()
	if dialog == nil {
		return "", apperr.New(apperr.ExportFailed, "dialog not available")
	}
	path, err := dialog("moefrp-" + time.Now().Format("20060102-150405") + ".log")
	if err != nil {
		return "", apperr.Wrap(apperr.ExportFailed, err)
	}
	if path == "" {
		return "", nil
	}
	if err := t.ExportLogs(path); err != nil {
		return "", err
	}
	return path, nil
}
