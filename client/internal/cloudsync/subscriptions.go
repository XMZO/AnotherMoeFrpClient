package cloudsync

// 订阅的同步（PLAN 7.6、Phase 4）。
//
// state 里只有订阅摘要（不含机密）。摘要的 shareRevision 或订阅 revision 和本机缓存的 runtime
// 不一致时，拉取 GET /subscriptions/{id}/runtime，把可运行的内容存进缓存（DPAPI 加密），
// 作为一条只读的"订阅"条目（Profile.ID = 订阅 ID），引擎和普通配置一样启动它。
//
// 分享被撤销或过期：清空缓存里的内容（节点 token、secretKey），正在运行就停止隧道，
// 条目保留并显示"已撤销 / 已过期"，由用户删除。订阅在云端没有了：删除条目，正在运行就停止。

import (
	"context"
	"time"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// syncSubscriptions 按 state 里的订阅摘要更新缓存，返回内容有变化的条目和需要询问是否应用的运行中条目。
// 网络、会话、版本过低这类错误中止并返回，由调用方处理。
func (m *Manager) syncSubscriptions(ctx context.Context, subs []api.SubscriptionSummary) (changed []string, remote []RemoteUpdate, err error) {
	seen := map[string]bool{}
	for _, sum := range subs {
		seen[sum.ID] = true
		ch, rm, err := m.applySummary(ctx, sum, false)
		if err != nil {
			return changed, remote, err
		}
		if ch {
			changed = append(changed, sum.ID)
		}
		if rm != nil {
			remote = append(remote, *rm)
		}
	}
	for _, c := range m.profiles.All() {
		if c.IsSubscription() && !seen[c.Profile.ID] {
			m.RemoveSubscription(c.Profile.ID)
			changed = append(changed, c.Profile.ID)
		}
	}
	return changed, remote, nil
}

// applySummary 用一条摘要更新缓存；force 为 true 时总是重新拉取 runtime。
func (m *Manager) applySummary(ctx context.Context, sum api.SubscriptionSummary, force bool) (changed bool, remote *RemoteUpdate, err error) {
	old, exists := m.profiles.Get(sum.ID)
	if exists && !old.IsSubscription() {
		m.logf("sync: subscription %s collides with a profile id, skipped", sum.ID)
		return false, nil, nil
	}
	inactive := sum.Revoked || sum.Expired
	meta := store.SubscriptionMeta{Summary: sum}
	profile := model.Profile{ID: sum.ID, Name: sum.ShareName, Revision: sum.Revision, UpdatedAt: sum.UpdatedAt}

	if !inactive {
		need := force || !exists || old.Sub == nil ||
			old.Sub.RuntimeShareRevision != sum.ShareRevision || old.Sub.RuntimeRevision != sum.Revision ||
			len(old.Profile.Nodes) == 0
		if need {
			rt, err := m.api.SubscriptionRuntime(ctx, sum.ID)
			switch code := apierr.CodeOf(err); {
			case err == nil:
				profile.Nodes, profile.Proxies, profile.Visitors = rt.Data.Nodes, rt.Data.Proxies, rt.Data.Visitors
				meta.RuntimeShareRevision, meta.RuntimeRevision, meta.DefaultNodeID = rt.ShareRevision, rt.Revision, rt.NodeID
			case code == apierr.ShareRevoked:
				meta.Summary.Revoked, inactive = true, true
			case code == apierr.ShareExpired:
				meta.Summary.Expired, inactive = true, true
			case code == apierr.NotFound:
				m.RemoveSubscription(sum.ID)
				return true, nil, nil
			case apiclient.IsOffline(err) || apiclient.SessionEnded(err) || apiclient.IsOutdated(err) || code == apierr.RateLimited:
				return false, nil, err
			default:
				m.logf("sync: runtime of subscription %s: %v", sum.ID, err)
				if !exists {
					return false, nil, nil
				}
				// 保留旧内容，下一轮再试
				profile.Nodes, profile.Proxies, profile.Visitors = old.Profile.Nodes, old.Profile.Proxies, old.Profile.Visitors
				meta.RuntimeShareRevision, meta.RuntimeRevision, meta.DefaultNodeID = -1, -1, old.Sub.DefaultNodeID
			}
		} else {
			profile.Nodes, profile.Proxies, profile.Visitors = old.Profile.Nodes, old.Profile.Proxies, old.Profile.Visitors
			meta.RuntimeShareRevision, meta.RuntimeRevision, meta.DefaultNodeID = old.Sub.RuntimeShareRevision, old.Sub.RuntimeRevision, old.Sub.DefaultNodeID
		}
	}
	profile.Normalize()

	runID, _, running := m.eng.RunningProfile()
	isRunning := running && runID == sum.ID
	contentChanged := !exists || !sameContent(old.Profile, profile)
	metaChanged := !exists || old.Sub == nil || old.Sub.Summary.Revoked != meta.Summary.Revoked ||
		old.Sub.Summary.Expired != meta.Summary.Expired || old.Sub.Summary.ShareName != meta.Summary.ShareName ||
		old.Sub.Summary.Revision != meta.Summary.Revision || old.Sub.Summary.ShareRevision != meta.Summary.ShareRevision

	if inactive && isRunning {
		m.logf("sync: subscription %s is revoked or expired, stopping tunnel", sum.ID)
		_ = m.eng.Stop()
	}

	err = m.profiles.Tx(func(t *store.Txn) error {
		lc := t.Find(sum.ID)
		if lc == nil {
			t.Add(store.CachedProfile{Profile: profile, Origin: store.OriginSubscription, LocalUpdatedAt: time.Now().UTC(), Sub: &meta})
			return nil
		}
		lc.Profile, lc.Sub = profile, &meta
		if contentChanged || metaChanged {
			lc.LocalUpdatedAt = time.Now().UTC()
		}
		return nil
	})
	if err != nil {
		m.logf("sync: save subscription %s: %v", sum.ID, err)
		return false, nil, nil
	}
	if isRunning && !inactive && exists && contentChanged {
		r := RemoteUpdate{ProfileID: sum.ID, Name: sum.ShareName, Mode: m.eng.PredictUpdate(profile)}
		m.mu.Lock()
		m.remote[sum.ID] = r
		m.mu.Unlock()
		remote = &r
	}
	return contentChanged || metaChanged, remote, nil
}

// ApplySubscription 在订阅、修改订阅参数之后立即更新缓存（不等下一轮同步），并通知界面。
func (m *Manager) ApplySubscription(ctx context.Context, sum api.SubscriptionSummary) error {
	changed, remote, err := m.applySummary(ctx, sum, true)
	if err != nil {
		m.CheckError(err)
		return apperr.Normalize(err)
	}
	if changed {
		m.fire(EventSyncChanged, Changed{ProfileIDs: []string{sum.ID}})
	}
	if remote != nil {
		m.fire(EventSyncRemote, *remote)
	}
	return nil
}

// RemoveSubscription 删除缓存里的订阅条目；正在运行就先停止隧道。
func (m *Manager) RemoveSubscription(id string) {
	if runID, _, running := m.eng.RunningProfile(); running && runID == id {
		m.logf("sync: subscription %s removed, stopping tunnel", id)
		_ = m.eng.Stop()
	}
	_ = m.profiles.Tx(func(t *store.Txn) error {
		if lc := t.Find(id); lc != nil && lc.IsSubscription() {
			t.Remove(id)
		}
		return nil
	})
	m.mu.Lock()
	delete(m.remote, id)
	m.mu.Unlock()
}

// CheckError 处理直接调用 API（分享、订阅）时遇到的会话失效和版本过低，和同步循环的处理一致。
func (m *Manager) CheckError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	s := m.cur
	m.mu.Unlock()
	switch {
	case s == nil:
	case apiclient.SessionEnded(err):
		m.endSession(s, err)
	case apiclient.IsOutdated(err):
		m.setOutdated(err)
	}
}

// API 返回 API 客户端（分享服务直接调用分享、订阅接口）。
func (m *Manager) API() *apiclient.Client { return m.api }

// NotifyChanged 通知界面这些条目有变化（分享服务删除订阅后调用）。
func (m *Manager) NotifyChanged(ids ...string) {
	m.fire(EventSyncChanged, Changed{ProfileIDs: ids})
}
