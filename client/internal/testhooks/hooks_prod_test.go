//go:build !e2e

package testhooks

import "testing"

// 正式构建里设置了环境变量也不能生效。
func TestProdBuildIgnoresEnv(t *testing.T) {
	for _, k := range []string{EnvHome, EnvDiag, EnvCDPPort, EnvBrowserPath, EnvAPIBase} {
		t.Setenv(k, "x")
	}
	if Enabled || Get() != (Hooks{}) {
		t.Fatalf("hooks must be disabled in non-e2e builds: %+v", Get())
	}
}
