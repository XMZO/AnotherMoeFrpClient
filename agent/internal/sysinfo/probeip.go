package sysinfo

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// 出口公网地址：网卡上常常没有公网 IPv4（云服务器的 IPv4 是 NAT 出来的），面板看到的连接来源地址
// 又只有 agent 连面板时用的那一种（走 IPv6 就拿不到 IPv4，反过来也一样）。所以分别强制走 IPv4、IPv6
// 去问外面的"我的 IP 是什么"服务（和 https://env.moe/ips 一个思路），几个服务同时问，取最先回来的有效答案。

// ProbeTimeout 是一次探测（每种协议）的总时长。
const ProbeTimeout = 6 * time.Second

// ipEchoURLs 是回显来源地址的公共服务，都同时有 A 和 AAAA 记录，两种协议都能问。
// 回复格式不一：JSON 的 "ip" 字段、cdn-cgi/trace 的 ip= 行、纯文本，parseEchoIP 都认。
var ipEchoURLs = []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://env.moe/ip", "https://api64.ipify.org"}

// ProbePublicIPs 返回出口的公网 IPv4 和 IPv6（拿不到的为空串）。extra 是另外要问的回显地址
// （agent 传面板的 RouteMyIP：节点本来就连得上面板，国内节点访问不了国外服务时靠它）。
func ProbePublicIPs(ctx context.Context, extra ...string) (v4, v6 string) {
	urls := append(append([]string(nil), extra...), ipEchoURLs...)
	done := make(chan struct{})
	go func() {
		v6 = probeFamily(ctx, "tcp6", urls)
		close(done)
	}()
	v4 = probeFamily(ctx, "tcp4", urls)
	<-done
	return v4, v6
}

func probeFamily(ctx context.Context, network string, urls []string) string {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	client := probeClient(network)
	defer client.CloseIdleConnections()

	got := make(chan string, len(urls))
	for _, u := range urls {
		go func() {
			got <- askEcho(ctx, client, u, network == "tcp4")
		}()
	}
	for range urls {
		select {
		case ip := <-got:
			if ip != "" {
				return ip
			}
		case <-ctx.Done():
			return ""
		}
	}
	return ""
}

// probeClient 是只走 network（tcp4 / tcp6）、不走代理的 HTTP 客户端。
func probeClient(network string) *http.Client {
	d := &net.Dialer{Timeout: 3 * time.Second}
	return &http.Client{Transport: &http.Transport{
		Proxy: nil, // 直连：要的是本机自己的出口地址
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr) // 强制 IPv4 / IPv6
		},
		TLSHandshakeTimeout: 3 * time.Second,
	}}
}

func askEcho(ctx context.Context, client *http.Client, url string, want4 bool) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "curl/8") // 有的服务对浏览器返回网页、对 curl 返回纯文本
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	ip := parseEchoIP(string(body))
	if ip == nil || (ip.To4() != nil) != want4 || !publicIP(ip) {
		return ""
	}
	return ip.String()
}

// parseEchoIP 从回显服务的回复里取 IP：JSON 的 "ip" 字段、"ip=" 开头的行，或者整个回复就是 IP。
func parseEchoIP(body string) net.IP {
	var j struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal([]byte(body), &j) == nil && j.IP != "" {
		return net.ParseIP(strings.TrimSpace(j.IP))
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "ip="); ok {
			return net.ParseIP(v)
		}
	}
	return net.ParseIP(strings.TrimSpace(body))
}
