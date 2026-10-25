package vnet

import (
	"net"

	transport "github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
)

// udpBuffer 是打洞（直连）UDP socket 的收发缓冲区大小。Windows 默认很小（几十 KB），
// 游戏突发大量数据时（MC 走动加载区块）缓冲区一满，包就在本机被丢了，TCP 重传、回弹、延迟乱跳。
const udpBuffer = 4 << 20

// bufNet 是给 pion/ice 用的网络：和标准的一样，只是新建的 UDP socket 把收发缓冲区调大。
type bufNet struct{ *stdnet.Net }

func newBufNet() (transport.Net, error) {
	n, err := stdnet.NewNet()
	if err != nil {
		return nil, err
	}
	return bufNet{n}, nil
}

type bufferSetter interface {
	SetReadBuffer(bytes int) error
	SetWriteBuffer(bytes int) error
}

func growBuffers(c any) {
	if b, ok := c.(bufferSetter); ok {
		_ = b.SetReadBuffer(udpBuffer)
		_ = b.SetWriteBuffer(udpBuffer)
	}
}

func (n bufNet) ListenUDP(network string, laddr *net.UDPAddr) (transport.UDPConn, error) {
	c, err := n.Net.ListenUDP(network, laddr)
	if err == nil {
		growBuffers(c)
	}
	return c, err
}

func (n bufNet) ListenPacket(network, address string) (net.PacketConn, error) {
	c, err := n.Net.ListenPacket(network, address)
	if err == nil {
		growBuffers(c)
	}
	return c, err
}
