// Package access 是官方节点的准入控制：面板下发授权用户表，agent 在本机起一个 frps HTTP 插件，
// frps 在客户端登录、新建代理、收到用户连接时来问它（docs/node-agent.md 第 12 节）。
//
//   - Login：metadatas 里的节点凭证必须在表里，frp 的 user 必须是这个用户的昵称
//     （frps 会把它加在代理名前面，不同用户的代理名不会冲突）；
//   - NewProxy：代理类型、远程端口、同时在线的代理数要在限额内；
//   - NewUserConn：用户被撤销后，他的代理不再接受新连接（frps 没有踢掉已登录客户端的接口）。
//
// 在线代理数取自 frps 自己的统计（按 user 字段），不靠插件计数：frps 注册代理失败时不会通知插件，
// 自己计数会越算越多。
//
// 表保存在 /etc/moefrp-agent/access.json，面板离线时节点照样按最后的表执行。
package access

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/fatedier/frp/pkg/metrics/mem"
	plugin "github.com/fatedier/frp/pkg/plugin/server"

	"moefrp/agent/internal/agentcfg"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/quota"
)

// PluginOps 是挂到 frps 上的插件操作。
var PluginOps = []string{plugin.OpLogin, plugin.OpNewProxy, plugin.OpNewUserConn}

// Store 是授权用户表。
type Store struct {
	path string
	// Online 返回某个 frp user 当前在线的代理数，不算名字为 except 的那个（同名代理重新注册）。
	// 默认读 frps 的内存统计，测试时替换。
	Online func(user, except string) int

	mu      sync.RWMutex
	enabled bool
	byCred  map[string]agentproto.AccessUser // 凭证哈希 → 用户
}

var proxyTypes = []string{"tcp", "udp", "http", "https", "tcpmux", "stcp", "sudp", "xtcp"}

// onlineFromStats 按 frps 的统计数某个 user 的在线代理。
func onlineFromStats(user, except string) int {
	n := 0
	for _, t := range proxyTypes {
		for _, p := range mem.StatsCollector.GetProxiesByType(t) {
			if p.User == user && p.Name != except && p.LastStartAt > 0 && p.LastStartAt >= p.LastCloseAt {
				n++
			}
		}
	}
	return n
}

// Load 读取保存的表；文件不存在时是"非官方节点"。
func Load(path string) (*Store, error) {
	s := &Store{path: path, Online: onlineFromStats, byCred: map[string]agentproto.AccessUser{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var t agentproto.AccessTable
	if err := json.Unmarshal(b, &t); err != nil {
		return s, fmt.Errorf("%s：%w", path, err)
	}
	s.set(t)
	return s, nil
}

func (s *Store) set(t agentproto.AccessTable) {
	s.enabled = t.Enabled
	s.byCred = make(map[string]agentproto.AccessUser, len(t.Users))
	for _, u := range t.Users {
		s.byCred[strings.ToLower(u.CredHash)] = u
	}
}

// Apply 换上新表并保存，返回"是否官方节点"有没有变化（变了要重启 frps 挂上或去掉插件）。
func (s *Store) Apply(t agentproto.AccessTable) (enabledChanged bool, err error) {
	b, err := json.Marshal(t)
	if err != nil {
		return false, err
	}
	if err := agentcfg.WriteFileAtomic(s.path, b, 0o600); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	enabledChanged = s.enabled != t.Enabled
	s.set(t)
	return enabledChanged, nil
}

// Enabled 判断是不是官方节点。
func (s *Store) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// Users 返回表里的用户数。
func (s *Store) Users() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byCred)
}

// HashCredential 是节点凭证在表里的形式。
func HashCredential(cred string) string {
	sum := sha256.Sum256([]byte(cred))
	return hex.EncodeToString(sum[:])
}

func (s *Store) lookup(metas map[string]string) (agentproto.AccessUser, bool) {
	cred := metas[api.MetaCredential]
	if cred == "" {
		return agentproto.AccessUser{}, false
	}
	u, ok := s.byCred[HashCredential(cred)]
	return u, ok
}

const denied = "这是 MoeFrp 官方节点：请在 MoeFrp 客户端登录后，从「官方节点」里选择它（凭证无效或已被撤销）"

// Listen 在本机回环地址上起插件服务，返回给 frps 用的插件配置地址（127.0.0.1:端口）。
func (s *Store) Listen() (addr string, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	go func() { _ = http.Serve(ln, s) }()
	return ln.Addr().String(), nil
}

// ServeHTTP 实现 frps 的 HTTP 插件协议。
func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Op      string          `json:"op"`
		Content json.RawMessage `json:"content"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reject := s.decide(req.Op, req.Content)
	res := plugin.Response{Unchange: true}
	if reject != "" {
		res = plugin.Response{Reject: true, RejectReason: reject}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// decide 返回拒绝原因，空串表示放行。
func (s *Store) decide(op string, raw json.RawMessage) string {
	switch op {
	case plugin.OpLogin:
		var c plugin.LoginContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析"
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		if !s.enabled {
			return ""
		}
		u, ok := s.lookup(c.Metas)
		if !ok {
			return denied
		}
		if c.User != u.Nickname {
			return fmt.Sprintf("frp 的 user 必须是你的昵称 %s", u.Nickname)
		}
		return ""

	case plugin.OpNewProxy:
		var c plugin.NewProxyContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析"
		}
		s.mu.RLock()
		enabled := s.enabled
		u, ok := s.lookup(c.User.Metas)
		s.mu.RUnlock()
		if !enabled {
			return ""
		}
		if !ok {
			return denied
		}
		return s.admit(u, c)

	case plugin.OpNewUserConn:
		var c plugin.NewUserConnContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析"
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		if !s.enabled {
			return ""
		}
		if _, ok := s.lookup(c.User.Metas); !ok {
			return denied
		}
		return ""
	}
	return ""
}

// admit 检查新代理是否在限额内。
func (s *Store) admit(u agentproto.AccessUser, c plugin.NewProxyContent) string {
	q := u.Quota
	if !q.AllowsType(c.ProxyType) {
		return fmt.Sprintf("你的账号在这个节点上不能使用 %s 代理（允许：%s）", c.ProxyType, strings.Join(q.Types, "、"))
	}
	if c.ProxyType == "tcp" || c.ProxyType == "udp" {
		ranges, err := quota.ParsePorts(q.Ports)
		if err == nil && len(ranges) > 0 {
			if c.RemotePort == 0 {
				return fmt.Sprintf("请为代理指定远程端口，你可以用的范围是 %s", ranges)
			}
			if !ranges.Contains(c.RemotePort) {
				return fmt.Sprintf("远程端口 %d 不在你可以用的范围内（%s）", c.RemotePort, ranges)
			}
		}
	}
	if q.MaxProxies > 0 && s.Online(c.User.User, c.ProxyName) >= q.MaxProxies {
		return fmt.Sprintf("你的账号在这个节点上最多同时运行 %d 个代理", q.MaxProxies)
	}
	return ""
}
