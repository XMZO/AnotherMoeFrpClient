package cloudsync

import (
	"testing"

	"moefrp/client/internal/engine"
	"moefrp/shared/api"
	"moefrp/shared/model"
)

func subFixture(shareRev int64, remotePort int) (api.SubscriptionSummary, api.SubscriptionRuntime) {
	sum := api.SubscriptionSummary{ID: "sub1", ShareID: "sh1", ShareName: "朋友的节点", Kind: model.ShareFull,
		OwnerNickname: "bob", Revision: 1, ShareRevision: shareRev}
	rt := api.SubscriptionRuntime{SubscriptionID: "sub1", ShareID: "sh1", Kind: model.ShareFull, Revision: 1, ShareRevision: shareRev, NodeID: "n1",
		Data: model.ProfileData{
			Nodes:   []model.Node{{ID: "n1", Remark: "节点", ServerAddr: "frp.example.com", ServerPort: 7000, Token: "secret"}},
			Proxies: []model.Proxy{{ID: "p1", Name: "ssh", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: remotePort}},
		}}
	return sum, rt
}

func TestSubscriptionRuntimeCachedAndRefreshed(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	sum, rt := subFixture(1, 6000)
	srv.setSub(sum, rt)

	a := newDevice(t, srv, "")
	a.login(t, false)
	c := mustGet(t, a.ps, "sub1")
	if !c.IsSubscription() || c.Profile.Name != "朋友的节点" || len(c.Profile.Proxies) != 1 || c.Profile.Nodes[0].Token != "secret" || c.Sub.DefaultNodeID != "n1" {
		t.Fatalf("cached subscription: %+v", c)
	}
	calls := srv.runtimeCalls

	// 没有变化：不再拉 runtime
	srv.mu.Lock()
	srv.bump()
	srv.mu.Unlock()
	a.syncNow(t)
	if srv.runtimeCalls != calls {
		t.Fatalf("runtime refetched without change")
	}

	// 分享者重新发布，订阅正在运行：缓存更新，并询问是否应用
	a.eng.mu.Lock()
	a.eng.running = "sub1"
	a.eng.mu.Unlock()
	sum2, rt2 := subFixture(2, 7000)
	srv.setSub(sum2, rt2)
	a.syncNow(t)
	if got := mustGet(t, a.ps, "sub1"); got.Profile.Proxies[0].RemotePort != 7000 || got.Sub.RuntimeShareRevision != 2 {
		t.Fatalf("not refreshed: %+v", got)
	}
	if ru := a.m.RemoteUpdates(); len(ru) != 1 || ru[0].ProfileID != "sub1" || ru[0].Mode != engine.UpdateHot {
		t.Fatalf("remote updates: %+v", ru)
	}

	// 撤销：停止隧道、清空内容、保留条目
	sum3 := sum2
	sum3.Revoked, sum3.ShareRevision = true, 3
	srv.setSub(sum3, api.SubscriptionRuntime{})
	a.syncNow(t)
	got := mustGet(t, a.ps, "sub1")
	if !got.Sub.Summary.Revoked || len(got.Profile.Nodes) != 0 || len(got.Profile.Proxies) != 0 {
		t.Fatalf("revoked entry should be emptied: %+v", got)
	}
	if a.eng.stopped != 1 {
		t.Fatalf("running revoked subscription should be stopped")
	}

	// 云端删掉订阅：条目消失
	srv.removeSub("sub1")
	a.syncNow(t)
	if _, ok := a.ps.Get("sub1"); ok {
		t.Fatal("subscription should be removed")
	}
}

func TestSubscriptionsClearedOnLogout(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	sum, rt := subFixture(1, 6000)
	srv.setSub(sum, rt)
	a := newDevice(t, srv, "")
	a.login(t, false)
	mustGet(t, a.ps, "sub1")
	if err := a.m.Logout(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.ps.Get("sub1"); ok {
		t.Fatal("subscription cache must be cleared on logout")
	}
}
