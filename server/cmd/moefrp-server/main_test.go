package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
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

func TestMigrateAndAdmin(t *testing.T) {
	c := newCLI(t)

	// 没迁移时管理命令拒绝执行，也不会凭空建库
	if code, _, errs := c.run("admin", "invite", "list", "-c", c.cfg); code != 1 || !strings.Contains(errs, "不存在") {
		t.Fatalf("库不存在时应报错：%d %s", code, errs)
	}

	if out := c.ok("migrate", "-c", c.cfg); !strings.Contains(out, "0001_init.sql") {
		t.Fatalf("migrate 输出：%s", out)
	}
	if out := c.ok("migrate", "-c", c.cfg); !strings.Contains(out, "已是最新") {
		t.Fatalf("第二次 migrate：%s", out)
	}

	out := c.ok("admin", "invite", "create", "-n", "3", "--note", "给 测试", "-c", c.cfg)
	codes := strings.Fields(out)
	re := regexp.MustCompile(`^FRPT-[A-Z2-9]{4}-[A-Z2-9]{4}$`)
	if len(codes) != 3 || !re.MatchString(codes[0]) {
		t.Fatalf("invite create 输出：%q", out)
	}
	out = c.ok("admin", "invite", "list", "--unused", "-c", c.cfg)
	if strings.Count(out, "FRPT-") != 3 || !strings.Contains(out, "给 测试") {
		t.Fatalf("invite list：%s", out)
	}
	c.ok("admin", "invite", "revoke", codes[0], "-c", c.cfg) // 选项写在参数后面也可以
	if out := c.ok("admin", "invite", "list", "--unused", "-c", c.cfg); strings.Count(out, "FRPT-") != 2 {
		t.Fatalf("撤销后 unused 应剩 2 个：%s", out)
	}
	if code, _, errs := c.run("admin", "invite", "revoke", codes[0], "-c", c.cfg); code != 1 || !strings.Contains(errs, "已撤销") {
		t.Fatalf("重复撤销：%d %s", code, errs)
	}

	if out := c.ok("admin", "user", "list", "-c", c.cfg); !strings.Contains(out, "NICKNAME") {
		t.Fatalf("user list：%s", out)
	}
	if code, _, errs := c.run("admin", "user", "set-role", "nobody", "vip", "-c", c.cfg); code != 1 || !strings.Contains(errs, "用户不存在") {
		t.Fatalf("set-role 不存在的用户：%d %s", code, errs)
	}
	if code, _, _ := c.run("admin", "user", "set-role", "-c", c.cfg, "only-one-arg"); code != 2 {
		t.Fatal("参数个数不对应返回 2")
	}
	for _, sub := range []string{"disable", "enable", "kick", "reset-code"} {
		if code, _, _ := c.run("admin", "user", sub, "nobody", "-c", c.cfg); code != 1 {
			t.Fatalf("%s 不存在的用户应返回 1", sub)
		}
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
	for _, args := range [][]string{{}, {"nope"}, {"admin"}, {"admin", "foo", "bar"}, {"migrate", "extra"}} {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("%v 应返回 2", args)
		}
	}
	if code, out, _ := c.run("version"); code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("version：%d %s", code, out)
	}
	if code, out, _ := c.run("help"); code != 0 || !strings.Contains(out, "admin user reset-code") {
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
