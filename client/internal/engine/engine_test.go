package engine

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/engine/frpstest"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// 集成测试：在进程内起 frps，让引擎连上去（PLAN 第 11 节）。每次升级 frp 都必须通过。

const testToken = "engine-test-token"

var (
	sinkOnce sync.Once
	sink     *LogSink
)

// 整个测试进程只装一次全局日志出口（与 PLAN 6.3.6 一致）
func globalSink() *LogSink {
	sinkOnce.Do(func() {
		sink = NewLogSink(20000)
		sink.InstallGlobal("info")
	})
	return sink
}

func newEngine(t *testing.T, opts Options) *Engine {
	t.Helper()
	e := New(globalSink(), opts)
	t.Cleanup(func() {
		_ = e.Shutdown(0, 10*time.Second)
		e.Close()
	})
	return e
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func waitState(t *testing.T, e *Engine, want State, timeout time.Duration) Status {
	t.Helper()
	waitFor(t, "state "+string(want), timeout, func() bool { return e.Status().State == want })
	return e.Status()
}

func proxyStatus(e *Engine, name string) (ProxyStatus, bool) {
	for _, p := range e.ProxyStatuses() {
		if p.Name == name {
			return p, true
		}
	}
	return ProxyStatus{}, false
}

func waitProxyPhase(t *testing.T, e *Engine, name, phase string) ProxyStatus {
	t.Helper()
	waitFor(t, name+" phase "+phase, 10*time.Second, func() bool {
		st, ok := proxyStatus(e, name)
		return ok && st.Phase == phase
	})
	st, _ := proxyStatus(e, name)
	return st
}

func countLines(substr string) int {
	n := 0
	for _, l := range globalSink().Lines() {
		if strings.Contains(l.Text, substr) {
			n++
		}
	}
	return n
}

func echoRoundTrip(port int, msg string) error {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(c, "%s\n", msg); err != nil {
		return err
	}
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != msg {
		return fmt.Errorf("echo mismatch: got %q want %q", got, msg)
	}
	return nil
}

type fixture struct {
	frps     *frpstest.Frps
	echoPort int
}

func startFixture(t *testing.T) *fixture {
	t.Helper()
	echo, err := frpstest.StartEcho("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	frps, err := frpstest.StartFrps(0, testToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(frps.Close)
	return &fixture{frps: frps, echoPort: echo.Addr().(*net.TCPAddr).Port}
}

func (f *fixture) profile(proxies ...model.Proxy) model.Profile {
	return model.Profile{
		ID:   model.NewID(),
		Name: "it",
		Nodes: []model.Node{{
			ID: model.NewID(), Remark: "local frps", ServerAddr: "127.0.0.1", ServerPort: f.frps.Port, Token: testToken,
		}},
		Proxies:  proxies,
		Visitors: []model.Visitor{},
	}
}

func (f *fixture) tcpProxy(name string, remote int) model.Proxy {
	return model.Proxy{ID: model.NewID(), Name: name, Type: model.ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: f.echoPort, RemotePort: remote}
}

func freePort(t *testing.T) int {
	t.Helper()
	p, err := frpstest.FreePort()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func statesOf(ts []Transition) []State {
	out := make([]State, 0, len(ts))
	for _, x := range ts {
		out = append(out, x.To)
	}
	return out
}

// 状态机 Connecting → Running；tcp 真实转发；热更新增删代理不重连；校验失败的更新不提交；
// 停止后端口释放；关闭后的 Service 不复用，重新启动能工作。
func TestEngineEndToEnd(t *testing.T) {
	f := startFixture(t)
	remote1, remote2 := freePort(t), freePort(t)
	p := f.profile(f.tcpProxy("echo1", remote1))
	disabled := f.tcpProxy("disabled", remote2)
	disabled.Enabled = false
	p.Proxies = append(p.Proxies, disabled)
	nodeID := p.Nodes[0].ID

	e := newEngine(t, Options{})
	var mu sync.Mutex
	var events []Status
	e.SetHandlers(func(s Status) { mu.Lock(); events = append(events, s); mu.Unlock() }, nil)

	if err := e.Start(p, nodeID); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, e, StateRunning, 10*time.Second)
	if got := statesOf(e.Transitions()); len(got) < 2 || got[0] != StateConnecting || got[1] != StateRunning {
		t.Fatalf("transitions: %v", got)
	}
	if st.RunID == "" || st.Logins != 1 || st.ProfileID != p.ID || st.NodeID != nodeID {
		t.Fatalf("status after login: %+v", st)
	}
	tag := fmt.Sprintf("[run-%d]", e.gen)
	if countLines(tag+" try to connect to server") != 1 {
		t.Fatalf("frp log lines should carry the %s prefix", tag)
	}

	ps := waitProxyPhase(t, e, "echo1", "running")
	if ps.ID != p.Proxies[0].ID || !strings.HasSuffix(ps.RemoteAddr, ":"+strconv.Itoa(remote1)) {
		t.Fatalf("proxy status: %+v", ps)
	}
	if _, ok := proxyStatus(e, "disabled"); ok {
		t.Fatalf("disabled proxy must not be polled")
	}
	if err := echoRoundTrip(remote1, "hello-through-frp"); err != nil {
		t.Fatalf("forward via echo1: %v", err)
	}

	// 热更新：加一个代理
	serverLogins := countLines("client login info")
	p2 := p
	p2.Proxies = []model.Proxy{p.Proxies[0], f.tcpProxy("echo2", remote2)}
	mode, err := e.Update(p2)
	if err != nil || mode != UpdateHot {
		t.Fatalf("update add: mode=%s err=%v", mode, err)
	}
	waitProxyPhase(t, e, "echo2", "running")
	if err := echoRoundTrip(remote2, "hello-echo2"); err != nil {
		t.Fatalf("forward via echo2: %v", err)
	}

	// 一条长连接贯穿后面的热更新，验证"连接不断"
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(remote2))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rd := bufio.NewReader(conn)
	ping := func(msg string) error {
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := fmt.Fprintf(conn, "%s\n", msg); err != nil {
			return err
		}
		got, err := rd.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimSpace(got) != msg {
			return fmt.Errorf("got %q", got)
		}
		return nil
	}
	if err := ping("before"); err != nil {
		t.Fatal(err)
	}

	// 热更新：删掉 echo1，改名字不相关的字段（备注），连接不断
	p3 := p2
	p3.Proxies = []model.Proxy{p2.Proxies[1]}
	p3.Nodes = []model.Node{p.Nodes[0]}
	p3.Nodes[0].Remark = "renamed"
	if mode, err := e.Update(p3); err != nil || mode != UpdateHot {
		t.Fatalf("update remove: mode=%s err=%v", mode, err)
	}
	waitFor(t, "echo1 removed", 10*time.Second, func() bool { _, ok := proxyStatus(e, "echo1"); return !ok })
	waitFor(t, "remote1 released", 10*time.Second, func() bool { return frpstest.PortFree(remote1) })
	if err := ping("after-hot-update"); err != nil {
		t.Fatalf("long-lived connection broken by hot update: %v", err)
	}
	st2 := e.Status()
	if st2.RunID != st.RunID || st2.Logins != 1 || countLines("client login info") != serverLogins || st2.NodeLabel != "renamed" {
		t.Fatalf("hot update caused a reconnect: before=%+v after=%+v", st, st2)
	}

	// 校验失败（重名）：不提交，运行中的隧道保持原样
	bad := p3
	bad.Proxies = []model.Proxy{p3.Proxies[0], f.tcpProxy("echo2", 0)}
	if mode, err := e.Update(bad); mode != UpdateNone || apierr.CodeOf(err) != apierr.ValidationFailed {
		t.Fatalf("duplicate update should be rejected, mode=%s err=%v", mode, err)
	}
	if _, ok := proxyStatus(e, "echo2"); !ok || e.Status().State != StateRunning {
		t.Fatalf("running tunnel changed after rejected update")
	}
	if err := ping("after-rejected-update"); err != nil {
		t.Fatal(err)
	}

	// 停止：状态 Stopping → Idle，端口释放
	t0 := time.Now()
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if e.Status().State != StateStopping {
		t.Fatalf("Stop should switch to stopping immediately, got %s", e.Status().State)
	}
	waitState(t, e, StateIdle, 5*time.Second)
	t.Logf("stop took %dms", time.Since(t0).Milliseconds())
	waitFor(t, "remote2 released", 10*time.Second, func() bool { return frpstest.PortFree(remote2) })
	if len(e.ProxyStatuses()) != 0 {
		t.Fatalf("proxy statuses should be cleared after stop")
	}

	// 重启：必须新建 Service
	if err := e.Start(p, nodeID); err != nil {
		t.Fatal(err)
	}
	waitProxyPhase(t, e, "echo1", "running")
	if err := echoRoundTrip(remote1, "after-restart"); err != nil {
		t.Fatalf("forward after restart: %v", err)
	}
	if e.Status().RunID == st.RunID {
		t.Fatalf("restart should get a new run id")
	}
	mu.Lock()
	n := len(events)
	mu.Unlock()
	if n == 0 {
		t.Fatalf("status handler never called")
	}
}

func TestEngineWrongToken(t *testing.T) {
	f := startFixture(t)
	p := f.profile(f.tcpProxy("x", 0))
	p.Nodes[0].Token = "wrong-token"
	e := newEngine(t, Options{})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, e, StateFailed, 15*time.Second)
	if st.Error == nil || st.Error.Code != apperr.NodeTokenInvalid {
		t.Fatalf("want node_token_invalid, got %+v", st.Error)
	}
	if strings.Contains(st.Error.Message, "loginFailExit") {
		t.Fatalf("frp's suffix should be stripped: %q", st.Error.Message)
	}
	// Failed 时 Stop 清掉错误回到 Idle
	_ = e.Stop()
	if e.Status().State != StateIdle {
		t.Fatalf("stop from failed: %s", e.Status().State)
	}
}

// 连不上服务器时 frp 的原文是 "session shutdown"（坑 8），引擎要用 TCP 探测给出"连接被拒绝"。
func TestEngineServerRefused(t *testing.T) {
	port := freePort(t) // 没有人监听
	p := model.Profile{ID: model.NewID(), Name: "refused",
		Nodes:   []model.Node{{ID: model.NewID(), ServerAddr: "127.0.0.1", ServerPort: port, Token: testToken}},
		Proxies: []model.Proxy{}, Visitors: []model.Visitor{}}
	e := newEngine(t, Options{})
	t0 := time.Now()
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, e, StateFailed, 20*time.Second)
	t.Logf("refused: failed after %dms, error=%+v", time.Since(t0).Milliseconds(), st.Error)
	if st.Error == nil || st.Error.Code != apperr.NodeRefused {
		t.Fatalf("want node_refused, got %+v", st.Error)
	}
}

func TestEngineServerDNS(t *testing.T) {
	p := model.Profile{ID: model.NewID(), Name: "dns",
		Nodes:   []model.Node{{ID: model.NewID(), ServerAddr: "no-such-host.invalid", ServerPort: 7000, Token: testToken}},
		Proxies: []model.Proxy{}, Visitors: []model.Visitor{}}
	e := newEngine(t, Options{})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, e, StateFailed, 20*time.Second)
	if st.Error == nil || st.Error.Code != apperr.NodeDNS {
		t.Fatalf("want node_dns, got %+v", st.Error)
	}
}

// 拨号途中停止：Run 返回 "login to the server failed: <nil>"，不能报成登录失败（坑 6）。
func TestStopWhileConnecting(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr string
		port int
	}{
		{"refused", "127.0.0.1", 1},
		{"blackhole", "10.255.255.1", 7000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := model.Profile{ID: model.NewID(), Name: tc.name,
				Nodes:   []model.Node{{ID: model.NewID(), ServerAddr: tc.addr, ServerPort: tc.port, Token: testToken}},
				Proxies: []model.Proxy{}, Visitors: []model.Visitor{}}
			e := newEngine(t, Options{})
			if err := e.Start(p, p.Nodes[0].ID); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			t0 := time.Now()
			_ = e.Stop()
			runErr, ok := e.Wait(15 * time.Second)
			if !ok {
				t.Fatalf("Run did not return")
			}
			st := e.Status()
			t.Logf("Run returned after %dms: %v; state=%s", time.Since(t0).Milliseconds(), runErr, st.State)
			if st.State != StateIdle || st.Error != nil {
				t.Fatalf("stop while connecting must end in idle without error, got %+v", st)
			}
			for _, tr := range e.Transitions() {
				if tr.To == StateFailed {
					t.Fatalf("must never pass through failed: %v", statesOf(e.Transitions()))
				}
			}
		})
	}
}

// 换节点：旧 Service 还在拨号（最多约 5 秒），必须等它的 Run 返回再启动新的（坑 6、9）。
func TestSwitchNodeWaitsForOldRun(t *testing.T) {
	f := startFixture(t)
	remote := freePort(t)
	p := f.profile(f.tcpProxy("echo", remote))
	good := p.Nodes[0]
	bad := model.Node{ID: model.NewID(), Remark: "blackhole", ServerAddr: "10.255.255.1", ServerPort: 7000, Token: testToken}
	p.Nodes = append(p.Nodes, bad)

	e := newEngine(t, Options{})
	if err := e.Start(p, bad.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := e.Start(p, good.ID); err != nil {
		t.Fatal(err)
	}
	st := e.Status()
	if st.State != StateStopping || !st.Pending {
		t.Fatalf("switch should report stopping+pending, got %+v", st)
	}
	st = waitState(t, e, StateRunning, 20*time.Second)
	if st.NodeID != good.ID || st.Error != nil {
		t.Fatalf("after switch: %+v", st)
	}
	waitProxyPhase(t, e, "echo", "running")
	if err := echoRoundTrip(remote, "after-switch"); err != nil {
		t.Fatal(err)
	}
	got := statesOf(e.Transitions())
	t.Logf("transitions: %v", got)
	for _, s := range got {
		if s == StateFailed {
			t.Fatalf("switch must not pass through failed: %v", got)
		}
	}
}

// 热更新时节点的连接参数变了：停掉再重新启动。
func TestUpdateNodeChangeRestarts(t *testing.T) {
	f := startFixture(t)
	remote := freePort(t)
	p := f.profile(f.tcpProxy("echo", remote))
	e := newEngine(t, Options{})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	first := waitState(t, e, StateRunning, 10*time.Second)
	p2 := p
	p2.Nodes = []model.Node{p.Nodes[0]}
	p2.Nodes[0].User = "someone"
	mode, err := e.Update(p2)
	if err != nil || mode != UpdateRestart {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	waitFor(t, "new run", 10*time.Second, func() bool {
		s := e.Status()
		return s.State == StateRunning && s.RunID != first.RunID
	})
	ps := waitProxyPhase(t, e, "echo", "running")
	if err := echoRoundTrip(remote, "after-node-change"); err != nil {
		t.Fatalf("%v (%+v)", err, ps)
	}
}

// 代理重名必须被拦下来，不能被静默覆盖（坑 5）。
func TestStartRejectsDuplicateNames(t *testing.T) {
	f := startFixture(t)
	p := f.profile(f.tcpProxy("dup", 0), f.tcpProxy("dup", 0))
	e := newEngine(t, Options{})
	err := e.Start(p, p.Nodes[0].ID)
	assertFieldError(t, err, "proxies[1].name", validate.ReasonDuplicate)
	if e.Status().State != StateIdle {
		t.Fatalf("nothing should start, state=%s", e.Status().State)
	}
}

// 远程端口冲突：一个代理 running，另一个 start error，Err 里是 port already used。
// 端口被 frps 机器上别的程序占用时同样如此。
func TestRemotePortConflict(t *testing.T) {
	f := startFixture(t)
	remote := freePort(t)
	p := f.profile(f.tcpProxy("a", remote), f.tcpProxy("b", remote))
	e := newEngine(t, Options{})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "one proxy in start error", 10*time.Second, func() bool {
		a, _ := proxyStatus(e, "a")
		b, _ := proxyStatus(e, "b")
		return (a.Phase == "running" && b.Phase == "start error") || (b.Phase == "running" && a.Phase == "start error")
	})
	a, _ := proxyStatus(e, "a")
	b, _ := proxyStatus(e, "b")
	failed := a
	if b.Phase == "start error" {
		failed = b
	}
	t.Logf("conflict: a=%+v b=%+v", a, b)
	if failed.Code != apperr.ProxyPortInUse || !strings.Contains(failed.Err, "port already used") {
		t.Fatalf("want proxy_port_in_use, got %+v", failed)
	}

	// 被别的程序占用
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	occupied := busy.Addr().(*net.TCPAddr).Port
	p2 := p
	p2.Proxies = []model.Proxy{p.Proxies[0], f.tcpProxy("c", occupied)}
	if _, err := e.Update(p2); err != nil {
		t.Fatal(err)
	}
	c := waitProxyPhase(t, e, "c", "start error")
	t.Logf("occupied by another program: %+v", c)
	if c.Code != apperr.ProxyPortInUse && c.Code != apperr.ProxyPortUnavail {
		t.Fatalf("want a port error, got %+v", c)
	}
}

// 同名代理已被别的客户端注册在 frps 上：start error，原因是 already exists。
// 同时验证两个引擎同时运行时，日志前缀能把两个 Service 的日志分开。
func TestProxyNameTakenOnServer(t *testing.T) {
	f := startFixture(t)
	p1 := f.profile(f.tcpProxy("shared-name", 0))
	e1 := newEngine(t, Options{})
	if err := e1.Start(p1, p1.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitProxyPhase(t, e1, "shared-name", "running")
	run1 := e1.Status().RunID

	p2 := f.profile(f.tcpProxy("shared-name", 0))
	e2 := newEngine(t, Options{})
	if err := e2.Start(p2, p2.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	st := waitProxyPhase(t, e2, "shared-name", "start error")
	if st.Code != apperr.ProxyNameTaken {
		t.Fatalf("want proxy_name_taken, got %+v", st)
	}
	s1 := e1.Status()
	if s1.RunID != run1 || s1.Logins != 1 || s1.State != StateRunning {
		t.Fatalf("engine 1 picked up engine 2's log lines: %+v", s1)
	}
}

// Stopping 期间对外接口不阻塞：GracefulClose(2s) 期间 Status / ProxyStatuses 立即返回（坑 7）。
func TestStatusNotBlockedWhileStopping(t *testing.T) {
	f := startFixture(t)
	p := f.profile(f.tcpProxy("a", 0))
	e := newEngine(t, Options{StopGrace: 2 * time.Second, PollInterval: 50 * time.Millisecond})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitProxyPhase(t, e, "a", "running")
	t0 := time.Now()
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Fatalf("Stop blocked for %s", d)
	}
	time.Sleep(300 * time.Millisecond) // 让轮询 goroutine 有机会撞上 frp 的锁
	for i := 0; i < 10; i++ {
		t1 := time.Now()
		_ = e.Status()
		_ = e.ProxyStatuses()
		if d := time.Since(t1); d > 50*time.Millisecond {
			t.Fatalf("status call blocked for %s while stopping", d)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e.Status().State != StateStopping {
		t.Fatalf("expected still stopping, got %s", e.Status().State)
	}
	waitState(t, e, StateIdle, 5*time.Second)
}

// stcp：一个引擎提供 stcp 代理，另一个引擎用 stcp 访客访问（两边都是 frp 自带的 client 包）。
func TestSTCPVisitor(t *testing.T) {
	f := startFixture(t)
	provider := f.profile(model.Proxy{ID: model.NewID(), Name: "secret-echo", Type: model.ProxySTCP, Enabled: true,
		LocalIP: "127.0.0.1", LocalPort: f.echoPort, SecretKey: testSecret})
	e1 := newEngine(t, Options{})
	if err := e1.Start(provider, provider.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitProxyPhase(t, e1, "secret-echo", "running")

	bind := freePort(t)
	visitor := f.profile()
	visitor.Visitors = []model.Visitor{{ID: model.NewID(), Name: "visit-echo", Type: model.VisitorSTCP, Enabled: true,
		ServerName: "secret-echo", SecretKey: testSecret, BindAddr: "127.0.0.1", BindPort: bind}}
	e2 := newEngine(t, Options{})
	if err := e2.Start(visitor, visitor.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, e2, StateRunning, 10*time.Second)
	var lastErr error
	waitFor(t, "stcp round trip", 10*time.Second, func() bool {
		lastErr = echoRoundTrip(bind, "via-stcp-visitor")
		return lastErr == nil
	})

	// 错误的 secretKey：连接被拒
	visitor2 := visitor
	visitor2.Visitors = []model.Visitor{visitor.Visitors[0]}
	visitor2.Visitors[0].SecretKey = "wrong-secret-wrong-secret-wrong!"
	if mode, err := e2.Update(visitor2); err != nil || mode != UpdateHot {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	waitFor(t, "visitor rebound", 5*time.Second, func() bool { return echoRoundTrip(bind, "x") != nil })
}

// 登录成功后 frps 断开：Reconnecting（带原因），frps 恢复后自动回到 Running。
func TestReconnect(t *testing.T) {
	echo, err := frpstest.StartEcho("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	frps, err := frpstest.StartFrps(0, testToken)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{frps: frps, echoPort: echo.Addr().(*net.TCPAddr).Port}
	p := f.profile(f.tcpProxy("echo", 0))
	e := newEngine(t, Options{})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, StateRunning, 10*time.Second)

	frps.Close()
	st := waitState(t, e, StateReconnecting, 15*time.Second)
	waitFor(t, "reconnect error explained", 15*time.Second, func() bool {
		s := e.Status()
		return s.Error != nil && s.Error.Code == apperr.NodeRefused
	})
	t.Logf("reconnecting: %+v", e.Status().Error)

	frps2, err := frpstest.StartFrps(frps.Port, testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer frps2.Close()
	st = waitState(t, e, StateRunning, 40*time.Second)
	if st.Logins != 2 || st.Error != nil {
		t.Fatalf("after reconnect: %+v", st)
	}
}

// 退出流程：GracefulClose(500ms)，最多等 2 秒，frps 上的端口被释放（PLAN 6.3.8）。
func TestShutdown(t *testing.T) {
	f := startFixture(t)
	remote := freePort(t)
	p := f.profile(f.tcpProxy("echo", remote))
	e := newEngine(t, Options{})
	if err := e.Start(p, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	waitProxyPhase(t, e, "echo", "running")
	t0 := time.Now()
	if err := e.Shutdown(500*time.Millisecond, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	t.Logf("shutdown took %dms", time.Since(t0).Milliseconds())
	waitFor(t, "remote released", 5*time.Second, func() bool { return frpstest.PortFree(remote) })

	// 拨号中退出：超时直接返回，不卡住
	bad := model.Profile{ID: model.NewID(), Name: "bh",
		Nodes:   []model.Node{{ID: model.NewID(), ServerAddr: "10.255.255.1", ServerPort: 7000, Token: testToken}},
		Proxies: []model.Proxy{}, Visitors: []model.Visitor{}}
	if err := e.Start(bad, bad.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	t1 := time.Now()
	err := e.Shutdown(500*time.Millisecond, 2*time.Second)
	if d := time.Since(t1); d > 2500*time.Millisecond {
		t.Fatalf("shutdown waited %s", d)
	}
	t.Logf("shutdown while dialing: %v after %dms", err, time.Since(t1).Milliseconds())
}

func TestPingNodes(t *testing.T) {
	f := startFixture(t)
	nodes := []model.Node{
		{ID: "ok", ServerAddr: "127.0.0.1", ServerPort: f.frps.Port},
		{ID: "refused", ServerAddr: "127.0.0.1", ServerPort: freePort(t)},
		{ID: "dns", ServerAddr: "no-such-host.invalid", ServerPort: 7000},
		{ID: "kcp", ServerAddr: "127.0.0.1", ServerPort: 7000, Protocol: model.ProtocolKCP},
	}
	res := PingNodes(t.Context(), nodes, time.Second)
	if !res[0].OK || res[1].Code != apperr.NodeRefused || res[2].Code != apperr.NodeDNS || res[3].Code != apperr.NodeUDPUnprobed {
		t.Fatalf("ping results: %+v", res)
	}
}
