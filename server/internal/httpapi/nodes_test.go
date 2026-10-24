package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"moefrp/server/internal/config"
	"moefrp/server/internal/nodes"
	"moefrp/shared/agentproto"
	"moefrp/shared/apierr"
	"moefrp/shared/frpsset"
	"moefrp/shared/nodeticket"
)

// fakeAgent 是测试用的 agent：只实现协议，不跑 frps。
type fakeAgent struct {
	t    *testing.T
	ws   *websocket.Conn
	reqs chan agentproto.Message
	gone chan struct{}
}

func dialAgent(t *testing.T, base, token string, h agentproto.Hello) (*fakeAgent, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+agentproto.RouteWS, &websocket.DialOptions{
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + token}},
		Subprotocols: []string{agentproto.Subprotocol},
	})
	if err != nil {
		return nil, resp, err
	}
	f := &fakeAgent{t: t, ws: ws, reqs: make(chan agentproto.Message, 16), gone: make(chan struct{})}
	raw, _ := json.Marshal(h)
	f.send(agentproto.Message{ID: "h1", Type: agentproto.TypeRequest, Method: agentproto.MethodHello, Data: raw})
	acked := make(chan struct{})
	go func() {
		defer close(f.gone)
		for {
			_, b, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			var m agentproto.Message
			json.Unmarshal(b, &m)
			if m.Type == agentproto.TypeResponse && m.ID == "h1" {
				close(acked)
				continue
			}
			f.reqs <- m
		}
	}()
	select {
	case <-acked:
	case <-time.After(5 * time.Second):
		t.Fatal("hello 没有响应")
	}
	t.Cleanup(func() { ws.CloseNow() })
	return f, resp, nil
}

func (f *fakeAgent) send(m agentproto.Message) {
	f.t.Helper()
	b, _ := json.Marshal(m)
	if err := f.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeAgent) expect(method string) agentproto.Message {
	f.t.Helper()
	select {
	case m := <-f.reqs:
		if m.Method != method {
			f.t.Fatalf("期望请求 %s，收到 %s", method, m.Method)
		}
		return m
	case <-time.After(5 * time.Second):
		f.t.Fatalf("没收到 %s", method)
	}
	return agentproto.Message{}
}

func (f *fakeAgent) reply(m agentproto.Message, data any) {
	raw, _ := json.Marshal(data)
	f.send(agentproto.Message{ID: m.ID, Type: agentproto.TypeResponse, Method: m.Method, Data: raw})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

type nodeEnv struct {
	*env
	http   *httptest.Server
	cookie string
}

func newNodeEnv(t *testing.T, opts ...envOpt) *nodeEnv {
	e := newEnv(t, opts...)
	hs := httptest.NewServer(e.srv)
	t.Cleanup(hs.Close)
	return &nodeEnv{env: e, http: hs, cookie: e.setupAdmin("boss")}
}

func (n *nodeEnv) admin(method, path string, body any) resp {
	return n.adminReq(method, path, n.cookie, body)
}

func (n *nodeEnv) createNode(name string) (id, code string) {
	n.t.Helper()
	var c struct {
		Node           struct{ ID string }
		EnrollCode     string
		InstallCommand string
	}
	n.admin("POST", "/nodes", createNodeRequest{Name: name, Region: "intl"}).expect(http.StatusCreated).decode(&c)
	if !strings.HasPrefix(c.EnrollCode, "MFN-") || !strings.Contains(c.InstallCommand, "/agent/install.sh' | sudo sh -s -- "+c.EnrollCode) {
		n.t.Fatalf("创建节点：%+v", c)
	}
	return c.Node.ID, c.EnrollCode
}

func (n *nodeEnv) enroll(code string) agentproto.EnrollResponse {
	n.t.Helper()
	var out agentproto.EnrollResponse
	n.do(req{method: "POST", path: agentproto.RouteEnroll, body: agentproto.EnrollRequest{Code: code, Hostname: "vps"}}).
		expect(http.StatusOK).decode(&out)
	return out
}

func (n *nodeEnv) node(id string) map[string]any {
	n.t.Helper()
	var v map[string]any
	n.admin("GET", "/nodes/"+id, nil).expect(http.StatusOK).decode(&v)
	return v
}

func TestNodeLifecycle(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("东京 1")
	if v := n.node(id); v["status"] != "pending" || v["settings"].(map[string]any)["bindPort"] != float64(7000) || len(v["roles"].([]any)) != 0 {
		t.Fatalf("新节点（默认配置、只给管理员用）：%v", v)
	}

	// 注册：安装码只能用一次
	n.do(req{method: "POST", path: agentproto.RouteEnroll, body: agentproto.EnrollRequest{Code: "MFN-AAAA-AAAA-AAAA"}}).apiErr(apierr.Forbidden)
	en := n.enroll(strings.ToLower(code))
	if en.NodeID != id || !strings.HasPrefix(en.Token, "mfn_") {
		t.Fatalf("enroll: %+v", en)
	}
	n.do(req{method: "POST", path: agentproto.RouteEnroll, body: agentproto.EnrollRequest{Code: code}}).apiErr(apierr.Forbidden)

	// 长连接认证
	if _, resp, err := dialAgent(t, n.http.URL, "mfn_bad", agentproto.Hello{}); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatal("错误令牌应被拒绝")
	}
	a, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.4.0", OS: "linux", Arch: "amd64", Hostname: "vps",
		MachineID: "machine-1", Features: []string{agentproto.FeatureAccess, agentproto.FeatureTickets, agentproto.FeatureSettings}})
	if err != nil {
		t.Fatal(err)
	}

	// 上线后：先下发存储租约（7 天），再推授权表（始终启用：节点只认票据），最后下发结构化配置（没有 token）
	m := a.expect(agentproto.MethodStoreLease)
	var lease agentproto.StoreLease
	json.Unmarshal(m.Data, &lease)
	if lease.Key == "" || lease.LeaseUntil-lease.ServerTime != 7*24*3600 {
		t.Fatalf("存储租约：%+v", lease)
	}
	a.reply(m, struct{}{})
	m = a.expect(agentproto.MethodAccessSync)
	var tb agentproto.AccessTable
	json.Unmarshal(m.Data, &tb)
	if !tb.Enabled || tb.TicketKey == "" || tb.NodeID != id {
		t.Fatalf("授权表：%+v", tb)
	}
	a.reply(m, agentproto.AccessResult{})
	m = a.expect(agentproto.MethodFrpsApply)
	var ap agentproto.ApplyRequest
	json.Unmarshal(m.Data, &ap)
	if ap.Rev != 1 || ap.Settings == nil || ap.Settings.BindPort != 7000 || ap.Config != "" {
		t.Fatalf("初始配置：%+v", ap)
	}
	a.reply(m, agentproto.ApplyResult{Rev: 1, Applied: true})
	eventually(t, "applied_rev = 1", func() bool { return n.node(id)["appliedRev"] == float64(1) })

	raw, _ := json.Marshal(agentproto.Report{CPU: 12.5, NetRx: 1000, Frps: agentproto.FrpsStatus{State: agentproto.FrpsRunning, Clients: 3}})
	a.send(agentproto.Message{Type: agentproto.TypeEvent, Method: agentproto.MethodReport, Data: raw})
	eventually(t, "report", func() bool {
		v := n.node(id)
		r, _ := v["report"].(map[string]any)
		return v["status"] == "online" && r != nil && r["cpu"] == 12.5
	})
	v := n.node(id)
	cl := v["client"].(map[string]any)
	if _, hasToken := cl["token"]; cl["port"] != float64(7000) || hasToken || cl["addr"] != "127.0.0.1" {
		t.Fatalf("客户端连接信息（没有 token）：%v", cl)
	}

	// 配置不合法：标出字段，不下发
	bad := frpsset.Defaults()
	bad.VhostHTTPPort = 7000
	e := n.admin("PUT", "/nodes/"+id+"/settings", bad).apiErr(apierr.ValidationFailed)
	if fs, _ := e.Details["fields"].([]any); len(fs) != 1 || fs[0].(map[string]any)["field"] != "vhostHTTPPort" {
		t.Fatalf("应标出字段：%+v", e)
	}
	var schema struct{ Fields []map[string]any }
	n.admin("GET", "/frps-schema", nil).expect(http.StatusOK).decode(&schema)
	if len(schema.Fields) < 15 {
		t.Fatalf("schema：%d", len(schema.Fields))
	}

	// 改配置：节点回滚时如实返回
	set := frpsset.Defaults()
	set.BindPort = 7001
	done := make(chan resp, 1)
	go func() { done <- n.admin("PUT", "/nodes/"+id+"/settings", set) }()
	m = a.expect(agentproto.MethodFrpsApply)
	json.Unmarshal(m.Data, &ap)
	if ap.Settings == nil || ap.Settings.BindPort != 7001 {
		t.Fatalf("下发的配置：%+v", ap)
	}
	a.reply(m, agentproto.ApplyResult{Rev: 2, RolledBack: true, Error: "frps 启动失败：端口被占用"})
	var out struct {
		Rev    int64
		Pushed bool
		Result agentproto.ApplyResult
	}
	(<-done).expect(http.StatusOK).decode(&out)
	if out.Rev != 2 || !out.Pushed || !out.Result.RolledBack {
		t.Fatalf("回滚：%+v", out)
	}
	if v := n.node(id); v["appliedRev"] != float64(1) || !strings.Contains(v["applyError"].(string), "端口") {
		t.Fatalf("应保留旧版本号和错误：%v", v)
	}

	// 执行命令要先重新输入密码
	n.admin("POST", "/nodes/exec", nodeExecRequest{Nodes: []string{id}, Command: "uptime"}).apiErr(apierr.ReauthRequired)
	n.admin("POST", "/elevate", elevateRequest{Password: "wrong-password"}).apiErr(apierr.InvalidCredentials)
	n.admin("POST", "/elevate", elevateRequest{Password: "password-boss"}).expect(http.StatusOK)
	var ex struct{ Tasks []struct{ ID, Status string } }
	n.admin("POST", "/nodes/exec", nodeExecRequest{Nodes: []string{id}, Command: "uptime"}).expect(http.StatusAccepted).decode(&ex)
	m = a.expect(agentproto.MethodExec)
	a.reply(m, agentproto.ExecResult{ExitCode: 0, Output: "up 3 days"})
	eventually(t, "任务完成", func() bool {
		var tk map[string]any
		n.admin("GET", "/tasks/"+ex.Tasks[0].ID, nil).expect(http.StatusOK).decode(&tk)
		return tk["status"] == "done" && tk["output"] == "up 3 days"
	})

	// 日志：透传节点的结果；节点报错转成 node_failed
	go func() {
		m := a.expect(agentproto.MethodLogs)
		a.send(agentproto.Message{ID: m.ID, Type: agentproto.TypeResponse, Error: &agentproto.Error{Code: "failed", Message: "坏了"}})
	}()
	n.admin("GET", "/nodes/"+id+"/logs", nil).apiErr(apierr.NodeFailed)

	// 审计日志里有命令全文
	var audit struct{ Entries []map[string]any }
	n.admin("GET", "/audit?action=node.", nil).expect(http.StatusOK).decode(&audit)
	found := false
	for _, en := range audit.Entries {
		if d, _ := en["detail"].(map[string]any); en["action"] == "node.exec" && d["command"] == "uptime" {
			found = true
		}
	}
	if !found {
		t.Fatalf("审计日志：%v", audit.Entries)
	}

	// 删除：连接断开，令牌作废
	n.admin("DELETE", "/nodes/"+id, nil).expect(http.StatusNoContent)
	select {
	case <-a.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("删除后连接应断开")
	}
	if _, resp, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{}); err == nil || resp.StatusCode != 401 {
		t.Fatal("删除后令牌应作废")
	}
}

func TestNodeOfflineUninstallAndUpgrade(t *testing.T) {
	dist := t.TempDir()
	bin := []byte("fake agent binary")
	os.WriteFile(filepath.Join(dist, "moefrp-agent-linux-amd64"), bin, 0o755)
	os.WriteFile(filepath.Join(dist, "VERSION"), []byte("2.0\n"), 0o644)
	n := newNodeEnv(t, withConfig(func(c *config.Config) { c.AgentDistDir = dist }))

	// 安装脚本和下载
	r := n.do(req{method: "GET", path: agentproto.RouteInstallScript}).expect(http.StatusOK) // httptest 的 Host 是 example.com
	if !strings.Contains(r.Body.String(), "SERVER='http://example.com'") {
		t.Fatal("安装脚本里应写好面板地址")
	}
	sum := sha256.Sum256(bin)
	r = n.do(req{method: "GET", path: agentproto.RouteDownload + "linux-amd64.sha256"}).expect(http.StatusOK)
	if !strings.HasPrefix(r.Body.String(), hex.EncodeToString(sum[:])) {
		t.Fatalf("sha256：%s", r.Body.String())
	}
	resp, err := http.Get(n.http.URL + agentproto.RouteDownload + "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != string(bin) {
		t.Fatal("下载内容不对")
	}
	n.do(req{method: "GET", path: agentproto.RouteDownload + "linux-mips"}).apiErr(apierr.NotFound)

	id, code := n.createNode("香港")
	en := n.enroll(code)

	// 离线时卸载：先标记，上线后执行，然后节点被删除
	n.admin("POST", "/elevate", elevateRequest{Password: "password-boss"}).expect(http.StatusOK)
	var un struct{ Done bool }
	n.admin("POST", "/nodes/"+id+"/uninstall", nil).expect(http.StatusOK).decode(&un)
	if un.Done || n.node(id)["pendingUninstall"] != true {
		t.Fatal("离线时应只标记")
	}
	n.admin("POST", "/nodes/"+id+"/cancel-uninstall", nil).expect(http.StatusNoContent)

	// 升级：旧版本上线，可以升级到打包的 2.0
	a, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.0", OS: "linux", Arch: "amd64", AppliedRev: 1})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "upgradeTo", func() bool { return n.node(id)["upgradeTo"] == "2.0" })
	var tk struct{ ID, Status string }
	n.admin("POST", "/nodes/"+id+"/upgrade", nil).expect(http.StatusAccepted).decode(&tk)
	m := a.expect(agentproto.MethodUpgrade)
	var up agentproto.UpgradeRequest
	json.Unmarshal(m.Data, &up)
	if up.Version != "2.0" || up.URL != agentproto.RouteDownload+"linux-amd64" || up.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("升级请求：%+v", up)
	}
	a.reply(m, struct{}{})
	n.admin("POST", "/nodes/"+id+"/upgrade", nil).apiErr(apierr.ValidationFailed) // 正在升级
	a.ws.CloseNow()
	// 新版本上线并报告结果
	a2, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "2.0", OS: "linux", Arch: "amd64", AppliedRev: 1,
		Upgrade: &agentproto.UpgradeResult{From: "1.0", To: "2.0", OK: true}})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "升级任务完成", func() bool {
		var x map[string]any
		n.admin("GET", "/tasks/"+tk.ID, nil).expect(http.StatusOK).decode(&x)
		return x["status"] == "done"
	})
	if n.node(id)["upgradeTo"] != nil {
		t.Fatal("已是最新版本")
	}

	// 在线卸载：节点收到 uninstall 后面板删除节点
	go func() {
		m := a2.expect(agentproto.MethodUninstall)
		a2.reply(m, struct{}{})
	}()
	n.admin("POST", "/nodes/"+id+"/uninstall", nil).expect(http.StatusOK).decode(&un)
	if !un.Done {
		t.Fatal("在线时应直接卸载")
	}
	n.admin("GET", "/nodes/"+id, nil).apiErr(apierr.NotFound)
}

// stubSource 是测试用的 GitHub 来源：固定返回一个版本。
type stubSource struct{ b *nodes.Build }

func (s stubSource) Latest(context.Context) (*nodes.Build, error) { return s.b, nil }
func (s stubSource) Cached() (*nodes.Build, error)                { return s.b, nil }
func (s stubSource) Kind() string                                 { return "github" }

func TestNodeDownloadMirror(t *testing.T) {
	const gh = "https://github.com/o/r/releases/download/agent-v2.0.0/moefrp-agent-linux-amd64"
	sum := strings.Repeat("ab", 32)
	n := newNodeEnv(t, withConfig(func(c *config.Config) { c.AgentMirror = "https://default.example.com/" }))
	n.srv.nodes = nodes.New(nodes.Options{Store: n.st, Now: n.clock.Now, Source: stubSource{&nodes.Build{
		Version: "2.0.0", Source: "github", Files: map[string]nodes.BuildFile{"amd64": {URL: gh, SHA256: sum}}}}})

	// 添加节点时指定下载代理：安装命令带上它，脚本里的下载地址加前缀
	var c struct {
		Node           struct{ ID, DownloadMirror string }
		EnrollCode     string
		InstallCommand string
	}
	n.admin("POST", "/nodes", createNodeRequest{Name: "国内", DownloadMirror: "https://gh.example.com", Region: "cn"}).expect(http.StatusCreated).decode(&c)
	if c.Node.DownloadMirror != "https://gh.example.com/" {
		t.Fatalf("应补上末尾的 /：%q", c.Node.DownloadMirror)
	}
	want := "curl -fsSL 'http://example.com/agent/install.sh?mirror=https%3A%2F%2Fgh.example.com%2F' | sudo sh -s -- " + c.EnrollCode
	if c.InstallCommand != want {
		t.Fatalf("安装命令：\n%s\n%s", c.InstallCommand, want)
	}
	r := n.do(req{method: "GET", path: "/agent/install.sh?mirror=https%3A%2F%2Fgh.example.com%2F"}).expect(http.StatusOK)
	if !strings.Contains(r.Body.String(), "URL_AMD64='https://gh.example.com/"+gh+"'") || !strings.Contains(r.Body.String(), "SHA_AMD64='"+sum+"'") {
		t.Fatal("脚本里的下载地址应加上代理")
	}
	r = n.do(req{method: "GET", path: "/agent/install.sh?mirror=http%3A%2F%2Fbad"}).expect(http.StatusOK)
	if !strings.Contains(r.Body.String(), "UNAVAILABLE='安装命令里的下载代理不正确") {
		t.Fatal("代理不正确时脚本应报错")
	}
	n.admin("POST", "/nodes", createNodeRequest{Name: "x", DownloadMirror: "http://insecure.example.com/", Region: "cn"}).apiErr(apierr.ValidationFailed)

	// 列表里给出默认代理和用过的代理
	var list struct {
		DefaultMirror string
		Mirrors       []string
	}
	n.admin("GET", "/nodes", nil).expect(http.StatusOK).decode(&list)
	if list.DefaultMirror != "https://default.example.com/" || len(list.Mirrors) != 2 {
		t.Fatalf("代理列表：%+v", list)
	}

	// 升级用节点的下载代理；在设置里改掉后升级直连
	en := n.enroll(c.EnrollCode)
	a, _, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{AgentVersion: "1.0.0", OS: "linux", Arch: "amd64", AppliedRev: 1})
	if err != nil {
		t.Fatal(err)
	}
	upgrade := func() agentproto.UpgradeRequest {
		t.Helper()
		var tk struct{ ID string }
		n.admin("POST", "/nodes/"+c.Node.ID+"/upgrade", nil).expect(http.StatusAccepted).decode(&tk)
		m := a.expect(agentproto.MethodUpgrade)
		var up agentproto.UpgradeRequest
		json.Unmarshal(m.Data, &up)
		a.send(agentproto.Message{ID: m.ID, Type: agentproto.TypeResponse, Error: &agentproto.Error{Code: "failed", Message: "测试"}})
		eventually(t, "任务结束", func() bool {
			var x map[string]any
			n.admin("GET", "/tasks/"+tk.ID, nil).expect(http.StatusOK).decode(&x)
			return x["status"] == "failed"
		})
		return up
	}
	if up := upgrade(); up.URL != "https://gh.example.com/"+gh || up.SHA256 != sum {
		t.Fatalf("升级应经过代理：%+v", up)
	}
	empty := ""
	n.admin("PATCH", "/nodes/"+c.Node.ID, updateNodeRequest{Name: "国内", DownloadMirror: &empty}).expect(http.StatusNoContent)
	if up := upgrade(); up.URL != gh {
		t.Fatalf("去掉代理后直连：%+v", up)
	}
	// 不传 downloadMirror 时不改
	n.admin("PATCH", "/nodes/"+c.Node.ID, updateNodeRequest{Name: "国内 2"}).expect(http.StatusNoContent)
	if n.node(c.Node.ID)["downloadMirror"] != "" {
		t.Fatal("没传 downloadMirror 时不应改动")
	}
}

func TestNodeResetToken(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("A")
	en := n.enroll(code)
	n.admin("POST", "/nodes/"+id+"/reset-token", nil).apiErr(apierr.ReauthRequired)
	n.admin("POST", "/elevate", elevateRequest{Password: "password-boss"}).expect(http.StatusOK)
	var c struct{ EnrollCode string }
	n.admin("POST", "/nodes/"+id+"/reset-token", nil).expect(http.StatusOK).decode(&c)
	if _, resp, err := dialAgent(t, n.http.URL, en.Token, agentproto.Hello{}); err == nil || resp.StatusCode != 401 {
		t.Fatal("旧令牌应作废")
	}
	if en2 := n.enroll(c.EnrollCode); en2.NodeID != id || en2.Token == en.Token {
		t.Fatal("新安装码应能注册同一个节点")
	}
	// 节点不在线时的操作
	n.admin("POST", "/nodes/"+id+"/restart", nil).apiErr(apierr.NodeOffline)
	n.admin("POST", "/nodes", createNodeRequest{Name: ""}).apiErr(apierr.ValidationFailed)
}

// TestNodeOfflineGrace：节点断开后先显示"重连中"（宽限期内仍算在线），宽限期内重连上，上线时间接着算；
// 超过宽限期才显示离线。断线次数按 24 小时统计。
func TestNodeOfflineGrace(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("不稳的节点")
	en := n.enroll(code)
	hello := agentproto.Hello{AgentVersion: "1.3.0", OS: "linux", Arch: "amd64"}
	fa, _, err := dialAgent(t, n.http.URL, en.Token, hello)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "在线", func() bool { return n.node(id)["status"] == "online" })
	connectedAt := n.node(id)["connectedAt"]
	if n.node(id)["drops24h"] != float64(0) {
		t.Fatalf("还没断过：%v", n.node(id)["drops24h"])
	}

	// 断开：宽限期内显示在线、标为重连中，记一次断线
	fa.ws.Close(websocket.StatusNormalClosure, "")
	eventually(t, "重连中", func() bool { v := n.node(id); return v["status"] == "online" && v["reconnecting"] == true })
	if n.node(id)["drops24h"] != float64(1) {
		t.Fatalf("断线次数：%v", n.node(id)["drops24h"])
	}

	// 宽限期内重连上：恢复正常，上线时间接着算
	n.clock.Add(30 * time.Second)
	fa2, _, err := dialAgent(t, n.http.URL, en.Token, hello)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "重连上", func() bool { v := n.node(id); return v["status"] == "online" && v["reconnecting"] == nil })
	if got := n.node(id)["connectedAt"]; got != connectedAt {
		t.Fatalf("宽限期内重连，上线时间应接着算：%v → %v", connectedAt, got)
	}

	// 再断开，超过宽限期：离线
	fa2.ws.Close(websocket.StatusNormalClosure, "")
	eventually(t, "第二次断开", func() bool { return n.node(id)["reconnecting"] == true })
	n.clock.Add((agentproto.OfflineGrace + 1) * time.Second)
	if v := n.node(id); v["status"] != "offline" || v["drops24h"] != float64(2) {
		t.Fatalf("超过宽限期：%v drops=%v", v["status"], v["drops24h"])
	}
	// 24 小时以前的断线不再计数（分几步走，中间的请求让管理后台的会话不因空闲过期）
	for i := 0; i < 3; i++ {
		n.clock.Add(9 * time.Hour)
		n.node(id)
	}
	if v := n.node(id); v["drops24h"] != float64(0) {
		t.Fatalf("24 小时后：%v", v["drops24h"])
	}
}

// TestNodeMachineAndSecret：机器指纹第一次上线时记下，令牌被拷到别的机器上会被拒绝，重置令牌后可以换机器；
// 存储密钥绑定机器指纹；轮换面板主密钥后，在线节点马上收到新的存储密钥和授权表。
func TestNodeMachineAndSecret(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("机器")
	en := n.enroll(code)
	hello := func(machine string) agentproto.Hello {
		return agentproto.Hello{AgentVersion: "1.4.0", OS: "linux", Arch: "amd64", MachineID: machine,
			Features: []string{agentproto.FeatureAccess, agentproto.FeatureTickets, agentproto.FeatureSettings}}
	}
	auto := func(f *fakeAgent) *autoAgent { a := &autoAgent{fakeAgent: f}; a.run(); return a }
	a1, _, err := dialAgent(t, n.http.URL, en.Token, hello("machine-1"))
	if err != nil {
		t.Fatal(err)
	}
	aa := auto(a1)
	eventually(t, "租约", func() bool { return aa.lease() != nil })
	key1 := aa.lease().Key

	// 同一个令牌换一台机器：拒绝，原来的连接不受影响
	a2, _, err := dialAgent(t, n.http.URL, en.Token, hello("machine-2"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a2.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("机器指纹不同的连接应被断开")
	}
	if n.node(id)["status"] != "online" {
		t.Fatal("原来的节点应还在线")
	}

	// 轮换主密钥：指纹变了，在线节点收到新的存储密钥（和旧的不同）
	var before, after struct{ Fingerprint string }
	n.admin("GET", "/node-secret", nil).expect(http.StatusOK).decode(&before)
	n.admin("POST", "/node-secret/rotate", nil).apiErr(apierr.ReauthRequired)
	n.admin("POST", "/elevate", elevateRequest{Password: "password-boss"}).expect(http.StatusOK)
	n.admin("POST", "/node-secret/rotate", nil).expect(http.StatusOK).decode(&after)
	if after.Fingerprint == before.Fingerprint || before.Fingerprint == "" {
		t.Fatalf("指纹：%s → %s", before.Fingerprint, after.Fingerprint)
	}
	eventually(t, "新存储密钥", func() bool { l := aa.lease(); return l != nil && l.Key != key1 })

	// 重置令牌后可以装到另一台机器上
	var c struct{ EnrollCode string }
	n.admin("POST", "/nodes/"+id+"/reset-token", nil).expect(http.StatusOK).decode(&c)
	en2 := n.enroll(c.EnrollCode)
	a3, _, err := dialAgent(t, n.http.URL, en2.Token, hello("machine-2"))
	if err != nil {
		t.Fatal(err)
	}
	a3a := auto(a3)
	eventually(t, "新机器上线", func() bool { return a3a.lease() != nil })
	key3, _ := base64.StdEncoding.DecodeString(a3a.lease().Key)
	if string(key3) == string(nodeticket.StoreKey([]byte("x"), id, "machine-2")) || len(key3) != 32 {
		t.Fatal("存储密钥应由面板主密钥派生")
	}
}

// TestNodeAccess：用户访问地址——IP 自动检测（网卡上的公网地址，内网的不算），管理员可以覆盖、填 "-" 不显示；
// 格式不对时标出字段。
func TestNodeAccess(t *testing.T) {
	n := newNodeEnv(t)
	id, code := n.createNode("上海")
	en := n.enroll(code)
	h := managedHello
	h.PublicIPs = []string{"10.0.0.2", "fe80::1", "2001:db8:1::5"}
	a, _, err := dialAgent(t, n.http.URL, en.Token, h)
	if err != nil {
		t.Fatal(err)
	}
	(&autoAgent{fakeAgent: a}).run()
	eventually(t, "上线", func() bool { return n.node(id)["status"] == "online" })
	acc := n.node(id)["access"].(map[string]any)
	if acc["detectedV6"] != "2001:db8:1::5" || acc["detectedV4"] != nil {
		t.Fatalf("自动检测：%v", acc)
	}
	n.admin("PUT", "/nodes/"+id+"/access", map[string]string{"domain": "http://x"}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/nodes/"+id+"/access", map[string]string{"ipv4": "2001:db8::1"}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/nodes/"+id+"/access", map[string]string{"domain": "SH1.Example.com", "ipv4": "203.0.113.9", "ipv6": "-"}).expect(http.StatusNoContent)
	eff := n.node(id)["access"].(map[string]any)["effective"].(map[string]any)
	if eff["domain"] != "sh1.example.com" || eff["ipv4"] != "203.0.113.9" || eff["ipv6"] != "" {
		t.Fatalf("覆盖后：%v", eff)
	}
}

// 节点排序：管理后台的列表按它排，新节点排最后，重复或不存在的 ID 报错；新建节点必须选地区
func TestNodeOrder(t *testing.T) {
	n := newNodeEnv(t)
	a, _ := n.createNode("A")
	b, _ := n.createNode("B")
	c, _ := n.createNode("C")
	names := func() string {
		var list struct{ Nodes []struct{ Name string } }
		n.admin("GET", "/nodes", nil).expect(http.StatusOK).decode(&list)
		var out []string
		for _, x := range list.Nodes {
			out = append(out, x.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(); got != "A,B,C" {
		t.Fatalf("默认按创建顺序：%s", got)
	}
	n.admin("PUT", "/nodes-order", map[string]any{"ids": []string{c, a, b}}).expect(http.StatusNoContent)
	n.createNode("D")
	if got := names(); got != "C,A,B,D" {
		t.Fatalf("排序后、新节点在最后：%s", got)
	}
	// 地区必选；改地区时只认三种
	n.admin("POST", "/nodes", createNodeRequest{Name: "E"}).apiErr(apierr.ValidationFailed)
	n.admin("PATCH", "/nodes/"+a, map[string]any{"name": "A", "region": "mars"}).apiErr(apierr.ValidationFailed)
	n.admin("PATCH", "/nodes/"+a, map[string]any{"name": "A", "region": "hmt"}).expect(http.StatusNoContent)
	if r := n.node(a)["region"]; r != "hmt" {
		t.Fatalf("地区：%v", r)
	}
	n.admin("PUT", "/nodes-order", map[string]any{"ids": []string{a, a}}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/nodes-order", map[string]any{"ids": []string{"nope"}}).apiErr(apierr.NotFound)
}
