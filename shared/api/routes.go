// Package api 定义两端共用的路由常量、请求头和请求/响应结构体（PLAN 5.3、7.5）。
package api

import (
	"net/url"
	"strconv"
	"strings"
)

// Prefix 是新版 API 的路径前缀。
const Prefix = "/api/v1"

// 路由。带 {id} 的路径可以直接作为 Go 1.22 ServeMux 的模式使用；
// 客户端拼路径时用下面的 XxxPath 函数。
const (
	RouteHealthz      = "/healthz"
	RouteClientConfig = Prefix + "/client/config"
	// RouteClientImages 是图片面板的图源，登录后才能拿（公开的 client/config 里不带）。
	RouteClientImages = Prefix + "/client/images"

	RouteRegister       = Prefix + "/auth/register"
	RouteLogin          = Prefix + "/auth/login"
	RouteLogout         = Prefix + "/auth/logout"
	RouteResetPassword  = Prefix + "/auth/reset-password"
	RouteChangePassword = Prefix + "/auth/change-password"

	RouteMe    = Prefix + "/me"
	RouteState = Prefix + "/state"

	// RouteOfficialNodes 是面板下发给当前用户的官方节点（docs/node-agent.md 第 12 节），支持 ETag。
	RouteOfficialNodes = Prefix + "/official-nodes"
	// RouteOfficialTickets 领取官方节点的登录票据（POST），{id} 是面板上的节点 ID。
	RouteOfficialTickets = Prefix + "/official-nodes/{id}/tickets"

	RouteProfiles = Prefix + "/profiles"
	RouteProfile  = Prefix + "/profiles/{id}"

	// 以下属于 Phase 4（分享与订阅），先占位，服务端暂未实现。
	RouteShares              = Prefix + "/shares"
	RouteShare               = Prefix + "/shares/{id}"
	RouteShareRepublish      = Prefix + "/shares/{id}/republish"
	RouteSharePreview        = Prefix + "/shares/{id}/preview"
	RouteSubscriptions       = Prefix + "/subscriptions"
	RouteSubscription        = Prefix + "/subscriptions/{id}"
	RouteSubscriptionRuntime = Prefix + "/subscriptions/{id}/runtime"

	// LegacyChallenge 是旧客户端登录第一步请求的路径，切换后返回假挑战码（PLAN 7.12）。
	LegacyChallenge = "/api/login/get_challenge"
	// LegacyPrefix 下除 Prefix 以外的路径都属于旧客户端。
	LegacyPrefix = "/api/"
)

// 请求头。
const (
	HeaderAuthorization = "Authorization"
	HeaderClientVersion = "X-Client-Version"
	HeaderInstallID     = "X-Install-Id"
	// HeaderDeviceID 是客户端的设备指纹（机器 GUID 加盐哈希）：登录时记在会话上，领官方节点票据时要对得上。
	HeaderDeviceID    = "X-Device-Id"
	HeaderRequestID   = "X-Request-Id"
	HeaderIfNoneMatch = "If-None-Match"
	HeaderETag        = "ETag"
	HeaderRetryAfter  = "Retry-After"

	// BearerPrefix 是 Authorization 头里令牌前面的部分。
	BearerPrefix = "Bearer "
	// TokenPrefix 是会话令牌的固定前缀，方便在泄露扫描中识别（PLAN 7.4）。
	TokenPrefix = "mfs_"
)

// ProfilePath 返回单个配置的路径。
func ProfilePath(id string) string { return withID(RouteProfile, id) }

// OfficialTicketsPath 返回领取某个官方节点登录票据的路径。
func OfficialTicketsPath(nodeID string) string { return withID(RouteOfficialTickets, nodeID) }

// SharePath 返回单个分享的路径。
func SharePath(id string) string { return withID(RouteShare, id) }

// SharePreviewPath 返回分享预览的路径。
func SharePreviewPath(id string) string { return withID(RouteSharePreview, id) }

// ShareRepublishPath 返回重新发布分享的路径。
func ShareRepublishPath(id string) string { return withID(RouteShareRepublish, id) }

// SubscriptionPath 返回单个订阅的路径。
func SubscriptionPath(id string) string { return withID(RouteSubscription, id) }

// SubscriptionRuntimePath 返回订阅运行数据的路径。
func SubscriptionRuntimePath(id string) string { return withID(RouteSubscriptionRuntime, id) }

func withID(route, id string) string {
	return strings.Replace(route, "{id}", url.PathEscape(id), 1)
}

// StateETag 把 stateVersion 格式化成 ETag（带引号）。
func StateETag(stateVersion int64) string {
	return `"` + strconv.FormatInt(stateVersion, 10) + `"`
}
