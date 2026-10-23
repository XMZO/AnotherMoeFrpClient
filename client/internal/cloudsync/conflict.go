package cloudsync

import (
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// Conflict 是 sync:conflict 事件的内容：本机和云端都改了同一份配置（PLAN 6.6）。
type Conflict struct {
	ProfileID      string     `json:"profileId"`
	Name           string     `json:"name"`         // 本机的名称（本机已删除时用云端的）
	LocalDeleted   bool       `json:"localDeleted"` // 本机删除了，云端又改了
	CloudDeleted   bool       `json:"cloudDeleted"` // 云端删除了，本机又改了（或正在运行）
	CloudName      string     `json:"cloudName,omitempty"`
	CloudRevision  int64      `json:"cloudRevision"`
	CloudUpdatedAt *time.Time `json:"cloudUpdatedAt,omitempty"`
	Running        bool       `json:"running"` // 这份配置正在运行
	Diff           []DiffItem `json:"diff"`    // 差异摘要
	DetectedAt     time.Time  `json:"detectedAt"`

	local *model.Profile
	cloud *model.Profile
}

// DiffItem 是差异摘要的一行。中文说明由前端拼。
type DiffItem struct {
	Section string   `json:"section"`          // profile / nodes / proxies / visitors
	Name    string   `json:"name"`             // 代理名、访客名、节点备注（或地址）
	Change  string   `json:"change"`           // local_only（只有本机有）/ cloud_only（只有云端有）/ changed（两边不同）
	Fields  []string `json:"fields,omitempty"` // changed 时不同的字段（JSON 字段名）
	Local   string   `json:"local,omitempty"`  // section 为 profile、字段为 name 时：本机的值
	Cloud   string   `json:"cloud,omitempty"`  // 同上：云端的值
}

func newConflict(local, cloud *model.Profile, running bool) Conflict {
	c := Conflict{Running: running, DetectedAt: time.Now().UTC(), local: local, cloud: cloud}
	if local != nil {
		c.ProfileID, c.Name = local.ID, local.Name
	} else {
		c.LocalDeleted = true
	}
	if cloud != nil {
		c.ProfileID, c.CloudName, c.CloudRevision = cloud.ID, cloud.Name, cloud.Revision
		if !cloud.UpdatedAt.IsZero() {
			t := cloud.UpdatedAt
			c.CloudUpdatedAt = &t
		}
		if c.Name == "" {
			c.Name = cloud.Name
		}
	} else {
		c.CloudDeleted = true
	}
	c.Diff = Diff(local, cloud)
	return c
}

// Diff 计算本机和云端两份配置的差异摘要。任一方为 nil 时返回空（界面直接说明"已删除"）。
func Diff(local, cloud *model.Profile) []DiffItem {
	out := []DiffItem{}
	if local == nil || cloud == nil {
		return out
	}
	if local.Name != cloud.Name {
		out = append(out, DiffItem{Section: "profile", Name: local.Name, Change: "changed", Fields: []string{"name"}, Local: local.Name, Cloud: cloud.Name})
	}
	out = append(out, diffItems("nodes", local.Nodes, cloud.Nodes, func(n model.Node) (string, string) {
		if n.Remark != "" {
			return n.ID, n.Remark
		}
		return n.ID, n.ServerAddr
	})...)
	out = append(out, diffItems("proxies", local.Proxies, cloud.Proxies, func(p model.Proxy) (string, string) { return p.ID, p.Name })...)
	out = append(out, diffItems("visitors", local.Visitors, cloud.Visitors, func(v model.Visitor) (string, string) { return v.ID, v.Name })...)
	return out
}

func diffItems[T any](section string, local, cloud []T, key func(T) (id, name string)) []DiffItem {
	var out []DiffItem
	cloudByID := map[string]T{}
	for _, c := range cloud {
		id, _ := key(c)
		cloudByID[id] = c
	}
	localIDs := map[string]bool{}
	for _, l := range local {
		id, name := key(l)
		localIDs[id] = true
		c, ok := cloudByID[id]
		if !ok {
			out = append(out, DiffItem{Section: section, Name: name, Change: "local_only"})
			continue
		}
		if fields := changedFields(l, c); len(fields) > 0 {
			out = append(out, DiffItem{Section: section, Name: name, Change: "changed", Fields: fields})
		}
	}
	for _, c := range cloud {
		id, name := key(c)
		if !localIDs[id] {
			out = append(out, DiffItem{Section: section, Name: name, Change: "cloud_only"})
		}
	}
	return out
}

// changedFields 按 JSON 字段比较两个结构体，返回不同的字段名（排序后）。
func changedFields(a, b any) []string {
	am, bm := toMap(a), toMap(b)
	var out []string
	for k, av := range am {
		if !reflect.DeepEqual(av, bm[k]) {
			out = append(out, k)
		}
	}
	for k := range bm {
		if _, ok := am[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func toMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

// sameContent 比较两份配置的内容（名称、节点、代理、访客），不看版本号和时间。
func sameContent(a, b model.Profile) bool {
	a.Normalize()
	b.Normalize()
	type content struct {
		Name string
		Data model.ProfileData
	}
	ja, _ := json.Marshal(content{a.Name, a.Data()})
	jb, _ := json.Marshal(content{b.Name, b.Data()})
	return string(ja) == string(jb)
}

// Conflicts 返回未解决的冲突，按发现时间排序。
func (m *Manager) Conflicts() []Conflict {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Conflict, 0, len(m.conflicts))
	for _, c := range m.conflicts {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b Conflict) int { return a.DetectedAt.Compare(b.DetectedAt) })
	return out
}

// ResolveResult 是解决冲突的结果。选"使用云端"且这份配置正在运行时，会按 6.3.5 应用到隧道。
type ResolveResult struct {
	Applied    engine.UpdateMode `json:"applied"`
	ApplyError *apierr.Error     `json:"applyError,omitempty"`
	Stopped    bool              `json:"stopped"` // 云端已删除、选了"使用云端"，正在运行的隧道已停止
}

// Resolve 解决冲突：keep 为 "local" 时用本机的覆盖云端（基于云端当前版本重新推送），
// 为 "cloud" 时用云端的覆盖本机。
func (m *Manager) Resolve(id, keep string) (ResolveResult, error) {
	res := ResolveResult{Applied: engine.UpdateNone}
	if keep != KeepLocal && keep != KeepCloud {
		return res, apperr.New(apperr.InvalidArg, "keep must be local or cloud")
	}
	m.mu.Lock()
	c, ok := m.conflicts[id]
	s := m.cur
	m.mu.Unlock()
	if !ok {
		return res, apperr.New(apperr.ConflictGone, id)
	}

	var err error
	if keep == KeepLocal {
		err = m.profiles.Tx(func(t *store.Txn) error {
			lc := t.Find(id)
			if lc == nil {
				return nil
			}
			switch {
			case c.cloud == nil && lc.Deleted: // 两边都删了
				t.Remove(id)
			case c.cloud == nil: // 云端删了：重新上传（同一个 ID）
				lc.Profile.Revision, lc.BaseRevision, lc.Dirty = 0, 0, true
				lc.Edits++
			default: // 基于云端当前版本覆盖
				lc.Profile.Revision, lc.BaseRevision, lc.Dirty = c.cloud.Revision, c.cloud.Revision, true
				lc.Edits++
			}
			return nil
		})
	} else {
		runID, _, running := m.eng.RunningProfile()
		running = running && runID == id
		if c.cloud == nil {
			if running {
				_ = m.eng.Stop()
				res.Stopped = true
			}
			err = m.profiles.Tx(func(t *store.Txn) error {
				t.Remove(id)
				return nil
			})
		} else {
			cp := *c.cloud
			err = m.profiles.Tx(func(t *store.Txn) error {
				lc := t.Find(id)
				if lc == nil {
					t.Add(store.CachedProfile{Profile: cp, Origin: store.OriginCloud, BaseRevision: cp.Revision, LocalUpdatedAt: time.Now().UTC()})
					return nil
				}
				replaceWithCloud(lc, cp)
				lc.Edits++
				return nil
			})
			if err == nil && running {
				mode, aerr := m.eng.Update(cp)
				res.Applied = mode
				if aerr != nil {
					res.ApplyError, _ = apierr.As(apperr.Normalize(aerr))
				}
			}
		}
	}
	if err != nil {
		return res, apperr.Wrap(apperr.StorageFailed, err)
	}
	m.mu.Lock()
	delete(m.conflicts, id)
	delete(m.pushErrs, id)
	delete(m.remote, id)
	m.mu.Unlock()
	m.logf("sync: conflict on %s resolved: keep %s", id, keep)
	m.fire(EventSyncChanged, Changed{ProfileIDs: []string{id}})
	m.emitStatus()
	if s != nil {
		s.kick("resolve")
	}
	return res, nil
}

// RemoteUpdates 返回还没处理的"运行中的配置被云端修改"提示（只保留仍在运行的那份）。
func (m *Manager) RemoteUpdates() []RemoteUpdate {
	runID, _, running := m.eng.RunningProfile()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RemoteUpdate{}
	for id, r := range m.remote {
		if running && id == runID {
			out = append(out, r)
		} else {
			delete(m.remote, id)
		}
	}
	return out
}

// ApplyRemote 把缓存里（已经是云端的新内容）的配置应用到正在运行的隧道：
// 只改了代理、访客就热更新，改了节点连接参数就重启（PLAN 6.3.5）。
func (m *Manager) ApplyRemote(id string) (engine.UpdateMode, error) {
	m.mu.Lock()
	delete(m.remote, id)
	m.mu.Unlock()
	c, ok := m.profiles.Get(id)
	if !ok {
		return engine.UpdateNone, apperr.New(apperr.ProfileNotFound, id)
	}
	mode, err := m.eng.Update(c.Profile)
	return mode, apperr.Normalize(err)
}

// DismissRemote 忽略提示：隧道继续用旧内容运行，下次启动时用新内容。
func (m *Manager) DismissRemote(id string) {
	m.mu.Lock()
	delete(m.remote, id)
	m.mu.Unlock()
}
