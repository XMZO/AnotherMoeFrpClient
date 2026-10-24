// Package model 定义两端共用的数据模型（PLAN 5.1）。
//
// JSON 字段一律 camelCase，并与 frp 的字段同名。
package model

import "time"

// 代理类型，与 frp 的 v1.ProxyType 取值一致。
const (
	ProxyTCP    = "tcp"
	ProxyUDP    = "udp"
	ProxyHTTP   = "http"
	ProxyHTTPS  = "https"
	ProxyTCPMux = "tcpmux"
	ProxySTCP   = "stcp"
	ProxyXTCP   = "xtcp"
	ProxySUDP   = "sudp"
)

// 访客类型，与 frp 的 v1.VisitorType 取值一致。
const (
	VisitorSTCP = "stcp"
	VisitorXTCP = "xtcp"
	VisitorSUDP = "sudp"
)

// 节点连接 frps 的协议，空字符串表示 frp 默认的 tcp。
const (
	ProtocolTCP       = "tcp"
	ProtocolKCP       = "kcp"
	ProtocolQUIC      = "quic"
	ProtocolWebsocket = "websocket"
	ProtocolWSS       = "wss"
)

// ProxyTypes 是全部支持的代理类型，顺序固定。
var ProxyTypes = []string{ProxyTCP, ProxyUDP, ProxyHTTP, ProxyHTTPS, ProxyTCPMux, ProxySTCP, ProxyXTCP, ProxySUDP}

// VisitorTypes 是全部支持的访客类型，顺序固定。
var VisitorTypes = []string{VisitorSTCP, VisitorXTCP, VisitorSUDP}

// Protocols 是节点允许的协议（不含表示默认值的空字符串）。
var Protocols = []string{ProtocolTCP, ProtocolKCP, ProtocolQUIC, ProtocolWebsocket, ProtocolWSS}

// Profile 是一份隧道配置。Revision 与 UpdatedAt 由服务端维护。
type Profile struct {
	ID        string    `json:"id"` // UUIDv7
	Name      string    `json:"name"`
	Nodes     []Node    `json:"nodes"`
	Proxies   []Proxy   `json:"proxies"`
	Visitors  []Visitor `json:"visitors"`
	Revision  int64     `json:"revision"` // 由服务端递增
	UpdatedAt time.Time `json:"updatedAt"`
}

type Node struct {
	ID         string `json:"id"`
	Remark     string `json:"remark"`
	ServerAddr string `json:"serverAddr"`
	ServerPort int    `json:"serverPort"`
	Token      string `json:"token"`
	User       string `json:"user,omitempty"`     // frp 的 user（代理名前缀）
	Protocol   string `json:"protocol,omitempty"` // tcp/kcp/quic/websocket/wss，默认 tcp
	TLS        *bool  `json:"tls,omitempty"`      // nil 表示跟随 frp 默认（开启）
	ProxyURL   string `json:"proxyURL,omitempty"` // 通过代理连接 frps，可选
	// Metadatas 对应 frpc 的 metadatas，登录时发给 frps 插件。官方节点用它带上用户的节点凭证，
	// 用户自己的节点一般不用。
	Metadatas map[string]string `json:"metadatas,omitempty"`
}

type Proxy struct {
	ID             string   `json:"id"` // 稳定 ID：界面和同步都用它，不用行号
	Name           string   `json:"name"`
	Type           string   `json:"type"` // tcp udp http https tcpmux stcp xtcp sudp
	Enabled        bool     `json:"enabled"`
	LocalIP        string   `json:"localIP"`
	LocalPort      int      `json:"localPort"`
	RemotePort     int      `json:"remotePort,omitempty"`
	CustomDomains  []string `json:"customDomains,omitempty"`
	SubDomain      string   `json:"subdomain,omitempty"`
	SecretKey      string   `json:"secretKey,omitempty"`
	AllowUsers     []string `json:"allowUsers,omitempty"`
	UseEncryption  bool     `json:"useEncryption,omitempty"`
	UseCompression bool     `json:"useCompression,omitempty"`
	// WithUDP 只用于 tcp 代理：同一个本地端口、同一个远程端口同时转发 UDP（很多游戏两种都要）。
	// 运行时拆成两个 frp 代理：Name（tcp）和 UDPName()（udp），在节点上占两个代理名额。
	WithUDP bool `json:"withUdp,omitempty"`
}

// UDPNameSuffix 是 tcp+udp 代理里 udp 那一半的 frp 代理名后缀。
const UDPNameSuffix = "-udp"

// UDPName 是 tcp+udp 代理里 udp 那一半的 frp 代理名。
func (p Proxy) UDPName() string { return p.Name + UDPNameSuffix }

// FrpNames 返回这个代理在 frp 里的名字：一般是一个，tcp+udp 是两个。
func (p Proxy) FrpNames() []string {
	if p.Type == ProxyTCP && p.WithUDP {
		return []string{p.Name, p.UDPName()}
	}
	return []string{p.Name}
}

type Visitor struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // stcp xtcp sudp
	Enabled    bool   `json:"enabled"`
	ServerName string `json:"serverName"`
	ServerUser string `json:"serverUser,omitempty"`
	SecretKey  string `json:"secretKey"`
	BindAddr   string `json:"bindAddr"` // 默认 127.0.0.1
	BindPort   int    `json:"bindPort"`
	FallbackTo string `json:"fallbackTo,omitempty"` // xtcp 打洞失败时回退到某个 stcp visitor
	// 加密、压缩：xtcp 打洞成功后两边直连，必须和对方代理的设置一致，否则数据对不上、连不通；
	// stcp / sudp 经过 frps 中转，访客这一段和代理那一段各自独立，不需要一致。
	UseEncryption  bool `json:"useEncryption,omitempty"`
	UseCompression bool `json:"useCompression,omitempty"`
}

// ProfileData 是配置里除元数据以外的内容，服务端把它整体存成一列 JSON。
type ProfileData struct {
	Nodes    []Node    `json:"nodes"`
	Proxies  []Proxy   `json:"proxies"`
	Visitors []Visitor `json:"visitors"`
}

// Data 取出配置内容。
func (p Profile) Data() ProfileData {
	return ProfileData{Nodes: p.Nodes, Proxies: p.Proxies, Visitors: p.Visitors}
}

// Normalize 把 nil 切片换成空切片，保证 JSON 里输出的是 [] 而不是 null。
func (d *ProfileData) Normalize() {
	if d.Nodes == nil {
		d.Nodes = []Node{}
	}
	if d.Proxies == nil {
		d.Proxies = []Proxy{}
	}
	if d.Visitors == nil {
		d.Visitors = []Visitor{}
	}
}

// Normalize 同 ProfileData.Normalize。
func (p *Profile) Normalize() {
	d := p.Data()
	d.Normalize()
	p.Nodes, p.Proxies, p.Visitors = d.Nodes, d.Proxies, d.Visitors
}

// EnsureIDs 给缺少 ID 的节点、代理、访客补上新 ID，返回是否有改动。
// 已有的 ID 原样保留。
func (d *ProfileData) EnsureIDs() bool {
	changed := false
	for i := range d.Nodes {
		if d.Nodes[i].ID == "" {
			d.Nodes[i].ID = NewID()
			changed = true
		}
	}
	for i := range d.Proxies {
		if d.Proxies[i].ID == "" {
			d.Proxies[i].ID = NewID()
			changed = true
		}
	}
	for i := range d.Visitors {
		if d.Visitors[i].ID == "" {
			d.Visitors[i].ID = NewID()
			changed = true
		}
	}
	return changed
}

// IsP2PType 判断代理类型是否需要 secretKey（stcp / xtcp / sudp）。
func IsP2PType(proxyType string) bool {
	switch proxyType {
	case ProxySTCP, ProxyXTCP, ProxySUDP:
		return true
	}
	return false
}

// IsDomainType 判断代理类型是否按域名路由（http / https / tcpmux）。
func IsDomainType(proxyType string) bool {
	switch proxyType {
	case ProxyHTTP, ProxyHTTPS, ProxyTCPMux:
		return true
	}
	return false
}

// HasRemotePort 判断代理类型是否使用 remotePort（tcp / udp）。
func HasRemotePort(proxyType string) bool {
	return proxyType == ProxyTCP || proxyType == ProxyUDP
}
