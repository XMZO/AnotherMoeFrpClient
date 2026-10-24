package model

// 分享与订阅（PLAN 7.6）。
//
// 分享保存的是创建时的快照（SharePayload），之后分享者改自己的配置不会影响分享，
// 需要时"重新发布"。快照里的节点、代理沿用来源配置里的 ID，重新发布时按这些 ID 重新取。
//
//	template：快照只有节点；订阅者选一个节点 + 自己的代理。
//	full    ：快照有节点 + 代理；订阅者可选节点，代理原样运行。
//	p2p     ：快照有一个节点 + 若干 stcp/xtcp/sudp 目标；订阅者为每个目标填本地绑定端口。

// ShareKinds 是全部分享类型。
var ShareKinds = []string{ShareTemplate, ShareFull, ShareP2P}

// ValidShareKind 判断分享类型是否合法。
func ValidShareKind(k string) bool {
	switch k {
	case ShareTemplate, ShareFull, ShareP2P:
		return true
	}
	return false
}

// SharePayload 是分享快照，存于 shares.payload。含节点 token 和 secretKey，
// 只返回给分享者本人；订阅者通过 runtime 拿到运行所需的部分。
type SharePayload struct {
	Nodes   []Node      `json:"nodes"`
	Proxies []Proxy     `json:"proxies"` // 仅 full
	Targets []P2PTarget `json:"targets"` // 仅 p2p
}

// Normalize 把 nil 切片换成空切片。
func (p *SharePayload) Normalize() {
	if p.Nodes == nil {
		p.Nodes = []Node{}
	}
	if p.Proxies == nil {
		p.Proxies = []Proxy{}
	}
	if p.Targets == nil {
		p.Targets = []P2PTarget{}
	}
}

// P2PTarget 是 p2p 分享里可以被访问的一个 stcp/xtcp/sudp 代理。
// ID 沿用来源代理的 ID；ServerName 是分享者那边的代理名。
type P2PTarget struct {
	ID         string `json:"id"`
	ServerName string `json:"serverName"`
	Type       string `json:"type"` // stcp xtcp sudp
	SecretKey  string `json:"secretKey"`
	ServerUser string `json:"serverUser,omitempty"` // 分享者节点上的 frp user
	LocalPort  int    `json:"localPort,omitempty"`  // 仅用于预览时提示（例如 3389），不参与运行
	// 目标代理的加密、压缩设置：订阅者的访客照着设（xtcp 两边必须一致）。旧的分享快照里没有，重新发布后才有
	UseEncryption  bool `json:"useEncryption,omitempty"`
	UseCompression bool `json:"useCompression,omitempty"`
}

// SubscriptionParams 是订阅者为订阅提供的参数，结构随分享类型不同。
type SubscriptionParams struct {
	NodeID  string    `json:"nodeId,omitempty"` // template 必填；full 可选（空表示第一个节点）
	Proxies []Proxy   `json:"proxies"`          // 仅 template：订阅者自己的代理
	Binds   []P2PBind `json:"binds"`            // 仅 p2p：每个目标一条
}

// Normalize 把 nil 切片换成空切片。
func (p *SubscriptionParams) Normalize() {
	if p.Proxies == nil {
		p.Proxies = []Proxy{}
	}
	if p.Binds == nil {
		p.Binds = []P2PBind{}
	}
}

// P2PBind 描述订阅者在本机为某个 p2p 目标开的监听，运行时生成一个 visitor。
type P2PBind struct {
	ID       string `json:"id"`       // 生成的 visitor 的 ID
	TargetID string `json:"targetId"` // 对应 P2PTarget.ID
	Name     string `json:"name"`     // 生成的 visitor 名，默认 "p2p-" + serverName
	Enabled  bool   `json:"enabled"`
	BindAddr string `json:"bindAddr"` // 默认 127.0.0.1
	BindPort int    `json:"bindPort"`
}

// BuildRuntime 按分享类型把快照和订阅参数组装成可运行的配置内容。
// 调用前快照和参数都应已通过校验；找不到的引用会被跳过。
func BuildRuntime(kind string, payload SharePayload, params SubscriptionParams) (ProfileData, string) {
	data := ProfileData{Nodes: append([]Node(nil), payload.Nodes...)}
	nodeID := params.NodeID
	switch kind {
	case ShareTemplate:
		data.Proxies = append([]Proxy(nil), params.Proxies...)
	case ShareFull:
		data.Proxies = append([]Proxy(nil), payload.Proxies...)
	case ShareP2P:
		targets := map[string]P2PTarget{}
		for _, t := range payload.Targets {
			targets[t.ID] = t
		}
		for _, b := range params.Binds {
			t, ok := targets[b.TargetID]
			if !ok {
				continue
			}
			addr := b.BindAddr
			if addr == "" {
				addr = "127.0.0.1"
			}
			data.Visitors = append(data.Visitors, Visitor{
				ID: b.ID, Name: b.Name, Type: t.Type, Enabled: b.Enabled,
				ServerName: t.ServerName, ServerUser: t.ServerUser, SecretKey: t.SecretKey,
				BindAddr: addr, BindPort: b.BindPort, UseEncryption: t.UseEncryption, UseCompression: t.UseCompression,
			})
		}
	}
	found := false
	for _, n := range data.Nodes {
		if n.ID == nodeID {
			found = true
			break
		}
	}
	if !found {
		nodeID = ""
		if len(data.Nodes) > 0 {
			nodeID = data.Nodes[0].ID
		}
	}
	data.Normalize()
	return data, nodeID
}
