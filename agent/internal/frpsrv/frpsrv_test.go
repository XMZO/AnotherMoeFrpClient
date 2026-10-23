package frpsrv

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"moefrp/shared/agentproto"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func conf(port int) string {
	return fmt.Sprintf("bindAddr = \"127.0.0.1\"\nbindPort = %d\nauth.token = \"t\"\n", port)
}

func TestParse(t *testing.T) {
	if _, _, err := Parse(""); err == nil {
		t.Fatal("空配置应报错")
	}
	if _, _, err := Parse("bindPort = \"abc\""); err == nil {
		t.Fatal("类型错误应报错")
	}
	if _, _, err := Parse("bindPort = 7000\nunknownField = 1\n"); err == nil {
		t.Fatal("未知字段应报错（严格模式）")
	}
	if _, _, err := Parse("bindPort = 7000\nwebServer.port = 7500\n"); err == nil || !strings.Contains(err.Error(), "dashboard") {
		t.Fatalf("不允许 dashboard：%v", err)
	}
	if _, _, err := Parse("[common]\nbind_port = 7000\n"); err == nil {
		t.Fatal("INI 格式应报错")
	}
	if cfg, _, err := Parse(conf(7000)); err != nil || cfg.BindPort != 7000 {
		t.Fatalf("合法配置：%v", err)
	}
}

func TestApplyAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frps.toml")
	m := New(path)
	defer m.Close()

	if st := m.Status(); st.State != agentproto.FrpsStopped {
		t.Fatalf("初始状态：%+v", st)
	}
	p1 := freePort(t)
	if r := m.Apply(1, conf(p1)); !r.Applied || r.Error != "" {
		t.Fatalf("应用 1：%+v", r)
	}
	if st := m.Status(); st.State != agentproto.FrpsRunning || st.BindPort != p1 || st.AppliedRev != 1 {
		t.Fatalf("运行中：%+v", st)
	}
	if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p1)); err != nil {
		t.Fatalf("frps 应该在监听：%v", err)
	} else {
		c.Close()
	}

	// 端口被占用：启动失败，回滚到旧配置
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	r := m.Apply(2, conf(busy.Addr().(*net.TCPAddr).Port))
	if r.Applied || !r.RolledBack || r.Error == "" {
		t.Fatalf("应回滚：%+v", r)
	}
	if st := m.Status(); st.State != agentproto.FrpsRunning || st.AppliedRev != 1 || st.BindPort != p1 {
		t.Fatalf("回滚后仍是旧配置：%+v", st)
	}

	// 配置错误：不动当前运行的 frps
	if r := m.Apply(3, "bindPort = 'x'"); r.Applied || r.RolledBack || !strings.Contains(r.Error, "配置不正确") {
		t.Fatalf("错误配置：%+v", r)
	}

	// 换端口成功，并且写入文件；新 Manager 能从文件启动
	p2 := freePort(t)
	if r := m.Apply(4, conf(p2)); !r.Applied {
		t.Fatalf("应用 4：%+v", r)
	}
	if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p1)); err == nil {
		c.Close()
		t.Fatal("旧端口应已释放")
	}
	m.Close()
	m2 := New(path)
	defer m2.Close()
	if err := m2.Boot(); err != nil {
		t.Fatal(err)
	}
	if st := m2.Status(); st.AppliedRev != 4 || st.BindPort != p2 || st.State != agentproto.FrpsRunning {
		t.Fatalf("从文件启动：%+v", st)
	}
	if err := m2.Restart(); err != nil {
		t.Fatal(err)
	}
}
