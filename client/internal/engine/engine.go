// Package engine 在进程内运行 frp client（PLAN 6.3）。
//
// 同一时间只运行一个 frp Service，也就是一个配置加一个节点（PLAN 6.3.7）。
//
// 锁的规则（违反会死锁）：
//   - e.mu 只保护状态字段。持有 e.mu 时不能调用任何 frp 方法，也不能写日志 sink：
//     frp 打日志时会同步回调 onLog，而 onLog 要拿 e.mu。
//   - e.ctl 串行化所有控制操作（启动、停止、热更新、Run 返回后的收尾），持有它时可以调用 frp。
//     日志回调和状态轮询都不拿 e.ctl。
package engine

import (
	"context"
	"fmt"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	frpclient "github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/xlog"

	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// State 是隧道状态机的状态（PLAN 6.3.4）。
type State string

const (
	StateIdle         State = "idle"
	StateConnecting   State = "connecting"
	StateRunning      State = "running"
	StateReconnecting State = "reconnecting"
	StateFailed       State = "failed"
	StateStopping     State = "stopping"
)

// Status 是引擎对外的状态快照，也是 tunnel:state 事件的内容。
type Status struct {
	State       State         `json:"state"`
	ProfileID   string        `json:"profileId,omitempty"`
	ProfileName string        `json:"profileName,omitempty"`
	NodeID      string        `json:"nodeId,omitempty"`
	NodeLabel   string        `json:"nodeLabel,omitempty"`
	RunID       string        `json:"runId,omitempty"` // frps 分配的 run id，热更新时不变
	Logins      int           `json:"logins"`          // 本次运行登录成功的次数，重连会加 1
	Error       *apierr.Error `json:"error,omitempty"` // Failed 的原因；Reconnecting 时是最近一次重连失败的原因
	Pending     bool          `json:"pending"`         // Stopping 结束后会启动另一份配置（换节点、重启）
	StartedAt   string        `json:"startedAt,omitempty"`
	ConnectedAt string        `json:"connectedAt,omitempty"`
}

// Busy 表示正在停止或切换，界面上启停按钮不可点。
func (s Status) Busy() bool { return s.State == StateStopping }

// Active 表示有 Service 在运行（含连接中、重连中）。
func (s Status) Active() bool {
	return s.State == StateConnecting || s.State == StateRunning || s.State == StateReconnecting
}

// ProxyStatus 是一个代理在 frp 里的运行状态（tunnel:proxy 事件的元素）。
type ProxyStatus struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	Phase      string      `json:"phase"` // frp 的 Phase：new / wait start / start error / running / check failed / closed；空表示还没注册
	Err        string      `json:"err,omitempty"`
	Code       apierr.Code `json:"code,omitempty"` // Err 的分类，前端据此显示中文原因
	RemoteAddr string      `json:"remoteAddr,omitempty"`
}

// UpdateMode 是热更新的结果。
type UpdateMode string

const (
	UpdateNone    UpdateMode = "none"    // 没有运行中的隧道，或不是正在运行的配置
	UpdateHot     UpdateMode = "hot"     // UpdateConfigSource，连接不断
	UpdateRestart UpdateMode = "restart" // 节点连接参数变了，停掉再重新启动
)

type Options struct {
	StopGrace    time.Duration // 用户点停止时 GracefulClose 的时长
	PollInterval time.Duration // 代理状态轮询间隔
	ProbeTimeout time.Duration // 连接失败时 TCP 探测的超时
	// Tickets 给官方节点的每次登录提供登录票据（client/internal/tickets.Pool）。为空时官方节点登录不带票据。
	Tickets TicketSource
	// DeviceID 是设备指纹（client/internal/device），官方节点登录时和票据一起带上，节点核对票据里的设备绑定。
	DeviceID string
}

// TicketSource 提供官方节点的登录票据：Peek 拿一张但不取走，连接建立、登录消息发出后再 Used。
type TicketSource interface {
	Peek(ctx context.Context, nodeID string) (string, error)
	Used(nodeID, ticket string)
}

func (o *Options) complete() {
	if o.StopGrace <= 0 {
		o.StopGrace = 500 * time.Millisecond
	}
	if o.PollInterval <= 0 {
		o.PollInterval = time.Second
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = DefaultProbeTimeout
	}
}

var (
	reLoginSuccess = regexp.MustCompile(`login to server success, get run id \[([^\]]*)\]`)
	reConnectErr   = regexp.MustCompile(`connect to server error: (.*)$`)
	reLoginFailed  = regexp.MustCompile(`^login to the server failed: (.*?)(\. With loginFailExit enabled.*)?$`)
)

// runSeq 给每个 Service 编号。必须是进程级的：日志出口是全局的，多个 Engine 的编号也不能重复。
var runSeq atomic.Uint64

// run 是一次 frp Service 的生命周期。
type run struct {
	gen    uint64
	tag    string // 日志前缀 [run-N]，用来从全局日志里认出这个 Service 的行
	svc    *frpclient.Service
	common *v1.ClientCommonConfig
	cancel context.CancelFunc
	done   chan struct{}
	poke   chan struct{} // 立即轮询一次代理状态

	// 以下字段由 e.mu 保护
	profile  model.Profile
	node     model.Node
	stopping bool // 用户主动停止（或为了切换而停止），Run 返回的错误不算登录失败
	loggedIn bool
	probing  bool
	runErr   error
}

// Engine 见包注释。
type Engine struct {
	sink *LogSink
	opts Options

	ctl sync.Mutex

	mu        sync.Mutex
	gen       uint64
	cur       *run
	pending   *Prepared // 当前 Service 停止后要启动的配置
	status    Status
	proxies   []ProxyStatus
	onStatus  func(Status)
	onProxies func([]ProxyStatus)
	lastState State
	history   []Transition

	extra []model.Node // 配置之外也能选的节点（官方节点），见 SetExtraNodes

	statusCh chan struct{}
	proxyCh  chan struct{}
	closed   chan struct{}
	unsub    func()
}

// New 创建引擎。sink 必须已经通过 InstallGlobal 装到 frp 的全局 Logger 上。
func New(sink *LogSink, opts Options) *Engine {
	opts.complete()
	e := &Engine{
		sink:      sink,
		opts:      opts,
		status:    Status{State: StateIdle},
		lastState: StateIdle,
		statusCh:  make(chan struct{}, 1),
		proxyCh:   make(chan struct{}, 1),
		closed:    make(chan struct{}),
	}
	e.unsub = sink.Subscribe(e.onLog)
	go e.deliver()
	return e
}

// Close 释放引擎自己的 goroutine 和日志订阅，不会停止隧道（先调用 Shutdown）。
func (e *Engine) Close() {
	e.unsub()
	close(e.closed)
}

// SetHandlers 设置状态变化和代理状态变化的回调。回调在同一个 goroutine 上按顺序执行，
// 多次变化可能合并成一次，回调拿到的总是最新的快照。
func (e *Engine) SetHandlers(onStatus func(Status), onProxies func([]ProxyStatus)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onStatus, e.onProxies = onStatus, onProxies
	e.notifyLocked()
	e.notifyProxiesLocked()
}

func (e *Engine) deliver() {
	for {
		select {
		case <-e.closed:
			return
		case <-e.statusCh:
			e.mu.Lock()
			cb, s := e.onStatus, e.status
			e.mu.Unlock()
			if cb != nil {
				safeCall(e.sink, "status handler", func() { cb(s) })
			}
		case <-e.proxyCh:
			e.mu.Lock()
			cb, ps := e.onProxies, slices.Clone(e.proxies)
			e.mu.Unlock()
			if cb != nil {
				if ps == nil {
					ps = []ProxyStatus{}
				}
				safeCall(e.sink, "proxy handler", func() { cb(ps) })
			}
		}
	}
}

// Transition 是一次状态变化，Transitions() 保留最近 64 次，供测试和诊断使用。
type Transition struct {
	From State     `json:"from"`
	To   State     `json:"to"`
	At   time.Time `json:"at"`
}

// Transitions 返回最近的状态变化（不会因为事件合并而漏掉中间状态）。
func (e *Engine) Transitions() []Transition {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.history)
}

// notifyLocked 在状态改动之后调用（持有 e.mu）：记录状态变化，并通知 deliver goroutine。
func (e *Engine) notifyLocked() {
	if e.status.State != e.lastState {
		e.history = append(e.history, Transition{From: e.lastState, To: e.status.State, At: time.Now()})
		if len(e.history) > 64 {
			e.history = slices.Delete(e.history, 0, len(e.history)-64)
		}
		e.lastState = e.status.State
	}
	select {
	case e.statusCh <- struct{}{}:
	default:
	}
}

func (e *Engine) notifyProxiesLocked() {
	select {
	case e.proxyCh <- struct{}{}:
	default:
	}
}

// Status 返回当前状态，不会阻塞在 frp 上。
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// ProxyStatuses 返回最近一次轮询到的代理状态。只读缓存，Stopping 期间也不会阻塞（坑 7）。
func (e *Engine) ProxyStatuses() []ProxyStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := slices.Clone(e.proxies)
	if out == nil {
		out = []ProxyStatus{}
	}
	return out
}

// RunningProfile 返回正在运行（含连接中、停止中）的配置和节点。
func (e *Engine) RunningProfile() (profileID, nodeID string, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur == nil {
		return "", "", false
	}
	return e.cur.profile.ID, e.cur.node.ID, true
}

// Start 校验配置并启动。已有 Service（运行中、连接中、停止中）时，先停掉旧的，
// 等旧的 Run 返回之后再启动新的（PLAN 6.3.5，坑 6、9），这段时间状态是 Stopping。
func (e *Engine) Start(p model.Profile, nodeID string) error {
	prep, err := Prepare(p, nodeID, e.extraNodes()...)
	if err != nil {
		return err
	}
	e.ctl.Lock()
	defer e.ctl.Unlock()
	return e.startLocked(prep)
}

func (e *Engine) startLocked(prep *Prepared) error {
	e.mu.Lock()
	r := e.cur
	if r == nil {
		e.mu.Unlock()
		return e.launchLocked(prep)
	}
	e.pending = prep
	first := !r.stopping
	r.stopping = true
	e.status.State = StateStopping
	e.status.Pending = true
	e.notifyLocked()
	e.mu.Unlock()
	e.sink.Appendf("info", "switching to profile %q node %s: waiting for the current service to stop", prep.Profile.Name, nodeLabel(prep.Node))
	if first {
		r.svc.GracefulClose(e.opts.StopGrace)
	}
	return nil
}

// launchLocked 新建 Service 并在后台运行。调用方持有 e.ctl，且 e.cur 为 nil。
func (e *Engine) launchLocked(prep *Prepared) error {
	fail := func(err error) error {
		ae := apperr.Wrap(apperr.TunnelStart, err)
		e.mu.Lock()
		e.status = Status{State: StateFailed, ProfileID: prep.Profile.ID, ProfileName: prep.Profile.Name,
			NodeID: prep.Node.ID, NodeLabel: nodeLabel(prep.Node), Error: ae}
		e.notifyLocked()
		e.mu.Unlock()
		e.sink.Appendf("error", "start failed: %v", err)
		return ae
	}

	cs := source.NewConfigSource()
	if err := cs.ReplaceAll(prep.Proxies, prep.Visitors); err != nil {
		return fail(err)
	}
	so := frpclient.ServiceOptions{
		Common:                 prep.Common,
		ConfigSourceAggregator: source.NewAggregator(cs),
		UnsafeFeatures:         prep.Unsafe,
	}
	if id, ok := strings.CutPrefix(prep.Node.ID, api.OfficialNodePrefix); ok && e.opts.Tickets != nil {
		so.ConnectorCreator = ticketConnector(e.opts.Tickets, id, e.opts.DeviceID)
	}
	svc, err := frpclient.NewService(so)
	if err != nil {
		return fail(err)
	}

	gen := runSeq.Add(1)
	e.mu.Lock()
	e.gen = gen
	r := &run{
		gen:     gen,
		tag:     fmt.Sprintf("run-%d", gen),
		svc:     svc,
		common:  prep.Common,
		done:    make(chan struct{}),
		poke:    make(chan struct{}, 1),
		profile: prep.Profile,
		node:    prep.Node,
	}
	// frp 的 Run 会沿用 ctx 里的 xlog.Logger，于是这个 Service 打出的每一行都带 [run-N] 前缀。
	// 全局日志出口是共享的（PLAN 6.3.7），靠这个前缀才能确定一行日志属于哪个 Service。
	xl := xlog.New().AddPrefix(xlog.LogPrefix{Name: "moefrp", Value: r.tag, Priority: 1})
	ctx, cancel := context.WithCancel(xlog.NewContext(context.Background(), xl))
	r.cancel = cancel
	e.cur = r
	e.status = Status{
		State:       StateConnecting,
		ProfileID:   prep.Profile.ID,
		ProfileName: prep.Profile.Name,
		NodeID:      prep.Node.ID,
		NodeLabel:   nodeLabel(prep.Node),
		StartedAt:   time.Now().Format(time.RFC3339Nano),
	}
	e.proxies = nil
	e.notifyLocked()
	e.notifyProxiesLocked()
	e.mu.Unlock()

	e.sink.Appendf("info", "[%s] start profile %q via node %s (%s), %d proxies, %d visitors",
		r.tag, prep.Profile.Name, nodeLabel(prep.Node), prep.Node.ServerAddr+":"+fmt.Sprint(prep.Node.ServerPort),
		countEnabledProxies(prep.Profile), countEnabledVisitors(prep.Profile))
	go e.runLoop(r, ctx)
	go e.pollLoop(r)
	return nil
}

// Update 热更新正在运行的配置（PLAN 6.3.5）：
//   - 只改了代理、访客：先完整校验（含重名），通过后调用 UpdateConfigSource，连接不断；
//   - 节点的连接参数变了：停掉再重新启动；
//   - 校验不通过：不提交，返回错误，正在运行的隧道保持原样。
func (e *Engine) Update(p model.Profile) (UpdateMode, error) {
	e.ctl.Lock()
	defer e.ctl.Unlock()

	e.mu.Lock()
	r := e.cur
	if r == nil || r.profile.ID != p.ID {
		e.mu.Unlock()
		return UpdateNone, nil
	}
	if r.stopping {
		e.mu.Unlock()
		return UpdateNone, apperr.New(apperr.TunnelBusy, "tunnel is stopping")
	}
	nodeID, oldNode, common := r.node.ID, r.node, r.common
	e.mu.Unlock()

	prep, err := Prepare(p, nodeID, e.extraNodes()...)
	if err != nil {
		return UpdateNone, err
	}
	if !SameConnection(oldNode, prep.Node) {
		e.sink.Appendf("info", "[%s] node connection settings changed, restarting", r.tag)
		return UpdateRestart, e.startLocked(prep)
	}
	if err := r.svc.UpdateConfigSource(common, prep.Proxies, prep.Visitors); err != nil {
		return UpdateNone, apperr.Wrap(apperr.TunnelNotRunning, err)
	}
	e.mu.Lock()
	r.profile, r.node = prep.Profile, prep.Node
	e.status.ProfileName, e.status.NodeLabel = p.Name, nodeLabel(prep.Node)
	e.notifyLocked()
	e.mu.Unlock()
	e.sink.Appendf("info", "[%s] hot update applied: %d proxies, %d visitors", r.tag, countEnabledProxies(p), countEnabledVisitors(p))
	select {
	case r.poke <- struct{}{}:
	default:
	}
	return UpdateHot, nil
}

// PredictUpdate 返回 Update(p) 会采用的方式，不实际应用：不是正在运行的配置时为 none；
// 正在用的节点连接参数变了（或节点被删了）为 restart；否则为 hot。
func (e *Engine) PredictUpdate(p model.Profile) UpdateMode {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.cur
	if r == nil || r.profile.ID != p.ID {
		return UpdateNone
	}
	for _, n := range append(slices.Clone(p.Nodes), e.extra...) {
		if n.ID == r.node.ID {
			if SameConnection(r.node, n) {
				return UpdateHot
			}
			return UpdateRestart
		}
	}
	return UpdateRestart
}

// SetExtraNodes 设置配置之外也能选的节点（面板下发的官方节点）。启动和热更新时按 ID 查找。
func (e *Engine) SetExtraNodes(nodes []model.Node) {
	e.mu.Lock()
	e.extra = slices.Clone(nodes)
	e.mu.Unlock()
}

func (e *Engine) extraNodes() []model.Node {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.extra)
}

// Stop 发起停止并立即返回，状态变为 Stopping，Run 返回后变为 Idle。
// 也会取消等待中的切换。处于 Failed 时调用 Stop 会清掉错误回到 Idle。
func (e *Engine) Stop() error {
	e.ctl.Lock()
	defer e.ctl.Unlock()
	return e.stopLocked(e.opts.StopGrace)
}

func (e *Engine) stopLocked(grace time.Duration) error {
	e.mu.Lock()
	r := e.cur
	e.pending = nil
	if r == nil {
		if e.status.State == StateFailed {
			e.status = Status{State: StateIdle}
			e.notifyLocked()
		}
		e.mu.Unlock()
		return nil
	}
	e.status.Pending = false
	first := !r.stopping
	r.stopping = true
	e.status.State = StateStopping
	e.notifyLocked()
	e.mu.Unlock()
	if first {
		e.sink.Appendf("info", "[%s] stopping", r.tag)
		r.svc.GracefulClose(grace)
	}
	return nil
}

// Wait 等待当前 Service 的 Run 返回，返回 Run 的错误；超时返回 false。没有 Service 时立即返回。
func (e *Engine) Wait(timeout time.Duration) (error, bool) {
	e.mu.Lock()
	r := e.cur
	e.mu.Unlock()
	if r == nil {
		return nil, true
	}
	select {
	case <-r.done:
		e.mu.Lock()
		defer e.mu.Unlock()
		return r.runErr, true
	case <-time.After(timeout):
		return nil, false
	}
}

// Shutdown 用于退出程序（PLAN 6.3.8）：GracefulClose(grace)，再最多等 wait 让 Run 返回。
// 超时只会发生在还在拨号的时候，这时本来就没有建立隧道，调用方直接退出进程即可。
func (e *Engine) Shutdown(grace, wait time.Duration) error {
	e.ctl.Lock()
	e.mu.Lock()
	r := e.cur
	e.mu.Unlock()
	_ = e.stopLocked(grace)
	e.ctl.Unlock()
	if r == nil {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-time.After(wait):
		r.cancel()
		return fmt.Errorf("frp service did not stop within %s", wait)
	}
}

// runLoop 运行 Service，Run 返回后决定下一个状态。
func (e *Engine) runLoop(r *run, ctx context.Context) {
	var err error
	func() {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("frp service panic: %v", v)
				e.sink.Appendf("error", "[%s] panic: %v\n%s", r.tag, v, debug.Stack())
			}
		}()
		err = r.svc.Run(ctx)
	}()
	r.cancel()

	e.mu.Lock()
	stopping, node := r.stopping, r.node
	e.mu.Unlock()

	// 首次登录失败：解释原因。可能要做一次 TCP 探测（最多 ProbeTimeout），所以放在锁外。
	var fail *apierr.Error
	if err != nil && !stopping {
		fail = e.explainLoginFailure(node, err)
	}

	e.ctl.Lock()
	e.mu.Lock()
	stopping = r.stopping
	if e.cur == r {
		e.cur = nil
	}
	r.runErr = err
	pending := e.pending
	e.pending = nil
	e.proxies = nil
	switch {
	case pending != nil:
		// 保持 Stopping，马上由下面的 launchLocked 切到 Connecting
	case fail != nil && !stopping:
		e.status = Status{State: StateFailed, ProfileID: r.profile.ID, ProfileName: r.profile.Name,
			NodeID: r.node.ID, NodeLabel: nodeLabel(r.node), Error: fail}
	default:
		e.status = Status{State: StateIdle}
	}
	e.notifyLocked()
	e.notifyProxiesLocked()
	e.mu.Unlock()
	close(r.done)

	switch {
	case fail != nil && !stopping:
		e.sink.Appendf("error", "[%s] login failed: %s (%s)", r.tag, fail.Code, fail.Message)
	case err != nil && stopping:
		// 拨号途中被停止时 Run 返回 "login to the server failed: <nil>"，这是主动停止，不是失败（坑 6）
		e.sink.Appendf("info", "[%s] stopped while connecting", r.tag)
	default:
		e.sink.Appendf("info", "[%s] stopped", r.tag)
	}
	if pending != nil {
		_ = e.launchLocked(pending)
	}
	e.ctl.Unlock()
}

// explainLoginFailure 把首次登录失败的 frp 原文转换成错误码（PLAN 6.3.4，坑 8）。
func (e *Engine) explainLoginFailure(node model.Node, err error) *apierr.Error {
	msg := err.Error()
	if m := reLoginFailed.FindStringSubmatch(msg); m != nil {
		msg = m[1]
	}
	if strings.Contains(msg, "token in login doesn't match") || strings.Contains(msg, "authorization failed") {
		return &apierr.Error{Code: apperr.NodeTokenInvalid, Message: msg}
	}
	if ae := ticketFailure(msg); ae != nil {
		return ae
	}
	return classifyFailure(node, msg, Probe(context.Background(), node, e.opts.ProbeTimeout))
}

// classifyFailure 根据 TCP 探测结果解释登录或重连失败。
// 官方节点能连上却登录失败，多半是节点拒绝了（会话已退出、凭证被重置、没有权限），把节点给的原因原样显示。
func classifyFailure(node model.Node, msg string, pr PingResult) *apierr.Error {
	ae := &apierr.Error{Code: apperr.NodeHandshakeFailed, Message: msg, Details: map[string]any{"target": pr.Target}}
	switch {
	case !pr.OK:
		ae.Code = pr.Code
		ae.Details["probe"] = pr.Message
	case strings.HasPrefix(node.ID, api.OfficialNodePrefix):
		ae.Code = apperr.OfficialDenied
		ae.Details["reason"] = msg
	}
	return ae
}

// onLog 根据带 [run-N] 前缀的 frp 日志推断控制连接状态（PLAN 6.3.4）。
// 判断依据都出自 client/service.go 的 loopLoginUntilSuccess，升级 frp 时由集成测试兜底。
func (e *Engine) onLog(l LogLine) {
	text := l.Text
	if !strings.Contains(text, "to server") { // 三种关键行都包含它，先粗筛
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.cur
	if r == nil || r.stopping || !strings.Contains(text, "["+r.tag+"]") {
		return
	}
	switch {
	case strings.Contains(text, "try to connect to server"):
		if r.loggedIn {
			e.status.State = StateReconnecting
		} else {
			e.status.State = StateConnecting
		}
		e.notifyLocked()
	case strings.Contains(text, "login to server success"):
		r.loggedIn = true
		if m := reLoginSuccess.FindStringSubmatch(text); m != nil {
			e.status.RunID = m[1]
		}
		e.status.State = StateRunning
		e.status.Logins++
		e.status.Error = nil
		e.status.ConnectedAt = time.Now().Format(time.RFC3339Nano)
		e.notifyLocked()
	case strings.Contains(text, "connect to server error"):
		if !r.loggedIn {
			return // 首次登录失败，Run 马上会返回，由 runLoop 解释原因
		}
		msg := text
		if m := reConnectErr.FindStringSubmatch(text); m != nil {
			msg = m[1]
		}
		e.status.State = StateReconnecting
		e.status.Error = &apierr.Error{Code: apperr.NodeUnreachable, Message: msg}
		e.notifyLocked()
		if !r.probing {
			r.probing = true
			go e.probeReconnect(r, r.node, msg)
		}
	}
}

// probeReconnect 在重连失败时探测节点，给出更具体的原因。
func (e *Engine) probeReconnect(r *run, node model.Node, msg string) {
	pr := Probe(context.Background(), node, e.opts.ProbeTimeout)
	e.mu.Lock()
	defer e.mu.Unlock()
	r.probing = false
	if e.cur != r || r.stopping || e.status.State != StateReconnecting {
		return
	}
	if ae := ticketFailure(msg); ae != nil {
		e.status.Error = ae
	} else {
		e.status.Error = classifyFailure(node, msg, pr)
	}
	e.notifyLocked()
}

// pollLoop 每隔 PollInterval 读一次所有启用代理的状态，有变化才推送（PLAN 6.3.4）。
// GetProxyStatus 在 GracefulClose 期间会被 frp 的锁阻塞（坑 7），所以：
// 轮询在独立 goroutine 上跑；Stopping 时跳过；对外接口只读缓存。
func (e *Engine) pollLoop(r *run) {
	defer func() {
		if v := recover(); v != nil {
			e.sink.Appendf("error", "[%s] status poller panic: %v", r.tag, v)
		}
	}()
	first := time.NewTimer(200 * time.Millisecond)
	defer first.Stop()
	tick := time.NewTicker(e.opts.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-first.C:
		case <-tick.C:
		case <-r.poke:
		}
		e.mu.Lock()
		if e.cur != r || r.stopping {
			e.mu.Unlock()
			continue
		}
		proxies := make([]model.Proxy, 0, len(r.profile.Proxies))
		for _, p := range r.profile.Proxies {
			if p.Enabled {
				proxies = append(proxies, p)
			}
		}
		e.mu.Unlock()

		exp := r.svc.StatusExporter()
		list := make([]ProxyStatus, 0, len(proxies))
		for _, p := range proxies {
			st := ProxyStatus{ID: p.ID, Name: p.Name, Type: p.Type}
			if ws, ok := exp.GetProxyStatus(p.Name); ok && ws != nil {
				st.Phase, st.Err, st.RemoteAddr = ws.Phase, ws.Err, ws.RemoteAddr
				if ws.Err != "" {
					st.Code = ClassifyProxyError(ws.Err)
				}
			}
			list = append(list, st)
		}

		e.mu.Lock()
		if e.cur == r && !r.stopping && !slices.Equal(list, e.proxies) {
			e.proxies = list
			e.notifyProxiesLocked()
		}
		e.mu.Unlock()
	}
}

// ClassifyProxyError 把 frps 拒绝代理时的原文分类（文本出自 frp server/ports、server/control.go 等）。
func ClassifyProxyError(msg string) apierr.Code {
	switch {
	case strings.Contains(msg, "port already used"):
		return apperr.ProxyPortInUse
	case strings.Contains(msg, "port not allowed"):
		return apperr.ProxyPortNotAllowed
	case strings.Contains(msg, "port unavailable"):
		return apperr.ProxyPortUnavail
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "already in use"):
		return apperr.ProxyNameTaken
	case strings.Contains(msg, "router config conflict"):
		return apperr.ProxyDomainConflict
	case strings.Contains(msg, "not support"):
		return apperr.ProxyTypeNotAllowed
	default:
		return apperr.ProxyStartError
	}
}

func nodeLabel(n model.Node) string {
	if strings.TrimSpace(n.Remark) != "" {
		return n.Remark
	}
	return fmt.Sprintf("%s:%d", n.ServerAddr, n.ServerPort)
}

func countEnabledProxies(p model.Profile) int {
	n := 0
	for _, x := range p.Proxies {
		if x.Enabled {
			n++
		}
	}
	return n
}

func countEnabledVisitors(p model.Profile) int {
	n := 0
	for _, x := range p.Visitors {
		if x.Enabled {
			n++
		}
	}
	return n
}

func safeCall(sink *LogSink, what string, fn func()) {
	defer func() {
		if v := recover(); v != nil {
			sink.Appendf("error", "%s panic: %v", what, v)
		}
	}()
	fn()
}
