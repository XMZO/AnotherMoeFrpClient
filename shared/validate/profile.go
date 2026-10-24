package validate

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"moefrp/shared/model"
)

// 数量上限（PLAN 5.2）。
const (
	MaxNodesPerProfile    = 20
	MaxProxiesPerProfile  = 100
	MaxVisitorsPerProfile = 50

	MaxProfilesPerUser      = 50
	MaxSharesPerUser        = 50
	MaxSubscriptionsPerUser = 100
)

// 长度上限。PLAN 只规定了代理名、访客名；其余是实现时定的保守值。
const (
	MaxNameLen        = 50  // 代理名、访客名（字符数）
	MaxProfileNameLen = 50  // 配置名（字符数）
	MaxRemarkLen      = 50  // 节点备注（字符数）
	MaxIDLen          = 64  // 稳定 ID
	MaxHostLen        = 253 // 地址、域名
	MaxSecretLen      = 256 // 节点 token、secretKey
	MaxUserLen        = 50  // frp user、serverUser、allowUsers 的每一项
	MaxServerNameLen  = 100 // 访客的 serverName
	MaxSubDomainLen   = 63
	MaxCustomDomains  = 20
	MaxAllowUsers     = 50
	MaxProxyURLLen    = 512
	MaxMetadatas      = 8 // 节点 metadatas 的条数
	MaxMetaKeyLen     = 32
	MaxMetaValueLen   = 256
	MaxPort           = 65535
	MinPort           = 1
)

var metaKeyRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// ProxyURLSchemes 是节点 proxyURL 允许的协议，与 frp 支持的一致。
var ProxyURLSchemes = []string{"http", "socks5", "ntlm"}

// Profile 校验整份配置：ID、名称、内容。
func Profile(p model.Profile) error {
	var c collector
	c.merge(ID("id", p.ID))
	c.merge(ProfileName(p.Name))
	c.merge(ProfileData(p.Data()))
	return c.err()
}

// ID 校验稳定 ID：1–64 个字符，只能是字母、数字、- 和 _。UUIDv7 满足这个规则。
func ID(field, s string) error {
	var c collector
	checkID(&c, field, s)
	return c.err()
}

// ProfileName 校验配置名：去掉首尾空白后不能为空，最多 50 个字符，不能有控制字符。
func ProfileName(name string) error {
	var c collector
	checkText(&c, "name", name, MaxProfileNameLen, true)
	return c.err()
}

// ProfileData 校验配置内容：节点、代理、访客。
//
// 代理名和访客名合在一起检查重名。frp 的 ConfigSource.ReplaceAll 以名字为 key，
// 重名时后一个会静默覆盖前一个，ValidateAllClientConfig 也不查重名，所以只能靠这里。
func ProfileData(d model.ProfileData) error {
	var c collector

	if len(d.Nodes) > MaxNodesPerProfile {
		c.add("nodes", ReasonTooMany, "节点最多 %d 个", MaxNodesPerProfile)
	}
	if len(d.Proxies) > MaxProxiesPerProfile {
		c.add("proxies", ReasonTooMany, "代理最多 %d 个", MaxProxiesPerProfile)
	}
	if len(d.Visitors) > MaxVisitorsPerProfile {
		c.add("visitors", ReasonTooMany, "访客最多 %d 个", MaxVisitorsPerProfile)
	}

	nodeIDs := map[string]bool{}
	for i, n := range d.Nodes {
		checkNode(&c, i, n)
		if n.ID != "" {
			if nodeIDs[n.ID] {
				c.add(path("nodes", i, "id"), ReasonDuplicate, "节点 ID 重复")
			}
			nodeIDs[n.ID] = true
		}
	}

	names := map[string]string{} // 名字 → 第一次出现的位置
	checkName := func(field, name string) {
		if name == "" {
			return
		}
		if first, ok := names[name]; ok {
			c.add(field, ReasonDuplicate, "名称 %q 与 %s 重复（代理和访客的名称不能相同）", name, first)
			return
		}
		names[name] = field
	}

	proxyIDs := map[string]bool{}
	for i, p := range d.Proxies {
		checkProxy(&c, i, p)
		checkName(path("proxies", i, "name"), p.Name)
		if p.Type == model.ProxyTCP && p.WithUDP && p.Name != "" {
			// udp 那一半在 frp 里叫 name-udp，也不能和别的代理、访客重名
			if first, ok := names[p.UDPName()]; ok {
				c.add(path("proxies", i, "name"), ReasonDuplicate, "同时转发 UDP 时会用到名称 %q，与 %s 重复", p.UDPName(), first)
			} else {
				names[p.UDPName()] = path("proxies", i, "name")
			}
		}
		if p.ID != "" {
			if proxyIDs[p.ID] {
				c.add(path("proxies", i, "id"), ReasonDuplicate, "代理 ID 重复")
			}
			proxyIDs[p.ID] = true
		}
	}

	visitorIDs := map[string]bool{}
	visitorTypes := map[string]string{} // 访客名 → 类型，用于检查 fallbackTo
	for _, v := range d.Visitors {
		if _, ok := visitorTypes[v.Name]; !ok {
			visitorTypes[v.Name] = v.Type
		}
	}
	for i, v := range d.Visitors {
		checkVisitor(&c, i, v, visitorTypes)
		checkName(path("visitors", i, "name"), v.Name)
		if v.ID != "" {
			if visitorIDs[v.ID] {
				c.add(path("visitors", i, "id"), ReasonDuplicate, "访客 ID 重复")
			}
			visitorIDs[v.ID] = true
		}
	}

	return c.err()
}

func checkNode(c *collector, i int, n model.Node) {
	checkID(c, path("nodes", i, "id"), n.ID)
	checkText(c, path("nodes", i, "remark"), n.Remark, MaxRemarkLen, false)
	checkHost(c, path("nodes", i, "serverAddr"), n.ServerAddr, true)
	checkPort(c, path("nodes", i, "serverPort"), n.ServerPort, true)
	checkSecret(c, path("nodes", i, "token"), n.Token, false)
	checkWord(c, path("nodes", i, "user"), n.User, MaxUserLen, false)
	if n.Protocol != "" && !slices.Contains(model.Protocols, n.Protocol) {
		c.add(path("nodes", i, "protocol"), ReasonUnknown, "不支持的协议 %q", n.Protocol)
	}
	if len(n.Metadatas) > MaxMetadatas {
		c.add(path("nodes", i, "metadatas"), ReasonTooLong, "metadatas 最多 %d 项", MaxMetadatas)
	}
	for k, v := range n.Metadatas {
		field := path("nodes", i, "metadatas")
		if !metaKeyRe.MatchString(k) {
			c.add(field, ReasonInvalid, "metadatas 的键 %q 只能是 1–%d 位小写字母、数字和下划线", k, MaxMetaKeyLen)
		} else if len(v) > MaxMetaValueLen {
			c.add(field, ReasonTooLong, "metadatas 的值最多 %d 个字符", MaxMetaValueLen)
		}
	}
	if n.ProxyURL != "" {
		field := path("nodes", i, "proxyURL")
		if len(n.ProxyURL) > MaxProxyURLLen {
			c.add(field, ReasonTooLong, "代理地址最多 %d 个字符", MaxProxyURLLen)
		} else if u, err := url.Parse(n.ProxyURL); err != nil || u.Host == "" || !slices.Contains(ProxyURLSchemes, u.Scheme) {
			c.add(field, ReasonInvalid, "代理地址格式应为 http://、socks5:// 或 ntlm:// 开头的 URL")
		}
	}
}

func checkProxy(c *collector, i int, p model.Proxy) {
	checkID(c, path("proxies", i, "id"), p.ID)
	checkProxyName(c, path("proxies", i, "name"), p.Name)
	if !slices.Contains(model.ProxyTypes, p.Type) {
		if p.Type == "" {
			c.add(path("proxies", i, "type"), ReasonRequired, "代理类型不能为空")
		} else {
			c.add(path("proxies", i, "type"), ReasonUnknown, "不支持的代理类型 %q", p.Type)
		}
	}
	checkHost(c, path("proxies", i, "localIP"), p.LocalIP, false)
	// 不支持插件，所以每种类型都需要本地端口（frp 在没有插件时同样要求 localPort）。
	checkPort(c, path("proxies", i, "localPort"), p.LocalPort, true)
	if p.RemotePort != 0 {
		checkPort(c, path("proxies", i, "remotePort"), p.RemotePort, false)
	}
	if p.WithUDP {
		switch {
		case p.Type != model.ProxyTCP:
			c.add(path("proxies", i, "withUdp"), ReasonInvalid, "只有 tcp 代理可以同时转发 UDP")
		case p.RemotePort == 0:
			// 留空时节点给 tcp 和 udp 各分配一个随机端口，两边对不上
			c.add(path("proxies", i, "remotePort"), ReasonRequired, "同时转发 TCP 和 UDP 时要填远程端口（两种协议用同一个端口）")
		}
	}

	for j, d := range p.CustomDomains {
		field := path("proxies", i, "customDomains") + "[" + strconv.Itoa(j) + "]"
		checkHost(c, field, d, true)
	}
	if len(p.CustomDomains) > MaxCustomDomains {
		c.add(path("proxies", i, "customDomains"), ReasonTooMany, "自定义域名最多 %d 个", MaxCustomDomains)
	}
	if p.SubDomain != "" {
		field := path("proxies", i, "subdomain")
		if len(p.SubDomain) > MaxSubDomainLen {
			c.add(field, ReasonTooLong, "子域名最多 %d 个字符", MaxSubDomainLen)
		} else if !isSubDomain(p.SubDomain) {
			c.add(field, ReasonInvalid, "子域名只能包含字母、数字和 -")
		}
	}
	if model.IsDomainType(p.Type) && len(p.CustomDomains) == 0 && p.SubDomain == "" {
		c.add(path("proxies", i, "customDomains"), ReasonRequired, "%s 代理需要填写自定义域名或子域名", p.Type)
	}

	checkSecret(c, path("proxies", i, "secretKey"), p.SecretKey, model.IsP2PType(p.Type))
	for j, u := range p.AllowUsers {
		field := path("proxies", i, "allowUsers") + "[" + strconv.Itoa(j) + "]"
		checkWord(c, field, u, MaxUserLen, true)
	}
	if len(p.AllowUsers) > MaxAllowUsers {
		c.add(path("proxies", i, "allowUsers"), ReasonTooMany, "允许的用户最多 %d 个", MaxAllowUsers)
	}
}

func checkVisitor(c *collector, i int, v model.Visitor, visitorTypes map[string]string) {
	checkID(c, path("visitors", i, "id"), v.ID)
	checkProxyName(c, path("visitors", i, "name"), v.Name)
	if !slices.Contains(model.VisitorTypes, v.Type) {
		if v.Type == "" {
			c.add(path("visitors", i, "type"), ReasonRequired, "访客类型不能为空")
		} else {
			c.add(path("visitors", i, "type"), ReasonUnknown, "不支持的访客类型 %q", v.Type)
		}
	}
	checkWord(c, path("visitors", i, "serverName"), v.ServerName, MaxServerNameLen, true)
	checkWord(c, path("visitors", i, "serverUser"), v.ServerUser, MaxUserLen, false)
	checkSecret(c, path("visitors", i, "secretKey"), v.SecretKey, true)
	checkHost(c, path("visitors", i, "bindAddr"), v.BindAddr, false)
	checkPort(c, path("visitors", i, "bindPort"), v.BindPort, true)

	if v.Type == model.VisitorXTCP && v.FallbackTo != "" {
		field := path("visitors", i, "fallbackTo")
		t, ok := visitorTypes[v.FallbackTo]
		switch {
		case v.FallbackTo == v.Name:
			c.add(field, ReasonReference, "不能回退到自己")
		case !ok:
			c.add(field, ReasonReference, "回退目标 %q 不存在", v.FallbackTo)
		case t != model.VisitorSTCP:
			c.add(field, ReasonReference, "回退目标 %q 必须是 stcp 访客", v.FallbackTo)
		}
	}
}

// ---- 字段级规则 ----

func checkID(c *collector, field, s string) {
	switch {
	case s == "":
		c.add(field, ReasonRequired, "ID 不能为空")
	case len(s) > MaxIDLen:
		c.add(field, ReasonTooLong, "ID 最多 %d 个字符", MaxIDLen)
	default:
		for i := 0; i < len(s); i++ {
			b := s[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				c.add(field, ReasonInvalid, "ID 只能包含字母、数字、- 和 _")
				return
			}
		}
	}
}

// checkProxyName：1–50 个字符；只能是可打印字符，不能有空白和 "."（"." 会和 frp 的 user. 前缀混淆）。
func checkProxyName(c *collector, field, s string) {
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0:
		c.add(field, ReasonRequired, "名称不能为空")
	case n > MaxNameLen:
		c.add(field, ReasonTooLong, "名称最多 %d 个字符", MaxNameLen)
	default:
		for _, r := range s {
			if r == '.' || r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
				c.add(field, ReasonInvalid, "名称只能是可打印字符，不能有空白和 \".\"")
				return
			}
		}
	}
}

// checkText：普通文本，允许空格，不允许控制字符。
func checkText(c *collector, field, s string, maxLen int, required bool) {
	if required && strings.TrimSpace(s) == "" {
		c.add(field, ReasonRequired, "不能为空")
		return
	}
	if utf8.RuneCountInString(s) > maxLen {
		c.add(field, ReasonTooLong, "最多 %d 个字符", maxLen)
		return
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			c.add(field, ReasonInvalid, "不能包含控制字符或非 UTF-8 字符")
			return
		}
	}
}

// checkWord：可打印、不含空白的短字符串（frp user、serverName 等）。
func checkWord(c *collector, field, s string, maxLen int, required bool) {
	if s == "" {
		if required {
			c.add(field, ReasonRequired, "不能为空")
		}
		return
	}
	if utf8.RuneCountInString(s) > maxLen {
		c.add(field, ReasonTooLong, "最多 %d 个字符", maxLen)
		return
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			c.add(field, ReasonInvalid, "只能是可打印字符，不能有空白")
			return
		}
	}
}

// checkSecret：节点 token、secretKey。不能有控制字符，最多 256 字节。
func checkSecret(c *collector, field, s string, required bool) {
	if s == "" {
		if required {
			c.add(field, ReasonRequired, "不能为空")
		}
		return
	}
	if len(s) > MaxSecretLen {
		c.add(field, ReasonTooLong, "最多 %d 个字节", MaxSecretLen)
		return
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			c.add(field, ReasonInvalid, "不能包含控制字符或非 UTF-8 字符")
			return
		}
	}
}

// checkHost：IP、域名或主机名。允许字母（含非 ASCII）、数字和 - . _ : *，
// 不允许端口以外的 URL 成分，也不允许方括号形式的 IPv6。
func checkHost(c *collector, field, s string, required bool) {
	if s == "" {
		if required {
			c.add(field, ReasonRequired, "不能为空")
		}
		return
	}
	if len(s) > MaxHostLen {
		c.add(field, ReasonTooLong, "最多 %d 个字符", MaxHostLen)
		return
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-._:*", r)) {
			c.add(field, ReasonInvalid, "地址格式不正确")
			return
		}
	}
}

func checkPort(c *collector, field string, port int, required bool) {
	if port == 0 && required {
		c.add(field, ReasonRequired, "端口不能为空")
		return
	}
	if port < MinPort || port > MaxPort {
		c.add(field, ReasonRange, "端口范围是 %d–%d", MinPort, MaxPort)
	}
}

func isSubDomain(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}
