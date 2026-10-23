//go:build e2e

package testhooks

import "os"

// Enabled 表示当前是不是端到端测试构建。
const Enabled = true

// Get 读取测试钩子的环境变量。
func Get() Hooks {
	return Hooks{
		Home:        os.Getenv(EnvHome),
		Diag:        os.Getenv(EnvDiag),
		CDPPort:     os.Getenv(EnvCDPPort),
		BrowserPath: os.Getenv(EnvBrowserPath),
		APIBase:     os.Getenv(EnvAPIBase),
	}
}
