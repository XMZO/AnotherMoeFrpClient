package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moefrp/agent/internal/paths"
)

func TestDownloadVerify(t *testing.T) {
	body := []byte("new binary")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)
	dst := filepath.Join(t.TempDir(), "bin")
	if err := download(context.Background(), srv.URL, dst, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), srv.URL, dst, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("哈希不对应报错：%v", err)
	}
}

func TestStartupRollback(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{Bin: filepath.Join(dir, "bin"), StateDir: filepath.Join(dir, "state")}
	os.MkdirAll(p.StateDir, 0o700)
	os.WriteFile(p.Bin, []byte("new"), 0o755)
	os.WriteFile(p.BackupBin(), []byte("old"), 0o755)
	if err := saveState(p, &State{From: "1.0", To: "2.0"}); err != nil {
		t.Fatal(err)
	}
	// 新版本启动 3 次都没确认
	for i := 1; i <= maxAttempts; i++ {
		if _, rb, err := Startup(p, "2.0"); rb || err != nil {
			t.Fatalf("第 %d 次不应回滚：%v", i, err)
		}
	}
	s, rb, err := Startup(p, "2.0")
	if !rb || err != nil {
		t.Fatalf("第 4 次应回滚：%v", err)
	}
	if b, _ := os.ReadFile(p.Bin); string(b) != "old" {
		t.Fatal("应换回旧程序")
	}
	// 旧版本启动：上报失败
	s, rb, _ = Startup(p, "1.0")
	if rb || s == nil {
		t.Fatal("旧版本不应再回滚")
	}
	if r := Result(s, "1.0"); r.OK || !r.RolledBack || r.Error == "" {
		t.Fatalf("结果：%+v", r)
	}
	Finish(p)
	if _, err := os.Stat(p.UpgradeState()); !os.IsNotExist(err) {
		t.Fatal("Finish 应删除状态文件")
	}
	// 成功的情况
	saveState(p, &State{From: "1.0", To: "2.0"})
	s, _, _ = Startup(p, "2.0")
	if r := Result(s, "2.0"); !r.OK {
		t.Fatalf("成功：%+v", r)
	}
}
