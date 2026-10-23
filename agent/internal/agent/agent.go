// Package agent 把各部分接起来：内嵌 frps、连接面板、处理面板的请求。
package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	frplog "github.com/fatedier/frp/pkg/util/log"
	frpversion "github.com/fatedier/frp/pkg/util/version"
	golog "github.com/fatedier/golib/log"

	"moefrp/agent/internal/agentcfg"
	"moefrp/agent/internal/execx"
	"moefrp/agent/internal/frpsrv"
	"moefrp/agent/internal/install"
	"moefrp/agent/internal/link"
	"moefrp/agent/internal/logbuf"
	"moefrp/agent/internal/paths"
	"moefrp/agent/internal/selfupdate"
	"moefrp/agent/internal/sysinfo"
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
		res := a.frps.Apply(req.Rev, req.Config)
		a.log.Info("应用 frps 配置", "rev", req.Rev, "applied", res.Applied, "rolled_back", res.RolledBack, "err", res.Error)
		return res, nil

	case agentproto.MethodFrpsRestart:
		return struct{}{}, a.frps.Restart()

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
