// Package migrations 把 *.sql 迁移文件嵌入二进制。
//
// 文件名格式为 NNNN_说明.sql，按编号顺序执行，每个文件在一个事务里执行，
// 执行记录写入 schema_migrations 表（由 0001 创建）。已发布的迁移文件不要再修改，
// 改表结构一律新增文件。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
