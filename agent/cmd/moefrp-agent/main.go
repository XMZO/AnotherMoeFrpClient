// moefrp-agent 是 MoeFrp 的节点 agent：内嵌 frps，由管理后台远程配置、升级和卸载（docs/node-agent.md）。
//
//	moefrp-agent install --server https://面板 --code 安装码 [--no-exec] [--force]
//	moefrp-agent run         # systemd 服务入口
//	moefrp-agent uninstall   # 停止服务并删除所有文件
//	moefrp-agent version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"moefrp/agent/internal/agent"
	"moefrp/agent/internal/install"
	"moefrp/agent/internal/paths"
)

// version 在构建时用 -ldflags "-X main.version=<版本>" 设置，和面板版本一致。
var version = "dev"

const usage = `用法：
  moefrp-agent install --server https://面板地址 --code 安装码 [--no-exec] [--force]
  moefrp-agent run
  moefrp-agent uninstall
  moefrp-agent version

一般不用手动执行 install：在管理后台"节点"页添加节点，复制给出的安装命令即可。
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "run":
		err = agent.Run(ctx, version)
	case "install":
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		fs.SetOutput(stderr)
		o := install.Options{Out: stdout}
		fs.StringVar(&o.Server, "server", "", "面板地址")
		fs.StringVar(&o.Code, "code", "", "一次性安装码")
		fs.BoolVar(&o.NoExec, "no-exec", false, "不允许面板在这台机器上执行命令")
		fs.BoolVar(&o.Force, "force", false, "已安装时也重新注册")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		err = install.Install(ctx, paths.Default(), o)
	case "uninstall":
		err = install.Uninstall(ctx, paths.Default(), stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "未知命令 %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	return 0
}
