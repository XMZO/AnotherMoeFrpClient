package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting 读取设置，不存在时返回 ok = false。
func GetSetting(ctx context.Context, q Querier, key string) (value string, ok bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting 写入设置。
func SetSetting(ctx context.Context, q Querier, key, value string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// InsertSettingIfAbsent 只在不存在时写入，返回最终的值（并发启动时只有一个生效）。
func InsertSettingIfAbsent(ctx context.Context, q Querier, key, value string) (string, error) {
	if _, err := q.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, key, value); err != nil {
		return "", err
	}
	v, _, err := GetSetting(ctx, q, key)
	return v, err
}

// NodeOfficial 是节点的官方节点设置。
type NodeOfficial struct {
	Official    bool
	Roles       string // JSON 数组
	Description string
}

// SetNodeOfficial 修改节点的官方节点设置。
func SetNodeOfficial(ctx context.Context, q Querier, id string, o NodeOfficial, now int64) error {
	return execOne(ctx, q, `UPDATE nodes SET official = ?, roles = ?, description = ?, updated_at = ? WHERE id = ?`,
		o.Official, o.Roles, o.Description, now, id)
}

// AccessUser 是计算授权表需要的用户字段。
type AccessUser struct {
	ID        string
	Nickname  string
	Role      string
	Status    string
	CredGen   int64
	QuotaJSON string
}

const accessUserColumns = `id, nickname, role, status, node_cred_gen, node_quota`

func scanAccessUser(row interface{ Scan(...any) error }) (AccessUser, error) {
	var u AccessUser
	err := row.Scan(&u.ID, &u.Nickname, &u.Role, &u.Status, &u.CredGen, &u.QuotaJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// ListAccessUsers 列出所有正常状态的用户。
func ListAccessUsers(ctx context.Context, q Querier) ([]AccessUser, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+accessUserColumns+` FROM users WHERE status = 'active' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessUser
	for rows.Next() {
		u, err := scanAccessUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetAccessUser 按 ID 查用户的节点相关字段。
func GetAccessUser(ctx context.Context, q Querier, id string) (AccessUser, error) {
	return scanAccessUser(q.QueryRowContext(ctx, `SELECT `+accessUserColumns+` FROM users WHERE id = ?`, id))
}

// SetUserNodeQuota 写入用户的限额覆盖（JSON，空串表示不覆盖）。
func SetUserNodeQuota(ctx context.Context, q Querier, userID, quotaJSON string) error {
	return execOne(ctx, q, `UPDATE users SET node_quota = ? WHERE id = ?`, quotaJSON, userID)
}

// BumpUserNodeCred 让用户的节点凭证换代（旧凭证立即失效）。
func BumpUserNodeCred(ctx context.Context, q Querier, userID string) error {
	return execOne(ctx, q, `UPDATE users SET node_cred_gen = node_cred_gen + 1 WHERE id = ?`, userID)
}

// Grant 是一条单独授权。
type Grant struct {
	UserID string
	NodeID string
	Allow  bool
}

// ListGrants 列出所有单独授权。
func ListGrants(ctx context.Context, q Querier) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id, node_id, allow FROM node_grants`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.UserID, &g.NodeID, &g.Allow); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGrant 设置单独授权；allow 为 nil 时删除（回到按角色）。
func SetGrant(ctx context.Context, q Querier, userID, nodeID string, allow *bool) error {
	if allow == nil {
		_, err := q.ExecContext(ctx, `DELETE FROM node_grants WHERE user_id = ? AND node_id = ?`, userID, nodeID)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO node_grants (user_id, node_id, allow) VALUES (?, ?, ?)
		ON CONFLICT (user_id, node_id) DO UPDATE SET allow = excluded.allow`, userID, nodeID, *allow)
	return err
}
