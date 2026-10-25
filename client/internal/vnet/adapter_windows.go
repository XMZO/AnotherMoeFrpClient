package vnet

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/tun"

	"moefrp/shared/api"
)

// IsAdmin 判断当前进程有没有管理员权限（建虚拟网卡要）。
func IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// configureAdapter 给虚拟网卡配地址：静态 IP、/24、不设网关（不影响默认路由）；
// 跃点数设成 1，游戏的局域网广播从这块网卡发出；网络类别设成"专用"，防火墙不按公用网络拦。
func configureAdapter(name, ip string) error {
	var err error
	for i := 0; i < 10; i++ { // 网卡刚建出来，系统可能还没准备好
		if err = run("netsh", "interface", "ipv4", "set", "address", "name="+name, "source=static",
			"address="+ip, "mask="+api.VnetMask, "gateway=none"); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	_ = run("netsh", "interface", "ipv4", "set", "interface", "interface="+name, "metric=1")
	// 组播（224.0.0.0/4，MC 的局域网发现发到 224.0.2.60）和受限广播（255.255.255.255）：程序没指定网卡时，
	// Windows 按"路由跃点 + 网卡跃点"选出口。别的虚拟网卡（Radmin VPN 等）跃点也是 1 时会打平，包从它那里发出去，
	// 房间里的人就发现不了。把这块网卡上这两条路由的跃点改成 0（只改当前，网卡删掉就没了）。
	for _, r := range []string{"224.0.0.0/4", "255.255.255.255/32"} {
		for i := 0; i < 6; i++ { // 地址刚配上，系统的路由可能还没生成
			if run("netsh", "interface", "ipv4", "set", "route", r, "interface="+name, "metric=0", "store=active") == nil {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	go func() { // 慢（PowerShell 启动要一两秒），不等
		_ = run("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"Set-NetConnectionProfile -InterfaceAlias '"+name+"' -NetworkCategory Private")
	}()
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DriverInstalled 判断 Wintun 驱动装好了没有：看注册表里有没有 wintun 驱动服务。
// 不用 wintun.RunningVersion：那会加载 wintun.dll（只是打开联机页也加载），没必要。
func DriverInstalled() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\wintun`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_ = k.Close()
	return true
}

// Shutdown 在程序退出前调用：清掉 wintun 的日志回调（见 wintun.DisableLogger）。
func Shutdown() { wintun.DisableLogger() }

// InstallDriver 装 Wintun 驱动：建一块虚拟网卡再关掉（第一次建网卡时驱动自动装好）。要管理员权限。
func InstallDriver() error {
	t, err := tun.CreateTUN(AdapterName, mtu)
	if err != nil {
		return fmt.Errorf("安装虚拟网卡驱动失败：%w", err)
	}
	return t.Close()
}
