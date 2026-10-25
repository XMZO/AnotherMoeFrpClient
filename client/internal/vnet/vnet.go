// Package vnet 是联机房间的虚拟局域网（shared/api/vnet.go）。
//
// 一块虚拟网卡（Wintun，wintun.dll 编在程序里、从内存加载）+ WireGuard 加密。每个成员是一个 WireGuard peer，
// 它的"地址"是座位号，由 bind 决定怎么送达：
//   - 直连：ICE 打洞（pion/ice，UDP、IPv4/IPv6），打通了就走它；
//   - 中继：打不通、还在打或者断了，经面板的实时通道转发（Transport.Relay）。
//
// 打洞用的 socket 绑在物理网卡的地址上（过滤掉 VPN、代理软件的 TUN 网卡），Windows 按源地址选出口，
// 不会被 Clash / sing-box 等的 TUN 模式接管。游戏发现局域网房间用的广播、组播包，复制成发给每个成员的单播。
package vnet

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"moefrp/shared/api"
)

// Transport 是到面板的实时通道：发打洞信息、中继数据包。
type Transport interface {
	Signal(room string, to int, data string) error
	Relay(to int, payload []byte) error       // 经面板中继
	SetRelay(room string, relay string) error // 告诉面板我的收件中继（节点 ID，空表示面板）
}

// 连接方式。
const (
	ModeDirect     = "direct"     // 打洞直连
	ModeRelay      = "relay"      // 经面板中继
	ModeOffline    = "offline"    // 不在房间里
	ModeConnecting = "connecting" // 在线，正在打洞（数据先走中继）
)

// PeerState 是界面上的一个成员。
type PeerState struct {
	Slot     int    `json:"slot"`
	Nickname string `json:"nickname"`
	IP       string `json:"ip"`
	Self     bool   `json:"self"`
	Mode     string `json:"mode"`
	RTT      int    `json:"rtt,omitempty"` // 延迟（毫秒），0 表示还不知道
	Via      string `json:"via,omitempty"` // 走中继时，发给他的数据经哪个节点（"面板" 表示面板）
}

// AdapterName 是虚拟网卡的名字（网络连接里看到的）。
const AdapterName = "MoeFrp"

const mtu = 1380

// Engine 是一次进房间的虚拟网。
type Engine struct {
	tr       Transport
	logf     func(string, ...any)
	onChange func()

	mu    sync.Mutex
	room  string
	self  int
	priv  []byte
	pub   string // base64，发给面板
	tun   tun.Device
	dev   *device.Device
	bind  *bind
	peers map[int]*peer
	done  chan struct{}
	sigq  chan outSignal
	rs    relayState
	name  string // 房间名（面板在成员列表里带过来，房主改名后跟着变）
}

type peer struct {
	slot     int
	nickname string
	ip       string
	key      string // base64 公钥，空表示不在线
	relay    string // 他的收件中继（节点 ID），空表示面板
	ice      *iceSession
	direct   *directConn
	rtt      time.Duration // 延迟（平滑后），见 latency.go
	rttAt    time.Time
}

// New 创建引擎，还没有网卡。onChange 在成员或连接方式变化时调用（界面刷新）。
func New(tr Transport, logf func(string, ...any), onChange func()) *Engine {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if onChange == nil {
		onChange = func() {}
	}
	return &Engine{tr: tr, logf: logf, onChange: onChange, peers: map[int]*peer{}}
}

// Start 建虚拟网卡和 WireGuard，返回这次的公钥（进房间时发给面板）。需要管理员权限。
func (e *Engine) Start(room string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dev != nil {
		if e.room == room {
			return e.pub, nil
		}
		return "", errors.New("已经在另一个房间里")
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	t, err := tun.CreateTUN(AdapterName, mtu)
	if err != nil {
		return "", fmt.Errorf("创建虚拟网卡失败：%w", err)
	}
	e.room, e.self, e.priv, e.pub = room, 0, key.Bytes(), base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	e.tun = t
	e.bind = newBind(e)
	e.done = make(chan struct{})
	e.sigq = make(chan outSignal, 256)
	go e.sendSignals(e.sigq, e.done)
	go e.pingLoop(e.done)
	e.dev = device.NewDevice(&bcastTun{Device: t, e: e}, e.bind, device.NewLogger(device.LogLevelError, "vnet: "))
	if err := e.dev.IpcSet("private_key=" + hex.EncodeToString(e.priv) + "\n"); err != nil {
		e.closeLocked()
		return "", err
	}
	if err := e.dev.Up(); err != nil {
		e.closeLocked()
		return "", err
	}
	return e.pub, nil
}

// Stop 关掉虚拟网卡和所有连接。
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
}

func (e *Engine) closeLocked() {
	for _, p := range e.peers {
		e.dropDirectLocked(p)
		if p.ice != nil {
			p.ice.close()
		}
	}
	e.peers = map[int]*peer{}
	e.closeRelaysLocked()
	if e.done != nil {
		close(e.done)
		e.done, e.sigq = nil, nil
	}
	if e.dev != nil {
		e.dev.Close() // 也会关掉 tun 和 bind
	} else if e.tun != nil {
		_ = e.tun.Close()
	}
	e.dev, e.tun, e.bind, e.room, e.self, e.name = nil, nil, nil, "", 0, ""
}

// Room 返回当前房间（没在房间里为空）。
func (e *Engine) Room() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.room
}

// Running 判断虚拟网是否在运行。
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dev != nil
}

// PublicKey 返回这次的公钥。
func (e *Engine) PublicKey() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pub
}

// SetPeers 按面板推来的成员列表更新：自己的座位（第一次时配上虚拟 IP）、WireGuard peers、打洞、中继。
func (e *Engine) SetPeers(msg api.LiveVnetBody) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dev == nil {
		return nil
	}
	self, list := msg.Self, msg.Peers
	if msg.RoomName != "" {
		e.name = msg.RoomName
	}
	defer e.updateRelaysLocked(msg)
	if e.self != self {
		e.self = self
		if err := configureAdapter(AdapterName, api.VnetIP(self)); err != nil {
			e.logf("vnet: configure adapter: %v", err)
			return fmt.Errorf("设置虚拟网卡地址失败：%w", err)
		}
	}
	seen := map[int]bool{}
	var b strings.Builder
	for _, x := range list {
		if x.Slot == self || x.Slot < 1 || x.Slot > 250 {
			continue
		}
		seen[x.Slot] = true
		p := e.peers[x.Slot]
		if p == nil {
			p = &peer{slot: x.Slot}
			e.peers[x.Slot] = p
		}
		p.nickname, p.ip, p.relay = x.Nickname, x.IP, x.Relay
		if p.key == x.Key {
			continue
		}
		// 公钥变了：对方重新进了房间（或者下线了）
		if old := wgKeyHex(p.key); old != "" {
			fmt.Fprintf(&b, "public_key=%s\nremove=true\n", old)
		}
		e.dropDirectLocked(p)
		if p.ice != nil {
			p.ice.close()
			p.ice = nil
		}
		p.key, p.rtt = x.Key, 0
		if k := wgKeyHex(x.Key); k != "" {
			fmt.Fprintf(&b, "public_key=%s\nreplace_allowed_ips=true\nallowed_ip=%s/32\nendpoint=%d\npersistent_keepalive_interval=25\n", k, x.IP, x.Slot)
			if self < x.Slot { // 座位号小的一方发起打洞
				e.startICELocked(p, 0)
			}
		}
	}
	for slot, p := range e.peers {
		if seen[slot] {
			continue
		}
		if k := wgKeyHex(p.key); k != "" {
			fmt.Fprintf(&b, "public_key=%s\nremove=true\n", k)
		}
		e.dropDirectLocked(p)
		if p.ice != nil {
			p.ice.close()
		}
		delete(e.peers, slot)
	}
	if b.Len() > 0 {
		if err := e.dev.IpcSet(b.String()); err != nil {
			return err
		}
	}
	go e.onChange()
	return nil
}

// Snapshot 是界面上的状态。
type Snapshot struct {
	Room     string           `json:"room"`
	RoomName string           `json:"roomName,omitempty"`
	Self     int              `json:"self"`
	Peers    []PeerState      `json:"peers"`
	Relays   []RelayView      `json:"relays"`   // 能用的中继节点（按延迟）
	Home     string           `json:"home"`     // 我的收件中继（节点 ID，空表示面板）
	Pref     string           `json:"pref"`     // 我的选择：auto / panel / 节点 ID
	Settings api.RoomSettings `json:"settings"` // 房间设置（默认中继、是否锁定）
}

// State 返回成员、连接方式和中继（不含自己）。
func (e *Engine) State() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	pref := e.rs.pref
	if pref == "" {
		pref = RelayAuto
	}
	st := Snapshot{Room: e.room, RoomName: e.name, Self: e.self, Peers: []PeerState{}, Relays: e.relayViewsLocked(), Home: e.rs.home, Pref: pref, Settings: e.rs.settings}
	for _, p := range e.peers {
		mode := ModeOffline
		switch {
		case p.key == "":
		case p.direct != nil:
			mode = ModeDirect
		case p.ice != nil:
			mode = ModeConnecting
		default:
			mode = ModeRelay
		}
		via := ""
		if mode == ModeRelay || mode == ModeConnecting {
			via = "面板"
			if n, ok := e.rs.nodes[p.relay]; ok && e.rs.conns[p.relay] != nil {
				via = n.Name
			}
		}
		st.Peers = append(st.Peers, PeerState{Slot: p.slot, Nickname: p.nickname, IP: p.ip, Mode: mode, RTT: p.rttMs(), Via: via})
	}
	slices.SortFunc(st.Peers, func(a, b PeerState) int { return a.Slot - b.Slot })
	return st
}

// OnPacket 收到面板中继过来的数据包。
func (e *Engine) OnPacket(from int, payload []byte) {
	e.mu.Lock()
	b := e.bind
	e.mu.Unlock()
	if b != nil {
		b.deliver(from, payload)
	}
}

// send 把 WireGuard 的数据包送给座位号为 slot 的成员：打通了就直连，否则送到他的收件中继，再不行走面板。
func (e *Engine) send(slot int, pkt []byte) error {
	e.mu.Lock()
	var d *directConn
	relay := ""
	if p := e.peers[slot]; p != nil {
		d, relay = p.direct, p.relay
	}
	e.mu.Unlock()
	if d != nil {
		if _, err := d.conn.Write(pkt); err == nil {
			return nil
		}
		e.mu.Lock()
		if p := e.peers[slot]; p != nil && p.direct == d {
			e.dropDirectLocked(p)
			go e.onChange()
		}
		e.mu.Unlock()
	}
	if relay != "" && e.relaySend(relay, slot, pkt) {
		return nil
	}
	return e.tr.Relay(slot, pkt)
}

// onlineIPs 返回在线成员的虚拟 IP（广播复制给他们）。
func (e *Engine) onlineIPs() []netip.Addr {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []netip.Addr
	for _, p := range e.peers {
		if p.key == "" {
			continue
		}
		if a, err := netip.ParseAddr(p.ip); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func wgKeyHex(b64 string) string {
	if b64 == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != 32 {
		return ""
	}
	return hex.EncodeToString(b)
}

// ---- bind：WireGuard 的"网络"，地址是座位号 ----

type pkt struct {
	slot int
	b    []byte
}

type bind struct {
	e      *Engine
	recv   chan pkt
	mu     sync.Mutex
	closed chan struct{}
}

func newBind(e *Engine) *bind { return &bind{e: e, recv: make(chan pkt, 1024)} }

func (b *bind) deliver(from int, payload []byte) {
	if b.e.handleProbe(from, payload) {
		return
	}
	c := make([]byte, len(payload))
	copy(c, payload)
	select {
	case b.recv <- pkt{from, c}:
	default: // 来不及处理就丢，WireGuard 会重传
	}
}

func (b *bind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	closed := make(chan struct{})
	b.closed = closed
	b.mu.Unlock()
	fn := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		select {
		case p := <-b.recv:
			sizes[0] = copy(packets[0], p.b)
			eps[0] = endpoint(p.slot)
			return 1, nil
		case <-closed:
			return 0, net.ErrClosed
		}
	}
	return []conn.ReceiveFunc{fn}, 1, nil
}

func (b *bind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed != nil {
		close(b.closed)
		b.closed = nil
	}
	return nil
}

func (b *bind) SetMark(uint32) error { return nil }
func (b *bind) BatchSize() int       { return 1 }

func (b *bind) Send(bufs [][]byte, ep conn.Endpoint) error {
	slot, ok := ep.(endpoint)
	if !ok {
		return conn.ErrWrongEndpointType
	}
	for _, buf := range bufs {
		if err := b.e.send(int(slot), buf); err != nil {
			return err
		}
	}
	return nil
}

func (b *bind) ParseEndpoint(s string) (conn.Endpoint, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 255 {
		return nil, fmt.Errorf("bad endpoint %q", s)
	}
	return endpoint(n), nil
}

// endpoint 是成员的座位号。
type endpoint int

func (endpoint) ClearSrc()             {}
func (endpoint) SrcToString() string   { return "" }
func (p endpoint) DstToString() string { return strconv.Itoa(int(p)) }
func (p endpoint) DstToBytes() []byte  { return []byte{byte(p)} }
func (p endpoint) DstIP() netip.Addr   { return netip.MustParseAddr(api.VnetIP(int(p))) }
func (endpoint) SrcIP() netip.Addr     { return netip.Addr{} }

// ---- 直连 ----

type directConn struct {
	conn interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}
}

// useDirect 打洞成功：之后发给这个成员的包都走直连，收到的交给 WireGuard。
func (e *Engine) useDirect(p *peer, s *iceSession, c *directConn) {
	e.mu.Lock()
	if e.peers[p.slot] != p || p.ice != s || e.bind == nil {
		e.mu.Unlock()
		_ = c.conn.Close()
		return
	}
	p.direct = c
	b := e.bind
	e.mu.Unlock()
	e.logf("vnet: %s 打洞成功，直连", p.nickname)
	go e.onChange()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := c.conn.Read(buf)
			if err != nil {
				e.mu.Lock()
				if p.direct == c {
					e.dropDirectLocked(p)
				}
				e.mu.Unlock()
				go e.onChange()
				return
			}
			b.deliver(p.slot, buf[:n])
		}
	}()
}

func (e *Engine) dropDirectLocked(p *peer) {
	if p.direct != nil {
		_ = p.direct.conn.Close()
		p.direct = nil
	}
}

// retryLater：打洞没成功，过一会再试（数据一直走中继）。只由发起方（座位号小的）调用。
func (e *Engine) retryLater(p *peer, s *iceSession) {
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	if done == nil {
		return
	}
	go func() {
		select {
		case <-done:
			return
		case <-time.After(45 * time.Second):
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.peers[p.slot] == p && p.ice == s && p.direct == nil && p.key != "" {
			s.close()
			e.startICELocked(p, s.gen+1)
		}
	}()
}
