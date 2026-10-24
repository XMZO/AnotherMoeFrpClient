// Package cloudsync 管理账号会话和云端同步（PLAN 6.4、6.6）。
//
// 账号：登录、注册、退出、改密码、重置密码。"保持登录"时令牌存进凭据管理器（credstore），
// 启动时用它自动登录。服务端说会话失效（session_replaced、session_expired、unauthorized、
// account_disabled）时下线并通知界面；client_outdated 时停止同步，但本地缓存的配置照样能启动。
//
// 同步：每 60 秒、窗口获得焦点时、登录后立即拉一次 GET /api/v1/state（带 If-None-Match）；
// 本地编辑后 1.5 秒推送（带 baseRevision）；冲突交给用户选"保留本机 / 使用云端"；
// 连不上服务端时显示"离线"，照常编辑和启动，恢复后再推送。
//
// 所有网络请求都在每个会话自己的后台 goroutine 里串行执行，界面调用不会因为同步而阻塞。
package cloudsync

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/credstore"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// 推送给前端的事件名（PLAN 6.4）。
const (
	EventAuthState    = "auth:state"         // AccountState：登录状态变化
	EventAuthExpired  = "auth:expired"       // *apierr.Error：会话失效或被挤下线，附带错误码
	EventSyncStatus   = "sync:status"        // Status：同步状态指示
	EventSyncChanged  = "sync:changed"       // Changed：云端数据有更新，列表需要刷新
	EventSyncConflict = "sync:conflict"      // Conflict：某个配置出现版本冲突
	EventSyncRemote   = "sync:remote-update" // RemoteUpdate：正在运行的配置被云端修改，询问是否应用
	EventSyncError    = "sync:error"         // PushError：某个配置推送失败（不是网络问题）
)

// 同步状态。
const (
	StateOff      = "off"      // 没有登录
	StateSyncing  = "syncing"  // 正在同步
	StateSynced   = "synced"   // 已同步
	StateOffline  = "offline"  // 连不上服务端
	StateOutdated = "outdated" // 客户端版本过低，同步已停止
	StateError    = "error"    // 其他错误（例如服务端 500），稍后重试
)

// 冲突的两种选择（ResolveConflict 的 keep 参数）。
const (
	KeepLocal = "local"
	KeepCloud = "cloud"
)

// DefaultProfileName 是云端没有任何配置时自动创建的配置名（docs/phase1.md 第 1 条）。
const DefaultProfileName = "我的云端配置"

// 默认的时间参数（PLAN 6.2、6.6）。
const (
	DefaultPollInterval = 60 * time.Second
	DefaultOfflineRetry = 15 * time.Second
	DefaultPushDelay    = 1500 * time.Millisecond
	focusMinInterval    = 3 * time.Second
	logoutTimeout       = 5 * time.Second
)

// AccountState 是 auth:state 事件的内容。
type AccountState struct {
	LoggedIn     bool          `json:"loggedIn"`
	User         *api.UserInfo `json:"user,omitempty"` // 离线自动登录时可能只有昵称，第一次同步成功后补全
	KeepLoggedIn bool          `json:"keepLoggedIn"`
	// Ended 是最近一次被动下线的原因（session_replaced、session_expired、account_disabled、unauthorized），
	// 重新登录后清空。
	Ended *apierr.Error `json:"ended,omitempty"`
	// Outdated 是 client_outdated 错误（details 里有 latestVersion、minVersion、downloadUrl）。
	Outdated *apierr.Error `json:"outdated,omitempty"`
	// CacheOwner 是本机缓存的云端配置所属账号的昵称。未登录时界面据此提示"这些是 xxx 的云端配置"。
	CacheOwner string `json:"cacheOwner,omitempty"`
}

// Status 是 sync:status 事件的内容。
type Status struct {
	State      string        `json:"state"`
	LastSyncAt *time.Time    `json:"lastSyncAt,omitempty"`
	Pending    int           `json:"pending"`   // 等待推送的云端配置数量
	Conflicts  int           `json:"conflicts"` // 未解决的冲突数量
	Error      *apierr.Error `json:"error,omitempty"`
}

// Changed 是 sync:changed 事件的内容。
type Changed struct {
	ProfileIDs []string `json:"profileIds"` // 内容被云端改动或删除的配置
}

// RemoteUpdate 是 sync:remote-update 事件的内容。
type RemoteUpdate struct {
	ProfileID string            `json:"profileId"`
	Name      string            `json:"name"`
	Mode      engine.UpdateMode `json:"mode"` // 应用时是热更新（hot）还是重启（restart）
}

// PushError 是 sync:error 事件的内容。
type PushError struct {
	ProfileID string        `json:"profileId"`
	Name      string        `json:"name"`
	Error     *apierr.Error `json:"error"`
	Reverted  bool          `json:"reverted"` // 上传失败，已改回本地配置
}

// Engine 是同步需要用到的引擎接口。
type Engine interface {
	RunningProfile() (profileID, nodeID string, ok bool)
	Update(p model.Profile) (engine.UpdateMode, error)
	PredictUpdate(p model.Profile) engine.UpdateMode
	Stop() error
}

// Options 是 New 的参数。
type Options struct {
	API        *apiclient.Client
	Creds      credstore.Store
	Profiles   *store.ProfileStore
	Engine     Engine
	DeviceName string

	PollInterval time.Duration
	OfflineRetry time.Duration
	PushDelay    time.Duration

	// Logf 记录同步过程（不含令牌、密码、节点 token）。
	Logf func(format string, args ...any)

	// OnOfficial 在官方节点列表变化时调用（退出登录时传 nil）。
	OnOfficial func([]api.OfficialNode)
	// OnImages 在拿到图片面板的图源时调用（图源只给登录的用户；退出登录、会话失效时传 nil）。
	OnImages func([]api.ImageSource)
	// OnSessionChange 在登录（新会话开始）、退出登录、会话被服务端结束时调用：
	// 官方节点的登录票据是按会话发的，换了会话旧的都不能用了（main 用它清空票据池）。
	OnSessionChange func()
}

type pushErr struct {
	edits int64
	err   *apierr.Error
}

// Manager 见包注释。
type Manager struct {
	api         *apiclient.Client
	creds       credstore.Store
	profiles    *store.ProfileStore
	eng         Engine
	device      string
	poll        time.Duration
	retry       time.Duration
	delay       time.Duration
	logf        func(string, ...any)
	onOfficial  func([]api.OfficialNode)
	onImages    func([]api.ImageSource)
	onSession   func()
	imagesStale atomic.Bool // 面板改了客户端配置，下次同步重新拉图源

	mu        sync.Mutex
	emit      func(string, any)
	acct      AccountState
	status    Status
	cur       *session
	conflicts map[string]*Conflict
	remote    map[string]RemoteUpdate
	pushErrs  map[string]pushErr
	pushTimer *time.Timer
}

// session 是一次登录对应的后台同步循环。
type session struct {
	ctx      context.Context
	cancel   context.CancelFunc
	kickCh   chan string // 提前唤醒同步循环，内容是原因（记日志用）
	done     chan struct{}
	lastPull atomic.Int64 // 上次拉取完成的时间（UnixNano）

	// 以下只在循环 goroutine 里读写
	etag        int64 // 上次拉到的 stateVersion；-1 表示不带 If-None-Match（每个会话第一次总是全量拉取）
	wantDefault bool  // 交互登录后第一次拉取：云端没有配置时建一个默认配置
	images      bool  // 这次登录已经拉到过图源
}

func (s *session) kick(reason string) {
	select {
	case s.kickCh <- reason:
	default:
	}
}

// New 创建 Manager。还没有登录；启动时调用 Restore 尝试自动登录。
func New(o Options) *Manager {
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultPollInterval
	}
	if o.OfflineRetry <= 0 {
		o.OfflineRetry = DefaultOfflineRetry
	}
	if o.PushDelay <= 0 {
		o.PushDelay = DefaultPushDelay
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	m := &Manager{
		api: o.API, creds: o.Creds, profiles: o.Profiles, eng: o.Engine, device: apiclient.DeviceName(o.DeviceName),
		poll: o.PollInterval, retry: o.OfflineRetry, delay: o.PushDelay, logf: o.Logf, onOfficial: o.OnOfficial, onImages: o.OnImages,
		onSession: o.OnSessionChange,
		conflicts: map[string]*Conflict{}, remote: map[string]RemoteUpdate{}, pushErrs: map[string]pushErr{},
	}
	m.acct.CacheOwner = o.Profiles.Sync().Nickname
	m.status = Status{State: StateOff}
	return m
}

// SetEmitter 注入事件推送函数（Wails 应用启动后）。
func (m *Manager) SetEmitter(emit func(string, any)) {
	m.mu.Lock()
	m.emit = emit
	m.mu.Unlock()
}

func (m *Manager) fire(name string, data any) {
	m.mu.Lock()
	emit := m.emit
	m.mu.Unlock()
	if emit != nil {
		emit(name, data)
	}
}

// Account 返回当前登录状态。
func (m *Manager) Account() AccountState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acctLocked()
}

func (m *Manager) acctLocked() AccountState {
	a := m.acct
	if a.User != nil {
		u := *a.User
		a.User = &u
	}
	return a
}

// LoggedIn 表示当前有登录的会话（包括离线自动登录、版本过低）。
func (m *Manager) LoggedIn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acct.LoggedIn
}

// Status 返回同步状态。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	s := m.status
	s.Conflicts = len(m.conflicts)
	s.Pending = 0
	for _, c := range m.profiles.All() {
		if c.IsCloud() && c.Dirty {
			s.Pending++
		}
	}
	return s
}

// setState 更新同步状态并推送 sync:status。
func (m *Manager) setState(state string, err error) {
	m.mu.Lock()
	m.status.State = state
	m.status.Error = nil
	if err != nil {
		e, _ := apierr.As(apperr.Normalize(err))
		m.status.Error = e
	}
	if state == StateSynced {
		now := time.Now().UTC()
		m.status.LastSyncAt = &now
	}
	st := m.statusLocked()
	m.mu.Unlock()
	m.fire(EventSyncStatus, st)
}

func (m *Manager) emitStatus() {
	m.fire(EventSyncStatus, m.Status())
}

func (m *Manager) emitAccount() {
	m.fire(EventAuthState, m.Account())
}

// ---- 会话 ----

// Restore 在启动时调用：凭据管理器里有令牌就用它自动登录。网络不通时进入"离线"，之后自动重试。
func (m *Manager) Restore() {
	c, ok, err := m.creds.Load()
	if err != nil {
		m.logf("restore: read credential: %v", err)
		return
	}
	if !ok || c.Token == "" {
		return
	}
	user := &api.UserInfo{Nickname: c.Nickname}
	if sync := m.profiles.Sync(); sync.Nickname == c.Nickname {
		user.ID = sync.Account
	}
	m.logf("restore: auto login as %q", c.Nickname)
	m.start(c.Token, user, true, false)
}

// Login 登录。keep 为 true 时把令牌存进凭据管理器，下次启动自动登录；否则只放在内存里。
func (m *Manager) Login(ctx context.Context, nickname, password string, keep bool) (AccountState, error) {
	res, err := m.api.Login(ctx, api.LoginRequest{Nickname: nickname, Password: password, DeviceName: m.device})
	if err != nil {
		if apiclient.IsOutdated(err) {
			m.mu.Lock()
			m.acct.Outdated, _ = apierr.As(err)
			m.mu.Unlock()
			m.emitAccount()
		}
		return m.Account(), apperr.Normalize(err)
	}
	user := res.User
	m.logf("login ok: %q keep=%v", user.Nickname, keep)
	m.start(res.Token, &user, keep, true)
	return m.Account(), nil
}

// start 开始一个会话：换账号时先清掉旧账号的云端缓存，保存或删除凭据，启动同步循环。
func (m *Manager) start(token string, user *api.UserInfo, keep, interactive bool) {
	m.stopSession()
	if sync := m.profiles.Sync(); user.ID != "" && sync.Account != "" && sync.Account != user.ID {
		m.logf("cache belongs to another account, clearing cloud profiles")
		m.clearCloud()
	}
	if user.ID != "" {
		_ = m.profiles.Tx(func(t *store.Txn) error {
			s := t.Sync()
			s.Account, s.Nickname = user.ID, user.Nickname
			return nil
		})
	}
	if keep {
		if err := m.creds.Save(credstore.Cred{Nickname: user.Nickname, Token: token}); err != nil {
			m.logf("save credential: %v", err)
		}
	} else if err := m.creds.Delete(); err != nil {
		m.logf("delete credential: %v", err)
	}
	m.api.SetToken(token)

	ctx, cancel := context.WithCancel(context.Background())
	s := &session{ctx: ctx, cancel: cancel, kickCh: make(chan string, 1), done: make(chan struct{}), etag: -1, wantDefault: interactive}
	m.sessionChanged()
	m.mu.Lock()
	m.cur = s
	m.acct = AccountState{LoggedIn: true, User: user, KeepLoggedIn: keep, CacheOwner: user.Nickname}
	m.conflicts = map[string]*Conflict{}
	m.pushErrs = map[string]pushErr{}
	m.mu.Unlock()
	m.emitAccount()
	go m.loop(s)
}

// stopSession 停掉当前的同步循环并等它退出（正在进行的请求会被取消）。
func (m *Manager) stopSession() {
	m.mu.Lock()
	s := m.cur
	m.cur = nil
	if m.pushTimer != nil {
		m.pushTimer.Stop()
		m.pushTimer = nil
	}
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(apiclient.DefaultTimeout + time.Second):
	}
}

// current 返回当前会话；s 不是当前会话时返回 false（已退出或换了账号）。
func (m *Manager) isCurrent(s *session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur == s && s.ctx.Err() == nil
}

// endSession 在服务端说会话失效时调用：下线、删除凭据、通知界面。
// 本机缓存的云端配置保留（离线仍可启动）；同一账号重新登录后继续同步，没推送的修改不会丢。
func (m *Manager) endSession(s *session, err error) {
	ae, _ := apierr.As(apperr.Normalize(err))
	m.mu.Lock()
	if m.cur != s {
		m.mu.Unlock()
		return
	}
	m.cur = nil
	s.cancel()
	if m.pushTimer != nil {
		m.pushTimer.Stop()
		m.pushTimer = nil
	}
	owner := m.acct.CacheOwner
	m.acct = AccountState{Ended: ae, CacheOwner: owner}
	m.conflicts = map[string]*Conflict{}
	m.status = Status{State: StateOff}
	m.mu.Unlock()
	m.logf("session ended by server: %s", ae.Code)
	m.clearImages()
	m.sessionChanged()
	if derr := m.creds.Delete(); derr != nil {
		m.logf("delete credential: %v", derr)
	}
	m.api.SetToken("")
	m.emitAccount()
	m.fire(EventAuthExpired, ae)
	m.emitStatus()
}

// Logout 退出登录：尽量把没推送的修改推上去，吊销服务端会话，删除凭据，
// 清掉本机的云端配置缓存（本地配置保留）。正在运行的是云端配置时先停止隧道。
func (m *Manager) Logout(ctx context.Context) error {
	m.mu.Lock()
	loggedIn := m.acct.LoggedIn
	m.mu.Unlock()
	m.stopSession()
	if loggedIn && m.api.Token() != "" {
		c, cancel := context.WithTimeout(ctx, logoutTimeout)
		if err := m.push(c); err != nil {
			m.logf("logout: final push: %v", err)
		}
		if err := m.api.Logout(c); err != nil {
			m.logf("logout: revoke session: %v", err)
		}
		cancel()
	}
	if err := m.creds.Delete(); err != nil {
		m.logf("logout: delete credential: %v", err)
	}
	m.api.SetToken("")
	ids := m.clearCloud()
	m.mu.Lock()
	m.acct = AccountState{}
	m.conflicts = map[string]*Conflict{}
	m.remote = map[string]RemoteUpdate{}
	m.pushErrs = map[string]pushErr{}
	m.status = Status{State: StateOff}
	m.mu.Unlock()
	m.clearImages()
	m.sessionChanged()
	m.logf("logged out, %d cloud profiles removed from cache", len(ids))
	m.emitAccount()
	m.fire(EventSyncChanged, Changed{ProfileIDs: ids})
	m.emitStatus()
	return nil
}

// clearCloud 删除缓存里全部云端配置（含墓碑）和订阅，并重置同步游标，返回删掉的 ID。
// 正在运行的是云端配置时先停止隧道。
func (m *Manager) clearCloud() []string {
	var ids []string
	runID, _, running := m.eng.RunningProfile()
	_ = m.profiles.Tx(func(t *store.Txn) error {
		ids = ids[:0]
		for _, c := range t.Entries() {
			if c.IsCloud() || c.IsSubscription() {
				ids = append(ids, c.Profile.ID)
			}
		}
		for _, id := range ids {
			t.Remove(id)
		}
		*t.Sync() = store.SyncState{}
		return nil
	})
	if m.onOfficial != nil {
		m.onOfficial(nil) // 官方节点属于原来的账号
	}
	if running && slices.Contains(ids, runID) {
		m.logf("stopping tunnel: running profile was a cloud profile")
		_ = m.eng.Stop()
	}
	return ids
}

// Register 注册。成功后不自动登录（服务端的行为），界面切到登录页。
func (m *Manager) Register(ctx context.Context, nickname, password, invite string) (api.UserInfo, error) {
	res, err := m.api.Register(ctx, api.RegisterRequest{Nickname: nickname, Password: password, InviteCode: invite})
	if err != nil {
		return api.UserInfo{}, apperr.Normalize(err)
	}
	return res.User, nil
}

// ResetPassword 用管理员给的重置码设置新密码。成功后该账号的所有会话都会被吊销。
func (m *Manager) ResetPassword(ctx context.Context, nickname, code, newPassword string) error {
	return apperr.Normalize(m.api.ResetPassword(ctx, api.ResetPasswordRequest{Nickname: nickname, Code: code, NewPassword: newPassword}))
}

// ChangePassword 修改密码。旧密码错误返回 invalid_credentials（不会下线）；
// 成功后服务端吊销该账号在其他设备上的会话，本机保持登录。
func (m *Manager) ChangePassword(ctx context.Context, oldPassword, newPassword string) error {
	m.mu.Lock()
	s := m.cur
	m.mu.Unlock()
	if s == nil {
		return apperr.New(apperr.NotLoggedIn, "not logged in")
	}
	err := m.api.ChangePassword(ctx, api.ChangePasswordRequest{OldPassword: oldPassword, NewPassword: newPassword})
	if err != nil && apiclient.SessionEnded(err) {
		m.endSession(s, err)
	}
	return apperr.Normalize(err)
}

// Me 刷新并返回当前用户。离线时返回缓存的信息和 server_unreachable。
func (m *Manager) Me(ctx context.Context) (AccountState, error) {
	m.mu.Lock()
	s := m.cur
	m.mu.Unlock()
	if s == nil {
		return m.Account(), apperr.New(apperr.NotLoggedIn, "not logged in")
	}
	res, err := m.api.Me(ctx)
	switch {
	case err == nil:
		m.updateUser(res.User)
	case apiclient.SessionEnded(err):
		m.endSession(s, err)
	case apiclient.IsOutdated(err):
		m.setOutdated(err)
	}
	return m.Account(), apperr.Normalize(err)
}

func (m *Manager) updateUser(u api.UserInfo) {
	m.mu.Lock()
	changed := m.acct.User == nil || *m.acct.User != u
	if m.acct.LoggedIn {
		m.acct.User = &u
		m.acct.CacheOwner = u.Nickname
	}
	m.mu.Unlock()
	if changed {
		m.emitAccount()
	}
}

func (m *Manager) setOutdated(err error) {
	ae, _ := apierr.As(err)
	m.mu.Lock()
	m.acct.Outdated = ae
	m.mu.Unlock()
	m.emitAccount()
}

// SyncNow 立即同步一次（"立即同步"按钮、网络恢复）。
func (m *Manager) SyncNow() {
	m.mu.Lock()
	s := m.cur
	m.mu.Unlock()
	if s != nil {
		s.kick("manual")
	}
}

// RefreshImages 在面板改了客户端配置（实时通道推送）时调用：下次同步重新拉图源，并立即同步一次。
func (m *Manager) RefreshImages() {
	m.imagesStale.Store(true)
	m.SyncNow()
}

func (m *Manager) sessionChanged() {
	if m.onSession != nil {
		m.onSession()
	}
}

func (m *Manager) clearImages() {
	if m.onImages != nil {
		m.onImages(nil)
	}
}

// Focus 在窗口获得焦点时调用：距离上次拉取超过 3 秒就立即拉一次（PLAN 6.6）。
func (m *Manager) Focus() {
	m.mu.Lock()
	s := m.cur
	m.mu.Unlock()
	if s != nil && time.Since(time.Unix(0, s.lastPull.Load())) > focusMinInterval {
		s.kick("focus")
	}
}

// LocalEdited 在本地修改（保存、删除、上传）一份配置后调用：云端配置在 1.5 秒内没有新的修改就推送。
func (m *Manager) LocalEdited(id string) {
	m.mu.Lock()
	delete(m.remote, id)
	s := m.cur
	if s != nil {
		if m.pushTimer != nil {
			m.pushTimer.Stop()
		}
		m.pushTimer = time.AfterFunc(m.delay, func() { s.kick("edit") })
	}
	m.mu.Unlock()
	m.emitStatus()
}

// Close 停止后台循环（退出程序时）。不删除凭据，下次启动仍会自动登录。
func (m *Manager) Close() {
	m.stopSession()
}

// errStopped 表示会话已经结束（退出登录、换账号），本次同步的结果作废。
var errStopped = errors.New("cloudsync: session stopped")
