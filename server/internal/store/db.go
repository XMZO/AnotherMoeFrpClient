// Package store 负责 SQLite 连接、迁移和各表的查询（PLAN 7.3）。
//
// 查询函数接受 Querier，既可以传 *sql.DB 也可以传 *sql.Tx，
// 业务层据此把多步操作放进同一个事务。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrNotFound 表示查询的行不存在。
var ErrNotFound = errors.New("store: not found")

// Querier 是 *sql.DB 和 *sql.Tx 的公共部分。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// pragmas 在每个新连接上执行（PLAN 7.3）。busy_timeout 由驱动保证最先执行。
var pragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"foreign_keys(ON)",
	"synchronous(NORMAL)",
}

// Store 包装一个 SQLite 连接池。
//
// 写事务用 BEGIN IMMEDIATE（DSN 的 _txlock=immediate），一开始就拿写锁，
// 避免 WAL 模式下"先读后写"的事务在升级锁时直接得到 SQLITE_BUSY；
// 只读事务（sql.TxOptions{ReadOnly: true}）仍是普通的 BEGIN，不阻塞写入。
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（必要时创建）数据库文件。不会执行迁移，见 Migrate。
//
// 新建的数据库文件权限为 0600（Windows 上无效）。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create directory: %w", err)
		}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("store: create database file: %w", err)
		}
		if f != nil {
			f.Close()
		}
	}

	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// 空闲连接保留几条即可；SQLite 的并发写由文件锁串行化。
	db.SetMaxIdleConns(4)
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return &Store{db: db, path: path}, nil
}

// DB 返回底层连接池。
func (s *Store) DB() *sql.DB { return s.db }

// Path 返回数据库文件路径。
func (s *Store) Path() string { return s.path }

// Close 关闭连接池。
func (s *Store) Close() error { return s.db.Close() }

// Tx 在写事务里执行 fn：fn 返回 nil 时提交，否则回滚并原样返回 fn 的错误。
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.tx(ctx, nil, fn)
}

// ReadTx 在只读事务里执行 fn，事务内看到的是同一个快照。
func (s *Store) ReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.tx(ctx, &sql.TxOptions{ReadOnly: true}, fn)
}

func (s *Store) tx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
		if err != nil {
			tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Ping 检查数据库是否可用，供 /healthz 使用。
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Backup 用 VACUUM INTO 把数据库完整复制到 to。目标文件已存在时报错。
func (s *Store) Backup(ctx context.Context, to string) error {
	if to == "" {
		return errors.New("store: empty backup path")
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("store: backup target %s already exists", to)
	}
	if dir := filepath.Dir(to); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("store: create backup directory: %w", err)
		}
	}
	// 先用 0600 建一个空文件再 VACUUM INTO（SQLite 接受已存在的空文件），
	// 备份里有密码哈希和节点 token，不能有任何时刻按 umask 变成所有人可读。
	f, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("store: create backup file: %w", err)
	}
	f.Close()
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", to); err != nil {
		os.Remove(to)
		return fmt.Errorf("store: backup: %w", err)
	}
	return nil
}

// IsUniqueViolation 判断错误是否是 UNIQUE / PRIMARY KEY 约束冲突。
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return true
		}
	}
	return false
}

// IsBusy 判断错误是否是数据库忙（busy_timeout 到期仍拿不到锁）。
func IsBusy(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()&0xff == sqlite3.SQLITE_BUSY || se.Code()&0xff == sqlite3.SQLITE_LOCKED
	}
	return strings.Contains(err.Error(), "database is locked")
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func ptrInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
