package api

import "fmt"

// 联机房间（虚拟局域网）：房间里的设备组成一个虚拟网段，游戏里像在同一个局域网一样。
//
// 数据面：每台设备一块虚拟网卡（Wintun）+ WireGuard 加密；设备之间优先用 ICE 打洞直连，
// 打不通时经面板的实时通道中继（二进制帧）。控制面：房间、成员、虚拟 IP 存在面板上，
// 进房间、换公钥、交换打洞信息都走实时通道的 vnet 消息。
const (
	RouteRooms      = Prefix + "/rooms"            // GET 我的房间；POST 新建 {name}
	RouteRoomJoin   = Prefix + "/rooms/join"       // POST {code}
	RouteRoomLeave  = Prefix + "/rooms/{id}/leave" // POST：离开（房主离开等于解散）
	RouteRoomKick   = Prefix + "/rooms/{id}/kick"  // POST {userId}：房主踢人
	RouteRoomCode   = Prefix + "/rooms/{id}/code"  // POST：房主换新邀请码（旧码作废，已经在房间里的人不受影响），返回 Room
	RouteRoom       = Prefix + "/rooms/{id}"       // PATCH RoomSettings（可以带 name 改房间名）：房主改设置
	RouteRoomRelays = Prefix + "/rooms/relays"     // GET：能用的中继节点（房主选默认中继用，不含票据）

	// 游客（没登录，用邀请码 + 昵称进房间，房主允许时）：拿到只能用于这个房间的游客令牌（Bearer）。
	RouteRoomGuest      = Prefix + "/rooms/guest"       // POST GuestJoinRequest（不需要登录）；GET 房间信息（游客令牌）
	RouteRoomGuestLive  = Prefix + "/rooms/guest/live"  // 游客的实时通道（WebSocket，游客令牌），只收发 vnet 消息
	RouteRoomGuestLeave = Prefix + "/rooms/guest/leave" // POST：游客离开（令牌作废）

	// GuestPrefix 是游客在房间成员里的 UserID 前缀。
	GuestPrefix = "guest:"

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

// VnetIP6Prefix 是虚拟网的 IPv6 网段（/64，ULA）：成员的 IPv6 地址是 VnetIP6Prefix + 座位号（十六进制）。
// 客户端自己按座位号算，面板不用管；有的游戏（新版 MC 开了 IPv6 优先时）用 IPv6 发现和连接局域网房间。
const VnetIP6Prefix = "fd4d:6f65:4672::"

// VnetIP6 返回座位号对应的虚拟 IPv6 地址。
func VnetIP6(slot int) string { return fmt.Sprintf("%s%x", VnetIP6Prefix, slot) }

// Room 是一个联机房间。
type Room struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Code      string       `json:"code"` // 邀请码，别人输入它加入
	OwnerID   string       `json:"ownerId"`
	OwnerName string       `json:"ownerName"`
	CreatedAt int64        `json:"createdAt"`
	Members   []RoomMember `json:"members"`
	RoomSettings
}

// RoomSettings 是房主能改的设置（PATCH /api/v1/rooms/{id}）。
type RoomSettings struct {
	AllowGuests bool   `json:"allowGuests"` // 允许没登录的人用邀请码以游客身份进来
	Relay       string `json:"relay"`       // 默认中继：节点 ID，空表示自动（每人选最近的）
	RelayLocked bool   `json:"relayLocked"` // 锁定：所有人只能用 Relay（为空时都用自动）
	NoVoice     bool   `json:"noVoice"`     // 关闭语音（默认开着）
	Muted       []int  `json:"muted"`       // 被禁言的座位号（不能在语音里说话）
}

// RoomPatch：PATCH /api/v1/rooms/{id}。Name 为空表示不改名。
type RoomPatch struct {
	RoomSettings
	Name string `json:"name,omitempty"`
}

// GuestJoinRequest：POST /api/v1/rooms/guest。
type GuestJoinRequest struct {
	Code     string `json:"code"`
	Nickname string `json:"nickname"`
}

// GuestJoinResponse 是游客令牌和房间。
type GuestJoinResponse struct {
	Token string `json:"token"`
	Room  Room   `json:"room"`
}

// VnetRelay 是一个能中继的节点：成员连 ws://Addr:Port/vnet，带面板签的票据（LiveVnetBody.Tickets）。
type VnetRelay struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region,omitempty"`
	Addr   string `json:"addr"`
	Port   int    `json:"port"`
}

// RouteVnetRelay 是节点 agent 上中继的路径（WebSocket，Authorization: Bearer <票据>）。
const RouteVnetRelay = "/vnet"

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
	VnetJoin    = "join"   // 客户端 → 面板：进入房间的虚拟网，Key 是这次的 WireGuard 公钥
	VnetLeave   = "leave"  // 客户端 → 面板：离开
	VnetSignal  = "signal" // 双向：打洞信息，客户端发时 To 是对方座位号，面板转发时 From 是来源座位号
	VnetPeers   = "peers"  // 面板 → 客户端：房间成员（进入后、成员变化时）
	VnetClosed  = "closed" // 面板 → 客户端：房间没了或被踢出，Reason 是原因
	VnetRelayOp = "relay"  // 客户端 → 面板：我的收件中继换成 Relay（节点 ID，空表示面板）
	VnetChat    = "chat"   // 双向：房间聊天。客户端发 Text；面板转发给房间里所有人（Chat），进房间时带最近的（History）
	// 语音（WebRTC，走虚拟网）的协商借用 signal：Data 是客户端自己的 JSON（{"t":"voice",...}），面板原样转发。
)

// VnetChatMax 是一条聊天消息的最大字数，VnetChatKeep 是面板给每个房间在内存里留的条数。
const (
	VnetChatMax  = 500
	VnetChatKeep = 50
)

// VnetChatMsg 是一条房间聊天消息。
type VnetChatMsg struct {
	From     int    `json:"from"` // 座位号
	Nickname string `json:"nickname"`
	Text     string `json:"text"`
	At       int64  `json:"at"` // Unix 毫秒
}

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

	// peers 里还带：房间设置、能用的中继节点、自己连各中继的票据（节点 ID → 票据）
	Settings *RoomSettings     `json:"settings,omitempty"`
	Relays   []VnetRelay       `json:"relays,omitempty"`
	Tickets  map[string]string `json:"tickets,omitempty"`
	Relay    string            `json:"relay,omitempty"`    // relay：我的收件中继
	RoomName string            `json:"roomName,omitempty"` // peers：房间名（房主改名后成员跟着变）
	RoomCode string            `json:"roomCode,omitempty"` // peers：邀请码（房主换码后成员跟着变）

	Text    string        `json:"text,omitempty"`    // chat（客户端发）
	Chat    *VnetChatMsg  `json:"chat,omitempty"`    // chat（面板转发）
	History []VnetChatMsg `json:"history,omitempty"` // chat：进房间时最近的消息
}

// VnetPeer 是房间里的一个成员（peers 消息里）。Key 为空表示不在线。
type VnetPeer struct {
	Slot     int    `json:"slot"`
	Nickname string `json:"nickname"`
	IP       string `json:"ip"`
	Key      string `json:"key,omitempty"`
	Relay    string `json:"relay,omitempty"` // 他的收件中继（节点 ID），空表示面板；发给他的中继包送到这里
}

// 中继帧（实时通道的二进制消息）：第一个字节是座位号，后面是 WireGuard 数据包。
// 客户端发出时是对方的座位号，面板转发时换成发送方的座位号。
