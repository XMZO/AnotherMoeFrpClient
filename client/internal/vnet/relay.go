package vnet

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
)

// 节点中继（docs/vnet.md）：每个成员连着一到两个"收件中继"节点（主 + 备用），别人发给他的、打洞没通的包送到其中一个；
// 发包的人从对方连着的节点里挑一个自己也连着、延迟最低的发。一个节点出问题马上换另一个，不用退回面板。
// 主收件中继：房主锁定了就用房主选的；否则按自己的选择（自动 / 面板 / 某个节点），自动 = 房主设的默认，没设就测延迟选最近的；
// 备用 = 除主的以外延迟最低的（房主锁定了某个节点、或者选了面板时没有备用）。节点都不行就用面板。

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
	q    chan []byte // 待发的帧：单独的协程写，发包的人不用等（中继的 TCP 堵了也不拖慢直连）
}

// relayQueue 是每条中继连接的发送队列长度，满了就丢（和网络拥塞丢包一样，TCP 会重传）。
const relayQueue = 512

// relayState 是中继相关的状态，Engine.mu 保护。
type relayState struct {
	nodes    map[string]api.VnetRelay
	tickets  map[string]string
	settings api.RoomSettings
	pref     string         // 我的选择：RelayAuto、RelayPanel 或节点 ID
	latency  map[string]int // 节点 ID → 毫秒（-1 连不上）
	home     string         // 主收件中继（节点 ID，空表示面板）
	backup   string         // 备用收件中继
	reported string         // 上次告诉面板的（免得重复发）
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
	if !r.measured && len(r.nodes) > 0 && e.done != nil {
		r.measured = true
		go e.measureLoop(e.done)
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
			// 防抖：现在用的还能连、只比最快的慢一点（20% + 10ms 以内）就不换，免得每次重测都来回切
			if cur, ok := r.latency[r.home]; ok && cur > 0 && best > 0 && cur <= best*6/5+10 {
				want = r.home
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
	backup := ""
	if want != "" && pick != RelayPanel && !(r.settings.RelayLocked && r.settings.Relay != "") {
		// 备用：除主的以外延迟最低的；现在的备用还能连就不换
		if l, ok := r.latency[r.backup]; ok && l > 0 && r.backup != want {
			backup = r.backup
		} else {
			best := -1
			for id := range r.nodes {
				if l, ok := r.latency[id]; ok && l > 0 && id != want && (best < 0 || l < best) {
					best, backup = l, id
				}
			}
		}
	}
	if want != r.home || backup != r.backup {
		r.home, r.backup = want, backup
		go e.onChange()
	}
	for _, id := range []string{want, backup} {
		if id != "" {
			e.ensureRelayLocked(id)
		}
	}
	e.reportHomesLocked()
}

// homesLocked 返回要告诉面板的收件中继：relay 是主的（给旧版本客户端），homes 是现在真正连着的（主的在前）。
// 只报连着的：报了没连着的节点，别人发到那里的包会被节点丢掉（黑洞）。
func (e *Engine) homesLocked() (relay string, homes []string) {
	r := &e.rs
	for _, id := range []string{r.home, r.backup} {
		if id != "" && r.conns[id] != nil {
			homes = append(homes, id)
		}
	}
	relay = r.home
	if len(homes) > 0 && homes[0] != r.home {
		relay = homes[0] // 主的没连上、备用连着：旧版本客户端也送到连着的那个
	}
	return relay, homes
}

// Homes 返回要告诉面板的收件中继（实时通道重连后再报一次用）。
func (e *Engine) Homes() (string, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.homesLocked()
}

// reportHomesLocked：收件中继（或者它们的连接状态）变了就告诉面板。调用方持有 e.mu。
func (e *Engine) reportHomesLocked() {
	relay, homes := e.homesLocked()
	key := relay + "|" + strings.Join(homes, ",")
	if key == e.rs.reported || e.room == "" {
		return
	}
	e.rs.reported = key
	room := e.room
	go func() {
		if err := e.tr.SetRelay(room, relay, homes); err != nil {
			e.logf("vnet: set relay: %v", err)
		}
		e.onChange()
	}()
}

// measureEvery 是重测中继延迟的间隔（线路的延迟会变，界面上的数字也跟着更新）。
const measureEvery = 5 * time.Second

// measureLoop 进房间后马上测一次中继延迟，之后定期重测，直到离开房间。
func (e *Engine) measureLoop(done chan struct{}) {
	t := time.NewTicker(measureEvery)
	defer t.Stop()
	for {
		e.measure()
		select {
		case <-done:
			return
		case <-t.C:
		}
	}
}

// measure 测到各中继节点的延迟（TCP 连接时间，连两次取快的），测完重新选收件中继。
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
			ms := -1
			for range 2 {
				start := time.Now()
				c, err := net.DialTimeout("tcp", net.JoinHostPort(x.Addr, strconv.Itoa(x.Port)), 3*time.Second)
				if err != nil {
					continue
				}
				d := max(1, int(time.Since(start).Milliseconds()))
				_ = c.Close()
				if ms < 0 || d < ms {
					ms = d
				}
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
		e.chooseHomeLocked() // 也会补上没连着的收件中继
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
				e.redialHomeLater(id, done, 3*time.Second)
			} else {
				ws.CloseNow()
			}
			return
		}
		ws.SetReadLimit(64 << 10)
		c := &relayConn{ws: ws, done: make(chan struct{}), q: make(chan []byte, relayQueue)}
		r.conns[id] = c
		go func() { // 发
			for {
				select {
				case <-c.done:
					return
				case b := <-c.q:
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					err := ws.Write(ctx, websocket.MessageBinary, b)
					cancel()
					if err != nil {
						ws.CloseNow() // 收的协程会读到错误、清理、重连
						return
					}
				}
			}
		}()
		b := e.bind
		e.reportHomesLocked()
		e.mu.Unlock()
		e.logf("vnet: 连上中继 %s", n.Name)
		go func() { // 收：第一个字节是发送方的座位号
			defer func() {
				e.mu.Lock()
				if r.conns[id] == c {
					delete(r.conns, id)
					if e.done == done {
						e.reportHomesLocked()
					}
				}
				e.mu.Unlock()
				close(c.done)
				ws.CloseNow()
				e.redialHomeLater(id, done, time.Second)
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

// redialHomeLater：连收件中继的连接断了（或没连上）就过一会重连。
// 收件中继必须一直连着：别人发给我的包送到那个节点，我不在的话节点只能丢掉，对方还以为发出去了（黑洞，游戏超时）。
func (e *Engine) redialHomeLater(id string, done chan struct{}, after time.Duration) {
	go func() {
		select {
		case <-done:
			return
		case <-time.After(after):
		}
		e.mu.Lock()
		if e.done == done && (e.rs.home == id || e.rs.backup == id) {
			e.ensureRelayLocked(id)
		}
		e.mu.Unlock()
	}()
}

// relayForLocked 返回现在发给这个成员会走的节点（界面显示用），没有就是空（走面板）。调用方持有 e.mu。
func (e *Engine) relayForLocked(p *peer) string {
	cands := p.homes
	if len(cands) == 0 && p.relay != "" {
		cands = []string{p.relay}
	}
	pick, best := "", 0
	for _, id := range cands {
		if e.rs.conns[id] == nil {
			continue
		}
		l := e.rs.latency[id]
		if l <= 0 {
			l = 1 << 20
		}
		if pick == "" || l < best {
			pick, best = id, l
		}
	}
	return pick
}

// relaySend 经节点中继发一个包给座位号为 slot 的成员：从他连着的节点（homes，旧版本只有 primary）里
// 挑一个我也连着、延迟最低的；一个都没连着就返回 false（调用方改走面板），同时在后台去连。
func (e *Engine) relaySend(primary string, homes []string, slot int, pkt []byte) bool {
	cands := homes
	if len(cands) == 0 && primary != "" {
		cands = []string{primary}
	}
	if len(cands) == 0 {
		return false
	}
	e.mu.Lock()
	c := e.rs.conns[e.relayForLocked(&peer{relay: primary, homes: homes})]
	if c == nil {
		for _, id := range cands {
			e.ensureRelayLocked(id)
		}
	}
	e.mu.Unlock()
	if c == nil {
		return false
	}
	b := make([]byte, 1+len(pkt))
	b[0] = byte(slot)
	copy(b[1:], pkt)
	select {
	case c.q <- b:
	case <-c.done:
		return false
	default: // 队列满了：丢（拥塞），不改走面板
	}
	return true
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
