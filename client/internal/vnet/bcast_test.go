package vnet

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

func udpPacket(src, dst [4]byte, payload []byte) []byte {
	p := make([]byte, 28+len(payload))
	p[0], p[8], p[9] = 0x45, 1, 17
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[12:16], src[:])
	copy(p[16:20], dst[:])
	binary.BigEndian.PutUint16(p[20:22], 51444)
	binary.BigEndian.PutUint16(p[22:24], 4445)
	binary.BigEndian.PutUint16(p[24:26], uint16(8+len(payload)))
	copy(p[28:], payload)
	p[26], p[27] = 0xbe, 0xef // 原来有校验和（随便填，下面重算）
	fixChecksums(p)
	return p
}

func check4(t *testing.T, c []byte) {
	t.Helper()
	if fold(sum(c[:20], 0)) != 0xffff {
		t.Fatal("IPv4 头校验和不对")
	}
	ulen := int(binary.BigEndian.Uint16(c[24:26]))
	if fold(sum(c[20:20+ulen], sum(c[12:20], 0)+17+uint32(ulen))) != 0xffff {
		t.Fatal("UDP（IPv4）校验和不对")
	}
}

func check6(t *testing.T, c []byte) {
	t.Helper()
	ulen := int(binary.BigEndian.Uint16(c[44:46]))
	if fold(sum(c[40:40+ulen], sum(c[8:40], 0)+17+uint32(ulen))) != 0xffff {
		t.Fatal("UDP（IPv6）校验和不对")
	}
}

var motd = []byte("[MOTD]A Minecraft Server[/MOTD][AD]25565[/AD]")

// 广播 / 组播封装后原样拆出来
func TestEncapRoundTrip(t *testing.T) {
	self, peer := netip.MustParseAddr("10.241.0.2"), netip.MustParseAddr("10.241.0.1")
	for _, dst := range [][4]byte{{224, 0, 2, 60}, {255, 255, 255, 255}, {10, 241, 0, 255}} {
		p := udpPacket([4]byte{10, 241, 0, 2}, dst, motd)
		if !isBroadcast(p) {
			t.Fatalf("%v 应识别为广播 / 组播", dst)
		}
		c := encap(p, self, peer)
		if c[9] != encapProto || c[19] != 1 || isBroadcast(c) || fold(sum(c[:20], 0)) != 0xffff {
			t.Fatalf("封装包不对：%x", c[:20])
		}
		inner, ok := decap(c)
		if !ok || !bytes.Equal(inner, p) {
			t.Fatal("拆出来应该和原包一样")
		}
	}
	// 普通单播、普通包不拆
	if _, ok := decap(udpPacket([4]byte{10, 241, 0, 2}, [4]byte{10, 241, 0, 1}, []byte("x"))); ok {
		t.Fatal("普通包不该拆")
	}
}

// MC 的局域网发现：IPv4 → IPv6 → IPv4 翻译，内容和源成员不变
func TestTranslateMCLan(t *testing.T) {
	p4 := udpPacket([4]byte{10, 241, 0, 2}, mcLan4, motd)
	p6 := translateMCLan(p4)
	if p6 == nil || p6[0]>>4 != 6 || !isBroadcast(p6) {
		t.Fatal("应翻译成 IPv6 组播")
	}
	if netip.AddrFrom16([16]byte(p6[8:24])).String() != "fd4d:6f65:4672::2" || [16]byte(p6[24:40]) != mcLan6 {
		t.Fatalf("IPv6 地址不对：%x", p6[8:40])
	}
	if !bytes.Equal(p6[48:], motd) {
		t.Fatal("内容变了")
	}
	check6(t, p6)
	back := translateMCLan(p6)
	if back == nil || [4]byte(back[12:16]) != [4]byte{10, 241, 0, 2} || [4]byte(back[16:20]) != mcLan4 || !bytes.Equal(back[28:], motd) {
		t.Fatal("应翻译回同一个成员的 IPv4 组播")
	}
	check4(t, back)
	// 别的端口、别的组播不翻译
	if translateMCLan(udpPacket([4]byte{10, 241, 0, 2}, [4]byte{224, 0, 0, 251}, motd)) != nil {
		t.Fatal("mDNS 不该翻译")
	}
}
