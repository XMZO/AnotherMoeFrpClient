package validate

import (
	"testing"

	"moefrp/shared/model"
)

func shareNode() model.Node {
	return model.Node{ID: "n1", Remark: "节点", ServerAddr: "frp.example.com", ServerPort: 7000, Token: "t"}
}

func TestSharePayload(t *testing.T) {
	node := shareNode()
	proxy := model.Proxy{ID: "p1", Name: "ssh", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6000}
	target := model.P2PTarget{ID: "t1", ServerName: "rdp", Type: "stcp", SecretKey: "sk"}

	ok := []struct {
		kind string
		p    model.SharePayload
	}{
		{model.ShareTemplate, model.SharePayload{Nodes: []model.Node{node}}},
		{model.ShareFull, model.SharePayload{Nodes: []model.Node{node}, Proxies: []model.Proxy{proxy}}},
		{model.ShareP2P, model.SharePayload{Nodes: []model.Node{node}, Targets: []model.P2PTarget{target}}},
	}
	for _, c := range ok {
		if err := SharePayload(c.kind, c.p); err != nil {
			t.Errorf("%s: %v", c.kind, err)
		}
	}

	node2 := node
	node2.ID = "n2"
	bad := []struct {
		name, kind, field string
		p                 model.SharePayload
	}{
		{"无节点", model.ShareTemplate, "payload.nodes", model.SharePayload{}},
		{"模板带代理", model.ShareTemplate, "payload.proxies", model.SharePayload{Nodes: []model.Node{node}, Proxies: []model.Proxy{proxy}}},
		{"完整无代理", model.ShareFull, "payload.proxies", model.SharePayload{Nodes: []model.Node{node}}},
		{"p2p 两个节点", model.ShareP2P, "payload.nodes", model.SharePayload{Nodes: []model.Node{node, node2}, Targets: []model.P2PTarget{target}}},
		{"p2p 无目标", model.ShareP2P, "payload.targets", model.SharePayload{Nodes: []model.Node{node}}},
		{"p2p 目标是 tcp", model.ShareP2P, "payload.targets[0].type", model.SharePayload{Nodes: []model.Node{node}, Targets: []model.P2PTarget{{ID: "t1", ServerName: "x", Type: "tcp", SecretKey: "s"}}}},
		{"p2p 目标无密钥", model.ShareP2P, "payload.targets[0].secretKey", model.SharePayload{Nodes: []model.Node{node}, Targets: []model.P2PTarget{{ID: "t1", ServerName: "x", Type: "stcp"}}}},
		{"未知类型", "foo", "kind", model.SharePayload{Nodes: []model.Node{node}}},
	}
	for _, c := range bad {
		err := SharePayload(c.kind, c.p)
		es, _ := err.(Errors)
		if !es.Has(c.field, "") {
			t.Errorf("%s: want error on %s, got %v", c.name, c.field, err)
		}
	}
}

func TestSubscriptionParams(t *testing.T) {
	payload := model.SharePayload{
		Nodes:   []model.Node{shareNode()},
		Targets: []model.P2PTarget{{ID: "t1", ServerName: "rdp", Type: "stcp", SecretKey: "sk"}, {ID: "t2", ServerName: "ssh", Type: "xtcp", SecretKey: "sk"}},
	}
	proxy := model.Proxy{ID: "p1", Name: "web", Type: "tcp", Enabled: true, LocalPort: 80}

	if err := SubscriptionParams(model.ShareTemplate, payload, model.SubscriptionParams{NodeID: "n1", Proxies: []model.Proxy{proxy}}); err != nil {
		t.Errorf("template ok: %v", err)
	}
	if err := SubscriptionParams(model.ShareFull, payload, model.SubscriptionParams{}); err != nil {
		t.Errorf("full ok: %v", err)
	}
	binds := []model.P2PBind{{ID: "b1", TargetID: "t1", Name: "p2p-rdp", Enabled: true, BindPort: 13389}}
	if err := SubscriptionParams(model.ShareP2P, payload, model.SubscriptionParams{Binds: binds}); err != nil {
		t.Errorf("p2p ok: %v", err)
	}

	cases := []struct {
		name, kind, field string
		p                 model.SubscriptionParams
	}{
		{"模板缺节点", model.ShareTemplate, "params.nodeId", model.SubscriptionParams{}},
		{"模板节点不存在", model.ShareTemplate, "params.nodeId", model.SubscriptionParams{NodeID: "zz"}},
		{"模板代理重名", model.ShareTemplate, "params.proxies[1].name", model.SubscriptionParams{NodeID: "n1", Proxies: []model.Proxy{proxy, func() model.Proxy { p := proxy; p.ID = "p2"; return p }()}}},
		{"完整自定义代理", model.ShareFull, "params.proxies", model.SubscriptionParams{Proxies: []model.Proxy{proxy}}},
		{"p2p 无绑定", model.ShareP2P, "params.binds", model.SubscriptionParams{}},
		{"p2p 目标不存在", model.ShareP2P, "params.binds[0].targetId", model.SubscriptionParams{Binds: []model.P2PBind{{ID: "b1", TargetID: "x", Name: "a", BindPort: 1}}}},
		{"p2p 重复目标", model.ShareP2P, "params.binds[1].targetId", model.SubscriptionParams{Binds: []model.P2PBind{
			{ID: "b1", TargetID: "t1", Name: "a", BindPort: 1}, {ID: "b2", TargetID: "t1", Name: "b", BindPort: 2}}}},
		{"p2p 端口冲突", model.ShareP2P, "params.binds[1].bindPort", model.SubscriptionParams{Binds: []model.P2PBind{
			{ID: "b1", TargetID: "t1", Name: "a", BindPort: 5000}, {ID: "b2", TargetID: "t2", Name: "b", BindPort: 5000}}}},
	}
	for _, c := range cases {
		err := SubscriptionParams(c.kind, payload, c.p)
		es, _ := err.(Errors)
		if !es.Has(c.field, "") {
			t.Errorf("%s: want error on %s, got %v", c.name, c.field, err)
		}
	}
}

func TestBuildRuntime(t *testing.T) {
	node := shareNode()
	payload := model.SharePayload{
		Nodes:   []model.Node{node},
		Proxies: []model.Proxy{{ID: "p1", Name: "ssh", Type: "tcp", Enabled: true, LocalPort: 22}},
		Targets: []model.P2PTarget{{ID: "t1", ServerName: "rdp", Type: "stcp", SecretKey: "sk", ServerUser: "owner"}},
	}
	d, nodeID := model.BuildRuntime(model.ShareFull, payload, model.SubscriptionParams{})
	if nodeID != "n1" || len(d.Proxies) != 1 || len(d.Visitors) != 0 {
		t.Errorf("full: %v %+v", nodeID, d)
	}
	d, _ = model.BuildRuntime(model.ShareP2P, payload, model.SubscriptionParams{Binds: []model.P2PBind{{ID: "b1", TargetID: "t1", Name: "v", Enabled: true, BindPort: 9000}}})
	if len(d.Visitors) != 1 || d.Visitors[0].SecretKey != "sk" || d.Visitors[0].ServerUser != "owner" || d.Visitors[0].BindAddr != "127.0.0.1" || len(d.Proxies) != 0 {
		t.Errorf("p2p: %+v", d)
	}
	if err := ProfileData(d); err != nil {
		t.Errorf("p2p runtime invalid: %v", err)
	}
	d, _ = model.BuildRuntime(model.ShareTemplate, payload, model.SubscriptionParams{NodeID: "n1", Proxies: []model.Proxy{{ID: "x", Name: "mine", Type: "tcp", LocalPort: 1}}})
	if len(d.Proxies) != 1 || d.Proxies[0].Name != "mine" {
		t.Errorf("template: %+v", d)
	}
}
