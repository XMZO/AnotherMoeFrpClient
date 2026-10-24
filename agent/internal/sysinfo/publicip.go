package sysinfo

import (
	"net"
	"slices"
)

// PublicIPs 返回网卡上的公网地址（IPv4、IPv6；不含内网、回环、链路本地、运营商级 NAT），面板据此给用户显示访问地址。
// 云服务器的公网 IPv4 常常是 NAT 出来的、不在网卡上，面板会再加上它看到的连接来源地址。
func PublicIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || !publicIP(ipn.IP) {
			continue
		}
		if s := ipn.IP.String(); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64)
}
