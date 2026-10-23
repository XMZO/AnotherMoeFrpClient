package model

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func sampleProfile() Profile {
	tls := false
	return Profile{
		ID:   NewID(),
		Name: "家里",
		Nodes: []Node{{
			ID: NewID(), Remark: "香港", ServerAddr: "frp.example.com", ServerPort: 7000,
			Token: "secret", User: "alice", Protocol: ProtocolQUIC, TLS: &tls, ProxyURL: "socks5://127.0.0.1:1080",
		}},
		Proxies: []Proxy{
			{ID: NewID(), Name: "ssh", Type: ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
			{ID: NewID(), Name: "web", Type: ProxyHTTP, Enabled: false, LocalIP: "127.0.0.1", LocalPort: 80,
				CustomDomains: []string{"a.example.com"}, SubDomain: "a", UseEncryption: true, UseCompression: true},
			{ID: NewID(), Name: "rdp", Type: ProxySTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 3389,
				SecretKey: "k", AllowUsers: []string{"*"}},
		},
		Visitors: []Visitor{{
			ID: NewID(), Name: "rdp-v", Type: VisitorXTCP, Enabled: true, ServerName: "rdp", ServerUser: "bob",
			SecretKey: "k", BindAddr: "127.0.0.1", BindPort: 13389, FallbackTo: "rdp-s",
		}},
		Revision:  3,
		UpdatedAt: time.Unix(1760000000, 0).UTC(),
	}
}

func TestProfileJSONRoundTrip(t *testing.T) {
	p := sampleProfile()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Profile
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, got) {
		t.Fatalf("round trip mismatch:\nwant %+v\ngot  %+v", p, got)
	}
}

// 字段名必须和 frp 的 JSON 字段同名（PLAN 5.1）。旧版 sk / secretKey 不一致的问题就出在这里。
func TestJSONFieldNames(t *testing.T) {
	b, _ := json.Marshal(sampleProfile())
	s := string(b)
	for _, name := range []string{
		`"id"`, `"name"`, `"nodes"`, `"proxies"`, `"visitors"`, `"revision"`, `"updatedAt"`,
		`"remark"`, `"serverAddr"`, `"serverPort"`, `"token"`, `"user"`, `"protocol"`, `"tls"`, `"proxyURL"`,
		`"type"`, `"enabled"`, `"localIP"`, `"localPort"`, `"remotePort"`, `"customDomains"`, `"subdomain"`,
		`"secretKey"`, `"allowUsers"`, `"useEncryption"`, `"useCompression"`,
		`"serverName"`, `"serverUser"`, `"bindAddr"`, `"bindPort"`, `"fallbackTo"`,
	} {
		if !strings.Contains(s, name+":") {
			t.Errorf("JSON 缺少字段 %s：%s", name, s)
		}
	}
	for _, bad := range []string{`"sk"`, `"local_ip"`, `"local_port"`, `"server_addr"`, `"custom_domains"`} {
		if strings.Contains(s, bad) {
			t.Errorf("JSON 不应出现旧字段 %s", bad)
		}
	}
}

func TestOmitEmpty(t *testing.T) {
	b, _ := json.Marshal(Proxy{ID: "x", Name: "n", Type: ProxyTCP, LocalPort: 1})
	s := string(b)
	for _, name := range []string{"remotePort", "customDomains", "subdomain", "secretKey", "allowUsers", "useEncryption", "useCompression"} {
		if strings.Contains(s, name) {
			t.Errorf("零值字段 %s 应该省略：%s", name, s)
		}
	}
	// enabled、localIP 不省略：false / 空字符串也是有意义的值
	for _, name := range []string{`"enabled":false`, `"localIP":""`} {
		if !strings.Contains(s, name) {
			t.Errorf("缺少 %s：%s", name, s)
		}
	}
	b, _ = json.Marshal(Node{ID: "x"})
	if strings.Contains(string(b), "tls") {
		t.Errorf("TLS 为 nil 时应省略：%s", b)
	}
}

func TestNormalize(t *testing.T) {
	var p Profile
	p.Normalize()
	b, _ := json.Marshal(p)
	for _, want := range []string{`"nodes":[]`, `"proxies":[]`, `"visitors":[]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Normalize 后应输出 %s：%s", want, b)
		}
	}
}

func TestEnsureIDs(t *testing.T) {
	d := ProfileData{
		Nodes:    []Node{{ID: "keep"}, {}},
		Proxies:  []Proxy{{}},
		Visitors: []Visitor{{ID: "v"}},
	}
	if !d.EnsureIDs() {
		t.Fatal("应报告有改动")
	}
	if d.Nodes[0].ID != "keep" || d.Visitors[0].ID != "v" {
		t.Fatal("已有 ID 不能改")
	}
	if d.Nodes[1].ID == "" || d.Proxies[0].ID == "" {
		t.Fatal("缺失的 ID 应被补上")
	}
	if d.EnsureIDs() {
		t.Fatal("第二次调用不应有改动")
	}
}

var uuidV7Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIDFormatAndOrder(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 20000; i++ {
		id := NewID()
		if !uuidV7Re.MatchString(id) {
			t.Fatalf("不是 UUIDv7：%s", id)
		}
		if seen[id] {
			t.Fatalf("ID 重复：%s", id)
		}
		seen[id] = true
		if prev != "" && id <= prev {
			t.Fatalf("ID 不单调递增：%s <= %s", id, prev)
		}
		prev = id
	}
}

func TestNewIDTimestamp(t *testing.T) {
	before := time.Now().UnixMilli()
	id := strings.ReplaceAll(NewID(), "-", "")
	var ms int64
	for _, c := range id[:12] {
		ms = ms*16 + int64(strings.IndexRune("0123456789abcdef", c))
	}
	if ms < before || ms > time.Now().UnixMilli()+1000 {
		t.Fatalf("时间戳不对：%d（当前 %d）", ms, before)
	}
}

func TestTypeHelpers(t *testing.T) {
	for _, tc := range []struct {
		typ                 string
		p2p, domain, remote bool
	}{
		{ProxyTCP, false, false, true},
		{ProxyUDP, false, false, true},
		{ProxyHTTP, false, true, false},
		{ProxyHTTPS, false, true, false},
		{ProxyTCPMux, false, true, false},
		{ProxySTCP, true, false, false},
		{ProxyXTCP, true, false, false},
		{ProxySUDP, true, false, false},
	} {
		if IsP2PType(tc.typ) != tc.p2p || IsDomainType(tc.typ) != tc.domain || HasRemotePort(tc.typ) != tc.remote {
			t.Errorf("%s 的类型判断不对", tc.typ)
		}
	}
	if len(ProxyTypes) != 8 || len(VisitorTypes) != 3 {
		t.Fatal("类型数量不对")
	}
	if !ValidRole(RoleVIP) || ValidRole("root") {
		t.Fatal("ValidRole 不对")
	}
}
