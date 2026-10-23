package validate

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"moefrp/shared/model"
)

func validData() model.ProfileData {
	return model.ProfileData{
		Nodes: []model.Node{
			{ID: "n1", Remark: "香港 1", ServerAddr: "frp.example.com", ServerPort: 7000, Token: "t"},
			{ID: "n2", ServerAddr: "2001:db8::1", ServerPort: 443, Protocol: model.ProtocolWSS, ProxyURL: "http://127.0.0.1:8080"},
		},
		Proxies: []model.Proxy{
			{ID: "p1", Name: "ssh", Type: model.ProxyTCP, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
			{ID: "p2", Name: "dns", Type: model.ProxyUDP, LocalPort: 53},
			{ID: "p3", Name: "web", Type: model.ProxyHTTP, LocalPort: 80, CustomDomains: []string{"a.example.com", "*.b.example.com"}},
			{ID: "p4", Name: "tls", Type: model.ProxyHTTPS, LocalPort: 443, SubDomain: "tls-1"},
			{ID: "p5", Name: "mux", Type: model.ProxyTCPMux, LocalPort: 8080, SubDomain: "mux"},
			{ID: "p6", Name: "远程桌面", Type: model.ProxySTCP, LocalPort: 3389, SecretKey: "k", AllowUsers: []string{"*"}},
			{ID: "p7", Name: "x", Type: model.ProxyXTCP, LocalPort: 1, SecretKey: "k"},
			{ID: "p8", Name: "s", Type: model.ProxySUDP, LocalPort: 65535, SecretKey: "k"},
		},
		Visitors: []model.Visitor{
			{ID: "v1", Name: "rdp-s", Type: model.VisitorSTCP, ServerName: "rdp", SecretKey: "k", BindAddr: "127.0.0.1", BindPort: 13389},
			{ID: "v2", Name: "rdp-x", Type: model.VisitorXTCP, ServerName: "rdp", ServerUser: "bob", SecretKey: "k", BindPort: 13390, FallbackTo: "rdp-s"},
			{ID: "v3", Name: "game", Type: model.VisitorSUDP, ServerName: "game", SecretKey: "k", BindPort: 27015},
		},
	}
}

func asErrors(t *testing.T, err error) Errors {
	t.Helper()
	if err == nil {
		t.Fatal("期望校验失败，实际通过")
	}
	var es Errors
	if !errors.As(err, &es) {
		t.Fatalf("错误类型不是 Errors：%T", err)
	}
	return es
}

func TestValidProfile(t *testing.T) {
	p := model.Profile{ID: model.NewID(), Name: "我的配置", Revision: 1}
	d := validData()
	p.Nodes, p.Proxies, p.Visitors = d.Nodes, d.Proxies, d.Visitors
	if err := Profile(p); err != nil {
		t.Fatalf("合法配置被拒绝：%v", err)
	}
	if err := ProfileData(model.ProfileData{}); err != nil {
		t.Fatalf("空配置应该合法：%v", err)
	}
}

func TestDuplicateNames(t *testing.T) {
	// 代理之间重名
	d := validData()
	d.Proxies[1].Name = "ssh"
	es := asErrors(t, ProfileData(d))
	if !es.Has("proxies[1].name", ReasonDuplicate) {
		t.Fatalf("应报代理重名：%v", es)
	}

	// 代理和访客重名（合在一起算）
	d = validData()
	d.Visitors[2].Name = "web"
	es = asErrors(t, ProfileData(d))
	if !es.Has("visitors[2].name", ReasonDuplicate) {
		t.Fatalf("应报代理和访客重名：%v", es)
	}

	// 访客之间重名
	d = validData()
	d.Visitors[2].Name = "rdp-x"
	es = asErrors(t, ProfileData(d))
	if !es.Has("visitors[2].name", ReasonDuplicate) {
		t.Fatalf("应报访客重名：%v", es)
	}

	// 大小写不同不算重名（frp 的名字区分大小写）
	d = validData()
	d.Proxies[1].Name = "SSH"
	if err := ProfileData(d); err != nil {
		t.Fatalf("大小写不同不应报重名：%v", err)
	}
}

func TestDuplicateIDs(t *testing.T) {
	d := validData()
	d.Nodes[1].ID = "n1"
	d.Proxies[2].ID = "p1"
	d.Visitors[1].ID = "v1"
	es := asErrors(t, ProfileData(d))
	for _, f := range []string{"nodes[1].id", "proxies[2].id", "visitors[1].id"} {
		if !es.Has(f, ReasonDuplicate) {
			t.Errorf("应报 %s 重复：%v", f, es)
		}
	}
}

func TestPorts(t *testing.T) {
	// 旧版限制 1024 以上是 bug：22、80、443 必须能填
	for _, port := range []int{1, 22, 80, 443, 1024, 65535} {
		d := validData()
		d.Proxies[0].LocalPort = port
		d.Proxies[0].RemotePort = port
		d.Nodes[0].ServerPort = port
		d.Visitors[0].BindPort = port
		if err := ProfileData(d); err != nil {
			t.Errorf("端口 %d 应该合法：%v", port, err)
		}
	}
	for _, port := range []int{-1, 65536, 100000} {
		d := validData()
		d.Proxies[0].LocalPort = port
		d.Proxies[0].RemotePort = port
		d.Nodes[0].ServerPort = port
		d.Visitors[0].BindPort = port
		es := asErrors(t, ProfileData(d))
		for _, f := range []string{"proxies[0].localPort", "proxies[0].remotePort", "nodes[0].serverPort", "visitors[0].bindPort"} {
			if !es.Has(f, ReasonRange) {
				t.Errorf("端口 %d：%s 应报超出范围：%v", port, f, es)
			}
		}
	}
	// 必填端口为 0
	d := validData()
	d.Proxies[0].LocalPort = 0
	d.Nodes[0].ServerPort = 0
	d.Visitors[0].BindPort = 0
	es := asErrors(t, ProfileData(d))
	for _, f := range []string{"proxies[0].localPort", "nodes[0].serverPort", "visitors[0].bindPort"} {
		if !es.Has(f, ReasonRequired) {
			t.Errorf("%s 应报必填：%v", f, es)
		}
	}
	// remotePort 为 0 表示由 frps 分配
	d = validData()
	d.Proxies[0].RemotePort = 0
	if err := ProfileData(d); err != nil {
		t.Errorf("remotePort=0 应该合法：%v", err)
	}
}

func TestLimits(t *testing.T) {
	d := model.ProfileData{}
	for i := 0; i <= MaxNodesPerProfile; i++ {
		d.Nodes = append(d.Nodes, model.Node{ID: model.NewID(), ServerAddr: "a", ServerPort: 7000})
	}
	for i := 0; i <= MaxProxiesPerProfile; i++ {
		d.Proxies = append(d.Proxies, model.Proxy{ID: model.NewID(), Name: fmt.Sprintf("p%d", i), Type: model.ProxyTCP, LocalPort: 1})
	}
	for i := 0; i <= MaxVisitorsPerProfile; i++ {
		d.Visitors = append(d.Visitors, model.Visitor{ID: model.NewID(), Name: fmt.Sprintf("v%d", i), Type: model.VisitorSTCP, ServerName: "s", SecretKey: "k", BindPort: 1})
	}
	es := asErrors(t, ProfileData(d))
	for _, f := range []string{"nodes", "proxies", "visitors"} {
		if !es.Has(f, ReasonTooMany) {
			t.Errorf("%s 应报超出上限：%v", f, es)
		}
	}
	// 刚好等于上限是允许的
	d.Nodes = d.Nodes[:MaxNodesPerProfile]
	d.Proxies = d.Proxies[:MaxProxiesPerProfile]
	d.Visitors = d.Visitors[:MaxVisitorsPerProfile]
	if err := ProfileData(d); err != nil {
		t.Fatalf("刚好等于上限应该合法：%v", err)
	}
}

func TestProxyNames(t *testing.T) {
	for _, bad := range []string{"", "a b", "a.b", "tab\tx", strings.Repeat("长", 51), "a b", "x\x00"} {
		d := validData()
		d.Proxies[0].Name = bad
		es := asErrors(t, ProfileData(d))
		if !es.Has("proxies[0].name", "") {
			t.Errorf("代理名 %q 应被拒绝：%v", bad, es)
		}
	}
	for _, good := range []string{"a", "my-ssh_1", "家里的电脑", strings.Repeat("长", 50), "a:b@c"} {
		d := validData()
		d.Proxies[0].Name = good
		if err := ProfileData(d); err != nil {
			t.Errorf("代理名 %q 应该合法：%v", good, err)
		}
	}
	d := validData()
	d.Visitors[0].Name = "a.b"
	d.Visitors[1].FallbackTo = "a.b"
	es := asErrors(t, ProfileData(d))
	if !es.Has("visitors[0].name", ReasonInvalid) {
		t.Errorf("访客名同样不能有 .：%v", es)
	}
}

func TestTypeSpecificRules(t *testing.T) {
	// http / https / tcpmux：customDomains 和 subdomain 至少填一个
	for _, typ := range []string{model.ProxyHTTP, model.ProxyHTTPS, model.ProxyTCPMux} {
		d := validData()
		d.Proxies[2].Type = typ
		d.Proxies[2].CustomDomains = nil
		d.Proxies[2].SubDomain = ""
		es := asErrors(t, ProfileData(d))
		if !es.Has("proxies[2].customDomains", ReasonRequired) {
			t.Errorf("%s 缺少域名应报错：%v", typ, es)
		}
	}
	// stcp / xtcp / sudp：secretKey 必填
	for _, i := range []int{5, 6, 7} {
		d := validData()
		d.Proxies[i].SecretKey = ""
		es := asErrors(t, ProfileData(d))
		if !es.Has(proxyField(i, "secretKey"), ReasonRequired) {
			t.Errorf("proxies[%d] 缺少 secretKey 应报错：%v", i, es)
		}
	}
	// 访客：secretKey、serverName 必填
	d := validData()
	d.Visitors[0].SecretKey = ""
	d.Visitors[0].ServerName = ""
	d.Visitors[1].FallbackTo = ""
	es := asErrors(t, ProfileData(d))
	if !es.Has("visitors[0].secretKey", ReasonRequired) || !es.Has("visitors[0].serverName", ReasonRequired) {
		t.Errorf("访客缺字段应报错：%v", es)
	}
	// 未知类型
	d = validData()
	d.Proxies[0].Type = "socks"
	d.Visitors[2].Type = "tcp"
	d.Nodes[0].Protocol = "ftp"
	es = asErrors(t, ProfileData(d))
	if !es.Has("proxies[0].type", ReasonUnknown) || !es.Has("visitors[2].type", ReasonUnknown) || !es.Has("nodes[0].protocol", ReasonUnknown) {
		t.Errorf("未知类型应报错：%v", es)
	}
	// 子域名不能带 .
	d = validData()
	d.Proxies[3].SubDomain = "a.b"
	es = asErrors(t, ProfileData(d))
	if !es.Has("proxies[3].subdomain", ReasonInvalid) {
		t.Errorf("子域名带 . 应报错：%v", es)
	}
}

func proxyField(i int, f string) string { return path("proxies", i, f) }

func TestFallbackTo(t *testing.T) {
	cases := map[string]string{
		"missing": "不存在",
		"rdp-x":   "自己",
		"game":    "不是 stcp",
	}
	for target, why := range cases {
		d := validData()
		d.Visitors[1].FallbackTo = target
		es := asErrors(t, ProfileData(d))
		if !es.Has("visitors[1].fallbackTo", ReasonReference) {
			t.Errorf("fallbackTo=%s（%s）应报错：%v", target, why, es)
		}
	}
}

func TestNodeFields(t *testing.T) {
	d := validData()
	d.Nodes[0].ServerAddr = ""
	d.Nodes[1].ProxyURL = "ftp://x"
	es := asErrors(t, ProfileData(d))
	if !es.Has("nodes[0].serverAddr", ReasonRequired) || !es.Has("nodes[1].proxyURL", ReasonInvalid) {
		t.Errorf("节点字段错误应报出：%v", es)
	}
	for _, bad := range []string{"http://x/y", "a b", "[::1]", "a/b"} {
		d := validData()
		d.Nodes[0].ServerAddr = bad
		es := asErrors(t, ProfileData(d))
		if !es.Has("nodes[0].serverAddr", ReasonInvalid) {
			t.Errorf("serverAddr=%q 应被拒绝：%v", bad, es)
		}
	}
	d = validData()
	d.Nodes[0].Token = strings.Repeat("a", MaxSecretLen+1)
	es = asErrors(t, ProfileData(d))
	if !es.Has("nodes[0].token", ReasonTooLong) {
		t.Errorf("token 过长应报错：%v", es)
	}
}

func TestIDAndName(t *testing.T) {
	if err := ID("id", model.NewID()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "a b", "a/b", strings.Repeat("a", 65), "中文"} {
		if ID("id", bad) == nil {
			t.Errorf("ID %q 应被拒绝", bad)
		}
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 51), "a\nb"} {
		if ProfileName(bad) == nil {
			t.Errorf("配置名 %q 应被拒绝", bad)
		}
	}
	if err := ProfileName("家里的 配置"); err != nil {
		t.Errorf("配置名允许空格：%v", err)
	}
	es := asErrors(t, Profile(model.Profile{}))
	if !es.Has("id", ReasonRequired) || !es.Has("name", ReasonRequired) {
		t.Errorf("空 Profile 应报 id 和 name：%v", es)
	}
}

func TestNickname(t *testing.T) {
	for _, good := range []string{"abc", "Alice_01", strings.Repeat("a", 20)} {
		if err := Nickname(good); err != nil {
			t.Errorf("%q 应该合法：%v", good, err)
		}
	}
	for _, bad := range []string{"", "ab", strings.Repeat("a", 21), "a-b", "中文昵称", "a b", "a.b"} {
		if Nickname(bad) == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	for _, r := range []string{"all", "ALL", "Admin", "root"} {
		es := asErrors(t, Nickname(r))
		if !es.Has("nickname", ReasonReserved) {
			t.Errorf("%q 是保留词：%v", r, es)
		}
	}
}

func TestIsNicknameFormat(t *testing.T) {
	// 保留词只在注册时拒绝；格式检查（登录用）不管保留词
	for _, good := range []string{"admin", "all", "Alice_01", "abc"} {
		if !IsNicknameFormat(good) {
			t.Errorf("%q 格式合法", good)
		}
	}
	for _, bad := range []string{"", "ab", strings.Repeat("a", 21), "a b", "中文昵称", "a.b"} {
		if IsNicknameFormat(bad) {
			t.Errorf("%q 格式不合法", bad)
		}
	}
}

func TestPassword(t *testing.T) {
	if err := Password("password", "12345678"); err != nil {
		t.Error(err)
	}
	if err := Password("password", strings.Repeat("密", 128)); err != nil {
		t.Errorf("按字符数计算长度：%v", err)
	}
	cases := map[string]string{"": ReasonRequired, "1234567": ReasonTooShort, strings.Repeat("a", 129): ReasonTooLong}
	for pw, reason := range cases {
		es := asErrors(t, Password("newPassword", pw))
		if !es.Has("newPassword", reason) {
			t.Errorf("密码长度 %d 应报 %s：%v", len(pw), reason, es)
		}
	}
}

// 向量由旧版 server.py 的 luhn_checksum 现场计算得出（见 docs/phase1.md）。
var legacyInviteVectors = []string{
	"FRPT-AAAA-AAAA",
	"FRPT-2345-678N",
	"FRPT-ZZZZ-ZZZ2",
	"FRPT-MQEF-RPXN",
	"FRPT-K7P2-W9QK",
	"FRPT-HJKM-NPQB",
	"FRPT-9999-9999",
	"FRPT-ABCD-EFGZ",
}

func TestInviteCodeLegacyVectors(t *testing.T) {
	for _, code := range legacyInviteVectors {
		if err := InviteCode(code); err != nil {
			t.Errorf("%s 应该合法：%v", code, err)
		}
		body := code[5:9] + code[10:13]
		got, err := MakeInviteCode(body)
		if err != nil || got != code {
			t.Errorf("MakeInviteCode(%s) = %s, %v; want %s", body, got, err, code)
		}
	}
}

func TestInviteCodeRejects(t *testing.T) {
	cases := map[string]string{
		"":                ReasonRequired,
		"FRPT-AAAA-AAAB":  ReasonChecksum,
		"FRPT-AAAA-AAA":   ReasonInvalid,
		"FRPX-AAAA-AAAA":  ReasonInvalid,
		"FRPT-AAAAAAAA-":  ReasonInvalid,
		"FRPT-IAAA-AAAA":  ReasonInvalid, // I、L、O 不在字符集里
		"FRPT-0AAA-AAAA":  ReasonInvalid,
		"frpt-aaaa-aaaa":  ReasonInvalid, // 需要先 Normalize
		"FRPT-AAAA-AAAA ": ReasonInvalid,
	}
	for code, reason := range cases {
		es := asErrors(t, InviteCode(code))
		if !es.Has("inviteCode", reason) {
			t.Errorf("%q 应报 %s：%v", code, reason, es)
		}
	}
	if InviteCode(NormalizeInviteCode("  frpt-aaaa-aaaa \n")) != nil {
		t.Error("Normalize 后应该合法")
	}
}

func TestNewInviteCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		code := NewInviteCode()
		if err := InviteCode(code); err != nil {
			t.Fatalf("生成的邀请码不合法：%s %v", code, err)
		}
		seen[code] = true
	}
	if len(seen) < 1990 {
		t.Fatalf("随机性不足：2000 个里只有 %d 个不同", len(seen))
	}
	// 旧算法是 Luhn mod 31 的变体：校验位本身的笔误一定能发现；
	// 正文里被"加倍"的位置上 d 与 d+15 会撞（例如 B 和 S），所以只要求绝大多数笔误能发现。
	code := NewInviteCode()
	total, missed := 0, 0
	for pos := 5; pos < len(code); pos++ {
		if code[pos] == '-' {
			continue
		}
		for j := 0; j < len(InviteCharset); j++ {
			c := InviteCharset[j]
			if c == code[pos] {
				continue
			}
			typo := code[:pos] + string(c) + code[pos+1:]
			total++
			if InviteCode(typo) == nil {
				missed++
				if pos == len(code)-1 {
					t.Fatalf("校验位的笔误 %s → %s 没有被发现", code, typo)
				}
			}
		}
	}
	if missed*10 > total {
		t.Fatalf("笔误检出率太低：%d/%d 未发现", missed, total)
	}
}

func TestRandomString(t *testing.T) {
	s := RandomString("ab", 1000)
	if len(s) != 1000 || strings.Trim(s, "ab") != "" {
		t.Fatal("RandomString 输出不对")
	}
	if n := strings.Count(s, "a"); n < 400 || n > 600 {
		t.Fatalf("分布偏差太大：a 出现 %d 次", n)
	}
}

func TestErrorsString(t *testing.T) {
	err := Errors{{Field: "a", Reason: ReasonRequired, Message: "x"}, {Message: "y"}}
	if err.Error() != "validate: a: x; y" {
		t.Fatalf("Error() = %q", err.Error())
	}
}
