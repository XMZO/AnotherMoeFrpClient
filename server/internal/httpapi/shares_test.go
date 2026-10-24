package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

const (
	secretToken = "node-secret-token-xyz"
	secretKey   = "p2p-secret-key-abc"
)

// ownerProfile 给 token 对应的用户建一个含两个节点、tcp/http/stcp 代理的配置。
func (e *env) ownerProfile(token string) model.Profile {
	e.t.Helper()
	body := api.CreateProfileRequest{
		Name: "分享源",
		Nodes: []model.Node{
			{ID: "node-a", Remark: "上海", ServerAddr: "a.example.com", ServerPort: 7000, Token: secretToken, User: "owner"},
			{ID: "node-b", Remark: "香港", ServerAddr: "b.example.com", ServerPort: 7000, Token: secretToken},
		},
		Proxies: []model.Proxy{
			{ID: "px-ssh", Name: "ssh", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
			{ID: "px-web", Name: "web", Type: "http", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 80, CustomDomains: []string{"w.example.com"}},
			{ID: "px-rdp", Name: "rdp", Type: "stcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 3389, SecretKey: secretKey, UseEncryption: true},
		},
	}
	var p model.Profile
	e.do(req{method: "POST", path: api.RouteProfiles, token: token, body: body}).expect(http.StatusCreated).decode(&p)
	return p
}

func (e *env) createShare(token string, r api.CreateShareRequest) api.Share {
	e.t.Helper()
	var sh api.Share
	e.do(req{method: "POST", path: api.RouteShares, token: token, body: r}).expect(http.StatusCreated).decode(&sh)
	return sh
}

func TestShareLifecycleAllKinds(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner")
	sub := e.user("subber")
	p := e.ownerProfile(owner)

	// ---- full ----
	full := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "完整分享", Kind: model.ShareFull, ProxyIDs: []string{"px-ssh", "px-web"}})
	if len(full.ID) != 22 || full.Revision != 1 || len(full.Payload.Nodes) != 2 || len(full.Payload.Proxies) != 2 {
		t.Fatalf("full share: %+v", full)
	}
	// 分享者自己能看到快照里的 token
	if full.Payload.Nodes[0].Token != secretToken {
		t.Fatalf("owner should see token")
	}

	// 预览不含任何机密
	pv := e.do(req{method: "GET", path: api.SharePreviewPath(full.ID), token: sub}).expect(http.StatusOK)
	if body := pv.Body.String(); strings.Contains(body, secretToken) || strings.Contains(body, "a.example.com") {
		t.Fatalf("preview leaks secrets: %s", body)
	}
	var preview api.SharePreview
	pv.decode(&preview)
	if preview.OwnerNickname != "owner" || len(preview.Nodes) != 2 || len(preview.Proxies) != 2 || preview.Subscription != nil {
		t.Fatalf("preview: %+v", preview)
	}

	var fs api.SubscriptionSummary
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: full.ID}}).
		expect(http.StatusCreated).decode(&fs)
	if fs.Kind != model.ShareFull || fs.Revision != 1 || fs.ShareName != "完整分享" {
		t.Fatalf("full sub: %+v", fs)
	}
	// 重复订阅
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: full.ID}}).apiErr(apierr.AlreadySubscribed)

	var rt api.SubscriptionRuntime
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(fs.ID), token: sub}).expect(http.StatusOK).decode(&rt)
	if rt.NodeID != "node-a" || len(rt.Data.Proxies) != 2 || rt.Data.Nodes[0].Token != secretToken {
		t.Fatalf("full runtime: %+v", rt)
	}
	// 别人（包括分享者）不能读订阅者的 runtime
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(fs.ID), token: owner}).apiErr(apierr.NotFound)

	// state 里有订阅摘要，且摘要不含机密
	stResp := e.do(req{method: "GET", path: api.RouteState, token: sub}).expect(http.StatusOK)
	if strings.Contains(stResp.Body.String(), secretToken) {
		t.Fatalf("state leaks token")
	}
	var st api.StateResponse
	stResp.decode(&st)
	if len(st.Subscriptions) != 1 || st.Subscriptions[0].ID != fs.ID || len(st.Subscriptions[0].Nodes) != 2 {
		t.Fatalf("state subs: %+v", st.Subscriptions)
	}

	// ---- template ----
	tpl := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "模板", Kind: model.ShareTemplate, NodeIDs: []string{"node-b"}})
	if len(tpl.Payload.Nodes) != 1 || len(tpl.Payload.Proxies) != 0 {
		t.Fatalf("template: %+v", tpl.Payload)
	}
	// 缺节点
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: tpl.ID}}).apiErr(apierr.ValidationFailed)
	var ts api.SubscriptionSummary
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: tpl.ID, Params: model.SubscriptionParams{
		NodeID: "node-b", Proxies: []model.Proxy{{Name: "mine", Type: "tcp", Enabled: true, LocalPort: 8080, RemotePort: 18080}},
	}}}).expect(http.StatusCreated).decode(&ts)
	if len(ts.Params.Proxies) != 1 || ts.Params.Proxies[0].ID == "" {
		t.Fatalf("template params should get IDs: %+v", ts.Params)
	}
	// 修改参数（revision 冲突 + 正常）
	upd := api.UpdateSubscriptionRequest{BaseRevision: 9, Params: ts.Params}
	e.do(req{method: "PUT", path: api.SubscriptionPath(ts.ID), token: sub, body: upd}).apiErr(apierr.RevisionConflict)
	upd.BaseRevision = 1
	upd.Params.Proxies[0].LocalPort = 9090
	var ts2 api.SubscriptionSummary
	e.do(req{method: "PUT", path: api.SubscriptionPath(ts.ID), token: sub, body: upd}).expect(http.StatusOK).decode(&ts2)
	if ts2.Revision != 2 || ts2.Params.Proxies[0].LocalPort != 9090 {
		t.Fatalf("updated: %+v", ts2)
	}
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(ts.ID), token: sub}).expect(http.StatusOK).decode(&rt)
	if rt.NodeID != "node-b" || len(rt.Data.Nodes) != 1 || rt.Data.Proxies[0].LocalPort != 9090 {
		t.Fatalf("template runtime: %+v", rt)
	}

	// ---- p2p ----
	e.do(req{method: "POST", path: api.RouteShares, token: owner, body: api.CreateShareRequest{ProfileID: p.ID, Name: "p2p", Kind: model.ShareP2P, NodeIDs: []string{"node-a"}, ProxyIDs: []string{"px-ssh"}}}).
		apiErr(apierr.ValidationFailed) // tcp 不能做 p2p 目标
	p2p := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "远程桌面", Kind: model.ShareP2P, NodeIDs: []string{"node-a"}, ProxyIDs: []string{"px-rdp"}})
	// 目标代理的加密设置跟着快照走，订阅者的访客照着设
	if len(p2p.Payload.Targets) != 1 || p2p.Payload.Targets[0].ServerUser != "owner" || !p2p.Payload.Targets[0].UseEncryption {
		t.Fatalf("p2p payload: %+v", p2p.Payload)
	}
	pv = e.do(req{method: "GET", path: api.SharePreviewPath(p2p.ID), token: sub}).expect(http.StatusOK)
	if strings.Contains(pv.Body.String(), secretKey) {
		t.Fatalf("p2p preview leaks secretKey")
	}
	var ps api.SubscriptionSummary
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: p2p.ID, Params: model.SubscriptionParams{
		Binds: []model.P2PBind{{TargetID: "px-rdp", Enabled: true, BindPort: 13389}},
	}}}).expect(http.StatusCreated).decode(&ps)
	if ps.Params.Binds[0].Name != "p2p-rdp" || ps.Params.Binds[0].BindAddr != "127.0.0.1" {
		t.Fatalf("p2p defaults: %+v", ps.Params)
	}
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(ps.ID), token: sub}).expect(http.StatusOK).decode(&rt)
	if len(rt.Data.Visitors) != 1 || rt.Data.Visitors[0].SecretKey != secretKey || rt.Data.Visitors[0].ServerName != "rdp" || rt.Data.Visitors[0].ServerUser != "owner" {
		t.Fatalf("p2p runtime: %+v", rt.Data)
	}

	// ---- 我的分享 ----
	var list api.ListSharesResponse
	e.do(req{method: "GET", path: api.RouteShares, token: owner}).expect(http.StatusOK).decode(&list)
	if len(list.Shares) != 3 {
		t.Fatalf("list: %d", len(list.Shares))
	}
	for _, s := range list.Shares {
		if s.Subscribers != 1 {
			t.Errorf("share %s subscribers = %d", s.Name, s.Subscribers)
		}
	}
	// 别人看不到也改不了
	e.do(req{method: "DELETE", path: api.SharePath(full.ID), token: sub}).apiErr(apierr.NotFound)

	// ---- 撤销 ----
	before := e.state(sub).StateVersion
	e.do(req{method: "DELETE", path: api.SharePath(full.ID), token: owner}).expect(http.StatusNoContent)
	e.do(req{method: "DELETE", path: api.SharePath(full.ID), token: owner}).expect(http.StatusNoContent) // 幂等
	st = e.state(sub)
	if st.StateVersion <= before {
		t.Fatalf("revoke should bump subscriber stateVersion")
	}
	for _, s := range st.Subscriptions {
		if s.ID == fs.ID && !s.Revoked {
			t.Fatalf("subscription should show revoked")
		}
	}
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(fs.ID), token: sub}).apiErr(apierr.ShareRevoked)
	e.do(req{method: "GET", path: api.SharePreviewPath(full.ID), token: sub}).apiErr(apierr.ShareRevoked)
	// 已撤销的订阅仍可删除
	e.do(req{method: "DELETE", path: api.SubscriptionPath(fs.ID), token: sub}).expect(http.StatusNoContent)
	e.do(req{method: "DELETE", path: api.SubscriptionPath(fs.ID), token: sub}).apiErr(apierr.NotFound)

	// 日志里不能有 token 和 secretKey
	if logs := e.logs.String(); strings.Contains(logs, secretToken) || strings.Contains(logs, secretKey) {
		t.Fatalf("logs leak secrets")
	}
}

func TestShareRepublish(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner")
	sub := e.user("subber")
	p := e.ownerProfile(owner)
	sh := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "完整", Kind: model.ShareFull, NodeIDs: []string{"node-a"}, ProxyIDs: []string{"px-ssh"}})
	var s api.SubscriptionSummary
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: sh.ID}}).expect(http.StatusCreated).decode(&s)

	// 改来源配置：改 ssh 远程端口，删掉 node-a 以外的节点不影响
	p.Proxies[0].RemotePort = 7022
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: owner, body: api.UpdateProfileRequest{
		BaseRevision: p.Revision, Name: p.Name, Nodes: p.Nodes, Proxies: p.Proxies, Visitors: p.Visitors,
	}}).expect(http.StatusOK).decode(&p)

	var rt api.SubscriptionRuntime
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(s.ID), token: sub}).expect(http.StatusOK).decode(&rt)
	if rt.Data.Proxies[0].RemotePort != 6022 {
		t.Fatalf("snapshot should not change before republish")
	}
	var re api.Share
	e.do(req{method: "POST", path: api.ShareRepublishPath(sh.ID), token: owner}).expect(http.StatusOK).decode(&re)
	if re.Revision != 2 || len(re.Payload.Proxies) != 1 || re.Payload.Proxies[0].RemotePort != 7022 {
		t.Fatalf("republish: %+v", re)
	}
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(s.ID), token: sub}).expect(http.StatusOK).decode(&rt)
	if rt.ShareRevision != 2 || rt.Data.Proxies[0].RemotePort != 7022 {
		t.Fatalf("runtime after republish: %+v", rt)
	}

	// 来源配置删除后不能重新发布
	e.do(req{method: "DELETE", path: api.ProfilePath(p.ID), token: owner}).expect(http.StatusNoContent)
	e.do(req{method: "POST", path: api.ShareRepublishPath(sh.ID), token: owner}).apiErr(apierr.ValidationFailed)
	// 但已有订阅照样能用
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(s.ID), token: sub}).expect(http.StatusOK)
}

func TestShareExpiryAndUpdate(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner")
	sub := e.user("subber")
	p := e.ownerProfile(owner)
	past := e.clock.Now().Add(-time.Hour)
	e.do(req{method: "POST", path: api.RouteShares, token: owner, body: api.CreateShareRequest{ProfileID: p.ID, Name: "x", Kind: model.ShareFull, ExpiresAt: &past}}).
		apiErr(apierr.ValidationFailed)

	exp := e.clock.Now().Add(time.Hour)
	sh := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "限时", Kind: model.ShareFull, ExpiresAt: &exp})
	var s api.SubscriptionSummary
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: sub, body: api.CreateSubscriptionRequest{ShareID: sh.ID}}).expect(http.StatusCreated).decode(&s)

	e.clock.Add(2 * time.Hour)
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(s.ID), token: sub}).apiErr(apierr.ShareExpired)
	if st := e.state(sub); !st.Subscriptions[0].Expired {
		t.Fatalf("state should show expired")
	}

	// 分享者延期 → 恢复
	name := "改名了"
	var up api.Share
	e.do(req{method: "PATCH", path: api.SharePath(sh.ID), token: owner, body: api.UpdateShareRequest{Name: &name, ClearExpiry: true}}).
		expect(http.StatusOK).decode(&up)
	if up.Name != name || up.ExpiresAt != nil || up.Expired {
		t.Fatalf("update: %+v", up)
	}
	e.do(req{method: "GET", path: api.SubscriptionRuntimePath(s.ID), token: sub}).expect(http.StatusOK)
	if st := e.state(sub); st.Subscriptions[0].ShareName != name {
		t.Fatalf("subscriber should see new name")
	}
}

func TestShareMaxSubscribersConcurrent(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner")
	p := e.ownerProfile(owner)
	sh := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "名额", Kind: model.ShareFull, MaxSubscribers: 3})

	const n = 12
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = e.user("u" + string(rune('a'+i)) + "xx")
	}
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := e.do(req{method: "POST", path: api.RouteSubscriptions, token: tokens[i], body: api.CreateSubscriptionRequest{ShareID: sh.ID}})
			codes[i] = r.Code
		}(i)
	}
	wg.Wait()
	ok, full := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			ok++
		case apierr.ShareFull.Status():
			full++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 3 || full != n-3 {
		t.Fatalf("ok=%d full=%d", ok, full)
	}
	var list api.ListSharesResponse
	e.do(req{method: "GET", path: api.RouteShares, token: owner}).expect(http.StatusOK).decode(&list)
	if list.Shares[0].Subscribers != 3 {
		t.Fatalf("subscribers = %d", list.Shares[0].Subscribers)
	}
}

func TestShareValidationAndAccess(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner")
	other := e.user("other")
	p := e.ownerProfile(owner)

	cases := []api.CreateShareRequest{
		{ProfileID: p.ID, Name: "", Kind: model.ShareFull},
		{ProfileID: p.ID, Name: "x", Kind: "weird"},
		{ProfileID: p.ID, Name: "x", Kind: model.ShareTemplate},                                                            // 没选节点
		{ProfileID: p.ID, Name: "x", Kind: model.ShareTemplate, NodeIDs: []string{"node-a"}, ProxyIDs: []string{"px-ssh"}}, // 模板带代理
		{ProfileID: p.ID, Name: "x", Kind: model.ShareFull, ProxyIDs: []string{"nope"}},
		{ProfileID: p.ID, Name: "x", Kind: model.ShareP2P, NodeIDs: []string{"node-a", "node-b"}, ProxyIDs: []string{"px-rdp"}},
		{ProfileID: p.ID, Name: "x", Kind: model.ShareFull, MaxSubscribers: -1},
	}
	for i, c := range cases {
		r := e.do(req{method: "POST", path: api.RouteShares, token: owner, body: c})
		if r.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d %s", i, r.Code, r.Body.String())
		}
	}
	// 不能分享别人的配置
	e.do(req{method: "POST", path: api.RouteShares, token: other, body: api.CreateShareRequest{ProfileID: p.ID, Name: "x", Kind: model.ShareFull}}).
		apiErr(apierr.NotFound)
	// 不存在的分享
	e.do(req{method: "GET", path: api.SharePreviewPath("doesNotExist"), token: other}).apiErr(apierr.NotFound)
	e.do(req{method: "POST", path: api.RouteSubscriptions, token: other, body: api.CreateSubscriptionRequest{ShareID: "doesNotExist"}}).apiErr(apierr.NotFound)
	// 需要登录
	e.do(req{method: "GET", path: api.RouteShares}).apiErr(apierr.Unauthorized)
}

func TestSharePreviewRateLimit(t *testing.T) {
	limits := noLimits
	limits.SharePreview.Limit, limits.SharePreview.Window = 2, time.Minute
	e := newEnv(t, withLimits(limits))
	owner := e.user("owner")
	p := e.ownerProfile(owner)
	sh := e.createShare(owner, api.CreateShareRequest{ProfileID: p.ID, Name: "x", Kind: model.ShareFull})
	for range 2 {
		e.do(req{method: "GET", path: api.SharePreviewPath(sh.ID), token: owner}).expect(http.StatusOK)
	}
	e.do(req{method: "GET", path: api.SharePreviewPath(sh.ID), token: owner}).apiErr(apierr.RateLimited)
}
