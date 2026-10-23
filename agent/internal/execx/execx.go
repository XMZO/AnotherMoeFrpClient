// Package execx 以 agent 的身份（root）执行面板下发的 shell 命令：有超时、输出上限，
// 超时时杀掉整个进程组，不留后台子进程。
package execx

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"moefrp/shared/agentproto"
)

// Run 执行命令并返回结果，不会返回 error：所有失败都写进结果里。
func Run(ctx context.Context, req agentproto.ExecRequest) agentproto.ExecResult {
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = agentproto.ExecDefaultTimeout
	}
	if timeout > agentproto.ExecMaxTimeout {
		timeout = agentproto.ExecMaxTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()
	out := &capped{max: agentproto.ExecMaxOutput}
	cmd := shellCommand(ctx, req.Command)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 3 * time.Second // 命令结束后，还占着输出管道的孙进程最多再等 3 秒
	err := cmd.Run()

	res := agentproto.ExecResult{
		Output:     out.String(),
		Truncated:  out.truncated,
		DurationMs: time.Since(start).Milliseconds(),
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case ctx.Err() == context.DeadlineExceeded:
		res.ExitCode, res.Error = -1, "超时（"+(time.Duration(timeout)*time.Second).String()+"），已结束整个进程组"
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		res.ExitCode = ee.ExitCode()
	default:
		res.ExitCode, res.Error = -1, err.Error()
	}
	return res
}

// capped 只保留前 max 字节。
type capped struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - len(c.buf); room > 0 {
		if len(p) > room {
			c.buf = append(c.buf, p[:room]...)
			c.truncated = true
		} else {
			c.buf = append(c.buf, p...)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}
