// Package frpsrv 在 agent 进程里运行 frps（frp v0.71.1 的 server 包），负责配置校验、
// 进程内重启和失败回滚。
//
// 托管节点（面板 1.4 起）：面板下发结构化配置（frpsset.Settings），这里在内存里拼出 frps 配置，不落 TOML；
// 配置加密保存在本地存储（vault）里。frps 不设 token，只认准入插件放行的登录，所以没有插件时绝不启动。
// 旧版本留下的 frps.toml 只在升级过渡期用来启动，面板下发结构化配置后删掉。
package frpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/metrics"
	"github.com/fatedier/frp/pkg/metrics/mem"
	"github.com/fatedier/frp/pkg/policy/security"
	"github.com/fatedier/frp/server"

	"moefrp/agent/internal/agentcfg"
	"moefrp/agent/internal/vault"
	"moefrp/shared/agentproto"
	"moefrp/shared/frpsset"
	"moefrp/shared/quota"
)

var enableMemOnce sync.Once

// Manager 管理一个 frps Service。所有方法可以并发调用。
type Manager struct {
	path string // 旧的 frps.toml（agent 1.3.x 和旧面板用）

	// Plugin 在每次启动 frps 时调用，返回非 nil 时把这个 HTTP 插件加到配置里（准入控制）。
	// 结构化配置下 frps 没有 token，Plugin 返回 nil 时拒绝启动。在 Boot 之前设置。
	Plugin func() *v1.HTTPPluginOptions
	// Vault 是本地加密存储，结构化配置存在里面。在 Boot 之前设置；为 nil 时只支持旧的 TOML。
	Vault *vault.Vault

	mu        sync.Mutex
	svc       *server.Service
	done      chan struct{}
	raw       string            // 旧的 TOML 配置（settings 为 nil 时）
	settings  *frpsset.Settings // 结构化配置
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

// vaultName 是结构化配置在本地存储里的文件名。
const vaultName = "frps"

type savedSettings struct {
	Rev      int64            `json:"rev"`
	Settings frpsset.Settings `json:"settings"`
}

// ServerConfig 把结构化配置转换成 frps 的配置：不设 token（只认准入插件），零值用 frps 的默认值。
func ServerConfig(s frpsset.Settings) (*v1.ServerConfig, string, error) {
	if err := s.Validate(); err != nil {
		return nil, "", err
	}
	detailed := s.DetailedErrorsToClient
	cfg := &v1.ServerConfig{
		BindAddr: s.BindAddr, BindPort: s.BindPort, KCPBindPort: s.KCPBindPort, QUICBindPort: s.QUICBindPort,
		VhostHTTPPort: s.VhostHTTPPort, VhostHTTPSPort: s.VhostHTTPSPort, TCPMuxHTTPConnectPort: s.TCPMuxHTTPConnectPort,
		SubDomainHost: s.SubDomainHost, VhostHTTPTimeout: int64(s.VhostHTTPTimeout), MaxPortsPerClient: int64(s.MaxPortsPerClient),
		UserConnTimeout: int64(s.UserConnTimeout), UDPPacketSize: int64(s.UDPPacketSize),
		NatHoleAnalysisDataReserveHours: int64(s.NatHoleReserveHours),
		DetailedErrorsToClient:          &detailed,
	}
	cfg.Log.Level = s.LogLevel
	tcpMux := s.TCPMux
	cfg.Transport.TCPMux = &tcpMux
	cfg.Transport.MaxPoolCount, cfg.Transport.HeartbeatTimeout = int64(s.MaxPoolCount), int64(s.HeartbeatTimeout)
	cfg.Transport.TLS.Force = s.TLSForce
	ranges, _ := quota.ParsePorts(s.AllowPorts)
	for _, r := range ranges {
		if r.Lo == r.Hi {
			cfg.AllowPorts = append(cfg.AllowPorts, types.PortsRange{Single: r.Lo})
		} else {
			cfg.AllowPorts = append(cfg.AllowPorts, types.PortsRange{Start: r.Lo, End: r.Hi})
		}
	}
	if err := cfg.Complete(); err != nil {
		return nil, "", err
	}
	cfg.Auth.Token = "" // 没有 token：谁能登录由准入插件按面板签发的票据决定
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

// Boot 用保存的配置启动 frps：先找本地存储里的结构化配置，没有（或存储锁着）时用旧的 frps.toml。
// 已经在运行或没有配置时什么都不做。存储稍后解锁时可以再调用一次。
// CurrentSettings 返回正在用的结构化配置（还是旧 TOML 配置或没有配置时为 nil）。
func (m *Manager) CurrentSettings() *frpsset.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings == nil {
		return nil
	}
	s := *m.settings
	return &s
}

func (m *Manager) Boot() error {
	m.mu.Lock()
	running := m.svc != nil
	m.mu.Unlock()
	if running {
		return nil
	}
	if m.Vault != nil {
		b, err := m.Vault.Read(vaultName)
		switch {
		case err == nil:
			var sv savedSettings
			if err := json.Unmarshal(b, &sv); err != nil {
				return fmt.Errorf("本地存储里的 frps 配置损坏：%w", err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			m.settings, m.raw, m.rev = &sv.Settings, "", sv.Rev
			err := m.startCurrentLocked()
			if err != nil {
				m.state, m.errMsg = agentproto.FrpsError, err.Error()
			}
			return err
		case !errors.Is(err, os.ErrNotExist) && !errors.Is(err, vault.ErrLocked):
			return err
		}
	}
	raw, rev, err := m.LoadSaved()
	if err != nil || raw == "" {
		return err
	}
	cfg, _, err := Parse(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw, m.settings, m.rev = raw, nil, rev
	if err == nil {
		err = m.startLocked(cfg, false)
	}
	if err != nil {
		m.state, m.errMsg = agentproto.FrpsError, err.Error()
	}
	return err
}

// ApplySettings 校验并应用结构化配置：失败时恢复旧配置继续运行。成功后加密保存，并删掉旧的 frps.toml。
func (m *Manager) ApplySettings(rev int64, s frpsset.Settings) agentproto.ApplyResult {
	res := agentproto.ApplyResult{Rev: rev}
	cfg, warning, err := ServerConfig(s)
	res.Warning = warning
	if err != nil {
		res.Error = "配置不正确：" + err.Error()
		return res
	}
	if m.Vault == nil || !m.Vault.Unlocked() {
		res.Error = "本地加密存储还锁着（没收到面板下发的存储密钥），配置没法保存"
		return res
	}
	b, _ := json.Marshal(savedSettings{Rev: rev, Settings: s})

	m.mu.Lock()
	defer m.mu.Unlock()
	oldRaw, oldSettings, wasRunning := m.raw, m.settings, m.svc != nil
	m.stopLocked()
	if err := m.startLocked(cfg, true); err != nil {
		res.Error = "frps 启动失败：" + err.Error()
		if wasRunning {
			m.raw, m.settings = oldRaw, oldSettings
			if m.startCurrentLocked() == nil {
				res.RolledBack = true
				return res
			}
		}
		m.state, m.errMsg = agentproto.FrpsError, res.Error
		return res
	}
	if err := m.Vault.Write(vaultName, b); err != nil {
		// 已经在用新配置运行，只是没能保存；下次重启会回到旧配置
		res.Warning = strings.TrimSpace(res.Warning + "\n保存配置失败：" + err.Error())
	} else if m.path != "" {
		_ = os.Remove(m.path) // 旧的明文 TOML 不再需要
	}
	m.settings, m.raw, m.rev = &s, "", rev
	res.Applied = true
	return res
}

// Apply 校验并应用旧的 TOML 配置（旧面板下发的）：失败时恢复旧配置继续运行。成功后才写入 frps.toml。
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
	oldRaw, oldSettings, wasRunning := m.raw, m.settings, m.svc != nil
	m.stopLocked()
	if err := m.startLocked(cfg, false); err != nil {
		res.Error = "frps 启动失败：" + err.Error()
		if wasRunning {
			m.raw, m.settings = oldRaw, oldSettings
			if m.startCurrentLocked() == nil {
				res.RolledBack = true
				return res
			}
		}
		m.state, m.errMsg = agentproto.FrpsError, res.Error
		return res
	}
	if err := m.save(raw, rev); err != nil {
		res.Warning = strings.TrimSpace(res.Warning + "\n保存配置文件失败：" + err.Error())
	}
	m.raw, m.settings, m.rev = raw, nil, rev
	res.Applied = true
	return res
}

// Restart 用当前配置重启 frps。
func (m *Manager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.raw == "" && m.settings == nil {
		return errors.New("还没有 frps 配置")
	}
	m.stopLocked()
	if err := m.startCurrentLocked(); err != nil {
		m.state, m.errMsg = agentproto.FrpsError, err.Error()
		return err
	}
	return nil
}

// startCurrentLocked 用 m.settings 或 m.raw 启动 frps。
func (m *Manager) startCurrentLocked() error {
	if m.settings != nil {
		cfg, _, err := ServerConfig(*m.settings)
		if err != nil {
			return err
		}
		return m.startLocked(cfg, true)
	}
	if m.raw == "" {
		return errors.New("还没有 frps 配置")
	}
	cfg, _, err := Parse(m.raw)
	if err != nil {
		return err
	}
	return m.startLocked(cfg, false)
}

// Close 停止 frps。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	m.state = agentproto.FrpsStopped
}

// startLocked 启动 frps。managed 为 true（结构化配置，没有 token）时必须有准入插件，否则拒绝启动。
func (m *Manager) startLocked(cfg *v1.ServerConfig, managed bool) error {
	var p *v1.HTTPPluginOptions
	if m.Plugin != nil {
		p = m.Plugin()
	}
	if p != nil {
		cfg.HTTPPlugins = append(cfg.HTTPPlugins, *p)
	} else if managed {
		return errors.New("授权表还没下发，frps 暂不启动（节点没有 token，不挂准入插件的话谁都能连）")
	}
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
