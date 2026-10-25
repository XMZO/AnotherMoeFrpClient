package api

import "fmt"

// 联机房间（虚拟局域网）：房间里的设备组成一个虚拟网段，游戏里像在同一个局域网一样。
//
// 数据面：每台设备一块虚拟网卡（Wintun）+ WireGuard 加密；设备之间优先用 ICE 打洞直连，
// 打不通时经面板的实时通道中继（二进制帧）。控制面：房间、成员、虚拟 IP 存在面板上，
// 进房间、换公钥、交换打洞信息都走实时通道的 vnet 消息。
const (
	RouteRooms     = Prefix + "/rooms"            // GET 我的房间；POST 新建 {name}
	RouteRoomJoin  = Prefix + "/rooms/join"       // POST {code}
	RouteRoomLeave = Prefix + "/rooms/{id}/leave" // POST：离开（房主离开等于解散）
	RouteRoomKick  = Prefix + "/rooms/{id}/kick"  // POST {userId}：房主踢人

	// LiveVnet 是实时通道里联机房间的消息（两个方向都用），内容在 Vnet 字段。
	LiveVnet = "vnet"

	// VnetPrefix 是虚拟网段（/24）：成员的虚拟 IP 是 VnetPrefix + 座位号（1–250）。
	// 避开常见的网段：198.18/15（代理软件的 fake-ip）、100.64/10（Tailscale）、10.0.x、10.147.x（ZeroTier）等。
	VnetPrefix = "10.241.0."
	VnetMask   = "255.255.255.0"
	// VnetMaxMembers 是一个房间最多的人数，VnetMaxRooms 是一个人最多加入的房间数。
	VnetMaxMembers = 32
	VnetMaxRooms   = 10
)

// VnetIP 返回座位号对应的虚拟 IP。
func VnetIP(slot int) string { return fmt.Sprintf("%s%d", VnetPrefix, slot) }

// Room 是一个联机房间。
type Room struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Code      string       `json:"code"` // 邀请码，别人输入它加入
	OwnerID   string       `json:"ownerId"`
	OwnerName string       `json:"ownerName"`
	CreatedAt int64        `json:"createdAt"`
	Members   []RoomMember `json:"members"`
}

// RoomMember 是房间成员。
type RoomMember struct {
	UserID   string `json:"userId"`
	Nickname string `json:"nickname"`
	Slot     int    `json:"slot"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"` // 正在房间里（客户端连着虚拟网）
}

// RoomsResponse：GET /api/v1/rooms。
type RoomsResponse struct {
	Rooms []Room `json:"rooms"`
}

// CreateRoomRequest：POST /api/v1/rooms。
type CreateRoomRequest struct {
	Name string `json:"name"`
}

// JoinRoomRequest：POST /api/v1/rooms/join。
type JoinRoomRequest struct {
	Code string `json:"code"`
}

// KickRoomRequest：POST /api/v1/rooms/{id}/kick。
type KickRoomRequest struct {
	UserID string `json:"userId"`
}

// vnet 消息的操作。
const (
	VnetJoin   = "join"   // 客户端 → 面板：进入房间的虚拟网，Key 是这次的 WireGuard 公钥
	VnetLeave  = "leave"  // 客户端 → 面板：离开
	VnetSignal = "signal" // 双向：打洞信息，客户端发时 To 是对方座位号，面板转发时 From 是来源座位号
	VnetPeers  = "peers"  // 面板 → 客户端：房间成员（进入后、成员变化时）
	VnetClosed = "closed" // 面板 → 客户端：房间没了或被踢出，Reason 是原因
)

// LiveVnetBody 是 vnet 消息。
type LiveVnetBody struct {
	Op     string     `json:"op"`
	Room   string     `json:"room"`
	Key    string     `json:"key,omitempty"`
	To     int        `json:"to,omitempty"`
	From   int        `json:"from,omitempty"`
	Data   string     `json:"data,omitempty"`
	Self   int        `json:"self,omitempty"` // peers：自己的座位号
	Peers  []VnetPeer `json:"peers,omitempty"`
	Reason string     `json:"reason,omitempty"`
}

// VnetPeer 是房间里的一个成员（peers 消息里）。Key 为空表示不在线。
type VnetPeer struct {
	Slot     int    `json:"slot"`
	Nickname string `json:"nickname"`
	IP       string `json:"ip"`
	Key      string `json:"key,omitempty"`
}

// 中继帧（实时通道的二进制消息）：第一个字节是座位号，后面是 WireGuard 数据包。
// 客户端发出时是对方的座位号，面板转发时换成发送方的座位号。
