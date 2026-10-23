//go:build e2e

package testhooks

import "testing"

func TestE2EBuildReadsEnv(t *testing.T) {
	t.Setenv(EnvHome, `C:\tmp\a`)
	t.Setenv(EnvCDPPort, "9350")
	h := Get()
	if !Enabled || h.Home != `C:\tmp\a` || h.CDPPort != "9350" {
		t.Fatalf("got %+v", h)
	}
}
