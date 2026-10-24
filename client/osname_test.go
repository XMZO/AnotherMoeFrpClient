package main

import "testing"

// 设备信息都能读到（在 Windows 上跑）
func TestDeviceInfo(t *testing.T) {
	t.Logf("os=%q build=%q cpu=%q mem=%d model=%q", osName(), osBuild(), cpuName(), memoryMB(), machineModel())
	if osBuild() == "" || cpuName() == "" || memoryMB() <= 0 {
		t.Fatal("设备信息读不到")
	}
}
