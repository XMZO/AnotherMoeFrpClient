package cloudsync

import (
	"context"
	"strconv"
	"time"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// loop 是一个会话的后台循环：同步一次，然后等到下一次（60 秒；离线时 15 秒），
// 或者被 kick（焦点、编辑后的防抖、立即同步）提前唤醒。
func (m *Manager) loop(s *session) {
	defer close(s.done)
	reason := "start"
	for {
		wait := m.syncOnce(s, reason)
		if s.ctx.Err() != nil {
			return
		}
		t := time.NewTimer(wait)
		select {
		case <-s.ctx.Done():
			t.Stop()
			return
		case reason = <-s.kickCh:
			t.Stop()
		case <-t.C:
			reason = "timer"
		}
	}
}

// syncOnce 先拉取再推送，返回到下一次同步的等待时间。
func (m *Manager) syncOnce(s *session, reason string) time.Duration {
	m.setState(StateSyncing, nil)
	err := m.pull(s, reason)
	if err == nil {
		m.syncOfficial(s)
		err = m.push(s.ctx)
	}
	if s.ctx.Err() != nil || !m.isCurrent(s) {
		return 0
	}
	switch {
	case err == nil:
		m.setState(StateSynced, nil)
		return m.poll
	case apiclient.IsOffline(err):
		m.logf("sync: offline: %v", err)
		m.setState(StateOffline, err)
		return m.retry
	case apiclient.SessionEnded(err):
		m.endSession(s, err)
		return 0
	case apiclient.IsOutdated(err):
		m.logf("sync: client outdated, sync paused")
		m.setOutdated(err)
		m.setState(StateOutdated, err)
		return time.Hour
	case apierr.CodeOf(err) == apierr.RateLimited:
		m.setState(StateError, err)
		return retryAfter(err, m.retry)
	default:
		m.logf("sync: error: %v", err)
		m.setState(StateError, err)
		return m.poll
	}
}

func retryAfter(err error, def time.Duration) time.Duration {
	if e, ok := apierr.As(err); ok && e.Details != nil {
		switch v := e.Details[apierr.DetailRetryAfter].(type) {
		case float64:
			if v > 0 {
				return time.Duration(v) * time.Second
			}
		case string:
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return time.Duration(n) * time.Second
			}
		}
	}
	return def
}

// ---- 官方节点 ----

// syncOfficial 拉取面板下发的官方节点，有变化时存进缓存并通知隧道服务。
// 失败只记日志：旧版服务端没有这个接口，不能影响配置同步。
func (m *Manager) syncOfficial(s *session) {
	nodes, etag, notModified, err := m.api.OfficialNodes(s.ctx, m.profiles.Sync().OfficialETag)
	if err != nil {
		if apierr.CodeOf(err) != apierr.NotFound {
			m.logf("official nodes: %v", err)
		}
		return
	}
	if notModified || !m.isCurrent(s) {
		return
	}
	_ = m.profiles.Tx(func(t *store.Txn) error {
		st := t.Sync()
		st.Official, st.OfficialETag = nodes, etag
		return nil
	})
	m.logf("official nodes: %d", len(nodes))
	if m.onOfficial != nil {
		m.onOfficial(nodes)
	}
}

// ---- 拉取 ----

// pull 拉取云端状态并合并到本机缓存。
func (m *Manager) pull(s *session, reason string) error {
	st, notModified, err := m.api.State(s.ctx, s.etag)
	if err != nil {
		return err
	}
	s.lastPull.Store(time.Now().UnixNano())
	if !m.isCurrent(s) {
		return errStopped
	}
	m.clearOutdated()
	if notModified {
		m.logf("sync: not modified (trigger=%s)", reason)
		return nil
	}
	m.updateUser(st.User)
	res := m.merge(st)
	subChanged, subRemote, subErr := m.syncSubscriptions(s.ctx, st.Subscriptions)
	res.changed = append(res.changed, subChanged...)
	res.remote = append(res.remote, subRemote...)
	if subErr == nil {
		s.etag = st.StateVersion
	} else {
		s.etag = -1 // 订阅的 runtime 没拉完：下一轮全量拉取，不能被 304 跳过
	}
	m.logf("sync: pulled stateVersion=%d profiles=%d subscriptions=%d changed=%d conflicts=%d (trigger=%s)", st.StateVersion, len(st.Profiles), len(st.Subscriptions), len(res.changed), len(res.conflicts), reason)

	if s.wantDefault {
		s.wantDefault = false
		if len(st.Profiles) == 0 && !m.hasCloudEntries() {
			m.createDefault()
		}
	}
	if len(res.changed) > 0 {
		m.fire(EventSyncChanged, Changed{ProfileIDs: res.changed})
	}
	for _, c := range res.conflicts {
		m.fire(EventSyncConflict, c)
	}
	for _, r := range res.remote {
		m.fire(EventSyncRemote, r)
	}
	return subErr
}

func (m *Manager) clearOutdated() {
	m.mu.Lock()
	had := m.acct.Outdated != nil
	m.acct.Outdated = nil
	m.mu.Unlock()
	if had {
		m.emitAccount()
	}
}

func (m *Manager) hasCloudEntries() bool {
	for _, c := range m.profiles.All() {
		if c.IsCloud() {
			return true
		}
	}
	return false
}

// createDefault 在云端和本机都没有云端配置时新建一份空的默认配置，随后推送上去。
func (m *Manager) createDefault() {
	p := model.Profile{ID: model.NewID(), Name: DefaultProfileName}
	if _, err := m.profiles.PutNew(p, store.OriginCloud); err != nil {
		m.logf("sync: create default profile: %v", err)
		return
	}
	m.logf("sync: cloud is empty, created default profile")
	m.fire(EventSyncChanged, Changed{ProfileIDs: []string{p.ID}})
}

type mergeResult struct {
	changed   []string
	conflicts []Conflict
	remote    []RemoteUpdate
}

// merge 把云端状态合并进缓存（PLAN 6.6）。规则：
//
//   - 本机没有 → 加进来；
//   - 本机没有修改、云端版本更新 → 用云端的；正在运行的配置会询问是否应用；
//   - 本机有修改：云端版本还是修改所基于的版本 → 保留本机等推送；云端也改了 → 冲突
//     （两边内容一样时直接采用云端版本号）；
//   - 本机删除了：云端没再改 → 等推送删除；云端又改了 → 冲突；
//   - 云端没有了：本机没修改、没在运行 → 删掉；否则 → 冲突；从没上传成功的 → 保留等推送。
func (m *Manager) merge(st api.StateResponse) mergeResult {
	var res mergeResult
	runID, _, running := m.eng.RunningProfile()
	isRunning := func(id string) bool { return running && id == runID }
	resolved := map[string]bool{}
	now := time.Now().UTC()

	_ = m.profiles.Tx(func(t *store.Txn) error {
		res = mergeResult{}
		sync := t.Sync()
		if sync.Account != "" && sync.Account != st.User.ID {
			for _, c := range t.Entries() {
				if c.IsCloud() || c.IsSubscription() {
					res.changed = append(res.changed, c.Profile.ID)
				}
			}
			for _, id := range res.changed {
				t.Remove(id)
			}
		}
		sync.Account, sync.Nickname, sync.StateVersion, sync.LastSyncAt = st.User.ID, st.User.Nickname, st.StateVersion, &now

		seen := map[string]bool{}
		for i := range st.Profiles {
			cp := st.Profiles[i]
			seen[cp.ID] = true
			lc := t.Find(cp.ID)
			if lc == nil {
				t.Add(store.CachedProfile{Profile: cp, Origin: store.OriginCloud, BaseRevision: cp.Revision, LocalUpdatedAt: cp.UpdatedAt})
				res.changed = append(res.changed, cp.ID)
				resolved[cp.ID] = true
				continue
			}
			if !lc.IsCloud() { // 和本地配置撞了 ID（几乎不可能）：当成还没上传的云端配置处理
				lc.Origin, lc.Profile.Revision, lc.BaseRevision, lc.Dirty = store.OriginCloud, 0, 0, true
			}
			switch {
			case lc.Deleted:
				if cp.Revision == lc.BaseRevision {
					resolved[cp.ID] = true
				} else {
					res.conflicts = append(res.conflicts, newConflict(nil, &cp, isRunning(cp.ID)))
				}
			case lc.Dirty:
				switch {
				case lc.Profile.Revision != 0 && cp.Revision == lc.BaseRevision:
					resolved[cp.ID] = true
				case sameContent(lc.Profile, cp):
					adopt(lc, cp)
					resolved[cp.ID] = true
				default:
					local := lc.Profile
					res.conflicts = append(res.conflicts, newConflict(&local, &cp, isRunning(cp.ID)))
				}
			default:
				resolved[cp.ID] = true
				if cp.Revision == lc.Profile.Revision {
					continue
				}
				old := lc.Profile
				replaceWithCloud(lc, cp)
				res.changed = append(res.changed, cp.ID)
				if isRunning(cp.ID) && !sameContent(old, cp) {
					res.remote = append(res.remote, RemoteUpdate{ProfileID: cp.ID, Name: cp.Name, Mode: m.eng.PredictUpdate(cp)})
				}
			}
		}

		var gone []string
		for _, lc := range t.Entries() {
			id := lc.Profile.ID
			if !lc.IsCloud() || seen[id] {
				continue
			}
			switch {
			case lc.Profile.Revision == 0 && !lc.Deleted: // 还没上传成功，等推送
				resolved[id] = true
			case lc.Deleted || (!lc.Dirty && !isRunning(id)):
				gone = append(gone, id)
				resolved[id] = true
			default:
				local := lc.Profile
				res.conflicts = append(res.conflicts, newConflict(&local, nil, isRunning(id)))
			}
		}
		for _, id := range gone {
			t.Remove(id)
			res.changed = append(res.changed, id)
		}
		return nil
	})

	m.mu.Lock()
	for id := range resolved {
		delete(m.conflicts, id)
	}
	fresh := res.conflicts[:0]
	for _, c := range res.conflicts {
		prev, ok := m.conflicts[c.ProfileID]
		cc := c
		m.conflicts[c.ProfileID] = &cc
		if !ok || prev.CloudRevision != c.CloudRevision || prev.CloudDeleted != c.CloudDeleted || prev.LocalDeleted != c.LocalDeleted {
			fresh = append(fresh, c)
		}
	}
	res.conflicts = fresh
	for _, r := range res.remote {
		m.remote[r.ProfileID] = r
	}
	m.mu.Unlock()
	return res
}

// adopt：本机内容和云端一样，只采用云端的版本号，清掉修改标记。
func adopt(lc *store.CachedProfile, cp model.Profile) {
	lc.Profile.Revision, lc.BaseRevision, lc.Dirty, lc.Deleted = cp.Revision, cp.Revision, false, false
	lc.Profile.UpdatedAt = cp.UpdatedAt
}

// replaceWithCloud：用云端版本覆盖本机。
func replaceWithCloud(lc *store.CachedProfile, cp model.Profile) {
	lc.Profile, lc.BaseRevision, lc.Dirty, lc.Deleted = cp, cp.Revision, false, false
	lc.Origin = store.OriginCloud
	lc.LocalUpdatedAt = time.Now().UTC()
}

// ---- 推送 ----

// push 把有修改的云端配置推上去：删除 → DELETE，从没上传过 → POST（带本机 ID），其余 → PUT（带 baseRevision）。
// 网络、会话、版本过低、限流这几类错误会中止本轮推送并返回；单个配置的其他错误记下来，等它再被修改时重试。
func (m *Manager) push(ctx context.Context) error {
	for _, c := range m.profiles.All() {
		if !c.IsCloud() || !c.Dirty || m.blocked(c) {
			continue
		}
		if ctx.Err() != nil {
			return errStopped
		}
		id := c.Profile.ID
		var err error
		switch {
		case c.Deleted:
			err = m.api.DeleteProfile(ctx, id)
			if err == nil || apierr.CodeOf(err) == apierr.NotFound {
				m.removeTombstone(id, c.Edits)
				m.logf("sync: pushed delete of %s", id)
				continue
			}
		case c.Profile.Revision == 0:
			var p model.Profile
			p, err = m.api.CreateProfile(ctx, api.CreateProfileRequest{
				ID: id, Name: c.Profile.Name, Nodes: c.Profile.Nodes, Proxies: c.Profile.Proxies, Visitors: c.Profile.Visitors,
			})
			if err == nil {
				m.markPushed(id, c.Edits, p)
				m.logf("sync: created %s revision=%d", id, p.Revision)
				continue
			}
		default:
			var p model.Profile
			p, err = m.api.UpdateProfile(ctx, id, api.UpdateProfileRequest{
				BaseRevision: c.BaseRevision, Name: c.Profile.Name, Nodes: c.Profile.Nodes, Proxies: c.Profile.Proxies, Visitors: c.Profile.Visitors,
			})
			if err == nil {
				m.markPushed(id, c.Edits, p)
				m.logf("sync: pushed %s base=%d -> revision=%d", id, c.BaseRevision, p.Revision)
				continue
			}
			if apierr.CodeOf(err) == apierr.NotFound {
				m.addConflict(c, nil)
				continue
			}
		}

		if cur, ok := apiclient.ConflictCurrent(err); ok {
			if !c.Deleted && sameContent(c.Profile, cur) {
				m.markPushed(id, c.Edits, cur)
				continue
			}
			m.addConflict(c, &cur)
			continue
		}
		if apiclient.IsOffline(err) || apiclient.SessionEnded(err) || apiclient.IsOutdated(err) ||
			apierr.CodeOf(err) == apierr.RateLimited || err == errStopped {
			return err
		}
		m.recordPushError(c, err)
	}
	return nil
}

// blocked：有未解决的冲突，或上次推送失败后还没有再修改过。
func (m *Manager) blocked(c store.CachedProfile) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conflicts[c.Profile.ID]; ok {
		return true
	}
	pe, ok := m.pushErrs[c.Profile.ID]
	return ok && pe.edits == c.Edits
}

// markPushed：推送成功，记下云端版本。推送期间本机又改过的话保持 Dirty，下一轮再推。
func (m *Manager) markPushed(id string, edits int64, p model.Profile) {
	_ = m.profiles.Tx(func(t *store.Txn) error {
		lc := t.Find(id)
		if lc == nil {
			// 上传期间用户把这份（当时还没上传成功的）配置删掉了：留个墓碑，下一轮删掉云端的
			t.Add(store.CachedProfile{Profile: p, Origin: store.OriginCloud, BaseRevision: p.Revision, Dirty: true, Deleted: true, LocalUpdatedAt: time.Now().UTC()})
			return nil
		}
		lc.Profile.Revision, lc.BaseRevision = p.Revision, p.Revision
		if lc.Edits == edits && !lc.Deleted {
			lc.Dirty = false
			lc.Profile.UpdatedAt = p.UpdatedAt
		}
		return nil
	})
	m.mu.Lock()
	delete(m.pushErrs, id)
	m.mu.Unlock()
}

func (m *Manager) removeTombstone(id string, edits int64) {
	_ = m.profiles.Tx(func(t *store.Txn) error {
		if lc := t.Find(id); lc != nil && lc.Deleted && lc.Edits == edits {
			t.Remove(id)
		}
		return nil
	})
}

func (m *Manager) addConflict(c store.CachedProfile, cloud *model.Profile) {
	runID, _, running := m.eng.RunningProfile()
	var local *model.Profile
	if !c.Deleted {
		p := c.Profile
		local = &p
	}
	cf := newConflict(local, cloud, running && runID == c.Profile.ID)
	m.mu.Lock()
	m.conflicts[c.Profile.ID] = &cf
	m.mu.Unlock()
	m.logf("sync: conflict on %s (cloud deleted=%v)", c.Profile.ID, cloud == nil)
	m.fire(EventSyncConflict, cf)
}

// recordPushError 记录不会自动重试的推送错误（例如超出数量上限、校验失败）。
// 上传（第一次推送）因为超出数量上限失败时，把它改回本地配置，免得一直挂着。
func (m *Manager) recordPushError(c store.CachedProfile, err error) {
	ae, _ := apierr.As(apperr.Normalize(err))
	id := c.Profile.ID
	reverted := false
	if c.Profile.Revision == 0 && ae.Code == apierr.LimitExceeded {
		_ = m.profiles.Tx(func(t *store.Txn) error {
			if lc := t.Find(id); lc != nil && lc.Profile.Revision == 0 {
				lc.Origin, lc.Dirty = store.OriginLocal, true
				reverted = true
			}
			return nil
		})
	}
	m.mu.Lock()
	if !reverted {
		m.pushErrs[id] = pushErr{edits: c.Edits, err: ae}
	}
	m.mu.Unlock()
	m.logf("sync: push %s failed: %v (reverted=%v)", id, err, reverted)
	m.fire(EventSyncError, PushError{ProfileID: id, Name: c.Profile.Name, Error: ae, Reverted: reverted})
	if reverted {
		m.fire(EventSyncChanged, Changed{ProfileIDs: []string{id}})
	}
}

// ProfileSync 返回一份配置的同步状态（列表上的标记）：local / synced / pending / conflict / error，
// error 时附带错误。
func (m *Manager) ProfileSync(c store.CachedProfile) (string, *apierr.Error) {
	if c.IsSubscription() {
		return "subscription", nil
	}
	if !c.IsCloud() {
		return "local", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conflicts[c.Profile.ID]; ok {
		return "conflict", nil
	}
	if pe, ok := m.pushErrs[c.Profile.ID]; ok && pe.edits == c.Edits {
		return "error", pe.err
	}
	if c.Dirty {
		return "pending", nil
	}
	return "synced", nil
}

// CloudCount 返回缓存里云端配置的数量（不含墓碑），用于上传前检查每个用户的配置上限。
func (m *Manager) CloudCount() int {
	n := 0
	for _, c := range m.profiles.All() {
		if c.IsCloud() && !c.Deleted {
			n++
		}
	}
	return n
}

// MaxCloudProfiles 是每个用户的云端配置上限（PLAN 5.2）。
const MaxCloudProfiles = validate.MaxProfilesPerUser
