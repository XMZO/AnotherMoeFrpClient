// Package install 负责安装成 systemd 服务、注册到面板，以及干净地卸载（见 docs/node-agent.md 第 7、8 节）。
package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"moefrp/agent/internal/agentcfg"
	"moefrp/agent/internal/paths"
	"moefrp/shared/agentproto"
)

// Options 是 install 子命令的参数。
type Options struct {
	Server string
	Code   string // 一次性安装码；已安装且没有 Force 时可以为空
	NoExec bool
	Force  bool // 已安装时也重新注册
	Out    io.Writer
}

// unit 是 systemd 单元。令牌在 agent.toml 里，不写在这里。
const unit = `[Unit]
Description=MoeFrp 节点 agent（内嵌 frps）
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=%s run
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`

// Install 安装或更新 agent。
func Install(ctx context.Context, p paths.Paths, o Options) error {
	if err := preflight(p); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}

	_, statErr := os.Stat(p.AgentConfig())
	installed := statErr == nil
	switch {
	case installed && !o.Force:
		fmt.Fprintln(o.Out, "已经安装过，只更新程序并重启服务（要重新注册请加 --force）")
	default:
		if o.Code == "" {
			return errors.New("缺少安装码")
		}
		server, err := agentcfg.NormalizeServer(o.Server)
		if err != nil {
			return err
		}
		host, _ := os.Hostname()
		res, err := Enroll(ctx, server, o.Code, host)
		if err != nil {
			return err
		}
		cfg := agentcfg.Config{Server: server, NodeID: res.NodeID, Token: res.Token, Exec: !o.NoExec}
		if err := agentcfg.Save(p.AgentConfig(), cfg); err != nil {
			return err
		}
		fmt.Fprintf(o.Out, "已注册为节点「%s」（%s）\n", res.Name, res.NodeID)
		if o.NoExec {
			fmt.Fprintln(o.Out, "已关闭远程执行命令（--no-exec）")
		}
	}

	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return err
	}
	if err := installBinary(self, p.Bin); err != nil {
		return err
	}
	if err := agentcfg.WriteFileAtomic(p.Unit, []byte(fmt.Sprintf(unit, p.Bin)), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", p.UnitName()},
		{"restart", p.UnitName()},
	} {
		if err := systemctl(ctx, args...); err != nil {
			return err
		}
	}
	fmt.Fprintf(o.Out, "服务 %s 已启动。查看日志：journalctl -u %s -f\n", p.UnitName(), paths.ServiceName)
	return nil
}

func preflight(p paths.Paths) error {
	if os.Getenv("MOEFRP_AGENT_PREFIX") != "" {
		return nil // 测试
	}
	if os.Geteuid() != 0 {
		return errors.New("需要 root 权限，请用 sudo 运行")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("这台机器没有用 systemd，暂不支持")
	}
	return nil
}

func installBinary(self, dst string) error {
	if self == dst {
		return nil
	}
	b, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	return agentcfg.WriteFileAtomic(dst, b, 0o755)
}

// Enroll 用安装码向面板换取长期令牌。
func Enroll(ctx context.Context, server, code, hostname string) (agentproto.EnrollResponse, error) {
	var out agentproto.EnrollResponse
	body, _ := json.Marshal(agentproto.EnrollRequest{Code: strings.TrimSpace(code), Hostname: hostname})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+agentproto.RouteEnroll, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("连接面板 %s：%w", server, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			return out, fmt.Errorf("注册失败：%s", e.Error.Message)
		}
		return out, fmt.Errorf("注册失败：HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Token == "" || out.NodeID == "" {
		return out, errors.New("注册失败：面板返回的内容不正确")
	}
	return out, nil
}

// Uninstall 停止并删除服务和 agent 创建的所有文件。可以重复执行。
func Uninstall(ctx context.Context, p paths.Paths, out io.Writer) error {
	if err := preflight(p); err != nil {
		return err
	}
	// 失败也继续：服务可能已经不在了
	_ = systemctl(ctx, "disable", "--now", p.UnitName())
	var errs []error
	for _, path := range p.All() {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Fprintln(out, "已删除", path)
	}
	_ = systemctl(ctx, "daemon-reload")
	_ = systemctl(ctx, "reset-failed", p.UnitName())
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	fmt.Fprintf(out, "卸载完成。systemd 日志里的历史记录没有删除，需要的话可以执行：journalctl --vacuum-time=1s -u %s\n", p.UnitName())
	return nil
}

// UninstallDetached 由服务自身发起卸载：用 systemd-run 起一个独立的临时单元执行 uninstall，
// 这样停止 moefrp-agent 服务时不会把卸载过程一起杀掉。
func UninstallDetached(ctx context.Context, p paths.Paths) error {
	if os.Getenv("MOEFRP_AGENT_PREFIX") != "" {
		return errors.New("测试环境不支持")
	}
	// 程序先复制一份再执行：卸载会删除 /usr/local/bin/moefrp-agent。副本放在 StateDir 里
	// （/tmp 可能是 noexec），卸载删除 StateDir 时一起删掉
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.StateDir, "uninstall-*")
	if err != nil {
		return err
	}
	tmp.Close()
	if err := copyExec(p.Bin, tmp.Name()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	cmd := exec.CommandContext(ctx, "systemd-run", "--unit=moefrp-agent-uninstall", "--collect", "--quiet",
		"/bin/sh", "-c", fmt.Sprintf("%q uninstall; rm -f %q", tmp.Name(), tmp.Name()))
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("systemd-run：%v：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyExec(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, 0o700); err != nil {
		return err
	}
	// dst 可能是 CreateTemp 建的（0600），WriteFile 不会改已有文件的权限
	return os.Chmod(dst, 0o700)
}

func systemctl(ctx context.Context, args ...string) error {
	if os.Getenv("MOEFRP_AGENT_PREFIX") != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s：%v：%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
