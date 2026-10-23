package apiclient

import "golang.org/x/sys/windows/registry"

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// loadSystemProxy 读取当前用户"Internet 选项"里的手动代理。自动配置脚本（PAC）不支持。
func loadSystemProxy() SystemProxySettings {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return SystemProxySettings{}
	}
	defer k.Close()
	var s SystemProxySettings
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.Enabled = v != 0
	}
	s.Server, _, _ = k.GetStringValue("ProxyServer")
	s.Override, _, _ = k.GetStringValue("ProxyOverride")
	return s
}
