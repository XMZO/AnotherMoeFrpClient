// Package sysinfo 读取系统信息和资源占用（Linux 下读 /proc，不依赖第三方库）。
package sysinfo

import (
	"bufio"
	"strconv"
	"strings"
	"sync"
)

// Static 是不常变化的信息，连接时上报一次。
type Static struct {
	Distro   string
	Kernel   string
	Hostname string
	BootTime int64
}

// Usage 是一次采样。
type Usage struct {
	Uptime    int64
	CPU       float64
	Load      [3]float64
	MemTotal  uint64
	MemUsed   uint64
	DiskTotal uint64
	DiskUsed  uint64
	NetRx     uint64
	NetTx     uint64
}

// Sampler 记住上一次的 CPU 计数，用差值算占用率。
type Sampler struct {
	mu              sync.Mutex
	prevIdle, prevT uint64
}

// cpuPercent 由两次 /proc/stat 的差值算出占用率。
func (s *Sampler) cpuPercent(idle, total uint64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.prevIdle, s.prevT = idle, total }()
	if s.prevT == 0 || total <= s.prevT {
		return 0
	}
	dt, di := float64(total-s.prevT), float64(idle-s.prevIdle)
	if di > dt {
		return 0
	}
	return (dt - di) / dt * 100
}

// parseCPU 解析 /proc/stat 的第一行 "cpu  user nice system idle iowait irq softirq steal ..."。
func parseCPU(stat string) (idle, total uint64, ok bool) {
	line, _, _ := strings.Cut(stat, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range f[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if i >= 8 { // guest、guest_nice 已经算在 user、nice 里
			break
		}
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return idle, total, true
}

// parseBootTime 取 /proc/stat 的 btime。
func parseBootTime(stat string) int64 {
	sc := bufio.NewScanner(strings.NewReader(stat))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// parseMeminfo 返回总内存和已用内存（总量减去 MemAvailable），单位字节。
func parseMeminfo(s string) (total, used uint64) {
	var avail uint64
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	if avail > total {
		avail = total
	}
	return total, total - avail
}

// parseLoad 解析 /proc/loadavg。
func parseLoad(s string) (l [3]float64) {
	f := strings.Fields(s)
	for i := 0; i < 3 && i < len(f); i++ {
		l[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return l
}

// parseNetDev 汇总 /proc/net/dev 里除 lo 以外所有网卡的收发字节数。
func parseNetDev(s string) (rx, tx uint64) {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if virtualNIC(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// virtualNICPrefixes 是不计入流量的网卡：回环，以及容器、网桥、虚拟机、隧道这些虚拟网卡——
// 经过它们的流量也会经过物理网卡，加在一起就重复计算了（容器的流量会被算三遍）。
var virtualNICPrefixes = []string{"lo", "docker", "veth", "br-", "virbr", "vnet", "vmbr", "cni", "flannel", "cali", "kube",
	"tun", "tap", "wg", "tailscale", "zt", "ifb", "dummy"}

func virtualNIC(name string) bool {
	for _, p := range virtualNICPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// parseOSRelease 取 /etc/os-release 的 PRETTY_NAME。
func parseOSRelease(s string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// parseUptime 解析 /proc/uptime 的第一个数。
func parseUptime(s string) int64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}
