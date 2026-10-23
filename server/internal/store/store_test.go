package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "sub", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	if err := st.CheckSchema(ctx); err == nil {
		t.Fatal("未迁移的库应该报错")
	}
	done, err := st.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) == 0 || done[0].Name != "0001_init.sql" {
		t.Fatalf("第一次应执行 0001：%v", done)
	}
	done, err = st.Migrate(ctx)
	if err != nil || len(done) != 0 {
		t.Fatalf("第二次不应执行任何迁移：%v %v", done, err)
	}
	if err := st.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "sessions", "invite_codes", "password_reset_codes", "profiles", "shares", "subscriptions", "audit_log", "schema_migrations"} {
		var n int
		if err := st.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("缺少表 %s", table)
		}
	}
}

// PRAGMA 必须在每个连接上生效（PLAN 7.3），而不只是第一个连接。
func TestPragmasOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	var conns []*sql.Conn
	for i := 0; i < 3; i++ {
		c, err := st.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		var journal string
		var busy, fk, sync int
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy)
		c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk)
		c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync)
		if strings.ToLower(journal) != "wal" || busy != 5000 || fk != 1 || sync != 1 {
			t.Errorf("连接 %d：journal=%s busy=%d fk=%d sync=%d", i, journal, busy, fk, sync)
		}
		c.Close()
	}
}

func TestForeignKeysAndUnique(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u := User{ID: "u1", Nickname: "Alice", PasswordHash: "h", Role: "user", Status: "active", CreatedAt: 1, UpdatedAt: 1}
	if err := InsertUser(ctx, st.DB(), u); err != nil {
		t.Fatal(err)
	}
	u.ID = "u2"
	u.Nickname = "alice" // COLLATE NOCASE
	if err := InsertUser(ctx, st.DB(), u); !IsUniqueViolation(err) {
		t.Fatalf("昵称应不区分大小写唯一：%v", err)
	}
	got, err := GetUserByNickname(ctx, st.DB(), "ALICE")
	if err != nil || got.ID != "u1" {
		t.Fatalf("按昵称查询应不区分大小写：%v %v", got, err)
	}
	// 外键：会话必须属于存在的用户
	err = InsertSession(ctx, st.DB(), Session{ID: "s", UserID: "nobody", TokenHash: []byte{1}, CreatedAt: 1, LastSeenAt: 1, ExpiresAt: 2})
	if err == nil {
		t.Fatal("外键约束没有生效")
	}
	if _, err := GetUser(ctx, st.DB(), "missing"); err != ErrNotFound {
		t.Fatalf("应返回 ErrNotFound：%v", err)
	}
}

func TestClaimInviteOnce(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	st.Migrate(ctx)
	for _, id := range []string{"u1", "u2"} {
		InsertUser(ctx, st.DB(), User{ID: id, Nickname: id, PasswordHash: "h", Role: "user", Status: "active"})
	}
	InsertInvite(ctx, st.DB(), "FRPT-AAAA-AAAA", "", 1)
	ok, err := ClaimInvite(ctx, st.DB(), "FRPT-AAAA-AAAA", "u1", 2)
	if err != nil || !ok {
		t.Fatalf("第一次占用应成功：%v %v", ok, err)
	}
	ok, _ = ClaimInvite(ctx, st.DB(), "FRPT-AAAA-AAAA", "u2", 3)
	if ok {
		t.Fatal("同一个邀请码不能被占用两次")
	}
	inv, _ := GetInvite(ctx, st.DB(), "FRPT-AAAA-AAAA")
	if inv.UsedBy != "u1" || inv.UsedByNickname != "u1" || inv.UsedAt == nil || *inv.UsedAt != 2 {
		t.Fatalf("占用记录不对：%+v", inv)
	}
	if err := RevokeInvite(ctx, st.DB(), "FRPT-AAAA-AAAA", 4); err != ErrInviteNotRevocable {
		t.Fatalf("已使用的邀请码不能撤销：%v", err)
	}
	if err := RevokeInvite(ctx, st.DB(), "FRPT-NONE-XXXX", 4); err != ErrNotFound {
		t.Fatalf("不存在的邀请码：%v", err)
	}
}

func TestBackup(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	st.Migrate(ctx)
	InsertUser(ctx, st.DB(), User{ID: "u1", Nickname: "bob", PasswordHash: "h", Role: "user", Status: "active"})
	to := filepath.Join(t.TempDir(), "bak", "backup.db")
	if err := st.Backup(ctx, to); err != nil {
		t.Fatal(err)
	}
	if err := st.Backup(ctx, to); err == nil {
		t.Fatal("目标已存在时应报错")
	}
	bak, err := Open(to)
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	if err := bak.CheckSchema(ctx); err != nil {
		t.Fatalf("备份应包含迁移记录：%v", err)
	}
	if u, err := GetUserByNickname(ctx, bak.DB(), "bob"); err != nil || u.ID != "u1" {
		t.Fatalf("备份里缺少数据：%v", err)
	}
}

func TestNewDatabaseFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && fi.Mode().Perm() != 0o600 {
		t.Fatalf("数据库文件权限应为 0600：%v", fi.Mode().Perm())
	}
}

func TestLoadMigrationsRejectsBadNames(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"no underscore": {"0001.sql": {Data: []byte("")}},
		"bad version":   {"abc_x.sql": {Data: []byte("")}},
		"duplicate":     {"0001_a.sql": {Data: []byte("")}, "0001_b.sql": {Data: []byte("")}},
	} {
		if _, err := loadMigrations(fsys); err == nil {
			t.Errorf("%s：应报错", name)
		}
	}
	ms, err := loadMigrations(fstest.MapFS{"0002_b.sql": {}, "0001_a.sql": {}, "README.md": {}})
	if err != nil || len(ms) != 2 || ms[0].Version != 1 || ms[1].Version != 2 {
		t.Fatalf("应按版本排序并忽略非 .sql 文件：%v %v", ms, err)
	}
}

func TestTxRollback(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	st.Migrate(ctx)
	errBoom := os.ErrInvalid
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		InsertUser(ctx, tx, User{ID: "u1", Nickname: "x", PasswordHash: "h", Role: "user", Status: "active"})
		return errBoom
	})
	if err != errBoom {
		t.Fatalf("应原样返回 fn 的错误：%v", err)
	}
	if _, err := GetUser(ctx, st.DB(), "u1"); err != ErrNotFound {
		t.Fatal("事务应已回滚")
	}
}
