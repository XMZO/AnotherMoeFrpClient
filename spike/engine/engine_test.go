package engine

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	golog "github.com/fatedier/golib/log"

	"spike/engine/frpstest"
)

const token = "spike-token"

var (
	sinkOnce sync.Once
	sink     *LogSink
)

// 整个测试进程只装一次全局日志出口（与 PLAN 6.3.6 一致）
func globalSink() *LogSink {
	sinkOnce.Do(func() {
		sink = NewLogSink(5000)
		sink.InstallGlobal(golog.InfoLevel)
	})
	return sink
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

func countLines(s *LogSink, substr string) int {
	n := 0
	for _, l := range s.Lines() {
		if strings.Contains(l, substr) {
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

func report(t *testing.T, format string, args ...any) {
	t.Helper()
	line := fmt.Sprintf(format, args...)
	t.Log(line)
	if f := os.Getenv("SPIKE_REPORT"); f != "" {
		if fh, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(fh, line)
			fh.Close()
		}
	}
}

func TestEngineEndToEnd(t *testing.T) {
	s := globalSink()
	goroutinesBefore := runtime.NumGoroutine()

	echo, err := frpstest.StartEcho("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoPort := echo.Addr().(*net.TCPAddr).Port

	frps, err := frpstest.StartFrps(0, token)
	if err != nil {
		t.Fatal(err)
	}
	frpsClosed := false
	defer func() {
		if !frpsClosed {
			frps.Close()
		}
	}()

	remote1, _ := frpstest.FreePort()
	remote2, _ := frpstest.FreePort()
	node := Node{ServerAddr: "127.0.0.1", ServerPort: frps.Port, Token: token}
	p1 := Proxy{Name: "echo1", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: echoPort, RemotePort: remote1}
	p2 := Proxy{Name: "echo2", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: echoPort, RemotePort: remote2}
	pDisabled := Proxy{Name: "disabled", Type: "tcp", Enabled: false, LocalIP: "127.0.0.1", LocalPort: echoPort, RemotePort: remote2}

	e := New(s)
	t0 := time.Now()
	if err := e.Start(node, []Proxy{p1, pDisabled}); err != nil {
		t.Fatal(err)
	}

	// 3) 截获日志并识别 login to server success
	waitFor(t, "login success via log classification", 10*time.Second, func() bool { return e.Snapshot().State == StateRunning })
	loginMs := time.Since(t0).Milliseconds()
	snap := e.Snapshot()
	report(t, "login: state=%s runID=%s logins=%d connects=%d after %dms", snap.State, snap.RunID, snap.Logins, snap.Connects, loginMs)
	if countLines(s, "login to server success") != 1 {
		t.Fatalf("expected exactly one 'login to server success' line")
	}

	// 4) GetProxyStatus 读到 running
	waitFor(t, "echo1 running", 10*time.Second, func() bool {
		st, ok := e.ProxyStatus("echo1")
		return ok && st.Phase == "running"
	})
	st, _ := e.ProxyStatus("echo1")
	report(t, "GetProxyStatus(echo1): phase=%q remoteAddr=%q err=%q after %dms", st.Phase, st.RemoteAddr, st.Err, time.Since(t0).Milliseconds())
	if _, ok := e.ProxyStatus("disabled"); ok {
		t.Fatalf("disabled proxy should have been filtered out by FilterClientConfigurers")
	}

	// 2) 真实转发
	if err := echoRoundTrip(remote1, "hello-through-frp"); err != nil {
		t.Fatalf("forward via echo1 failed: %v", err)
	}
	report(t, "forward: 127.0.0.1:%d -> frps -> frpc -> echo 127.0.0.1:%d OK", remote1, echoPort)

	// 5) UpdateConfigSource 增删代理，确认不重连
	runID := snap.RunID
	loginLines := countLines(s, "login to server success")
	connectLines := countLines(s, "try to connect to server")
	serverLogins := countLines(s, "client login info")

	if err := e.Update([]Proxy{p1, p2}); err != nil {
		t.Fatalf("update add echo2: %v", err)
	}
	waitFor(t, "echo2 running", 10*time.Second, func() bool {
		st, ok := e.ProxyStatus("echo2")
		return ok && st.Phase == "running"
	})
	if err := echoRoundTrip(remote2, "hello-echo2"); err != nil {
		t.Fatalf("forward via echo2 failed: %v", err)
	}
	if err := e.Update([]Proxy{p2}); err != nil {
		t.Fatalf("update remove echo1: %v", err)
	}
	waitFor(t, "echo1 removed", 10*time.Second, func() bool {
		_, ok := e.ProxyStatus("echo1")
		return !ok
	})
	waitFor(t, "remote1 released on frps", 10*time.Second, func() bool { return frpstest.PortFree(remote1) })
	if err := echoRoundTrip(remote2, "still-alive"); err != nil {
		t.Fatalf("echo2 broken after removing echo1: %v", err)
	}
	snap2 := e.Snapshot()
	report(t, "hot update: runID %s -> %s, login lines %d -> %d, connect lines %d -> %d, frps 'client login info' %d -> %d",
		runID, snap2.RunID, loginLines, countLines(s, "login to server success"),
		connectLines, countLines(s, "try to connect to server"),
		serverLogins, countLines(s, "client login info"))
	if snap2.RunID != runID || snap2.Logins != 1 ||
		countLines(s, "login to server success") != loginLines ||
		countLines(s, "try to connect to server") != connectLines ||
		countLines(s, "client login info") != serverLogins {
		t.Fatalf("hot update caused a reconnect")
	}

	// 校验失败的更新不应提交：重复的远程端口在客户端校验不出来，用非法类型代替
	if err := e.Update([]Proxy{p2, {Name: "bad", Type: "nope", Enabled: true}}); err == nil {
		t.Fatalf("expected validation error for bad proxy type")
	} else {
		report(t, "invalid update rejected before submit: %v", err)
	}
	if _, ok := e.ProxyStatus("echo2"); !ok {
		t.Fatalf("echo2 lost after rejected update")
	}

	// 6) GracefulClose 后 Run 返回、端口释放
	tStop := time.Now()
	if err := e.Stop(500*time.Millisecond, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	stopMs := time.Since(tStop).Milliseconds()
	runErr, returned := e.Wait(time.Second)
	if !returned {
		t.Fatalf("Run did not return")
	}
	waitFor(t, "remote2 released on frps", 10*time.Second, func() bool { return frpstest.PortFree(remote2) })
	report(t, "stop: GracefulClose(500ms) -> Run returned in %dms, err=%v, state=%s, remote port %d free",
		stopMs, runErr, e.Snapshot().State, remote2)
	if e.Snapshot().State != StateIdle {
		t.Fatalf("expected idle after stop, got %s", e.Snapshot().State)
	}

	// 关闭后的 Service 不能复用：Start 必须新建 Service（PLAN 6.3.5）；这里验证重启后又能工作
	if err := e.Start(node, []Proxy{p1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart running", 10*time.Second, func() bool {
		st, ok := e.ProxyStatus("echo1")
		return ok && st.Phase == "running"
	})
	if err := echoRoundTrip(remote1, "after-restart"); err != nil {
		t.Fatalf("forward after restart: %v", err)
	}
	report(t, "restart with a new Service: OK (runID=%s)", e.Snapshot().RunID)
	if err := e.Stop(500*time.Millisecond, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	frps.Close()
	frpsClosed = true
	waitFor(t, "frps bind port released", 5*time.Second, func() bool { return frpstest.PortFree(frps.Port) })
	time.Sleep(500 * time.Millisecond)
	report(t, "frps closed, bind port %d free; goroutines before=%d after=%d", frps.Port, goroutinesBefore, runtime.NumGoroutine())
}

func TestEngineWrongToken(t *testing.T) {
	s := globalSink()
	frps, err := frpstest.StartFrps(0, token)
	if err != nil {
		t.Fatal(err)
	}
	defer frps.Close()

	e := New(s)
	t0 := time.Now()
	if err := e.Start(Node{ServerAddr: "127.0.0.1", ServerPort: frps.Port, Token: "wrong-token"},
		[]Proxy{{Name: "x", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 1, RemotePort: 0}}); err != nil {
		t.Fatal(err)
	}
	runErr, returned := e.Wait(15 * time.Second)
	if !returned {
		t.Fatalf("Run did not return with wrong token (LoginFailExit should default to true)")
	}
	if runErr == nil {
		t.Fatalf("expected error from Run")
	}
	snap := e.Snapshot()
	report(t, "wrong token: Run returned after %dms, state=%s, err=%v", time.Since(t0).Milliseconds(), snap.State, runErr)
	if snap.State != StateFailed {
		t.Fatalf("expected failed state, got %s", snap.State)
	}
}

func TestEngineServerUnreachable(t *testing.T) {
	s := globalSink()
	port, _ := frpstest.FreePort() // 没有人监听
	e := New(s)
	t0 := time.Now()
	if err := e.Start(Node{ServerAddr: "127.0.0.1", ServerPort: port, Token: token}, nil); err != nil {
		t.Fatal(err)
	}
	runErr, returned := e.Wait(15 * time.Second)
	if !returned || runErr == nil {
		t.Fatalf("expected Run to return an error, returned=%v err=%v", returned, runErr)
	}
	report(t, "unreachable server: Run returned after %dms, lastError=%q", time.Since(t0).Milliseconds(), e.Snapshot().LastError)
}

func TestValidationRejectsDuplicateNames(t *testing.T) {
	s := globalSink()
	e := New(s)
	p := Proxy{Name: "dup", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 0}
	err := e.Start(Node{ServerAddr: "127.0.0.1", ServerPort: 1, Token: token}, []Proxy{p, p})
	report(t, "duplicate proxy names: Start error = %v", err)
	// ConfigSource 以名字为 key，重名会被后一个覆盖，不会报错——这是需要在 shared/validate 里自己挡住的点
	if err == nil {
		_ = e.Stop(0, 2*time.Second)
	}
}

// 连接阶段（拨号还没返回）就 Stop：Run 要多久才返回？决定退出流程里"最多等 2 秒"是否够。
func TestStopWhileConnecting(t *testing.T) {
	s := globalSink()
	for _, tc := range []struct{ name, addr string; port int }{
		{"refused(127.0.0.1:1)", "127.0.0.1", 1},
		{"blackhole(10.255.255.1:7000)", "10.255.255.1", 7000},
	} {
		e := New(s)
		if err := e.Start(Node{ServerAddr: tc.addr, ServerPort: tc.port, Token: token}, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		t0 := time.Now()
		stopErr := e.Stop(500*time.Millisecond, 2*time.Second)
		runErr, returned := e.Wait(15 * time.Second)
		report(t, "stop while connecting to %s: Stop()=%v, Run returned=%v after %dms, runErr=%v, state=%s",
			tc.name, stopErr, returned, time.Since(t0).Milliseconds(), runErr, e.Snapshot().State)
	}
}

func TestUnreachableLogLines(t *testing.T) {
	s := globalSink()
	before := len(s.Lines())
	port, _ := frpstest.FreePort()
	e := New(s)
	_ = e.Start(Node{ServerAddr: "127.0.0.1", ServerPort: port, Token: token}, nil)
	_, _ = e.Wait(15 * time.Second)
	for _, l := range s.Lines()[before:] {
		if strings.Contains(l, "client/") {
			report(t, "log: %s", l)
		}
	}
}

// 远程端口冲突：第二个代理应进入 start error，Err 里带原因（PLAN 6.3.4）
func TestStartErrorReason(t *testing.T) {
	s := globalSink()
	echo, _ := frpstest.StartEcho("127.0.0.1:0")
	defer echo.Close()
	frps, err := frpstest.StartFrps(0, token)
	if err != nil {
		t.Fatal(err)
	}
	defer frps.Close()
	remote, _ := frpstest.FreePort()
	lp := echo.Addr().(*net.TCPAddr).Port
	e := New(s)
	err = e.Start(Node{ServerAddr: "127.0.0.1", ServerPort: frps.Port, Token: token}, []Proxy{
		{Name: "a", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: lp, RemotePort: remote},
		{Name: "b", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: lp, RemotePort: remote},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop(0, 3*time.Second)
	waitFor(t, "one proxy in start error", 10*time.Second, func() bool {
		a, _ := e.ProxyStatus("a")
		b, _ := e.ProxyStatus("b")
		return (a.Phase == "running" && b.Phase == "start error") || (b.Phase == "running" && a.Phase == "start error")
	})
	a, _ := e.ProxyStatus("a")
	b, _ := e.ProxyStatus("b")
	report(t, "remote port conflict: a=%q err=%q | b=%q err=%q", a.Phase, a.Err, b.Phase, b.Err)
}

// GracefulClose(d) 期间 GetProxyStatus 会不会被阻塞（UI 每秒轮询状态）
func TestProxyStatusDuringGracefulClose(t *testing.T) {
	s := globalSink()
	echo, _ := frpstest.StartEcho("127.0.0.1:0")
	defer echo.Close()
	frps, err := frpstest.StartFrps(0, token)
	if err != nil {
		t.Fatal(err)
	}
	defer frps.Close()
	remote, _ := frpstest.FreePort()
	e := New(s)
	if err := e.Start(Node{ServerAddr: "127.0.0.1", ServerPort: frps.Port, Token: token}, []Proxy{
		{Name: "a", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: echo.Addr().(*net.TCPAddr).Port, RemotePort: remote},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", 10*time.Second, func() bool { st, ok := e.ProxyStatus("a"); return ok && st.Phase == "running" })
	go func() { _ = e.Stop(2*time.Second, 5*time.Second) }()
	time.Sleep(100 * time.Millisecond)
	t0 := time.Now()
	_, _ = e.ProxyStatus("a")
	report(t, "GetProxyStatus during GracefulClose(2s) blocked for %dms", time.Since(t0).Milliseconds())
	_, _ = e.Wait(5 * time.Second)
}
