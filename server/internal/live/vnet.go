package live

import (
	"context"
	"time"

	"moefrp/shared/api"
)

// 联机房间（shared/api/vnet.go）：哪些客户端正在哪个房间的虚拟网里、各自的 WireGuard 公钥，只放在内存里。
// 打洞信息（signal）和打不通时的数据包（二进制帧）都经这里在同一房间的成员之间转发。

// VnetRooms 由面板实现：查成员资格和成员列表。
type VnetRooms interface {
	Slot(ctx context.Context, roomID, userID string) (int, error) // 不是成员时返回错误
	Members(ctx context.Context, roomID string) ([]api.RoomMember, error)
}

// vnetState 是一个客户端在虚拟网里的状态，h.mu 保护。
type vnetState struct {
	room string
	slot int
	key  string
}

const relayQueue = 256

func (h *Hub) onVnet(ctx context.Context, c *Client, b *api.LiveVnetBody) {
	if b == nil || h.Rooms == nil {
		return
	}
	switch b.Op {
	case api.VnetJoin:
		if len(b.Key) > 64 || b.Room == "" {
			return
		}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		slot, err := h.Rooms.Slot(qctx, b.Room, c.UserID)
		cancel()
		if err != nil {
			c.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetClosed, Room: b.Room, Reason: "你不在这个房间里"}})
			return
		}
		h.mu.Lock()
		old := c.vnet
		c.vnet = &vnetState{room: b.Room, slot: slot, key: b.Key}
		h.mu.Unlock()
		if old != nil && old.room != b.Room {
			h.VnetRoomChanged(ctx, old.room)
		}
		h.VnetRoomChanged(ctx, b.Room)
	case api.VnetLeave:
		h.mu.Lock()
		old := c.vnet
		c.vnet = nil
		h.mu.Unlock()
		if old != nil {
			h.VnetRoomChanged(ctx, old.room)
		}
	case api.VnetSignal:
		if len(b.Data) > 8<<10 {
			return
		}
		h.mu.Lock()
		me := c.vnet
		var to *Client
		if me != nil {
			to = h.vnetMemberLocked(me.room, b.To)
		}
		h.mu.Unlock()
		if to != nil {
			to.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetSignal, Room: me.room, From: me.slot, Data: b.Data}})
		}
	}
}

// relay 转发一个中继帧：b[0] 是对方座位号，转发时换成发送方的。
func (h *Hub) relay(c *Client, b []byte) {
	if len(b) < 2 {
		return
	}
	h.mu.Lock()
	me := c.vnet
	var to *Client
	if me != nil {
		to = h.vnetMemberLocked(me.room, int(b[0]))
	}
	h.mu.Unlock()
	if to == nil || to == c {
		return
	}
	out := make([]byte, len(b))
	copy(out, b)
	out[0] = byte(me.slot)
	select {
	case to.sendBin <- out:
	default: // 对方发不过来就丢，WireGuard 自己会重传
	}
}

func (h *Hub) vnetMemberLocked(room string, slot int) *Client {
	for _, x := range h.clients {
		if x.vnet != nil && x.vnet.room == room && x.vnet.slot == slot {
			return x
		}
	}
	return nil
}

// VnetRoomChanged 给房间里在线的客户端推一次成员列表（成员进出、有人上下线、改了名单时调用）；
// 已经不是成员的客户端收到 closed 并移出。
func (h *Hub) VnetRoomChanged(ctx context.Context, room string) {
	if h.Rooms == nil {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	members, err := h.Rooms.Members(qctx, room)
	cancel()
	if err != nil {
		members = nil // 房间没了：所有人移出
	}
	slots := map[int]api.RoomMember{}
	for _, m := range members {
		slots[m.Slot] = m
	}
	h.mu.Lock()
	var inRoom, gone []*Client
	keys := map[int]string{}
	for _, x := range h.clients {
		if x.vnet == nil || x.vnet.room != room {
			continue
		}
		if m, ok := slots[x.vnet.slot]; !ok || m.UserID != x.UserID {
			x.vnet = nil
			gone = append(gone, x)
			continue
		}
		inRoom = append(inRoom, x)
		keys[x.vnet.slot] = x.vnet.key
	}
	selfSlot := map[*Client]int{}
	for _, x := range inRoom {
		selfSlot[x] = x.vnet.slot
	}
	h.mu.Unlock()

	peers := make([]api.VnetPeer, 0, len(members))
	for _, m := range members {
		peers = append(peers, api.VnetPeer{Slot: m.Slot, Nickname: m.Nickname, IP: m.IP, Key: keys[m.Slot]})
	}
	for _, x := range inRoom {
		x.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetPeers, Room: room, Self: selfSlot[x], Peers: peers}})
	}
	for _, x := range gone {
		x.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetClosed, Room: room, Reason: "房间已解散，或你被移出了房间"}})
	}
}

// VnetOnline 返回房间里正在虚拟网中的成员座位号。
func (h *Hub) VnetOnline(room string) map[int]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[int]bool{}
	for _, x := range h.clients {
		if x.vnet != nil && x.vnet.room == room {
			out[x.vnet.slot] = true
		}
	}
	return out
}

// trySend 发一条消息，队列满了就丢（不阻塞调用方）。
func (c *Client) trySend(m api.LiveMessage) {
	select {
	case c.send <- m:
	default:
	}
}
