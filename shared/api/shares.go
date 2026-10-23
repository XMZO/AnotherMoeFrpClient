package api

import (
	"time"

	"moefrp/shared/model"
)

// ---- 分享（分享者视角） ----

// CreateShareRequest：POST /api/v1/shares，成功返回 201 和 Share。
//
// NodeIDs、ProxyIDs 引用来源配置里的节点和代理：
//   - template：NodeIDs 至少一个；ProxyIDs 必须为空。
//   - full    ：NodeIDs 为空表示全部节点；ProxyIDs 为空表示全部代理。
//   - p2p     ：NodeIDs 恰好一个；ProxyIDs 至少一个，且只能是 stcp/xtcp/sudp。
type CreateShareRequest struct {
	ProfileID      string     `json:"profileId"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	NodeIDs        []string   `json:"nodeIds"`
	ProxyIDs       []string   `json:"proxyIds"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	MaxSubscribers int        `json:"maxSubscribers,omitempty"` // 0 表示不限
}

// UpdateShareRequest：PATCH /api/v1/shares/{id}，只改出现的字段。
type UpdateShareRequest struct {
	Name           *string    `json:"name,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	ClearExpiry    bool       `json:"clearExpiry,omitempty"`    // true 时去掉过期时间
	MaxSubscribers *int       `json:"maxSubscribers,omitempty"` // 0 表示不限
}

// Share 是返回给分享者本人的分享，含完整快照（包括节点 token）。
type Share struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Kind            string             `json:"kind"`
	SourceProfileID string             `json:"sourceProfileId,omitempty"`
	Revision        int64              `json:"revision"`
	ExpiresAt       *time.Time         `json:"expiresAt,omitempty"`
	MaxSubscribers  int                `json:"maxSubscribers"` // 0 表示不限
	Subscribers     int                `json:"subscribers"`
	Revoked         bool               `json:"revoked"`
	Expired         bool               `json:"expired"`
	CreatedAt       time.Time          `json:"createdAt"`
	UpdatedAt       time.Time          `json:"updatedAt"`
	Payload         model.SharePayload `json:"payload"`
}

// ListSharesResponse：GET /api/v1/shares。
type ListSharesResponse struct {
	Shares []Share `json:"shares"`
}

// ---- 订阅前预览（不含任何机密） ----

// SharePreview：GET /api/v1/shares/{id}/preview。不含节点 token 和 secretKey（PLAN 7.6）。
type SharePreview struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Kind           string           `json:"kind"`
	OwnerNickname  string           `json:"ownerNickname"`
	Revision       int64            `json:"revision"`
	ExpiresAt      *time.Time       `json:"expiresAt,omitempty"`
	MaxSubscribers int              `json:"maxSubscribers"`
	Subscribers    int              `json:"subscribers"`
	Nodes          []PreviewNode    `json:"nodes"`
	Proxies        []PreviewProxy   `json:"proxies"`
	Targets        []PreviewTarget  `json:"targets"`
	Subscription   *SubscriptionRef `json:"subscription,omitempty"` // 当前用户已订阅时给出
}

// PreviewNode 是预览里的节点：只有备注和协议，不含地址和 token。
type PreviewNode struct {
	ID       string `json:"id"`
	Remark   string `json:"remark"`
	Protocol string `json:"protocol,omitempty"`
}

// PreviewProxy 是 full 分享预览里的代理摘要。
type PreviewProxy struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	RemotePort    int      `json:"remotePort,omitempty"`
	CustomDomains []string `json:"customDomains,omitempty"`
	SubDomain     string   `json:"subdomain,omitempty"`
}

// PreviewTarget 是 p2p 分享预览里的目标摘要（不含 secretKey）。
type PreviewTarget struct {
	ID         string `json:"id"`
	ServerName string `json:"serverName"`
	Type       string `json:"type"`
	LocalPort  int    `json:"localPort,omitempty"`
}

// SubscriptionRef 指向一个已有的订阅。
type SubscriptionRef struct {
	ID string `json:"id"`
}

// ---- 订阅（订阅者视角） ----

// CreateSubscriptionRequest：POST /api/v1/subscriptions，成功返回 201 和 SubscriptionSummary。
type CreateSubscriptionRequest struct {
	ShareID string                   `json:"shareId"`
	Params  model.SubscriptionParams `json:"params"`
}

// UpdateSubscriptionRequest：PUT /api/v1/subscriptions/{id}，冲突时返回 409，details.current 为当前摘要。
type UpdateSubscriptionRequest struct {
	BaseRevision int64                    `json:"baseRevision"`
	Params       model.SubscriptionParams `json:"params"`
}

// SubscriptionRuntime：GET /api/v1/subscriptions/{id}/runtime，只返回给订阅者本人。
// Data 是可以直接交给引擎运行的配置内容（含节点 token 和 secretKey）。
type SubscriptionRuntime struct {
	SubscriptionID string            `json:"subscriptionId"`
	ShareID        string            `json:"shareId"`
	Kind           string            `json:"kind"`
	Revision       int64             `json:"revision"`      // 订阅的 revision
	ShareRevision  int64             `json:"shareRevision"` // 分享快照的 revision
	NodeID         string            `json:"nodeId"`        // 默认运行的节点
	Data           model.ProfileData `json:"data"`
}
