// Package frpstest 在进程内起 frps 和 echo 服务，供引擎集成测试和本地演示（cmd/localfrps）使用。
// 主程序不引用这个包，所以 exe 里不会编进 frp 的 server 包。
package frpstest

import (
	"context"
	"io"
	"net"
	"strconv"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"
	"github.com/fatedier/frp/server"
)

type Frps struct {
	Svc    *server.Service
	Addr   string
	Port   int
	cancel context.CancelFunc
	done   chan struct{}
}

// StartFrps 在 127.0.0.1:port 起 frps；port=0 时自动选端口。
func StartFrps(port int, token string) (*Frps, error) {
	return StartFrpsWith(port, token, nil)
}

// StartFrpsWith 同 StartFrps，可以在 Complete 之前改配置（例如打开 vhostHTTPPort）。
func StartFrpsWith(port int, token string, tweak func(*v1.ServerConfig)) (*Frps, error) {
	if port == 0 {
		p, err := FreePort()
		if err != nil {
			return nil, err
		}
		port = p
	}
	cfg := &v1.ServerConfig{BindAddr: "127.0.0.1", BindPort: port}
	cfg.Auth.Token = token
	if tweak != nil {
		tweak(cfg)
	}
	if err := cfg.Complete(); err != nil {
		return nil, err
	}
	if _, err := validation.NewConfigValidator(security.NewUnsafeFeatures(nil)).ValidateServerConfig(cfg); err != nil {
		return nil, err
	}
	svc, err := server.NewService(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &Frps{Svc: svc, Addr: "127.0.0.1", Port: port, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		svc.Run(ctx)
	}()
	return f, nil
}

func (f *Frps) Close() {
	f.cancel()
	_ = f.Svc.Close()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
	}
}

// StartEcho 起一个 TCP echo 服务，返回监听器（调用方负责 Close）。
func StartEcho(addr string) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return l, nil
}

func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// PortFree 判断 127.0.0.1:port 当前能否被重新监听。
func PortFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
