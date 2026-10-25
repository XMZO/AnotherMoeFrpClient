package vnet

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// 广播包改成单播后，IP 头和 UDP 的校验和都要对
func TestUnicastCopy(t *testing.T) {
	payload := []byte("MC-LAN discovery")
	p := make([]byte, 28+len(payload))
	p[0], p[8], p[9] = 0x45, 64, 17
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[12:16], []byte{10, 241, 0, 1})
	copy(p[16:20], []byte{255, 255, 255, 255})
	binary.BigEndian.PutUint16(p[20:22], 4445)
	binary.BigEndian.PutUint16(p[22:24], 4445)
	binary.BigEndian.PutUint16(p[24:26], uint16(8+len(payload)))
	copy(p[28:], payload)
	p[26], p[27] = 0xbe, 0xef // 原来有校验和（随便填，改写后会重算）
	if !isBroadcast(p) {
		t.Fatal("应识别为广播")
	}
	c := unicastCopy(p, netip.MustParseAddr("10.241.0.7"))
	if c[19] != 7 || isBroadcast(c) {
		t.Fatalf("目的地址：%v", c[16:20])
	}
	if fold(sum(c[:20], 0)) != 0xffff {
		t.Fatal("IP 头校验和不对")
	}
	s := sum(c[12:20], 0) + 17 + uint32(8+len(payload))
	if fold(sum(c[20:], s)) != 0xffff {
		t.Fatal("UDP 校验和不对")
	}
}
