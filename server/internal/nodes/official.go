package nodes

// 官方节点（docs/node-agent.md 第 12 节）：
//   - 节点可以"下发给用户"，按角色开放；个别用户可以单独允许或禁止（node_grants）；
//   - 限额按角色设（settings.role_quotas），个别用户可以覆盖（users.node_quota）；
//   - 每个用户有一个节点凭证 = HMAC(面板密钥, 用户 ID + 代数)，重置凭证就是代数 +1；
//   - 每个官方节点的授权用户表（凭证哈希 + 昵称 + 有效限额）推给 agent，由它的 frps 插件执行。

import (
	"context"
	"crypto/hmac"
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
	"moefrp/shared/quota"
)

const (
	settingCredKey    = "node_cred_key"
	settingRoleQuotas = "role_quotas"
	maxDescriptionLen = 200
	accessDebounce    = 300 * time.Millisecond
)

var allRoles = []string{model.RoleUser, model.RoleVIP, model.RoleAdmin}

func hasFeature(h *agentproto.Hello, f string) bool {
	return h != nil && slices.Contains(h.Features, f)
}

func parseRoles(s string) []string {
	var r []string
	if json.Unmarshal([]byte(s), &r) != nil {
		return nil
	}
	return r
}

// ---- 凭证 ----

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

// credential 是用户的节点凭证。不存库：由面板密钥、用户 ID 和代数算出来。
func credential(key []byte, userID string, gen int64) string {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "moefrp-node-cred:%s:%d", userID, gen)
	return "mfc_" + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func credHash(cred string) string {
	sum := sha256.Sum256([]byte(cred))
	return hex.EncodeToString(sum[:])
}

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
	key    []byte
	roles  quota.Roles
	users  []store.AccessUser
	grants map[[2]string]bool // {用户 ID, 节点 ID} → 允许
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

func (d accessData) allowed(u store.AccessUser, n store.Node) bool {
	if u.Status != model.StatusActive || !n.Official {
		return false
	}
	if g, ok := d.grants[[2]string{u.ID, n.ID}]; ok {
		return g
	}
	return slices.Contains(parseRoles(n.Roles), u.Role)
}

func (d accessData) quotaOf(u store.AccessUser) quota.Quota {
	return d.roles.For(u.Role).Apply(parseOverride(u.QuotaJSON))
}

func (d accessData) table(n store.Node) agentproto.AccessTable {
	t := agentproto.AccessTable{Enabled: n.Official, Users: []agentproto.AccessUser{}}
	if !n.Official {
		return t
	}
	for _, u := range d.users {
		if d.allowed(u, n) {
			t.Users = append(t.Users, agentproto.AccessUser{
				CredHash: credHash(credential(d.key, u.ID, u.CredGen)), UserID: u.ID, Nickname: u.Nickname, Quota: d.quotaOf(u),
			})
		}
	}
	return t
}

// ---- 推送 ----

// AccessChanged 在用户、角色、限额、授权变化后调用：稍等片刻后把所有在线官方节点的表重新算一遍，
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
	}
}

// syncAccess 给一个在线节点推送授权表。旧版本 agent（不支持 access）跳过。
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

// SetOfficial 修改节点的官方节点设置。节点在线时立即推送（开关变化时节点上的 frps 会重启）。
func (s *Service) SetOfficial(ctx context.Context, by Actor, id string, official bool, roles []string, description string) error {
	for _, r := range roles {
		if !slices.Contains(allRoles, r) {
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
	if official {
		v := s.view(n, false)
		if v.Info != nil && v.Info.AgentVersion != "" && !hasFeature(v.Info, agentproto.FeatureAccess) {
			return apierr.Newf(apierr.ValidationFailed, "节点上的 agent（%s）不支持官方节点，请先升级 agent", v.Info.AgentVersion)
		}
		if clientInfo(n.FrpsConfig) == nil {
			return apierr.Newf(apierr.ValidationFailed, "节点还没有可用的 frps 配置")
		}
	}
	rb, _ := json.Marshal(roles)
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.SetNodeOfficial(ctx, tx, id, store.NodeOfficial{Official: official, Roles: string(rb), Description: description}, s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeOfficial, n.Name, map[string]any{"node": id, "official": official, "roles": roles})
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

// ResetUserCredential 让用户的节点凭证换代：旧凭证在所有节点上立即失效，客户端下次同步时拿到新的。
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

// OfficialFor 返回用户能用的官方节点，带上他的节点凭证。
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
		if !d.allowed(u, n) {
			continue
		}
		ci := clientInfo(n.FrpsConfig)
		addr := n.PublicAddr
		if addr == "" {
			addr = n.LastIP
		}
		if ci == nil || addr == "" {
			continue // 还没有配置或者不知道地址，下发了也连不上
		}
		out = append(out, api.OfficialNode{
			ID: n.ID, Name: n.Name, Description: n.Description, ServerAddr: addr, ServerPort: ci.Port, Token: ci.Token,
			User: u.Nickname, Credential: credential(d.key, u.ID, u.CredGen), Online: s.Online(n.ID), Quota: d.quotaOf(u),
		})
	}
	return out, nil
}
