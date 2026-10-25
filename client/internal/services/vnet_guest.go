package services

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// guestLive 是游客的实时通道（没登录时进联机房间）：用游客令牌连 /api/v1/rooms/guest/live，
// 只收发 vnet 消息和中继帧；断了自动重连，令牌失效（被移出、房间解散）时退出游客模式。
type guestLive struct {
	s     *VnetService
	token string
	room  string

	mu   sync.Mutex
	ws   *websocket.Conn
	stop chan struct{}
	once sync.Once
}

func newGuestLive(s *VnetService, token, room string) *guestLive {
	return &guestLive{s: s, token: token, room: room, stop: make(chan struct{})}
}

func (g *guestLive) close() {
	g.once.Do(func() { close(g.stop) })
	g.mu.Lock()
	if g.ws != nil {
		g.ws.CloseNow()
	}
	g.mu.Unlock()
}

func (g *guestLive) run() {
	wait := time.Second
	for {
		select {
		case <-g.stop:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		ws, err := g.s.api.DialGuestLive(ctx, g.token)
		cancel()
		if err != nil {
			if apierr.CodeOf(err) == apierr.Unauthorized {
				go g.s.dropGuest("游客身份已失效（离开了房间、被移出或房间已解散）")
				return
			}
			select {
			case <-g.stop:
				return
			case <-time.After(wait):
			}
			wait = min(wait*2, 30*time.Second)
			continue
		}
		wait = time.Second
		ws.SetReadLimit(64 << 10)
		g.mu.Lock()
		g.ws = ws
		g.mu.Unlock()
		g.s.OnLiveOnline(true)
		for {
			typ, b, err := ws.Read(context.Background())
			if err != nil {
				break
			}
			if typ == websocket.MessageBinary {
				if len(b) > 1 {
					g.s.OnPacket(int(b[0]), b[1:])
				}
				continue
			}
			var m api.LiveMessage
			if json.Unmarshal(b, &m) == nil && m.Type == api.LiveVnet && m.Vnet != nil {
				g.s.OnLive(*m.Vnet)
			}
		}
		g.mu.Lock()
		g.ws = nil
		g.mu.Unlock()
		ws.CloseNow()
	}
}

var errGuestOffline = errors.New("guest channel not connected")

func (g *guestLive) conn() *websocket.Conn {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ws
}

// SendVnet 发一条 vnet 消息。
func (g *guestLive) SendVnet(b api.LiveVnetBody) error {
	ws := g.conn()
	if ws == nil {
		return errGuestOffline
	}
	raw, _ := json.Marshal(api.LiveMessage{Type: api.LiveVnet, Vnet: &b})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, raw)
}

// SendPacket 经面板中继一个包。
func (g *guestLive) SendPacket(to int, payload []byte) error {
	ws := g.conn()
	if ws == nil {
		return errGuestOffline
	}
	b := make([]byte, 1+len(payload))
	b[0] = byte(to)
	copy(b[1:], payload)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageBinary, b)
}
