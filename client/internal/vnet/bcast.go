package vnet

import (
	"encoding/binary"
	"net/netip"

	"golang.zx2c4.com/wireguard/tun"
)

// bcastTun 包住虚拟网卡：游戏发现局域网房间用的广播（255.255.255.255、10.241.0.255）和组播包，
// WireGuard 按目的地址找不到 peer 会丢掉，这里把它们复制成发给每个在线成员的单播（改目的地址、重算校验和）。
// 对方的游戏只要监听在 0.0.0.0 上（绝大多数如此）就能收到。
type bcastTun struct {
	tun.Device
	e       *Engine
	pending [][]byte
}

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
				for _, ip := range t.e.onlineIPs() {
					t.pending = append(t.pending, unicastCopy(p, ip))
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

// isBroadcast：IPv4 的受限广播、虚拟网段的广播、组播。
func isBroadcast(p []byte) bool {
	if len(p) < 20 || p[0]>>4 != 4 {
		return false
	}
	d := p[16:20]
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

// unicastCopy 复制一个 IPv4 包，目的地址换成 dst，重算 IP 头校验和；UDP 带校验和的也重算。
func unicastCopy(p []byte, dst netip.Addr) []byte {
	c := make([]byte, len(p))
	copy(c, p)
	d := dst.As4()
	copy(c[16:20], d[:])
	ihl := int(c[0]&0x0f) * 4
	if ihl < 20 || ihl > len(c) {
		return c
	}
	c[10], c[11] = 0, 0
	binary.BigEndian.PutUint16(c[10:12], ^fold(sum(c[:ihl], 0)))
	frag := binary.BigEndian.Uint16(c[6:8]) & 0x1fff
	if c[9] == 17 && frag == 0 && len(c) >= ihl+8 { // UDP，第一个分片
		u := c[ihl:]
		ulen := int(binary.BigEndian.Uint16(u[4:6]))
		if ulen < 8 || ulen > len(u) || (u[6] == 0 && u[7] == 0) {
			return c // 长度不对，或者没用校验和
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
	return c
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
