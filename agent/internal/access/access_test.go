package access

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	frpclient "github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	plugin "github.com/fatedier/frp/pkg/plugin/server"

	"moefrp/agent/internal/frpsrv"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/quota"
)

func table(enabled bool) agentproto.AccessTable {
	return agentproto.AccessTable{Enabled: enabled, Users: []agentproto.AccessUser{{
		CredHash: HashCredential("cred-alice"), UserID: "u1", Nickname: "alice",
		Quota: quota.Quota{MaxProxies: 2, Ports: "20000-20010", Types: []string{"tcp", "udp"}},
	}}}
}

func decide(t *testing.T, s *Store, op string, content any) string {
	t.Helper()
	b, _ := json.Marshal(content)
	return s.decide(op, b)
}

func TestDecide(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "access.json"))
	if err != nil {
		t.Fatal(err)
	}
	online := 0
	s.Online = func(user, except string) int { return online }
	metas := map[string]string{api.MetaCredential: "cred-alice"}
	login := plugin.LoginContent{Login: msg.Login{User: "alice", Metas: metas}}

	// 不是官方节点：全部放行
	if r := decide(t, s, plugin.OpLogin, plugin.LoginContent{}); r != "" {
		t.Fatalf("非官方节点应放行：%s", r)
	}
	changed, err := s.Apply(table(true))
	if err != nil || !changed || !s.Enabled() || s.Users() != 1 {
		t.Fatalf("apply: %v %v", changed, err)
	}

	if r := decide(t, s, plugin.OpLogin, login); r != "" {
		t.Fatalf("合法登录：%s", r)
	}
	if r := decide(t, s, plugin.OpLogin, plugin.LoginContent{Login: msg.Login{User: "alice"}}); !strings.Contains(r, "官方节点") {
		t.Fatalf("没有凭证：%q", r)
	}
	if r := decide(t, s, plugin.OpLogin, plugin.LoginContent{Login: msg.Login{User: "bob", Metas: metas}}); !strings.Contains(r, "alice") {
		t.Fatalf("user 不是昵称：%q", r)
	}

	user := plugin.UserInfo{User: "alice", Metas: metas, RunID: "r1"}
	np := func(typ string, port int) string {
		return decide(t, s, plugin.OpNewProxy, plugin.NewProxyContent{User: user, NewProxy: msg.NewProxy{ProxyName: "alice.p", ProxyType: typ, RemotePort: port}})
	}
	if r := np("tcp", 20005); r != "" {
		t.Fatalf("范围内的端口：%s", r)
	}
	if r := np("tcp", 30000); !strings.Contains(r, "20000-20010") {
		t.Fatalf("范围外的端口：%q", r)
	}
	if r := np("tcp", 0); !strings.Contains(r, "指定远程端口") {
		t.Fatalf("随机端口：%q", r)
	}
	if r := np("http", 0); !strings.Contains(r, "http") {
		t.Fatalf("不允许的类型：%q", r)
	}
	online = 2
	if r := np("udp", 20006); !strings.Contains(r, "最多同时运行 2") {
		t.Fatalf("超过数量：%q", r)
	}

	uc := plugin.NewUserConnContent{User: user}
	if r := decide(t, s, plugin.OpNewUserConn, uc); r != "" {
		t.Fatalf("用户连接：%s", r)
	}
	// 撤销：表里没有了，新用户连接被拒绝
	empty := table(true)
	empty.Users = nil
	if changed, _ := s.Apply(empty); changed {
		t.Fatal("开关没变")
	}
	if r := decide(t, s, plugin.OpNewUserConn, uc); r == "" {
		t.Fatal("撤销后应拒绝新连接")
	}

	// 表保存在文件里，重启后还在
	s2, err := Load(s.path)
	if err != nil || !s2.Enabled() || s2.Users() != 0 {
		t.Fatalf("reload: %v", err)
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

// TestWithFrps 用真的 frps（带插件）和 frpc 走一遍：凭证、昵称、端口范围、代理数。
func TestWithFrps(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "access.json"))
	if _, err := s.Apply(table(true)); err != nil {
		t.Fatal(err)
	}
	addr, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	m := frpsrv.New(filepath.Join(t.TempDir(), "frps.toml"))
	m.Plugin = func() *v1.HTTPPluginOptions {
		return &v1.HTTPPluginOptions{Name: "moefrp-access", Addr: addr, Path: "/handler", Ops: PluginOps}
	}
	bind := freePort(t)
	if r := m.Apply(1, fmt.Sprintf("bindAddr = \"127.0.0.1\"\nbindPort = %d\nauth.token = \"public\"\nallowPorts = [{ start = 20000, end = 20010 }]\n", bind)); !r.Applied {
		t.Fatalf("frps: %+v", r)
	}
	defer m.Close()

	run := func(user, cred string, ports ...int) (*frpclient.Service, chan error) {
		t.Helper()
		common := &v1.ClientCommonConfig{ServerAddr: "127.0.0.1", ServerPort: bind, User: user,
			Metadatas: map[string]string{api.MetaCredential: cred}}
		common.Auth.Token = "public"
		f := false
		common.Transport.TLS.Enable = &f
		if err := common.Complete(); err != nil {
			t.Fatal(err)
		}
		var proxies []v1.ProxyConfigurer
		for i, p := range ports {
			pc := &v1.TCPProxyConfig{RemotePort: p}
			pc.Name, pc.Type = fmt.Sprintf("p%d", i), "tcp"
			pc.LocalIP, pc.LocalPort = "127.0.0.1", 1
			proxies = append(proxies, pc)
		}
		cs := source.NewConfigSource()
		if err := cs.ReplaceAll(proxies, nil); err != nil {
			t.Fatal(err)
		}
		svc, err := frpclient.NewService(frpclient.ServiceOptions{Common: common, ConfigSourceAggregator: source.NewAggregator(cs)})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- svc.Run(context.Background()) }()
		return svc, done
	}
	expectLoginFail := func(user, cred, want string) {
		t.Helper()
		svc, done := run(user, cred)
		defer svc.Close()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("登录应失败（%s）：%v", want, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("登录没有失败")
		}
	}
	expectLoginFail("alice", "wrong", "官方节点")
	expectLoginFail("bob", "cred-alice", "alice")

	// 合法用户：两个范围内的代理成功，第三个超过数量，范围外的被拒绝
	svc, _ := run("alice", "cred-alice", 20001, 20002, 20003, 30000)
	defer svc.Close()
	status := func(name string) (string, string) {
		if st, ok := svc.StatusExporter().GetProxyStatus(name); ok && st != nil {
			return st.Phase, st.Err
		}
		return "", ""
	}
	deadline := time.Now().Add(10 * time.Second)
	settled := func() bool {
		for _, n := range []string{"p0", "p1", "p2", "p3"} {
			if ph, _ := status(n); ph != "running" && ph != "start error" {
				return false
			}
		}
		return true
	}
	for time.Now().Before(deadline) && !settled() {
		time.Sleep(100 * time.Millisecond)
	}
	running, limited := 0, 0
	for _, n := range []string{"p0", "p1", "p2"} {
		ph, e := status(n)
		switch {
		case ph == "running":
			running++
		case strings.Contains(e, "最多同时运行 2"):
			limited++
		}
	}
	if running != 2 || limited != 1 {
		t.Fatalf("应有 2 个成功、1 个超过数量：running=%d limited=%d", running, limited)
	}
	if _, e := status("p3"); !strings.Contains(e, "不在你可以用的范围") {
		t.Fatalf("范围外的端口：%q", e)
	}
}
