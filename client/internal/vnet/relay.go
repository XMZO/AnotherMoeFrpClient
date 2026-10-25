package vnet

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
)

// 节点中继（docs/vnet.md）：每个成员有一个"收件中继"，别人发给他的、打洞没通的包送到那里；
// 发包的人连对方的收件中继发。收件中继：房主锁定了就用房主选的；否则按自己的选择（自动 / 面板 / 某个节点），
// 自动 = 房主设的默认，没设就测延迟选最近的；节点都不行就用面板。

// 收件中继的选择。
const (
	RelayAuto  = "auto"  // 自动
	RelayPanel = "panel" // 面板（实时通道）
)

// RelayView 是界面上的一个中继选项。
type RelayView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Region  string `json:"region,omitempty"`
	Latency int    `json:"latency"` // 毫秒，-1 表示连不上，0 表示还没测
}

type relayConn struct {
	ws   *websocket.Conn
	done chan struct{}
}

// relayState 是中继相关的状态，Engine.mu 保护。
type relayState struct {
	nodes    map[string]api.VnetRelay
	tickets  map[string]string
	settings api.RoomSettings
	pref     string         // 我的选择：RelayAuto、RelayPanel 或节点 ID
	latency  map[string]int // 节点 ID → 毫秒（-1 连不上）
	home     string         // 实际的收件中继（节点 ID，空表示面板）
	conns    map[string]*relayConn
	dialing  map[string]bool
	measured bool
}

// updateRelaysLocked 收到面板的成员列表后更新中继信息，重新决定收件中继。调用方持有 e.mu。
func (e *Engine) updateRelaysLocked(b api.LiveVnetBody) {
	r := &e.rs
	r.nodes = map[string]api.VnetRelay{}
	for _, x := range b.Relays {
		r.nodes[x.ID] = x
	}
	r.tickets = b.Tickets
	if b.Settings != nil {
		r.settings = *b.Settings
	}
	if !r.measured && len(r.nodes) > 0 {
		r.measured = true
		go e.measure()
	}
	e.chooseHomeLocked()
}

// SetRelayPref 改我的收件中继选择（房主锁定时不生效）。
func (e *Engine) SetRelayPref(pref string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rs.pref = pref
	e.chooseHomeLocked()
}

// chooseHomeLocked 算出收件中继，变了就连上并告诉面板。调用方持有 e.mu。
func (e *Engine) chooseHomeLocked() {
	r := &e.rs
	want := ""
	pick := r.pref
	if r.settings.RelayLocked || pick == "" {
		pick = RelayAuto
		if r.settings.RelayLocked && r.settings.Relay != "" {
			pick = r.settings.Relay
		}
	}
	switch {
	case pick == RelayPanel:
	case pick == RelayAuto:
		if _, ok := r.nodes[r.settings.Relay]; ok && r.latency[r.settings.Relay] >= 0 {
			want = r.settings.Relay
		} else {
			best := -1
			for id := range r.nodes {
				if l, ok := r.latency[id]; ok && l > 0 && (best < 0 || l < best) {
					best, want = l, id
				}
			}
		}
	default:
		if _, ok := r.nodes[pick]; ok {
			want = pick
		}
	}
	if _, ok := r.nodes[want]; !ok {
		want = ""
	}
	if want == r.home {
		return
	}
	r.home = want
	if want != "" {
		e.ensureRelayLocked(want)
	}
	room := e.room
	go func() {
		if err := e.tr.SetRelay(room, want); err != nil {
			e.logf("vnet: set relay: %v", err)
		}
		e.onChange()
	}()
}

// measure 测到各中继节点的延迟（TCP 连接时间），测完重新选收件中继。
func (e *Engine) measure() {
	e.mu.Lock()
	nodes := make([]api.VnetRelay, 0, len(e.rs.nodes))
	for _, x := range e.rs.nodes {
		nodes = append(nodes, x)
	}
	e.mu.Unlock()
	res := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, x := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(x.Addr, strconv.Itoa(x.Port)), 3*time.Second)
			ms := -1
			if err == nil {
				ms = max(1, int(time.Since(start).Milliseconds()))
				_ = c.Close()
			}
			mu.Lock()
			res[x.ID] = ms
			mu.Unlock()
		}()
	}
	wg.Wait()
	e.mu.Lock()
	e.rs.latency = res
	if e.dev != nil {
		e.chooseHomeLocked()
	}
	e.mu.Unlock()
	e.onChange()
}

// ensureRelayLocked 确保连着这个中继（后台连）。调用方持有 e.mu。
func (e *Engine) ensureRelayLocked(id string) {
	r := &e.rs
	if r.conns == nil {
		r.conns, r.dialing = map[string]*relayConn{}, map[string]bool{}
	}
	if r.conns[id] != nil || r.dialing[id] {
		return
	}
	n, ok := r.nodes[id]
	tk := r.tickets[id]
	if !ok || tk == "" || e.done == nil {
		return
	}
	r.dialing[id] = true
	done := e.done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ws, _, err := websocket.Dial(ctx, "ws://"+net.JoinHostPort(n.Addr, strconv.Itoa(n.Port))+api.RouteVnetRelay,
			&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tk}}})
		cancel()
		e.mu.Lock()
		delete(r.dialing, id)
		if err != nil || e.done != done {
			e.mu.Unlock()
			if err != nil {
				e.logf("vnet: relay %s: %v", n.Name, err)
			} else {
				ws.CloseNow()
			}
			return
		}
		ws.SetReadLimit(64 << 10)
		c := &relayConn{ws: ws, done: make(chan struct{})}
		r.conns[id] = c
		b := e.bind
		e.mu.Unlock()
		e.logf("vnet: 连上中继 %s", n.Name)
		go func() { // 收：第一个字节是发送方的座位号
			defer func() {
				e.mu.Lock()
				if r.conns[id] == c {
					delete(r.conns, id)
				}
				e.mu.Unlock()
				close(c.done)
				ws.CloseNow()
			}()
			for {
				typ, p, err := ws.Read(context.Background())
				if err != nil {
					return
				}
				if typ == websocket.MessageBinary && len(p) > 1 && b != nil {
					b.deliver(int(p[0]), p[1:])
				}
			}
		}()
	}()
}

// relaySend 经节点中继发一个包；没连上返回 false（调用方改走面板），同时在后台去连。
func (e *Engine) relaySend(id string, slot int, pkt []byte) bool {
	e.mu.Lock()
	c := e.rs.conns[id]
	if c == nil {
		e.ensureRelayLocked(id)
	}
	e.mu.Unlock()
	if c == nil {
		return false
	}
	b := make([]byte, 1+len(pkt))
	b[0] = byte(slot)
	copy(b[1:], pkt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageBinary, b) == nil
}

// closeRelaysLocked 断开所有中继。调用方持有 e.mu。
func (e *Engine) closeRelaysLocked() {
	for _, c := range e.rs.conns {
		c.ws.CloseNow()
	}
	e.rs = relayState{pref: e.rs.pref}
}

// relayViewsLocked 返回界面上的中继选项（按延迟）。调用方持有 e.mu。
func (e *Engine) relayViewsLocked() []RelayView {
	out := make([]RelayView, 0, len(e.rs.nodes))
	for id, x := range e.rs.nodes {
		out = append(out, RelayView{ID: id, Name: x.Name, Region: x.Region, Latency: e.rs.latency[id]})
	}
	slices.SortFunc(out, func(a, b RelayView) int {
		la, lb := a.Latency, b.Latency
		if la <= 0 {
			la = 1 << 30
		}
		if lb <= 0 {
			lb = 1 << 30
		}
		return la - lb
	})
	return out
}
