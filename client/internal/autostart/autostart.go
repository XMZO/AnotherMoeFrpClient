// Package autostart 管理开机自启（PLAN 6.8）：在 HKCU\Software\Microsoft\Windows\CurrentVersion\Run
// 下写一个值，内容是当前 exe 的路径加 --autostart。用户级设置，不需要管理员权限。
package autostart

import "strings"

// Flag 是开机自启时附加的命令行参数：带着它启动时只显示托盘，不弹出主窗口。
const Flag = "--autostart"

// Entry 描述一个自启项。
type Entry struct {
	Name string // Run 键下的值名，例如 MoeFrp
	Exe  string // exe 的绝对路径
}

// Command 返回写进注册表的命令行。
func (e Entry) Command() string { return `"` + e.Exe + `" ` + Flag }

// Launched 判断本次启动是不是开机自启（命令行里带 --autostart）。
func Launched(args []string) bool {
	for _, a := range args {
		if strings.EqualFold(a, Flag) {
			return true
		}
	}
	return false
}
