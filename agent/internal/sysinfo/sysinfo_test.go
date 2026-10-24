package sysinfo

import "testing"

func TestParsers(t *testing.T) {
	stat := "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3 4\nbtime 1700000000\n"
	idle, total, ok := parseCPU(stat)
	if !ok || idle != 800 || total != 1000 {
		t.Fatalf("cpu: %d %d %v", idle, total, ok)
	}
	if parseBootTime(stat) != 1700000000 {
		t.Fatal("btime")
	}
	var s Sampler
	if s.cpuPercent(800, 1000) != 0 {
		t.Fatal("第一次采样为 0")
	}
	if p := s.cpuPercent(850, 1100); p != 50 {
		t.Fatalf("cpu%%: %v", p)
	}
	tot, used := parseMeminfo("MemTotal:       2000 kB\nMemFree: 100 kB\nMemAvailable:   500 kB\n")
	if tot != 2000*1024 || used != 1500*1024 {
		t.Fatalf("mem: %d %d", tot, used)
	}
	if l := parseLoad("0.50 1.00 2.25 1/100 123"); l != [3]float64{0.5, 1, 2.25} {
		t.Fatalf("load: %v", l)
	}
	dev := "Inter-|   Receive |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes\n    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n  eth0: 100 1 0 0 0 0 0 0 200 1 0 0 0 0 0 0\n  eth1: 10 1 0 0 0 0 0 0 20 1 0 0 0 0 0 0\n docker0: 5000 1 0 0 0 0 0 0 5000 1 0 0 0 0 0 0\nveth0be5b5c: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\nbr-f1c8c0c790d9: 9000 1 0 0 0 0 0 0 9000 1 0 0 0 0 0 0\n"
	// 回环和容器、网桥这些虚拟网卡不算（流量也经过物理网卡，会重复计算）
	if rx, tx := parseNetDev(dev); rx != 110 || tx != 220 {
		t.Fatalf("net: %d %d", rx, tx)
	}
	if parseOSRelease("NAME=x\nPRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n") != "Debian GNU/Linux 13 (trixie)" {
		t.Fatal("os-release")
	}
	if parseUptime("12345.67 999") != 12345 {
		t.Fatal("uptime")
	}
}
