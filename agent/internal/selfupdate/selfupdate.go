// Package selfupdate 实现 agent 的升级和失败回滚（见 docs/node-agent.md 第 6 节）。
//
// 升级：下载 → 校验 SHA-256 → 运行新版本的 version 预检 → 旧版本复制成 .bak → 原子替换 → 退出，
// systemd 拉起新版本。新版本连上面板后调用 Finish 删除 .bak；启动了 maxAttempts 次还没确认，
// 或者启动后 confirmWithin 内没连上面板，就换回 .bak 再退出，由旧版本上报"升级失败，已回滚"。
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const (
	maxAttempts   = 3
	maxDownload   = 200 << 20
	ConfirmWithin = 3 * time.Minute
)

// State 是进行中的升级，存在 upgrade.json。
type State struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Attempts   int    `json:"attempts"`
	RolledBack bool   `json:"rolledBack,omitempty"`
	Error      string `json:"error,omitempty"`
}

func loadState(p paths.Paths) (*State, error) {
	b, err := os.ReadFile(p.UpgradeState())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveState(p paths.Paths, s *State) error {
	b, _ := json.Marshal(s)
	return agentcfg.WriteFileAtomic(p.UpgradeState(), b, 0o600)
}

// Prepare 下载并替换程序。返回 nil 后调用方应尽快退出进程（退出码非 0），让 systemd 用新版本重启。
func Prepare(ctx context.Context, p paths.Paths, req agentproto.UpgradeRequest, current string) error {
	if req.URL == "" || len(req.SHA256) != 64 || req.Version == "" {
		return errors.New("升级参数不完整")
	}
	if req.Version == current {
		return fmt.Errorf("已经是 %s", current)
	}
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return err
	}
	if err := download(ctx, req.URL, p.NewBin(), req.SHA256); err != nil {
		os.Remove(p.NewBin())
		return err
	}
	// 预检：新程序能在这台机器上运行，并且版本号和面板说的一致
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, p.NewBin(), "version").Output()
	if err != nil {
		os.Remove(p.NewBin())
		return fmt.Errorf("新版本无法运行：%w", err)
	}
	if v := strings.TrimSpace(string(out)); v != req.Version {
		os.Remove(p.NewBin())
		return fmt.Errorf("新版本报告的版本号是 %q，期望 %q", v, req.Version)
	}
	if err := copyFile(p.Bin, p.BackupBin(), 0o755); err != nil {
		os.Remove(p.NewBin())
		return fmt.Errorf("备份当前版本：%w", err)
	}
	if err := saveState(p, &State{From: current, To: req.Version}); err != nil {
		return err
	}
	if err := os.Rename(p.NewBin(), p.Bin); err != nil {
		os.Remove(p.UpgradeState())
		return fmt.Errorf("替换程序：%w", err)
	}
	return nil
}

func download(ctx context.Context, url, dst, wantHex string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载：HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("下载：%w", err)
	}
	if n > maxDownload {
		return errors.New("下载的文件太大")
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantHex) {
		return fmt.Errorf("SHA-256 不匹配：期望 %s，实际 %s", wantHex, got)
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return agentcfg.WriteFileAtomic(dst, b, perm)
}

// Startup 在 agent 启动时调用，处理进行中的升级。
//
// 返回 rollbackNow 为 true 时，已经换回了旧版本，调用方应立即退出让 systemd 重启旧版本。
func Startup(p paths.Paths, current string) (pending *State, rollbackNow bool, err error) {
	s, err := loadState(p)
	if err != nil || s == nil {
		return nil, false, err
	}
	if s.RolledBack || current != s.To {
		// 旧版本（回滚后，或者替换没成功）在运行：等连上面板后上报失败
		return s, false, nil
	}
	s.Attempts++
	if s.Attempts > maxAttempts {
		return s, true, Rollback(p, s, fmt.Sprintf("新版本启动 %d 次都没能连上面板", maxAttempts))
	}
	return s, false, saveState(p, s)
}

// Rollback 换回 .bak。
func Rollback(p paths.Paths, s *State, reason string) error {
	s.RolledBack, s.Error = true, reason
	if err := saveState(p, s); err != nil {
		return err
	}
	if err := os.Rename(p.BackupBin(), p.Bin); err != nil {
		return fmt.Errorf("回滚：%w", err)
	}
	return nil
}

// Result 把进行中的升级换算成上报给面板的结果。
func Result(s *State, current string) *agentproto.UpgradeResult {
	if s == nil {
		return nil
	}
	r := &agentproto.UpgradeResult{From: s.From, To: s.To, RolledBack: s.RolledBack, Error: s.Error}
	r.OK = !s.RolledBack && current == s.To
	if !r.OK && r.Error == "" {
		r.Error = "新版本没有生效"
	}
	return r
}

// Finish 在结果已经报给面板后调用：删除 .bak 和状态文件。
func Finish(p paths.Paths) {
	os.Remove(p.BackupBin())
	os.Remove(p.UpgradeState())
	os.Remove(p.NewBin())
}
