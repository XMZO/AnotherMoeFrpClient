package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/cloudsync"
	"moefrp/client/internal/credstore"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/engine/frpstest"
	"moefrp/client/internal/store"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

type env struct {
	profiles *ProfileService
	tunnel   *TunnelService
	settings *store.SettingsStore
	eng      *engine.Engine
	store    *store.ProfileStore
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	settings, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	ps, _, err := store.OpenProfiles(filepath.Join(dir, "cache.bin"))
	if err != nil {
		t.Fatal(err)
	}
	sink := engine.NewLogSink(1000)
	sink.InstallGlobal("info")
	eng := engine.New(sink, engine.Options{})
	t.Cleanup(func() {
		_ = eng.Shutdown(0, 10*time.Second)
		eng.Close()
	})
	tunnel, _ := NewTunnelService(eng, sink, ps, settings)
	api, err := apiclient.New("http://127.0.0.1:1", "3.0.0", settings.Get().InstallID, apiclient.ProxyNone)
	if err != nil {
		t.Fatal(err)
	}
	mgr := cloudsync.New(cloudsync.Options{API: api, Creds: &credstore.Memory{}, Profiles: ps, Engine: eng})
	t.Cleanup(mgr.Close)
	return &env{profiles: NewProfileService(ps, settings, eng, mgr), tunnel: tunnel, settings: settings, eng: eng, store: ps}
}

func TestProfileSaveFillsIDsAndSecrets(t *testing.T) {
	e := newEnv(t)
	p, err := e.profiles.Create("A", "")
	if err != nil {
		t.Fatal(err)
	}
	p.Nodes = []model.Node{{ServerAddr: "127.0.0.1", ServerPort: 7000}}
	p.Proxies = []model.Proxy{{Name: "rdp", Type: model.ProxySTCP, Enabled: true, LocalPort: 3389}}
	res, err := e.profiles.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Profile
	if got.Nodes[0].ID == "" || got.Proxies[0].ID == "" {
		t.Fatalf("ids should be filled: %+v", got)
	}
	if len(got.Proxies[0].SecretKey) < 32 {
		t.Fatalf("stcp secretKey should be generated, got %q", got.Proxies[0].SecretKey)
	}
	if res.Applied != engine.UpdateNone || res.ApplyError != nil {
		t.Fatalf("not running, nothing to apply: %+v", res)
	}
	// 再保存一次，ID 和密钥保持不变
	res2, err := e.profiles.Save(got)
	if err != nil || res2.Profile.Proxies[0].ID != got.Proxies[0].ID || res2.Profile.Proxies[0].SecretKey != got.Proxies[0].SecretKey {
		t.Fatalf("second save changed ids or secret: %+v %v", res2.Profile.Proxies, err)
	}
}

func TestProfileSaveRejectsDuplicateWithoutWriting(t *testing.T) {
	e := newEnv(t)
	p, _ := e.profiles.Create("A", "")
	p.Proxies = []model.Proxy{
		{Name: "web", Type: model.ProxyTCP, Enabled: true, LocalPort: 80},
		{Name: "web", Type: model.ProxyTCP, Enabled: true, LocalPort: 81},
	}
	_, err := e.profiles.Save(p)
	if apierr.CodeOf(err) != apierr.ValidationFailed {
		t.Fatalf("want validation_failed, got %v", err)
	}
	stored, _ := e.profiles.Get(p.ID)
	if len(stored.Proxies) != 0 {
		t.Fatalf("rejected save must not be written: %+v", stored.Proxies)
	}
}

func TestExportLogs(t *testing.T) {
	e := newEnv(t)
	e.tunnel.sink.Appendf("info", "第一行 <b>不是 HTML</b>")
	e.tunnel.sink.Appendf("warn", "second")
	path := filepath.Join(t.TempDir(), "out.log")
	if err := e.tunnel.ExportLogs(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "第一行 <b>不是 HTML</b>\r\n") || !strings.HasSuffix(s, "second\r\n") {
		t.Fatalf("exported: %q", s)
	}
	if err := e.tunnel.ExportLogs(""); apierr.CodeOf(err) != apperr.InvalidArg {
		t.Fatalf("empty path: %v", err)
	}
	if err := e.tunnel.ExportLogs(filepath.Join(t.TempDir(), "no", "such", "dir", "x.log")); apierr.CodeOf(err) != apperr.ExportFailed {
		t.Fatalf("bad path: %v", err)
	}
	if _, err := e.tunnel.ExportLogsDialog(); apierr.CodeOf(err) != apperr.ExportFailed {
		t.Fatalf("no dialog injected: %v", err)
	}
}

func TestSaveWhileRunningHotUpdatesAndDeleteIsRefused(t *testing.T) {
	echo, err := frpstest.StartEcho("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	frps, err := frpstest.StartFrps(0, "svc-token")
	if err != nil {
		t.Fatal(err)
	}
	defer frps.Close()

	e := newEnv(t)
	p, _ := e.profiles.Create("run", "")
	p.Nodes = []model.Node{{ServerAddr: "127.0.0.1", ServerPort: frps.Port, Token: "svc-token"}}
	p.Proxies = []model.Proxy{{Name: "a", Type: model.ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 1}}
	res, err := e.profiles.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	p = res.Profile
	if err := e.tunnel.Start(p.ID, p.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.eng.Status().State != engine.StateRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if e.eng.Status().State != engine.StateRunning {
		t.Fatalf("not running: %+v", e.eng.Status())
	}
	if s := e.settings.Get(); s.LastProfileID != p.ID || s.LastNodeIDs[p.ID] != p.Nodes[0].ID {
		t.Fatalf("last profile/node not remembered: %+v", s)
	}

	p.Proxies = append(p.Proxies, model.Proxy{Name: "b", Type: model.ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 2})
	res, err = e.profiles.Save(p)
	if err != nil || res.Applied != engine.UpdateHot || res.ApplyError != nil {
		t.Fatalf("save while running should hot update: %+v %v", res, err)
	}

	// 删掉正在用的节点：保存成功，但应用失败并说明原因，隧道保持原样
	p = res.Profile
	p.Nodes = []model.Node{{ServerAddr: "127.0.0.1", ServerPort: 1}}
	res, err = e.profiles.Save(p)
	if err != nil || res.ApplyError == nil || res.ApplyError.Code != apperr.NodeNotFound {
		t.Fatalf("want saved + node_not_found apply error, got %+v %v", res, err)
	}
	if e.eng.Status().State != engine.StateRunning {
		t.Fatalf("tunnel should keep running")
	}

	if err := e.profiles.Delete(p.ID); apierr.CodeOf(err) != apperr.ProfileInUse {
		t.Fatalf("deleting a running profile should be refused, got %v", err)
	}
}
