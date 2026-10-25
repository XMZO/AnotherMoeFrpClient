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
		go func() { _ = e.send(from, resp) }()
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
	}
	e.mu.Unlock()
	return true
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
		now := map[int]int{}
		for slot, p := range e.peers {
			if p.key != "" {
				slots = append(slots, slot)
			}
			now[slot] = p.rttMs()
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
			binary.BigEndian.PutUint64(b[1:], uint64(time.Now().UnixNano()))
			_ = e.send(slot, b)
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
