package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"moefrp/server/migrations"
)

// Migration 是一个嵌入的迁移文件。
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations 读取并按版本号排序嵌入的迁移文件。
func LoadMigrations() ([]Migration, error) {
	return loadMigrations(migrations.FS)
}

func loadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("store: migration %s: name must be NNNN_description.sql", name)
		}
		v, err := strconv.Atoi(num)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("store: migration %s: bad version", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("store: migrations %s and %s share version %d", prev, name, v)
		}
		seen[v] = name
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// AppliedVersions 返回已执行的迁移版本。schema_migrations 表还不存在时返回空。
func (s *Store) AppliedVersions(ctx context.Context) (map[int]bool, error) {
	return appliedVersions(ctx, s.db)
}

func appliedVersions(ctx context.Context, q Querier) (map[int]bool, error) {
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&n); err != nil {
		return nil, err
	}
	out := map[int]bool{}
	if n == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// Pending 返回尚未执行的迁移。
func (s *Store) Pending(ctx context.Context) ([]Migration, error) {
	all, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	applied, err := s.AppliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, m := range all {
		if !applied[m.Version] {
			out = append(out, m)
		}
	}
	return out, nil
}

// Migrate 依次执行未执行的迁移，每个迁移一个事务，返回本次执行的迁移。
//
// 0001 本身会创建 schema_migrations 表，所以第一次迁移时先建表再写记录，
// 两者在同一个事务里（SQLite 的 DDL 是事务性的）。
func (s *Store) Migrate(ctx context.Context) ([]Migration, error) {
	all, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	var done []Migration
	for _, m := range all {
		err := s.Tx(ctx, func(tx *sql.Tx) error {
			applied, err := appliedVersions(ctx, tx)
			if err != nil {
				return err
			}
			if applied[m.Version] {
				return errSkip
			}
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return fmt.Errorf("store: migration %s: %w", m.Name, err)
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, m.Version, time.Now().Unix())
			return err
		})
		if err == errSkip {
			continue
		}
		if err != nil {
			return done, err
		}
		done = append(done, m)
	}
	return done, nil
}

var errSkip = fmt.Errorf("store: migration already applied")

// CheckSchema 确认所有迁移都已执行，serve 启动时调用。
func (s *Store) CheckSchema(ctx context.Context) error {
	pending, err := s.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		names := make([]string, len(pending))
		for i, m := range pending {
			names[i] = m.Name
		}
		return fmt.Errorf("数据库有 %d 个未执行的迁移（%s），请先备份再执行 moefrp-server migrate", len(pending), strings.Join(names, ", "))
	}
	return nil
}
