package services

import (
	"context"
	"strings"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/cloudsync"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// ShareService：分享与订阅（PLAN 6.4、7.6）。都需要登录，并且直接请求服务端（不走离线缓存）。
//
// 订阅成功后立即拉取 runtime 存进缓存，订阅就会作为一条只读条目出现在配置列表里，
// 和普通配置一样启动；之后的变化由 cloudsync 的定时同步处理。
type ShareService struct {
	sync     *cloudsync.Manager
	profiles *store.ProfileStore
}

func NewShareService(sync *cloudsync.Manager, profiles *store.ProfileStore) *ShareService {
	return &ShareService{sync: sync, profiles: profiles}
}

func (s *ShareService) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
}

func (s *ShareService) check() error {
	if !s.sync.LoggedIn() {
		return apperr.New(apperr.NotLoggedIn, "login required")
	}
	return nil
}

// fail 处理会话失效、版本过低，并把错误规整成前端能识别的格式。
func (s *ShareService) fail(err error) error {
	s.sync.CheckError(err)
	return apperr.Normalize(err)
}

// ListMine 返回自己创建的分享（含已撤销的）。
func (s *ShareService) ListMine() ([]api.Share, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	list, err := s.sync.API().ListShares(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	return list, nil
}

// syncedCloudProfile 检查配置是已同步到云端的云端配置：服务端按云端那一份生成快照，
// 本机还有没推送的修改时先等同步完成，免得分享出去的内容和界面上看到的不一样。
func (s *ShareService) syncedCloudProfile(id string) error {
	c, ok := s.profiles.Get(id)
	switch {
	case !ok:
		return apperr.New(apperr.ProfileNotFound, id)
	case !c.IsCloud():
		return apperr.New(apperr.ShareNeedsCloud, id)
	case c.Dirty || c.Profile.Revision == 0:
		s.sync.SyncNow()
		return apperr.New(apperr.ShareNotSynced, id)
	}
	return nil
}

// Create 从一份云端配置创建分享。
func (s *ShareService) Create(req api.CreateShareRequest) (api.Share, error) {
	if err := s.check(); err != nil {
		return api.Share{}, err
	}
	if err := s.syncedCloudProfile(req.ProfileID); err != nil {
		return api.Share{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	sh, err := s.sync.API().CreateShare(ctx, req)
	if err != nil {
		return api.Share{}, s.fail(err)
	}
	return sh, nil
}

// Update 修改名称、过期时间、人数上限。
func (s *ShareService) Update(id string, req api.UpdateShareRequest) (api.Share, error) {
	if err := s.check(); err != nil {
		return api.Share{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	sh, err := s.sync.API().UpdateShare(ctx, id, req)
	if err != nil {
		return api.Share{}, s.fail(err)
	}
	return sh, nil
}

// Republish 用来源配置在云端的当前内容更新分享快照。sourceProfileID 是分享的来源配置，
// 本机还有没推送的修改时先等同步完成。
func (s *ShareService) Republish(id, sourceProfileID string) (api.Share, error) {
	if err := s.check(); err != nil {
		return api.Share{}, err
	}
	if sourceProfileID != "" {
		if c, ok := s.profiles.Get(sourceProfileID); ok && c.IsCloud() && (c.Dirty || c.Profile.Revision == 0) {
			s.sync.SyncNow()
			return api.Share{}, apperr.New(apperr.ShareNotSynced, sourceProfileID)
		}
	}
	ctx, cancel := s.ctx()
	defer cancel()
	sh, err := s.sync.API().RepublishShare(ctx, id)
	if err != nil {
		return api.Share{}, s.fail(err)
	}
	return sh, nil
}

// Revoke 撤销分享。订阅者在下一次同步时停止隧道并看到"已撤销"。
func (s *ShareService) Revoke(id string) error {
	if err := s.check(); err != nil {
		return err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.sync.API().RevokeShare(ctx, id); err != nil {
		return s.fail(err)
	}
	s.sync.SyncNow()
	return nil
}

// Preview 订阅前预览（不含任何机密）。shareID 前后的空白会被去掉。
func (s *ShareService) Preview(shareID string) (api.SharePreview, error) {
	if err := s.check(); err != nil {
		return api.SharePreview{}, err
	}
	shareID = strings.TrimSpace(shareID)
	if shareID == "" {
		return api.SharePreview{}, apperr.New(apperr.InvalidArg, "empty share id")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	pv, err := s.sync.API().SharePreview(ctx, shareID)
	if err != nil {
		return api.SharePreview{}, s.fail(err)
	}
	return pv, nil
}

// Subscribe 订阅分享，成功后立即拉取 runtime 存进缓存。返回订阅 ID（也是配置列表里条目的 ID）。
func (s *ShareService) Subscribe(shareID string, params model.SubscriptionParams) (api.SubscriptionSummary, error) {
	if err := s.check(); err != nil {
		return api.SubscriptionSummary{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	sum, err := s.sync.API().Subscribe(ctx, api.CreateSubscriptionRequest{ShareID: strings.TrimSpace(shareID), Params: params})
	if err != nil {
		return api.SubscriptionSummary{}, s.fail(err)
	}
	if err := s.sync.ApplySubscription(ctx, sum); err != nil {
		// 订阅已经成功，只是 runtime 还没拉下来：下一轮同步会补上
		s.sync.SyncNow()
	}
	return sum, nil
}

// Subscription 返回缓存里的订阅摘要（编辑订阅参数时用）。
func (s *ShareService) Subscription(id string) (api.SubscriptionSummary, error) {
	c, ok := s.profiles.Get(id)
	if !ok || !c.IsSubscription() || c.Sub == nil {
		return api.SubscriptionSummary{}, apperr.New(apperr.ProfileNotFound, id)
	}
	return c.Sub.Summary, nil
}

// SubscriptionSaveResult 是修改订阅参数的结果。订阅正在运行时会顺带应用（热更新或重启）。
type SubscriptionSaveResult struct {
	Summary    api.SubscriptionSummary `json:"summary"`
	Applied    engine.UpdateMode       `json:"applied"`
	ApplyError *apierr.Error           `json:"applyError,omitempty"`
}

// UpdateSubscription 修改订阅参数（模板：节点和自己的代理；P2P：本地绑定）。
// 这是用户自己的修改，订阅正在运行时直接应用，不再询问。
func (s *ShareService) UpdateSubscription(id string, params model.SubscriptionParams) (SubscriptionSaveResult, error) {
	res := SubscriptionSaveResult{Applied: engine.UpdateNone}
	if err := s.check(); err != nil {
		return res, err
	}
	cur, err := s.Subscription(id)
	if err != nil {
		return res, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	sum, err := s.sync.API().UpdateSubscription(ctx, id, api.UpdateSubscriptionRequest{BaseRevision: cur.Revision, Params: params})
	if err != nil {
		if apierr.CodeOf(err) == apierr.RevisionConflict {
			s.sync.SyncNow() // 别的设备改过：拉最新的，让用户在新版本上再改
		}
		return res, s.fail(err)
	}
	res.Summary = sum
	if err := s.sync.ApplySubscription(ctx, sum); err != nil {
		s.sync.SyncNow()
		return res, nil
	}
	for _, r := range s.sync.RemoteUpdates() {
		if r.ProfileID == id {
			mode, err := s.sync.ApplyRemote(id)
			res.Applied = mode
			if err != nil {
				res.ApplyError, _ = apierr.As(apperr.Normalize(err))
			}
		}
	}
	return res, nil
}

// Unsubscribe 取消订阅（分享已撤销的也可以）。正在运行就先停止隧道。
func (s *ShareService) Unsubscribe(id string) error {
	if err := s.check(); err != nil {
		return err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.sync.API().Unsubscribe(ctx, id); err != nil && apierr.CodeOf(err) != apierr.NotFound {
		return s.fail(err)
	}
	s.sync.RemoveSubscription(id)
	s.sync.NotifyChanged(id)
	return nil
}
