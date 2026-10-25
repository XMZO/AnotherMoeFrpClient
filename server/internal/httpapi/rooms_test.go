package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// vnetConn 是测试用的实时通道：分开收 vnet 消息和中继帧。
type vnetConn struct {
	t    *testing.T
	ws   *websocket.Conn
	msgs chan api.LiveVnetBody
	pkts chan []byte
}

func (n *nodeEnv) dialVnet(token string) *vnetConn { return n.dialVnetAt(api.RouteClientLive, token) }

func (n *nodeEnv) dialVnetAt(route, token string) *vnetConn {
	n.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set(api.HeaderAuthorization, api.BearerPrefix+token)
	h.Set(api.HeaderClientVersion, "3.1.0")
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(n.http.URL, "http")+route, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		n.t.Fatal(err)
	}
	c := &vnetConn{t: n.t, ws: ws, msgs: make(chan api.LiveVnetBody, 64), pkts: make(chan []byte, 64)}
	go func() {
		for {
			typ, b, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				c.pkts <- b
				continue
			}
			var m api.LiveMessage
			if json.Unmarshal(b, &m) == nil && m.Vnet != nil {
				c.msgs <- *m.Vnet
			}
		}
	}()
	n.t.Cleanup(func() { ws.CloseNow() })
	return c
}

func (c *vnetConn) send(b api.LiveVnetBody) {
	c.t.Helper()
	raw, _ := json.Marshal(api.LiveMessage{Type: api.LiveVnet, Vnet: &b})
	if err := c.ws.Write(context.Background(), websocket.MessageText, raw); err != nil {
		c.t.Fatal(err)
	}
}

// expect 等一条指定操作的 vnet 消息（满足 ok 的）。
func (c *vnetConn) expect(op string, ok func(api.LiveVnetBody) bool) api.LiveVnetBody {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-c.msgs:
			if m.Op == op && (ok == nil || ok(m)) {
				return m
			}
		case <-timeout:
			c.t.Fatalf("等不到 vnet %s", op)
		}
	}
}

// TestRooms：建房、邀请码加入、进虚拟网后互相看到公钥、打洞信息和数据包经面板转发、踢人、解散。
func TestRooms(t *testing.T) {
	n := newNodeEnv(t, withConfigSingle(false))
	n.register("alice", "password-alice")
	n.register("bob", "password-bob")
	n.register("carol", "password-carol")
	ta, tb, tc := n.login("alice", "password-alice"), n.login("bob", "password-bob"), n.login("carol", "password-carol")

	var room api.Room
	n.do(req{method: "POST", path: api.RouteRooms, token: ta, body: api.CreateRoomRequest{Name: "周末 MC"}}).expect(http.StatusCreated).decode(&room)
	if len(room.Code) != 8 || room.Members[0].IP != api.VnetIP(1) {
		t.Fatalf("房间：%+v", room)
	}
	n.do(req{method: "POST", path: api.RouteRoomJoin, token: tb, body: api.JoinRoomRequest{Code: "nope"}}).apiErr(apierr.ValidationFailed)
	n.do(req{method: "POST", path: api.RouteRoomJoin, token: tb, body: api.JoinRoomRequest{Code: strings.ToLower(room.Code[:4] + "-" + room.Code[4:])}}).expect(http.StatusNoContent)

	a, b, c := n.dialVnet(ta), n.dialVnet(tb), n.dialVnet(tc)
	// 不是成员的进不来
	c.send(api.LiveVnetBody{Op: api.VnetJoin, Room: room.ID, Key: "kc"})
	c.expect(api.VnetClosed, nil)

	a.send(api.LiveVnetBody{Op: api.VnetJoin, Room: room.ID, Key: "ka"})
	b.send(api.LiveVnetBody{Op: api.VnetJoin, Room: room.ID, Key: "kb"})
	bothKeys := func(m api.LiveVnetBody) bool {
		keys := map[int]string{}
		for _, p := range m.Peers {
			keys[p.Slot] = p.Key
		}
		return keys[1] == "ka" && keys[2] == "kb"
	}
	if m := a.expect(api.VnetPeers, bothKeys); m.Self != 1 {
		t.Fatalf("alice 的座位：%+v", m)
	}
	if m := b.expect(api.VnetPeers, bothKeys); m.Self != 2 {
		t.Fatalf("bob 的座位：%+v", m)
	}

	// 收件中继（主 + 备用）：报给面板后，别人在成员列表里看到；超过上限的不收
	a.send(api.LiveVnetBody{Op: api.VnetRelayOp, Room: room.ID, Relay: "n1", Homes: []string{"n1", "n2"}})
	b.expect(api.VnetPeers, func(m api.LiveVnetBody) bool {
		for _, p := range m.Peers {
			if p.Slot == 1 {
				return p.Relay == "n1" && slices.Equal(p.Homes, []string{"n1", "n2"})
			}
		}
		return false
	})
	a.send(api.LiveVnetBody{Op: api.VnetRelayOp, Room: room.ID, Relay: "n1", Homes: []string{"n1", "n2", "n3"}})

	// 打洞信息转发
	a.send(api.LiveVnetBody{Op: api.VnetSignal, Room: room.ID, To: 2, Data: "offer"})
	if m := b.expect(api.VnetSignal, nil); m.From != 1 || m.Data != "offer" {
		t.Fatalf("signal：%+v", m)
	}
	// 中继帧：第一个字节换成发送方的座位号
	if err := a.ws.Write(context.Background(), websocket.MessageBinary, []byte{2, 'h', 'i'}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-b.pkts:
		if string(p) != "\x01hi" {
			t.Fatalf("中继帧：%q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("中继帧没到")
	}

	// 聊天：房间里所有人都收到（包括自己），后进来的人看到最近的
	a.send(api.LiveVnetBody{Op: api.VnetChat, Room: room.ID, Text: "  来 MC 吗  "})
	if m := b.expect(api.VnetChat, func(m api.LiveVnetBody) bool { return m.Chat != nil }); m.Chat.Text != "来 MC 吗" || m.Chat.From != 1 || m.Chat.Nickname != "alice" {
		t.Fatalf("chat：%+v", m.Chat)
	}
	a.expect(api.VnetChat, func(m api.LiveVnetBody) bool { return m.Chat != nil })

		// 名单里能看到谁在线
	var rs api.RoomsResponse
	n.do(req{method: "GET", path: api.RouteRooms, token: tb}).expect(http.StatusOK).decode(&rs)
	if len(rs.Rooms) != 1 || len(rs.Rooms[0].Members) != 2 || !rs.Rooms[0].Members[1].Online {
		t.Fatalf("房间列表：%+v", rs)
	}

	// 只有房主能踢人；踢掉后 bob 收到 closed，alice 的名单里没了 bob
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomKick, "{id}", room.ID, 1), token: tb, body: api.KickRoomRequest{UserID: rs.Rooms[0].Members[0].UserID}}).apiErr(apierr.Forbidden)
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomKick, "{id}", room.ID, 1), token: ta, body: api.KickRoomRequest{UserID: rs.Rooms[0].Members[1].UserID}}).expect(http.StatusNoContent)
	b.expect(api.VnetClosed, nil)
	a.expect(api.VnetPeers, func(m api.LiveVnetBody) bool { return len(m.Peers) == 1 })

	// 游客：房主没打开时进不来；打开后用邀请码 + 昵称进来，走游客的实时通道进虚拟网
	n.do(req{method: "POST", path: api.RouteRoomGuest, body: api.GuestJoinRequest{Code: room.Code, Nickname: "路人"}}).apiErr(apierr.Forbidden)
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: tb, body: api.RoomSettings{AllowGuests: true}}).apiErr(apierr.Forbidden)
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: api.RoomSettings{AllowGuests: true}}).expect(http.StatusNoContent)
	// 语音设置：禁言列表去重、去掉无效座位；旧版本客户端改设置（不带 noVoice / muted）时保持原样
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta,
		body: map[string]any{"allowGuests": true, "noVoice": true, "muted": []int{3, 3, 999}}}).expect(http.StatusNoContent)
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: map[string]any{"allowGuests": true}}).expect(http.StatusNoContent)
	var vr api.RoomsResponse
	n.do(req{method: "GET", path: api.RouteRooms, token: ta}).expect(http.StatusOK).decode(&vr)
	if len(vr.Rooms) != 1 || !vr.Rooms[0].NoVoice || len(vr.Rooms[0].Muted) != 1 || vr.Rooms[0].Muted[0] != 3 {
		t.Fatalf("语音设置：%+v", vr.Rooms)
	}
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: api.RoomSettings{AllowGuests: true, Muted: []int{}}}).expect(http.StatusNoContent)
	// 房主改房间名：在房间里的成员收到新名字；空名字不行
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: map[string]any{"allowGuests": true, "name": "  "}}).apiErr(apierr.ValidationFailed)
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: map[string]any{"allowGuests": true, "name": "新名字"}}).expect(http.StatusNoContent)
	a.expect(api.VnetPeers, func(m api.LiveVnetBody) bool { return m.RoomName == "新名字" })
	// 禁止直连：在线成员收到设置；不带 noDirect 改设置时保持原样
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: map[string]any{"allowGuests": true, "noDirect": true}}).expect(http.StatusNoContent)
	a.expect(api.VnetPeers, func(m api.LiveVnetBody) bool { return m.Settings != nil && m.Settings.NoDirect })
	n.do(req{method: "PATCH", path: strings.Replace(api.RouteRoom, "{id}", room.ID, 1), token: ta, body: map[string]any{"allowGuests": true}}).expect(http.StatusNoContent)
	a.expect(api.VnetPeers, func(m api.LiveVnetBody) bool { return m.Settings != nil && m.Settings.NoDirect })
	// 房主换邀请码：旧码作废，在线成员收到新码；别人不能换
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomCode, "{id}", room.ID, 1), token: tb}).apiErr(apierr.Forbidden)
	var rc api.Room
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomCode, "{id}", room.ID, 1), token: ta}).expect(http.StatusOK).decode(&rc)
	if rc.Code == "" || rc.Code == room.Code {
		t.Fatalf("新邀请码：%q（旧 %q）", rc.Code, room.Code)
	}
	a.expect(api.VnetPeers, func(m api.LiveVnetBody) bool { return m.RoomCode == rc.Code })
	n.do(req{method: "POST", path: api.RouteRoomGuest, body: api.GuestJoinRequest{Code: room.Code, Nickname: "路人2"}}).apiErr(apierr.ValidationFailed)
	room.Code = rc.Code
	var gj api.GuestJoinResponse
	n.do(req{method: "POST", path: api.RouteRoomGuest, body: api.GuestJoinRequest{Code: room.Code, Nickname: "路人"}}).expect(http.StatusOK).decode(&gj)
	if gj.Token == "" || !gj.Room.AllowGuests {
		t.Fatalf("游客：%+v", gj)
	}
	g := n.dialVnetAt(api.RouteRoomGuestLive, gj.Token)
	g.send(api.LiveVnetBody{Op: api.VnetJoin, Room: room.ID, Key: "kg"})
	var gslot int
	m := g.expect(api.VnetPeers, func(m api.LiveVnetBody) bool { return m.Self > 0 })
	gslot = m.Self
	a.expect(api.VnetPeers, func(m api.LiveVnetBody) bool {
		for _, p := range m.Peers {
			if p.Slot == gslot && p.Key == "kg" && strings.Contains(p.Nickname, "游客") {
				return true
			}
		}
		return false
	})
	// 房主踢游客：游客收到 closed，令牌作废
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomKick, "{id}", room.ID, 1), token: ta, body: api.KickRoomRequest{UserID: api.GuestPrefix + "x"}}).apiErr(apierr.NotFound)
	var gr api.Room
	n.do(req{method: "GET", path: api.RouteRoomGuest, token: gj.Token}).expect(http.StatusOK).decode(&gr)
	var guestID string
	for _, mm := range gr.Members {
		if mm.Slot == gslot {
			guestID = mm.UserID
		}
	}
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomKick, "{id}", room.ID, 1), token: ta, body: api.KickRoomRequest{UserID: guestID}}).expect(http.StatusNoContent)
	g.expect(api.VnetClosed, nil)
	n.do(req{method: "GET", path: api.RouteRoomGuest, token: gj.Token}).apiErr(apierr.Unauthorized)

	// 房主离开等于解散
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomLeave, "{id}", room.ID, 1), token: ta}).expect(http.StatusNoContent)
	a.expect(api.VnetClosed, nil)
	n.do(req{method: "GET", path: api.RouteRooms, token: ta}).expect(http.StatusOK).decode(&rs)
	if len(rs.Rooms) != 0 {
		t.Fatalf("解散后还有房间：%+v", rs)
	}
}
