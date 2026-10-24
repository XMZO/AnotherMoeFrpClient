package nodes

// 官方节点（docs/node-agent.md 第 12 节）：
//   - 节点可以"下发给用户"，按角色开放；个别用户可以单独允许或禁止（node_grants）；
//   - 限额按角色设（settings.role_quotas），个别用户可以覆盖（users.node_quota）；
//   - 客户端每次登录官方节点前向面板领一张一次性的短期票据（shared/nodeticket），票据绑定用户、客户端会话、
//     节点和用户的凭证代数；"重置节点凭证"就是代数 +1，旧票据全部失效；
//   - 每个官方节点的授权表（节点票据密钥 + 授权用户的昵称、代数、有效限额 + 已撤销的会话）推给 agent，
//     由它的 frps 插件执行。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"moefrp/server/internal/store"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/nodeticket"
	"moefrp/shared/quota"
)

const (
	settingCredKey    = "node_cred_key"
	settingRoleQuotas = "role_quotas"
	maxDescriptionLen = 200
	accessDebounce    = 300 * time.Millisecond

	// TicketTTL 是登录票据的有效期。客户端预取几张备用，面板短暂不可用时还能重连。
	TicketTTL = 15 * time.Minute
)

var allRoles = []string{model.RoleUser, model.RoleAdmin}

func hasFeature(h *agentproto.Hello, f string) bool {
	return h != nil && slices.Contains(h.Features, f)
}

// parseRoles 解析节点开放给哪些角色，去掉已经不存在的角色（例如 nodes.roles 列默认值里的 vip）。
func parseRoles(s string) []string {
	var r []string
	if json.Unmarshal([]byte(s), &r) != nil {
		return nil
	}
	return slices.DeleteFunc(r, func(role string) bool { return !slices.Contains(allRoles, role) })
}

// ---- 密钥 ----

func (s *Service) credKey(ctx context.Context) ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	v, err := store.InsertSettingIfAbsent(ctx, s.st.DB(), settingCredKey, base64.StdEncoding.EncodeToString(b))
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(key) < 32 {
		return nil, errors.New("nodes: bad node_cred_key setting")
	}
	s.key = key
	return key, nil
}

// credKey 是面板主密钥（setting node_cred_key），各节点的票据密钥由它派生（nodeticket.NodeKey）。

// ---- 限额 ----

// RoleQuotas 返回三个角色的限额，没设置过时用默认值。
func (s *Service) RoleQuotas(ctx context.Context) (quota.Roles, error) {
	v, ok, err := store.GetSetting(ctx, s.st.DB(), settingRoleQuotas)
	if err != nil || !ok {
		return quota.DefaultRoles(), err
	}
	var r quota.Roles
	if err := json.Unmarshal([]byte(v), &r); err != nil {
		return quota.DefaultRoles(), nil
	}
	return r, nil
}

// SetRoleQuotas 修改角色限额，马上推给所有官方节点。
func (s *Service) SetRoleQuotas(ctx context.Context, by Actor, r quota.Roles) error {
	if err := r.Validate(); err != nil {
		return badRequest("quotas", err.Error())
	}
	b, _ := json.Marshal(r)
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.SetSetting(ctx, tx, settingRoleQuotas, string(b)); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditQuotaRoles, "", map[string]any{"quotas": r})
	})
	if err == nil {
		s.AccessChanged()
	}
	return err
}

func parseOverride(s string) *quota.Override {
	if s == "" {
		return nil
	}
	var o quota.Override
	if json.Unmarshal([]byte(s), &o) != nil {
		return nil
	}
	return &o
}

// ---- 授权计算 ----

type accessData struct {
	key     []byte
	roles   quota.Roles
	users   []store.AccessUser
	grants  map[[2]string]bool // {用户 ID, 节点 ID} → 允许
	revoked []string           // 已吊销的客户端会话
}

func (s *Service) loadAccess(ctx context.Context) (accessData, error) {
	var d accessData
	var err error
	if d.key, err = s.credKey(ctx); err != nil {
		return d, err
	}
	if d.roles, err = s.RoleQuotas(ctx); err != nil {
		return d, err
	}
	if d.users, err = store.ListAccessUsers(ctx, s.st.DB()); err != nil {
		return d, err
	}
	if d.revoked, err = store.ListRevokedClientSessions(ctx, s.st.DB()); err != nil {
		return d, err
	}
	gs, err := store.ListGrants(ctx, s.st.DB())
	if err != nil {
		return d, err
	}
	d.grants = map[[2]string]bool{}
	for _, g := range gs {
		d.grants[[2]string{g.UserID, g.NodeID}] = g.Allow
	}
	return d, nil
}

// allowed 判断用户能不能用这个节点：单独授权优先；没有单独授权时，管理员总能用，其他角色看节点开放给谁。
func (d accessData) allowed(u store.AccessUser, n store.Node) bool {
	if u.Status != model.StatusActive {
		return false
	}
	if g, ok := d.grants[[2]string{u.ID, n.ID}]; ok {
		return g
	}
	return u.Role == model.RoleAdmin || slices.Contains(parseRoles(n.Roles), u.Role)
}

func (d accessData) quotaOf(u store.AccessUser) quota.Quota {
	return d.roles.For(u.Role).Apply(parseOverride(u.QuotaJSON))
}

func (d accessData) table(n store.Node) agentproto.AccessTable {
	// 节点都由面板托管：frps 始终挂准入插件，只认登录票据
	t := agentproto.AccessTable{Enabled: true, Users: []agentproto.AccessUser{}}
	t.NodeID = n.ID
	t.TicketKey = base64.StdEncoding.EncodeToString(nodeticket.NodeKey(d.key, n.ID))
	t.RevokedSessions = d.revoked
	for _, u := range d.users {
		if d.allowed(u, n) {
			t.Users = append(t.Users, agentproto.AccessUser{UserID: u.ID, Nickname: u.Nickname, Gen: u.CredGen, Quota: d.quotaOf(u)})
		}
	}
	return t
}

// ---- 推送 ----

// AccessChanged 在用户、角色、限额、授权变化，或者客户端会话被吊销后调用：稍等片刻后把所有在线官方节点的表重新算一遍，
// 有变化的才推送（一次批量操作只推一次）。
func (s *Service) AccessChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accessTimer != nil {
		s.accessTimer.Reset(accessDebounce)
		return
	}
	s.accessTimer = time.AfterFunc(accessDebounce, func() {
		s.mu.Lock()
		s.accessTimer = nil
		s.mu.Unlock()
		s.SyncAllAccess(context.Background(), false)
	})
}

// SyncAllAccess 给所有在线节点推送授权表；force 为 false 时跳过没有变化的。
func (s *Service) SyncAllAccess(ctx context.Context, force bool) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.conns))
	for id := range s.conns {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.syncAccess(ctx, id, force); err != nil {
			s.log.Warn("sync node access failed", "node", id, "err", err)
		}
		// 顺便给在线节点续存储租约（隔 leaseRenew 续一次）
		if err := s.pushLease(ctx, id, false); err != nil {
			s.log.Warn("renew store lease failed", "node", id, "err", err)
		}
	}
}

// syncAccess 给一个在线节点推送授权表。旧版本 agent（不支持 access）跳过；支持 access 但不支持票据的
// （1.1.x / 1.2.x）照样推送，它们看不懂新表，官方节点上所有人都会被拒绝，管理后台会提示升级。
func (s *Service) syncAccess(ctx context.Context, id string, force bool) error {
	s.mu.Lock()
	c := s.conns[id]
	s.mu.Unlock()
	if c == nil || !hasFeature(&c.hello, agentproto.FeatureAccess) {
		return nil
	}
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return err
	}
	d, err := s.loadAccess(ctx)
	if err != nil {
		return err
	}
	t := d.table(n)
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	s.mu.Lock()
	same := s.pushed[id] == h
	s.mu.Unlock()
	if same && !force {
		return nil
	}
	var res agentproto.AccessResult
	if err := s.Call(ctx, id, agentproto.MethodAccessSync, t, &res, callTimeout); err != nil {
		return err
	}
	s.mu.Lock()
	s.pushed[id] = h
	s.mu.Unlock()
	if res.Restarted {
		s.log.Info("node switched official mode", "node", id, "official", t.Enabled)
	}
	return nil
}

// ---- 管理 ----

// SetOfficial 修改节点开放给哪些角色（管理员总能用）和给用户看的说明，立即推送新的授权表。
func (s *Service) SetOfficial(ctx context.Context, by Actor, id string, roles []string, description string) error {
	for _, r := range roles {
		if !slices.Contains(allRoles, r) || r == model.RoleAdmin {
			return badRequest("roles", fmt.Sprintf("不认识的角色 %q", r))
		}
	}
	if roles == nil {
		roles = []string{}
	}
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > maxDescriptionLen {
		return badRequest("description", fmt.Sprintf("说明最多 %d 个字符", maxDescriptionLen))
	}
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return notFound(err)
	}
	rb, _ := json.Marshal(roles)
	official := openToUsers(roles)
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.SetNodeOfficial(ctx, tx, id, store.NodeOfficial{Official: official, Roles: string(rb), Description: description}, s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeOfficial, n.Name, map[string]any{"node": id, "roles": roles})
	})
	if err != nil {
		return notFound(err)
	}
	if err := s.syncAccess(ctx, id, false); err != nil {
		s.log.Warn("sync node access failed", "node", id, "err", err)
	}
	return nil
}

// UserNodeGrant 是用户在一个节点上的授权情况（管理后台显示用）。
type UserNodeGrant struct {
	NodeID   string   `json:"nodeId"`
	Name     string   `json:"name"`
	Official bool     `json:"official"`
	Roles    []string `json:"roles"`
	Grant    *bool    `json:"grant"`   // nil 表示按角色
	Allowed  bool     `json:"allowed"` // 最终能不能用
}

// UserNodes 是一个用户的节点权限（管理后台）。
type UserNodes struct {
	Role      string          `json:"role"`
	RoleQuota quota.Quota     `json:"roleQuota"`
	Override  *quota.Override `json:"override"`
	Effective quota.Quota     `json:"effective"`
	Nodes     []UserNodeGrant `json:"nodes"`
}

func (s *Service) userByNickname(ctx context.Context, nickname string) (store.AccessUser, error) {
	u, err := store.GetUserByNickname(ctx, s.st.DB(), nickname)
	if errors.Is(err, store.ErrNotFound) {
		return store.AccessUser{}, apierr.Newf(apierr.NotFound, "用户不存在")
	}
	if err != nil {
		return store.AccessUser{}, err
	}
	return store.GetAccessUser(ctx, s.st.DB(), u.ID)
}

// GetUserNodes 返回用户的节点权限。
func (s *Service) GetUserNodes(ctx context.Context, nickname string) (UserNodes, error) {
	u, err := s.userByNickname(ctx, nickname)
	if err != nil {
		return UserNodes{}, err
	}
	d, err := s.loadAccess(ctx)
	if err != nil {
		return UserNodes{}, err
	}
	ns, err := store.ListNodes(ctx, s.st.DB())
	if err != nil {
		return UserNodes{}, err
	}
	out := UserNodes{Role: u.Role, RoleQuota: d.roles.For(u.Role), Override: parseOverride(u.QuotaJSON), Effective: d.quotaOf(u), Nodes: []UserNodeGrant{}}
	for _, n := range ns {
		g := UserNodeGrant{NodeID: n.ID, Name: n.Name, Official: n.Official, Roles: parseRoles(n.Roles), Allowed: d.allowed(u, n)}
		if v, ok := d.grants[[2]string{u.ID, n.ID}]; ok {
			g.Grant = &v
		}
		out.Nodes = append(out.Nodes, g)
	}
	return out, nil
}

// SetUserNodes 修改用户的限额覆盖和单独授权。grants 里没有的节点不改；值为 nil 表示回到按角色。
func (s *Service) SetUserNodes(ctx context.Context, by Actor, nickname string, override *quota.Override, grants map[string]*bool) error {
	u, err := s.userByNickname(ctx, nickname)
	if err != nil {
		return err
	}
	if err := override.Validate(); err != nil {
		return badRequest("override", err.Error())
	}
	qj := ""
	if !override.Empty() {
		b, _ := json.Marshal(override)
		qj = string(b)
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.SetUserNodeQuota(ctx, tx, u.ID, qj); err != nil {
			return err
		}
		for nodeID, allow := range grants {
			if _, err := store.GetNode(ctx, tx, nodeID); err != nil {
				return notFound(err)
			}
			if err := store.SetGrant(ctx, tx, u.ID, nodeID, allow); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, by, store.AuditUserNodes, u.Nickname, map[string]any{"override": override, "grants": grants})
	})
	if err == nil {
		s.AccessChanged()
	}
	return err
}

// ResetUserCredential 让用户的节点凭证换代：已签发的票据和用它们登录的连接在所有节点上立即失效，
// 客户端会自动领新票据重连。
func (s *Service) ResetUserCredential(ctx context.Context, by Actor, nickname string) error {
	u, err := s.userByNickname(ctx, nickname)
	if err != nil {
		return err
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.BumpUserNodeCred(ctx, tx, u.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditUserNodeCred, u.Nickname, nil)
	})
	if err == nil {
		s.AccessChanged()
	}
	return err
}

// ---- 下发给客户端 ----

// managedNode 判断节点上的 agent 支持托管（1.4.0 起）。还没上过线（不知道版本）的算支持，连上后再说。
func (s *Service) managedNode(n store.Node) bool {
	return managed(s.view(n, false).Info)
}

// OfficialFor 返回用户能用的节点（不含登录票据，见 IssueTickets；也没有 token，节点只认票据）。
func (s *Service) OfficialFor(ctx context.Context, userID string) ([]api.OfficialNode, error) {
	u, err := store.GetAccessUser(ctx, s.st.DB(), userID)
	if err != nil {
		return nil, err
	}
	d, err := s.loadAccess(ctx)
	if err != nil {
		return nil, err
	}
	ns, err := store.ListNodes(ctx, s.st.DB())
	if err != nil {
		return nil, err
	}
	out := []api.OfficialNode{}
	for _, n := range ns {
		if !n.Enrolled || !d.allowed(u, n) || !s.managedNode(n) {
			continue
		}
		ci := clientInfo(n)
		if ci.Addr == "" {
			continue // 不知道地址，下发了也连不上
		}
		out = append(out, api.OfficialNode{
			ID: n.ID, Name: n.Name, Description: n.Description, ServerAddr: ci.Addr, ServerPort: ci.Port,
			User: u.Nickname, Online: s.Online(n.ID), Quota: d.quotaOf(u),
		})
	}
	return out, nil
}

// IssueTickets 给用户的一个客户端会话签发 count 张某个节点的登录票据。
// deviceID 是这个会话登录时的设备指纹（调用方核对过请求带的和会话记下的一致），写进票据的设备绑定。
func (s *Service) IssueTickets(ctx context.Context, userID, sessionID, deviceID, nodeID string, count int) ([]api.NodeTicket, error) {
	if count < 1 || count > api.MaxTicketsPerRequest {
		return nil, badRequest("count", fmt.Sprintf("一次可以领 1 到 %d 张", api.MaxTicketsPerRequest))
	}
	if deviceID == "" {
		return nil, apierr.Newf(apierr.Forbidden, "客户端版本太旧，没有上报设备指纹，请升级客户端后重新登录")
	}
	u, err := store.GetAccessUser(ctx, s.st.DB(), userID)
	if err != nil {
		return nil, err
	}
	n, err := store.GetNode(ctx, s.st.DB(), nodeID)
	if err != nil {
		return nil, apierr.Newf(apierr.NotFound, "节点不存在")
	}
	d, err := s.loadAccess(ctx)
	if err != nil {
		return nil, err
	}
	if !d.allowed(u, n) {
		return nil, apierr.Newf(apierr.NotFound, "节点不存在，或者你没有权限使用")
	}
	key := nodeticket.NodeKey(d.key, n.ID)
	now := s.now()
	exp := now.Add(TicketTTL).Unix()
	out := make([]api.NodeTicket, 0, count)
	for range count {
		out = append(out, api.NodeTicket{ExpiresAt: exp, Ticket: nodeticket.Sign(key, nodeticket.Claims{
			User: u.ID, Session: sessionID, Node: n.ID, Gen: u.CredGen, Issued: now.Unix(), Expires: exp, Nonce: nodeticket.NewNonce(),
			Device: nodeticket.DeviceTag(key, deviceID),
		})})
	}
	return out, nil
}
