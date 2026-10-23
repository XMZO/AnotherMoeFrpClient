package validate

import (
	"slices"
	"strconv"

	"moefrp/shared/model"
)

// 分享相关的上限。
const (
	MaxShareNameLen    = 50
	MaxTargetsPerShare = 50
	MaxBindsPerSub     = 50
	MaxSubscribersCap  = 10000 // maxSubscribers 的取值上限
)

// ShareName 校验分享名：去掉首尾空白后不能为空，最多 50 个字符。
func ShareName(name string) error {
	var c collector
	checkText(&c, "name", name, MaxShareNameLen, true)
	return c.err()
}

// ShareKind 校验分享类型。
func ShareKind(kind string) error {
	var c collector
	if !model.ValidShareKind(kind) {
		if kind == "" {
			c.add("kind", ReasonRequired, "分享类型不能为空")
		} else {
			c.add("kind", ReasonUnknown, "不支持的分享类型 %q", kind)
		}
	}
	return c.err()
}

// MaxSubscribers 校验订阅人数上限：0 表示不限。
func MaxSubscribers(n int) error {
	var c collector
	if n < 0 || n > MaxSubscribersCap {
		c.add("maxSubscribers", ReasonRange, "订阅人数上限的范围是 0–%d（0 表示不限）", MaxSubscribersCap)
	}
	return c.err()
}

// SharePayload 按分享类型校验快照（PLAN 7.6）。
func SharePayload(kind string, p model.SharePayload) error {
	var c collector
	c.merge(ShareKind(kind))

	switch {
	case len(p.Nodes) == 0:
		c.add("payload.nodes", ReasonRequired, "分享至少需要一个节点")
	case kind == model.ShareP2P && len(p.Nodes) != 1:
		c.add("payload.nodes", ReasonTooMany, "P2P 分享只能有一个节点")
	}

	switch kind {
	case model.ShareTemplate:
		if len(p.Proxies) > 0 {
			c.add("payload.proxies", ReasonInvalid, "模板分享不包含代理")
		}
		if len(p.Targets) > 0 {
			c.add("payload.targets", ReasonInvalid, "模板分享不包含 P2P 目标")
		}
	case model.ShareFull:
		if len(p.Proxies) == 0 {
			c.add("payload.proxies", ReasonRequired, "完整分享至少需要一个代理")
		}
		if len(p.Targets) > 0 {
			c.add("payload.targets", ReasonInvalid, "完整分享不包含 P2P 目标")
		}
	case model.ShareP2P:
		if len(p.Proxies) > 0 {
			c.add("payload.proxies", ReasonInvalid, "P2P 分享不包含普通代理")
		}
		if len(p.Targets) == 0 {
			c.add("payload.targets", ReasonRequired, "P2P 分享至少需要一个 stcp/xtcp/sudp 代理")
		}
	}

	// 节点和 full 的代理沿用配置的校验规则（含重名检查）
	if err := ProfileData(model.ProfileData{Nodes: p.Nodes, Proxies: p.Proxies}); err != nil {
		if es, ok := err.(Errors); ok {
			for _, e := range es {
				e.Field = "payload." + e.Field
				c.errs = append(c.errs, e)
			}
		}
	}

	if len(p.Targets) > MaxTargetsPerShare {
		c.add("payload.targets", ReasonTooMany, "P2P 目标最多 %d 个", MaxTargetsPerShare)
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for i, t := range p.Targets {
		base := "payload.targets[" + strconv.Itoa(i) + "]"
		checkID(&c, base+".id", t.ID)
		if ids[t.ID] {
			c.add(base+".id", ReasonDuplicate, "目标 ID 重复")
		}
		ids[t.ID] = true
		checkProxyName(&c, base+".serverName", t.ServerName)
		if names[t.ServerName] {
			c.add(base+".serverName", ReasonDuplicate, "目标名称 %q 重复", t.ServerName)
		}
		names[t.ServerName] = true
		if !model.IsP2PType(t.Type) {
			c.add(base+".type", ReasonUnknown, "P2P 目标只能是 stcp、xtcp 或 sudp")
		}
		checkSecret(&c, base+".secretKey", t.SecretKey, true)
		checkWord(&c, base+".serverUser", t.ServerUser, MaxUserLen, false)
		if t.LocalPort != 0 {
			checkPort(&c, base+".localPort", t.LocalPort, false)
		}
	}
	return c.err()
}

// SubscriptionParams 按分享类型和快照校验订阅参数。
func SubscriptionParams(kind string, payload model.SharePayload, p model.SubscriptionParams) error {
	var c collector
	nodeExists := func(id string) bool {
		return slices.ContainsFunc(payload.Nodes, func(n model.Node) bool { return n.ID == id })
	}

	switch kind {
	case model.ShareTemplate:
		if p.NodeID == "" {
			c.add("params.nodeId", ReasonRequired, "请选择一个节点")
		} else if !nodeExists(p.NodeID) {
			c.add("params.nodeId", ReasonReference, "选择的节点不在分享里")
		}
		if err := ProfileData(model.ProfileData{Proxies: p.Proxies}); err != nil {
			if es, ok := err.(Errors); ok {
				for _, e := range es {
					e.Field = "params." + e.Field
					c.errs = append(c.errs, e)
				}
			}
		}
		if len(p.Binds) > 0 {
			c.add("params.binds", ReasonInvalid, "模板分享不需要 P2P 绑定")
		}
	case model.ShareFull:
		if p.NodeID != "" && !nodeExists(p.NodeID) {
			c.add("params.nodeId", ReasonReference, "选择的节点不在分享里")
		}
		if len(p.Proxies) > 0 {
			c.add("params.proxies", ReasonInvalid, "完整分享的代理由分享者决定，不能自定义")
		}
		if len(p.Binds) > 0 {
			c.add("params.binds", ReasonInvalid, "完整分享不需要 P2P 绑定")
		}
	case model.ShareP2P:
		if p.NodeID != "" && !nodeExists(p.NodeID) {
			c.add("params.nodeId", ReasonReference, "选择的节点不在分享里")
		}
		if len(p.Proxies) > 0 {
			c.add("params.proxies", ReasonInvalid, "P2P 分享不能自定义代理")
		}
		checkBinds(&c, payload, p.Binds)
	default:
		c.merge(ShareKind(kind))
	}
	return c.err()
}

func checkBinds(c *collector, payload model.SharePayload, binds []model.P2PBind) {
	if len(binds) == 0 {
		c.add("params.binds", ReasonRequired, "至少为一个目标设置本地端口")
	}
	if len(binds) > MaxBindsPerSub {
		c.add("params.binds", ReasonTooMany, "最多 %d 个绑定", MaxBindsPerSub)
	}
	targets := map[string]bool{}
	for _, t := range payload.Targets {
		targets[t.ID] = true
	}
	ids, names, used, listen := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, b := range binds {
		base := "params.binds[" + strconv.Itoa(i) + "]"
		checkID(c, base+".id", b.ID)
		if ids[b.ID] {
			c.add(base+".id", ReasonDuplicate, "绑定 ID 重复")
		}
		ids[b.ID] = true
		if !targets[b.TargetID] {
			c.add(base+".targetId", ReasonReference, "目标不在分享里")
		} else if used[b.TargetID] {
			c.add(base+".targetId", ReasonDuplicate, "同一个目标只能绑定一次")
		}
		used[b.TargetID] = true
		checkProxyName(c, base+".name", b.Name)
		if names[b.Name] {
			c.add(base+".name", ReasonDuplicate, "名称 %q 重复", b.Name)
		}
		names[b.Name] = true
		checkHost(c, base+".bindAddr", b.BindAddr, false)
		checkPort(c, base+".bindPort", b.BindPort, true)
		addr := b.BindAddr
		if addr == "" {
			addr = "127.0.0.1"
		}
		key := addr + ":" + strconv.Itoa(b.BindPort)
		if b.BindPort != 0 && listen[key] {
			c.add(base+".bindPort", ReasonDuplicate, "本地端口 %d 已被另一个绑定使用", b.BindPort)
		}
		listen[key] = true
	}
}
