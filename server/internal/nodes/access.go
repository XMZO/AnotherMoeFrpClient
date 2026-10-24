package nodes

// 用户访问地址（docs/node-agent.md 第 12 节）：用户在客户端里看到、复制，用来访问自己映射在这个节点上的
// tcp / udp 代理（例如 sh1.example.com:10405）。域名由管理员填；IPv4、IPv6 默认自动检测
// （面板看到的节点连接地址 + agent 上报的网卡公网地址），管理员可以改，填 "-" 表示不显示。

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"strings"

	"moefrp/server/internal/store"
	"moefrp/shared/agentproto"
)

// hiddenAccess 表示这一项不显示（不自动检测）。
const hiddenAccess = "-"

// Access 是管理员设置的访问地址：空表示自动检测（域名没有自动），"-" 表示不显示。
type Access struct {
	Domain string `json:"domain"`
	IPv4   string `json:"ipv4"`
	IPv6   string `json:"ipv6"`
}

// AccessView 是后台显示的访问地址：设置值、检测到的值和最终给用户的值。
type AccessView struct {
	Set        Access `json:"set"`
	DetectedV4 string `json:"detectedV4,omitempty"`
	DetectedV6 string `json:"detectedV6,omitempty"`
	Effective  Access `json:"effective"`
}

func accessOf(n store.Node) Access {
	var a Access
	if n.Access != "" {
		_ = json.Unmarshal([]byte(n.Access), &a)
	}
	return a
}

// detectIPs 从节点的连接地址和 agent 上报的网卡地址里找公网 IPv4 和 IPv6（各取第一个）。
func detectIPs(n store.Node) (v4, v6 string) {
	cands := []string{n.LastIP}
	if n.Info != "" {
		var h agentproto.Hello
		if json.Unmarshal([]byte(n.Info), &h) == nil {
			cands = append(cands, h.PublicIPs...)
		}
	}
	for _, c := range cands {
		ip := net.ParseIP(strings.TrimSpace(c))
		if ip == nil || !publicIP(ip) {
			continue
		}
		if ip.To4() != nil {
			if v4 == "" {
				v4 = ip.String()
			}
		} else if v6 == "" {
			v6 = ip.String()
		}
	}
	return v4, v6
}

func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) // 100.64.0.0/10 运营商级 NAT
}

func accessView(n store.Node) AccessView {
	set := accessOf(n)
	v4, v6 := detectIPs(n)
	pick := func(setv, detected string) string {
		switch setv {
		case hiddenAccess:
			return ""
		case "":
			return detected
		}
		return setv
	}
	eff := Access{Domain: pick(set.Domain, ""), IPv4: pick(set.IPv4, v4), IPv6: pick(set.IPv6, v6)}
	return AccessView{Set: set, DetectedV4: v4, DetectedV6: v6, Effective: eff}
}

// SetAccess 保存访问地址，马上推给能用这个节点的在线客户端。
func (s *Service) SetAccess(ctx context.Context, by Actor, id string, a Access) error {
	a.Domain = strings.ToLower(strings.TrimSpace(a.Domain))
	a.IPv4, a.IPv6 = strings.TrimSpace(a.IPv4), strings.TrimSpace(a.IPv6)
	if a.Domain != "" && a.Domain != hiddenAccess && !validDomain(a.Domain) {
		return badRequest("domain", "域名格式不对，例如 sh1.example.com（不要带 http:// 和端口）")
	}
	if a.IPv4 != "" && a.IPv4 != hiddenAccess {
		if ip := net.ParseIP(a.IPv4); ip == nil || ip.To4() == nil {
			return badRequest("ipv4", "IPv4 格式不对，例如 1.2.3.4；留空自动检测，填 - 不显示")
		}
	}
	if a.IPv6 != "" && a.IPv6 != hiddenAccess {
		if ip := net.ParseIP(a.IPv6); ip == nil || ip.To4() != nil {
			return badRequest("ipv6", "IPv6 格式不对，例如 2001:db8::1；留空自动检测，填 - 不显示")
		}
		a.IPv6 = net.ParseIP(a.IPv6).String()
	}
	b, _ := json.Marshal(a)
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.SetNodeAccess(ctx, tx, id, string(b), s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeUpdate, id, map[string]any{"access": a})
	})
	if err != nil {
		return notFound(err)
	}
	s.officialChanged()
	return nil
}

func validDomain(d string) bool {
	if len(d) > 253 || !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
