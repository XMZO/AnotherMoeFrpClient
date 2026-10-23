// localfrps 在本机起一个 frps 和一个 echo 服务，给界面自动化测试当对端用（scripts/e2e）。
// 单独成一个进程，这样客户端 exe 里不会编进 frp 的 server 包。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"

	"moefrp/client/internal/engine/frpstest"
)

func main() {
	port := flag.Int("port", 17000, "frps bind port")
	token := flag.String("token", "e2e-token", "frps auth token")
	echoAddr := flag.String("echo", "127.0.0.1:17001", "echo service listen address")
	flag.Parse()

	echo, err := frpstest.StartEcho(*echoAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "echo:", err)
		os.Exit(1)
	}
	defer echo.Close()

	frps, err := frpstest.StartFrps(*port, *token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "frps:", err)
		os.Exit(1)
	}
	defer frps.Close()

	fmt.Printf("frps listening on 127.0.0.1:%d (token %q), echo on %s\n", frps.Port, *token, *echoAddr)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
}
