// Package agent 把各部分接起来：内嵌 frps、连接面板、处理面板的请求。
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	frplog "github.com/fatedier/frp/pkg/util/log"
	frpversion "github.com/fatedier/frp/pkg/util/version"
	golog "github.com/fatedier/golib/log"

	"moefrp/agent/internal/access"
	"moefrp/agent/internal/agentcfg"
	"moefrp/agent/internal/execx"
	"moefrp/agent/internal/frpsrv"
	"moefrp/agent/internal/install"
	"moefrp/agent/internal/link"
	"moefrp/agent/internal/logbuf"
	"moefrp/agent/internal/paths"
	"moefrp/agent/internal/selfupdate"
	"moefrp/agent/internal/sysinfo"
	"moefrp/agent/internal/vault"
	"moefrp/shared/agentproto"
)

// Agent 是运行中的 agent。
type Agent struct {
	p       paths.Paths
	cfg     agentcfg.Config
	version string
	log     *slog.Logger
	logs    *logbuf.Buffer
	frps    *frpsrv.Manager
	access  *access.Store
	vault   *vault.Vault // 本地加密存储：frps 配置、授权表
	machine string       // 机器指纹
	sampler sysinfo.Sampler
	static  sysinfo.Static

	exit func(code int) // 测试时替换

	busy      atomic.Bool // 正在升级或卸载
	pending   *selfupdate.State
	confirmed atomic.Bool
}

// Run 是 `moefrp-agent run`：systemd 服务的入口。
func Run(ctx context.Context, version string) error {
	p := paths.Default()
	logs := logbuf.New(2000, os.Stderr)
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// frp 的日志也进同一个缓冲，面板上能看到 frps 的输出
	frplog.Logger = frplog.Logger.WithOptions(golog.WithOutput(logs), golog.WithLevel(golog.InfoLevel))

	cfg, err := agentcfg.Load(p.AgentConfig())
	if err != nil {
		return err
	}
	a := &Agent{
		p: p, cfg: cfg, version: version, log: log, logs: logs,
		frps:   frpsrv.New(p.FrpsConfig()),
		static: sysinfo.ReadStatic(),
		exit:   os.Exit,
	}
	// 本地加密存储：用面板下发的存储密钥加密，密钥本身用机器指纹和令牌封存（租约内重启、面板离线也能解开）
	a.machine = sysinfo.MachineID(p.MachineIDFallback())
	a.vault = vault.Open(p.ConfDir, a.machine, cfg.Token, nil)
	if !a.vault.Unlocked() {
		log.Warn("本地加密存储还锁着，frps 等面板下发存储密钥后再启动", "reason", a.vault.Reason())
	}
	a.frps.Vault = a.vault
	// 准入：本机起 frps 插件，按面板签发的登录票据放行
	acc, err := access.Load(p.Access(), a.vault)
	if err != nil {
		log.Error("读取授权表失败", "err", err)
	}
	a.access = acc
	pluginAddr, err := acc.Listen()
	if err != nil {
		return fmt.Errorf("启动准入插件：%w", err)
	}
	a.frps.Plugin = func() *v1.HTTPPluginOptions {
		if !acc.Enabled() {
			return nil
		}
		return &v1.HTTPPluginOptions{Name: "moefrp-access", Addr: pluginAddr, Path: "/handler", Ops: access.PluginOps}
	}

	pending, rollbackNow, err := selfupdate.Startup(p, version)
	if err != nil {
		log.Error("读取升级状态失败", "err", err)
	}
	if rollbackNow {
		log.Error("新版本多次启动都没能连上面板，已换回旧版本，退出后由 systemd 重启")
		return nil // 退出码 0 也会被 Restart=always 拉起
	}
	a.pending = pending
	if pending != nil && !pending.RolledBack && version == pending.To {
		go a.confirmTimer(ctx)
	}

	log.Info("moefrp-agent 启动", "version", version, "node", cfg.NodeID, "server", cfg.Server, "exec", cfg.Exec)
	if err := a.frps.Boot(); err != nil {
		log.Error("frps 启动失败", "err", err)
	}
	a.sampler.Sample() // 第一次采样只是为了下次能算出 CPU 占用率
	// 每小时推高本地存储的时间高水位：之后把系统时间往回调就能被发现
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.vault.Touch()
			}
		}
	}()

	link.Run(ctx, link.Options{
		Server: cfg.Server, Token: cfg.Token,
		Hello: a.hello, Report: a.report, Handle: a.handle, OnReady: a.ready, Log: log,
	})
	a.frps.Close()
	return nil
}

// confirmTimer：升级后的新版本在 ConfirmWithin 内没连上面板就回滚。
func (a *Agent) confirmTimer(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(selfupdate.ConfirmWithin):
		if a.confirmed.Load() {
			return
		}
		if err := selfupdate.Rollback(a.p, a.pending, "新版本启动后没能连上面板"); err != nil {
			a.log.Error("回滚失败", "err", err)
			return
		}
		a.log.Error("新版本没能连上面板，已换回旧版本，退出后由 systemd 重启")
		a.exit(1)
	}
}

func (a *Agent) hello() agentproto.Hello {
	h := agentproto.Hello{
		AgentVersion: a.version, FrpVersion: frpversion.Full(),
		OS: runtime.GOOS, Arch: runtime.GOARCH,
		Distro: a.static.Distro, Kernel: a.static.Kernel, Hostname: a.static.Hostname, BootTime: a.static.BootTime,
		ExecEnabled: a.cfg.Exec, AppliedRev: a.frps.AppliedRev(),
		Features:  []string{agentproto.FeatureAccess, agentproto.FeatureTickets, agentproto.FeatureSettings},
		MachineID: a.machine,
		PublicIPs: sysinfo.PublicIPs(),
	}
	if a.pending != nil && !a.confirmed.Load() {
		h.Upgrade = selfupdate.Result(a.pending, a.version)
	}
	return h
}

// ready：hello 被面板接受，说明这个版本能正常工作，升级到此确认完成。
func (a *Agent) ready(agentproto.HelloAck) {
	if a.pending != nil && a.confirmed.CompareAndSwap(false, true) {
		selfupdate.Finish(a.p)
	}
}

func (a *Agent) report() agentproto.Report {
	u := a.sampler.Sample()
	return agentproto.Report{
		Uptime: u.Uptime, CPU: u.CPU, Load: u.Load,
		MemTotal: u.MemTotal, MemUsed: u.MemUsed, DiskTotal: u.DiskTotal, DiskUsed: u.DiskUsed,
		NetRx: u.NetRx, NetTx: u.NetTx,
		Frps: a.frps.Status(),
	}
}

func bad(err error) error {
	return &agentproto.Error{Code: agentproto.ErrBadRequest, Message: err.Error()}
}

func (a *Agent) handle(ctx context.Context, method string, data json.RawMessage) (any, error) {
	switch method {
	case agentproto.MethodFrpsApply:
		var req agentproto.ApplyRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, bad(err)
		}
		var res agentproto.ApplyResult
		if req.Settings != nil {
			res = a.frps.ApplySettings(req.Rev, *req.Settings)
		} else {
			res = a.frps.Apply(req.Rev, req.Config) // 旧面板下发的 TOML
		}
		a.log.Info("应用 frps 配置", "rev", req.Rev, "applied", res.Applied, "rolled_back", res.RolledBack, "err", res.Error)
		return res, nil

	case agentproto.MethodStoreLease:
		var l agentproto.StoreLease
		if err := json.Unmarshal(data, &l); err != nil {
			return nil, bad(err)
		}
		key, err := base64.StdEncoding.DecodeString(l.Key)
		if err != nil {
			return nil, bad(err)
		}
		unlocked, err := a.vault.SetLease(key, l.LeaseUntil, l.ServerTime)
		if err != nil {
			return nil, err
		}
		if unlocked {
			// 刚解锁：读出加密保存的授权表，再用保存的配置启动 frps（之前因为锁着没启动）
			if err := a.access.Reload(); err != nil {
				a.log.Error("读取授权表失败", "err", err)
			}
			if err := a.frps.Boot(); err != nil {
				a.log.Error("frps 启动失败", "err", err)
			}
			a.log.Info("本地加密存储已解锁", "lease_until", a.vault.LeaseUntil().Format(time.RFC3339))
		}
		return struct{}{}, nil

	case agentproto.MethodFrpsRestart:
		return struct{}{}, a.frps.Restart()

	case agentproto.MethodAccessSync:
		var t agentproto.AccessTable
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, bad(err)
		}
		changed, err := a.access.Apply(t)
		if err != nil {
			return nil, err
		}
		res := agentproto.AccessResult{Users: len(t.Users)}
		if a.frps.Status().State != agentproto.FrpsRunning {
			// frps 可能因为授权表还没到（没有准入插件不能启动）而没启动
			if err := a.frps.Boot(); err != nil {
				a.log.Error("frps 启动失败", "err", err)
			}
		} else if changed {
			// 挂上或去掉插件要重启 frps；还没有 frps 配置时等配置下发后自然生效
			if err := a.frps.Restart(); err == nil {
				res.Restarted = true
			}
			a.log.Info("官方节点开关变化", "enabled", t.Enabled, "frps_restarted", res.Restarted)
		}
		return res, nil

	case agentproto.MethodFrpsProxies:
		return agentproto.ProxiesResult{Proxies: a.frps.Proxies()}, nil

	case agentproto.MethodLogs:
		var req agentproto.LogsRequest
		_ = json.Unmarshal(data, &req)
		if req.Lines <= 0 || req.Lines > 2000 {
			req.Lines = 500
		}
		return agentproto.LogsResult{Lines: a.logs.Tail(req.Lines)}, nil

	case agentproto.MethodExec:
		if !a.cfg.Exec {
			return nil, &agentproto.Error{Code: agentproto.ErrDisabled, Message: "这台节点安装时关闭了远程执行命令（--no-exec）"}
		}
		var req agentproto.ExecRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, bad(err)
		}
		a.log.Info("执行面板下发的命令", "command", req.Command)
		return execx.Run(ctx, req), nil

	case agentproto.MethodUpgrade:
		var req agentproto.UpgradeRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, bad(err)
		}
		return a.upgrade(ctx, req)

	case agentproto.MethodUninstall:
		if !a.busy.CompareAndSwap(false, true) {
			return nil, &agentproto.Error{Code: agentproto.ErrBusy, Message: "正在升级或卸载"}
		}
		a.log.Warn("面板要求卸载")
		if err := install.UninstallDetached(ctx, a.p); err != nil {
			a.busy.Store(false)
			return nil, err
		}
		return struct{}{}, nil
	}
	return nil, &agentproto.Error{Code: agentproto.ErrUnknownMethod, Message: "不支持的操作：" + method}
}

func (a *Agent) upgrade(ctx context.Context, req agentproto.UpgradeRequest) (any, error) {
	if !a.busy.CompareAndSwap(false, true) {
		return nil, &agentproto.Error{Code: agentproto.ErrBusy, Message: "正在升级或卸载"}
	}
	if strings.HasPrefix(req.URL, "/") {
		req.URL = a.cfg.Server + req.URL // 面板给的是相对路径，用自己配置的面板地址补全
	}
	a.log.Info("开始升级", "from", a.version, "to", req.Version)
	if err := selfupdate.Prepare(ctx, a.p, req, a.version); err != nil {
		a.busy.Store(false)
		a.log.Error("升级失败", "err", err)
		return nil, err
	}
	// 先把响应发出去，再退出让 systemd 拉起新版本
	go func() {
		time.Sleep(500 * time.Millisecond)
		a.log.Info("升级包已就绪，重启到新版本")
		a.frps.Close()
		a.exit(1)
	}()
	return struct{}{}, nil
}
