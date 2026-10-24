package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestVault(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	v := Open(dir, "machine-1", "token-1", c.now)
	if v.Unlocked() || v.Reason() == "" {
		t.Fatal("还没收到密钥时应锁着")
	}
	if err := v.Write("frps", []byte("x")); !errors.Is(err, ErrLocked) {
		t.Fatalf("锁着时写：%v", err)
	}

	// 面板下发：面板时间比本机快 1 小时，租约 7 天
	server := c.t.Add(time.Hour)
	unlocked, err := v.SetLease(key(1), server.Add(7*24*time.Hour).Unix(), server.Unix())
	if err != nil || !unlocked || !v.Unlocked() {
		t.Fatalf("解锁：%v %v", unlocked, err)
	}
	if got := v.LeaseUntil(); !got.Equal(c.t.Add(7 * 24 * time.Hour)) {
		t.Fatalf("租约应按本机时间算 7 天：%v", got)
	}
	if err := v.Write("frps", []byte(`{"bindPort":7000}`)); err != nil {
		t.Fatal(err)
	}
	// 磁盘上没有明文
	raw, _ := os.ReadFile(filepath.Join(dir, "frps.enc"))
	if bytes.Contains(raw, []byte("bindPort")) {
		t.Fatal("磁盘上不应有明文")
	}

	// 重启（面板离线）：租约内照常解开
	c.t = c.t.Add(3 * 24 * time.Hour)
	v2 := Open(dir, "machine-1", "token-1", c.now)
	if b, err := v2.Read("frps"); err != nil || string(b) != `{"bindPort":7000}` {
		t.Fatalf("租约内重启：%q %v %s", b, err, v2.Reason())
	}
	// 换了机器（或令牌）：解不开
	if v3 := Open(dir, "machine-2", "token-1", c.now); v3.Unlocked() || !strings.Contains(v3.Reason(), "机器指纹") {
		t.Fatalf("换机器：%s", v3.Reason())
	}
	// 系统时间往回调：拒绝
	back := &clock{t: c.t.Add(-time.Hour)}
	if v4 := Open(dir, "machine-1", "token-1", back.now); v4.Unlocked() || !strings.Contains(v4.Reason(), "往回调") {
		t.Fatalf("回调时间：%s", v4.Reason())
	}
	// 租约过期：锁着
	c.t = c.t.Add(5 * 24 * time.Hour)
	v5 := Open(dir, "machine-1", "token-1", c.now)
	if v5.Unlocked() || !strings.Contains(v5.Reason(), "到期") {
		t.Fatalf("租约过期：%s", v5.Reason())
	}
	// 面板连上续约（同一个密钥）：解锁，内容还在
	if _, err := v5.SetLease(key(1), c.t.Add(7*24*time.Hour).Unix(), c.t.Unix()); err != nil {
		t.Fatal(err)
	}
	if b, _ := v5.Read("frps"); string(b) != `{"bindPort":7000}` {
		t.Fatal("续约后内容应还在")
	}

	// 面板轮换主密钥：新密钥重新加密，旧密钥解不开新文件
	if _, err := v5.SetLease(key(2), c.t.Add(7*24*time.Hour).Unix(), c.t.Unix()); err != nil {
		t.Fatal(err)
	}
	if b, err := v5.Read("frps"); err != nil || string(b) != `{"bindPort":7000}` {
		t.Fatalf("换密钥后：%q %v", b, err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "frps.enc"))
	if _, err := open(key(1), raw); err == nil {
		t.Fatal("旧密钥不应再能解开")
	}

	// 锁着时收到一个解不开旧文件的新密钥（例如租约过期期间面板换了主密钥）：旧文件删掉，等面板重新下发
	c.t = c.t.Add(30 * 24 * time.Hour)
	v6 := Open(dir, "machine-1", "token-1", c.now)
	if v6.Unlocked() {
		t.Fatal("应该过期了")
	}
	if _, err := v6.SetLease(key(3), c.t.Add(time.Hour).Unix(), c.t.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := v6.Read("frps"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("解不开的旧文件应删掉：%v", err)
	}
}
