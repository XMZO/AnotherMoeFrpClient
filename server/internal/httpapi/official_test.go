package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
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

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestOfficialNodes(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("东京")
	en := n.enroll(code)
	fa, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.1.0", OS: "linux", Arch: "amd64",
		Features: []string{agentproto.FeatureAccess}})
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
	vip := "vip"
	n.admin("PATCH", "/users/bob", updateUserRequest{Role: &vip}).expect(http.StatusNoContent)

	official := func(nick, tok string) []api.OfficialNode {
		t.Helper()
		var out api.OfficialNodesResponse
		n.do(req{method: "GET", path: api.RouteOfficialNodes, token: tok}).expect(http.StatusOK).decode(&out)
		return out.Nodes
	}
	if len(official("alice", aliceTok)) != 0 {
		t.Fatal("没开官方节点时不下发")
	}

	// 只开放给 VIP
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: true, Roles: []string{"vip"}, Description: "VIP 专用"}).expect(http.StatusNoContent)
	eventually(t, "VIP 可用", func() bool { u := a.users(); _, ok := u["bob"]; return a.enabled() && ok && len(u) == 1 })
	if len(official("alice", aliceTok)) != 0 {
		t.Fatal("普通用户不能用 VIP 节点")
	}
	bobNodes := official("bob", bobTok)
	if len(bobNodes) != 1 {
		t.Fatalf("bob: %+v", bobNodes)
	}
	b := bobNodes[0]
	if b.ServerAddr != "tokyo.example.com" || b.ServerPort != 7000 || b.Token == "" || b.User != "bob" || b.Description != "VIP 专用" ||
		!b.Online || b.Quota.MaxProxies != 20 {
		t.Fatalf("下发的节点：%+v", b)
	}
	if a.users()["bob"].CredHash != sha(b.Credential) {
		t.Fatal("节点上的凭证哈希应和下发给客户端的凭证对应")
	}
	if nd := b.ToNode(); nd.ID != api.OfficialNodePrefix+id || nd.Metadatas[api.MetaCredential] != b.Credential {
		t.Fatalf("ToNode：%+v", nd)
	}

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
	bad.VIP.Ports = "70000"
	n.admin("PUT", "/quotas", bad).apiErr(apierr.ValidationFailed)

	// 重置凭证：旧凭证在节点上失效
	old := a.users()["alice"].CredHash
	n.admin("POST", "/users/alice/reset-node-credential", nil).expect(http.StatusNoContent)
	eventually(t, "凭证换代", func() bool { u := a.users()["alice"]; return u.CredHash != "" && u.CredHash != old })
	if nodes := official("alice", aliceTok); sha(nodes[0].Credential) != a.users()["alice"].CredHash {
		t.Fatal("客户端拿到的是新凭证")
	}

	// 停用用户：从表里去掉
	n.admin("PATCH", "/users/alice", updateUserRequest{Disabled: &yes}).expect(http.StatusNoContent)
	eventually(t, "停用后撤销", func() bool { _, ok := a.users()["alice"]; return !ok })

	// 关闭官方节点
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: false, Roles: []string{"vip"}}).expect(http.StatusNoContent)
	eventually(t, "关闭", func() bool { return !a.enabled() })
	if len(official("bob", bobTok)) != 0 {
		t.Fatal("关闭后不下发")
	}
	n.admin("PUT", "/nodes/"+id+"/official", nodeOfficialRequest{Official: true, Roles: []string{"god"}}).apiErr(apierr.ValidationFailed)
}

func TestOfficialNeedsNewAgent(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("旧节点")
	en := n.enroll(code)
	fa, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.0.0", OS: "linux", Arch: "amd64"})
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
