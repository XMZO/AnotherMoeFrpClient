package vnet

import (
	"encoding/binary"
	"time"
)

// 成员之间的延迟：每隔一会给每个在线成员发一个探测包，对方原样回一个，量往返时间。
// 探测包和 WireGuard 的包走同一条路（直连 / 节点中继 / 面板），量出来就是游戏数据实际的延迟。
// 第一个字节区分：WireGuard 的包是 1–4，这里用 0xFE（探测）、0xFF（回复），后面 8 字节是发出时间（不加密，只有时间戳）。
// 旧版本客户端收到探测包交给 WireGuard，会被当成无效包丢掉，只是量不出延迟。
const (
	pingReq  = 0xFE
	pingResp = 0xFF

	pingEvery = 2 * time.Second
	pingStale = 7 * time.Second // 这么久没回复就不显示延迟
)

// handleProbe 处理探测包，返回 false 表示不是探测包（交给 WireGuard）。
func (e *Engine) handleProbe(from int, b []byte) bool {
	if len(b) != 9 || (b[0] != pingReq && b[0] != pingResp) {
		return false
	}
	if b[0] == pingReq {
		resp := make([]byte, 9)
		resp[0] = pingResp
		copy(resp[1:], b[1:])
		go func() { _ = e.sendPkt(from, resp, false) }() // 回复也只走直连（量的是直连本身的丢包）
		return true
	}
	sent := int64(binary.BigEndian.Uint64(b[1:]))
	rtt := time.Duration(time.Now().UnixNano() - sent)
	if rtt < 0 || rtt > 30*time.Second {
		return true
	}
	e.mu.Lock()
	if p := e.peers[from]; p != nil {
		if p.rtt == 0 || time.Since(p.rttAt) > pingStale {
			p.rtt = rtt
		} else {
			p.rtt = (p.rtt + rtt) / 2 // 平滑一下，跳得没那么厉害
		}
		p.rttAt = time.Now()
		if sent == p.probeSent {
			p.probeGot = true
		}
	}
	e.mu.Unlock()
	return true
}

// 直连丢包检测：每次探测记一笔"上一个探测有没有回"，保留最近 lossWindow 次。
// 最近这些次里有丢的就打开冗余发送（直连 + 中继各发一份，WireGuard 自带防重放，重复的包对方直接丢掉），
// 连续 lossWindow 次都没丢才关。MC 这类用 TCP 的游戏丢一个包整条连接都要等重传，回弹、卡区块就是这么来的。
const lossWindow = 30

// recordProbeLocked 记录上一个探测的结果，返回这次探测的时间戳（发出去的包里带它）。调用方持有 e.mu。
func (p *peer) recordProbeLocked(now int64) {
	if p.probeSent != 0 {
		p.probeHist = append(p.probeHist, p.probeGot)
		if len(p.probeHist) > lossWindow {
			p.probeHist = p.probeHist[len(p.probeHist)-lossWindow:]
		}
	}
	p.probeSent, p.probeGot = now, false
}

// lossPct 是最近的探测丢包率（%），探测次数太少时为 0。
func (p *peer) lossPct() int {
	if len(p.probeHist) < 5 {
		return 0
	}
	lost := 0
	for _, ok := range p.probeHist {
		if !ok {
			lost++
		}
	}
	return lost * 100 / len(p.probeHist)
}

// lossy：最近有丢包，直连时要冗余发送。
func (p *peer) lossy() bool {
	for _, ok := range p.probeHist {
		if !ok {
			return true
		}
	}
	return false
}

// pingLoop 定期给在线成员发探测包，延迟有变化时刷新界面。
func (e *Engine) pingLoop(done chan struct{}) {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	last := map[int]int{}
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		e.mu.Lock()
		var slots []int
		stamps := map[int]int64{}
		now := map[int]int{}
		for slot, p := range e.peers {
			if p.key != "" {
				slots = append(slots, slot)
				ts := time.Now().UnixNano()
				p.recordProbeLocked(ts)
				stamps[slot] = ts
			} else {
				p.probeHist, p.probeSent = nil, 0
			}
			now[slot] = p.rttMs()*1000 + p.lossPct() // 延迟或丢包变了都刷新界面
		}
		e.mu.Unlock()
		changed := len(now) != len(last)
		for slot, ms := range now {
			if last[slot] != ms {
				changed = true
			}
		}
		last = now
		if changed {
			go e.onChange()
		}
		for _, slot := range slots {
			b := make([]byte, 9)
			b[0] = pingReq
			binary.BigEndian.PutUint64(b[1:], uint64(stamps[slot]))
			_ = e.sendPkt(slot, b, false)
		}
	}
}

// rttMs 是界面上显示的延迟（毫秒，至少 1），0 表示不知道。
func (p *peer) rttMs() int {
	if p.key == "" || p.rtt == 0 || time.Since(p.rttAt) > pingStale {
		return 0
	}
	return max(1, int(p.rtt/time.Millisecond))
}
