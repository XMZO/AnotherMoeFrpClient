package vnet

import (
	"encoding/binary"
	"net/netip"

	"golang.zx2c4.com/wireguard/tun"

	"moefrp/shared/api"
)

// bcastTun 包住虚拟网卡，让游戏的局域网发现（广播、组播）在房间里能用：
//
//   - 发：IPv4 的受限广播、网段广播、组播，和 IPv6 的组播，WireGuard 按目的地址找不到 peer 会丢掉。
//     这里把整个包封装进一个发给每个在线成员的 IPv4 包（协议号 encapProto），经 WireGuard 送过去。
//   - 收：成员发来的封装包拆开，原样（还是广播 / 组播）交给系统。只改成单播不行：有的游戏是"加入组播组"来监听的
//     （MC Java 的 224.0.2.60:4445），系统不会把发给本机 IP 的单播交给它（"transport endpoint was not found"）。
//   - MC 的局域网发现在 IPv4（224.0.2.60）和 IPv6（ff75:230::60，开了 IPv6 优先时用）之间互相翻译一份：
//     一边只发 IPv4、另一边只听 IPv6 时也能发现（IPv6 地址见 api.VnetIP6）。
type bcastTun struct {
	tun.Device
	e       *Engine
	pending [][]byte
}

// encapProto 是封装包的 IP 协议号（253，RFC 3692 留给实验用）。
const encapProto = 253

func (t *bcastTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		if len(t.pending) > 0 {
			sizes[0] = copy(bufs[0][offset:], t.pending[0])
			t.pending = t.pending[1:]
			return 1, nil
		}
		n, err := t.Device.Read(bufs, sizes, offset)
		if err != nil || n == 0 {
			return n, err
		}
		out := 0
		for i := 0; i < n; i++ {
			p := bufs[i][offset : offset+sizes[i]]
			if isBroadcast(p) {
				if self, ok := t.e.selfIP(); ok {
					for _, ip := range t.e.onlineIPs() {
						t.pending = append(t.pending, encap(p, self, ip))
					}
				}
				continue
			}
			if out != i {
				copy(bufs[out][offset:], p)
				sizes[out] = sizes[i]
			}
			out++
		}
		if out > 0 {
			return out, nil
		}
	}
}

// Write：成员发来的封装包拆开再交给系统；MC 的局域网发现另外翻译一份另一种 IP 版本的。
func (t *bcastTun) Write(bufs [][]byte, offset int) (int, error) {
	var extra [][]byte
	for i, b := range bufs {
		inner, ok := decap(b[offset:])
		if !ok {
			continue
		}
		if x := translateMCLan(inner); x != nil {
			nb := make([]byte, offset+len(x))
			copy(nb[offset:], x)
			extra = append(extra, nb)
		}
		n := copy(b[offset:], inner)
		bufs[i] = b[:offset+n]
	}
	if extra == nil {
		return t.Device.Write(bufs, offset)
	}
	all := append(append(make([][]byte, 0, len(bufs)+len(extra)), bufs...), extra...)
	n, err := t.Device.Write(all, offset)
	return min(n, len(bufs)), err
}

// isBroadcast：IPv4 的受限广播、虚拟网段的广播、组播；IPv6 的组播。
func isBroadcast(p []byte) bool {
	switch {
	case len(p) >= 20 && p[0]>>4 == 4:
		return isBroadcast4(p[16:20])
	case len(p) >= 40 && p[0]>>4 == 6:
		return p[24] == 0xff
	}
	return false
}

func isBroadcast4(d []byte) bool {
	switch {
	case d[0] == 255 && d[1] == 255 && d[2] == 255 && d[3] == 255:
		return true
	case d[0] == 10 && d[1] == 241 && d[2] == 0 && d[3] == 255:
		return true
	case d[0] >= 224 && d[0] <= 239:
		return true
	}
	return false
}

// encap 把一个包封装进 src → dst 的 IPv4 包。
func encap(inner []byte, src, dst netip.Addr) []byte {
	c := make([]byte, 20+len(inner))
	c[0], c[8], c[9] = 0x45, 64, encapProto
	binary.BigEndian.PutUint16(c[2:4], uint16(len(c)))
	s, d := src.As4(), dst.As4()
	copy(c[12:16], s[:])
	copy(c[16:20], d[:])
	binary.BigEndian.PutUint16(c[10:12], ^fold(sum(c[:20], 0)))
	copy(c[20:], inner)
	return c
}

// decap：来自虚拟网段的封装包，里面是广播 / 组播包时返回它。
func decap(p []byte) ([]byte, bool) {
	if len(p) < 40 || p[0] != 0x45 || p[9] != encapProto || p[12] != 10 || p[13] != 241 || p[14] != 0 ||
		int(binary.BigEndian.Uint16(p[2:4])) != len(p) {
		return nil, false
	}
	inner := p[20:]
	if !isBroadcast(inner) {
		return nil, false
	}
	return inner, true
}

// MC Java 的局域网发现：组播地址（IPv4 / IPv6）和端口。
var (
	mcLan4       = [4]byte{224, 0, 2, 60}
	mcLan6       = netip.MustParseAddr("ff75:230::60").As16()
	mcLanPort    = uint16(4445)
	vnetPrefix6  = netip.MustParseAddr(api.VnetIP6Prefix).As16()
	vnetPrefix6N = 8 // 前 8 字节（/64）
)

// translateMCLan：MC 的局域网发现包，返回另一种 IP 版本的同样内容（源地址换成同一个成员的另一种地址）；不是就返回 nil。
func translateMCLan(p []byte) []byte {
	switch {
	case len(p) >= 28 && p[0] == 0x45 && p[9] == 17 && [4]byte(p[16:20]) == mcLan4 &&
		p[12] == 10 && p[13] == 241 && p[14] == 0 && binary.BigEndian.Uint16(p[22:24]) == mcLanPort:
		udp := p[20:]
		c := make([]byte, 40+len(udp))
		c[0] = 0x60
		binary.BigEndian.PutUint16(c[4:6], uint16(len(udp)))
		c[6], c[7] = 17, max(p[8], 1) // 下一个头 UDP；跳数沿用 TTL
		src := netip.MustParseAddr(api.VnetIP6(int(p[15]))).As16()
		copy(c[8:24], src[:])
		copy(c[24:40], mcLan6[:])
		copy(c[40:], udp)
		udpChecksum6(c)
		return c
	case len(p) >= 48 && p[0]>>4 == 6 && p[6] == 17 && [16]byte(p[24:40]) == mcLan6 &&
		[8]byte(p[8:16]) == [8]byte(vnetPrefix6[:vnetPrefix6N]) && binary.BigEndian.Uint16(p[42:44]) == mcLanPort:
		slot := binary.BigEndian.Uint16(p[22:24]) // 源地址的最后两字节是座位号
		if slot < 1 || slot > 250 {
			return nil
		}
		udp := p[40:]
		c := make([]byte, 20+len(udp))
		c[0], c[8], c[9] = 0x45, max(p[7], 1), 17
		binary.BigEndian.PutUint16(c[2:4], uint16(len(c)))
		copy(c[12:16], []byte{10, 241, 0, byte(slot)})
		copy(c[16:20], mcLan4[:])
		copy(c[20:], udp)
		fixChecksums(c)
		return c
	}
	return nil
}

// udpChecksum6 重算 IPv6 包里 UDP 的校验和（IPv6 里 UDP 必须带校验和）。
func udpChecksum6(c []byte) {
	u := c[40:]
	if len(u) < 8 {
		return
	}
	ulen := int(binary.BigEndian.Uint16(u[4:6]))
	if ulen < 8 || ulen > len(u) {
		return
	}
	u[6], u[7] = 0, 0
	s := sum(c[8:40], 0) + uint32(ulen) + 17 // 伪首部：源、目的地址，长度，下一个头
	s = sum(u[:ulen], s)
	ck := ^fold(s)
	if ck == 0 {
		ck = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:8], ck)
}

// fixChecksums 重算 IPv4 头校验和；UDP（第一个分片、带校验和的）也重算。
func fixChecksums(c []byte) {
	ihl := int(c[0]&0x0f) * 4
	if ihl < 20 || ihl > len(c) {
		return
	}
	c[10], c[11] = 0, 0
	binary.BigEndian.PutUint16(c[10:12], ^fold(sum(c[:ihl], 0)))
	frag := binary.BigEndian.Uint16(c[6:8]) & 0x1fff
	if c[9] != 17 || frag != 0 || len(c) < ihl+8 { // 只管 UDP 的第一个分片
		return
	}
	u := c[ihl:]
	ulen := int(binary.BigEndian.Uint16(u[4:6]))
	if ulen < 8 || ulen > len(u) || (u[6] == 0 && u[7] == 0) {
		return // 长度不对，或者没用校验和
	}
	u[6], u[7] = 0, 0
	s := sum(c[12:20], 0) // 伪首部：源、目的地址
	s += 17 + uint32(ulen)
	s = sum(u[:ulen], s)
	ck := ^fold(s)
	if ck == 0 {
		ck = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:8], ck)
}

func sum(b []byte, s uint32) uint32 {
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	return s
}

func fold(s uint32) uint16 {
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return uint16(s)
}
