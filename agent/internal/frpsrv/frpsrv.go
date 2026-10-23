// Package frpsrv 在 agent 进程里运行 frps（frp v0.71.1 的 server 包），负责配置校验、
// 进程内重启和失败回滚。
package frpsrv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatedier/frp/pkg/config"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/metrics"
	"github.com/fatedier/frp/pkg/metrics/mem"
	"github.com/fatedier/frp/pkg/policy/security"
	"github.com/fatedier/frp/server"

	"moefrp/agent/internal/agentcfg"
	"moefrp/shared/agentproto"
)

var enableMemOnce sync.Once

// Manager 管理一个 frps Service。所有方法可以并发调用。
type Manager struct {
	path string // frps.toml

	mu        sync.Mutex
	svc       *server.Service
	done      chan struct{}
	raw       string
	rev       int64
	bindPort  int
	state     string
	errMsg    string
	startedAt time.Time
}

// New 创建 Manager。frps 的内存统计（客户端数、流量）由 agent 统一打开一次，
// 所以 frps 配置里不允许再开 dashboard（见 Parse）。
func New(path string) *Manager {
	enableMemOnce.Do(metrics.EnableMem)
	return &Manager{path: path, state: agentproto.FrpsStopped}
}

// Parse 用 frp 自己的加载和校验代码检查配置，返回补全后的配置和警告。
func Parse(raw string) (*v1.ServerConfig, string, error) {
	cfg := &v1.ServerConfig{}
	if strings.TrimSpace(raw) == "" {
		return nil, "", errors.New("配置为空")
	}
	if config.DetectLegacyINIFormat([]byte(raw)) {
		return nil, "", errors.New("不支持旧的 INI 格式，请用 TOML")
	}
	if err := config.LoadConfigure([]byte(raw), cfg, true, "toml"); err != nil {
		return nil, "", err
	}
	if err := cfg.Complete(); err != nil {
		return nil, "", err
	}
	if cfg.WebServer.Port > 0 {
		return nil, "", errors.New("不支持 frps 自带的 dashboard（webServer）：节点状态和代理列表在面板上看，开启会导致统计重复计数")
	}
	warning, err := validation.NewConfigValidator(security.NewUnsafeFeatures(nil)).ValidateServerConfig(cfg)
	w := ""
	if warning != nil {
		w = fmt.Sprint(warning)
	}
	if err != nil {
		return nil, w, err
	}
	return cfg, w, nil
}

// header 写在 frps.toml 第一行，记录面板配置的版本号。
const header = "# 由 MoeFrp 面板管理，请在面板上修改，这里的改动会被覆盖。rev="

var revRe = regexp.MustCompile(`^# .*rev=(\d+)`)

// LoadSaved 读取上次生效的配置。文件不存在时返回空配置和 rev 0。
func (m *Manager) LoadSaved() (raw string, rev int64, err error) {
	b, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	s := string(b)
	first, rest, _ := strings.Cut(s, "\n")
	if mm := revRe.FindStringSubmatch(first); mm != nil {
		rev, _ = strconv.ParseInt(mm[1], 10, 64)
		return rest, rev, nil
	}
	return s, 0, nil
}

func (m *Manager) save(raw string, rev int64) error {
	return agentcfg.WriteFileAtomic(m.path, []byte(header+strconv.FormatInt(rev, 10)+"\n"+raw), 0o600)
}

// Boot 在 agent 启动时用上次保存的配置启动 frps。没有配置时什么都不做。
func (m *Manager) Boot() error {
	raw, rev, err := m.LoadSaved()
	if err != nil || raw == "" {
		return err
	}
	cfg, _, err := Parse(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw, m.rev = raw, rev
	if err == nil {
		err = m.startLocked(cfg)
	}
	if err != nil {
		m.state, m.errMsg = agentproto.FrpsError, err.Error()
	}
	return err
}

// Apply 校验并应用新配置：失败时恢复旧配置继续运行。成功后才写入 frps.toml。
func (m *Manager) Apply(rev int64, raw string) agentproto.ApplyResult {
	res := agentproto.ApplyResult{Rev: rev}
	cfg, warning, err := Parse(raw)
	res.Warning = warning
	if err != nil {
		res.Error = "配置不正确：" + err.Error()
		return res
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	oldRaw, wasRunning := m.raw, m.svc != nil
	m.stopLocked()
	if err := m.startLocked(cfg); err != nil {
		res.Error = "frps 启动失败：" + err.Error()
		if wasRunning {
			if oldCfg, _, perr := Parse(oldRaw); perr == nil && m.startLocked(oldCfg) == nil {
				res.RolledBack = true
				return res
			}
		}
		m.state, m.errMsg = agentproto.FrpsError, res.Error
		return res
	}
	if err := m.save(raw, rev); err != nil {
		// 已经在用新配置运行，只是没能保存；下次重启会回到旧配置
		res.Warning = strings.TrimSpace(res.Warning + "\n保存配置文件失败：" + err.Error())
	}
	m.raw, m.rev = raw, rev
	res.Applied = true
	return res
}

// Restart 用当前配置重启 frps。
func (m *Manager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.raw == "" {
		return errors.New("还没有 frps 配置")
	}
	cfg, _, err := Parse(m.raw)
	if err != nil {
		return err
	}
	m.stopLocked()
	if err := m.startLocked(cfg); err != nil {
		m.state, m.errMsg = agentproto.FrpsError, err.Error()
		return err
	}
	return nil
}

// Close 停止 frps。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	m.state = agentproto.FrpsStopped
}

func (m *Manager) startLocked(cfg *v1.ServerConfig) error {
	svc, err := server.NewService(cfg)
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(context.Background())
	}()
	m.svc, m.done = svc, done
	m.bindPort = cfg.BindPort
	m.state, m.errMsg, m.startedAt = agentproto.FrpsRunning, "", time.Now()
	return nil
}

func (m *Manager) stopLocked() {
	if m.svc == nil {
		return
	}
	_ = m.svc.Close()
	select {
	case <-m.done:
	case <-time.After(10 * time.Second):
	}
	m.svc, m.done = nil, nil
	m.state = agentproto.FrpsStopped
}

// AppliedRev 返回当前生效配置的版本号。
func (m *Manager) AppliedRev() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rev
}

// Status 返回运行状态和统计。
func (m *Manager) Status() agentproto.FrpsStatus {
	m.mu.Lock()
	st := agentproto.FrpsStatus{State: m.state, Error: m.errMsg, AppliedRev: m.rev}
	running := m.svc != nil
	if running {
		st.BindPort = m.bindPort
		st.StartedAt = m.startedAt.Unix()
	}
	m.mu.Unlock()
	if running {
		s := mem.StatsCollector.GetServer()
		st.Clients, st.Conns = s.ClientCounts, s.CurConns
		st.TrafficIn, st.TrafficOut = s.TotalTrafficIn, s.TotalTrafficOut
		for _, n := range s.ProxyTypeCounts {
			st.Proxies += n
		}
	}
	return st
}

var proxyTypes = []string{"tcp", "udp", "http", "https", "tcpmux", "stcp", "sudp", "xtcp"}

// Proxies 返回 frps 记录的代理（包括最近下线的）。
func (m *Manager) Proxies() []agentproto.ProxyInfo {
	out := []agentproto.ProxyInfo{}
	for _, t := range proxyTypes {
		for _, p := range mem.StatsCollector.GetProxiesByType(t) {
			out = append(out, agentproto.ProxyInfo{
				Name: p.Name, Type: p.Type, User: p.User, ClientID: p.ClientID,
				Online: p.LastStartAt > 0 && p.LastStartAt >= p.LastCloseAt,
				Conns:  p.CurConns, TrafficIn: p.TodayTrafficIn, TrafficOut: p.TodayTrafficOut,
				LastStart: p.LastStartTime,
			})
		}
	}
	return out
}
