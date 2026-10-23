package main

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"spike/engine"
)

// demoConfig 从环境变量读演示用的节点和代理，默认连 localfrps（127.0.0.1:17000）。
type demoConfig struct {
	Node  engine.Node
	Proxy engine.Proxy
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func loadDemoConfig() demoConfig {
	return demoConfig{
		Node: engine.Node{
			ServerAddr: envOr("SPIKE_FRPS_ADDR", "127.0.0.1"),
			ServerPort: envInt("SPIKE_FRPS_PORT", 17000),
			Token:      envOr("SPIKE_FRPS_TOKEN", "spike-token"),
		},
		Proxy: engine.Proxy{
			Name:       "echo",
			Type:       "tcp",
			Enabled:    true,
			LocalIP:    "127.0.0.1",
			LocalPort:  envInt("SPIKE_LOCAL_PORT", 17001),
			RemotePort: envInt("SPIKE_REMOTE_PORT", 17002),
		},
	}
}

// TunnelView 是推给前端的整体状态。
type TunnelView struct {
	Engine engine.Snapshot     `json:"engine"`
	Proxy  *engine.ProxyStatus `json:"proxy"`
	Node   string              `json:"node"`
	Remote int                 `json:"remote"`
}

// TunnelService 绑定给前端：Start / Stop / View / Logs。
type TunnelService struct {
	app  *application.App
	eng  *engine.Engine
	sink *engine.LogSink
	cfg  demoConfig

	mu      sync.Mutex
	pending []string
	onState func(TunnelView)
}

func NewTunnelService(sink *engine.LogSink) *TunnelService {
	t := &TunnelService{eng: engine.New(sink), sink: sink, cfg: loadDemoConfig()}
	t.eng.OnChange(func(engine.Snapshot) { t.emitState() })
	sink.Subscribe(func(line string) {
		t.mu.Lock()
		t.pending = append(t.pending, line)
		t.mu.Unlock()
	})
	return t
}

// run 在 app 启动后调用：每 200ms 批量推日志，每秒轮询一次代理状态（PLAN 6.3.4 / 6.3.6）。
func (t *TunnelService) run(app *application.App) {
	t.app = app
	go func() {
		logTick := time.NewTicker(200 * time.Millisecond)
		statusTick := time.NewTicker(time.Second)
		var lastPhase string
		for {
			select {
			case <-logTick.C:
				t.mu.Lock()
				batch := t.pending
				t.pending = nil
				t.mu.Unlock()
				if len(batch) > 0 {
					app.Event.Emit("tunnel:logs", batch)
				}
			case <-statusTick.C:
				v := t.View()
				phase := ""
				if v.Proxy != nil {
					phase = v.Proxy.Phase + "|" + v.Proxy.Err
				}
				if phase != lastPhase {
					lastPhase = phase
					t.emitState()
				}
			}
		}
	}()
}

func (t *TunnelService) emitState() {
	v := t.View()
	if t.app != nil {
		t.app.Event.Emit("tunnel:state", v)
	}
	t.mu.Lock()
	cb := t.onState
	t.mu.Unlock()
	if cb != nil {
		cb(v)
	}
}

func (t *TunnelService) View() TunnelView {
	v := TunnelView{
		Engine: t.eng.Snapshot(),
		Node:   t.cfg.Node.ServerAddr + ":" + strconv.Itoa(t.cfg.Node.ServerPort),
		Remote: t.cfg.Proxy.RemotePort,
	}
	if st, ok := t.eng.ProxyStatus(t.cfg.Proxy.Name); ok {
		v.Proxy = &st
	}
	return v
}

func (t *TunnelService) Start() error {
	diag("TunnelService.Start")
	return t.eng.Start(t.cfg.Node, []engine.Proxy{t.cfg.Proxy})
}

func (t *TunnelService) Stop() error {
	diag("TunnelService.Stop")
	return t.eng.Stop(500*time.Millisecond, 2*time.Second)
}

// Logs 返回缓冲里的全部日志（前端首次打开时用）。
func (t *TunnelService) Logs() []string {
	return t.sink.Lines()
}

func (t *TunnelService) setOnState(fn func(TunnelView)) {
	t.mu.Lock()
	t.onState = fn
	t.mu.Unlock()
}
