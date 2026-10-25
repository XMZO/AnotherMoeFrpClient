// Package frpsset 是节点 frps 的结构化配置（docs/node-agent.md 第 4 节）。
//
// 管理后台不再编辑 TOML：每个配置项在 Settings 上用 ui 标签描述一次（分组、名称、说明、取值范围），
// 后台按 Schema() 自动生成表单，agent 按字段在内存里拼出 frps 的配置。加一个配置项只要：
// 在 Settings 里加字段和 ui 标签、在 Validate 里加必要的检查、在 agent 的 frpsrv.ServerConfig 里映射。
//
// 节点都由面板托管：frps 不设 auth.token，只认面板签发的登录票据，所以这里没有 token。
package frpsset

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"moefrp/shared/quota"
)

// Settings 是一个节点的 frps 配置。零值表示"不启用 / 用 frps 默认值"，见各字段说明。
type Settings struct {
	// 基础
	BindAddr          string `json:"bindAddr" ui:"group=basic;label=监听地址;placeholder=0.0.0.0;hint=留空监听所有网卡"`
	BindPort          int    `json:"bindPort" ui:"group=basic;label=客户端连接端口;min=1;max=65535;hint=客户端用 tcp / websocket 协议时连这个端口"`
	AllowPorts        string `json:"allowPorts" ui:"group=basic;kind=ports;label=允许映射的端口;placeholder=不限，例如 10000-20000,30000;hint=tcp / udp 代理的远程端口范围；用户组和单个用户还可以再收窄"`
	MaxPortsPerClient int    `json:"maxPortsPerClient" ui:"group=basic;label=每个客户端最多占用端口;min=0;max=65535;hint=0 不限"`
	LogLevel          string `json:"logLevel" ui:"group=basic;kind=select;label=日志级别;options=trace,debug,info,warn,error;hint=在节点的「日志」页查看"`
	// VnetRelayPort 是联机房间的中继端口（agent 1.4.6 起，不是 frps 的）：打洞打不通的成员经它转发，0 关闭
	VnetRelayPort int `json:"vnetRelayPort,omitempty" ui:"group=basic;label=联机中继端口;min=0;max=65535;hint=TCP。联机房间里打洞打不通的成员经这个端口中继（需要在防火墙放行）；0 关闭"`

	// 传输协议
	KCPBindPort  int  `json:"kcpBindPort" ui:"group=transport;label=KCP 端口;min=0;max=65535;hint=UDP，可以和客户端连接端口相同；0 关闭"`
	QUICBindPort int  `json:"quicBindPort" ui:"group=transport;label=QUIC 端口;min=0;max=65535;hint=UDP，可以和客户端连接端口相同；0 关闭"`
	TCPMux       bool `json:"tcpMux" ui:"group=transport;label=TCP 多路复用;hint=一条连接承载所有代理，建议开启（客户端默认开启）"`
	TLSForce     bool `json:"tlsForce" ui:"group=transport;label=只接受加密连接;hint=拒绝没有开 TLS 的客户端（MoeFrp 客户端默认开 TLS）"`

	// HTTP / HTTPS 虚拟主机
	VhostHTTPPort         int    `json:"vhostHTTPPort" ui:"group=vhost;label=HTTP 端口;min=0;max=65535;hint=http 代理的入口，常用 80；0 关闭"`
	VhostHTTPSPort        int    `json:"vhostHTTPSPort" ui:"group=vhost;label=HTTPS 端口;min=0;max=65535;hint=https 代理的入口，常用 443；0 关闭"`
	SubDomainHost         string `json:"subDomainHost" ui:"group=vhost;label=子域名后缀;placeholder=例如 frp.example.com;hint=代理填子域名 abc 时访问 abc.后缀，需要泛域名解析到节点"`
	TCPMuxHTTPConnectPort int    `json:"tcpmuxHTTPConnectPort" ui:"group=vhost;label=TCPMux 端口;min=0;max=65535;hint=tcpmux 类型代理的 HTTP CONNECT 入口；0 关闭"`
	VhostHTTPTimeout      int    `json:"vhostHTTPTimeout" ui:"group=vhost;label=HTTP 响应超时（秒）;min=0;max=3600;hint=0 用默认 60 秒"`

	// 高级
	HeartbeatTimeout       int  `json:"heartbeatTimeout" ui:"group=advanced;label=心跳超时（秒）;min=0;max=3600;hint=客户端多久没心跳算断开；0 用默认（开多路复用时不检查）"`
	UserConnTimeout        int  `json:"userConnTimeout" ui:"group=advanced;label=工作连接等待（秒）;min=0;max=600;hint=用户连接等客户端建立工作连接的时间；0 用默认 10 秒"`
	MaxPoolCount           int  `json:"maxPoolCount" ui:"group=advanced;label=连接池上限;min=0;max=200;hint=每个代理预建的连接数上限；0 用默认 5"`
	UDPPacketSize          int  `json:"udpPacketSize" ui:"group=advanced;label=UDP 包大小;min=0;max=65535;hint=客户端和节点要一致；0 用默认 1500"`
	NatHoleReserveHours    int  `json:"natholeAnalysisDataReserveHours" ui:"group=advanced;label=打洞数据保留（小时）;min=0;max=8760;hint=xtcp 打洞的分析数据；0 用默认 7 天"`
	DetailedErrorsToClient bool `json:"detailedErrorsToClient" ui:"group=advanced;label=把具体原因告诉客户端;hint=关掉后用户只看到笼统的错误，看不到被拒绝的原因（例如超出限额）"`
}

// Defaults 是新节点的默认配置。
func Defaults() Settings {
	return Settings{BindPort: 7000, AllowPorts: "10000-60000", LogLevel: "info", TCPMux: true, DetailedErrorsToClient: true, VnetRelayPort: 7072}
}

// Group 是表单里的一组配置。
type Group struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// Groups 是分组，按显示顺序。
var Groups = []Group{
	{"basic", "基础", "客户端连接节点、映射端口的基本设置"},
	{"transport", "传输协议", "客户端可以选用的连接方式"},
	{"vhost", "HTTP / HTTPS 虚拟主机", "http、https、tcpmux 类型的代理需要"},
	{"advanced", "高级", "一般不用改"},
}

// Field 是表单里的一个配置项，由 Settings 的 ui 标签生成。
type Field struct {
	Key         string   `json:"key"`   // JSON 字段名
	Group       string   `json:"group"` // Groups 里的 Key
	Label       string   `json:"label"`
	Kind        string   `json:"kind"` // int / string / bool / select / ports
	Hint        string   `json:"hint,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
}

// Schema 返回所有配置项。
func Schema() []Field {
	t := reflect.TypeOf(Settings{})
	out := make([]Field, 0, t.NumField())
	for i := range t.NumField() {
		sf := t.Field(i)
		f := Field{Key: strings.Split(sf.Tag.Get("json"), ",")[0]}
		switch sf.Type.Kind() {
		case reflect.Int:
			f.Kind = "int"
		case reflect.Bool:
			f.Kind = "bool"
		default:
			f.Kind = "string"
		}
		for _, kv := range strings.Split(sf.Tag.Get("ui"), ";") {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "group":
				f.Group = v
			case "label":
				f.Label = v
			case "hint":
				f.Hint = v
			case "placeholder":
				f.Placeholder = v
			case "kind":
				f.Kind = v
			case "options":
				f.Options = strings.Split(v, ",")
			case "min", "max":
				n, err := strconv.Atoi(v)
				if err != nil {
					panic("frpsset: " + sf.Name + " 的 " + k + " 不是整数")
				}
				if k == "min" {
					f.Min = &n
				} else {
					f.Max = &n
				}
			}
		}
		if f.Group == "" || f.Label == "" {
			panic("frpsset: " + sf.Name + " 缺少 group 或 label")
		}
		out = append(out, f)
	}
	return out
}

// FieldError 是某个配置项不合法。
type FieldError struct {
	Key     string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

// Validate 检查配置，返回第一个问题（*FieldError，带字段名，后台据此标红对应的输入框）。
func (s Settings) Validate() error {
	v := reflect.ValueOf(s)
	for _, f := range Schema() {
		fv := v.FieldByIndex(fieldIndex(f.Key))
		if f.Kind == "int" {
			n := int(fv.Int())
			if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
				return &FieldError{f.Key, fmt.Sprintf("%s 要在 %d 到 %d 之间", f.Label, deref(f.Min), deref(f.Max))}
			}
		}
		if f.Kind == "select" && fv.String() != "" && !slices.Contains(f.Options, fv.String()) {
			return &FieldError{f.Key, fmt.Sprintf("%s 只能是 %s", f.Label, strings.Join(f.Options, " / "))}
		}
	}
	if s.BindAddr != "" && net.ParseIP(s.BindAddr) == nil {
		return &FieldError{"bindAddr", "监听地址要填 IP，例如 0.0.0.0 或 ::"}
	}
	if _, err := quota.ParsePorts(s.AllowPorts); err != nil {
		return &FieldError{"allowPorts", "允许映射的端口：" + err.Error()}
	}
	if s.SubDomainHost != "" && (strings.ContainsAny(s.SubDomainHost, " /:*") || !strings.Contains(s.SubDomainHost, ".")) {
		return &FieldError{"subDomainHost", "子域名后缀要填域名，例如 frp.example.com"}
	}
	// TCP 端口不能重复（KCP、QUIC 走 UDP，可以和客户端连接端口相同）
	tcp := map[int]string{s.BindPort: "客户端连接端口"}
	for _, p := range []struct {
		key, label string
		port       int
	}{
		{"vhostHTTPPort", "HTTP 端口", s.VhostHTTPPort},
		{"vhostHTTPSPort", "HTTPS 端口", s.VhostHTTPSPort},
		{"tcpmuxHTTPConnectPort", "TCPMux 端口", s.TCPMuxHTTPConnectPort},
	} {
		if p.port == 0 {
			continue
		}
		if other, ok := tcp[p.port]; ok {
			return &FieldError{p.key, fmt.Sprintf("%s 和%s都是 %d，要换一个", p.label, other, p.port)}
		}
		tcp[p.port] = p.label
	}
	if s.KCPBindPort != 0 && s.KCPBindPort == s.QUICBindPort {
		return &FieldError{"quicBindPort", "KCP 和 QUIC 都走 UDP，端口不能相同"}
	}
	if s.SubDomainHost != "" && s.VhostHTTPPort == 0 && s.VhostHTTPSPort == 0 && s.TCPMuxHTTPConnectPort == 0 {
		return &FieldError{"subDomainHost", "设置了子域名后缀，但 HTTP / HTTPS / TCPMux 端口都没开"}
	}
	return nil
}

// ErrNoSettings 表示节点还没有配置。
var ErrNoSettings = errors.New("节点还没有 frps 配置")

func fieldIndex(key string) []int {
	t := reflect.TypeOf(Settings{})
	for i := range t.NumField() {
		if strings.Split(t.Field(i).Tag.Get("json"), ",")[0] == key {
			return []int{i}
		}
	}
	panic("frpsset: 没有字段 " + key)
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
