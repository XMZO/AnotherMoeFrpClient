//go:build !e2e

package testhooks

// Enabled 表示当前是不是端到端测试构建。
const Enabled = false

// Get 在正式构建里永远返回零值。
func Get() Hooks { return Hooks{} }
