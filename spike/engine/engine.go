// Package engine 是 Phase 0 的最小 frp 引擎：按 PLAN.md 6.3.2 的流程在进程内跑 frp client。
package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	frpclient "github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"
)

type State string

const (
	StateIdle         State = "idle"
	StateConnecting   State = "connecting"
	StateRunning      State = "running"
	StateReconnecting State = "reconnecting"
	StateFailed       State = "failed"
	StateStopping     State = "stopping"
)

type Node struct {
	ServerAddr string
	ServerPort int
	Token      string
	User       string
	Protocol   string
}

type Proxy struct {
	Name       string
	Type       string // 目前只验证 tcp
	Enabled    bool
	LocalIP    string
	LocalPort  int
	RemotePort int
}

type ProxyStatus struct {
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	Err        string `json:"err"`
	RemoteAddr string `json:"remoteAddr"`
}

// Snapshot 是引擎对外的状态快照。
type Snapshot struct {
	State      State  `json:"state"`
	RunID      string `json:"runID"`
	Logins     int    `json:"logins"`
	Connects   int    `json:"connects"`
	LastError  string `json:"lastError"`
	StartedAt  string `json:"startedAt"`
	LoggedInAt string `json:"loggedInAt"`
}

var (
	reLoginSuccess = regexp.MustCompile(`login to server success, get run id \[([^\]]*)\]`)
	reConnectErr   = regexp.MustCompile(`connect to server error: (.*)$`)
)

// Engine 同一时间只跑一个 frp Service（frp 的 log.Logger 等是全局状态，见 PLAN 6.3.7）。
type Engine struct {
	sink *LogSink

	mu        sync.Mutex
	svc       *frpclient.Service
	common    *v1.ClientCommonConfig
	unsafe    *security.UnsafeFeatures
	cancel    context.CancelFunc
	done      chan struct{}
	runErr    error
	stopping  bool
	snap      Snapshot
	unsub     func()
	onChange  func(Snapshot)
	startedAt time.Time
}

func New(sink *LogSink) *Engine {
	return &Engine{sink: sink, snap: Snapshot{State: StateIdle}}
}

// OnChange 设置状态变化回调（Wails 里用来推事件）。
func (e *Engine) OnChange(fn func(Snapshot)) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snap
}

func (e *Engine) setLocked(fn func(*Snapshot)) {
	fn(&e.snap)
	if cb := e.onChange; cb != nil {
		// 异步回调，回调里重新读最新快照，避免乱序推送旧状态
		go func() { cb(e.Snapshot()) }()
	}
}

// classify 根据 frp 日志行推断控制连接状态（PLAN 6.3.4）。
func (e *Engine) classify(line string) {
	switch {
	case strings.Contains(line, "try to connect to server"):
		e.mu.Lock()
		e.setLocked(func(s *Snapshot) {
			s.Connects++
			if s.State == StateRunning || s.State == StateReconnecting {
				s.State = StateReconnecting
			} else if s.State != StateStopping {
				s.State = StateConnecting
			}
		})
		e.mu.Unlock()
	case strings.Contains(line, "login to server success"):
		runID := ""
		if m := reLoginSuccess.FindStringSubmatch(line); m != nil {
			runID = m[1]
		}
		e.mu.Lock()
		e.setLocked(func(s *Snapshot) {
			s.Logins++
			s.RunID = runID
			s.LoggedInAt = time.Now().Format(time.RFC3339Nano)
			if s.State != StateStopping {
				s.State = StateRunning
			}
		})
		e.mu.Unlock()
	case strings.Contains(line, "connect to server error"):
		msg := line
		if m := reConnectErr.FindStringSubmatch(line); m != nil {
			msg = m[1]
		}
		e.mu.Lock()
		e.setLocked(func(s *Snapshot) { s.LastError = msg })
		e.mu.Unlock()
	}
}

func buildCommon(n Node) (*v1.ClientCommonConfig, error) {
	common := &v1.ClientCommonConfig{
		ServerAddr: n.ServerAddr,
		ServerPort: n.ServerPort,
		User:       n.User,
	}
	common.Auth.Token = n.Token
	common.Transport.Protocol = n.Protocol
	// 不设置 WebServer.Port，就不会开 frpc 的管理端口
	if err := common.Complete(); err != nil { // 同时把 LoginFailExit 补成 true
		return nil, err
	}
	return common, nil
}

func mapProxies(ps []Proxy) ([]v1.ProxyConfigurer, error) {
	out := make([]v1.ProxyConfigurer, 0, len(ps))
	for _, p := range ps {
		c := v1.NewProxyConfigurerByType(v1.ProxyType(p.Type))
		if c == nil {
			return nil, fmt.Errorf("proxy %s: unsupported type %q", p.Name, p.Type)
		}
		base := c.GetBaseConfig()
		base.Name = p.Name
		enabled := p.Enabled
		base.Enabled = &enabled
		base.LocalIP = p.LocalIP
		base.LocalPort = p.LocalPort
		switch t := c.(type) {
		case *v1.TCPProxyConfig:
			t.RemotePort = p.RemotePort
		case *v1.UDPProxyConfig:
			t.RemotePort = p.RemotePort
		}
		out = append(out, c)
	}
	return out, nil
}

// validate 和 frpc 一样：先过滤、补全，再校验。用的是单独一份配置对象，不影响随后交给 frp 的那份。
func validate(common *v1.ClientCommonConfig, ps []Proxy, unsafe *security.UnsafeFeatures) error {
	cfgs, err := mapProxies(ps)
	if err != nil {
		return err
	}
	cs := source.NewConfigSource()
	if err := cs.ReplaceAll(cfgs, nil); err != nil {
		return err
	}
	loaded, visitors, err := source.NewAggregator(cs).Load()
	if err != nil {
		return err
	}
	loaded, visitors = config.FilterClientConfigurers(common, loaded, visitors)
	loaded = config.CompleteProxyConfigurers(loaded)
	visitors = config.CompleteVisitorConfigurers(visitors)
	_, err = validation.ValidateAllClientConfig(common, loaded, visitors, unsafe)
	return err
}

// Start 按 PLAN 6.3.2：ConfigSource → Aggregator → Filter/Complete → ValidateAllClientConfig → NewService → Run。
func (e *Engine) Start(n Node, ps []Proxy) error {
	e.mu.Lock()
	if e.svc != nil {
		e.mu.Unlock()
		return errors.New("engine already running")
	}
	e.mu.Unlock()

	common, err := buildCommon(n)
	if err != nil {
		return err
	}
	unsafe := security.NewUnsafeFeatures(nil)
	if err := validate(common, ps, unsafe); err != nil {
		return err
	}

	cfgs, err := mapProxies(ps)
	if err != nil {
		return err
	}
	cs := source.NewConfigSource()
	if err := cs.ReplaceAll(cfgs, nil); err != nil {
		return err
	}
	agg := source.NewAggregator(cs)

	svc, err := frpclient.NewService(frpclient.ServiceOptions{
		Common:                 common,
		ConfigSourceAggregator: agg,
		UnsafeFeatures:         unsafe,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	e.mu.Lock()
	e.svc, e.common, e.unsafe, e.cancel, e.done = svc, common, unsafe, cancel, done
	e.runErr, e.stopping = nil, false
	e.startedAt = time.Now()
	if e.unsub != nil {
		e.unsub()
	}
	e.unsub = e.sink.Subscribe(e.classify)
	e.setLocked(func(s *Snapshot) {
		*s = Snapshot{State: StateConnecting, StartedAt: e.startedAt.Format(time.RFC3339Nano)}
	})
	e.mu.Unlock()

	go func() {
		defer close(done)
		err := svc.Run(ctx)
		e.mu.Lock()
		e.runErr = err
		stopping := e.stopping
		e.svc = nil
		if e.unsub != nil {
			e.unsub()
			e.unsub = nil
		}
		e.setLocked(func(s *Snapshot) {
			if err != nil && !stopping {
				s.State = StateFailed
				s.LastError = err.Error()
			} else {
				s.State = StateIdle
			}
		})
		e.mu.Unlock()
	}()
	return nil
}

// Update 热更新代理列表（PLAN 6.3.5）：先校验，通过后再调用 UpdateConfigSource。
func (e *Engine) Update(ps []Proxy) error {
	e.mu.Lock()
	svc, common, unsafe := e.svc, e.common, e.unsafe
	e.mu.Unlock()
	if svc == nil {
		return errors.New("engine not running")
	}
	if err := validate(common, ps, unsafe); err != nil {
		return err
	}
	cfgs, err := mapProxies(ps)
	if err != nil {
		return err
	}
	return svc.UpdateConfigSource(common, cfgs, nil)
}

// Stop 按 PLAN 6.3.8：GracefulClose(grace)，然后最多等 wait 让 Run 返回。
func (e *Engine) Stop(grace, wait time.Duration) error {
	e.mu.Lock()
	svc, done := e.svc, e.done
	if svc == nil {
		e.mu.Unlock()
		return nil
	}
	e.stopping = true
	e.setLocked(func(s *Snapshot) { s.State = StateStopping })
	e.mu.Unlock()

	svc.GracefulClose(grace)
	select {
	case <-done:
		return nil
	case <-time.After(wait):
		e.cancel()
		return fmt.Errorf("frp service did not stop within %s", wait)
	}
}

// Wait 阻塞到 Run 返回，返回 Run 的错误。
func (e *Engine) Wait(timeout time.Duration) (error, bool) {
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	if done == nil {
		return nil, true
	}
	select {
	case <-done:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.runErr, true
	case <-time.After(timeout):
		return nil, false
	}
}

func (e *Engine) ProxyStatus(name string) (ProxyStatus, bool) {
	e.mu.Lock()
	svc := e.svc
	e.mu.Unlock()
	if svc == nil {
		return ProxyStatus{}, false
	}
	ws, ok := svc.StatusExporter().GetProxyStatus(name)
	if !ok || ws == nil {
		return ProxyStatus{}, false
	}
	return ProxyStatus{Name: ws.Name, Phase: ws.Phase, Err: ws.Err, RemoteAddr: ws.RemoteAddr}, true
}
