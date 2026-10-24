package engine

import (
	"fmt"
	"maps"

	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"

	"moefrp/client/internal/apperr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// tcpmux 只支持 httpconnect 这一种复用方式（frp 文档同样只有这一种）。
const tcpMuxMultiplexer = "httpconnect"

// Prepared 是校验通过、可以交给 frp 的一份配置。
type Prepared struct {
	Profile  model.Profile
	Node     model.Node
	Common   *v1.ClientCommonConfig
	Proxies  []v1.ProxyConfigurer
	Visitors []v1.VisitorConfigurer
	Unsafe   *security.UnsafeFeatures
}

// Prepare 按 PLAN 6.3.2 准备启动或热更新用的配置：
//
//  1. shared/validate 校验整份配置（含代理名、访客名重名，frp 自己不查）；
//  2. 找到选中的节点，组装 ClientCommonConfig；
//  3. 和 frpc 一样走一遍 ConfigSource → Aggregator → Filter / Complete → ValidateAllClientConfig。
//
// 校验用的是单独映射出来的一份对象（Complete 会改写字段），返回给调用方的是没有补全过的一份，
// 由 frp 在 NewService / UpdateConfigSource 里自己补全，和 frpc 的流程一致。
//
// extra 是配置之外也能选的节点（面板下发的官方节点），按 ID 和配置里的节点一起查找。
func Prepare(p model.Profile, nodeID string, extra ...model.Node) (*Prepared, error) {
	if err := validate.Profile(p); err != nil {
		return nil, apperr.FromValidate(err)
	}
	if nodeID == "" {
		return nil, apperr.New(apperr.NodeRequired, "no node selected")
	}
	var node *model.Node
	for i := range p.Nodes {
		if p.Nodes[i].ID == nodeID {
			node = &p.Nodes[i]
			break
		}
	}
	for i := range extra {
		if node == nil && extra[i].ID == nodeID {
			node = &extra[i]
		}
	}
	if node == nil {
		return nil, apperr.New(apperr.NodeNotFound, "node "+nodeID+" not in profile")
	}

	common, err := BuildCommon(*node)
	if err != nil {
		return nil, apperr.Wrap(apperr.FrpConfig, err)
	}
	unsafe := security.NewUnsafeFeatures(nil)
	if err := frpValidate(common, p, unsafe); err != nil {
		return nil, err
	}
	proxies, visitors, err := MapProfile(p)
	if err != nil {
		return nil, apperr.Wrap(apperr.FrpConfig, err)
	}
	return &Prepared{Profile: p, Node: *node, Common: common, Proxies: proxies, Visitors: visitors, Unsafe: unsafe}, nil
}

// BuildCommon 把节点转换成 frp 的公共配置。不设置 WebServer.Port，所以不会开 frpc 的管理端口。
func BuildCommon(n model.Node) (*v1.ClientCommonConfig, error) {
	common := &v1.ClientCommonConfig{
		ServerAddr: n.ServerAddr,
		ServerPort: n.ServerPort,
		User:       n.User,
	}
	common.Auth.Token = n.Token
	if len(n.Metadatas) > 0 {
		common.Metadatas = maps.Clone(n.Metadatas) // 官方节点的节点凭证
	}
	common.Transport.Protocol = n.Protocol
	common.Transport.ProxyURL = n.ProxyURL
	if n.TLS != nil {
		v := *n.TLS
		common.Transport.TLS.Enable = &v // nil 时由 Complete() 补成 true
	}
	if err := common.Complete(); err != nil { // 同时把 LoginFailExit 补成 true
		return nil, err
	}
	// Complete 在没填代理时会读 http_proxy 环境变量；代理只由节点设置和"网络代理"设置决定，不悄悄走环境变量
	common.Transport.ProxyURL = n.ProxyURL
	return common, nil
}

// SameConnection 判断两个节点连接 frps 的参数是否相同。不同就必须新建 Service（PLAN 6.3.5）。
// 备注（Remark）不影响连接。
func SameConnection(a, b model.Node) bool {
	tlsA, tlsB := a.TLS == nil || *a.TLS, b.TLS == nil || *b.TLS
	protoA, protoB := a.Protocol, b.Protocol
	if protoA == "" {
		protoA = model.ProtocolTCP
	}
	if protoB == "" {
		protoB = model.ProtocolTCP
	}
	return a.ServerAddr == b.ServerAddr && a.ServerPort == b.ServerPort && a.Token == b.Token &&
		a.User == b.User && protoA == protoB && tlsA == tlsB && a.ProxyURL == b.ProxyURL && maps.Equal(a.Metadatas, b.Metadatas)
}

// MapProfile 把模型里的代理和访客转换成 frp 的配置对象（PLAN 6.3.3）。
// 未启用的也会转换，Enabled=false，由 FilterClientConfigurers 过滤掉。
// tcp+udp 的代理（WithUDP）拆成两个：同名的 tcp 和 name-udp 的 udp，本地端口、远程端口相同。
func MapProfile(p model.Profile) ([]v1.ProxyConfigurer, []v1.VisitorConfigurer, error) {
	proxies := make([]v1.ProxyConfigurer, 0, len(p.Proxies))
	for _, px := range p.Proxies {
		c, err := MapProxy(px)
		if err != nil {
			return nil, nil, err
		}
		proxies = append(proxies, c)
		if px.Type == model.ProxyTCP && px.WithUDP {
			u := px
			u.Type, u.Name, u.WithUDP = model.ProxyUDP, px.UDPName(), false
			c, err := MapProxy(u)
			if err != nil {
				return nil, nil, err
			}
			proxies = append(proxies, c)
		}
	}
	visitors := make([]v1.VisitorConfigurer, 0, len(p.Visitors))
	for _, vs := range p.Visitors {
		c, err := MapVisitor(vs)
		if err != nil {
			return nil, nil, err
		}
		visitors = append(visitors, c)
	}
	return proxies, visitors, nil
}

// MapProxy 转换一个代理。所有类型共用的字段都在 ProxyBaseConfig 里。
func MapProxy(p model.Proxy) (v1.ProxyConfigurer, error) {
	c := v1.NewProxyConfigurerByType(v1.ProxyType(p.Type))
	if c == nil {
		return nil, fmt.Errorf("proxy %s: unsupported type %q", p.Name, p.Type)
	}
	base := c.GetBaseConfig()
	base.Name = p.Name
	enabled := p.Enabled
	base.Enabled = &enabled
	base.Transport.UseEncryption = p.UseEncryption
	base.Transport.UseCompression = p.UseCompression
	base.LocalIP = p.LocalIP
	base.LocalPort = p.LocalPort

	domains := v1.DomainConfig{CustomDomains: append([]string(nil), p.CustomDomains...), SubDomain: p.SubDomain}
	allow := append([]string(nil), p.AllowUsers...)
	switch t := c.(type) {
	case *v1.TCPProxyConfig:
		t.RemotePort = p.RemotePort
	case *v1.UDPProxyConfig:
		t.RemotePort = p.RemotePort
	case *v1.HTTPProxyConfig:
		t.DomainConfig = domains
	case *v1.HTTPSProxyConfig:
		t.DomainConfig = domains
	case *v1.TCPMuxProxyConfig:
		t.DomainConfig = domains
		t.Multiplexer = tcpMuxMultiplexer
	case *v1.STCPProxyConfig:
		t.Secretkey = p.SecretKey // 注意 Go 字段名是 Secretkey
		t.AllowUsers = allow
	case *v1.XTCPProxyConfig:
		t.Secretkey = p.SecretKey
		t.AllowUsers = allow
	case *v1.SUDPProxyConfig:
		t.Secretkey = p.SecretKey
		t.AllowUsers = allow
	default:
		return nil, fmt.Errorf("proxy %s: type %q is not supported by this client", p.Name, p.Type)
	}
	return c, nil
}

// MapVisitor 转换一个访客。
func MapVisitor(v model.Visitor) (v1.VisitorConfigurer, error) {
	c := v1.NewVisitorConfigurerByType(v1.VisitorType(v.Type))
	if c == nil {
		return nil, fmt.Errorf("visitor %s: unsupported type %q", v.Name, v.Type)
	}
	base := c.GetBaseConfig()
	base.Name = v.Name
	enabled := v.Enabled
	base.Enabled = &enabled
	base.SecretKey = v.SecretKey
	base.ServerName = v.ServerName
	base.ServerUser = v.ServerUser
	base.BindAddr = v.BindAddr // 为空时 Complete() 补成 127.0.0.1
	base.BindPort = v.BindPort
	// xtcp 必须和对方代理一致（打洞后直连）；stcp / sudp 只影响访客到 frps 这一段
	base.Transport.UseEncryption = v.UseEncryption
	base.Transport.UseCompression = v.UseCompression
	switch t := c.(type) {
	case *v1.STCPVisitorConfig, *v1.SUDPVisitorConfig:
	case *v1.XTCPVisitorConfig:
		t.FallbackTo = v.FallbackTo
	default:
		return nil, fmt.Errorf("visitor %s: type %q is not supported by this client", v.Name, v.Type)
	}
	return c, nil
}

// frpValidate 和 frpc 一样：先过滤、补全，再校验。
func frpValidate(common *v1.ClientCommonConfig, p model.Profile, unsafe *security.UnsafeFeatures) error {
	proxies, visitors, err := MapProfile(p)
	if err != nil {
		return apperr.Wrap(apperr.FrpConfig, err)
	}
	cs := source.NewConfigSource()
	if err := cs.ReplaceAll(proxies, visitors); err != nil {
		return apperr.Wrap(apperr.FrpConfig, err)
	}
	ps, vs, err := source.NewAggregator(cs).Load()
	if err != nil {
		return apperr.Wrap(apperr.FrpConfig, err)
	}
	ps, vs = config.FilterClientConfigurers(common, ps, vs)
	ps = config.CompleteProxyConfigurers(ps)
	vs = config.CompleteVisitorConfigurers(vs)
	if _, err := validation.ValidateAllClientConfig(common, ps, vs, unsafe); err != nil {
		return apperr.Wrap(apperr.FrpConfig, err)
	}
	return nil
}
