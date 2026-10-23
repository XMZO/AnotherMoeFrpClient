package execx

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"moefrp/shared/agentproto"
)

func TestRun(t *testing.T) {
	r := Run(context.Background(), agentproto.ExecRequest{Command: "echo hello"})
	if r.ExitCode != 0 || !strings.Contains(r.Output, "hello") {
		t.Fatalf("%+v", r)
	}
	r = Run(context.Background(), agentproto.ExecRequest{Command: "exit 3"})
	if r.ExitCode != 3 {
		t.Fatalf("退出码：%+v", r)
	}
	if runtime.GOOS != "windows" {
		r = Run(context.Background(), agentproto.ExecRequest{Command: "sleep 30 & sleep 30", TimeoutSec: 1})
		if r.ExitCode != -1 || !strings.Contains(r.Error, "超时") || r.DurationMs > 8000 {
			t.Fatalf("超时：%+v", r)
		}
	}
	c := &capped{max: 5}
	c.Write([]byte("abc"))
	c.Write([]byte("defg"))
	if c.String() != "abcde" || !c.truncated {
		t.Fatalf("截断：%q %v", c.String(), c.truncated)
	}
}
