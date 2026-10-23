package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"moefrp/server/internal/store"
)

// openExisting 打开已存在且已迁移的数据库，导入等命令不应该凭空建一个新库。
func openExisting(ctx context.Context, cfgPath string, stderr io.Writer) (*store.Store, error) {
	cfg, path, err := loadConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = "（未使用配置文件）"
	}
	fmt.Fprintf(stderr, "配置：%s  数据库：%s\n", path, cfg.DBPath)
	if err := dropPrivileges(filepath.Dir(cfg.DBPath), stderr); err != nil {
		return nil, err
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return nil, fmt.Errorf("数据库 %s 不存在，请检查 -c 或先执行 migrate：%w", cfg.DBPath, err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := st.CheckSchema(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}
