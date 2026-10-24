package engine

import (
	"context"
	"fmt"
	"maps"
	"net"
	"strings"
	"sync"

	frpclient "github.com/fatedier/frp/client"
	v1 "github.com/fatedier/frp/pkg/config/v1"

	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// ticketErrPrefix 是领不到登录票据时的错误前缀：错误经过 frp 后只剩文字，靠它认出来。
const ticketErrPrefix = "无法领取官方节点的登录票据："

// ticketConnector 让官方节点的每次登录（包括断线重连）都带一张新的登录票据（docs/node-agent.md 第 12 节）。
//
// frp 每次登录前调用 ConnectorCreator，传进来的正是随后拼登录消息时读 Metadatas 的那份配置，
// 而且在同一个 goroutine 里（client/control_session.go 的 Dial），所以在这里换掉 Metadatas，
// 登录消息就带上新票据，不用改 frp。这依赖 frp 的内部行为，agent/internal/access 的 TestWithFrps
// 用真的 frps 和 frpc 盯着它（包括断线重连）。
//
// 票据在连接建立（Connect 成功、登录消息马上发出）后才算用掉：连不上节点、反复重试时不消耗票据。
func ticketConnector(src TicketSource, nodeID, deviceID string) func(context.Context, *v1.ClientCommonConfig) frpclient.Connector {
	return func(ctx context.Context, c *v1.ClientCommonConfig) frpclient.Connector {
		tk, err := src.Peek(ctx, nodeID)
		if err != nil {
			return failedConnector{fmt.Errorf("%s%v", ticketErrPrefix, err)}
		}
		m := maps.Clone(c.Metadatas)
		if m == nil {
			m = map[string]string{}
		}
		m[api.MetaCredential] = tk
		m[api.MetaDevice] = deviceID
		c.Metadatas = m
		return &ticketedConnector{Connector: frpclient.NewConnector(ctx, c), used: func() { src.Used(nodeID, tk) }}
	}
}

type ticketedConnector struct {
	frpclient.Connector
	once sync.Once
	used func()
}

// Connect 第一次成功（控制连接，接下来就发登录消息）时标记票据已用。后面的工作连接也走这里，不重复标记。
func (c *ticketedConnector) Connect() (net.Conn, error) {
	conn, err := c.Connector.Connect()
	if err == nil {
		c.once.Do(c.used)
	}
	return conn, err
}

// failedConnector 让这次登录直接失败，错误原样交给 frp（首次登录时 Run 返回它，重连时写进日志）。
type failedConnector struct{ err error }

func (f failedConnector) Open() error                { return f.err }
func (f failedConnector) Connect() (net.Conn, error) { return nil, f.err }
func (f failedConnector) Close() error               { return nil }

// ticketFailure 认出领不到票据的错误。
func ticketFailure(msg string) *apierr.Error {
	i := strings.Index(msg, ticketErrPrefix)
	if i < 0 {
		return nil
	}
	return &apierr.Error{Code: apperr.NodeTicketFailed, Message: msg,
		Details: map[string]any{"reason": msg[i+len(ticketErrPrefix):]}}
}
