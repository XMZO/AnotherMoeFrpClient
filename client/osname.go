package main

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 上报给面板的设备信息（管理后台"用户 → 登录设备"、"客户端"页显示）。都只读注册表和系统接口，读不到的留空。

// osName 是系统名称，例如 "Windows 11 专业版 24H2"。读不到时用 GOOS/GOARCH。
func osName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	defer k.Close()
	name, _, _ := k.GetStringValue("ProductName")
	// Windows 11 的 ProductName 仍然写着 Windows 10，按内部版本号改过来
	if build, _, err := k.GetStringValue("CurrentBuildNumber"); err == nil && len(build) >= 5 && build >= "22000" {
		name = strings.Replace(name, "Windows 10", "Windows 11", 1)
	}
	if v, _, err := k.GetStringValue("DisplayVersion"); err == nil && v != "" {
		name += " " + v
	}
	if name == "" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return name
}

// osBuild 是完整的内部版本号，例如 10.0.26100.2894。
func osBuild() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	major, _, err1 := k.GetIntegerValue("CurrentMajorVersionNumber")
	minor, _, err2 := k.GetIntegerValue("CurrentMinorVersionNumber")
	build, _, err3 := k.GetStringValue("CurrentBuildNumber")
	if err1 != nil || err2 != nil || err3 != nil {
		return build
	}
	out := fmt.Sprintf("%d.%d.%s", major, minor, build)
	if ubr, _, err := k.GetIntegerValue("UBR"); err == nil {
		out += fmt.Sprintf(".%d", ubr)
	}
	return out
}

// cpuName 是处理器型号。
func cpuName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("ProcessorNameString")
	return strings.Join(strings.Fields(v), " ")
}

// machineModel 是电脑的品牌和型号，例如 "LENOVO 21D0"。
func machineModel() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	maker, _, _ := k.GetStringValue("SystemManufacturer")
	product, _, _ := k.GetStringValue("SystemProductName")
	// 组装机的 BIOS 里常是占位文字，改用主板型号
	if placeholder(product) {
		bm, _, _ := k.GetStringValue("BaseBoardManufacturer")
		bp, _, _ := k.GetStringValue("BaseBoardProduct")
		if !placeholder(bp) {
			maker, product = bm, bp+"（主板）"
		}
	}
	if placeholder(maker) {
		maker = ""
	}
	if placeholder(product) {
		product = ""
	}
	return strings.TrimSpace(strings.TrimSpace(maker) + " " + strings.TrimSpace(product))
}

func placeholder(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "" || strings.Contains(s, "system product name") || strings.Contains(s, "to be filled") ||
		strings.Contains(s, "default string") || s == "system manufacturer" || s == "o.e.m."
}

// memoryMB 是物理内存总量（MB）。
func memoryMB() int {
	// MEMORYSTATUSEX
	var m struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	m.Length = uint32(unsafe.Sizeof(m))
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0
	}
	return int(m.TotalPhys >> 20)
}
