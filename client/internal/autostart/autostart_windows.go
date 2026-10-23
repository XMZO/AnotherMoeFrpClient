package autostart

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// State 读取当前的自启项：是否存在、是否指向本 exe。
func (e Entry) State() (exists, current bool, err error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(e.Name)
	if errors.Is(err, registry.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, strings.EqualFold(strings.TrimSpace(v), e.Command()), nil
}

// Set 打开或关闭开机自启。
func (e Entry) Set(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(e.Name, e.Command())
	}
	if err := k.DeleteValue(e.Name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
