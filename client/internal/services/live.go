package services

import (
	"strings"
	"time"

	"moefrp/client/internal/engine"
	"moefrp/shared/api"
)

// 实时通道（docs/client-live.md）里和隧道有关的部分。

// TicketsInvalid 在面板作废登录票据（轮换密钥、重置凭证）后调用：丢掉预取的票据；正在用官方节点的
// 马上重新连接，用新票据登录，不用等旧连接自己发现。
func (c *TunnelControl) TicketsInvalid() {
	t := c.t
	t.mu.Lock()
	pool := t.tickets
	t.mu.Unlock()
	if pool != nil {
		pool.Clear()
	}
	pid, nid, ok := t.eng.RunningProfile()
	if !ok || !strings.HasPrefix(nid, api.OfficialNodePrefix) {
		return
	}
	t.sink.Appendf("info", "官方节点的登录票据已更新，重新连接")
	if err := t.Start(pid, nid); err != nil {
		t.sink.Appendf("warn", "重新连接失败：%v", err)
	}
}

// Remote 执行管理员的远程命令（停止 / 重启隧道），返回要告诉用户的话；隧道没在运行时返回空串。
func (c *TunnelControl) Remote(cmd api.LiveTunnelCommand) string {
	t := c.t
	pid, nid, ok := t.eng.RunningProfile()
	if !ok {
		return ""
	}
	why := ""
	if cmd.Reason != "" {
		why = "：" + cmd.Reason
	}
	switch cmd.Action {
	case api.TunnelStop:
		t.sink.Appendf("warn", "管理员停止了隧道%s", why)
		_ = t.Stop()
		return "管理员停止了隧道" + why
	case api.TunnelRestart:
		t.sink.Appendf("info", "管理员重启了隧道%s", why)
		if err := t.Start(pid, nid); err != nil {
			t.sink.Appendf("warn", "重启失败：%v", err)
		}
		return "管理员重启了隧道" + why
	}
	return ""
}

// LiveStatus 把隧道状态转成上报给面板的样子（只有种类、名称、数量，不含地址、令牌）。
func (c *TunnelControl) LiveStatus(st engine.Status) api.LiveTunnelInfo {
	t := c.t
	out := api.LiveTunnelInfo{State: string(st.State), NodeName: st.NodeLabel, ProfileName: st.ProfileName}
	if st.State == engine.StateFailed {
		out.State = "error"
	}
	if st.Error != nil {
		out.Error = st.Error.Message
	}
	if id, ok := strings.CutPrefix(st.NodeID, api.OfficialNodePrefix); ok {
		out.OfficialID = id
	}
	if c, ok := t.profiles.Get(st.ProfileID); ok {
		switch {
		case c.IsSubscription():
			out.ProfileKind = "subscription"
		case c.IsCloud():
			out.ProfileKind = "cloud"
		default:
			out.ProfileKind = "local"
		}
		for _, p := range c.Profile.Proxies {
			if p.Enabled {
				out.Proxies++
			}
		}
	}
	for _, p := range t.eng.ProxyStatuses() {
		if p.Phase == "running" {
			out.Running++
		}
	}
	if st.ConnectedAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, st.ConnectedAt); err == nil {
			out.Since = ts.Unix()
		}
	} else if st.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil {
			out.Since = ts.Unix()
		}
	}
	return out
}
