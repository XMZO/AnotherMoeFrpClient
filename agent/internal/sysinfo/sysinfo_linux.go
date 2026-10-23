package sysinfo

import (
	"os"
	"strings"
	"syscall"
)

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// ReadStatic 读取系统版本、内核、主机名、开机时间。
func ReadStatic() Static {
	host, _ := os.Hostname()
	return Static{
		Distro:   parseOSRelease(read("/etc/os-release")),
		Kernel:   strings.TrimSpace(read("/proc/sys/kernel/osrelease")),
		Hostname: host,
		BootTime: parseBootTime(read("/proc/stat")),
	}
}

// Sample 采样一次资源占用。CPU 占用率是和上一次调用之间的平均值，第一次为 0。
func (s *Sampler) Sample() Usage {
	var u Usage
	if idle, total, ok := parseCPU(read("/proc/stat")); ok {
		u.CPU = s.cpuPercent(idle, total)
	}
	u.Uptime = parseUptime(read("/proc/uptime"))
	u.Load = parseLoad(read("/proc/loadavg"))
	u.MemTotal, u.MemUsed = parseMeminfo(read("/proc/meminfo"))
	u.NetRx, u.NetTx = parseNetDev(read("/proc/net/dev"))
	var st syscall.Statfs_t
	if syscall.Statfs("/", &st) == nil {
		bs := uint64(st.Bsize)
		u.DiskTotal = st.Blocks * bs
		u.DiskUsed = (st.Blocks - st.Bfree) * bs
	}
	return u
}
