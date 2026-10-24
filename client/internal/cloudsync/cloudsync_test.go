package cloudsync

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
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

type fakeEngine struct {
	mu      sync.Mutex
	running string
	updates []model.Profile
	stopped int
}

func (e *fakeEngine) RunningProfile() (string, string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running, "n1", e.running != ""
}

func (e *fakeEngine) Update(p model.Profile) (engine.UpdateMode, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.ID != e.running {
		return engine.UpdateNone, nil
	}
	e.updates = append(e.updates, p)
	return engine.UpdateHot, nil
}

func (e *fakeEngine) PredictUpdate(p model.Profile) engine.UpdateMode {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.ID != e.running {
		return engine.UpdateNone
	}
	return engine.UpdateHot
}

func (e *fakeEngine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running = ""
	e.stopped++
	return nil
}

type events struct {
	mu   sync.Mutex
	list []string
	data []any
}

func (ev *events) emit(name string, data any) {
	ev.mu.Lock()
	ev.list = append(ev.list, name)
	ev.data = append(ev.data, data)
	ev.mu.Unlock()
}

func (ev *events) last(name string) (any, bool) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	for i := len(ev.list) - 1; i >= 0; i-- {
		if ev.list[i] == name {
			return ev.data[i], true
		}
	}
	return nil, false
}

type device struct {
	m        *Manager
	offMu    sync.Mutex
	official [][]api.OfficialNode // 每次 OnOfficial 回调收到的列表
	ps       *store.ProfileStore
	creds    *credstore.Memory
	eng      *fakeEngine
	ev       *events
	dir      string
}

func newDevice(t *testing.T, srv *fakeServer, dir string) *device {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	ps, _, err := store.OpenProfiles(filepath.Join(dir, "cache.bin"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := apiclient.New(srv.URL, "3.0.0", model.NewID(), apiclient.ProxyNone)
	if err != nil {
		t.Fatal(err)
	}
	d := &device{ps: ps, creds: &credstore.Memory{}, eng: &fakeEngine{}, ev: &events{}, dir: dir}
	d.m = New(Options{API: c, Creds: d.creds, Profiles: ps, Engine: d.eng, DeviceName: "test",
		PollInterval: time.Hour, OfflineRetry: 50 * time.Millisecond, PushDelay: 30 * time.Millisecond,
		Logf: t.Logf, OnOfficial: func(n []api.OfficialNode) {
			d.offMu.Lock()
			d.official = append(d.official, n)
			d.offMu.Unlock()
		}})
	d.m.SetEmitter(d.ev.emit)
	t.Cleanup(d.m.Close)
	return d
}

func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func (d *device) login(t *testing.T, keep bool) {
	t.Helper()
	if _, err := d.m.Login(context.Background(), "alice", "correct-horse", keep); err != nil {
		t.Fatal(err)
	}
	d.waitSynced(t)
}

func (d *device) waitSynced(t *testing.T) {
	t.Helper()
	waitFor(t, "synced", func() bool { s := d.m.Status(); return s.State == StateSynced && s.Pending == 0 })
}

// syncNow 触发一次同步并等它完成（等到同步时间晚于触发时间）。
func (d *device) syncNow(t *testing.T) {
	t.Helper()
	before := time.Now()
	d.m.SyncNow()
	waitFor(t, "sync after kick", func() bool {
		s := d.m.Status()
		return s.State != StateSyncing && s.LastSyncAt != nil && s.LastSyncAt.After(before)
	})
}

func (d *device) cloudProfiles() []store.CachedProfile {
	var out []store.CachedProfile
	for _, c := range d.ps.List() {
		if c.IsCloud() {
			out = append(out, c)
		}
	}
	return out
}

func TestLoginCreatesDefaultProfileAndSendsHeaders(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	if _, err := a.m.Login(context.Background(), "alice", "wrong", true); apierr.CodeOf(err) != apierr.InvalidCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	if a.m.LoggedIn() {
		t.Fatal("must not be logged in after invalid_credentials")
	}
	a.login(t, true)
	waitFor(t, "default profile uploaded", func() bool { return srv.count() == 1 })
	a.waitSynced(t)
	cp := a.cloudProfiles()
	if len(cp) != 1 || cp[0].Profile.Name != DefaultProfileName || cp[0].Profile.Revision != 1 || cp[0].Dirty {
		t.Fatalf("default profile: %+v", cp)
	}
	if c, ok, _ := a.creds.Load(); !ok || c.Token == "" || c.Nickname != "alice" {
		t.Fatalf("keep logged in should store the token: %+v", c)
	}
	srv.mu.Lock()
	for _, h := range srv.headers {
		if h.Get(api.HeaderClientVersion) != "3.0.0" || h.Get(api.HeaderInstallID) == "" {
			t.Fatalf("missing client headers: %v", h)
		}
	}
	srv.mu.Unlock()

	// 第二台设备登录不会再建默认配置（云端已有）
	b := newDevice(t, srv, "")
	b.login(t, false)
	if srv.count() != 1 || len(b.cloudProfiles()) != 1 {
		t.Fatalf("second device: server=%d local=%d", srv.count(), len(b.cloudProfiles()))
	}
	if _, ok, _ := b.creds.Load(); ok {
		t.Fatal("keepLoggedIn=false must not store the token")
	}
}

func TestEditPropagatesAndUsesETag(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	id := a.cloudProfiles()[0].Profile.ID
	srv.revokeAll("") // 不需要单设备模式
	b := newDevice(t, srv, "")
	b.login(t, false)
	srv.mu.Lock()
	for tk := range srv.tokens {
		srv.tokens[tk] = ""
	}
	srv.mu.Unlock()

	// 没有变化时 B 的拉取应是 304，不会重写缓存
	b.syncNow(t)

	c, _ := a.ps.Get(id)
	c.Profile.Proxies = []model.Proxy{{ID: model.NewID(), Name: "ssh", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022}}
	if _, err := a.ps.Put(c.Profile); err != nil {
		t.Fatal(err)
	}
	a.m.LocalEdited(id)
	waitFor(t, "pushed", func() bool { p, _ := srv.profile(id); return p.Revision == 2 })
	a.waitSynced(t)
	if got, _ := a.ps.Get(id); got.Dirty || got.Profile.Revision != 2 || got.BaseRevision != 2 {
		t.Fatalf("after push: %+v", got)
	}

	b.syncNow(t)
	got, _ := b.ps.Get(id)
	if len(got.Profile.Proxies) != 1 || got.Profile.Proxies[0].Name != "ssh" || got.Profile.Revision != 2 || got.Dirty {
		t.Fatalf("B did not receive the edit: %+v", got)
	}
	if ch, ok := b.ev.last(EventSyncChanged); !ok || len(ch.(Changed).ProfileIDs) != 1 {
		t.Fatalf("sync:changed not emitted: %v", ch)
	}
}

func TestConcurrentEditsConflict(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	id := a.cloudProfiles()[0].Profile.ID

	// 云端被别的设备改了，本机同时也改了
	srv.editCloud(id, func(p *model.Profile) { p.Name = "cloud-name" })
	c, _ := a.ps.Get(id)
	c.Profile.Name = "local-name"
	c.Profile.Proxies = []model.Proxy{{ID: model.NewID(), Name: "web", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 80}}
	_, _ = a.ps.Put(c.Profile)
	a.m.LocalEdited(id)

	waitFor(t, "conflict", func() bool { return len(a.m.Conflicts()) == 1 })
	cf := a.m.Conflicts()[0]
	if cf.ProfileID != id || cf.CloudRevision != 2 || cf.CloudDeleted || cf.LocalDeleted {
		t.Fatalf("conflict: %+v", cf)
	}
	var sawName, sawProxy bool
	for _, d := range cf.Diff {
		if d.Section == "profile" && d.Local == "local-name" && d.Cloud == "cloud-name" {
			sawName = true
		}
		if d.Section == "proxies" && d.Name == "web" && d.Change == "local_only" {
			sawProxy = true
		}
	}
	if !sawName || !sawProxy {
		t.Fatalf("diff summary: %+v", cf.Diff)
	}
	if _, ok := a.ev.last(EventSyncConflict); !ok {
		t.Fatal("sync:conflict not emitted")
	}
	if st, _ := a.m.ProfileSync(mustGet(t, a.ps, id)); st != "conflict" {
		t.Fatalf("profile sync state: %s", st)
	}
	// 冲突期间不会推送
	srv.mu.Lock()
	puts := srv.puts
	srv.mu.Unlock()
	a.syncNow(t)
	srv.mu.Lock()
	if srv.puts != puts {
		t.Fatalf("should not push while conflicted")
	}
	srv.mu.Unlock()

	// 保留本机：基于云端版本覆盖
	if _, err := a.m.Resolve(id, KeepLocal); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "local kept", func() bool { p, _ := srv.profile(id); return p.Name == "local-name" && p.Revision == 3 })
	a.waitSynced(t)

	// 再制造一次冲突，这次使用云端
	srv.editCloud(id, func(p *model.Profile) { p.Name = "cloud-2"; p.Proxies = nil })
	c, _ = a.ps.Get(id)
	c.Profile.Name = "local-2"
	_, _ = a.ps.Put(c.Profile)
	a.m.LocalEdited(id)
	waitFor(t, "second conflict", func() bool { return len(a.m.Conflicts()) == 1 })
	if _, err := a.m.Resolve(id, KeepCloud); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, a.ps, id)
	if got.Profile.Name != "cloud-2" || got.Dirty || got.Profile.Revision != 4 || len(got.Profile.Proxies) != 0 {
		t.Fatalf("use cloud: %+v", got)
	}
	if _, err := a.m.Resolve(id, KeepCloud); apierr.CodeOf(err) != apperr.ConflictGone {
		t.Fatalf("resolving twice: %v", err)
	}
}

func TestIdenticalEditsAreNotAConflict(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	id := a.cloudProfiles()[0].Profile.ID
	srv.editCloud(id, func(p *model.Profile) { p.Name = "same" })
	c, _ := a.ps.Get(id)
	c.Profile.Name = "same"
	_, _ = a.ps.Put(c.Profile)
	a.m.LocalEdited(id)
	waitFor(t, "adopted", func() bool { g := mustGet(t, a.ps, id); return !g.Dirty && g.Profile.Revision == 2 })
	if len(a.m.Conflicts()) != 0 {
		t.Fatalf("identical content should not conflict: %+v", a.m.Conflicts())
	}
}

func TestCloudDeleteWhileEditedIsConflict(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	id := a.cloudProfiles()[0].Profile.ID
	srv.deleteCloud(id)
	c, _ := a.ps.Get(id)
	c.Profile.Name = "edited"
	_, _ = a.ps.Put(c.Profile)
	a.m.LocalEdited(id)
	waitFor(t, "conflict", func() bool { return len(a.m.Conflicts()) == 1 })
	if cf := a.m.Conflicts()[0]; !cf.CloudDeleted {
		t.Fatalf("want cloudDeleted: %+v", cf)
	}
	// 保留本机：用同一个 ID 重新上传
	if _, err := a.m.Resolve(id, KeepLocal); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "re-uploaded", func() bool { p, ok := srv.profile(id); return ok && p.Name == "edited" })

	// 没有修改、没在运行的云端配置被别处删除：直接从本机删掉
	srv.deleteCloud(id)
	a.syncNow(t)
	if _, ok := a.ps.Get(id); ok {
		t.Fatal("profile deleted on cloud should be removed locally")
	}

	// 本机删除 → 推送 DELETE
	p := model.Profile{ID: model.NewID(), Name: "tmp"}
	_, _ = a.ps.PutNew(p, store.OriginCloud)
	a.m.LocalEdited(p.ID)
	waitFor(t, "uploaded", func() bool { _, ok := srv.profile(p.ID); return ok })
	a.waitSynced(t)
	if _, err := a.ps.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	a.m.LocalEdited(p.ID)
	waitFor(t, "deleted on cloud", func() bool { _, ok := srv.profile(p.ID); return !ok })
	waitFor(t, "tombstone removed", func() bool { return len(a.ps.All()) == 0 })
}

func TestRunningProfileRemoteUpdateAsks(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	id := a.cloudProfiles()[0].Profile.ID
	a.eng.mu.Lock()
	a.eng.running = id
	a.eng.mu.Unlock()

	srv.editCloud(id, func(p *model.Profile) {
		p.Proxies = []model.Proxy{{ID: model.NewID(), Name: "x", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 1}}
	})
	a.syncNow(t)
	ru := a.m.RemoteUpdates()
	if len(ru) != 1 || ru[0].ProfileID != id || ru[0].Mode != engine.UpdateHot {
		t.Fatalf("remote updates: %+v", ru)
	}
	if len(a.eng.updates) != 0 {
		t.Fatal("must not apply without asking")
	}
	if got := mustGet(t, a.ps, id); len(got.Profile.Proxies) != 1 {
		t.Fatalf("cache should already hold the cloud content: %+v", got)
	}
	mode, err := a.m.ApplyRemote(id)
	if err != nil || mode != engine.UpdateHot || len(a.eng.updates) != 1 {
		t.Fatalf("apply: %v %v %d", mode, err, len(a.eng.updates))
	}
	if len(a.m.RemoteUpdates()) != 0 {
		t.Fatal("remote update should be cleared after apply")
	}

	// 云端删除了正在运行的配置：冲突，"使用云端"会停止隧道
	srv.deleteCloud(id)
	a.syncNow(t)
	if cf := a.m.Conflicts(); len(cf) != 1 || !cf[0].CloudDeleted || !cf[0].Running {
		t.Fatalf("running + cloud deleted should conflict: %+v", cf)
	}
	res, err := a.m.Resolve(id, KeepCloud)
	if err != nil || !res.Stopped || a.eng.stopped != 1 {
		t.Fatalf("resolve: %+v %v", res, err)
	}
	if _, ok := a.ps.Get(id); ok {
		t.Fatal("profile should be removed")
	}
}

func TestSessionReplacedLogsOutButKeepsCache(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, true)
	b := newDevice(t, srv, "")
	b.login(t, true) // 单设备模式：A 被挤下线

	a.m.SyncNow()
	waitFor(t, "A logged out", func() bool { return !a.m.LoggedIn() })
	acct := a.m.Account()
	if acct.Ended == nil || acct.Ended.Code != apierr.SessionReplaced {
		t.Fatalf("want session_replaced, got %+v", acct)
	}
	if ev, ok := a.ev.last(EventAuthExpired); !ok || ev.(*apierr.Error).Code != apierr.SessionReplaced {
		t.Fatalf("auth:expired not emitted: %v", ev)
	}
	if _, ok, _ := a.creds.Load(); ok {
		t.Fatal("credential must be deleted after session_replaced")
	}
	if len(a.cloudProfiles()) != 1 || acct.CacheOwner != "alice" {
		t.Fatalf("cache should be kept for offline use: %+v %+v", a.cloudProfiles(), acct)
	}
	if a.m.Status().State != StateOff {
		t.Fatalf("status: %+v", a.m.Status())
	}
}

func TestChangePasswordWrongOldPasswordStaysLoggedIn(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	err := a.m.ChangePassword(context.Background(), "nope", "new-password-1")
	if apierr.CodeOf(err) != apierr.InvalidCredentials || !a.m.LoggedIn() {
		t.Fatalf("invalid_credentials must not log out: %v loggedIn=%v", err, a.m.LoggedIn())
	}
	if err := a.m.ChangePassword(context.Background(), "correct-horse", "new-password-1"); err != nil || !a.m.LoggedIn() {
		t.Fatalf("change password: %v", err)
	}
}

func TestOfflineEditsArePushedWhenBack(t *testing.T) {
	srv := newFakeServer()
	a := newDevice(t, srv, "")
	a.login(t, false)
	id := a.cloudProfiles()[0].Profile.ID

	defer srv.Close()
	srv.down.Store(true)

	c, _ := a.ps.Get(id)
	c.Profile.Name = "offline-edit"
	_, _ = a.ps.Put(c.Profile)
	a.m.LocalEdited(id)
	waitFor(t, "offline", func() bool { return a.m.Status().State == StateOffline })
	if s := a.m.Status(); s.Pending != 1 || s.Error == nil || s.Error.Code != apperr.ServerUnreachable {
		t.Fatalf("offline status: %+v", s)
	}
	if !a.m.LoggedIn() {
		t.Fatal("going offline must not log out")
	}

	srv.down.Store(false)
	waitFor(t, "pushed after reconnect", func() bool { p, _ := srv.profile(id); return p.Name == "offline-edit" })
	a.waitSynced(t)
}

func TestLogoutClearsCloudCacheKeepsLocal(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	local, _ := a.ps.PutNew(model.Profile{ID: model.NewID(), Name: "mine"}, store.OriginLocal)
	a.login(t, true)
	if len(a.cloudProfiles()) != 1 {
		t.Fatal("expected default cloud profile")
	}
	if err := a.m.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.m.LoggedIn() || len(a.cloudProfiles()) != 0 || a.ps.Sync().Account != "" {
		t.Fatalf("after logout: loggedIn=%v cloud=%d sync=%+v", a.m.LoggedIn(), len(a.cloudProfiles()), a.ps.Sync())
	}
	if _, ok := a.ps.Get(local.Profile.ID); !ok {
		t.Fatal("local profile must be kept")
	}
	if _, ok, _ := a.creds.Load(); ok {
		t.Fatal("credential must be deleted on logout")
	}
	// 服务端会话已吊销
	srv.mu.Lock()
	for tk, r := range srv.tokens {
		if r == "" {
			t.Fatalf("token %s still valid after logout", tk)
		}
	}
	srv.mu.Unlock()
	// 退出后重启：不会自动登录
	again := newDevice(t, srv, a.dir)
	again.creds = a.creds
	again.m.creds = a.creds
	again.m.Restore()
	if again.m.LoggedIn() {
		t.Fatal("must not auto login after logout")
	}
}

func TestRestoreAutoLogin(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, true)
	cred, _, _ := a.creds.Load()
	a.m.Close()

	// "重启"：同一个数据目录、同一份凭据
	b := newDevice(t, srv, a.dir)
	_ = b.creds.Save(cred)
	b.m.Restore()
	if !b.m.LoggedIn() {
		t.Fatal("should be logged in from credential")
	}
	b.waitSynced(t)
	if u := b.m.Account().User; u == nil || u.ID != "u1" || u.Nickname != "alice" {
		t.Fatalf("user after restore: %+v", u)
	}
	if srv.count() != 1 {
		t.Fatalf("auto login must not create another default profile: %d", srv.count())
	}
}

func TestOutdatedStopsSyncKeepsLogin(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, true)
	srv.setOutdated(true)
	a.m.SyncNow()
	waitFor(t, "outdated", func() bool { return a.m.Status().State == StateOutdated })
	acct := a.m.Account()
	if !acct.LoggedIn || acct.Outdated == nil || acct.Outdated.Details[apierr.DetailDownloadURL] != "https://example.com/new.exe" {
		t.Fatalf("outdated account: %+v", acct)
	}
	if len(a.cloudProfiles()) != 1 {
		t.Fatal("cache must be kept")
	}
	// 登录也会收到 426
	b := newDevice(t, srv, "")
	if _, err := b.m.Login(context.Background(), "alice", "correct-horse", false); apierr.CodeOf(err) != apierr.ClientOutdated {
		t.Fatalf("login when outdated: %v", err)
	}
	if b.m.Account().Outdated == nil {
		t.Fatal("outdated info should be recorded on failed login")
	}
}

func TestSwitchAccountClearsOtherCache(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	a := newDevice(t, srv, "")
	a.login(t, false)
	_ = a.m.Logout(context.Background())
	// 模拟缓存里残留了另一个账号的云端配置（例如被挤下线后换账号登录）
	_ = a.ps.Tx(func(tx *store.Txn) error {
		tx.Add(store.CachedProfile{Profile: model.Profile{ID: model.NewID(), Name: "bob's", Revision: 3}, Origin: store.OriginCloud, BaseRevision: 3})
		tx.Sync().Account, tx.Sync().Nickname = "u-bob", "bob"
		return nil
	})
	a.login(t, false)
	for _, c := range a.cloudProfiles() {
		if c.Profile.Name == "bob's" {
			t.Fatal("other account's cloud profiles must be cleared")
		}
	}
}

func mustGet(t *testing.T, ps *store.ProfileStore, id string) store.CachedProfile {
	t.Helper()
	c, ok := ps.Get(id)
	if !ok {
		t.Fatalf("profile %s not found", id)
	}
	return c
}

func (d *device) officialCalls() [][]api.OfficialNode {
	d.offMu.Lock()
	defer d.offMu.Unlock()
	return append([][]api.OfficialNode(nil), d.official...)
}

func TestOfficialNodesSync(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()
	srv.official = []api.OfficialNode{{ID: "n1", Name: "东京", ServerAddr: "tokyo.example.com", ServerPort: 7000,
		User: "alice", Online: true}}
	d := newDevice(t, srv, "")
	d.login(t, true)

	calls := d.officialCalls()
	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0].ServerAddr != "tokyo.example.com" {
		t.Fatalf("登录后拉到官方节点：%+v", calls)
	}
	if st := d.ps.Sync(); len(st.Official) != 1 || st.OfficialETag == "" {
		t.Fatalf("官方节点存进加密缓存：%+v", st)
	}

	// 没变化：304，不回调
	d.syncNow(t)
	if len(d.officialCalls()) != 1 {
		t.Fatal("没变化时不应回调")
	}
	// 变化了：回调新列表
	srv.mu.Lock()
	srv.official = append(srv.official, api.OfficialNode{ID: "n2", Name: "香港", ServerAddr: "hk.example.com", ServerPort: 7000})
	srv.mu.Unlock()
	d.syncNow(t)
	if c := d.officialCalls(); len(c) != 2 || len(c[1]) != 2 {
		t.Fatalf("变化后回调：%+v", c)
	}

	// 退出登录：清空并回调 nil
	if err := d.m.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := d.officialCalls(); c[len(c)-1] != nil || len(d.ps.Sync().Official) != 0 {
		t.Fatal("退出登录应清空官方节点")
	}
}

func TestOfficialNodesOldServer(t *testing.T) {
	srv := newFakeServer() // official 为 nil：旧版服务端，接口 404
	defer srv.Close()
	d := newDevice(t, srv, "")
	d.login(t, true)
	if len(d.officialCalls()) != 0 || d.m.Status().State != StateSynced {
		t.Fatal("旧版服务端没有官方节点接口时同步照常")
	}
}
