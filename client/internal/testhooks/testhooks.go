// Package testhooks 集中管理只给端到端测试用的钩子（环境变量）。
//
// 这些钩子只在带 -tags e2e 的构建里生效（hooks_e2e.go）。正式构建（hooks_prod.go）里
// Get 永远返回零值：设置了环境变量也不会打开远程调试端口、不会换数据目录或服务器地址
// （docs/phase2.md 第 1 条、docs/phase3.md）。
package testhooks

// 环境变量名。
const (
	EnvHome        = "MOEFRP_HOME"                 // 把全部数据放到这个目录（Roaming、Local 两个子目录），单实例锁和凭据目标名也随之区分
	EnvDiag        = "MOEFRP_DIAG"                 // 把关键事件追加写到这个文件，自动化脚本据此判定结果
	EnvCDPPort     = "MOEFRP_CDP_PORT"             // 打开 WebView2 远程调试端口，让脚本通过 CDP 操作界面
	EnvBrowserPath = "MOEFRP_WEBVIEW_BROWSER_PATH" // 指定 WebView2 固定版本目录（模拟"缺少 WebView2"）
	EnvAPIBase     = "MOEFRP_API_BASE"             // 服务端地址，例如 http://127.0.0.1:18080
)

// Hooks 是读取到的钩子取值，空串表示没有设置。
type Hooks struct {
	Home        string
	Diag        string
	CDPPort     string
	BrowserPath string
	APIBase     string
}
