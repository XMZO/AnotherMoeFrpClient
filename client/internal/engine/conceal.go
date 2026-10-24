package engine

import (
	"context"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"moefrp/shared/model"
)

// concealer 把官方节点的地址换成节点名（官方节点对用户是黑盒，docs/node-agent.md 第 12 节）。
// 日志（包括 frp 自己打的，例如 "dial tcp 1.2.3.4:7000: i/o timeout"）、连接失败的错误、测速结果都经过它。
// 替换域名、域名:端口、解析出的 IP:端口（IP 单独出现时不换，免得误伤同一 IP 上的本地地址）；只增不减，节点列表很少变化。
type concealer struct {
	mu    sync.RWMutex
	pairs map[string]string
	keys  []string // 按长度从长到短，先换 host:port 再换 host
}

func (c *concealer) add(needle, label string) {
	if needle == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pairs == nil {
		c.pairs = map[string]string{}
	}
	if _, ok := c.pairs[needle]; ok {
		return
	}
	c.pairs[needle] = label
	c.keys = append(c.keys, needle)
	slices.SortFunc(c.keys, func(a, b string) int { return len(b) - len(a) })
}

func (c *concealer) apply(s string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, k := range c.keys {
		if strings.Contains(s, k) {
			s = strings.ReplaceAll(s, k, c.pairs[k])
		}
	}
	return s
}

// HideNode 让这个节点的地址不出现在日志、错误和测速结果里，换成 [节点名]。
// 解析 IP 在后台进行（frp 连接失败时的报错里是 IP）。
func (s *LogSink) HideNode(n model.Node) {
	host := strings.TrimSpace(n.ServerAddr)
	if host == "" {
		return
	}
	label := "[托管节点]"
	if name := strings.TrimSpace(n.Remark); name != "" {
		label = "[" + name + "]"
	}
	port := strconv.Itoa(n.ServerPort)
	s.hide.add(net.JoinHostPort(host, port), label)
	if net.ParseIP(host) != nil {
		return
	}
	s.hide.add(host, label)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return
		}
		for _, ip := range ips {
			s.hide.add(net.JoinHostPort(ip, port), label)
		}
	}()
}

// Conceal 替换掉文本里官方节点的地址。
func (s *LogSink) Conceal(text string) string { return s.hide.apply(text) }

// ConcealPing 替换掉测速结果里官方节点的地址。
func (s *LogSink) ConcealPing(r PingResult) PingResult {
	r.Target, r.Message = s.Conceal(r.Target), s.Conceal(r.Message)
	return r
}
