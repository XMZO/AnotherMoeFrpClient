// Package access 是官方节点的准入控制：面板下发授权表，agent 在本机起一个 frps HTTP 插件，
// frps 在客户端登录、新建代理、新建工作连接、收到用户连接时来问它（docs/node-agent.md 第 12 节）。
//
//   - Login：metadatas 里要有面板签发的登录票据（shared/nodeticket）。票据签名正确、没过期、没用过（每张只能登录一次），
//     用户在表里、凭证代数一致、会话没被撤销，frp 的 user 必须是这个用户的昵称（frps 会把它加在代理名前面，
//     不同用户的代理名不会冲突）。同一个会话（设备）后登录的生效：插件给新登录分配 run ID 并记住，
//     之前那个 frpc 之后的新代理、新连接都会被拒绝（frps 没有踢掉已登录客户端的接口）；
//   - NewProxy：同上校验身份（不看过期和是否用过：登录后票据一直留在 metadatas 里），再看代理类型、远程端口、
//     同时在线的代理数是否在限额内；
//   - NewWorkConn / NewUserConn：同上校验身份。用户被撤销、会话退出登录或被踢、凭证被重置后，他的代理不再接受新连接。
//
// 在线代理数取自 frps 自己的统计（按 user 字段），不靠插件计数：frps 注册代理失败时不会通知插件，
// 自己计数会越算越多。被同一会话的新登录顶掉的 frpc 留下的代理不算。
//
// 表、用过的票据和见过的已撤销会话加密保存在本地存储（vault）里，面板离线、agent 重启后照样执行。
// 旧版本留下的明文 access.json / access-state.json 在存储解锁后搬进去并删掉。
//
// 设备绑定：票据里带 DeviceTag（面板按客户端登录时的设备指纹算的），frpc 登录时 metadatas 里要带上设备指纹，
// 算出来对得上才放行。旧面板签发的票据没有设备绑定，不检查。
package access

import (
	"crypto/rand"
	"encoding/base64"
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
	"time"

	"github.com/fatedier/frp/pkg/metrics/mem"
	plugin "github.com/fatedier/frp/pkg/plugin/server"

	"moefrp/agent/internal/agentcfg"
	"moefrp/agent/internal/vault"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/nodeticket"
	"moefrp/shared/quota"
)

// PluginOps 是挂到 frps 上的插件操作。
var PluginOps = []string{plugin.OpLogin, plugin.OpNewProxy, plugin.OpNewWorkConn, plugin.OpNewUserConn}

const (
	// clockSkew 容忍面板和节点的时钟误差。
	clockSkew = time.Minute
	// revokedKeep 是本地记住已撤销会话的时间。面板会定期删掉很久以前吊销的会话，节点自己多记一阵，
	// 防止用老会话登录、一直没断的 frpc 在面板删掉记录后又能新建代理。
	revokedKeep = 90 * 24 * time.Hour
)

// Store 是授权表和准入状态。
type Store struct {
	path      string // 旧的明文授权表（迁移用）
	statePath string // 旧的明文准入状态（迁移用）
	vault     *vault.Vault
	// Online 返回某个 frp user 当前在线的代理数，不算名字为 except 的那个（同名代理重新注册），
	// 也不算 live 返回 false 的 frpc（按 run ID）留下的。默认读 frps 的内存统计，测试时替换。
	Online func(user, except string, live func(runID string) bool) int
	// Now 返回当前时间，测试时替换。
	Now func() time.Time

	mu      sync.Mutex
	enabled bool
	nodeID  string
	key     []byte
	users   map[string]agentproto.AccessUser // 用户 ID → 用户
	revoked map[string]int64                 // 已撤销的会话 → 第一次见到的时间
	used    map[string]int64                 // 用过的票据随机数 → 票据过期时间
	active  map[string]string                // 会话 → 当前生效的 run ID（同一会话后登录的生效）
	runs    map[string]bool                  // 生效中的 run ID
}

// state 是需要跨重启保存的准入状态。
type state struct {
	Used    map[string]int64 `json:"used"`
	Revoked map[string]int64 `json:"revoked"`
}

var proxyTypes = []string{"tcp", "udp", "http", "https", "tcpmux", "stcp", "sudp", "xtcp"}

// onlineFromStats 按 frps 的统计数某个 user 的在线代理。
func onlineFromStats(user, except string, live func(string) bool) int {
	n := 0
	for _, t := range proxyTypes {
		for _, p := range mem.StatsCollector.GetProxiesByType(t) {
			if p.User == user && p.Name != except && p.LastStartAt > 0 && p.LastStartAt >= p.LastCloseAt && live(p.ClientID) {
				n++
			}
		}
	}
	return n
}

// 在本地存储里的文件名。
const (
	vaultTable = "access"
	vaultState = "access-state"
)

// Load 读取保存的表和状态；都没有时是"非官方节点"。v 为 nil 时用明文文件（测试和旧版本）。
func Load(path string, v *vault.Vault) (*Store, error) {
	s := &Store{
		path: path, statePath: strings.TrimSuffix(path, ".json") + "-state.json", vault: v,
		Online: onlineFromStats, Now: time.Now,
		users: map[string]agentproto.AccessUser{}, revoked: map[string]int64{}, used: map[string]int64{},
		active: map[string]string{}, runs: map[string]bool{},
	}
	return s, s.Reload()
}

// Reload 重新读取保存的表和状态（本地存储解锁后调用）。存储解锁时，旧的明文文件搬进去并删掉。
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb, err := s.readLocked(vaultTable, s.path)
	if err != nil {
		return err
	}
	if st, err := s.readLocked(vaultState, s.statePath); err == nil && st != nil {
		var x state
		if json.Unmarshal(st, &x) == nil {
			if x.Used != nil {
				s.used = x.Used
			}
			if x.Revoked != nil {
				s.revoked = x.Revoked
			}
		}
	}
	if tb == nil {
		return nil
	}
	var t agentproto.AccessTable
	if err := json.Unmarshal(tb, &t); err != nil {
		return fmt.Errorf("授权表损坏：%w", err)
	}
	s.set(t)
	return nil
}

// readLocked 读一份数据：优先本地存储；存储里没有时读旧的明文文件，存储已解锁的话顺便搬进去。
// 都没有时返回 nil。
func (s *Store) readLocked(name, legacy string) ([]byte, error) {
	if s.vault != nil {
		b, err := s.vault.Read(name)
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, vault.ErrLocked) {
			return nil, err
		}
	}
	b, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.vault != nil && s.vault.Unlocked() && s.vault.Write(name, b) == nil {
		_ = os.Remove(legacy)
	}
	return b, nil
}

// writeLocked 保存一份数据：有本地存储时加密写进去，否则写明文文件。
func (s *Store) writeLocked(name, legacy string, b []byte) error {
	if s.vault != nil {
		if err := s.vault.Write(name, b); err != nil {
			return err
		}
		_ = os.Remove(legacy)
		return nil
	}
	return agentcfg.WriteFileAtomic(legacy, b, 0o600)
}

func (s *Store) set(t agentproto.AccessTable) {
	s.enabled = t.Enabled
	s.nodeID = t.NodeID
	s.key, _ = base64.StdEncoding.DecodeString(t.TicketKey)
	s.users = make(map[string]agentproto.AccessUser, len(t.Users))
	for _, u := range t.Users {
		s.users[u.UserID] = u
	}
	now := s.Now().Unix()
	for _, id := range t.RevokedSessions {
		if _, ok := s.revoked[id]; !ok {
			s.revoked[id] = now
		}
	}
}

// Apply 换上新表并保存，返回"是否官方节点"有没有变化（变了要重启 frps 挂上或去掉插件）。
func (s *Store) Apply(t agentproto.AccessTable) (enabledChanged bool, err error) {
	b, err := json.Marshal(t)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeLocked(vaultTable, s.path, b); err != nil {
		return false, err
	}
	enabledChanged = s.enabled != t.Enabled
	s.set(t)
	return enabledChanged, s.saveLocked()
}

// saveLocked 清理过期的记录并保存状态。调用方持有锁。
func (s *Store) saveLocked() error {
	now := s.Now()
	for n, exp := range s.used {
		if time.Unix(exp, 0).Add(clockSkew).Before(now) {
			delete(s.used, n) // 票据过期后本来就不能登录，不用再记
		}
	}
	for id, seen := range s.revoked {
		if time.Unix(seen, 0).Add(revokedKeep).Before(now) {
			delete(s.revoked, id)
		}
	}
	b, err := json.Marshal(state{Used: s.used, Revoked: s.revoked})
	if err != nil {
		return err
	}
	return s.writeLocked(vaultState, s.statePath, b)
}

// Enabled 判断是不是官方节点。
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// Users 返回表里的用户数。
func (s *Store) Users() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

const (
	denied     = "这是 MoeFrp 官方节点：请在 MoeFrp 客户端登录后，从「官方节点」里选择它（没有有效的登录票据）"
	noAccess   = "你的账号不能使用这个官方节点（没有权限、已被停用，或管理员撤销了授权）"
	revoked    = "这台设备已经退出登录或被踢下线，请在客户端重新登录"
	resetCred  = "你的节点凭证已被管理员重置，请重新连接（客户端会自动领取新的登录票据）"
	superseded = "这台设备的账号在别处重新连接了这个节点，这个连接已停用"
)

// identify 校验票据代表的身份（不看过期和是否用过），返回声明和用户。调用方持有锁。
func (s *Store) identify(metas map[string]string) (nodeticket.Claims, agentproto.AccessUser, string) {
	ticket := metas[api.MetaCredential]
	if ticket == "" || len(s.key) == 0 {
		return nodeticket.Claims{}, agentproto.AccessUser{}, denied
	}
	c, err := nodeticket.Verify(s.key, ticket)
	if err != nil || c.Node != s.nodeID {
		return c, agentproto.AccessUser{}, denied
	}
	u, ok := s.users[c.User]
	switch {
	case !ok:
		return c, u, noAccess
	case u.Gen != c.Gen:
		return c, u, resetCred
	}
	if _, ok := s.revoked[c.Session]; ok {
		return c, u, revoked
	}
	return c, u, ""
}

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
	reject, content := s.decide(req.Op, req.Content)
	res := plugin.Response{Unchange: true}
	switch {
	case reject != "":
		res = plugin.Response{Reject: true, RejectReason: reject}
	case content != nil:
		res = plugin.Response{Unchange: false, Content: content}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// decide 返回拒绝原因（空串表示放行），需要改写请求时同时返回改写后的内容。
func (s *Store) decide(op string, raw json.RawMessage) (string, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return "", nil
	}
	switch op {
	case plugin.OpLogin:
		var c plugin.LoginContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析", nil
		}
		return s.login(&c)

	case plugin.OpNewProxy:
		var c plugin.NewProxyContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析", nil
		}
		u, reject := s.check(c.User)
		if reject != "" {
			return reject, nil
		}
		return s.admit(u, c), nil

	case plugin.OpNewWorkConn:
		var c plugin.NewWorkConnContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析", nil
		}
		_, reject := s.check(c.User)
		return reject, nil

	case plugin.OpNewUserConn:
		var c plugin.NewUserConnContent
		if json.Unmarshal(raw, &c) != nil {
			return "请求无法解析", nil
		}
		_, reject := s.check(c.User)
		return reject, nil
	}
	return "", nil
}

// login 处理登录：票据必须没过期、没用过。新登录（没有 run ID）由插件分配 run ID，
// 同一会话之前登录的 frpc 从此失效。
func (s *Store) login(c *plugin.LoginContent) (string, any) {
	claims, u, reject := s.identify(c.Metas)
	if reject != "" {
		return reject, nil
	}
	now := s.Now()
	if time.Unix(claims.Expires, 0).Add(clockSkew).Before(now) {
		return "登录票据已过期，请重新连接（客户端会自动领取新的登录票据）", nil
	}
	if _, ok := s.used[claims.Nonce]; ok {
		return "登录票据已经用过：每张票据只能登录一次", nil
	}
	if c.User != u.Nickname {
		return fmt.Sprintf("frp 的 user 必须是你的昵称 %s", u.Nickname), nil
	}
	if claims.Device != "" && nodeticket.DeviceTag(s.key, c.Metas[api.MetaDevice]) != claims.Device {
		return "登录票据和这台设备对不上（票据是发给另一台设备的）", nil
	}
	s.used[claims.Nonce] = claims.Expires
	if err := s.saveLocked(); err != nil {
		return "节点无法记录登录票据，请稍后再试", nil
	}
	var content any
	if c.RunID == "" {
		c.RunID = newRunID()
		content = c
	}
	if old := s.active[claims.Session]; old != "" && old != c.RunID {
		delete(s.runs, old)
	}
	s.active[claims.Session] = c.RunID
	s.runs[c.RunID] = true
	return "", content
}

// check 校验已登录 frpc 的身份，并且必须是这个会话当前生效的那个 frpc。
func (s *Store) check(ui plugin.UserInfo) (agentproto.AccessUser, string) {
	claims, u, reject := s.identify(ui.Metas)
	if reject != "" {
		return u, reject
	}
	if s.active[claims.Session] != ui.RunID {
		return u, superseded
	}
	return u, ""
}

func newRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// admit 检查新代理是否在限额内。调用方持有锁。
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
	live := func(runID string) bool { return s.runs[runID] }
	if q.MaxProxies > 0 && s.Online(c.User.User, c.ProxyName, live) >= q.MaxProxies {
		return fmt.Sprintf("你的账号在这个节点上最多同时运行 %d 个代理", q.MaxProxies)
	}
	return ""
}
