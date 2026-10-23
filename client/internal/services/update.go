package services

import (
	"context"
	"sync"
	"time"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// 更新检查（PLAN 6.12）：启动时和之后每 6 小时调用一次 GET /api/v1/client/config。
const (
	EventUpdateAvailable = "update:available" // UpdateInfo：有新版本（同一个版本只推一次）
	EventAnnouncement    = "app:announcement" // string：服务端公告（内容变化时推送）
	UpdateCheckInterval  = 6 * time.Hour
)

// UpdateInfo 是检查更新的结果。
type UpdateInfo struct {
	Current      string        `json:"current"`
	Latest       string        `json:"latest"`
	MinVersion   string        `json:"minVersion"`
	Available    bool          `json:"available"` // 有比当前更新的版本
	Required     bool          `json:"required"`  // 当前版本低于最低版本（账号和同步已停止，本地配置仍可启动）
	DownloadURL  string        `json:"downloadUrl"`
	SHA256       string        `json:"sha256"`
	ReleaseNotes string        `json:"releaseNotes"`
	Announcement string        `json:"announcement"`
	CheckedAt    *time.Time    `json:"checkedAt,omitempty"`
	Error        *apierr.Error `json:"error,omitempty"`
}

// Updater 定期拉取客户端配置：版本信息、公告、图源。
type Updater struct {
	api     *apiclient.Client
	version string

	mu        sync.Mutex
	info      UpdateInfo
	emit      Emitter
	notified  string // 已经推送过的最新版本
	announced string
	onConfig  []func(api.ClientConfigResponse)
}

func NewUpdater(c *apiclient.Client, version string) *Updater {
	return &Updater{api: c, version: version, info: UpdateInfo{Current: version}}
}

// SetEmitter 注入事件推送函数。
func (u *Updater) SetEmitter(emit Emitter) {
	u.mu.Lock()
	u.emit = emit
	u.mu.Unlock()
}

// OnConfig 注册拿到客户端配置后的回调（图片面板用它更新图源）。
func (u *Updater) OnConfig(fn func(api.ClientConfigResponse)) {
	u.mu.Lock()
	u.onConfig = append(u.onConfig, fn)
	u.mu.Unlock()
}

// Info 返回最近一次检查的结果。
func (u *Updater) Info() UpdateInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.info
}

// Check 立即检查一次。
func (u *Updater) Check(ctx context.Context) (UpdateInfo, error) {
	cfg, err := u.api.ClientConfig(ctx)
	now := time.Now().UTC()
	u.mu.Lock()
	if err != nil {
		u.info.Error, _ = apierr.As(apperr.Normalize(err))
		info := u.info
		u.mu.Unlock()
		return info, apperr.Normalize(err)
	}
	info := evaluate(u.version, cfg)
	info.CheckedAt = &now
	u.info = info
	emit, hooks := u.emit, append([]func(api.ClientConfigResponse){}, u.onConfig...)
	notify := info.Available && info.Latest != u.notified
	if notify {
		u.notified = info.Latest
	}
	announce := info.Announcement != "" && info.Announcement != u.announced
	if announce {
		u.announced = info.Announcement
	}
	u.mu.Unlock()

	for _, fn := range hooks {
		fn(cfg)
	}
	if emit != nil {
		if notify {
			emit(EventUpdateAvailable, info)
		}
		if announce {
			emit(EventAnnouncement, info.Announcement)
		}
	}
	return info, nil
}

// Run 启动后立即检查，之后每 6 小时一次，直到 stop 关闭。失败时 15 分钟后重试。
func (u *Updater) Run(stop <-chan struct{}) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
		_, err := u.Check(ctx)
		cancel()
		wait := UpdateCheckInterval
		if err != nil {
			wait = 15 * time.Minute
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// evaluate 比较版本。开发版（版本号解析不了，例如 dev）不提示更新。
func evaluate(current string, cfg api.ClientConfigResponse) UpdateInfo {
	info := UpdateInfo{
		Current: current, Latest: cfg.LatestVersion, MinVersion: cfg.MinVersion,
		DownloadURL: cfg.DownloadURL, SHA256: cfg.SHA256, ReleaseNotes: cfg.ReleaseNotes, Announcement: cfg.Announcement,
	}
	cur, err := api.ParseVersion(current)
	if err != nil {
		return info
	}
	if latest, err := api.ParseVersion(cfg.LatestVersion); err == nil && latest.Compare(cur) > 0 {
		info.Available = true
	}
	if minV, err := api.ParseVersion(cfg.MinVersion); err == nil && cur.Compare(minV) < 0 {
		info.Required = true
	}
	return info
}
