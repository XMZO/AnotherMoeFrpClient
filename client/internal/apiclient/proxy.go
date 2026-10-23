package apiclient

import (
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// 代理模式（PLAN 6.10），取值与 store.NetProxy* 相同。
const (
	ProxySystem = "system"
	ProxyNone   = "none"
	ProxyCustom = "custom"
)

// SystemProxySettings 是系统（WinINet，即"Internet 选项"）的手动代理设置。
type SystemProxySettings struct {
	Enabled  bool
	Server   string // ProxyServer：host:port，或 http=host:port;https=host:port;socks=host:port
	Override string // ProxyOverride：分号分隔的例外列表，<local> 表示不带点的主机名
}

// readSystemProxy 由平台相关的文件实现（Windows 读注册表）。测试里可以替换。
var readSystemProxy = loadSystemProxy

// proxyFunc 返回给 http.Transport 用的代理函数。custom 模式用 custom（解析失败时等同 none）。
// 回环地址在任何模式下都直连。
func proxyFunc(mode string, custom string) func(*http.Request) (*url.URL, error) {
	if mode == ProxyNone {
		return nil
	}
	if mode == ProxyCustom {
		u, err := url.Parse(custom)
		if err != nil || u.Host == "" {
			return nil
		}
		return func(r *http.Request) (*url.URL, error) {
			if isLoopback(r.URL.Hostname()) {
				return nil, nil
			}
			return u, nil
		}
	}
	return func(r *http.Request) (*url.URL, error) {
		host := r.URL.Hostname()
		if isLoopback(host) {
			return nil, nil
		}
		s := readSystemProxy()
		if !s.Enabled || strings.TrimSpace(s.Server) == "" {
			// 没有手动设置时退回环境变量（HTTP_PROXY / HTTPS_PROXY / NO_PROXY）
			return http.ProxyFromEnvironment(r)
		}
		if bypassed(host, s.Override) {
			return nil, nil
		}
		return pickProxy(s.Server, r.URL.Scheme), nil
	}
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pickProxy 按 WinINet 的规则从 ProxyServer 里选出代理：单个地址用于所有协议；
// 分协议的写法优先用同协议的，其次 socks。
func pickProxy(server, scheme string) *url.URL {
	server = strings.TrimSpace(server)
	if !strings.Contains(server, "=") {
		return proxyURL("http", server)
	}
	entries := map[string]string{}
	for _, part := range strings.Split(server, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && v != "" {
			entries[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	if v := entries[scheme]; v != "" {
		return proxyURL("http", v)
	}
	if v := entries["socks"]; v != "" {
		return proxyURL("socks5", v)
	}
	return nil
}

func proxyURL(defScheme, addr string) *url.URL {
	if !strings.Contains(addr, "://") {
		addr = defScheme + "://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// bypassed 判断主机是否在 ProxyOverride 的例外列表里。
func bypassed(host, override string) bool {
	host = strings.ToLower(host)
	for _, raw := range strings.Split(override, ";") {
		pat := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case pat == "":
			continue
		case pat == "<local>":
			if !strings.Contains(host, ".") {
				return true
			}
		default:
			if ok, _ := path.Match(pat, host); ok {
				return true
			}
		}
	}
	return false
}
