package access

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	frpclient "github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	plugin "github.com/fatedier/frp/pkg/plugin/server"

	"moefrp/agent/internal/frpsrv"
	"moefrp/agent/internal/vault"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/frpsset"
	"moefrp/shared/nodeticket"
	"moefrp/shared/quota"
)

var (
	testMaster = []byte("0123456789abcdef0123456789abcdef")
	testKey    = nodeticket.NodeKey(testMaster, "node-1")
)

func table(enabled bool) agentproto.AccessTable {
	return agentproto.AccessTable{Enabled: enabled, NodeID: "node-1", TicketKey: base64.StdEncoding.EncodeToString(testKey),
		Users: []agentproto.AccessUser{{
			UserID: "u1", Nickname: "alice", Gen: 1,
			Quota: quota.Quota{MaxProxies: 2, Ports: "20000-20010", Types: []string{"tcp", "udp"}},
		}}}
}

// ticket 签发一张 alice 在会话 sess 上的票据，edit 可以改声明。
func ticket(sess string, edit ...func(*nodeticket.Claims)) string {
	now := time.Now().Unix()
	c := nodeticket.Claims{User: "u1", Session: sess, Node: "node-1", Gen: 1, Issued: now, Expires: now + 900, Nonce: nodeticket.NewNonce(),
		Device: nodeticket.DeviceTag(testKey, "dev-1")}
	for _, e := range edit {
		e(&c)
	}
	return nodeticket.Sign(testKey, c)
}

// metas 是 frpc 登录时带的 metadatas：票据和设备指纹（dev-1）。
func metas(tk string) map[string]string {
	return map[string]string{api.MetaCredential: tk, api.MetaDevice: "dev-1"}
}

func decide(t *testing.T, s *Store, op string, content any) (string, any) {
	t.Helper()
	b, _ := json.Marshal(content)
	return s.decide(op, b)
}

// login 用票据登录，返回拒绝原因和插件分配的 run ID。
func login(t *testing.T, s *Store, user, tk, runID string) (string, string) {
	t.Helper()
	r, content := decide(t, s, plugin.OpLogin, plugin.LoginContent{Login: msg.Login{User: user, Metas: metas(tk), RunID: runID}})
	if c, ok := content.(*plugin.LoginContent); ok {
		return r, c.RunID
	}
	return r, runID
}

func TestDecide(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "access.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	online := 0
	var lastLive func(string) bool
	s.Online = func(user, except string, live func(string) bool) int { lastLive = live; return online }

	// 不是官方节点：全部放行
	if r, _ := decide(t, s, plugin.OpLogin, plugin.LoginContent{}); r != "" {
		t.Fatalf("非官方节点应放行：%s", r)
	}
	changed, err := s.Apply(table(true))
	if err != nil || !changed || !s.Enabled() || s.Users() != 1 {
		t.Fatalf("apply: %v %v", changed, err)
	}

	// 合法登录：插件分配 run ID
	tk := ticket("sess-a")
	r, run1 := login(t, s, "alice", tk, "")
	if r != "" || run1 == "" {
		t.Fatalf("合法登录：%q run=%q", r, run1)
	}
	// 同一张票据不能再登录
	if r, _ := login(t, s, "alice", tk, ""); !strings.Contains(r, "用过") {
		t.Fatalf("重复使用：%q", r)
	}
	bad := map[string]string{
		"没有票据":    "",
		"静态凭证":    "mfc_whatever",
		"别的节点":    nodeticket.Sign(nodeticket.NodeKey(testMaster, "node-2"), nodeticket.Claims{User: "u1", Session: "s", Node: "node-2", Gen: 1, Expires: time.Now().Unix() + 900, Nonce: "n1"}),
		"声明的节点不对": ticket("sess-a", func(c *nodeticket.Claims) { c.Node = "node-2" }),
	}
	for what, tk := range bad {
		if r, _ := login(t, s, "alice", tk, ""); !strings.Contains(r, "官方节点") {
			t.Fatalf("%s：%q", what, r)
		}
	}
	if r, _ := login(t, s, "alice", ticket("sess-a", func(c *nodeticket.Claims) { c.Expires = time.Now().Add(-2 * time.Minute).Unix() }), ""); !strings.Contains(r, "过期") {
		t.Fatalf("过期：%q", r)
	}
	if r, _ := login(t, s, "alice", ticket("sess-a", func(c *nodeticket.Claims) { c.User = "u2" }), ""); !strings.Contains(r, "不能使用") {
		t.Fatalf("没有权限：%q", r)
	}
	if r, _ := login(t, s, "alice", ticket("sess-a", func(c *nodeticket.Claims) { c.Gen = 0 }), ""); !strings.Contains(r, "重置") {
		t.Fatalf("旧代数：%q", r)
	}
	if r, _ := login(t, s, "bob", ticket("sess-a"), ""); !strings.Contains(r, "alice") {
		t.Fatalf("user 不是昵称：%q", r)
	}
	// 设备绑定：票据是发给 dev-1 的，换一台设备（或不带设备指纹）登录不了
	for _, dev := range []string{"dev-2", ""} {
		r, _ := decide(t, s, plugin.OpLogin, plugin.LoginContent{Login: msg.Login{User: "alice",
			Metas: map[string]string{api.MetaCredential: ticket("sess-a"), api.MetaDevice: dev}}})
		if !strings.Contains(r, "设备") {
			t.Fatalf("设备 %q：%q", dev, r)
		}
	}

	user := plugin.UserInfo{User: "alice", Metas: metas(tk), RunID: run1}
	np := func(u plugin.UserInfo, typ string, port int) string {
		r, _ := decide(t, s, plugin.OpNewProxy, plugin.NewProxyContent{User: u, NewProxy: msg.NewProxy{ProxyName: "alice.p", ProxyType: typ, RemotePort: port}})
		return r
	}
	if r := np(user, "tcp", 20005); r != "" {
		t.Fatalf("范围内的端口：%s", r)
	}
	if !lastLive(run1) || lastLive("other-run") {
		t.Fatal("在线代理只算生效中的 frpc")
	}
	if r := np(user, "tcp", 30000); !strings.Contains(r, "20000-20010") {
		t.Fatalf("范围外的端口：%q", r)
	}
	if r := np(user, "tcp", 0); !strings.Contains(r, "指定远程端口") {
		t.Fatalf("随机端口：%q", r)
	}
	if r := np(user, "http", 0); !strings.Contains(r, "http") {
		t.Fatalf("不允许的类型：%q", r)
	}
	online = 2
	if r := np(user, "udp", 20006); !strings.Contains(r, "最多同时运行 2") {
		t.Fatalf("超过数量：%q", r)
	}
	online = 0
	if r, _ := decide(t, s, plugin.OpNewUserConn, plugin.NewUserConnContent{User: user}); r != "" {
		t.Fatalf("用户连接：%s", r)
	}
	if r, _ := decide(t, s, plugin.OpNewWorkConn, plugin.NewWorkConnContent{User: user}); r != "" {
		t.Fatalf("工作连接：%s", r)
	}

	// 断线重连：frpc 带着原来的 run ID 和新票据登录，内容不用改写
	tk2 := ticket("sess-a")
	r, content := decide(t, s, plugin.OpLogin, plugin.LoginContent{Login: msg.Login{User: "alice", Metas: metas(tk2), RunID: run1}})
	if r != "" || content != nil {
		t.Fatalf("重连：%q %v", r, content)
	}

	// 同一会话在别处登录（新 run ID）：后登录的生效，原来的 frpc 不再接受新代理和新连接
	r, run2 := login(t, s, "alice", ticket("sess-a"), "")
	if r != "" || run2 == run1 {
		t.Fatalf("第二次登录：%q", r)
	}
	if r := np(user, "tcp", 20005); r != superseded {
		t.Fatalf("被顶掉的 frpc 新建代理：%q", r)
	}
	if r, _ := decide(t, s, plugin.OpNewWorkConn, plugin.NewWorkConnContent{User: user}); r != superseded {
		t.Fatalf("被顶掉的 frpc 工作连接：%q", r)
	}
	if lastLive(run1) {
		t.Fatal("被顶掉的 frpc 的代理不算在线数")
	}
	user2 := plugin.UserInfo{User: "alice", Metas: metas(tk2), RunID: run2}
	if r := np(user2, "tcp", 20005); r != "" {
		t.Fatalf("新 frpc：%q", r)
	}
	// 另一个会话（另一台设备）不受影响
	tkB := ticket("sess-b")
	if r, _ := login(t, s, "alice", tkB, ""); r != "" {
		t.Fatalf("另一台设备：%q", r)
	}

	// 会话被撤销：登录和已登录 frpc 的新连接都被拒绝
	tb := table(true)
	tb.RevokedSessions = []string{"sess-a"}
	if changed, _ := s.Apply(tb); changed {
		t.Fatal("开关没变")
	}
	if r, _ := decide(t, s, plugin.OpNewUserConn, plugin.NewUserConnContent{User: user2}); r != revoked {
		t.Fatalf("撤销后的用户连接：%q", r)
	}
	if r, _ := login(t, s, "alice", ticket("sess-a"), ""); r != revoked {
		t.Fatalf("撤销后登录：%q", r)
	}
	// 用户被撤销授权
	empty := table(true)
	empty.Users = nil
	s.Apply(empty)
	if r, _ := login(t, s, "alice", ticket("sess-b"), ""); !strings.Contains(r, "不能使用") {
		t.Fatalf("撤销授权：%q", r)
	}

	// 表、用过的票据、见过的已撤销会话都保存在文件里，重启后还在（即使新表里不再列出这个会话）
	s2, err := Load(s.path, nil)
	if err != nil || !s2.Enabled() || s2.Users() != 0 {
		t.Fatalf("reload: %v", err)
	}
	s2.Apply(table(true))
	if r, _ := login(t, s2, "alice", tkB, ""); !strings.Contains(r, "用过") {
		t.Fatalf("重启后用过的票据：%q", r)
	}
	if r, _ := login(t, s2, "alice", ticket("sess-a"), ""); r != revoked {
		t.Fatalf("重启后撤销的会话：%q", r)
	}
	if r, _ := login(t, s2, "alice", ticket("sess-c"), ""); r != "" {
		t.Fatalf("重启后新会话：%q", r)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// echoServer 是代理的本地目标。
func echoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func echoOK(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		return false
	}
	b := make([]byte, 4)
	_, err = io.ReadFull(c, b)
	return err == nil && string(b) == "ping"
}

// TestWithFrps 用真的 frps（带插件）和 frpc 走一遍：票据、昵称、端口范围、代理数、断线重连、同一会话后登录的生效。
//
// 客户端和这里一样用 frp 的 ServiceOptions.ConnectorCreator 在每次登录前换上新票据。这依赖 frp 内部的行为：
// 每次登录（包括重连）前调用 ConnectorCreator，传入的就是随后拼登录消息时读 Metadatas 的那份配置。
// 升级 frp 后这个测试失败，说明这个行为变了，客户端的票据注入要跟着改。
func TestWithFrps(t *testing.T) {
	dir := t.TempDir()
	v := vault.Open(dir, "machine-1", "agent-token", nil)
	if _, err := v.SetLease(bytes.Repeat([]byte{7}, 32), time.Now().Add(time.Hour).Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(filepath.Join(dir, "access.json"), v)
	if _, err := s.Apply(table(true)); err != nil {
		t.Fatal(err)
	}
	addr, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	set := frpsset.Defaults()
	set.BindAddr, set.BindPort, set.AllowPorts = "127.0.0.1", freePort(t), "20000-20010"
	bind := set.BindPort

	// 没有准入插件时绝不启动（节点没有 token，不挂插件谁都能连）
	bare := frpsrv.New(filepath.Join(dir, "frps.toml"))
	bare.Vault = v
	if r := bare.ApplySettings(1, set); r.Applied || !strings.Contains(r.Error, "授权表") {
		t.Fatalf("没有插件时应拒绝启动：%+v", r)
	}

	m := frpsrv.New(filepath.Join(dir, "frps.toml"))
	m.Vault = v
	m.Plugin = func() *v1.HTTPPluginOptions {
		return &v1.HTTPPluginOptions{Name: "moefrp-access", Addr: addr, Path: "/handler", Ops: PluginOps}
	}
	if r := m.ApplySettings(1, set); !r.Applied {
		t.Fatalf("frps: %+v", r)
	}
	defer m.Close()
	echo := echoServer(t)

	type client struct {
		svc    *frpclient.Service
		done   chan error
		logins atomic.Int32
	}
	// run 启动一个 frpc。fresh 为 true 时每次登录前换一张新票据（像客户端那样），否则一直用 metas 里的。
	run := func(user string, metasFor func() map[string]string, fresh bool, names []string, ports ...int) *client {
		t.Helper()
		cl := &client{done: make(chan error, 1)}
		// 节点没有 token：frpc 也不设
		common := &v1.ClientCommonConfig{ServerAddr: "127.0.0.1", ServerPort: bind, User: user, Metadatas: metasFor()}
		f := false
		common.Transport.TLS.Enable = &f
		if err := common.Complete(); err != nil {
			t.Fatal(err)
		}
		var proxies []v1.ProxyConfigurer
		for i, p := range ports {
			pc := &v1.TCPProxyConfig{RemotePort: p}
			pc.Name, pc.Type = names[i], "tcp"
			pc.LocalIP, pc.LocalPort = "127.0.0.1", echo
			proxies = append(proxies, pc)
		}
		cs := source.NewConfigSource()
		if err := cs.ReplaceAll(proxies, nil); err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		opts := frpclient.ServiceOptions{Common: common, ConfigSourceAggregator: source.NewAggregator(cs),
			ConnectorCreator: func(ctx context.Context, c *v1.ClientCommonConfig) frpclient.Connector {
				cl.logins.Add(1)
				if fresh {
					mu.Lock()
					c.Metadatas = metasFor()
					mu.Unlock()
				}
				return frpclient.NewConnector(ctx, c)
			}}
		svc, err := frpclient.NewService(opts)
		if err != nil {
			t.Fatal(err)
		}
		cl.svc = svc
		go func() { cl.done <- svc.Run(context.Background()) }()
		t.Cleanup(svc.Close)
		return cl
	}
	newTicket := func(sess string) func() map[string]string {
		return func() map[string]string { return metas(ticket(sess)) }
	}
	expectLoginFail := func(user string, m map[string]string, want string) {
		t.Helper()
		cl := run(user, func() map[string]string { return m }, false, nil)
		select {
		case err := <-cl.done:
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("登录应失败（%s）：%v", want, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("登录没有失败")
		}
		cl.svc.Close()
	}
	expectLoginFail("alice", metas("mfc_static"), "官方节点")
	expectLoginFail("bob", metas(ticket("s1")), "alice")
	used := ticket("s1")
	if r, _ := login(t, s, "alice", used, ""); r != "" {
		t.Fatal(r)
	}
	expectLoginFail("alice", metas(used), "用过")

	// 合法用户：两个范围内的代理成功，第三个超过数量，范围外的被拒绝
	c1 := run("alice", newTicket("sess-1"), true, []string{"p0", "p1", "p2", "p3"}, 20001, 20002, 20003, 30000)
	status := func(cl *client, name string) (string, string) {
		if st, ok := cl.svc.StatusExporter().GetProxyStatus(name); ok && st != nil {
			return st.Phase, st.Err
		}
		return "", ""
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("超时：%s", what)
	}
	waitFor("代理状态稳定", func() bool {
		for _, n := range []string{"p0", "p1", "p2", "p3"} {
			if ph, _ := status(c1, n); ph != "running" && ph != "start error" {
				return false
			}
		}
		return true
	})
	running, limited, runningPort := 0, 0, 0
	for i, n := range []string{"p0", "p1", "p2"} {
		ph, e := status(c1, n)
		switch {
		case ph == "running":
			running++
			runningPort = 20001 + i
		case strings.Contains(e, "最多同时运行 2"):
			limited++
		}
	}
	if running != 2 || limited != 1 {
		t.Fatalf("应有 2 个成功、1 个超过数量：running=%d limited=%d", running, limited)
	}
	if _, e := status(c1, "p3"); !strings.Contains(e, "不在你可以用的范围") {
		t.Fatalf("范围外的端口：%q", e)
	}
	if !echoOK(runningPort) {
		t.Fatal("流量应能通")
	}

	// 断线重连：frps 重启，frpc 自动重连，每次登录都换了新票据（旧票据已经用过，重发会被拒绝）
	if err := m.Restart(); err != nil {
		t.Fatal(err)
	}
	waitFor("重连后流量恢复", func() bool { return c1.logins.Load() >= 2 && echoOK(runningPort) })

	// 同一会话在另一个 frpc 登录：后登录的生效，原来的 frpc 不再接受新连接
	c2 := run("alice", newTicket("sess-1"), true, []string{"q0"}, 20009)
	waitFor("新 frpc 的代理运行", func() bool { ph, _ := status(c2, "q0"); return ph == "running" && echoOK(20009) })
	waitFor("原来的 frpc 失效", func() bool { return !echoOK(runningPort) })
}

// 面板轮换密钥后，旧密钥签的票据给出"密钥已更换"的原因（客户端据此自动领新票据重连），
// 没带票据的才提示要用 MoeFrp 客户端。
func TestStaleKeyReason(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "access.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(table(true)); err != nil {
		t.Fatal(err)
	}
	oldKey := nodeticket.NodeKey([]byte("another-master-key-0123456789abc"), "node-1")
	now := time.Now().Unix()
	stale := nodeticket.Sign(oldKey, nodeticket.Claims{User: "u1", Session: "s", Node: "node-1", Gen: 1, Issued: now, Expires: now + 900,
		Nonce: nodeticket.NewNonce(), Device: nodeticket.DeviceTag(oldKey, "dev-1")})
	if r, _ := login(t, s, "alice", stale, ""); !strings.Contains(r, "更换了节点密钥") {
		t.Fatalf("旧密钥的票据：%q", r)
	}
	if r, _ := login(t, s, "alice", "", ""); !strings.Contains(r, "MoeFrp 客户端") {
		t.Fatalf("没有票据：%q", r)
	}
}
