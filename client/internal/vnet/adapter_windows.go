package vnet

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

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
