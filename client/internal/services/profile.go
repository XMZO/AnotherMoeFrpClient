package services

import (
	"crypto/rand"
	"encoding/base64"
	"slices"
	"time"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/cloudsync"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// ProfileSummary 是配置列表的一行。
type ProfileSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Origin       string `json:"origin"` // local | cloud
	NodeCount    int    `json:"nodeCount"`
	ProxyCount   int    `json:"proxyCount"`
	VisitorCount int    `json:"visitorCount"`
	Dirty        bool   `json:"dirty"` // 有还没推送到云端的本地修改
	// Sync 是同步标记：local（本地配置）/ synced / pending（等待推送）/ conflict / error。
	Sync      string        `json:"sync"`
	SyncError *apierr.Error `json:"syncError,omitempty"`
	Revision  int64         `json:"revision"` // 本机已知的云端版本，0 表示还没上传
	UpdatedAt time.Time     `json:"updatedAt"`

	// 以下只在订阅（origin = subscription）上有
	ShareKind     string `json:"shareKind,omitempty"` // template / full / p2p
	ShareID       string `json:"shareId,omitempty"`
	OwnerNickname string `json:"ownerNickname,omitempty"`
	Revoked       bool   `json:"revoked,omitempty"`
	Expired       bool   `json:"expired,omitempty"`
	DefaultNodeID string `json:"defaultNodeId,omitempty"`
}

// SaveResult 是保存配置的结果。配置正在运行时会顺带应用（热更新或重启）。
type SaveResult struct {
	Profile    model.Profile     `json:"profile"`
	Applied    engine.UpdateMode `json:"applied"`
	ApplyError *apierr.Error     `json:"applyError,omitempty"` // 已保存，但没能应用到运行中的隧道
}

// ProfileService：配置的增删改查。本地配置和云端配置并存（PLAN 6.6）：云端配置的修改先写进本机缓存，
// 再交给 cloudsync 防抖推送；本地配置只在本机。
type ProfileService struct {
	profiles *store.ProfileStore
	settings *store.SettingsStore
	eng      *engine.Engine
	sync     *cloudsync.Manager
}

func NewProfileService(profiles *store.ProfileStore, settings *store.SettingsStore, eng *engine.Engine, sync *cloudsync.Manager) *ProfileService {
	return &ProfileService{profiles: profiles, settings: settings, eng: eng, sync: sync}
}

// List 返回所有配置的摘要，最近修改的在前。
func (s *ProfileService) List() []ProfileSummary {
	list := s.profiles.List()
	out := make([]ProfileSummary, 0, len(list))
	for _, c := range list {
		st, serr := s.sync.ProfileSync(c)
		sum := ProfileSummary{
			ID: c.Profile.ID, Name: c.Profile.Name, Origin: c.Origin,
			NodeCount: len(c.Profile.Nodes), ProxyCount: len(c.Profile.Proxies), VisitorCount: len(c.Profile.Visitors),
			Dirty: c.Dirty, Sync: st, SyncError: serr, Revision: c.Profile.Revision, UpdatedAt: c.LocalUpdatedAt,
		}
		if c.IsSubscription() && c.Sub != nil {
			m := c.Sub.Summary
			sum.ShareKind, sum.ShareID, sum.OwnerNickname = m.Kind, m.ShareID, m.OwnerNickname
			sum.Revoked, sum.Expired, sum.DefaultNodeID = m.Revoked, m.Expired, c.Sub.DefaultNodeID
		}
		out = append(out, sum)
	}
	return out
}

// Get 返回一份完整配置（含节点 token）。
func (s *ProfileService) Get(id string) (model.Profile, error) {
	c, ok := s.profiles.Get(id)
	if !ok {
		return model.Profile{}, apperr.New(apperr.ProfileNotFound, id)
	}
	return c.Profile, nil
}

// Create 新建一份空配置。origin 为 "cloud" 时属于登录的账号并同步到云端（需要先登录），
// 其余（含空串）为只在本机的本地配置。
func (s *ProfileService) Create(name, origin string) (model.Profile, error) {
	if origin != store.OriginCloud {
		origin = store.OriginLocal
	}
	if err := s.checkLimit(origin); err != nil {
		return model.Profile{}, err
	}
	p := model.Profile{ID: model.NewID(), Name: name}
	p.Normalize()
	if err := validate.Profile(p); err != nil {
		return model.Profile{}, apperr.FromValidate(err)
	}
	c, err := s.profiles.PutNew(p, origin)
	if err != nil {
		return model.Profile{}, apperr.Wrap(apperr.StorageFailed, err)
	}
	if origin == store.OriginCloud {
		s.sync.LocalEdited(p.ID)
	}
	return c.Profile, nil
}

// checkLimit：云端配置受每个用户 50 份的上限（PLAN 5.2），本地配置也按同样的数量限制。
func (s *ProfileService) checkLimit(origin string) error {
	if origin == store.OriginCloud {
		if !s.sync.LoggedIn() {
			return apperr.New(apperr.NotLoggedIn, "login required for cloud profiles")
		}
		if s.sync.CloudCount() >= cloudsync.MaxCloudProfiles {
			return apierr.New(apierr.LimitExceeded).With(apierr.DetailLimit, cloudsync.MaxCloudProfiles)
		}
		return nil
	}
	n := 0
	for _, c := range s.profiles.List() {
		if !c.IsCloud() {
			n++
		}
	}
	if n >= validate.MaxProfilesPerUser {
		return apierr.New(apierr.LimitExceeded).With(apierr.DetailLimit, validate.MaxProfilesPerUser)
	}
	return nil
}

// Upload 把一份本地配置上传到云端：变成云端配置（ID 不变，正在运行也不受影响），随后推送。
func (s *ProfileService) Upload(id string) (ProfileSummary, error) {
	c, ok := s.profiles.Get(id)
	if !ok {
		return ProfileSummary{}, apperr.New(apperr.ProfileNotFound, id)
	}
	if c.IsCloud() {
		return s.summary(id)
	}
	if c.IsSubscription() {
		return ProfileSummary{}, apperr.New(apperr.SubscriptionReadOnly, id)
	}
	if err := s.checkLimit(store.OriginCloud); err != nil {
		return ProfileSummary{}, err
	}
	err := s.profiles.Tx(func(t *store.Txn) error {
		lc := t.Find(id)
		if lc == nil || lc.Deleted {
			return apperr.New(apperr.ProfileNotFound, id)
		}
		lc.Origin, lc.Profile.Revision, lc.BaseRevision, lc.Dirty = store.OriginCloud, 0, 0, true
		lc.Edits++
		return nil
	})
	if err != nil {
		if _, ok := apierr.As(err); ok {
			return ProfileSummary{}, err
		}
		return ProfileSummary{}, apperr.Wrap(apperr.StorageFailed, err)
	}
	s.sync.LocalEdited(id)
	return s.summary(id)
}

func (s *ProfileService) summary(id string) (ProfileSummary, error) {
	for _, p := range s.List() {
		if p.ID == id {
			return p, nil
		}
	}
	return ProfileSummary{}, apperr.New(apperr.ProfileNotFound, id)
}

// Save 保存整份配置：补 ID、给 stcp/xtcp/sudp 代理补 secretKey，完整校验（含重名）后写入缓存。
// 校验不通过时什么都不改，返回 validation_failed（details.fields 指出具体字段）。
// 这份配置正在运行时按 PLAN 6.3.5 应用：只改代理/访客就热更新，改了节点连接参数就重启。
// 云端配置随后在 1.5 秒内没有新的修改就推送到云端。
func (s *ProfileService) Save(p model.Profile) (SaveResult, error) {
	cur, ok := s.profiles.Get(p.ID)
	if !ok {
		return SaveResult{}, apperr.New(apperr.ProfileNotFound, p.ID)
	}
	if cur.IsSubscription() {
		return SaveResult{}, apperr.New(apperr.SubscriptionReadOnly, p.ID)
	}
	p.Normalize()
	d := p.Data()
	d.EnsureIDs()
	p.Nodes, p.Proxies, p.Visitors = d.Nodes, d.Proxies, d.Visitors
	for i := range p.Proxies {
		if model.IsP2PType(p.Proxies[i].Type) && p.Proxies[i].SecretKey == "" {
			p.Proxies[i].SecretKey = GenerateSecret()
		}
	}
	if err := validate.Profile(p); err != nil {
		return SaveResult{}, apperr.FromValidate(err)
	}
	c, err := s.profiles.Put(p)
	if err != nil {
		return SaveResult{}, apperr.Wrap(apperr.StorageFailed, err)
	}
	if c.IsCloud() {
		s.sync.LocalEdited(c.Profile.ID)
	}
	res := SaveResult{Profile: c.Profile, Applied: engine.UpdateNone}
	mode, err := s.eng.Update(c.Profile)
	res.Applied = mode
	if err != nil {
		ae, _ := apierr.As(apperr.Normalize(err))
		res.ApplyError = ae
	}
	return res, nil
}

// Delete 删除配置。正在运行的配置不能删，先停止。云端配置会同步删除云端的那份。
func (s *ProfileService) Delete(id string) error {
	if pid, _, ok := s.eng.RunningProfile(); ok && pid == id {
		return apperr.New(apperr.ProfileInUse, id)
	}
	before, _ := s.profiles.Get(id)
	if before.IsSubscription() {
		return apperr.New(apperr.SubscriptionReadOnly, "use ShareService.Unsubscribe")
	}
	ok, err := s.profiles.Delete(id)
	if err != nil {
		return apperr.Wrap(apperr.StorageFailed, err)
	}
	if !ok {
		return apperr.New(apperr.ProfileNotFound, id)
	}
	if before.IsCloud() {
		s.sync.LocalEdited(id)
	}
	_, _ = s.settings.Update(func(st *store.Settings) error {
		delete(st.LastNodeIDs, id)
		if st.LastProfileID == id {
			st.LastProfileID = ""
		}
		return nil
	})
	return nil
}

// Duplicate 复制一份配置（新 ID、新的子项 ID），名称加"副本"后缀由前端传入。
// 副本总是本地配置，所以也可以用它把云端配置另存一份到本机。
func (s *ProfileService) Duplicate(id, name string) (model.Profile, error) {
	src, ok := s.profiles.Get(id)
	if !ok {
		return model.Profile{}, apperr.New(apperr.ProfileNotFound, id)
	}
	// 订阅不能复制成本地配置：复制出来的副本带着节点 token，分享者撤销后也收不回来
	if src.IsSubscription() {
		return model.Profile{}, apperr.New(apperr.SubscriptionReadOnly, id)
	}
	if err := s.checkLimit(store.OriginLocal); err != nil {
		return model.Profile{}, err
	}
	p := src.Profile
	p.ID, p.Name, p.Revision = model.NewID(), name, 0
	p.Nodes, p.Proxies, p.Visitors = slices.Clone(p.Nodes), slices.Clone(p.Proxies), slices.Clone(p.Visitors)
	for i := range p.Nodes {
		p.Nodes[i].ID = model.NewID()
	}
	for i := range p.Proxies {
		p.Proxies[i].ID = model.NewID()
	}
	for i := range p.Visitors {
		p.Visitors[i].ID = model.NewID()
	}
	if err := validate.Profile(p); err != nil {
		return model.Profile{}, apperr.FromValidate(err)
	}
	c, err := s.profiles.PutNew(p, store.OriginLocal)
	if err != nil {
		return model.Profile{}, apperr.Wrap(apperr.StorageFailed, err)
	}
	return c.Profile, nil
}

// Conflicts 返回未解决的同步冲突（前端启动时和收到 sync:conflict 后调用）。
func (s *ProfileService) Conflicts() []cloudsync.Conflict {
	return s.sync.Conflicts()
}

// ResolveConflict 解决冲突：keep 为 "local"（保留本机，覆盖云端）或 "cloud"（使用云端，覆盖本机）。
func (s *ProfileService) ResolveConflict(id, keep string) (cloudsync.ResolveResult, error) {
	return s.sync.Resolve(id, keep)
}

// RemoteUpdates 返回"正在运行的配置被云端修改"、还没处理的提示。
func (s *ProfileService) RemoteUpdates() []cloudsync.RemoteUpdate {
	return s.sync.RemoteUpdates()
}

// ApplyRemote 把云端的新内容应用到正在运行的隧道（热更新或重启，PLAN 6.3.5）。
func (s *ProfileService) ApplyRemote(id string) (SaveResult, error) {
	c, ok := s.profiles.Get(id)
	if !ok {
		return SaveResult{}, apperr.New(apperr.ProfileNotFound, id)
	}
	res := SaveResult{Profile: c.Profile, Applied: engine.UpdateNone}
	mode, err := s.sync.ApplyRemote(id)
	res.Applied = mode
	if err != nil {
		res.ApplyError, _ = apierr.As(apperr.Normalize(err))
	}
	return res, nil
}

// DismissRemote 暂不应用：隧道继续用旧内容运行，下次启动时用新内容。
func (s *ProfileService) DismissRemote(id string) {
	s.sync.DismissRemote(id)
}

// GenerateSecret 生成 stcp/xtcp/sudp 用的 secretKey：32 字节随机值（PLAN 5.2），base64url 编码。
func (s *ProfileService) GenerateSecret() string { return GenerateSecret() }

func GenerateSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
