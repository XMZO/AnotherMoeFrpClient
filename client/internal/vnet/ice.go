package vnet

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

// 打洞：座位号小的一方发起（offer），对方回 answer，双方把候选地址经面板发给对方，ICE 选出能通的那一对。
// 只用本机地址和 STUN 反射地址（不用 TURN），打不通就继续走面板中继。

// stunServers 用来发现自己的公网地址：国内的放前面。
var stunServers = []string{
	"stun:stun.miwifi.com:3478",
	"stun:stun.chat.bilibili.com:3478",
	"stun:stun.cloudflare.com:3478",
	"stun:stun.l.google.com:19302",
}

// skipIfaces 是不拿来打洞的网卡（名字里带这些的，不区分大小写）：自己的虚拟网卡、VPN 和代理软件的 TUN、虚拟机网卡。
// 走它们出去会被 VPN 接管（打洞必然失败，还可能绕回自己）。
var skipIfaces = []string{"moefrp", "wintun", "wireguard", "tailscale", "zerotier", "clash", "mihomo", "meta", "sing", "tun",
	"tap", "vpn", "vethernet", "virtualbox", "vmware", "loopback", "hyper-v", "docker", "wsl", "easytier", "radmin", "hamachi"}

func keepIface(name string) bool {
	n := strings.ToLower(name)
	for _, s := range skipIfaces {
		if strings.Contains(n, s) {
			return false
		}
	}
	return true
}

var skipNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range []string{"10.241.0.0/24", "198.18.0.0/15", "169.254.0.0/16", "fe80::/10"} {
		_, n, _ := net.ParseCIDR(s)
		out = append(out, n)
	}
	return out
}()

func keepIP(ip net.IP) bool {
	for _, n := range skipNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// signal 是打洞信息。
type signal struct {
	T   string `json:"t"`           // offer / answer / cand
	G   int    `json:"g"`           // 第几次尝试，旧的丢掉
	U   string `json:"u,omitempty"` // ufrag
	P   string `json:"p,omitempty"` // pwd
	Can string `json:"c,omitempty"` // 候选地址
}

type iceSession struct {
	gen    int
	agent  *ice.Agent
	cancel context.CancelFunc
}

func (s *iceSession) close() {
	s.cancel()
	_ = s.agent.Close()
}

func newAgent() (*ice.Agent, error) {
	var urls []*stun.URI
	for _, u := range stunServers {
		if x, err := stun.ParseURI(u); err == nil {
			urls = append(urls, x)
		}
	}
	return ice.NewAgent(&ice.AgentConfig{
		Urls:            urls,
		NetworkTypes:    []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6},
		CandidateTypes:  []ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive},
		InterfaceFilter: keepIface,
		IPFilter:        keepIP,
	})
}

// startICELocked 发起一次打洞（发起方）。调用方持有 e.mu。房主禁止了直连时不打洞（全部走中继）。
func (e *Engine) startICELocked(p *peer, gen int) {
	if e.rs.settings.NoDirect {
		return
	}
	a, err := newAgent()
	if err != nil {
		e.logf("vnet: ice agent: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &iceSession{gen: gen, agent: a, cancel: cancel}
	p.ice = s
	e.wireAgent(p, s)
	u, pw, _ := a.GetLocalUserCredentials()
	e.signalToLocked(p.slot, signal{T: "offer", G: gen, U: u, P: pw})
	_ = a.GatherCandidates()
	go func() { // 等对方回 answer：没回就过一会再试
		select {
		case <-ctx.Done():
		case <-time.After(30 * time.Second):
			e.mu.Lock()
			still := p.ice == s && p.direct == nil
			e.mu.Unlock()
			if still {
				e.retryLater(p, s)
			}
		}
	}()
}

// wireAgent：本地候选地址发给对方；连接断了就退回中继。
func (e *Engine) wireAgent(p *peer, s *iceSession) {
	_ = s.agent.OnCandidate(func(c ice.Candidate) {
		if c != nil {
			e.signalTo(p.slot, signal{T: "cand", G: s.gen, Can: c.Marshal()})
		}
	})
	_ = s.agent.OnConnectionStateChange(func(st ice.ConnectionState) {
		switch st {
		case ice.ConnectionStateDisconnected, ice.ConnectionStateConnected:
			// 暂时没响应：数据先走中继，直连留着等它恢复；恢复了切回直连（不关掉，免得来回重新打洞）
			down := st == ice.ConnectionStateDisconnected
			e.mu.Lock()
			changed := p.ice == s && p.direct != nil && p.directDown != down
			if changed {
				p.directDown = down
			}
			e.mu.Unlock()
			if changed {
				if down {
					e.logf("vnet: %s 直连暂时没响应，先走中继", p.nickname)
				} else {
					e.logf("vnet: %s 直连恢复", p.nickname)
				}
				go e.onChange()
			}
			return
		case ice.ConnectionStateFailed, ice.ConnectionStateClosed:
		default:
			return
		}
		e.mu.Lock()
		dropped := p.ice == s && p.direct != nil
		if dropped {
			e.dropDirectLocked(p)
		}
		e.mu.Unlock()
		if dropped {
			e.logf("vnet: %s 直连断了，改走中继", p.nickname)
			go e.onChange()
		}
		if st == ice.ConnectionStateFailed && e.self < p.slot {
			e.retryLater(p, s)
		}
	})
}

// signalTo 把打洞信息排进发送队列（按顺序发：offer / answer 要比候选地址先到）。
func (e *Engine) signalTo(slot int, s signal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.signalToLocked(slot, s)
}

func (e *Engine) signalToLocked(slot int, s signal) {
	if e.sigq == nil {
		return
	}
	b, _ := json.Marshal(s)
	select {
	case e.sigq <- outSignal{room: e.room, to: slot, data: string(b)}:
	default:
	}
}

type outSignal struct {
	room string
	to   int
	data string
}

// sendSignals 按顺序把队列里的打洞信息发给面板，直到虚拟网关掉。
func (e *Engine) sendSignals(q chan outSignal, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case s := <-q:
			if err := e.tr.Signal(s.room, s.to, s.data); err != nil {
				e.logf("vnet: signal: %v", err)
			}
		}
	}
}

// OnSignal 收到对方的打洞信息。
func (e *Engine) OnSignal(from int, data string) {
	var sg signal
	if json.Unmarshal([]byte(data), &sg) != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.peers[from]
	if p == nil || p.key == "" || e.dev == nil {
		return
	}
	switch sg.T {
	case "offer": // 我是应答方：换一个新的 agent
		if e.self < from || e.rs.settings.NoDirect {
			return // 两边都以为自己该发起：以座位号小的为准；房主禁止了直连
		}
		e.dropDirectLocked(p)
		if p.ice != nil {
			p.ice.close()
		}
		a, err := newAgent()
		if err != nil {
			e.logf("vnet: ice agent: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		s := &iceSession{gen: sg.G, agent: a, cancel: cancel}
		p.ice = s
		e.wireAgent(p, s)
		u, pw, _ := a.GetLocalUserCredentials()
		e.signalToLocked(from, signal{T: "answer", G: sg.G, U: u, P: pw})
		_ = a.GatherCandidates()
		go func() {
			c, err := a.Accept(ctx, sg.U, sg.P)
			if err != nil {
				return
			}
			e.useDirect(p, s, &directConn{conn: c})
		}()
	case "answer": // 我是发起方：开始连
		s := p.ice
		if s == nil || s.gen != sg.G {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, err := s.agent.Dial(ctx, sg.U, sg.P)
			if err != nil {
				e.retryLater(p, s)
				return
			}
			e.useDirect(p, s, &directConn{conn: c})
		}()
	case "cand":
		s := p.ice
		if s == nil || s.gen != sg.G {
			return
		}
		if c, err := ice.UnmarshalCandidate(sg.Can); err == nil {
			_ = s.agent.AddRemoteCandidate(c)
		}
	}
}
