package live

import (
	"context"
	"slices"
	"strings"
	"time"

	"moefrp/shared/api"
)

// 联机房间（shared/api/vnet.go）：哪些客户端正在哪个房间的虚拟网里、各自的 WireGuard 公钥，只放在内存里。
// 打洞信息（signal）和打不通时的数据包（二进制帧）都经这里在同一房间的成员之间转发。

// VnetRooms 由面板实现：查成员资格和成员列表。
type VnetRooms interface {
	Slot(ctx context.Context, roomID, userID string) (int, error) // 不是成员时返回错误（游客的 userID 是 api.GuestPrefix + 游客 ID）
	Members(ctx context.Context, roomID string) ([]api.RoomMember, error)
	// RelayInfo 返回房间名、房间设置、能用的中继节点，以及每个座位连各中继的票据（座位号 → 节点 ID → 票据）。
	RelayInfo(ctx context.Context, roomID string, slots []int) (api.Room, []api.VnetRelay, map[int]map[string]string)
}

// vnetState 是一个客户端在虚拟网里的状态，h.mu 保护。
type vnetState struct {
	room  string
	slot  int
	key   string
	relay string   // 收件中继（节点 ID），空表示面板
	homes []string // 连着的所有收件中继（主的在前）
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
		hist := append([]api.VnetChatMsg(nil), h.chat[b.Room]...)
		h.mu.Unlock()
		if len(hist) > 0 { // 刚进来的人看到最近的聊天
			c.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetChat, Room: b.Room, History: hist}})
		}
		if old != nil && old.room != b.Room {
			h.VnetRoomChanged(ctx, old.room)
		}
		h.VnetRoomChanged(ctx, b.Room)
	case api.VnetRelayOp:
		if len(b.Relay) > 64 || len(b.Homes) > api.VnetMaxHomes {
			return
		}
		for _, x := range b.Homes {
			if len(x) > 64 {
				return
			}
		}
		h.mu.Lock()
		me := c.vnet
		changed := me != nil && me.room == b.Room && (me.relay != b.Relay || !slices.Equal(me.homes, b.Homes))
		if changed {
			me.relay, me.homes = b.Relay, slices.Clone(b.Homes)
		}
		h.mu.Unlock()
		if changed {
			h.VnetRoomChanged(ctx, b.Room)
		}
	case api.VnetChat:
		text := []rune(strings.TrimSpace(b.Text))
		if len(text) == 0 {
			return
		}
		if len(text) > api.VnetChatMax {
			text = text[:api.VnetChatMax]
		}
		h.mu.Lock()
		me := c.vnet
		if me == nil || me.room != b.Room {
			h.mu.Unlock()
			return
		}
		msg := api.VnetChatMsg{From: me.slot, Nickname: c.Nickname, Text: string(text), At: h.now().UnixMilli()}
		if h.chat == nil {
			h.chat = map[string][]api.VnetChatMsg{}
		}
		hist := append(h.chat[me.room], msg)
		if len(hist) > api.VnetChatKeep {
			hist = hist[len(hist)-api.VnetChatKeep:]
		}
		h.chat[me.room] = hist
		var to []*Client
		for _, x := range h.clients {
			if x.vnet != nil && x.vnet.room == me.room {
				to = append(to, x)
			}
		}
		h.mu.Unlock()
		for _, x := range to {
			x.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetChat, Room: b.Room, Chat: &msg}})
		}
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
		members = nil // 房间没了：所有人移出，聊天记录也清掉
		h.mu.Lock()
		delete(h.chat, room)
		h.mu.Unlock()
	}
	slots := map[int]api.RoomMember{}
	for _, m := range members {
		slots[m.Slot] = m
	}
	h.mu.Lock()
	var inRoom, gone []*Client
	keys, relays, homes := map[int]string{}, map[int]string{}, map[int][]string{}
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
		relays[x.vnet.slot] = x.vnet.relay
		homes[x.vnet.slot] = x.vnet.homes
	}
	selfSlot := map[*Client]int{}
	for _, x := range inRoom {
		selfSlot[x] = x.vnet.slot
	}
	h.mu.Unlock()

	peers := make([]api.VnetPeer, 0, len(members))
	for _, m := range members {
		peers = append(peers, api.VnetPeer{Slot: m.Slot, Nickname: m.Nickname, IP: m.IP, Key: keys[m.Slot], Relay: relays[m.Slot], Homes: homes[m.Slot],
			Guest: strings.HasPrefix(m.UserID, api.GuestPrefix)})
	}
	var info api.Room
	var relayNodes []api.VnetRelay
	var tickets map[int]map[string]string
	if len(inRoom) > 0 {
		slots := make([]int, 0, len(inRoom))
		for _, x := range inRoom {
			slots = append(slots, selfSlot[x])
		}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, relayNodes, tickets = h.Rooms.RelayInfo(qctx, room, slots)
		cancel()
	}
	for _, x := range inRoom {
		st := info.RoomSettings
		x.trySend(api.LiveMessage{Type: api.LiveVnet, Vnet: &api.LiveVnetBody{Op: api.VnetPeers, Room: room, Self: selfSlot[x], Peers: peers,
			Settings: &st, Relays: relayNodes, Tickets: tickets[selfSlot[x]], RoomName: info.Name, RoomCode: info.Code}})
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
