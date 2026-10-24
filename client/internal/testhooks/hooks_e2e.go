//go:build e2e

package testhooks

import (
	"os"
	"path/filepath"
)

// Enabled 表示当前是不是端到端测试构建。
const Enabled = true

// Get 读取测试钩子的环境变量。
//
// 没设 MOEFRP_HOME 时（例如直接双击测试版 exe）也不用正式版的数据目录：放到临时目录下的 MoeFrp-e2e，
// 单实例锁和凭据目标名也随之区分。否则测试版会和正式版共用单实例，双击正式版只会唤出测试版的窗口，
// 测试版还会读写正式版的配置和登录状态。
func Get() Hooks {
	home := os.Getenv(EnvHome)
	if home == "" {
		home = filepath.Join(os.TempDir(), "MoeFrp-e2e")
	}
	return Hooks{
		Home:        home,
		Diag:        os.Getenv(EnvDiag),
		CDPPort:     os.Getenv(EnvCDPPort),
		BrowserPath: os.Getenv(EnvBrowserPath),
		APIBase:     os.Getenv(EnvAPIBase),
	}
}
