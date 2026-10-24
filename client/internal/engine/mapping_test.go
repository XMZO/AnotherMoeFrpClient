package engine

import (
	"strings"
	"testing"

	v1 "github.com/fatedier/frp/pkg/config/v1"

	"moefrp/client/internal/apperr"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func baseProfile() model.Profile {
	return model.Profile{
		ID:   model.NewID(),
		Name: "测试配置",
		Nodes: []model.Node{{
			ID: model.NewID(), Remark: "本机", ServerAddr: "127.0.0.1", ServerPort: 7000, Token: "t",
		}},
		Proxies:  []model.Proxy{},
		Visitors: []model.Visitor{},
	}
}

// allTypesProfile 包含 8 种代理和 3 种访客，每种都填了它特有的字段。
func allTypesProfile() model.Profile {
	p := baseProfile()
	tr := true
	p.Nodes[0].TLS = &tr
	p.Nodes[0].User = "alice"
	p.Proxies = []model.Proxy{
		{ID: model.NewID(), Name: "tcp1", Type: model.ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6000, UseEncryption: true, UseCompression: true},
		{ID: model.NewID(), Name: "udp1", Type: model.ProxyUDP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 53, RemotePort: 6001},
		{ID: model.NewID(), Name: "http1", Type: model.ProxyHTTP, Enabled: true, LocalPort: 80, CustomDomains: []string{"a.example.com"}},
		{ID: model.NewID(), Name: "https1", Type: model.ProxyHTTPS, Enabled: true, LocalPort: 443, SubDomain: "web"},
		{ID: model.NewID(), Name: "mux1", Type: model.ProxyTCPMux, Enabled: true, LocalPort: 8080, CustomDomains: []string{"mux.example.com"}},
		{ID: model.NewID(), Name: "stcp1", Type: model.ProxySTCP, Enabled: true, LocalPort: 3389, SecretKey: testSecret, AllowUsers: []string{"bob"}},
		{ID: model.NewID(), Name: "xtcp1", Type: model.ProxyXTCP, Enabled: true, LocalPort: 3390, SecretKey: testSecret, AllowUsers: []string{"*"}},
		{ID: model.NewID(), Name: "sudp1", Type: model.ProxySUDP, Enabled: false, LocalPort: 5353, SecretKey: testSecret},
	}
	p.Visitors = []model.Visitor{
		{ID: model.NewID(), Name: "v-stcp", Type: model.VisitorSTCP, Enabled: true, ServerName: "rdp", ServerUser: "bob", SecretKey: testSecret, BindAddr: "127.0.0.1", BindPort: 13389},
		{ID: model.NewID(), Name: "v-xtcp", Type: model.VisitorXTCP, Enabled: true, ServerName: "rdp2", SecretKey: testSecret, BindPort: 13390, FallbackTo: "v-stcp"},
		{ID: model.NewID(), Name: "v-sudp", Type: model.VisitorSUDP, Enabled: true, ServerName: "dns", SecretKey: testSecret, BindPort: 15353},
	}
	return p
}

// PLAN 第 11 节：每种代理和访客类型，从模型转成 frp 结构体后都要能通过 ValidateAllClientConfig。
func TestPrepareAllTypes(t *testing.T) {
	p := allTypesProfile()
	prep, err := Prepare(p, p.Nodes[0].ID)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(prep.Proxies) != len(model.ProxyTypes) || len(prep.Visitors) != len(model.VisitorTypes) {
		t.Fatalf("got %d proxies, %d visitors", len(prep.Proxies), len(prep.Visitors))
	}
	seen := map[string]bool{}
	for _, c := range prep.Proxies {
		seen[c.GetBaseConfig().Type] = true
	}
	for _, typ := range model.ProxyTypes {
		if !seen[typ] {
			t.Errorf("proxy type %s not mapped", typ)
		}
	}

	if prep.Common.User != "alice" || prep.Common.Auth.Token != "t" || prep.Common.ServerPort != 7000 {
		t.Errorf("common not mapped: %+v", prep.Common)
	}
	if prep.Common.LoginFailExit == nil || !*prep.Common.LoginFailExit {
		t.Errorf("LoginFailExit should default to true")
	}
	if prep.Common.WebServer.Port != 0 {
		t.Errorf("admin web server must stay disabled, got port %d", prep.Common.WebServer.Port)
	}

	byName := map[string]v1.ProxyConfigurer{}
	for _, c := range prep.Proxies {
		byName[c.GetBaseConfig().Name] = c
	}
	tcp := byName["tcp1"].(*v1.TCPProxyConfig)
	if tcp.RemotePort != 6000 || tcp.LocalPort != 22 || !tcp.Transport.UseEncryption || !tcp.Transport.UseCompression {
		t.Errorf("tcp: %+v", tcp)
	}
	if u := byName["udp1"].(*v1.UDPProxyConfig); u.RemotePort != 6001 {
		t.Errorf("udp remotePort: %d", u.RemotePort)
	}
	if h := byName["http1"].(*v1.HTTPProxyConfig); len(h.CustomDomains) != 1 || h.CustomDomains[0] != "a.example.com" {
		t.Errorf("http domains: %+v", h.DomainConfig)
	}
	if h := byName["https1"].(*v1.HTTPSProxyConfig); h.SubDomain != "web" {
		t.Errorf("https subdomain: %+v", h.DomainConfig)
	}
	if m := byName["mux1"].(*v1.TCPMuxProxyConfig); m.Multiplexer != "httpconnect" || m.CustomDomains[0] != "mux.example.com" {
		t.Errorf("tcpmux: %+v", m)
	}
	if s := byName["stcp1"].(*v1.STCPProxyConfig); s.Secretkey != testSecret || len(s.AllowUsers) != 1 || s.AllowUsers[0] != "bob" {
		t.Errorf("stcp: %+v", s)
	}
	if x := byName["xtcp1"].(*v1.XTCPProxyConfig); x.Secretkey != testSecret {
		t.Errorf("xtcp: %+v", x)
	}
	if s := byName["sudp1"].(*v1.SUDPProxyConfig); s.Secretkey != testSecret || s.Enabled == nil || *s.Enabled {
		t.Errorf("sudp should be mapped as disabled: %+v", s)
	}

	vByName := map[string]v1.VisitorConfigurer{}
	for _, c := range prep.Visitors {
		vByName[c.GetBaseConfig().Name] = c
	}
	sv := vByName["v-stcp"].(*v1.STCPVisitorConfig)
	if sv.ServerName != "rdp" || sv.ServerUser != "bob" || sv.SecretKey != testSecret || sv.BindPort != 13389 {
		t.Errorf("stcp visitor: %+v", sv)
	}
	if xv := vByName["v-xtcp"].(*v1.XTCPVisitorConfig); xv.FallbackTo != "v-stcp" {
		t.Errorf("xtcp visitor fallbackTo: %+v", xv)
	}
	if _, ok := vByName["v-sudp"].(*v1.SUDPVisitorConfig); !ok {
		t.Errorf("sudp visitor not mapped")
	}
}

// 每种类型单独过一遍 frp 校验，避免一个类型的问题被别的类型掩盖。
func TestPrepareEachTypeAlone(t *testing.T) {
	full := allTypesProfile()
	for _, px := range full.Proxies {
		p := baseProfile()
		px.Enabled = true
		p.Proxies = []model.Proxy{px}
		if _, err := Prepare(p, p.Nodes[0].ID); err != nil {
			t.Errorf("proxy type %s: %v", px.Type, err)
		}
	}
	for _, v := range full.Visitors {
		p := baseProfile()
		v.FallbackTo = ""
		p.Visitors = []model.Visitor{v}
		if _, err := Prepare(p, p.Nodes[0].ID); err != nil {
			t.Errorf("visitor type %s: %v", v.Type, err)
		}
	}
	for _, proto := range model.Protocols {
		p := baseProfile()
		p.Nodes[0].Protocol = proto
		if _, err := Prepare(p, p.Nodes[0].ID); err != nil {
			t.Errorf("protocol %s: %v", proto, err)
		}
	}
}

func TestPrepareRejectsDuplicateNames(t *testing.T) {
	p := baseProfile()
	p.Proxies = []model.Proxy{
		{ID: model.NewID(), Name: "dup", Type: model.ProxyTCP, Enabled: true, LocalPort: 80},
		{ID: model.NewID(), Name: "dup", Type: model.ProxyTCP, Enabled: true, LocalPort: 81},
	}
	_, err := Prepare(p, p.Nodes[0].ID)
	assertFieldError(t, err, "proxies[1].name", validate.ReasonDuplicate)

	// 代理和访客的名称合在一起算
	p = baseProfile()
	p.Proxies = []model.Proxy{{ID: model.NewID(), Name: "same", Type: model.ProxyTCP, Enabled: true, LocalPort: 80}}
	p.Visitors = []model.Visitor{{ID: model.NewID(), Name: "same", Type: model.VisitorSTCP, Enabled: true, ServerName: "x", SecretKey: testSecret, BindPort: 9000}}
	_, err = Prepare(p, p.Nodes[0].ID)
	assertFieldError(t, err, "visitors[0].name", validate.ReasonDuplicate)

	// 未启用的代理也参与查重：frp 的 ConfigSource 以名字为 key，启用后才冲突就晚了
	p = baseProfile()
	p.Proxies = []model.Proxy{
		{ID: model.NewID(), Name: "dup", Type: model.ProxyTCP, Enabled: true, LocalPort: 80},
		{ID: model.NewID(), Name: "dup", Type: model.ProxyTCP, Enabled: false, LocalPort: 81},
	}
	_, err = Prepare(p, p.Nodes[0].ID)
	assertFieldError(t, err, "proxies[1].name", validate.ReasonDuplicate)
}

func TestPrepareNodeErrors(t *testing.T) {
	p := baseProfile()
	if _, err := Prepare(p, ""); apierr.CodeOf(err) != apperr.NodeRequired {
		t.Errorf("empty node id: %v", err)
	}
	if _, err := Prepare(p, "nope"); apierr.CodeOf(err) != apperr.NodeNotFound {
		t.Errorf("unknown node id: %v", err)
	}
}

func assertFieldError(t *testing.T, err error, field, reason string) {
	t.Helper()
	ae, ok := apierr.As(err)
	if !ok || ae.Code != apierr.ValidationFailed {
		t.Fatalf("want validation_failed, got %v", err)
	}
	fields, _ := ae.Details[apierr.DetailFields].([]validate.FieldError)
	for _, f := range fields {
		if f.Field == field && f.Reason == reason {
			return
		}
	}
	t.Fatalf("want field error %s/%s, got %+v", field, reason, fields)
}

func TestSameConnection(t *testing.T) {
	a := model.Node{ID: "1", Remark: "x", ServerAddr: "h", ServerPort: 7000, Token: "t"}
	b := a
	b.Remark = "改了备注"
	if !SameConnection(a, b) {
		t.Errorf("remark must not force a restart")
	}
	b.Protocol = model.ProtocolTCP
	tr := true
	b.TLS = &tr
	if !SameConnection(a, b) {
		t.Errorf("explicit defaults (tcp, tls=true) should equal empty values")
	}
	for _, mut := range []func(*model.Node){
		func(n *model.Node) { n.ServerAddr = "h2" },
		func(n *model.Node) { n.ServerPort = 7001 },
		func(n *model.Node) { n.Token = "t2" },
		func(n *model.Node) { n.User = "u" },
		func(n *model.Node) { n.Protocol = model.ProtocolKCP },
		func(n *model.Node) { f := false; n.TLS = &f },
		func(n *model.Node) { n.ProxyURL = "socks5://127.0.0.1:1080" },
	} {
		c := a
		mut(&c)
		if SameConnection(a, c) {
			t.Errorf("change not detected: %+v", c)
		}
	}
}

func TestClassifyProxyError(t *testing.T) {
	for msg, want := range map[string]apierr.Code{
		"port already used":                  apperr.ProxyPortInUse,
		"port not allowed":                   apperr.ProxyPortNotAllowed,
		"port unavailable":                   apperr.ProxyPortUnavail,
		"proxy [web] already exists":         apperr.ProxyNameTaken,
		"proxy name [web] is already in use": apperr.ProxyNameTaken,
		"router config conflict":             apperr.ProxyDomainConflict,
		"proxy type not support":             apperr.ProxyTypeNotAllowed,
		"something else":                     apperr.ProxyStartError,
	} {
		if got := ClassifyProxyError(msg); got != want {
			t.Errorf("%q: got %s want %s", msg, got, want)
		}
	}
}

func TestLogSinkRingAndLevels(t *testing.T) {
	s := NewLogSink(3)
	var got []LogLine
	unsub := s.Subscribe(func(l LogLine) { got = append(got, l) })
	_, _ = s.Write([]byte("2026-10-18 19:10:48.763 [W] [client/service.go:346] a\n2026-10-18 19:10:48.763 [I] b\npart"))
	_, _ = s.Write([]byte("ial [E] x\n"))
	s.Appendf("error", "hello %d", 1)
	unsub()
	s.Appendf("info", "after unsubscribe")

	lines := s.Lines()
	if len(lines) != 3 {
		t.Fatalf("ring should keep 3 lines, got %d", len(lines))
	}
	if !strings.HasSuffix(lines[0].Text, "partial [E] x") || !strings.Contains(lines[1].Text, "[moefrp] hello 1") || !strings.HasSuffix(lines[2].Text, "after unsubscribe") {
		t.Fatalf("unexpected ring content: %+v", lines)
	}
	if lines[0].Seq >= lines[1].Seq || lines[1].Seq >= lines[2].Seq {
		t.Fatalf("seq must increase: %+v", lines)
	}
	if len(got) != 4 || got[0].Level != "warn" || got[1].Level != "info" || got[3].Level != "error" {
		t.Fatalf("subscriber got %+v", got)
	}
}

func TestPrepareOfficialNode(t *testing.T) {
	p := baseProfile()
	official := model.Node{ID: "official-n1", Remark: "东京", ServerAddr: "tokyo.example.com", ServerPort: 7000, Token: "t",
		User: "alice", Metadatas: map[string]string{"moefrp_cred": "mfc_secret"}}
	if _, err := Prepare(p, official.ID); apierr.CodeOf(err) != apperr.NodeNotFound {
		t.Fatalf("没有传额外节点时找不到：%v", err)
	}
	prep, err := Prepare(p, official.ID, official)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Common.User != "alice" || prep.Common.Metadatas["moefrp_cred"] != "mfc_secret" || prep.Common.ServerAddr != "tokyo.example.com" {
		t.Fatalf("公共配置：%+v", prep.Common)
	}
	changed := official
	changed.Metadatas = map[string]string{"moefrp_cred": "mfc_new"}
	if SameConnection(official, changed) {
		t.Fatal("凭证变了要重新连接")
	}
}
