package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"slices"
	"sync"

	"moefrp/shared/model"
)

// 设置项的取值。
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"

	CloseAsk    = "ask" // 每次点关闭都询问（询问窗口里勾"记住"后改成下面两个之一）
	CloseToTray = "tray"
	CloseQuit   = "quit"

	// 应用自身 HTTP 请求（API、图片）的代理模式（PLAN 6.10）。
	NetProxySystem = "system"
	NetProxyNone   = "none"
	NetProxyCustom = "custom" // 使用 ProxyURL（http://、https://、socks5://）

	// frp 连接节点时走不走上面的网络代理。节点自己填了代理（节点设置里的 proxyURL）时总是用节点的。
	FrpProxyDirect = "direct" // 直连，不走任何代理（默认；也不读 http_proxy 环境变量）
	FrpProxyApp    = "app"    // 跟随网络代理（系统 / 自定义；系统代理只支持 http 和 socks）
)

var (
	Themes       = []string{ThemeSystem, ThemeLight, ThemeDark}
	CloseActions = []string{CloseAsk, CloseToTray, CloseQuit}
	LogLevels    = []string{"trace", "debug", "info", "warn", "error"}
	NetProxies   = []string{NetProxySystem, NetProxyNone, NetProxyCustom}
)

// settingsSchema 2：新增 proxyUrl、autoStart、imagePanel（Phase 5）。
// 3：点关闭按钮默认改为每次询问。
const settingsSchema = 3

// Settings 是 %APPDATA%\MoeFrp\settings.json 的内容：明文，不含任何机密（PLAN 6.5）。
type Settings struct {
	SchemaVersion int    `json:"schemaVersion"`
	InstallID     string `json:"installId"` // 首次运行时生成的随机 UUID，Phase 3 起作为 X-Install-Id 发给服务端
	Theme         string `json:"theme"`
	CloseAction   string `json:"closeAction"`
	LogLevel      string `json:"logLevel"`
	// 上次启动的配置和每个配置上次选的节点，用于运行栏的默认选择和托盘的"启动上次的配置"。
	LastProfileID string            `json:"lastProfileId,omitempty"`
	LastNodeIDs   map[string]string `json:"lastNodeIds,omitempty"`
	// AutoConnect：启动后自动连接上次的配置（PLAN 6.8，默认关闭）。
	AutoConnect bool `json:"autoConnect"`
	// NetworkProxy：应用自身 HTTP 请求（API、图片）的代理：system（跟随系统，默认）、none、custom。
	NetworkProxy string `json:"networkProxy"`
	// ProxyURL：NetworkProxy 为 custom 时使用的代理，例如 http://127.0.0.1:7890、socks5://127.0.0.1:1080。
	ProxyURL string `json:"proxyUrl,omitempty"`
	// FrpProxy：frp 连接节点时走不走 NetworkProxy：direct（默认）、app。
	FrpProxy string `json:"frpProxy,omitempty"`
	// AutoStart：开机自启（写 HKCU\...\Run，PLAN 6.8，默认关闭）。实际的注册表状态以启动时核对为准。
	AutoStart bool `json:"autoStart"`
	// ImagePanel：是否显示图片面板（PLAN 6.9、13 节 #4，默认开启）。关闭后不发任何图片请求。
	ImagePanel bool `json:"imagePanel"`
	// ReleaseOnHide：隐藏到托盘时关闭窗口、释放 WebView2（约 200MB 内存），从托盘打开时重新创建（PLAN 6.8，默认开启）。
	ReleaseOnHide bool `json:"releaseOnHide"`
}

// ValidProxyURL 检查自定义代理地址：http、https 或 socks5，必须有主机和端口。
func ValidProxyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Port() == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "socks5"
}

// DefaultSettings 是首次运行时的设置。
func DefaultSettings() Settings {
	return Settings{
		SchemaVersion: settingsSchema,
		InstallID:     model.NewID(),
		Theme:         ThemeSystem,
		CloseAction:   CloseAsk,
		LogLevel:      "info",
		NetworkProxy:  NetProxySystem,
		ImagePanel:    true,
		ReleaseOnHide: true,
	}
}

// Validate 检查枚举值。
func (s Settings) Validate() error {
	var errs []error
	if !slices.Contains(Themes, s.Theme) {
		errs = append(errs, fmt.Errorf("theme: unknown value %q", s.Theme))
	}
	if !slices.Contains(CloseActions, s.CloseAction) {
		errs = append(errs, fmt.Errorf("closeAction: unknown value %q", s.CloseAction))
	}
	if !slices.Contains(LogLevels, s.LogLevel) {
		errs = append(errs, fmt.Errorf("logLevel: unknown value %q", s.LogLevel))
	}
	if !slices.Contains(NetProxies, s.NetworkProxy) {
		errs = append(errs, fmt.Errorf("networkProxy: unknown value %q", s.NetworkProxy))
	}
	if s.NetworkProxy == NetProxyCustom && !ValidProxyURL(s.ProxyURL) {
		errs = append(errs, fmt.Errorf("proxyUrl: %q is not a valid http/https/socks5 proxy url", s.ProxyURL))
	}
	return errors.Join(errs...)
}

// fill 把缺失或非法的字段补成默认值（旧版本写的文件、手工改坏的文件）。
func (s *Settings) fill() {
	d := DefaultSettings()
	if s.InstallID == "" {
		s.InstallID = d.InstallID
	}
	if !slices.Contains(Themes, s.Theme) {
		s.Theme = d.Theme
	}
	if !slices.Contains(CloseActions, s.CloseAction) {
		s.CloseAction = d.CloseAction
	}
	if !slices.Contains(LogLevels, s.LogLevel) {
		s.LogLevel = d.LogLevel
	}
	if !slices.Contains(NetProxies, s.NetworkProxy) || (s.NetworkProxy == NetProxyCustom && !ValidProxyURL(s.ProxyURL)) {
		s.NetworkProxy = d.NetworkProxy
	}
	if s.SchemaVersion < 2 { // 旧文件没有 imagePanel、releaseOnHide，按默认开启
		s.ImagePanel = true
		s.ReleaseOnHide = true
	}
	if s.SchemaVersion < 3 && s.CloseAction == CloseToTray { // 以前的默认值，不是用户选的：改成每次询问，勾"记住"后不再问
		s.CloseAction = CloseAsk
	}
	s.SchemaVersion = settingsSchema
}

// SettingsStore 读写 settings.json，并发安全。
type SettingsStore struct {
	path string
	mu   sync.Mutex
	cur  Settings
}

// OpenSettings 读取设置；文件不存在时用默认值并立即写入（生成 installId）。
// 文件损坏时改名备份、用默认值，warning 说明发生了什么。
func OpenSettings(path string) (st *SettingsStore, warning string, err error) {
	st = &SettingsStore{path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		st.cur = DefaultSettings()
		return st, "", st.saveLocked()
	case err != nil:
		return nil, "", err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		bad := quarantine(path)
		st.cur = DefaultSettings()
		return st, fmt.Sprintf("settings.json is corrupt (%v), reset to defaults; old file kept as %s", err, bad), st.saveLocked()
	}
	before := s
	s.fill()
	st.cur = s
	if before.InstallID != s.InstallID || before.SchemaVersion != s.SchemaVersion {
		return st, "", st.saveLocked()
	}
	return st, "", nil
}

// Get 返回当前设置的副本。
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.clone()
}

// Update 在锁内修改设置并保存；fn 返回错误时不保存。
func (s *SettingsStore) Update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur.clone()
	if err := fn(&next); err != nil {
		return s.cur.clone(), err
	}
	next.SchemaVersion = settingsSchema
	if next.InstallID == "" {
		next.InstallID = s.cur.InstallID
	}
	if err := next.Validate(); err != nil {
		return s.cur.clone(), err
	}
	prev := s.cur
	s.cur = next
	if err := s.saveLocked(); err != nil {
		s.cur = prev
		return prev.clone(), err
	}
	return next.clone(), nil
}

func (s *SettingsStore) saveLocked() error {
	data, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(data, '\n'))
}

func (s Settings) clone() Settings {
	s.LastNodeIDs = maps.Clone(s.LastNodeIDs)
	return s
}
