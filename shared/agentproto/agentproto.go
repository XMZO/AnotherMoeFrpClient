// Package agentproto 是面板（moefrp-server）和节点 agent（moefrp-agent）之间的协议，见 docs/node-agent.md。
//
// agent 主动用 WebSocket 连接面板，双方互发 JSON 消息。请求带 ID，响应用同一个 ID 回复；
// 事件（report）不需要响应。
package agentproto

import (
	"encoding/json"
	"moefrp/shared/frpsset"

	"moefrp/shared/quota"
)

const (
	// RouteWS 是 agent 的长连接，请求头 Authorization: Bearer <节点令牌>。
	RouteWS = "/api/v1/agent/ws"
	// RouteEnroll 用一次性安装码换取长期节点令牌。
	RouteEnroll = "/api/v1/agent/enroll"
	// RouteInstallScript 是安装脚本，内容里写好了面板地址。
	RouteInstallScript = "/agent/install.sh"
	// RouteDownload 下载 agent 程序，后接 linux-amd64 / linux-arm64；再加 .sha256 得到校验和。
	RouteDownload = "/agent/download/"

	// Subprotocol 是 WebSocket 子协议名，协议不兼容时改版本号。
	Subprotocol = "moefrp-agent.v1"

	// ReportInterval 是 agent 上报状态的间隔（秒），面板上的流量和资源按这个频率变化。
	ReportInterval = 3
	// PingInterval / PingTimeout：agent 每隔 PingInterval 秒检查一次连接，PingTimeout 秒内没有回应才断开重连。
	// 跨境线路丢包时 TCP 重传一卡就是十几秒，超时太短会把还活着的连接当成死的，断开重连反而更容易失败。
	PingInterval = 15
	PingTimeout  = 45
	// OfflineAfter：面板超过这么多秒没收到任何消息就断开连接。
	OfflineAfter = 90
	// OfflineGrace：连接断开后这么多秒内重连上，管理后台照样显示在线（标为"重连中"），不显示离线。
	OfflineGrace = 60
)

// 消息类型。
const (
	TypeRequest  = "req"
	TypeResponse = "res"
	TypeEvent    = "evt"
)

// 方法名。
const (
	MethodHello       = "hello"      // agent → 面板，连接后第一条请求，响应 HelloAck
	MethodReport      = "report"     // agent → 面板，事件
	MethodFrpsApply   = "frps.apply" // 面板 → agent
	MethodFrpsRestart = "frps.restart"
	MethodFrpsProxies = "frps.proxies"
	MethodLogs        = "logs"
	MethodExec        = "exec"
	MethodUpgrade     = "upgrade"
	MethodUninstall   = "uninstall"
	MethodAccessSync  = "access.sync" // 面板 → agent：官方节点的授权用户表
	MethodStoreLease  = "store.lease" // 面板 → agent：本地加密存储的密钥和租约（FeatureSettings）
)

// Message 是线上传输的一条消息。
type Message struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	Method string          `json:"method,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error 是响应里的错误。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// 错误码。
const (
	ErrUnknownMethod = "unknown_method"
	ErrBadRequest    = "bad_request"
	ErrDisabled      = "disabled" // 例如安装时加了 --no-exec
	ErrFailed        = "failed"
	ErrBusy          = "busy"
	// ErrMachineMismatch：节点令牌是在另一台机器上注册的（hello 里的机器指纹和记下的不同），
	// 要在面板上重置令牌后重新安装。
	ErrMachineMismatch = "machine_mismatch"
)

// ---- 注册 ----

// EnrollRequest：POST RouteEnroll。
type EnrollRequest struct {
	Code     string `json:"code"`
	Hostname string `json:"hostname"`
}

// EnrollResponse 返回长期令牌，只返回这一次。
type EnrollResponse struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
	Token  string `json:"token"`
}

// ---- hello / report ----

// Hello 是 agent 连接后的第一条请求。
type Hello struct {
	AgentVersion string `json:"agentVersion"`
	FrpVersion   string `json:"frpVersion"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Distro       string `json:"distro"`
	Kernel       string `json:"kernel"`
	Hostname     string `json:"hostname"`
	BootTime     int64  `json:"bootTime"`
	ExecEnabled  bool   `json:"execEnabled"`
	AppliedRev   int64  `json:"appliedRev"` // 当前生效的 frps 配置版本，0 表示还没收到过面板的配置
	// Features 是这个 agent 支持的可选功能，面板据此决定能不能开官方节点等（旧版本没有这个字段）。
	Features []string `json:"features,omitempty"`
	// MachineID 是机器指纹（/etc/machine-id 加盐哈希，不是原值）：面板据此派生节点的存储密钥，
	// 也用它发现 agent 的令牌被拷到了别的机器上（FeatureSettings 起）。
	MachineID string `json:"machineId,omitempty"`
	// Upgrade 是上一次升级的结果（升级后第一次连接时带上，之后为空）。
	Upgrade *UpgradeResult `json:"upgrade,omitempty"`
}

// HelloAck 是面板对 hello 的响应。
type HelloAck struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
}

// Report 是周期上报的状态。
type Report struct {
	Uptime    int64      `json:"uptime"` // 系统运行秒数
	CPU       float64    `json:"cpu"`    // 百分比，0–100
	Load      [3]float64 `json:"load"`
	MemTotal  uint64     `json:"memTotal"`
	MemUsed   uint64     `json:"memUsed"`
	DiskTotal uint64     `json:"diskTotal"`
	DiskUsed  uint64     `json:"diskUsed"`
	NetRx     uint64     `json:"netRx"` // 累计字节数，面板自己算速率
	NetTx     uint64     `json:"netTx"`
	Frps      FrpsStatus `json:"frps"`
}

// frps 状态。
const (
	FrpsRunning = "running"
	FrpsStopped = "stopped" // 还没有配置
	FrpsError   = "error"
)

// FrpsStatus 是内嵌 frps 的运行状态。
type FrpsStatus struct {
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	AppliedRev int64  `json:"appliedRev"`
	BindPort   int    `json:"bindPort,omitempty"`
	Clients    int64  `json:"clients"`
	Proxies    int64  `json:"proxies"`
	Conns      int64  `json:"conns"`
	TrafficIn  int64  `json:"trafficIn"` // 今天的累计流量（frp 按天统计）
	TrafficOut int64  `json:"trafficOut"`
	StartedAt  int64  `json:"startedAt,omitempty"`
}

// ---- frps ----

// ApplyRequest 下发 frps 配置（frps 原生 TOML）。
// 支持 FeatureSettings 的 agent 收 Settings（结构化配置，agent 在内存里拼出 frps 配置，加密保存）；
// 旧 agent 收 Config（frps 原生 TOML）。
type ApplyRequest struct {
	Rev      int64             `json:"rev"`
	Config   string            `json:"config,omitempty"`
	Settings *frpsset.Settings `json:"settings,omitempty"`
}

// ApplyResult 是应用结果。Applied 为 false 时 frps 仍在用旧配置运行（RolledBack）或没有运行。
type ApplyResult struct {
	Rev        int64  `json:"rev"`
	Applied    bool   `json:"applied"`
	RolledBack bool   `json:"rolledBack,omitempty"`
	Error      string `json:"error,omitempty"`
	Warning    string `json:"warning,omitempty"`
}

// ProxyInfo 是一个在线代理。
type ProxyInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	User       string `json:"user,omitempty"`
	ClientID   string `json:"clientId,omitempty"`
	Online     bool   `json:"online"`
	Conns      int64  `json:"conns"`
	TrafficIn  int64  `json:"trafficIn"`
	TrafficOut int64  `json:"trafficOut"`
	RemotePort int    `json:"remotePort,omitempty"`
	LastStart  string `json:"lastStart,omitempty"`
}

// ProxiesResult：frps.proxies 的响应。
type ProxiesResult struct {
	Proxies []ProxyInfo `json:"proxies"`
}

// ---- 日志 ----

// LogsRequest 取最近 Lines 行日志。
type LogsRequest struct {
	Lines int `json:"lines"`
}

// LogsResult：logs 的响应。
type LogsResult struct {
	Lines []string `json:"lines"`
}

// ---- 执行命令 ----

// 执行命令的限制。
const (
	ExecDefaultTimeout = 60
	ExecMaxTimeout     = 3600
	ExecMaxOutput      = 1 << 20
)

// ExecRequest 以 root 执行一条 shell 命令。
type ExecRequest struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeoutSec"`
}

// ExecResult：ExitCode 为 -1 表示没能正常结束（超时、启动失败），原因在 Error。
type ExecResult struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// ---- 升级 / 卸载 ----

// UpgradeRequest：agent 从 URL 下载新版本，校验 SHA256 后替换自己并重启。
// 响应在下载、校验、预检都通过、即将重启时返回；最终结果在重启后的 Hello.Upgrade 里。
type UpgradeRequest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

// UpgradeResult 是一次升级的最终结果。
type UpgradeResult struct {
	From       string `json:"from"`
	To         string `json:"to"`
	OK         bool   `json:"ok"`
	RolledBack bool   `json:"rolledBack,omitempty"`
	Error      string `json:"error,omitempty"`
}

// UninstallRequest：Purge 为 true 时连同配置一起删除（面板发起的卸载总是 true）。
type UninstallRequest struct {
	Purge bool `json:"purge"`
}

// ---- 官方节点授权 ----

// FeatureAccess：agent 支持官方节点的授权（access.sync 和 frps 插件）。
const FeatureAccess = "access"

// FeatureTickets：agent 用一次性登录票据（shared/nodeticket）校验官方节点的用户。
// 官方节点要求 agent 支持它（agent 1.3.0 起）。
const FeatureTickets = "tickets"

// FeatureSettings：agent 收结构化的 frps 配置（frpsset），frps 不设 token、始终挂准入插件，
// 配置和授权表用面板下发的存储密钥加密保存（agent 1.4.0 起）。
const FeatureSettings = "settings"

// StoreLease 是节点本地加密存储的密钥和租约。
//
// 密钥由面板主密钥、节点 ID 和机器指纹派生，agent 用它加密保存 frps 配置和授权表；它自己再用本机指纹和
// agent 令牌派生的密钥封存。租约到期前节点重启、面板离线也能解开配置照常启动；过期后要等面板重新下发。
// 时间都是 Unix 秒（UTC，和时区无关）；agent 按 ServerTime 和本机时间的差换算到期时间，本机时钟不准也不影响租约长短。
type StoreLease struct {
	Key        string `json:"key"`        // base64
	LeaseUntil int64  `json:"leaseUntil"` // 面板时间
	ServerTime int64  `json:"serverTime"` // 面板发出时的时间
}

// AccessTable 是一个节点的授权表，面板在变化时整表下发。
// Enabled 为 false 时节点不是官方节点：agent 不挂插件，frps 只按 token 认证。
type AccessTable struct {
	Enabled   bool         `json:"enabled"`
	NodeID    string       `json:"nodeId,omitempty"`
	TicketKey string       `json:"ticketKey,omitempty"` // 这个节点的票据密钥（base64），由面板主密钥派生
	Users     []AccessUser `json:"users"`
	// RevokedSessions 是已经撤销、但还没到原本过期时间的客户端会话：
	// 它们签发过的票据不能再登录，用它们登录的 frpc 也不再接受新代理和新连接。
	RevokedSessions []string `json:"revokedSessions,omitempty"`
}

// AccessUser 是一个可以使用这个节点的用户。
type AccessUser struct {
	UserID   string      `json:"userId"`
	Nickname string      `json:"nickname"` // 必须和 frpc 的 user 一致
	Gen      int64       `json:"gen"`      // 节点凭证代数，和票据里的不一致就拒绝（管理员重置过）
	Quota    quota.Quota `json:"quota"`
}

// AccessResult：access.sync 的响应。
type AccessResult struct {
	Users     int  `json:"users"`
	Restarted bool `json:"restarted"` // 开关官方节点时 frps 重启了
}
