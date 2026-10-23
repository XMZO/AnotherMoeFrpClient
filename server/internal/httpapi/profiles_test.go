package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

func sampleCreate() api.CreateProfileRequest {
	return api.CreateProfileRequest{
		Name:  "家里",
		Nodes: []model.Node{{Remark: "香港", ServerAddr: "frp.example.com", ServerPort: 7000, Token: "node-secret-token"}},
		Proxies: []model.Proxy{
			{Name: "ssh", Type: model.ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
			{Name: "web", Type: model.ProxyHTTP, Enabled: true, LocalPort: 80, CustomDomains: []string{"a.example.com"}},
		},
		Visitors: []model.Visitor{{Name: "rdp", Type: model.VisitorSTCP, Enabled: true, ServerName: "rdp", SecretKey: "sk", BindPort: 13389}},
	}
}

func (e *env) createProfile(tok string, body any) model.Profile {
	e.t.Helper()
	var p model.Profile
	e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: body}).expect(http.StatusCreated).decode(&p)
	return p
}

func (e *env) state(tok string) api.StateResponse {
	e.t.Helper()
	var s api.StateResponse
	e.do(req{method: "GET", path: api.RouteState, token: tok}).expect(http.StatusOK).decode(&s)
	return s
}

func updateReq(p model.Profile, base int64) api.UpdateProfileRequest {
	return api.UpdateProfileRequest{BaseRevision: base, Name: p.Name, Nodes: p.Nodes, Proxies: p.Proxies, Visitors: p.Visitors}
}

func TestProfileCRUD(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")

	r := e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: sampleCreate()}).expect(http.StatusCreated)
	var p model.Profile
	r.decode(&p)
	if p.ID == "" || p.Revision != 1 || p.Name != "家里" || p.UpdatedAt.IsZero() {
		t.Fatalf("新建结果不对：%+v", p)
	}
	if r.Header().Get("Location") != api.ProfilePath(p.ID) {
		t.Fatalf("Location 头不对：%s", r.Header().Get("Location"))
	}
	// 缺 ID 的节点、代理、访客由服务端补上
	if p.Nodes[0].ID == "" || p.Proxies[0].ID == "" || p.Proxies[1].ID == "" || p.Visitors[0].ID == "" {
		t.Fatalf("应补上稳定 ID：%+v", p)
	}
	if p.Nodes[0].Token != "node-secret-token" {
		t.Fatal("节点 token 应原样保存（只返回给本人）")
	}

	var got model.Profile
	e.do(req{method: "GET", path: api.ProfilePath(p.ID), token: tok}).expect(http.StatusOK).decode(&got)
	if got.ID != p.ID || got.Proxies[1].CustomDomains[0] != "a.example.com" || got.Revision != 1 {
		t.Fatalf("读取结果不对：%+v", got)
	}

	// 修改：带正确的 baseRevision
	got.Name = "家里 & 公司" // 存储时不做 HTML 转义
	got.Proxies[0].LocalPort = 2222
	var upd model.Profile
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(got, 1)}).expect(http.StatusOK).decode(&upd)
	if upd.Revision != 2 || upd.Name != "家里 & 公司" || upd.Proxies[0].LocalPort != 2222 || upd.Proxies[0].ID != p.Proxies[0].ID {
		t.Fatalf("修改结果不对：%+v", upd)
	}

	e.do(req{method: "DELETE", path: api.ProfilePath(p.ID), token: tok}).expect(http.StatusNoContent)
	e.do(req{method: "GET", path: api.ProfilePath(p.ID), token: tok}).apiErr(apierr.NotFound)
	e.do(req{method: "DELETE", path: api.ProfilePath(p.ID), token: tok}).apiErr(apierr.NotFound)
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(got, 2)}).apiErr(apierr.NotFound)

	// 需要登录
	e.do(req{method: "GET", path: api.ProfilePath(p.ID)}).apiErr(apierr.Unauthorized)
	e.do(req{method: "POST", path: api.RouteProfiles, body: sampleCreate()}).apiErr(apierr.Unauthorized)
}

// 版本冲突：两台设备基于同一个版本修改，后提交的收到 409 和云端当前版本（PLAN 6.6）。
func TestProfileRevisionConflict(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	p := e.createProfile(tok, sampleCreate())

	deviceA := p
	deviceA.Name = "A 改的"
	deviceB := p
	deviceB.Name = "B 改的"

	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(deviceA, 1)}).expect(http.StatusOK)
	er := e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(deviceB, 1)}).apiErr(apierr.RevisionConflict)

	raw, _ := json.Marshal(er.Details[apierr.DetailCurrent])
	var current model.Profile
	if err := json.Unmarshal(raw, &current); err != nil {
		t.Fatal(err)
	}
	if current.Revision != 2 || current.Name != "A 改的" || len(current.Proxies) != 2 {
		t.Fatalf("details.current 应是云端当前的完整配置：%+v", current)
	}
	// B 选择"保留本机"：基于云端最新版本重新提交
	var upd model.Profile
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(deviceB, current.Revision)}).expect(http.StatusOK).decode(&upd)
	if upd.Revision != 3 || upd.Name != "B 改的" {
		t.Fatalf("解决冲突后的结果不对：%+v", upd)
	}
	// baseRevision 必填
	er = e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(deviceB, 0)}).apiErr(apierr.ValidationFailed)
	if !hasField(er, "baseRevision") {
		t.Fatalf("应报 baseRevision：%+v", er.Details)
	}
	// 未来的版本号同样是冲突
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(deviceB, 99)}).apiErr(apierr.RevisionConflict)
}

// PUT 是整体覆盖：漏传 nodes / proxies / visitors 不能被当成清空（PLAN 7.7：缺字段返回 400）。
func TestProfileUpdateRequiresAllFields(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	p := e.createProfile(tok, sampleCreate())
	er := e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: `{"baseRevision":1,"name":"x"}`}).apiErr(apierr.ValidationFailed)
	for _, f := range []string{"nodes", "proxies", "visitors"} {
		if !hasField(er, f) {
			t.Errorf("应报缺少 %s：%+v", f, fields(er))
		}
	}
	er = e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: `{"baseRevision":1,"name":"x","nodes":null,"proxies":[],"visitors":[]}`}).apiErr(apierr.ValidationFailed)
	if !hasField(er, "nodes") || hasField(er, "proxies") {
		t.Errorf("null 等同于缺少：%+v", fields(er))
	}
	er = e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: `{"name":"x","nodes":[],"proxies":[],"visitors":[]}`}).apiErr(apierr.ValidationFailed)
	if !hasField(er, "baseRevision") {
		t.Errorf("应报缺少 baseRevision：%+v", fields(er))
	}
	var got model.Profile
	e.do(req{method: "GET", path: api.ProfilePath(p.ID), token: tok}).decode(&got)
	if got.Revision != 1 || len(got.Nodes) != 1 || len(got.Proxies) != 2 {
		t.Fatalf("失败的 PUT 不能改动配置：%+v", got)
	}
	// 显式传空数组才是清空
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: `{"baseRevision":1,"name":"x","nodes":[],"proxies":[],"visitors":[]}`}).expect(http.StatusOK)
	// 新建时数组可以省略，表示空配置
	e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: `{"name":"empty"}`}).expect(http.StatusCreated)
}

// 并发提交同一个版本：只有一个成功，其余都是 409。
func TestProfileConcurrentUpdate(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	p := e.createProfile(tok, sampleCreate())
	const n = 10
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := p
			q.Name = fmt.Sprintf("设备 %d", i)
			codes[i] = e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(q, 1)}).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("意外的状态码：%v", codes)
		}
	}
	if ok != 1 {
		t.Fatalf("应恰好 1 个成功：%v", codes)
	}
	var got model.Profile
	e.do(req{method: "GET", path: api.ProfilePath(p.ID), token: tok}).decode(&got)
	if got.Revision != 2 {
		t.Fatalf("revision 应为 2：%d", got.Revision)
	}
}

func TestProfileIsolation(t *testing.T) {
	e := newEnv(t, withConfigSingle(false))
	alice := e.user("alice")
	bob := e.user("bob")
	p := e.createProfile(alice, sampleCreate())
	e.do(req{method: "GET", path: api.ProfilePath(p.ID), token: bob}).apiErr(apierr.NotFound)
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: bob, body: updateReq(p, 1)}).apiErr(apierr.NotFound)
	e.do(req{method: "DELETE", path: api.ProfilePath(p.ID), token: bob}).apiErr(apierr.NotFound)
	if s := e.state(bob); len(s.Profiles) != 0 {
		t.Fatal("bob 不应看到 alice 的配置")
	}
	// bob 用 alice 的配置 ID 新建：拒绝，且不泄露内容
	body := sampleCreate()
	body.ID = p.ID
	er := e.do(req{method: "POST", path: api.RouteProfiles, token: bob, body: body}).apiErr(apierr.ValidationFailed)
	if er.Details[apierr.DetailCurrent] != nil || !hasField(er, "id") {
		t.Fatalf("不能泄露别人的配置：%+v", er)
	}
}

func withConfigSingle(single bool) envOpt {
	return func(o *envOptions) { o.cfg.SingleSession = single }
}

func TestProfileClientSuppliedID(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	body := sampleCreate()
	body.ID = model.NewID()
	body.Proxies[0].ID = "client-proxy-1"
	p := e.createProfile(tok, body)
	if p.ID != body.ID || p.Proxies[0].ID != "client-proxy-1" {
		t.Fatalf("应使用客户端给的 ID：%+v", p)
	}
	// 重试同一个创建请求（响应丢失的情况）：409，details.current 是云端版本
	er := e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: body}).apiErr(apierr.RevisionConflict)
	if er.Details[apierr.DetailCurrent] == nil {
		t.Fatal("应带上云端版本")
	}
	body.ID = "bad id"
	e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: body}).apiErr(apierr.ValidationFailed)
}

func TestProfileValidation(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	cases := map[string]func(b *api.CreateProfileRequest) string{
		"代理重名": func(b *api.CreateProfileRequest) string {
			b.Proxies[1].Name = "ssh"
			return "proxies[1].name"
		},
		"代理与访客重名": func(b *api.CreateProfileRequest) string {
			b.Visitors[0].Name = "web"
			return "visitors[0].name"
		},
		"端口超范围": func(b *api.CreateProfileRequest) string {
			b.Proxies[0].RemotePort = 70000
			return "proxies[0].remotePort"
		},
		"http 缺域名": func(b *api.CreateProfileRequest) string {
			b.Proxies[1].CustomDomains = nil
			return "proxies[1].customDomains"
		},
		"stcp 缺密钥": func(b *api.CreateProfileRequest) string {
			b.Visitors[0].SecretKey = ""
			return "visitors[0].secretKey"
		},
		"空名称": func(b *api.CreateProfileRequest) string {
			b.Name = " "
			return "name"
		},
		"节点过多": func(b *api.CreateProfileRequest) string {
			for i := 0; i < validate.MaxNodesPerProfile; i++ {
				b.Nodes = append(b.Nodes, b.Nodes[0])
			}
			return "nodes"
		},
	}
	for name, mutate := range cases {
		b := sampleCreate()
		field := mutate(&b)
		er := e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: b}).apiErr(apierr.ValidationFailed)
		if !hasField(er, field) {
			t.Errorf("%s：应报 %s：%+v", name, field, fields(er))
		}
	}
	// 22、80、443 这类 1024 以下的端口可以填（旧版的 bug）
	b := sampleCreate()
	b.Proxies[0].LocalPort, b.Proxies[0].RemotePort = 80, 443
	e.createProfile(tok, b)
	// 未知字段（例如旧版的 sk、local_ip）被拒绝
	er := e.do(req{method: "POST", path: api.RouteProfiles, token: tok,
		body: `{"name":"x","nodes":[],"proxies":[{"name":"a","type":"stcp","sk":"1"}],"visitors":[]}`}).apiErr(apierr.ValidationFailed)
	if !hasField(er, "sk") {
		t.Fatalf("应报未知字段 sk：%+v", fields(er))
	}
}

func TestProfileLimit(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	for i := 0; i < validate.MaxProfilesPerUser; i++ {
		e.createProfile(tok, api.CreateProfileRequest{Name: fmt.Sprintf("p%d", i)})
	}
	er := e.do(req{method: "POST", path: api.RouteProfiles, token: tok, body: api.CreateProfileRequest{Name: "one more"}}).apiErr(apierr.LimitExceeded)
	if er.Details[apierr.DetailLimit] != float64(validate.MaxProfilesPerUser) {
		t.Fatalf("应给出上限：%+v", er.Details)
	}
}

// ---- state 与 ETag ----

func TestStateETag(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")

	r := e.do(req{method: "GET", path: api.RouteState, token: tok}).expect(http.StatusOK)
	var s api.StateResponse
	r.decode(&s)
	if s.StateVersion != 0 || r.Header().Get("ETag") != `"0"` || s.Profiles == nil || s.Subscriptions == nil {
		t.Fatalf("初始 state 不对：%s %s", r.Header().Get("ETag"), r.Body.String())
	}
	if !strings.Contains(r.Body.String(), `"profiles":[]`) || !strings.Contains(r.Body.String(), `"subscriptions":[]`) {
		t.Fatalf("空列表应输出 []：%s", r.Body.String())
	}

	notModified := func(inm string) {
		t.Helper()
		r := e.do(req{method: "GET", path: api.RouteState, token: tok, header: map[string]string{"If-None-Match": inm}})
		if r.Code != http.StatusNotModified || r.Body.Len() != 0 || r.Header().Get("ETag") == "" {
			t.Fatalf("If-None-Match %s 应返回 304：%d %s", inm, r.Code, r.Body.String())
		}
	}
	notModified(`"0"`)
	notModified(`W/"0"`)
	notModified(`"7", "0"`)
	notModified(`*`)

	// 每次改动都让 stateVersion 加 1
	p := e.createProfile(tok, sampleCreate())
	s = e.state(tok)
	if s.StateVersion != 1 || len(s.Profiles) != 1 || s.Profiles[0].ID != p.ID {
		t.Fatalf("新建后 state 不对：%+v", s)
	}
	e.do(req{method: "GET", path: api.RouteState, token: tok, header: map[string]string{"If-None-Match": `"0"`}}).expect(http.StatusOK)
	notModified(`"1"`)

	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(p, 1)}).expect(http.StatusOK)
	if e.state(tok).StateVersion != 2 {
		t.Fatal("修改后 stateVersion 应为 2")
	}
	// 冲突和校验失败不改 stateVersion
	e.do(req{method: "PUT", path: api.ProfilePath(p.ID), token: tok, body: updateReq(p, 1)}).apiErr(apierr.RevisionConflict)
	if e.state(tok).StateVersion != 2 {
		t.Fatal("失败的修改不应改变 stateVersion")
	}
	e.do(req{method: "DELETE", path: api.ProfilePath(p.ID), token: tok}).expect(http.StatusNoContent)
	if s := e.state(tok); s.StateVersion != 3 || len(s.Profiles) != 0 {
		t.Fatalf("删除后 state 不对：%+v", s)
	}
	// 管理员改角色也让 stateVersion 加 1，客户端下次同步就能看到
	if err := e.admin.SetRole(context.Background(), "alice", model.RoleVIP); err != nil {
		t.Fatal(err)
	}
	if s := e.state(tok); s.StateVersion != 4 || s.User.Role != model.RoleVIP {
		t.Fatalf("改角色后 state 不对：%+v", s.User)
	}
	// 会话失效时 state 返回 401，客户端据此下线
	e.do(req{method: "POST", path: api.RouteLogout, token: tok}).expect(http.StatusNoContent)
	e.do(req{method: "GET", path: api.RouteState, token: tok}).apiErr(apierr.Unauthorized)
}

// 节点 token 不能出现在日志里。
func TestNodeTokenNotLogged(t *testing.T) {
	e := newEnv(t)
	tok := e.user("alice")
	e.createProfile(tok, sampleCreate())
	e.state(tok)
	if strings.Contains(e.logs.String(), "node-secret-token") {
		t.Fatal("节点 token 出现在日志里")
	}
	var data string
	e.st.DB().QueryRow(`SELECT data FROM profiles`).Scan(&data)
	if !strings.Contains(data, "node-secret-token") {
		t.Fatal("节点 token 应保存在 profiles.data 里")
	}
	rows, _ := store.ListAudit(context.Background(), e.st.DB(), "", 100)
	for _, r := range rows {
		if strings.Contains(r.Detail, "node-secret-token") {
			t.Fatal("节点 token 出现在审计里")
		}
	}
}
