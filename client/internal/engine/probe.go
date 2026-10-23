package engine

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"moefrp/client/internal/apperr"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// DefaultProbeTimeout 是测速和连接失败诊断用的 TCP 超时（PLAN 6.4：超时 2 秒）。
const DefaultProbeTimeout = 2 * time.Second

// Windows 上连接被拒绝是 WSAECONNREFUSED（10061），和 syscall.ECONNREFUSED 不是同一个值。
var wsaeconnrefused error = syscall.Errno(10061)

// PingResult 是对一个节点的 TCP 探测结果。
type PingResult struct {
	NodeID    string      `json:"nodeId"`
	OK        bool        `json:"ok"`
	LatencyMs int64       `json:"latencyMs"` // 只有 OK 时有意义
	Code      apierr.Code `json:"code,omitempty"`
	Message   string      `json:"message,omitempty"` // 原始错误，放日志和悬停提示
	Target    string      `json:"target"`            // 实际探测的地址
}

// Probe 用 TCP 连接节点并计时，PingNodes 和连接失败时的诊断共用这一套代码（PLAN 6.3.4、坑 8）。
//
//   - 节点配置了 proxyURL 时探测的是代理服务器，因为 frp 也是先连代理。
//   - kcp / quic 走 UDP，TCP 探测没有意义，只检查域名能否解析。
func Probe(ctx context.Context, n model.Node, timeout time.Duration) PingResult {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	host, port := n.ServerAddr, n.ServerPort
	if n.ProxyURL != "" {
		if u, err := url.Parse(n.ProxyURL); err == nil && u.Hostname() != "" {
			host = u.Hostname()
			port, _ = strconv.Atoi(u.Port())
			if port == 0 {
				port = map[string]int{"http": 80, "socks5": 1080, "ntlm": 80}[u.Scheme]
			}
		}
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))
	res := PingResult{NodeID: n.ID, Target: target}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if n.ProxyURL == "" && (n.Protocol == model.ProtocolKCP || n.Protocol == model.ProtocolQUIC) {
		if net.ParseIP(host) == nil {
			if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
				res.Code, res.Message = classifyDialError(err)
				return res
			}
		}
		res.Code, res.Message = apperr.NodeUDPUnprobed, "udp protocol "+n.Protocol+" cannot be probed over tcp"
		return res
	}

	t0 := time.Now()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		res.Code, res.Message = classifyDialError(err)
		return res
	}
	res.LatencyMs = time.Since(t0).Milliseconds()
	_ = c.Close()
	res.OK = true
	return res
}

// PingNodes 并发探测多个节点，结果顺序与输入一致。
func PingNodes(ctx context.Context, nodes []model.Node, timeout time.Duration) []PingResult {
	out := make([]PingResult, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Go(func() { out[i] = Probe(ctx, n, timeout) })
	}
	wg.Wait()
	return out
}

func classifyDialError(err error) (apierr.Code, string) {
	msg := err.Error()
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && !dnsErr.IsTimeout:
		return apperr.NodeDNS, msg
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) || isTimeout(err):
		return apperr.NodeTimeout, msg
	case errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeconnrefused):
		return apperr.NodeRefused, msg
	default:
		return apperr.NodeUnreachable, msg
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
