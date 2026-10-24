//go:build e2e

package testhooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestE2EBuildReadsEnv(t *testing.T) {
	t.Setenv(EnvHome, `C:\tmp\a`)
	t.Setenv(EnvCDPPort, "9350")
	h := Get()
	if !Enabled || h.Home != `C:\tmp\a` || h.CDPPort != "9350" {
		t.Fatalf("got %+v", h)
	}
}

// 没设 MOEFRP_HOME 时也不能落到正式版的数据目录（和正式版共用单实例、配置、登录状态）
func TestE2EBuildNeverUsesDefaultHome(t *testing.T) {
	t.Setenv(EnvHome, "")
	if h := Get(); h.Home != filepath.Join(os.TempDir(), "MoeFrp-e2e") {
		t.Fatalf("got %q", h.Home)
	}
}
