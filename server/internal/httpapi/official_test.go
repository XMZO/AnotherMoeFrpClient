package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/nodeticket"
	"moefrp/shared/quota"
)

// autoAgent 自动应答配置下发和授权表，记下最近一次收到的表。
type autoAgent struct {
	*fakeAgent
	mu    sync.Mutex
	table *agentproto.AccessTable
	syncs int
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
			default:
				a.reply(m, struct{}{})
			}
		}
	}()
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

func (a *autoAgent) enabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.table != nil && a.table.Enabled
}

func (a *autoAgent) last() agentproto.AccessTable {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.table == nil {
		return agentproto.AccessTable{}
	}
	return *a.table
}

// claims 用节点收到的票据密钥校验票据，就像节点上的 agent 那样。
func (a *autoAgent) claims(t *testing.T, ticket string) nodeticket.Claims {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(a.last().TicketKey)
	if err != nil || len(key) == 0 {
		t.Fatalf("票据密钥：%q", a.last().TicketKey)
	}
	c, err := nodeticket.Verify(key, ticket)
	if err != nil {
		t.Fatalf("节点应能校验面板签发的票据：%v", err)
	}
	return c
}

func TestOfficialNodes(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("东京")
	en := n.enroll(code)
	fa, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.3.0", OS: "linux", Arch: "amd64",
		Features: []string{agentproto.FeatureAccess, agentproto.FeatureTickets}})
	if err != nil {
		t.Fatal(err)
	}
	a := &autoAgent{fakeAgent: fa}
	a.run()
	n.admin("PATCH", "/nodes/"+id, updateNodeRequest{Name: "东京", PublicAddr: "tokyo.example.com"}).expect(http.StatusNoContent)
	eventually(t, "上线后推送表（未开启）", func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.table != nil && !a.table.Enabled })

	aliceTok := n.user("alice")
	bobTok := n.user("bob")
	n.user("carol")
	adm := "admin"
	n.admin("PATCH", "/users/bob", updateUserRequest{Role: &adm}).expect(http.StatusNoContent)

	official := func(nick, tok string) []api.OfficialNode {
		t.Helper()
		var out api.OfficialNodesResponse
		n.do(req{method: "GET", path: api.RouteOfficialNodes, token: tok}).expect(http.StatusOK).decode(&out)
		return out.Nodes
	}
	tickets := func(tok, node string, count int) resp {
		t.Helper()
		return n.do(req{method: "POST", path: "/api/v1/official-nodes/" + node + "/tickets", token: tok, body: api.NodeTicketsRequest{Count: count}})
	}
	issue := func(tok string) []api.NodeTicket {
		t.Helper()
		var out api.NodeTicketsResponse
		tickets(tok, id, 3).expect(http.StatusOK).decode(&out)
		if len(out.Tickets) != 3 {
			t.Fatalf("票据：%+v", out)
		}
		return out.Tickets
	}
	if len(official("alice", aliceTok)) != 0 {
		t.Fatal("没开官方节点时不下发")
	}

	// 只开放给管理员
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: true, Roles: []string{"admin"}, Description: "管理员专用"}).expect(http.StatusNoContent)
	eventually(t, "管理员可用", func() bool { u := a.users(); _, ok := u["bob"]; _, al := u["alice"]; return a.enabled() && ok && !al })
	if len(official("alice", aliceTok)) != 0 {
		t.Fatal("普通用户不能用只开放给管理员的节点")
	}
	bobNodes := official("bob", bobTok)
	if len(bobNodes) != 1 {
		t.Fatalf("bob: %+v", bobNodes)
	}
	b := bobNodes[0]
	if b.ServerAddr != "tokyo.example.com" || b.ServerPort != 7000 || b.Token == "" || b.User != "bob" || b.Description != "管理员专用" ||
		!b.Online || b.Quota.MaxProxies != 0 {
		t.Fatalf("下发的节点：%+v", b)
	}
	if nd := b.ToNode(); nd.ID != api.OfficialNodePrefix+id || len(nd.Metadatas) != 0 {
		t.Fatalf("ToNode：%+v", nd)
	}
	if tb := a.last(); tb.NodeID != id || tb.TicketKey == "" {
		t.Fatalf("授权表：%+v", tb)
	}

	// 票据：节点能用下发的密钥校验，绑定用户、会话、节点、代数，有效期 15 分钟，每张随机数不同
	bt := issue(bobTok)
	c0, c1 := a.claims(t, bt[0].Ticket), a.claims(t, bt[1].Ticket)
	if c0.User != a.users()["bob"].UserID || c0.Node != id || c0.Session == "" || c0.Gen != a.users()["bob"].Gen ||
		c0.Expires-c0.Issued != 15*60 || c0.Expires != bt[0].ExpiresAt || c0.Nonce == c1.Nonce || c0.Session != c1.Session {
		t.Fatalf("票据声明：%+v %+v", c0, c1)
	}
	tickets(aliceTok, id, 1).apiErr(apierr.NotFound) // 没权限
	tickets(bobTok, "no-such-node", 1).apiErr(apierr.NotFound)
	tickets(bobTok, id, 0).apiErr(apierr.ValidationFailed)
	tickets(bobTok, id, api.MaxTicketsPerRequest+1).apiErr(apierr.ValidationFailed)
	tickets("", id, 1).expect(http.StatusUnauthorized)

	// 另一台设备登录（测试环境是单设备模式）：新会话的票据绑定新会话，被挤下线的旧会话在节点上列为已撤销
	bob2 := n.login("bob", "password-bob")
	c2 := a.claims(t, issue(bob2)[0].Ticket)
	if c2.Session == c0.Session {
		t.Fatal("不同设备（会话）的票据应绑定各自的会话")
	}
	eventually(t, "挤下线的会话推给节点", func() bool { return slices.Contains(a.last().RevokedSessions, c0.Session) })
	if slices.Contains(a.last().RevokedSessions, c2.Session) {
		t.Fatal("在用的会话不应被撤销")
	}
	// 退出登录：这个会话也列为已撤销
	n.do(req{method: "POST", path: api.RouteLogout, token: bob2}).expect(http.StatusNoContent)
	eventually(t, "退出登录推给节点", func() bool { return slices.Contains(a.last().RevokedSessions, c2.Session) })
	bobTok = n.login("bob", "password-bob")

	// ETag：没变化时 304
	r := n.do(req{method: "GET", path: api.RouteOfficialNodes, token: bobTok}).expect(http.StatusOK)
	n.do(req{method: "GET", path: api.RouteOfficialNodes, token: bobTok, header: map[string]string{"If-None-Match": r.Header().Get("ETag")}}).
		expect(http.StatusNotModified)

	// 单独授权：alice 允许，bob 禁止；bob 的限额覆盖
	yes, no := true, false
	fifty := 50
	n.admin("PUT", "/users/alice/nodes", userNodesRequest{Grants: map[string]*bool{id: &yes}}).expect(http.StatusNoContent)
	n.admin("PUT", "/users/bob/nodes", userNodesRequest{Override: &quota.Override{MaxProxies: &fifty}, Grants: map[string]*bool{id: &no}}).expect(http.StatusNoContent)
	eventually(t, "单独授权生效", func() bool { u := a.users(); _, al := u["alice"]; _, bo := u["bob"]; return al && !bo })
	if len(official("bob", bobTok)) != 0 || len(official("alice", aliceTok)) != 1 {
		t.Fatal("单独授权应覆盖角色")
	}
	var un map[string]any
	n.admin("GET", "/users/bob/nodes", nil).expect(http.StatusOK).decode(&un)
	if eff := un["effective"].(map[string]any); eff["maxProxies"] != float64(50) {
		t.Fatalf("有效限额：%v", un)
	}
	// 去掉对 bob 的禁止：又按角色可用，带上覆盖后的限额
	n.admin("PUT", "/users/bob/nodes", userNodesRequest{Override: &quota.Override{MaxProxies: &fifty}, Grants: map[string]*bool{id: nil}}).expect(http.StatusNoContent)
	eventually(t, "bob 回来", func() bool { return a.users()["bob"].Quota.MaxProxies == 50 })

	// 角色限额
	roles := quota.DefaultRoles()
	roles.User = quota.Quota{MaxProxies: 3, Ports: "20000-21000", Types: []string{"tcp"}}
	n.admin("PUT", "/quotas", roles).expect(http.StatusNoContent)
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

	// 关闭官方节点
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: false, Roles: []string{"admin"}}).expect(http.StatusNoContent)
	eventually(t, "关闭", func() bool { return !a.enabled() })
	if len(official("bob", bobTok)) != 0 {
		t.Fatal("关闭后不下发")
	}
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: true, Roles: []string{"god"}}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: true, Roles: []string{"vip"}}).apiErr(apierr.ValidationFailed) // 已经没有 VIP 了
}

func TestOfficialNeedsNewAgent(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("旧节点")
	en := n.enroll(code)
	// 1.2.0 支持授权表但不支持票据：也要先升级
	fa, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.2.0", OS: "linux", Arch: "amd64",
		Features: []string{agentproto.FeatureAccess}})
	if err != nil {
		t.Fatal(err)
	}
	(&autoAgent{fakeAgent: fa}).run()
	eventually(t, "在线", func() bool { return n.node(id)["status"] == "online" })
	e := n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: true, Roles: []string{"user"}}).apiErr(apierr.ValidationFailed)
	if e.Message == "" || n.node(id)["accessSupported"] != false {
		t.Fatalf("旧 agent 不能开官方节点：%v", e)
	}
}
