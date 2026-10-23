//go:build !unix

package execx

import (
	"context"
	"os/exec"
)

// shellCommand 在非 Unix 系统上用 cmd /c（只用于开发和测试，agent 只支持 Linux）。
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/c", command)
}
