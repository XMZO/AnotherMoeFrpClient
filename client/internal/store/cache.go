package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

	"moefrp/shared/api"
	"moefrp/shared/model"
)

// 配置的来源。
const (
	OriginLocal = "local" // 只在本机，不同步
	OriginCloud = "cloud" // 属于登录的账号，和云端双向同步（Revision 为 0 表示还没上传成功）
	// OriginSubscription 是订阅的分享（PLAN 7.6）：Profile 是服务端 runtime 组装好的可运行内容，
	// 只读；Profile.ID 是订阅 ID。分享被撤销或过期时内容会被清空。
	OriginSubscription = "subscription"
)

const cacheSchema = 1

// cacheMagic 是 cache.bin 的文件头，后面紧跟 DPAPI 密文。
var cacheMagic = []byte("MFC1")

// CachedProfile 是缓存里的一份配置，带上同步需要的元数据（PLAN 6.6）：
//   - Profile.Revision 是本机已知的云端版本，0 表示从未上传；
//   - BaseRevision 是本地修改所基于的云端版本，推送时作为 baseRevision；
//   - Dirty 表示有还没推送的本地修改；
//   - Deleted 是本地已删除、等待同步到云端的墓碑（从未上传过的配置直接删掉，不留墓碑）；
//   - Edits 每次本地修改加 1，推送期间用户又改了的话，推送成功后仍保持 Dirty。
type CachedProfile struct {
	Profile        model.Profile `json:"profile"`
	Origin         string        `json:"origin"`
	BaseRevision   int64         `json:"baseRevision"`
	Dirty          bool          `json:"dirty"`
	Deleted        bool          `json:"deleted,omitempty"`
	Edits          int64         `json:"edits,omitempty"`
	LocalUpdatedAt time.Time     `json:"localUpdatedAt"`
	// Sub 只在订阅条目上有：服务端的订阅摘要（不含机密），以及缓存的 runtime 对应的版本。
	Sub *SubscriptionMeta `json:"sub,omitempty"`
}

// SubscriptionMeta 是订阅条目的元数据。
type SubscriptionMeta struct {
	Summary api.SubscriptionSummary `json:"summary"`
	// RuntimeShareRevision、RuntimeRevision 是缓存的 runtime 对应的分享版本和订阅版本，
	// 与摘要里的不一致时重新拉取 runtime。
	RuntimeShareRevision int64  `json:"runtimeShareRevision"`
	RuntimeRevision      int64  `json:"runtimeRevision"`
	DefaultNodeID        string `json:"defaultNodeId,omitempty"`
}

// IsCloud 表示这份配置属于账号、参与同步。
func (c CachedProfile) IsCloud() bool { return c.Origin == OriginCloud }

// IsSubscription 表示这是订阅的分享（只读）。
func (c CachedProfile) IsSubscription() bool { return c.Origin == OriginSubscription }

// SyncState 是云同步的游标。
type SyncState struct {
	Account      string     `json:"account,omitempty"`  // 缓存里的云端配置属于哪个账号（用户 ID）
	Nickname     string     `json:"nickname,omitempty"` // 该账号的昵称，离线时显示用
	StateVersion int64      `json:"stateVersion"`       // 最近一次拉到的 stateVersion
	LastSyncAt   *time.Time `json:"lastSyncAt,omitempty"`
	// Official 是面板下发的官方节点（含节点凭证，所以放在加密的缓存里），离线时也能用。
	Official     []api.OfficialNode `json:"official,omitempty"`
	OfficialETag string             `json:"officialETag,omitempty"`
}

type cacheFile struct {
	SchemaVersion int             `json:"schemaVersion"`
	Sync          SyncState       `json:"sync"`
	Profiles      []CachedProfile `json:"profiles"`
}

// ProfileStore 是 %APPDATA%\MoeFrp\cache.bin：所有配置（含节点 token），整体用 DPAPI（当前用户）加密。
// 每次修改都整体重写文件，配置数量有上限（每个用户 ≤ 50），文件很小。
type ProfileStore struct {
	path string
	mu   sync.Mutex
	data cacheFile
}

// OpenProfiles 读取缓存。文件不存在时为空；解不开（换了用户或机器）或损坏时，
// 改名备份后从空缓存开始，warning 说明发生了什么（PLAN 6.5：读不出来就重新从云端拉）。
func OpenProfiles(path string) (st *ProfileStore, warning string, err error) {
	st = &ProfileStore{path: path, data: cacheFile{SchemaVersion: cacheSchema}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, "", nil
	case err != nil:
		return nil, "", err
	}
	data, derr := decodeCache(raw)
	if derr != nil {
		bad := quarantine(path)
		return st, fmt.Sprintf("cache.bin cannot be read (%v); starting with an empty cache, old file kept as %s", derr, bad), nil
	}
	st.data = data
	return st, "", nil
}

func decodeCache(raw []byte) (cacheFile, error) {
	var data cacheFile
	if !bytes.HasPrefix(raw, cacheMagic) {
		return data, errors.New("unknown file format")
	}
	plain, err := unprotect(raw[len(cacheMagic):])
	if err != nil {
		return data, fmt.Errorf("dpapi: %w", err)
	}
	if err := json.Unmarshal(plain, &data); err != nil {
		return data, err
	}
	if data.SchemaVersion > cacheSchema {
		return data, fmt.Errorf("written by a newer client (schema %d)", data.SchemaVersion)
	}
	data.SchemaVersion = cacheSchema
	for i := range data.Profiles {
		data.Profiles[i].Profile.Normalize()
	}
	return data, nil
}

func (s *ProfileStore) saveLocked() error {
	plain, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	cipher, err := protect(plain)
	if err != nil {
		return fmt.Errorf("dpapi: %w", err)
	}
	return writeFileAtomic(s.path, append(slices.Clone(cacheMagic), cipher...))
}

// List 返回未删除的配置，按本地修改时间倒序。
func (s *ProfileStore) List() []CachedProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CachedProfile, 0, len(s.data.Profiles))
	for _, p := range s.data.Profiles {
		if !p.Deleted {
			out = append(out, cloneCached(p))
		}
	}
	slices.SortStableFunc(out, func(a, b CachedProfile) int { return b.LocalUpdatedAt.Compare(a.LocalUpdatedAt) })
	return out
}

// Get 按 ID 取一份未删除的配置。
func (s *ProfileStore) Get(id string) (CachedProfile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.indexLocked(id); i >= 0 && !s.data.Profiles[i].Deleted {
		return cloneCached(s.data.Profiles[i]), true
	}
	return CachedProfile{}, false
}

// Put 新建或覆盖一份本地编辑的配置，标记为 Dirty。Revision、来源等同步字段保持原样。
func (s *ProfileStore) Put(p model.Profile) (CachedProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Normalize()
	now := time.Now().UTC()
	i := s.indexLocked(p.ID)
	prev := slices.Clone(s.data.Profiles)
	if i < 0 {
		p.Revision = 0
		s.data.Profiles = append(s.data.Profiles, CachedProfile{Origin: OriginLocal})
		i = len(s.data.Profiles) - 1
	} else {
		p.Revision = s.data.Profiles[i].Profile.Revision // 本地编辑不改云端版本
	}
	p.UpdatedAt = now
	c := &s.data.Profiles[i]
	c.Profile, c.Dirty, c.Deleted, c.LocalUpdatedAt = p, true, false, now
	c.Edits++
	if err := s.saveLocked(); err != nil {
		s.data.Profiles = prev
		return CachedProfile{}, err
	}
	return cloneCached(*c), nil
}

// PutNew 新建一份配置，来源为 origin（local / cloud），标记为 Dirty。ID 已存在时返回错误。
func (s *ProfileStore) PutNew(p model.Profile, origin string) (CachedProfile, error) {
	var out CachedProfile
	err := s.Tx(func(t *Txn) error {
		if t.Find(p.ID) != nil {
			return fmt.Errorf("profile %s already exists", p.ID)
		}
		p.Normalize()
		now := time.Now().UTC()
		p.Revision, p.UpdatedAt = 0, now
		t.Add(CachedProfile{Profile: p, Origin: origin, Dirty: true, Edits: 1, LocalUpdatedAt: now})
		out = cloneCached(*t.Find(p.ID))
		return nil
	})
	return out, err
}

// Delete 删除一份配置：从未上传过的直接删掉，上传过的留墓碑，等同步时删除云端。
func (s *ProfileStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 || s.data.Profiles[i].Deleted {
		return false, nil
	}
	prev := slices.Clone(s.data.Profiles)
	if s.data.Profiles[i].Profile.Revision == 0 {
		s.data.Profiles = slices.Delete(s.data.Profiles, i, i+1)
	} else {
		c := &s.data.Profiles[i]
		c.Deleted, c.Dirty, c.LocalUpdatedAt = true, true, time.Now().UTC()
		c.Edits++
	}
	if err := s.saveLocked(); err != nil {
		s.data.Profiles = prev
		return false, err
	}
	return true, nil
}

// Count 返回未删除的配置数量。
func (s *ProfileStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.data.Profiles {
		if !p.Deleted {
			n++
		}
	}
	return n
}

// Sync 返回同步游标。
func (s *ProfileStore) Sync() SyncState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Sync
}

// All 返回全部条目（含墓碑），顺序同文件。
func (s *ProfileStore) All() []CachedProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CachedProfile, 0, len(s.data.Profiles))
	for _, p := range s.data.Profiles {
		out = append(out, cloneCached(p))
	}
	return out
}

// Txn 是 Tx 回调里对缓存的可写视图。回调返回前不要把指针带出去。
type Txn struct{ d *cacheFile }

// Find 按 ID 找条目（含墓碑），没有时返回 nil。
func (t *Txn) Find(id string) *CachedProfile {
	for i := range t.d.Profiles {
		if t.d.Profiles[i].Profile.ID == id {
			return &t.d.Profiles[i]
		}
	}
	return nil
}

// Entries 返回全部条目的指针（含墓碑）。在回调里增删条目后要重新获取。
func (t *Txn) Entries() []*CachedProfile {
	out := make([]*CachedProfile, len(t.d.Profiles))
	for i := range t.d.Profiles {
		out[i] = &t.d.Profiles[i]
	}
	return out
}

// Add 追加一个条目。
func (t *Txn) Add(c CachedProfile) {
	c.Profile.Normalize()
	t.d.Profiles = append(t.d.Profiles, c)
}

// Remove 彻底删除一个条目（不留墓碑）。
func (t *Txn) Remove(id string) bool {
	n := len(t.d.Profiles)
	t.d.Profiles = slices.DeleteFunc(t.d.Profiles, func(p CachedProfile) bool { return p.Profile.ID == id })
	return len(t.d.Profiles) != n
}

// Sync 返回可修改的同步游标。
func (t *Txn) Sync() *SyncState { return &t.d.Sync }

// Tx 在锁内修改缓存，有变化时整体写盘。fn 返回错误或写盘失败时恢复原样，返回该错误。
func (s *ProfileStore) Tx(fn func(t *Txn) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	restore := func() {
		var d cacheFile
		if json.Unmarshal(before, &d) == nil {
			s.data = d
		}
	}
	if err := fn(&Txn{d: &s.data}); err != nil {
		restore()
		return err
	}
	after, err := json.Marshal(s.data)
	if err != nil {
		restore()
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	if err := s.saveLocked(); err != nil {
		restore()
		return err
	}
	return nil
}

func (s *ProfileStore) indexLocked(id string) int {
	return slices.IndexFunc(s.data.Profiles, func(p CachedProfile) bool { return p.Profile.ID == id })
}

func cloneCached(c CachedProfile) CachedProfile {
	p := c.Profile
	p.Nodes = slices.Clone(p.Nodes)
	p.Proxies = slices.Clone(p.Proxies)
	p.Visitors = slices.Clone(p.Visitors)
	for i := range p.Proxies {
		p.Proxies[i].CustomDomains = slices.Clone(p.Proxies[i].CustomDomains)
		p.Proxies[i].AllowUsers = slices.Clone(p.Proxies[i].AllowUsers)
	}
	for i := range p.Nodes {
		if t := p.Nodes[i].TLS; t != nil {
			v := *t
			p.Nodes[i].TLS = &v
		}
	}
	c.Profile = p
	if c.Sub != nil {
		m := *c.Sub
		c.Sub = &m
	}
	return c
}
