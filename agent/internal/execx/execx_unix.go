//go:build unix

package execx

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// shellCommand 用 /bin/sh -c 执行，放进单独的进程组，超时时整组结束。
// 环境变量只给最基本的几个，不继承 agent 自己的环境。
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	home := "/root"
	if os.Getuid() != 0 {
		home, _ = os.UserHomeDir()
	}
	cmd.Dir = home
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + home,
		"LANG=C.UTF-8",
		"TERM=dumb",
	}
	return cmd
}
