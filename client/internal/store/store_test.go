package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moefrp/shared/model"
)

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	st, warn, err := OpenSettings(path)
	if err != nil || warn != "" {
		t.Fatalf("open: %v %q", err, warn)
	}
	s := st.Get()
	if s.InstallID == "" || s.Theme != ThemeSystem || s.CloseAction != CloseToTray || s.LogLevel != "info" {
		t.Fatalf("defaults: %+v", s)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("first run should write the file (installId): %v", err)
	}
	if _, err := st.Update(func(s *Settings) error { s.Theme = ThemeDark; s.LastNodeIDs = map[string]string{"p": "n"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Update(func(s *Settings) error { s.Theme = "pink"; return nil }); err == nil {
		t.Fatalf("unknown theme must be rejected")
	}
	st2, _, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	got := st2.Get()
	if got.Theme != ThemeDark || got.InstallID != s.InstallID || got.LastNodeIDs["p"] != "n" {
		t.Fatalf("round trip: %+v", got)
	}
	// 修改副本不影响内部状态
	got.LastNodeIDs["p"] = "changed"
	if st2.Get().LastNodeIDs["p"] != "n" {
		t.Fatalf("Get must return a copy")
	}
	// 不含任何机密：文件里只有这些键
	raw, _ := os.ReadFile(path)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for k := range m {
		if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "password") {
			t.Fatalf("settings.json must not contain secrets: %s", k)
		}
	}
}

func TestSettingsCorruptAndPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	st, warn, err := OpenSettings(path)
	if err != nil || warn == "" {
		t.Fatalf("corrupt file: err=%v warn=%q", err, warn)
	}
	if st.Get().Theme != ThemeSystem {
		t.Fatalf("should fall back to defaults")
	}
	matches, _ := filepath.Glob(path + ".bad-*")
	if len(matches) != 1 {
		t.Fatalf("corrupt file should be kept as a backup, got %v", matches)
	}

	// 旧版本或手工改坏的字段补成默认值
	_ = os.WriteFile(path, []byte(`{"theme":"dark","closeAction":"explode"}`), 0o600)
	st, warn, err = OpenSettings(path)
	if err != nil || warn != "" {
		t.Fatal(err, warn)
	}
	if s := st.Get(); s.Theme != ThemeDark || s.CloseAction != CloseToTray || s.InstallID == "" {
		t.Fatalf("fill: %+v", s)
	}
}

func sampleProfile(name string) model.Profile {
	return model.Profile{ID: model.NewID(), Name: name,
		Nodes:   []model.Node{{ID: model.NewID(), ServerAddr: "frp.example.com", ServerPort: 7000, Token: "super-secret-token"}},
		Proxies: []model.Proxy{{ID: model.NewID(), Name: "ssh", Type: model.ProxyTCP, Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, CustomDomains: []string{"x"}}}}
}

func TestProfileStoreEncryptedRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.bin")
	st, warn, err := OpenProfiles(path)
	if err != nil || warn != "" {
		t.Fatal(err, warn)
	}
	a, err := st.Put(sampleProfile("A"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Origin != OriginLocal || !a.Dirty || a.Profile.Revision != 0 || a.Profile.Visitors == nil {
		t.Fatalf("put: %+v", a)
	}
	b, _ := st.Put(sampleProfile("B"))

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, cacheMagic) {
		t.Fatalf("missing magic header")
	}
	if bytes.Contains(raw, []byte("super-secret-token")) || bytes.Contains(raw, []byte("frp.example.com")) {
		t.Fatalf("cache.bin must be encrypted")
	}

	st2, warn, err := OpenProfiles(path)
	if err != nil || warn != "" {
		t.Fatal(err, warn)
	}
	list := st2.List()
	if len(list) != 2 || list[0].Profile.ID != b.Profile.ID {
		t.Fatalf("list should be newest first: %+v", list)
	}
	got, ok := st2.Get(a.Profile.ID)
	if !ok || got.Profile.Nodes[0].Token != "super-secret-token" || got.Profile.Proxies[0].CustomDomains[0] != "x" {
		t.Fatalf("get: %+v", got)
	}
	got.Profile.Proxies[0].CustomDomains[0] = "mutated"
	if again, _ := st2.Get(a.Profile.ID); again.Profile.Proxies[0].CustomDomains[0] != "x" {
		t.Fatalf("Get must return a deep copy")
	}

	// 从未上传过的配置直接删除；上传过的（Revision>0）留墓碑
	if ok, err := st2.Delete(a.Profile.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	st2.mu.Lock()
	st2.data.Profiles[st2.indexLocked(b.Profile.ID)].Profile.Revision = 3
	st2.mu.Unlock()
	if ok, err := st2.Delete(b.Profile.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if st2.Count() != 0 || len(st2.List()) != 0 {
		t.Fatalf("both deleted")
	}
	st3, _, _ := OpenProfiles(path)
	if len(st3.data.Profiles) != 1 || !st3.data.Profiles[0].Deleted || !st3.data.Profiles[0].Dirty {
		t.Fatalf("tombstone expected for uploaded profile: %+v", st3.data.Profiles)
	}
	// 再次 Put 同一个 ID（例如撤销删除）保留云端版本号
	p := b.Profile
	c, _ := st3.Put(p)
	if c.Profile.Revision != 3 || c.Deleted {
		t.Fatalf("put after tombstone: %+v", c)
	}
}

func TestProfileStoreUnreadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.bin")
	// 模拟别的用户/机器加密的文件：头对，但密文解不开
	_ = os.WriteFile(path, append(append([]byte(nil), cacheMagic...), 1, 2, 3, 4), 0o600)
	st, warn, err := OpenProfiles(path)
	if err != nil || warn == "" || st.Count() != 0 {
		t.Fatalf("err=%v warn=%q", err, warn)
	}
	if m, _ := filepath.Glob(path + ".bad-*"); len(m) != 1 {
		t.Fatalf("unreadable cache should be kept as a backup: %v", m)
	}
	if _, err := st.Put(sampleProfile("new")); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPathsHomeOverride(t *testing.T) {
	dir := t.TempDir()
	p, err := DefaultPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.SettingsFile() != filepath.Join(dir, "Roaming", "settings.json") || p.WebView2Dir() != filepath.Join(dir, "Local", "WebView2") {
		t.Fatalf("paths: %+v", p)
	}
	p, _ = DefaultPaths("")
	if !strings.HasSuffix(p.Roaming, `\MoeFrp`) || !strings.HasSuffix(p.CrashLog(), `MoeFrp\crash.log`) {
		t.Fatalf("default paths: %+v", p)
	}
}
