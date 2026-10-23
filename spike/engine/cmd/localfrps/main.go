// localfrps 在本机起一个 frps 和一个 echo 服务，给 app-engine 的界面演示当对端用。
// 单独成一个进程，这样 app-engine.exe 里不会编进 frp 的 server 包，体积有代表性。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"

	"spike/engine/frpstest"
)

func main() {
	port := flag.Int("port", 17000, "frps bind port")
	token := flag.String("token", "spike-token", "frps auth token")
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
