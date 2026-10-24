package main

import (
	"runtime"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// osName 是上报给面板的系统名称，例如 "Windows 11 专业版 24H2"（后台"客户端"页显示）。读不到时用 GOOS/GOARCH。
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
