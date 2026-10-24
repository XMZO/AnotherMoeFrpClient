package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	v1 "github.com/fatedier/frp/pkg/config/v1"

	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/model"
)

type fakeTickets struct {
	seq  int
	err  error
	used []string
}

func (f *fakeTickets) Peek(_ context.Context, nodeID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return fmt.Sprintf("%s-t%d", nodeID, f.seq+1), nil
}

func (f *fakeTickets) Used(nodeID, ticket string) {
	f.seq++
	f.used = append(f.used, ticket)
}

type fakeConnector struct {
	err   error
	calls int
}

func (c *fakeConnector) Open() error { return nil }
func (c *fakeConnector) Connect() (net.Conn, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	a, b := net.Pipe()
	b.Close()
	return a, nil
}
func (c *fakeConnector) Close() error { return nil }

func TestTicketConnector(t *testing.T) {
	src := &fakeTickets{}
	create := ticketConnector(src, "n1", "dev-1")
	cfg := &v1.ClientCommonConfig{Metadatas: map[string]string{"other": "x"}}
	orig := cfg.Metadatas

	// 每次登录前换上票据（新的 map，不改原来的），保留其他 metadatas
	if _, ok := create(context.Background(), cfg).(*ticketedConnector); !ok {
		t.Fatal("应返回带票据的连接器")
	}
	if cfg.Metadatas[api.MetaCredential] != "n1-t1" || cfg.Metadatas[api.MetaDevice] != "dev-1" || cfg.Metadatas["other"] != "x" || orig[api.MetaCredential] != "" {
		t.Fatalf("metadatas：%v（原来的 %v）", cfg.Metadatas, orig)
	}

	// 连接失败不算用掉票据；成功一次才算，之后的工作连接不重复标记
	inner := &fakeConnector{err: errors.New("refused")}
	tc := &ticketedConnector{Connector: inner, used: func() { src.Used("n1", "n1-t1") }}
	if _, err := tc.Connect(); err == nil || len(src.used) != 0 {
		t.Fatal("连不上时不应用掉票据")
	}
	inner.err = nil
	for i := 0; i < 3; i++ {
		if c, err := tc.Connect(); err != nil {
			t.Fatal(err)
		} else {
			c.Close()
		}
	}
	if len(src.used) != 1 || src.used[0] != "n1-t1" {
		t.Fatalf("用掉的票据：%v", src.used)
	}
	create(context.Background(), cfg)
	if cfg.Metadatas[api.MetaCredential] != "n1-t2" {
		t.Fatalf("下一次登录换新票据：%v", cfg.Metadatas)
	}

	// 领不到票据：登录直接失败，错误能被认出来
	src.err = errors.New("连不上面板")
	fc := create(context.Background(), cfg)
	err := fc.Open()
	if err == nil {
		t.Fatal("领不到票据时 Open 应失败")
	}
	ae := ticketFailure("login to the server failed: " + err.Error())
	if ae == nil || ae.Code != apperr.NodeTicketFailed || ae.Details["reason"] != "连不上面板" {
		t.Fatalf("%+v", ae)
	}
	if ticketFailure("connection refused") != nil {
		t.Fatal("别的错误不是票据错误")
	}
}

func TestClassifyFailure(t *testing.T) {
	official := model.Node{ID: api.OfficialNodePrefix + "n1"}
	own := model.Node{ID: "mine"}
	ok := PingResult{OK: true, Target: "1.2.3.4:7000"}
	down := PingResult{OK: false, Code: apperr.NodeRefused, Target: "1.2.3.4:7000", Message: "refused"}

	if ae := classifyFailure(official, "这台设备已经退出登录", ok); ae.Code != apperr.OfficialDenied || ae.Details["reason"] != "这台设备已经退出登录" {
		t.Fatalf("官方节点拒绝登录应显示原因：%+v", ae)
	}
	if ae := classifyFailure(official, "x", down); ae.Code != apperr.NodeRefused {
		t.Fatalf("连不上时按网络错误：%+v", ae)
	}
	if ae := classifyFailure(own, "x", ok); ae.Code != apperr.NodeHandshakeFailed || !strings.Contains(fmt.Sprint(ae.Details["target"]), "7000") {
		t.Fatalf("自己的节点：%+v", ae)
	}
	// 能连上但数据发不过去（yamux 写超时）：是网络不稳定，不是节点拒绝
	if ae := classifyFailure(official, "connection write timeout", ok); ae.Code != apperr.NodeUnstable || ae.Details["reason"] != "connection write timeout" {
		t.Fatalf("网络不稳定：%+v", ae)
	}
}
