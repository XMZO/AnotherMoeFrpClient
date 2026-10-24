package engine

import (
	"strings"
	"testing"
	"time"

	"moefrp/shared/model"
)

// 官方节点的域名、域名:端口、解析出的 IP:端口在日志、错误、测速结果里都换成节点名；别的地址不动。
func TestConcealOfficialAddress(t *testing.T) {
	s := NewLogSink(10)
	s.HideNode(model.Node{ID: "official-n1", Remark: "常山1", ServerAddr: "localhost", ServerPort: 7000})
	deadline := time.Now().Add(5 * time.Second)
	for s.Conceal("127.0.0.1:7000") == "127.0.0.1:7000" { // IP 在后台解析
		if time.Now().After(deadline) {
			t.Fatal("没有解析出 localhost 的 IP")
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.Write([]byte("2026-10-24 19:00:53.197 [W] [client/service.go:335] connect to server error: dial tcp 127.0.0.1:7000: refused, server localhost:7000 (localhost)\n"))
	s.Appendf("info", "start via node 常山1 (localhost:7000)")
	for _, l := range s.Lines() {
		if strings.Contains(l.Text, "localhost") || strings.Contains(l.Text, "127.0.0.1:7000") || !strings.Contains(l.Text, "[常山1]") {
			t.Fatalf("日志里还有地址：%s", l.Text)
		}
	}
	pr := s.ConcealPing(PingResult{Target: "localhost:7000", Message: "dial tcp 127.0.0.1:7000: refused"})
	if pr.Target != "[常山1]" || strings.Contains(pr.Message, "127.0.0.1:7000") {
		t.Fatalf("测速结果：%+v", pr)
	}
	if got := s.Conceal("dial tcp 10.0.0.1:7000, local 127.0.0.1:26652"); got != "dial tcp 10.0.0.1:7000, local 127.0.0.1:26652" {
		t.Fatalf("别的地址不应被替换：%s", got)
	}
}
