// Package share 实现分享与订阅（PLAN 7.6）。
//
// 分享保存创建时的快照；订阅者通过 runtime 拿到可运行的数据，preview 和 state 摘要里
// 绝不出现节点 token 和 secretKey。分享或订阅变化时，相关用户的 stateVersion 加 1，
// 他们下一次同步就能看到。
package share

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// Service 是分享服务。
type Service struct {
	st  *store.Store
	now func() time.Time
}

// New 创建分享服务。now 为 nil 时用 time.Now。
func New(st *store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{st: st, now: now}
}

// ---------- 分享 ----------

// Create 从用户自己的配置创建分享。
func (s *Service) Create(ctx context.Context, userID string, req api.CreateShareRequest) (api.Share, error) {
	var c fieldErrs
	c.merge(validate.ShareName(req.Name))
	c.merge(validate.ShareKind(req.Kind))
	c.merge(validate.MaxSubscribers(req.MaxSubscribers))
	now := s.now()
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		c.add("expiresAt", validate.ReasonRange, "过期时间必须晚于现在")
	}
	if req.ProfileID == "" {
		c.add("profileId", validate.ReasonRequired, "请选择要分享的配置")
	}
	if err := c.err(); err != nil {
		return api.Share{}, err
	}

	var out api.Share
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		prof, err := loadProfile(ctx, tx, userID, req.ProfileID)
		if err != nil {
			return err
		}
		payload, err := buildPayload(req.Kind, prof, req.NodeIDs, req.ProxyIDs)
		if err != nil {
			return err
		}
		n, err := store.CountActiveShares(ctx, tx, userID)
		if err != nil {
			return err
		}
		if n >= validate.MaxSharesPerUser {
			return apierr.New(apierr.LimitExceeded).With(apierr.DetailLimit, validate.MaxSharesPerUser)
		}
		blob, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		ts := now.Unix()
		row := store.ShareRow{
			OwnerID: userID, Name: strings.TrimSpace(req.Name), Kind: req.Kind, Payload: string(blob),
			SourceProfileID: req.ProfileID, Revision: 1, MaxSubscribers: req.MaxSubscribers,
			CreatedAt: ts, UpdatedAt: ts,
		}
		if req.ExpiresAt != nil {
			e := req.ExpiresAt.Unix()
			row.ExpiresAt = &e
		}
		for attempt := 0; ; attempt++ {
			row.ID = NewShareID()
			err = store.InsertShare(ctx, tx, row)
			if err == nil || !store.IsUniqueViolation(err) || attempt >= 3 {
				break
			}
		}
		if err != nil {
			return err
		}
		if _, err := store.BumpStateVersion(ctx, tx, userID); err != nil {
			return err
		}
		if err := audit(ctx, tx, ts, userID, store.AuditShareCreate, row.ID, map[string]any{"kind": row.Kind}); err != nil {
			return err
		}
		out, err = toShare(row, 0, ts)
		return err
	})
	return out, err
}

// List 列出用户自己的分享（含已撤销的）。
func (s *Service) List(ctx context.Context, userID string) ([]api.Share, error) {
	out := []api.Share{}
	err := s.st.ReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := store.ListShares(ctx, tx, userID)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		for _, r := range rows {
			n, err := store.CountShareSubscribers(ctx, tx, r.ID)
			if err != nil {
				return err
			}
			sh, err := toShare(r, n, now)
			if err != nil {
				return err
			}
			out = append(out, sh)
		}
		return nil
	})
	return out, err
}

// Update 修改名称、过期时间、人数上限。已撤销的分享不能再改。
func (s *Service) Update(ctx context.Context, userID, id string, req api.UpdateShareRequest) (api.Share, error) {
	var out api.Share
	now := s.now()
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		row, err := ownedShare(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Revoked() {
			return apierr.New(apierr.ShareRevoked)
		}
		var c fieldErrs
		if req.Name != nil {
			c.merge(validate.ShareName(*req.Name))
			row.Name = strings.TrimSpace(*req.Name)
		}
		switch {
		case req.ClearExpiry:
			row.ExpiresAt = nil
		case req.ExpiresAt != nil:
			if !req.ExpiresAt.After(now) {
				c.add("expiresAt", validate.ReasonRange, "过期时间必须晚于现在")
			}
			e := req.ExpiresAt.Unix()
			row.ExpiresAt = &e
		}
		if req.MaxSubscribers != nil {
			c.merge(validate.MaxSubscribers(*req.MaxSubscribers))
			row.MaxSubscribers = *req.MaxSubscribers
		}
		if err := c.err(); err != nil {
			return err
		}
		ts := now.Unix()
		if err := store.UpdateShareMeta(ctx, tx, row, ts); err != nil {
			return err
		}
		row.UpdatedAt = ts
		if err := bumpShareUsers(ctx, tx, row); err != nil {
			return err
		}
		n, err := store.CountShareSubscribers(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		out, err = toShare(row, n, ts)
		return err
	})
	return out, err
}

// Republish 用来源配置的当前内容重新生成快照，沿用快照里已有的节点、代理 ID。
func (s *Service) Republish(ctx context.Context, userID, id string) (api.Share, error) {
	var out api.Share
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		row, err := ownedShare(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Revoked() {
			return apierr.New(apierr.ShareRevoked)
		}
		if row.SourceProfileID == "" {
			return fieldError("sourceProfileId", validate.ReasonReference, "来源配置已被删除，无法重新发布")
		}
		prof, err := loadProfile(ctx, tx, userID, row.SourceProfileID)
		if err != nil {
			if apierr.CodeOf(err) == apierr.NotFound {
				return fieldError("sourceProfileId", validate.ReasonReference, "来源配置已被删除，无法重新发布")
			}
			return err
		}
		old, err := decodePayload(row.Payload)
		if err != nil {
			return err
		}
		nodeIDs, proxyIDs := payloadIDs(row.Kind, old)
		nodeIDs = keepExisting(nodeIDs, func(id string) bool {
			return slices.ContainsFunc(prof.Nodes, func(n model.Node) bool { return n.ID == id })
		})
		proxyIDs = keepExisting(proxyIDs, func(id string) bool {
			return slices.ContainsFunc(prof.Proxies, func(p model.Proxy) bool { return p.ID == id })
		})
		if len(nodeIDs) == 0 {
			return fieldError("payload.nodes", validate.ReasonReference, "分享里的节点都已从来源配置中删除")
		}
		if row.Kind != model.ShareTemplate && len(proxyIDs) == 0 {
			return fieldError("payload.proxies", validate.ReasonReference, "分享里的代理都已从来源配置中删除")
		}
		payload, err := buildPayload(row.Kind, prof, nodeIDs, proxyIDs)
		if err != nil {
			return err
		}
		blob, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		ts := s.now().Unix()
		rev, err := store.UpdateSharePayload(ctx, tx, row.ID, userID, string(blob), ts)
		if err != nil {
			return err
		}
		row.Payload, row.Revision, row.UpdatedAt = string(blob), rev, ts
		if err := bumpShareUsers(ctx, tx, row); err != nil {
			return err
		}
		if err := audit(ctx, tx, ts, userID, store.AuditShareRepublish, row.ID, nil); err != nil {
			return err
		}
		n, err := store.CountShareSubscribers(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		out, err = toShare(row, n, ts)
		return err
	})
	return out, err
}

// Revoke 撤销分享（软删除）。订阅保留，订阅者会看到"已撤销"。
func (s *Service) Revoke(ctx context.Context, userID, id string) error {
	return s.st.Tx(ctx, func(tx *sql.Tx) error {
		row, err := ownedShare(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Revoked() {
			return nil // 幂等
		}
		ts := s.now().Unix()
		if err := store.RevokeShare(ctx, tx, row.ID, userID, ts); err != nil {
			return err
		}
		if err := bumpShareUsers(ctx, tx, row); err != nil {
			return err
		}
		return audit(ctx, tx, ts, userID, store.AuditShareRevoke, row.ID, nil)
	})
}

// Preview 返回订阅前的预览，不含任何机密。
func (s *Service) Preview(ctx context.Context, userID, id string) (api.SharePreview, error) {
	var out api.SharePreview
	err := s.st.ReadTx(ctx, func(tx *sql.Tx) error {
		row, err := activeShare(ctx, tx, id, s.now().Unix())
		if err != nil {
			return err
		}
		payload, err := decodePayload(row.Payload)
		if err != nil {
			return err
		}
		owner, err := store.GetUser(ctx, tx, row.OwnerID)
		if err != nil {
			return err
		}
		n, err := store.CountShareSubscribers(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		out = api.SharePreview{
			ID: row.ID, Name: row.Name, Kind: row.Kind, OwnerNickname: owner.Nickname,
			Revision: row.Revision, ExpiresAt: unixPtr(row.ExpiresAt), MaxSubscribers: row.MaxSubscribers,
			Subscribers: n, Nodes: previewNodes(payload), Targets: previewTargets(payload), Proxies: []api.PreviewProxy{},
		}
		for _, p := range payload.Proxies {
			out.Proxies = append(out.Proxies, api.PreviewProxy{
				Name: p.Name, Type: p.Type, WithUDP: p.Type == model.ProxyTCP && p.WithUDP, RemotePort: p.RemotePort, CustomDomains: p.CustomDomains, SubDomain: p.SubDomain,
			})
		}
		if sub, err := store.FindSubscription(ctx, tx, userID, row.ID); err == nil {
			out.Subscription = &api.SubscriptionRef{ID: sub.ID}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return nil
	})
	return out, err
}

// ---------- 订阅 ----------

// Subscribe 订阅分享。人数上限的检查和插入在同一个写事务里（写事务串行化），不会超卖。
func (s *Service) Subscribe(ctx context.Context, userID string, req api.CreateSubscriptionRequest) (api.SubscriptionSummary, error) {
	var out api.SubscriptionSummary
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now().Unix()
		row, err := activeShare(ctx, tx, req.ShareID, now)
		if err != nil {
			return err
		}
		if existing, err := store.FindSubscription(ctx, tx, userID, row.ID); err == nil {
			return apierr.New(apierr.AlreadySubscribed).With("subscriptionId", existing.ID)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		n, err := store.CountSubscriptions(ctx, tx, userID)
		if err != nil {
			return err
		}
		if n >= validate.MaxSubscriptionsPerUser {
			return apierr.New(apierr.LimitExceeded).With(apierr.DetailLimit, validate.MaxSubscriptionsPerUser)
		}
		if row.MaxSubscribers > 0 {
			cnt, err := store.CountShareSubscribers(ctx, tx, row.ID)
			if err != nil {
				return err
			}
			if cnt >= row.MaxSubscribers {
				return apierr.New(apierr.ShareFull).With(apierr.DetailLimit, row.MaxSubscribers)
			}
		}
		payload, err := decodePayload(row.Payload)
		if err != nil {
			return err
		}
		params, err := checkParams(row.Kind, payload, req.Params)
		if err != nil {
			return err
		}
		blob, err := json.Marshal(params)
		if err != nil {
			return err
		}
		sub := store.SubscriptionRow{
			ID: model.NewID(), UserID: userID, ShareID: row.ID, Params: string(blob),
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.InsertSubscription(ctx, tx, sub); err != nil {
			if store.IsUniqueViolation(err) {
				return apierr.New(apierr.AlreadySubscribed)
			}
			return err
		}
		if _, err := store.BumpStateVersion(ctx, tx, userID); err != nil {
			return err
		}
		out, err = summary(ctx, tx, sub, now)
		return err
	})
	return out, err
}

// UpdateSubscription 修改订阅参数，baseRevision 不一致时返回 409，details.current 为当前摘要。
func (s *Service) UpdateSubscription(ctx context.Context, userID, id string, req api.UpdateSubscriptionRequest) (api.SubscriptionSummary, error) {
	if req.BaseRevision <= 0 {
		return api.SubscriptionSummary{}, fieldError("baseRevision", validate.ReasonRequired, "baseRevision 必填")
	}
	var out api.SubscriptionSummary
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now().Unix()
		sub, err := ownedSubscription(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if sub.Revision != req.BaseRevision {
			cur, err := summary(ctx, tx, sub, now)
			if err != nil {
				return err
			}
			return apierr.New(apierr.RevisionConflict).With(apierr.DetailCurrent, cur)
		}
		row, err := activeShare(ctx, tx, sub.ShareID, now)
		if err != nil {
			return err
		}
		payload, err := decodePayload(row.Payload)
		if err != nil {
			return err
		}
		params, err := checkParams(row.Kind, payload, req.Params)
		if err != nil {
			return err
		}
		blob, err := json.Marshal(params)
		if err != nil {
			return err
		}
		updated, ok, err := store.UpdateSubscriptionIfRevision(ctx, tx, userID, id, req.BaseRevision, string(blob), now)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.RevisionConflict)
		}
		if _, err := store.BumpStateVersion(ctx, tx, userID); err != nil {
			return err
		}
		out, err = summary(ctx, tx, updated, now)
		return err
	})
	return out, err
}

// Unsubscribe 删除订阅（分享已撤销时也可以删）。
func (s *Service) Unsubscribe(ctx context.Context, userID, id string) error {
	return s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.DeleteSubscription(ctx, tx, userID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.New(apierr.NotFound)
			}
			return err
		}
		_, err := store.BumpStateVersion(ctx, tx, userID)
		return err
	})
}

// Runtime 返回订阅运行所需的完整数据，只给订阅者本人。
func (s *Service) Runtime(ctx context.Context, userID, id string) (api.SubscriptionRuntime, error) {
	var out api.SubscriptionRuntime
	err := s.st.ReadTx(ctx, func(tx *sql.Tx) error {
		sub, err := ownedSubscription(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		row, err := activeShare(ctx, tx, sub.ShareID, s.now().Unix())
		if err != nil {
			return err
		}
		payload, err := decodePayload(row.Payload)
		if err != nil {
			return err
		}
		params, err := decodeParams(sub.Params)
		if err != nil {
			return err
		}
		data, nodeID := model.BuildRuntime(row.Kind, payload, params)
		out = api.SubscriptionRuntime{
			SubscriptionID: sub.ID, ShareID: row.ID, Kind: row.Kind,
			Revision: sub.Revision, ShareRevision: row.Revision, NodeID: nodeID, Data: data,
		}
		return nil
	})
	return out, err
}

// Summaries 返回用户全部订阅的摘要，供 state 使用（在调用方的只读事务里执行）。
func Summaries(ctx context.Context, q store.Querier, userID string, now int64) ([]api.SubscriptionSummary, error) {
	subs, err := store.ListSubscriptions(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	out := make([]api.SubscriptionSummary, 0, len(subs))
	for _, sub := range subs {
		sm, err := summary(ctx, q, sub, now)
		if err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, nil
}

// ---------- 内部 ----------

func summary(ctx context.Context, q store.Querier, sub store.SubscriptionRow, now int64) (api.SubscriptionSummary, error) {
	row, err := store.GetShare(ctx, q, sub.ShareID)
	if err != nil {
		return api.SubscriptionSummary{}, err
	}
	payload, err := decodePayload(row.Payload)
	if err != nil {
		return api.SubscriptionSummary{}, err
	}
	params, err := decodeParams(sub.Params)
	if err != nil {
		return api.SubscriptionSummary{}, err
	}
	owner, err := store.GetUser(ctx, q, row.OwnerID)
	if err != nil {
		return api.SubscriptionSummary{}, err
	}
	return api.SubscriptionSummary{
		ID: sub.ID, ShareID: row.ID, ShareName: row.Name, Kind: row.Kind, OwnerNickname: owner.Nickname,
		Revision: sub.Revision, ShareRevision: row.Revision, Revoked: row.Revoked(), Expired: row.Expired(now),
		ExpiresAt: unixPtr(row.ExpiresAt), Nodes: previewNodes(payload), Targets: previewTargets(payload),
		Params: params, CreatedAt: time.Unix(sub.CreatedAt, 0).UTC(), UpdatedAt: time.Unix(sub.UpdatedAt, 0).UTC(),
	}, nil
}

// checkParams 补全默认值（ID、名称、绑定地址）后按快照校验。
func checkParams(kind string, payload model.SharePayload, p model.SubscriptionParams) (model.SubscriptionParams, error) {
	p.Normalize()
	if kind == model.ShareTemplate {
		d := model.ProfileData{Proxies: p.Proxies}
		d.EnsureIDs()
		p.Proxies = d.Proxies
	}
	targets := map[string]model.P2PTarget{}
	for _, t := range payload.Targets {
		targets[t.ID] = t
	}
	for i := range p.Binds {
		b := &p.Binds[i]
		if b.ID == "" {
			b.ID = model.NewID()
		}
		if b.Name == "" {
			if t, ok := targets[b.TargetID]; ok {
				b.Name = "p2p-" + t.ServerName
			}
		}
		if b.BindAddr == "" {
			b.BindAddr = "127.0.0.1"
		}
	}
	if err := validate.SubscriptionParams(kind, payload, p); err != nil {
		return p, toAPI(err)
	}
	return p, nil
}

// buildPayload 按选择从配置里取出节点和代理，生成快照并校验。
func buildPayload(kind string, prof model.Profile, nodeIDs, proxyIDs []string) (model.SharePayload, error) {
	var c fieldErrs
	payload := model.SharePayload{}

	nodeByID := map[string]model.Node{}
	for _, n := range prof.Nodes {
		nodeByID[n.ID] = n
	}
	proxyByID := map[string]model.Proxy{}
	for _, p := range prof.Proxies {
		proxyByID[p.ID] = p
	}

	if len(nodeIDs) == 0 && kind == model.ShareFull {
		payload.Nodes = append(payload.Nodes, prof.Nodes...)
	}
	seen := map[string]bool{}
	for i, id := range nodeIDs {
		n, ok := nodeByID[id]
		if !ok {
			c.add(fmt.Sprintf("nodeIds[%d]", i), validate.ReasonReference, "节点不在配置里")
			continue
		}
		if !seen[id] {
			payload.Nodes = append(payload.Nodes, n)
			seen[id] = true
		}
	}

	switch kind {
	case model.ShareTemplate:
		if len(proxyIDs) > 0 {
			c.add("proxyIds", validate.ReasonInvalid, "模板分享不包含代理")
		}
	case model.ShareFull:
		if len(proxyIDs) == 0 {
			payload.Proxies = append(payload.Proxies, prof.Proxies...)
		}
		seen := map[string]bool{}
		for i, id := range proxyIDs {
			p, ok := proxyByID[id]
			if !ok {
				c.add(fmt.Sprintf("proxyIds[%d]", i), validate.ReasonReference, "代理不在配置里")
				continue
			}
			if !seen[id] {
				payload.Proxies = append(payload.Proxies, p)
				seen[id] = true
			}
		}
	case model.ShareP2P:
		user := ""
		if len(payload.Nodes) > 0 {
			user = payload.Nodes[0].User
		}
		seen := map[string]bool{}
		for i, id := range proxyIDs {
			p, ok := proxyByID[id]
			if !ok {
				c.add(fmt.Sprintf("proxyIds[%d]", i), validate.ReasonReference, "代理不在配置里")
				continue
			}
			if !model.IsP2PType(p.Type) {
				c.add(fmt.Sprintf("proxyIds[%d]", i), validate.ReasonInvalid, "P2P 分享只能选择 stcp、xtcp、sudp 代理（%q 是 %s）", p.Name, p.Type)
				continue
			}
			if !seen[id] {
				payload.Targets = append(payload.Targets, model.P2PTarget{
					ID: p.ID, ServerName: p.Name, Type: p.Type, SecretKey: p.SecretKey,
					ServerUser: user, LocalPort: p.LocalPort, UseEncryption: p.UseEncryption, UseCompression: p.UseCompression,
				})
				seen[id] = true
			}
		}
	}
	if err := c.err(); err != nil {
		return payload, err
	}
	payload.Normalize()
	if err := validate.SharePayload(kind, payload); err != nil {
		return payload, toAPI(err)
	}
	return payload, nil
}

func payloadIDs(kind string, p model.SharePayload) (nodeIDs, proxyIDs []string) {
	for _, n := range p.Nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}
	switch kind {
	case model.ShareFull:
		for _, x := range p.Proxies {
			proxyIDs = append(proxyIDs, x.ID)
		}
	case model.ShareP2P:
		for _, t := range p.Targets {
			proxyIDs = append(proxyIDs, t.ID)
		}
	}
	return
}

func keepExisting(ids []string, exists func(string) bool) []string {
	out := ids[:0:0]
	for _, id := range ids {
		if exists(id) {
			out = append(out, id)
		}
	}
	return out
}

func loadProfile(ctx context.Context, q store.Querier, userID, id string) (model.Profile, error) {
	r, err := store.GetOwnedProfile(ctx, q, userID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Profile{}, apierr.New(apierr.NotFound)
	}
	if err != nil {
		return model.Profile{}, err
	}
	var d model.ProfileData
	if err := json.Unmarshal([]byte(r.Data), &d); err != nil {
		return model.Profile{}, fmt.Errorf("profile %s: %w", r.ID, err)
	}
	return model.Profile{ID: r.ID, Name: r.Name, Nodes: d.Nodes, Proxies: d.Proxies, Visitors: d.Visitors}, nil
}

func ownedShare(ctx context.Context, q store.Querier, userID, id string) (store.ShareRow, error) {
	row, err := store.GetShare(ctx, q, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.OwnerID != userID) {
		return store.ShareRow{}, apierr.New(apierr.NotFound)
	}
	return row, err
}

// activeShare 查分享，并检查未撤销、未过期。
func activeShare(ctx context.Context, q store.Querier, id string, now int64) (store.ShareRow, error) {
	if id == "" {
		return store.ShareRow{}, fieldError("shareId", validate.ReasonRequired, "分享 ID 不能为空")
	}
	row, err := store.GetShare(ctx, q, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.ShareRow{}, apierr.New(apierr.NotFound)
	}
	if err != nil {
		return store.ShareRow{}, err
	}
	if row.Revoked() {
		return store.ShareRow{}, apierr.New(apierr.ShareRevoked)
	}
	if row.Expired(now) {
		return store.ShareRow{}, apierr.New(apierr.ShareExpired)
	}
	return row, nil
}

func ownedSubscription(ctx context.Context, q store.Querier, userID, id string) (store.SubscriptionRow, error) {
	sub, err := store.GetOwnedSubscription(ctx, q, userID, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.SubscriptionRow{}, apierr.New(apierr.NotFound)
	}
	return sub, err
}

// bumpShareUsers 让分享者和所有订阅者的 stateVersion 加 1。
func bumpShareUsers(ctx context.Context, q store.Querier, row store.ShareRow) error {
	ids, err := store.ListShareSubscriberIDs(ctx, q, row.ID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range append([]string{row.OwnerID}, ids...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := store.BumpStateVersion(ctx, q, id); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

func audit(ctx context.Context, q store.Querier, at int64, actor, action, target string, detail map[string]any) error {
	return store.InsertAudit(ctx, q, store.AuditEntry{At: at, ActorID: actor, Action: action, Target: target, Detail: detail})
}

func toShare(r store.ShareRow, subscribers int, now int64) (api.Share, error) {
	payload, err := decodePayload(r.Payload)
	if err != nil {
		return api.Share{}, err
	}
	return api.Share{
		ID: r.ID, Name: r.Name, Kind: r.Kind, SourceProfileID: r.SourceProfileID, Revision: r.Revision,
		ExpiresAt: unixPtr(r.ExpiresAt), MaxSubscribers: r.MaxSubscribers, Subscribers: subscribers,
		Revoked: r.Revoked(), Expired: r.Expired(now),
		CreatedAt: time.Unix(r.CreatedAt, 0).UTC(), UpdatedAt: time.Unix(r.UpdatedAt, 0).UTC(),
		Payload: payload,
	}, nil
}

func previewNodes(p model.SharePayload) []api.PreviewNode {
	out := make([]api.PreviewNode, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		out = append(out, api.PreviewNode{ID: n.ID, Remark: n.Remark, Protocol: n.Protocol})
	}
	return out
}

func previewTargets(p model.SharePayload) []api.PreviewTarget {
	out := make([]api.PreviewTarget, 0, len(p.Targets))
	for _, t := range p.Targets {
		out = append(out, api.PreviewTarget{ID: t.ID, ServerName: t.ServerName, Type: t.Type, LocalPort: t.LocalPort})
	}
	return out
}

func decodePayload(s string) (model.SharePayload, error) {
	var p model.SharePayload
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return p, fmt.Errorf("share payload: %w", err)
	}
	p.Normalize()
	return p, nil
}

func decodeParams(s string) (model.SubscriptionParams, error) {
	var p model.SubscriptionParams
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return p, fmt.Errorf("subscription params: %w", err)
	}
	p.Normalize()
	return p, nil
}

func unixPtr(p *int64) *time.Time {
	if p == nil {
		return nil
	}
	t := time.Unix(*p, 0).UTC()
	return &t
}

// NewShareID 生成 128 位随机数的 base62 编码（22 个字符），PLAN 7.6。
func NewShareID() string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("share: crypto/rand failed: " + err.Error())
	}
	n := new(big.Int).SetBytes(b[:])
	base := big.NewInt(62)
	mod := new(big.Int)
	out := make([]byte, 22)
	for i := len(out) - 1; i >= 0; i-- {
		n.DivMod(n, base, mod)
		out[i] = alphabet[mod.Int64()]
	}
	return string(out)
}

// ---------- 错误 ----------

type fieldErrs struct{ errs validate.Errors }

func (f *fieldErrs) add(field, reason, msg string, args ...any) {
	f.errs = append(f.errs, validate.FieldError{Field: field, Reason: reason, Message: fmt.Sprintf(msg, args...)})
}

func (f *fieldErrs) merge(err error) {
	var es validate.Errors
	if errors.As(err, &es) {
		f.errs = append(f.errs, es...)
	}
}

func (f *fieldErrs) err() error {
	if len(f.errs) == 0 {
		return nil
	}
	return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, f.errs)
}

func fieldError(field, reason, msg string) error {
	return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{Field: field, Reason: reason, Message: msg}})
}

func toAPI(err error) error {
	var es validate.Errors
	if errors.As(err, &es) {
		return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, es)
	}
	return err
}
