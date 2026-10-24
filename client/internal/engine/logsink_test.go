package engine

import (
	"strings"
	"testing"
)

// 黑盒运行（云端配置、订阅）的日志只给订阅者里的引擎看：不进缓冲，带 Private 标记
func TestPrivateRunLogs(t *testing.T) {
	s := NewLogSink(10)
	var got []LogLine
	s.Subscribe(func(l LogLine) { got = append(got, l) })

	s.BeginRun("", true)
	s.Appendf("error", "start failed before tag")
	s.BeginRun("run-7", true)
	_, _ = s.Write([]byte("2026-10-24 10:00:00.000 [I] [client/service.go:1] [run-7] login to server success\n"))
	s.Privatef("info", "switching to profile %q", "云端")

	s.BeginRun("run-8", false)
	_, _ = s.Write([]byte("2026-10-24 10:00:01.000 [I] [client/service.go:1] [run-8] login to server success\n"))
	_, _ = s.Write([]byte("2026-10-24 10:00:02.000 [I] [client/service.go:1] [run-7] stopped late\n")) // 旧的黑盒运行晚到的行
	s.Appendf("info", "global line")

	var shown []string
	for _, l := range s.Lines() {
		shown = append(shown, l.Text)
	}
	all := strings.Join(shown, "\n")
	if len(shown) != 2 || !strings.Contains(all, "[run-8]") || !strings.Contains(all, "global line") {
		t.Fatalf("缓冲里只能有本地配置的行：%q", shown)
	}
	private := 0
	for _, l := range got {
		if l.Private {
			private++
		}
	}
	if len(got) != 6 || private != 4 {
		t.Fatalf("订阅者应收到全部 6 行，其中 4 行标记 Private：%+v", got)
	}
}
