package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cli struct {
	t   *testing.T
	cfg string
	dir string
}

func newCLI(t *testing.T) *cli {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	db := filepath.ToSlash(filepath.Join(dir, "data", "moefrp.db"))
	if err := os.WriteFile(cfg, []byte("db_path = \""+db+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOEFRP_CONFIG", "")
	return &cli{t: t, cfg: cfg, dir: dir}
}

func (c *cli) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func (c *cli) ok(args ...string) string {
	c.t.Helper()
	code, out, errs := c.run(args...)
	if code != 0 {
		c.t.Fatalf("%v 退出码 %d：%s%s", args, code, out, errs)
	}
	return out
}

func TestMigrateAndBackup(t *testing.T) {
	c := newCLI(t)

	// 没迁移时导入命令拒绝执行，也不会凭空建库
	if code, _, errs := c.run("import-legacy", "--from", filepath.Join(c.dir, "users.db"), "-c", c.cfg); code != 1 || !strings.Contains(errs, "不存在") {
		t.Fatalf("库不存在时应报错：%d %s", code, errs)
	}

	if out := c.ok("migrate", "-c", c.cfg); !strings.Contains(out, "0001_init.sql") {
		t.Fatalf("migrate 输出：%s", out)
	}
	if out := c.ok("migrate", "-c", c.cfg); !strings.Contains(out, "已是最新") {
		t.Fatalf("第二次 migrate：%s", out)
	}

	backup := filepath.Join(c.dir, "bak", "backup.db")
	c.ok("backup", "-c", c.cfg, "--to", backup)
	if _, err := os.Stat(backup); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := c.run("backup", "-c", c.cfg, "--to", backup); code != 1 {
		t.Fatal("备份目标已存在时应失败")
	}
	if code, _, _ := c.run("backup", "-c", c.cfg); code != 2 {
		t.Fatal("缺少 --to 应返回 2")
	}
}

func TestUsageErrors(t *testing.T) {
	c := newCLI(t)
	for _, args := range [][]string{{}, {"nope"}, {"migrate", "extra"}} {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("%v 应返回 2", args)
		}
	}
	if code, out, _ := c.run("version"); code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("version：%d %s", code, out)
	}
	if code, _, errs := c.run("admin", "invite", "create"); code != 1 || !strings.Contains(errs, "/admin/") {
		t.Fatalf("admin 子命令应提示改用网页：%d %s", code, errs)
	}
	if code, out, _ := c.run("help"); code != 0 || !strings.Contains(out, "/admin/") {
		t.Fatal("help 应打印用法")
	}
	// 配置文件里有未知字段：报错退出
	bad := filepath.Join(c.dir, "bad.toml")
	os.WriteFile(bad, []byte("listne = \"x\"\n"), 0o600)
	if code, _, errs := c.run("migrate", "-c", bad); code != 1 || !strings.Contains(errs, "listne") {
		t.Fatalf("未知配置项应报错：%d %s", code, errs)
	}
}

func TestParseArgsInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	c := fs.String("c", "", "")
	n := fs.Int("n", 0, "")
	pos, err := parseArgs(fs, []string{"alice", "-c", "x.toml", "vip", "-n", "3"})
	if err != nil || len(pos) != 2 || pos[0] != "alice" || pos[1] != "vip" || *c != "x.toml" || *n != 3 {
		t.Fatalf("交错解析失败：%v %v c=%s n=%d", pos, err, *c, *n)
	}
}

func TestLoadConfigDefaultPaths(t *testing.T) {
	dir := t.TempDir()
	etc, data := filepath.Join(dir, "etc.toml"), filepath.Join(dir, "data.toml")
	old := defaultConfigPaths
	defaultConfigPaths = []string{etc, data}
	t.Cleanup(func() { defaultConfigPaths = old })
	t.Setenv("MOEFRP_CONFIG", "")

	if _, p, err := loadConfig(""); err != nil || p != "" {
		t.Fatalf("都不存在时不用配置文件：%q %v", p, err)
	}
	os.WriteFile(data, []byte("log_level = \"debug\"\n"), 0o600)
	if cfg, p, err := loadConfig(""); err != nil || p != data || cfg.LogLevel != "debug" {
		t.Fatalf("应使用 /data/config.toml：%q %v", p, err)
	}
	os.WriteFile(etc, []byte("log_level = \"warn\"\n"), 0o600)
	if _, p, _ := loadConfig(""); p != etc {
		t.Fatalf("/etc 优先：%q", p)
	}
}
