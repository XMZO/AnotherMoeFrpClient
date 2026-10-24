package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/nodeticket"
	"moefrp/shared/quota"
)

// autoAgent 自动应答配置下发、存储租约和授权表，记下最近一次收到的表和租约。
type autoAgent struct {
	*fakeAgent
	mu     sync.Mutex
	table  *agentproto.AccessTable
	leased *agentproto.StoreLease
	syncs  int
}

func (a *autoAgent) run() {
	go func() {
		for m := range a.reqs {
			switch m.Method {
			case agentproto.MethodFrpsApply:
				var r agentproto.ApplyRequest
				json.Unmarshal(m.Data, &r)
				a.reply(m, agentproto.ApplyResult{Rev: r.Rev, Applied: true})
			case agentproto.MethodAccessSync:
				var t agentproto.AccessTable
				json.Unmarshal(m.Data, &t)
				a.mu.Lock()
				a.table, a.syncs = &t, a.syncs+1
				a.mu.Unlock()
				a.reply(m, agentproto.AccessResult{Users: len(t.Users)})
			case agentproto.MethodStoreLease:
				var l agentproto.StoreLease
				json.Unmarshal(m.Data, &l)
				a.mu.Lock()
				a.leased = &l
				a.mu.Unlock()
				a.reply(m, struct{}{})
			default:
				a.reply(m, struct{}{})
			}
		}
	}()
}

func (a *autoAgent) lease() *agentproto.StoreLease {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.leased
}

func (a *autoAgent) users() map[string]agentproto.AccessUser {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]agentproto.AccessUser{}
	if a.table == nil {
		return out
	}
	for _, u := range a.table.Users {
		out[u.Nickname] = u
	}
	return out
}

func (a *autoAgent) last() agentproto.AccessTable {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.table == nil {
		return agentproto.AccessTable{}
	}
	return *a.table
}

func (a *autoAgent) key(t *testing.T) []byte {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(a.last().TicketKey)
	if err != nil || len(key) == 0 {
		t.Fatalf("票据密钥：%q", a.last().TicketKey)
	}
	return key
}

// claims 用节点收到的票据密钥校验票据，就像节点上的 agent 那样。
func (a *autoAgent) claims(t *testing.T, ticket string) nodeticket.Claims {
	t.Helper()
	c, err := nodeticket.Verify(a.key(t), ticket)
	if err != nil {
		t.Fatalf("节点应能校验面板签发的票据：%v", err)
	}
	return c
}

var managedHello = agentproto.Hello{AgentVersion: "1.4.0", OS: "linux", Arch: "amd64", MachineID: "machine-1",
	Features: []string{agentproto.FeatureAccess, agentproto.FeatureTickets, agentproto.FeatureSettings}}

const (
	devA = "0123456789abcdef0123456789abcdef"
	devB = "fedcba9876543210fedcba9876543210"
)

// deviceLogin 带着设备指纹登录（像客户端那样），返回令牌。
func (n *nodeEnv) deviceLogin(nick, device string) string {
	n.t.Helper()
	var out api.LoginResponse
	n.do(req{method: "POST", path: api.RouteLogin, body: api.LoginRequest{Nickname: nick, Password: "password-" + nick},
		header: map[string]string{api.HeaderDeviceID: device}}).expect(http.StatusOK).decode(&out)
	return out.Token
}

func TestOfficialNodes(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("东京")
	en := n.enroll(code)
	fa, _, err := dialAgent(t, n.http.URL, en.Token, managedHello)
	if err != nil {
		t.Fatal(err)
	}
	a := &autoAgent{fakeAgent: fa}
	a.run()
	n.admin("PATCH", "/nodes/"+id, updateNodeRequest{Name: "东京", PublicAddr: "tokyo.example.com"}).expect(http.StatusNoContent)
	// 节点都由面板托管：授权表始终启用，新节点默认只给管理员（boss）用
	eventually(t, "上线后推送表", func() bool { _, ok := a.users()["boss"]; return a.last().Enabled && ok && len(a.users()) == 1 })

	n.register("alice", "password-alice")
	n.register("bob", "password-bob")
	n.register("carol", "password-carol")
	adm := "admin"
	n.admin("PATCH", "/users/bob", updateUserRequest{Role: &adm}).expect(http.StatusNoContent)
	aliceTok := n.deviceLogin("alice", devA)
	bobTok := n.deviceLogin("bob", devA)

	official := func(tok string) []api.OfficialNode {
		t.Helper()
		var out api.OfficialNodesResponse
		n.do(req{method: "GET", path: api.RouteOfficialNodes, token: tok}).expect(http.StatusOK).decode(&out)
		return out.Nodes
	}
	tickets := func(tok, device, node string, count int) resp {
		t.Helper()
		return n.do(req{method: "POST", path: api.OfficialTicketsPath(node), token: tok, body: api.NodeTicketsRequest{Count: count},
			header: map[string]string{api.HeaderDeviceID: device}})
	}
	issue := func(tok string) []api.NodeTicket {
		t.Helper()
		var out api.NodeTicketsResponse
		tickets(tok, devA, id, 3).expect(http.StatusOK).decode(&out)
		if len(out.Tickets) != 3 {
			t.Fatalf("票据：%+v", out)
		}
		return out.Tickets
	}

	// 管理员默认能用，普通用户不能
	eventually(t, "管理员 bob 可用", func() bool { _, ok := a.users()["bob"]; _, al := a.users()["alice"]; return ok && !al })
	if len(official(aliceTok)) != 0 {
		t.Fatal("没开放给普通用户时不下发")
	}
	bobNodes := official(bobTok)
	if len(bobNodes) != 1 {
		t.Fatalf("bob: %+v", bobNodes)
	}
	b := bobNodes[0]
	if b.ServerAddr != "tokyo.example.com" || b.ServerPort != 7000 || b.User != "bob" || !b.Online || b.Quota.MaxProxies != 0 {
		t.Fatalf("下发的节点：%+v", b)
	}
	raw := n.do(req{method: "GET", path: api.RouteOfficialNodes, token: bobTok}).expect(http.StatusOK).Body.String()
	if strings.Contains(raw, `"token"`) || strings.Contains(raw, `"credential"`) {
		t.Fatalf("下发给客户端的节点里不应有 token：%s", raw)
	}
	if nd := b.ToNode(); nd.ID != api.OfficialNodePrefix+id || nd.Token != "" || len(nd.Metadatas) != 0 {
		t.Fatalf("ToNode：%+v", nd)
	}

	// 开放给普通用户，带说明
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Roles: []string{"user"}, Description: "晚高峰稳定"}).expect(http.StatusNoContent)
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Roles: []string{"admin"}}).apiErr(apierr.ValidationFailed) // 管理员总能用，不用选
	eventually(t, "普通用户可用", func() bool { _, ok := a.users()["alice"]; return ok })
	if al := official(aliceTok); len(al) != 1 || al[0].Description != "晚高峰稳定" || al[0].Quota.MaxProxies != 5 {
		t.Fatalf("alice：%+v", al)
	}

	// 票据：节点能用下发的密钥校验，绑定用户、会话、节点、代数、设备，有效期 15 分钟，每张随机数不同
	at := issue(aliceTok)
	c0, c1 := a.claims(t, at[0].Ticket), a.claims(t, at[1].Ticket)
	if c0.User != a.users()["alice"].UserID || c0.Node != id || c0.Session == "" || c0.Gen != a.users()["alice"].Gen ||
		c0.Expires-c0.Issued != 15*60 || c0.Expires != at[0].ExpiresAt || c0.Nonce == c1.Nonce || c0.Session != c1.Session {
		t.Fatalf("票据声明：%+v %+v", c0, c1)
	}
	if c0.Device != nodeticket.DeviceTag(a.key(t), devA) || c0.Device == nodeticket.DeviceTag(a.key(t), devB) {
		t.Fatal("票据应绑定登录时的设备")
	}
	// 设备对不上（登录凭据被拷到别的电脑上）、没有设备指纹的旧客户端：领不到票据
	tickets(aliceTok, devB, id, 1).apiErr(apierr.Forbidden)
	tickets(aliceTok, "", id, 1).apiErr(apierr.Forbidden)
	oldTok := n.login("carol", "password-carol") // 不带设备指纹登录（旧客户端）
	n.admin("PUT", "/users/carol/nodes", userNodesRequest{Grants: map[string]*bool{id: ptrBool(true)}}).expect(http.StatusNoContent)
	tickets(oldTok, devA, id, 1).apiErr(apierr.Forbidden)
	// 其他错误
	tickets(aliceTok, devA, "no-such-node", 1).apiErr(apierr.NotFound)
	tickets(aliceTok, devA, id, 0).apiErr(apierr.ValidationFailed)
	tickets(aliceTok, devA, id, api.MaxTicketsPerRequest+1).apiErr(apierr.ValidationFailed)
	tickets("", devA, id, 1).expect(http.StatusUnauthorized)

	// 另一台设备登录（测试环境是单设备模式）：被挤下线的旧会话在节点上列为已撤销
	alice2 := n.deviceLogin("alice", devB)
	var out api.NodeTicketsResponse
	tickets(alice2, devB, id, 1).expect(http.StatusOK).decode(&out)
	c2 := a.claims(t, out.Tickets[0].Ticket)
	if c2.Session == c0.Session {
		t.Fatal("不同设备（会话）的票据应绑定各自的会话")
	}
	eventually(t, "挤下线的会话推给节点", func() bool { return slices.Contains(a.last().RevokedSessions, c0.Session) })
	// 退出登录：这个会话也列为已撤销
	n.do(req{method: "POST", path: api.RouteLogout, token: alice2}).expect(http.StatusNoContent)
	eventually(t, "退出登录推给节点", func() bool { return slices.Contains(a.last().RevokedSessions, c2.Session) })
	aliceTok = n.deviceLogin("alice", devA)

	// ETag：没变化时 304
	r := n.do(req{method: "GET", path: api.RouteOfficialNodes, token: bobTok}).expect(http.StatusOK)
	n.do(req{method: "GET", path: api.RouteOfficialNodes, token: bobTok, header: map[string]string{"If-None-Match": r.Header().Get("ETag")}}).
		expect(http.StatusNotModified)

	// 单独授权：禁止 alice（不管开放给谁）；管理员 bob 也可以单独禁止；alice 的限额覆盖
	yes, no := true, false
	fifty := 50
	n.admin("PUT", "/users/alice/nodes", userNodesRequest{Override: &quota.Override{MaxProxies: &fifty}, Grants: map[string]*bool{id: &no}}).expect(http.StatusNoContent)
	n.admin("PUT", "/users/bob/nodes", userNodesRequest{Grants: map[string]*bool{id: &no}}).expect(http.StatusNoContent)
	eventually(t, "单独禁止生效", func() bool { u := a.users(); _, al := u["alice"]; _, bo := u["bob"]; return !al && !bo })
	if len(official(aliceTok)) != 0 || len(official(bobTok)) != 0 {
		t.Fatal("单独禁止应覆盖角色")
	}
	n.admin("PUT", "/users/bob/nodes", userNodesRequest{Grants: map[string]*bool{id: &yes}}).expect(http.StatusNoContent)
	// 去掉对 alice 的禁止：又按角色可用，带上覆盖后的限额
	n.admin("PUT", "/users/alice/nodes", userNodesRequest{Override: &quota.Override{MaxProxies: &fifty}, Grants: map[string]*bool{id: nil}}).expect(http.StatusNoContent)
	eventually(t, "alice 回来", func() bool { return a.users()["alice"].Quota.MaxProxies == 50 })

	// 角色限额
	roles := quota.DefaultRoles()
	roles.User = quota.Quota{MaxProxies: 3, Ports: "20000-21000", Types: []string{"tcp"}}
	n.admin("PUT", "/quotas", roles).expect(http.StatusNoContent)
	n.admin("PUT", "/users/alice/nodes", userNodesRequest{}).expect(http.StatusNoContent)
	eventually(t, "角色限额推送", func() bool { return a.users()["alice"].Quota.Ports == "20000-21000" })
	bad := roles
	bad.Admin.Ports = "70000"
	n.admin("PUT", "/quotas", bad).apiErr(apierr.ValidationFailed)

	// 重置凭证：代数 +1，节点上旧票据失效，新票据带新代数
	old := a.users()["alice"].Gen
	oldTicket := a.claims(t, issue(aliceTok)[0].Ticket)
	n.admin("POST", "/users/alice/reset-node-credential", nil).expect(http.StatusNoContent)
	eventually(t, "凭证换代", func() bool { return a.users()["alice"].Gen == old+1 })
	if c := a.claims(t, issue(aliceTok)[0].Ticket); c.Gen != old+1 || oldTicket.Gen != old {
		t.Fatalf("新票据的代数：%d（旧 %d）", c.Gen, oldTicket.Gen)
	}

	// 管理员踢下线：alice 的会话列为已撤销
	n.admin("POST", "/users/alice/kick", nil).expect(http.StatusOK)
	eventually(t, "踢下线推给节点", func() bool { return slices.Contains(a.last().RevokedSessions, oldTicket.Session) })

	// 停用用户：从表里去掉
	n.admin("PATCH", "/users/alice", updateUserRequest{Disabled: &yes}).expect(http.StatusNoContent)
	eventually(t, "停用后撤销", func() bool { _, ok := a.users()["alice"]; return !ok })

	// 不再开放给普通用户
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Roles: []string{}}).expect(http.StatusNoContent)
	// 剩下管理员（boss、单独允许的 bob）和单独允许的 carol
	eventually(t, "只给管理员和单独允许的", func() bool {
		u := a.users()
		_, boss := u["boss"]
		_, bob := u["bob"]
		_, carol := u["carol"]
		return len(u) == 3 && boss && bob && carol
	})
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Roles: []string{"vip"}}).apiErr(apierr.ValidationFailed) // 已经没有 VIP 了
}

func TestOfficialNeedsNewAgent(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("旧节点")
	en := n.enroll(code)
	// 1.3.0 支持票据但不支持托管（结构化配置、没有 token）：不下发给用户，后台提示升级，配置保存但不下发
	fa, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.3.0", OS: "linux", Arch: "amd64",
		Features: []string{agentproto.FeatureAccess, agentproto.FeatureTickets}})
	if err != nil {
		t.Fatal(err)
	}
	(&autoAgent{fakeAgent: fa}).run()
	eventually(t, "在线", func() bool { return n.node(id)["status"] == "online" })
	if n.node(id)["accessSupported"] != false {
		t.Fatal("旧 agent 不支持托管")
	}
	n.admin("PATCH", "/nodes/"+id, updateNodeRequest{Name: "旧节点", PublicAddr: "old.example.com"}).expect(http.StatusNoContent)
	bossTok := n.deviceLogin("boss", devA)
	var out api.OfficialNodesResponse
	n.do(req{method: "GET", path: api.RouteOfficialNodes, token: bossTok}).expect(http.StatusOK).decode(&out)
	if len(out.Nodes) != 0 {
		t.Fatalf("旧 agent 的节点不下发：%+v", out.Nodes)
	}
	e := n.admin("PUT", "/nodes/"+id+"/settings", n.node(id)["settings"]).apiErr(apierr.NodeFailed)
	if e.Message == "" {
		t.Fatal("应提示升级")
	}
}

func ptrBool(v bool) *bool { return &v }
