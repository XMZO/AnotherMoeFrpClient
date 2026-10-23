//go:build !windows

package apiclient

func loadSystemProxy() SystemProxySettings { return SystemProxySettings{} }
