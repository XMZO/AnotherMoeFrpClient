package api

import (
	"time"

	"moefrp/shared/model"
	"moefrp/shared/quota"
)

// UserInfo 是返回给客户端的用户信息。
type UserInfo struct {
	ID        string    `json:"id"`
	Nickname  string    `json:"nickname"`
	Role      string    `json:"role"` // user / vip / admin
	CreatedAt time.Time `json:"createdAt"`
}

// ---- 认证 ----

// RegisterRequest：POST /api/v1/auth/register。
type RegisterRequest struct {
	Nickname   string `json:"nickname"`
	Password   string `json:"password"`
	InviteCode string `json:"inviteCode"`
}

// RegisterResponse 是注册成功（201）的响应。注册不会自动登录。
type RegisterResponse struct {
	User UserInfo `json:"user"`
}

// LoginRequest：POST /api/v1/auth/login。installId 取自 X-Install-Id 请求头。
type LoginRequest struct {
	Nickname   string `json:"nickname"`
	Password   string `json:"password"`
	DeviceName string `json:"deviceName,omitempty"`
}

// LoginResponse 是登录成功的响应。Token 只在这里出现一次，服务端只存它的哈希。
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"` // 滑动过期：每次使用都会往后推
	User      UserInfo  `json:"user"`
}

// ChangePasswordRequest：POST /api/v1/auth/change-password，成功返回 204。
type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// ResetPasswordRequest：POST /api/v1/auth/reset-password，成功返回 204。
type ResetPasswordRequest struct {
	Nickname    string `json:"nickname"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

// MeResponse：GET /api/v1/me。
type MeResponse struct {
	User UserInfo `json:"user"`
}

// ---- 云端状态与配置 ----

// StateResponse：GET /api/v1/state。ETag 为 StateETag(StateVersion)。
type StateResponse struct {
	StateVersion  int64                 `json:"stateVersion"`
	User          UserInfo              `json:"user"`
	Profiles      []model.Profile       `json:"profiles"`
	Subscriptions []SubscriptionSummary `json:"subscriptions"`
}

// SubscriptionSummary 是 state 里的订阅摘要，不含节点 token 和 secretKey。
// 运行所需的完整数据通过 GET /api/v1/subscriptions/{id}/runtime 获取；
// ShareRevision 变化时客户端应重新拉取 runtime。
type SubscriptionSummary struct {
	ID            string                   `json:"id"`
	ShareID       string                   `json:"shareId"`
	ShareName     string                   `json:"shareName"`
	Kind          string                   `json:"kind"` // template / full / p2p
	OwnerNickname string                   `json:"ownerNickname"`
	Revision      int64                    `json:"revision"`
	ShareRevision int64                    `json:"shareRevision"`
	Revoked       bool                     `json:"revoked"`
	Expired       bool                     `json:"expired"`
	ExpiresAt     *time.Time               `json:"expiresAt,omitempty"`
	Nodes         []PreviewNode            `json:"nodes"`   // 可选的节点（不含地址和 token）
	Targets       []PreviewTarget          `json:"targets"` // p2p 的目标（不含 secretKey）
	Params        model.SubscriptionParams `json:"params"`
	CreatedAt     time.Time                `json:"createdAt"`
	UpdatedAt     time.Time                `json:"updatedAt"`
}

// CreateProfileRequest：POST /api/v1/profiles，成功返回 201 和 model.Profile。
//
// ID 可选：客户端离线新建的配置可以带上自己生成的 UUIDv7，省得之后再改 ID；
// 不带时由服务端生成。节点、代理、访客缺 ID 时服务端会补上。
type CreateProfileRequest struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Nodes    []model.Node    `json:"nodes"`
	Proxies  []model.Proxy   `json:"proxies"`
	Visitors []model.Visitor `json:"visitors"`
}

// UpdateProfileRequest：PUT /api/v1/profiles/{id}，成功返回 model.Profile。
//
// BaseRevision 必填，是客户端编辑时基于的版本；与云端不一致时返回 409 revision_conflict，
// details.current 是云端当前的完整配置。
type UpdateProfileRequest struct {
	BaseRevision int64           `json:"baseRevision"`
	Name         string          `json:"name"`
	Nodes        []model.Node    `json:"nodes"`
	Proxies      []model.Proxy   `json:"proxies"`
	Visitors     []model.Visitor `json:"visitors"`
}

// Data 取出配置内容。
func (r CreateProfileRequest) Data() model.ProfileData {
	return model.ProfileData{Nodes: r.Nodes, Proxies: r.Proxies, Visitors: r.Visitors}
}

// Data 取出配置内容。
func (r UpdateProfileRequest) Data() model.ProfileData {
	return model.ProfileData{Nodes: r.Nodes, Proxies: r.Proxies, Visitors: r.Visitors}
}

// ---- 公开接口 ----

// ClientConfigResponse：GET /api/v1/client/config（不需要登录）。
type ClientConfigResponse struct {
	LatestVersion string        `json:"latestVersion"`
	MinVersion    string        `json:"minVersion"`
	DownloadURL   string        `json:"downloadUrl"`
	SHA256        string        `json:"sha256"`
	ReleaseNotes  string        `json:"releaseNotes"`
	Announcement  string        `json:"announcement"`
	ImageSources  []ImageSource `json:"imageSources"`
}

// ImageSource 是图片面板的一个图源（PLAN 6.9）。
type ImageSource struct {
	URL      string `json:"url"`
	Weight   int    `json:"weight"`
	IsAPI    bool   `json:"isApi"`              // true 时 URL 返回 JSON，图片地址在 JSONPath 处
	JSONPath string `json:"jsonPath,omitempty"` // 例如 "data.url"
}

// HealthResponse：GET /healthz。
type HealthResponse struct {
	Status  string `json:"status"` // ok
	Version string `json:"version"`
}

// ---- 旧客户端的升级提示（PLAN 7.12） ----

// LegacyChallengeResponse 是 POST /api/login/get_challenge 返回的假挑战码。
type LegacyChallengeResponse struct {
	Challenge string `json:"challenge"`
}

// LegacyErrorResponse 是旧路径统一返回的 426 响应体。旧客户端会把 error 原样弹出来。
type LegacyErrorResponse struct {
	Error string `json:"error"`
}

// ---- 官方节点 ----

// OfficialNode 是面板下发的一个官方节点：连接参数里已经带上了当前用户的节点凭证。
type OfficialNode struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	ServerAddr  string      `json:"serverAddr"`
	ServerPort  int         `json:"serverPort"`
	Token       string      `json:"token"` // frps 的 auth.token；真正的准入由节点凭证决定
	User        string      `json:"user"`  // frp 的 user，就是用户的昵称，代理名会加上这个前缀
	Credential  string      `json:"credential"`
	Online      bool        `json:"online"`
	Quota       quota.Quota `json:"quota"` // 当前用户在这个节点上的限额
}

// OfficialNodesResponse：GET /api/v1/official-nodes。
type OfficialNodesResponse struct {
	Nodes []OfficialNode `json:"nodes"`
}

// MetaCredential 是 frpc metadatas 里放节点凭证的键。
const MetaCredential = "moefrp_cred"

// ToNode 把官方节点转换成客户端能直接运行的节点。ID 加上 OfficialNodePrefix，和用户自己的节点区分开。
func (o OfficialNode) ToNode() model.Node {
	return model.Node{
		ID: OfficialNodePrefix + o.ID, Remark: o.Name, ServerAddr: o.ServerAddr, ServerPort: o.ServerPort,
		Token: o.Token, User: o.User, Metadatas: map[string]string{MetaCredential: o.Credential},
	}
}

// OfficialNodePrefix 是官方节点在客户端里的 ID 前缀。
const OfficialNodePrefix = "official-"
