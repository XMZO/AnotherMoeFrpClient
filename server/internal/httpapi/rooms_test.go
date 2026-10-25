package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
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

func (n *nodeEnv) dialVnet(token string) *vnetConn {
	n.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set(api.HeaderAuthorization, api.BearerPrefix+token)
	h.Set(api.HeaderClientVersion, "3.1.0")
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(n.http.URL, "http")+api.RouteClientLive, &websocket.DialOptions{HTTPHeader: h})
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

	// 房主离开等于解散
	n.do(req{method: "POST", path: strings.Replace(api.RouteRoomLeave, "{id}", room.ID, 1), token: ta}).expect(http.StatusNoContent)
	a.expect(api.VnetClosed, nil)
	n.do(req{method: "GET", path: api.RouteRooms, token: ta}).expect(http.StatusOK).decode(&rs)
	if len(rs.Rooms) != 0 {
		t.Fatalf("解散后还有房间：%+v", rs)
	}
}
