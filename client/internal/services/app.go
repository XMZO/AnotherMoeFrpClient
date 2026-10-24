package services

import (
	"context"
	"net/url"
	"runtime"
	"time"

	"moefrp/client/internal/apiclient"

	frpversion "github.com/fatedier/frp/pkg/util/version"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/store"
)

// WailsVersion 是锁定的 Wails 版本（PLAN 6.1）。
const WailsVersion = "v3.0.0-beta.31"

// VersionInfo 给"关于"页用。
type VersionInfo struct {
	App   string `json:"app"`
	Frp   string `json:"frp"`
	Wails string `json:"wails"`
	Go    string `json:"go"`
}

// StartupInfo 是前端启动时需要的一次性信息。
type StartupInfo struct {
	Version  VersionInfo `json:"version"`
	DataDir  string      `json:"dataDir"`
	LocalDir string      `json:"localDir"`
	// 显示用的短写法（%APPDATA%\MoeFrp 这样），完整路径用来悬停和复制
	DataDirShort  string   `json:"dataDirShort"`
	LocalDirShort string   `json:"localDirShort"`
	Warnings      []string `json:"warnings"` // 启动时发现的问题，例如缓存解不开被重置
}

// AppService：版本、检查更新、开源许可、打开链接、退出等。
type AppService struct {
	version  string
	paths    store.Paths
	warnings []string
	started  time.Time

	// 由 main 注入
	OpenBrowser func(string) error
	QuitApp     func()
	CloseAs     func(action string) // 按询问窗口的选择关闭主窗口：tray 或 quit
	OnReady     func(sinceStart time.Duration)
	Updater     *Updater
	Notices     *Notices // 管理员推送的通知
}

// CheckUpdate 立即检查更新（"关于"页的按钮）。
func (s *AppService) CheckUpdate() (UpdateInfo, error) {
	if s.Updater == nil {
		return UpdateInfo{Current: s.version}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	return s.Updater.Check(ctx)
}

// UpdateInfo 返回最近一次检查更新的结果（不发请求）。
func (s *AppService) UpdateInfo() UpdateInfo {
	if s.Updater == nil {
		return UpdateInfo{Current: s.version}
	}
	return s.Updater.Info()
}

// License 是一个开源组件的许可信息（"关于"页）。
type License struct {
	Name    string `json:"name"`
	License string `json:"license"`
	URL     string `json:"url"`
	Note    string `json:"note,omitempty"`
}

// Licenses 返回客户端用到的主要开源组件。
// "关于"页分两列显示，条数保持偶数，两列最后一行才对齐（不留空、不拉伸）。
func (s *AppService) Licenses() []License {
	return []License{
		{"frp", "Apache License 2.0", "https://github.com/fatedier/frp", "内嵌的 frp 客户端，版本见上方"},
		{"Wails", "MIT", "https://github.com/wailsapp/wails", ""},
		{"Go 标准库及 golang.org/x", "BSD-3-Clause", "https://go.dev", ""},
		{"Vue", "MIT", "https://github.com/vuejs/core", ""},
		{"Pinia", "MIT", "https://github.com/vuejs/pinia", ""},
		{"Nunito、JetBrains Mono 字体", "SIL OFL 1.1", "https://fontsource.org", "界面字体"},
		{"wincred", "MIT", "https://github.com/danieljoos/wincred", "Windows 凭据管理器"},
		{"golang.design/x/clipboard", "MIT", "https://github.com/golang-design/clipboard", "复制图片"},
		{"quic-go、kcp-go 等", "MIT", "https://github.com/fatedier/frp/blob/master/go.mod", "frp 的依赖"},
		{"yamux", "MPL-2.0", "https://github.com/fatedier/yamux", "frp 的多路复用"},
	}
}

func NewAppService(version string, paths store.Paths, warnings []string, processStart time.Time) *AppService {
	return &AppService{version: version, paths: paths, warnings: warnings, started: processStart}
}

func (s *AppService) Version() VersionInfo {
	return VersionInfo{App: s.version, Frp: frpversion.Full(), Wails: WailsVersion, Go: runtime.Version()}
}

func (s *AppService) Startup() StartupInfo {
	w := s.warnings
	if w == nil {
		w = []string{}
	}
	return StartupInfo{Version: s.Version(), DataDir: s.paths.Roaming, LocalDir: s.paths.Local,
		DataDirShort: shortPath(s.paths.Roaming), LocalDirShort: shortPath(s.paths.Local), Warnings: w}
}

// Ready 由前端挂载完成后调用一次，返回从进程创建到现在的毫秒数（记录启动耗时）。
func (s *AppService) Ready() int64 {
	d := time.Since(s.started)
	if s.OnReady != nil {
		s.OnReady(d)
	}
	return d.Milliseconds()
}

// OpenURL 用系统浏览器打开链接，只允许 http / https。
func (s *AppService) OpenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return apperr.New(apperr.InvalidArg, "only http(s) urls can be opened")
	}
	if s.OpenBrowser == nil {
		return apperr.New(apperr.InvalidArg, "browser not available")
	}
	return apperr.Normalize(s.OpenBrowser(u.String()))
}

// EventCloseAsk：点了关闭按钮、设置是"每次询问"时推给前端，由前端弹出询问窗口。
const EventCloseAsk = "app:close-ask"

// CloseWindow 按询问窗口的选择关闭主窗口：tray（最小化到托盘）或 quit（退出程序）。
// "记住我的选择"由前端先写进设置（closeAction），这里只执行这一次。
func (s *AppService) CloseWindow(action string) error {
	if action != store.CloseToTray && action != store.CloseQuit {
		return apperr.New(apperr.InvalidArg, "action must be tray or quit")
	}
	if s.CloseAs != nil {
		s.CloseAs(action)
	}
	return nil
}

// Quit 退出程序（与托盘的"退出"相同：先停隧道）。
func (s *AppService) Quit() {
	if s.QuitApp != nil {
		s.QuitApp()
	}
}
