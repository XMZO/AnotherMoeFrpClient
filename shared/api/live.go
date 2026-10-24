package api

// 客户端实时通道（docs/client-live.md）：登录后的客户端和面板保持一条 WebSocket，
// 面板有变化（配置、官方节点、限额、密钥、版本）时立刻推给客户端，管理员也能通过它远程控制客户端；
// 客户端反过来上报自己的隧道状态，后台"客户端"页据此显示在线设备。
//
// 帧都是 JSON 文本：{"type": "...", ...}。通道断了客户端自动重连，期间照常靠轮询兜底，不影响使用。

// RouteClientLive 是实时通道（WebSocket），和其他接口一样用 Bearer 会话令牌认证。
const RouteClientLive = Prefix + "/client/live"

// 面板 → 客户端的事件类型。
const (
	LiveWelcome  = "welcome"  // 连上后第一条：面板时间
	LiveSync     = "sync"     // 云端配置、订阅变了：马上拉一次
	LiveOfficial = "official" // 官方节点列表、限额、权限、访问地址变了：马上拉一次
	LiveTickets  = "tickets"  // 官方节点的登录票据作废了（轮换密钥、重置凭证）：丢掉预取的，正在用官方节点的马上重连
	LiveConfig   = "config"   // 客户端配置变了（版本、最低版本、公告）：Config 是新的内容
	LiveNotice   = "notice"   // 管理员推送的通知：Notice
	LiveTunnel   = "tunnel"   // 管理员远程控制隧道：Tunnel
	LiveEnded    = "ended"    // 会话结束了（被踢下线、改密码、账号停用）：Ended 是原因，客户端按被动下线处理
)

// 客户端 → 面板的消息类型。
const (
	LiveHello  = "hello"  // 连上后第一条：客户端信息
	LiveStatus = "status" // 隧道状态变了就发一次
)

// LiveMessage 是通道里的一帧。按 Type 只填对应的字段。
type LiveMessage struct {
	Type string `json:"type"`

	ServerTime int64                 `json:"serverTime,omitempty"` // welcome
	Node       string                `json:"node,omitempty"`       // tickets：只作废这个官方节点的（空表示全部）
	Config     *ClientConfigResponse `json:"config,omitempty"`     // config
	Notice     *LiveNoticeBody       `json:"notice,omitempty"`     // notice
	Tunnel     *LiveTunnelCommand    `json:"tunnel,omitempty"`     // tunnel
	Ended      *LiveEndedBody        `json:"ended,omitempty"`      // ended

	Hello  *LiveHelloBody  `json:"hello,omitempty"`  // hello
	Status *LiveTunnelInfo `json:"status,omitempty"` // status
}

// 通知的级别和显示方式。
const (
	NoticeInfo    = "info"
	NoticeSuccess = "success"
	NoticeWarning = "warning"
	NoticeError   = "error"

	NoticeToast  = "toast"  // 右上角通知
	NoticeDialog = "dialog" // 弹窗，要点"知道了"才关
)

// LiveNoticeBody 是一条推送通知。
type LiveNoticeBody struct {
	ID       string `json:"id"`
	Level    string `json:"level"`   // info / success / warning / error
	Display  string `json:"display"` // toast / dialog
	Title    string `json:"title"`
	Content  string `json:"content,omitempty"`
	LinkText string `json:"linkText,omitempty"` // 按钮文字，和 LinkURL 一起用
	LinkURL  string `json:"linkUrl,omitempty"`  // 只允许 http(s)
	Sticky   bool   `json:"sticky,omitempty"`   // toast 不自动消失
	From     string `json:"from,omitempty"`     // 发送的管理员昵称
}

// 远程控制隧道的动作。
const (
	TunnelStop    = "stop"
	TunnelRestart = "restart"
)

// LiveTunnelCommand 是管理员对客户端隧道的操作。
type LiveTunnelCommand struct {
	Action string `json:"action"` // stop / restart
	Reason string `json:"reason,omitempty"`
	From   string `json:"from,omitempty"`
}

// LiveEndedBody 是会话结束的原因。
type LiveEndedBody struct {
	Code    string `json:"code"` // session_revoked / session_replaced / account_disabled / ...
	Message string `json:"message"`
}

// LiveHelloBody 是客户端的基本信息和设备信息（管理后台"用户 → 登录设备"里显示，离线后也能看到）。
type LiveHelloBody struct {
	Version  string `json:"version"`
	OS       string `json:"os"` // 例如 Windows 11 专业版 24H2
	Hostname string `json:"hostname"`
	OSBuild  string `json:"osBuild,omitempty"` // 例如 10.0.26100.2894
	Arch     string `json:"arch,omitempty"`    // amd64 / arm64
	CPU      string `json:"cpu,omitempty"`     // 处理器型号
	Cores    int    `json:"cores,omitempty"`   // 逻辑处理器数
	MemoryMB int    `json:"memoryMb,omitempty"`
	Model    string `json:"model,omitempty"` // 电脑的品牌和型号，例如 LENOVO 21D0
}

// LiveTunnelInfo 是客户端上报的隧道状态（不含地址、令牌等配置细节）。
type LiveTunnelInfo struct {
	State       string `json:"state"`                 // idle / connecting / running / reconnecting / stopping / error
	ProfileKind string `json:"profileKind,omitempty"` // local / cloud / subscription
	ProfileName string `json:"profileName,omitempty"`
	NodeName    string `json:"nodeName,omitempty"`
	OfficialID  string `json:"officialId,omitempty"` // 用的是官方节点时的节点 ID
	Proxies     int    `json:"proxies"`              // 启用的代理数
	Running     int    `json:"running"`              // 正在运行的代理数
	Error       string `json:"error,omitempty"`
	Since       int64  `json:"since,omitempty"` // 进入当前状态的时间（Unix 秒）
}
