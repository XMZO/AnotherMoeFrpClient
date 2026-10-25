package vnet

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
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

// configureAdapter 给虚拟网卡配地址：静态 IP、/24、不设网关（不影响默认路由），再加一个 IPv6 地址（/64）；
// 局域网发现的广播 / 组播优先从这块网卡发出（setLanPriority）；网络类别设成"专用"，防火墙不按公用网络拦。
func configureAdapter(name, ip, ip6 string) error {
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
	// MTU：Wintun 网卡默认 65535，TCP 会按它算出很大的 MSS，发出比 WireGuard 能带的大得多的包，
	// 大包全丢（IPv6 的连接直接卡死，IPv4 靠重传也慢得多）。设成和 WireGuard 一样的 mtu（IPv4、IPv6 各设一次）。
	for _, v := range []string{"ipv4", "ipv6"} {
		_ = run("netsh", "interface", v, "set", "subinterface", name, "mtu="+strconv.Itoa(mtu), "store=active")
	}
	// IPv6 地址：没有 IPv6（系统里关掉了）也能用，只是用 IPv6 的游戏连不了
	_ = run("netsh", "interface", "ipv6", "add", "address", "interface="+name, "address="+ip6+"/64", "store=active")
	for i := 0; i < 6 && !setLanPriority(name); i++ { // 地址刚配上，系统的路由可能还没生成
		time.Sleep(300 * time.Millisecond)
	}
	go func() { // 慢（PowerShell 启动要一两秒），不等
		_ = run("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"Set-NetConnectionProfile -InterfaceAlias '"+name+"' -NetworkCategory Private")
	}()
	return nil
}

// lanTargets 是局域网发现用的地址：MC Java 的组播 224.0.2.60，和受限广播。
var lanTargets = [][4]byte{{224, 0, 2, 60}, {255, 255, 255, 255}}

// setLanPriority 让局域网发现的组播（224.0.0.0/4）和受限广播（255.255.255.255）优先从这块网卡发出。
// 程序没指定网卡时，Windows 按"路由跃点 + 网卡跃点"选出口；Radmin VPN、ZeroTier 等跃点也很低，打平时包会从它们那里出去，
// 房间里的人就发现不了。这里把网卡跃点和这两条路由的跃点都设成 0（最小值；只改当前，网卡删掉就没了）。
func setLanPriority(name string) bool {
	_ = run("netsh", "interface", "ipv4", "set", "interface", "interface="+name, "metric=0")
	ok := true
	for _, r := range []string{"224.0.0.0/4", "255.255.255.255/32"} {
		if run("netsh", "interface", "ipv4", "set", "route", r, "interface="+name, "metric=0", "store=active") != nil {
			ok = false
		}
	}
	// IPv6 的组播（新版 MC 开了 IPv6 优先时用 ff75:230::60）同样优先从这块网卡出；失败不算（可能没有 IPv6）
	_ = run("netsh", "interface", "ipv6", "set", "interface", "interface="+name, "metric=0")
	_ = run("netsh", "interface", "ipv6", "set", "route", "ff00::/8", "interface="+name, "metric=0", "store=active")
	return ok
}

// lanPriorityOK 检查局域网发现的包现在是不是从这块网卡出去（Windows 的 GetBestInterfaceEx）。
func lanPriorityOK(name string) bool {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return true // 网卡不在了：不用管
	}
	for _, a := range lanTargets {
		var idx uint32
		if windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: a}, &idx) != nil {
			continue
		}
		if int(idx) != ifc.Index {
			return false
		}
	}
	return true
}

// keepLanPriority 在房间里时每隔几秒检查一次：别的联机工具（Radmin、ZeroTier、Tailscale 等）后开、
// 或者把路由重置了，出口不是这块网卡就重新设置。
// 如果别的工具也在反复抢（1 分钟里被抢超过 3 次），停 5 分钟再说，免得两边每隔几秒互相改。
func keepLanPriority(name string, done <-chan struct{}, logf func(string, ...any)) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var fixes []time.Time
	var pauseUntil time.Time
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			if now.Before(pauseUntil) || lanPriorityOK(name) {
				continue
			}
			fixes = append(fixes, now)
			for len(fixes) > 0 && now.Sub(fixes[0]) > time.Minute {
				fixes = fixes[1:]
			}
			if len(fixes) > 3 {
				logf("vnet: 局域网发现的出口一直被别的网卡抢（可能另一个联机工具也在抢），停 5 分钟")
				pauseUntil, fixes = now.Add(5*time.Minute), nil
				continue
			}
			logf("vnet: 局域网发现的出口被别的网卡抢了，重新设置")
			setLanPriority(name)
		}
	}
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
