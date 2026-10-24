package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// liveConn 是测试用的客户端实时通道。
type liveConn struct {
	t  *testing.T
	ws *websocket.Conn
	ch chan api.LiveMessage
}

func (n *nodeEnv) dialLive(token string) *liveConn {
	n.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set(api.HeaderAuthorization, api.BearerPrefix+token)
	h.Set(api.HeaderClientVersion, "3.0.0")
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(n.http.URL, "http")+api.RouteClientLive, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		n.t.Fatal(err)
	}
	c := &liveConn{t: n.t, ws: ws, ch: make(chan api.LiveMessage, 64)}
	go func() {
		defer close(c.ch)
		for {
			_, b, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			var m api.LiveMessage
			json.Unmarshal(b, &m)
			c.ch <- m
		}
	}()
	n.t.Cleanup(func() { ws.CloseNow() })
	if m := c.expect(api.LiveWelcome); m.ServerTime == 0 {
		n.t.Fatal("welcome 没有面板时间")
	}
	return c
}

func (c *liveConn) send(m api.LiveMessage) {
	c.t.Helper()
	b, _ := json.Marshal(m)
	if err := c.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// expect 等一条指定类型的消息（跳过其他类型）。
func (c *liveConn) expect(typ string) api.LiveMessage {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-c.ch:
			if !ok {
				c.t.Fatalf("等 %s 时通道断了", typ)
			}
			if m.Type == typ {
				return m
			}
		case <-timeout:
			c.t.Fatalf("等不到 %s", typ)
		}
	}
}

// closed 等通道被面板断开。
func (c *liveConn) closed() {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.ch:
			if !ok {
				return
			}
		case <-timeout:
			c.t.Fatal("通道应被断开")
		}
	}
}

// TestLive：实时通道的推送和后台控制。
func TestLive(t *testing.T) {
	n := newNodeEnv(t, withConfigSingle(false))
	n.register("alice", "password-alice")
	tokA := n.deviceLogin("alice", devA)
	a := n.dialLive(tokA)
	a.send(api.LiveMessage{Type: api.LiveHello, Hello: &api.LiveHelloBody{Version: "3.0.0", OS: "Windows 11 专业版 24H2", Hostname: "alice-pc",
		OSBuild: "10.0.26100.2894", Arch: "amd64", CPU: "AMD Ryzen 7 7840HS", Cores: 16, MemoryMB: 32000, Model: "LENOVO 21D0"}})
	a.send(api.LiveMessage{Type: api.LiveStatus, Status: &api.LiveTunnelInfo{State: "running", ProfileKind: "cloud", NodeName: "东京", Proxies: 2, Running: 2}})

	// 后台看得到在线设备和隧道状态
	var list struct{ Clients []map[string]any }
	eventually(t, "在线设备", func() bool {
		n.admin("GET", "/clients", nil).expect(http.StatusOK).decode(&list)
		return len(list.Clients) == 1 && list.Clients[0]["hostname"] == "alice-pc" &&
			list.Clients[0]["tunnel"].(map[string]any)["state"] == "running"
	})
	sess := list.Clients[0]["sessionId"].(string)

	// 用户的登录设备：会话、设备指纹、登录 IP、设备信息、在线和隧道状态
	type devView struct {
		SessionID, DeviceID, LoginIP, LastIP string
		Online                                bool
		Device                                *api.LiveHelloBody
		Tunnel                                *api.LiveTunnelInfo
	}
	var devs struct{ Devices []devView }
	eventually(t, "登录设备", func() bool {
		n.admin("GET", "/users/alice/devices", nil).expect(http.StatusOK).decode(&devs)
		for _, d := range devs.Devices {
			if d.SessionID == sess {
				return d.LastIP != "" && d.Device != nil && d.Device.CPU == "AMD Ryzen 7 7840HS"
			}
		}
		return false
	})
	for _, d := range devs.Devices {
		if d.SessionID == sess && (d.DeviceID != devA || d.LoginIP == "" || !d.Online || d.Device.MemoryMB != 32000 || d.Tunnel.State != "running") {
			t.Fatalf("登录设备：%+v %+v", d, d.Device)
		}
	}
	n.admin("GET", "/users/nobody/devices", nil).apiErr(apierr.NotFound)

	// 推送通知：校验、发送
	n.admin("POST", "/clients/notice", map[string]any{"target": map[string]any{"all": true},
		"notice": map[string]any{"title": "", "linkUrl": "javascript:alert(1)"}}).apiErr(apierr.ValidationFailed)
	var sent struct{ Sent int }
	n.admin("POST", "/clients/notice", map[string]any{"target": map[string]any{"users": []string{list.Clients[0]["userId"].(string)}},
		"notice": map[string]any{"title": "维护通知", "content": "今晚 23 点维护", "level": "warning", "display": "dialog",
			"linkUrl": "https://example.com/status"}}).expect(http.StatusOK).decode(&sent)
	if sent.Sent != 1 {
		t.Fatalf("sent %d", sent.Sent)
	}
	if m := a.expect(api.LiveNotice); m.Notice.Title != "维护通知" || m.Notice.Display != "dialog" || m.Notice.LinkText != "打开链接" || m.Notice.From != "boss" {
		t.Fatalf("通知：%+v", m.Notice)
	}

	// 远程重启隧道
	n.admin("POST", "/clients/tunnel", map[string]any{"target": map[string]any{"sessions": []string{sess}}, "action": "restart", "reason": "换节点"}).expect(http.StatusOK)
	if m := a.expect(api.LiveTunnel); m.Tunnel.Action != api.TunnelRestart || m.Tunnel.Reason != "换节点" {
		t.Fatalf("隧道：%+v", m.Tunnel)
	}

	// 同一账号另一台设备改了云端配置：这台马上收到 sync，改的那台自己不收
	tokB := n.deviceLogin("alice", devB)
	b := n.dialLive(tokB)
	n.createProfile(tokB, sampleCreate())
	a.expect(api.LiveSync)

	// 官方节点相关的变化：限额改了 → official
	n.admin("PUT", "/quotas", map[string]any{"user": map[string]any{"maxProxies": 3}, "admin": map[string]any{}}).expect(http.StatusNoContent)
	b.expect(api.LiveOfficial)

	// 重置节点凭证 → tickets
	n.admin("POST", "/users/alice/reset-node-credential", nil).expect(http.StatusNoContent)
	b.expect(api.LiveTickets)

	// 修改客户端版本：推送新配置；最低版本立即生效
	n.admin("PUT", "/client-config", map[string]any{"latestVersion": "3.2.0", "minVersion": "9.0.0"}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/client-config", map[string]any{"latestVersion": "3.2.0", "minVersion": "3.1.0", "downloadUrl": "https://example.com/x.exe",
		"announcement": "新版本"}).expect(http.StatusOK)
	if m := b.expect(api.LiveConfig); m.Config.MinVersion != "3.1.0" || m.Config.Announcement != "新版本" {
		t.Fatalf("配置：%+v", m.Config)
	}
	n.do(req{method: "GET", path: api.RouteMe, token: tokB}).apiErr(apierr.ClientOutdated)
	var cc api.ClientConfigResponse
	n.do(req{method: "GET", path: api.RouteClientConfig}).expect(http.StatusOK).decode(&cc)
	if cc.LatestVersion != "3.2.0" {
		t.Fatalf("client config：%+v", cc)
	}
	n.admin("DELETE", "/client-config", nil).expect(http.StatusOK)
	if m := b.expect(api.LiveConfig); m.Config.MinVersion != "3.0.0" {
		t.Fatalf("回到配置文件：%+v", m.Config)
	}

	// 后台让一台设备下线：它的通道收到原因并断开，另一台不受影响
	n.admin("POST", "/clients/kick", map[string]any{"sessions": []string{sess}}).expect(http.StatusOK)
	if m := a.expect(api.LiveEnded); m.Ended.Code != string(apierr.SessionExpired) {
		t.Fatalf("下线：%+v", m.Ended)
	}
	a.closed()
	n.do(req{method: "GET", path: api.RouteMe, token: tokA, header: map[string]string{api.HeaderClientVersion: "3.0.0"}}).apiErr(apierr.SessionExpired)
	n.admin("GET", "/clients", nil).expect(http.StatusOK).decode(&list)
	if len(list.Clients) != 1 {
		t.Fatalf("剩下的设备：%v", list.Clients)
	}
	_ = model.Profile{}
}

// TestPanelSettings：后台"设置"页——图源随客户端配置保存并下发（校验每一项）；单设备模式、登录有效期立即生效；
// 节点的面板地址写进安装命令；每一组都能恢复为配置文件。
func TestPanelSettings(t *testing.T) {
	n := newNodeEnv(t, withConfigSingle(false))
	var ps struct {
		Client struct {
			Config    map[string]any
			FromPanel bool
		}
		Auth struct {
			Config    authSettings
			FromPanel bool
		}
		Nodes struct {
			Config    nodeSettings
			FromPanel bool
		}
		Deploy map[string]any
	}
	n.admin("GET", "/panel-settings", nil).expect(http.StatusOK).decode(&ps)
	if ps.Auth.FromPanel || ps.Auth.Config.SingleSession || ps.Auth.Config.SessionTTLHours != 720 || ps.Deploy["secretKey"] == "" {
		t.Fatalf("初始：%+v", ps)
	}

	// 图源：格式不对标出字段；保存后客户端拿到
	n.admin("PUT", "/client-config", map[string]any{"imageSources": []map[string]any{{"url": "ftp://x", "weight": 1}}}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/client-config", map[string]any{"imageSources": []map[string]any{{"url": "https://img.example.com/api", "weight": 2, "isApi": true}}}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/client-config", map[string]any{"latestVersion": "3.1.0", "imageSources": []map[string]any{
		{"url": "https://umi.example.com/random", "weight": 5},
		{"url": "https://img.example.com/api", "weight": 2, "isApi": true, "jsonPath": "data.0.url"},
	}}).expect(http.StatusOK)
	var cc api.ClientImagesResponse
	n.register("carol", "password-carol")
	n.do(req{method: "GET", path: api.RouteClientImages, token: n.login("carol", "password-carol")}).expect(http.StatusOK).decode(&cc)
	if len(cc.ImageSources) != 2 || cc.ImageSources[1].JSONPath != "data.0.url" || !cc.ImageSources[1].IsAPI {
		t.Fatalf("图源：%+v", cc.ImageSources)
	}

	// 单设备模式：打开后新登录挤掉旧的
	n.register("bob", "password-bob")
	tok1 := n.login("bob", "password-bob")
	n.admin("PUT", "/panel-settings/auth", authSettings{SingleSession: true, SessionTTLHours: 0}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/panel-settings/auth", authSettings{SingleSession: true, SessionTTLHours: 48}).expect(http.StatusOK)
	n.login("bob", "password-bob")
	n.do(req{method: "GET", path: api.RouteMe, token: tok1}).apiErr(apierr.SessionReplaced)
	if n.auth.SessionTTL() != 48*time.Hour {
		t.Fatalf("有效期：%v", n.auth.SessionTTL())
	}
	n.admin("DELETE", "/panel-settings/auth", nil).expect(http.StatusOK)
	if n.auth.SingleSession() || n.auth.SessionTTL() != 720*time.Hour {
		t.Fatal("应恢复为配置文件")
	}

	// 节点：面板地址写进安装命令；格式不对标出字段
	n.admin("PUT", "/panel-settings/nodes", nodeSettings{PublicURL: "https://x.example.com/path"}).apiErr(apierr.ValidationFailed)
	n.admin("PUT", "/panel-settings/nodes", nodeSettings{PublicURL: "https://panel.example.com/", AgentMirror: "https://gh.example.com/"}).expect(http.StatusOK)
	var created struct{ InstallCommand string }
	n.admin("POST", "/nodes", map[string]any{"name": "装机"}).expect(http.StatusCreated).decode(&created)
	if !strings.Contains(created.InstallCommand, "https://panel.example.com/agent/install.sh") {
		t.Fatalf("安装命令：%s", created.InstallCommand)
	}
	n.admin("GET", "/panel-settings", nil).expect(http.StatusOK).decode(&ps)
	if !ps.Nodes.FromPanel || ps.Nodes.Config.PublicURL != "https://panel.example.com" {
		t.Fatalf("节点设置：%+v", ps.Nodes)
	}
}
