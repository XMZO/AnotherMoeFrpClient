package store

import (
	"context"
	"database/sql"
	"errors"
)

// ResetCode 是 password_reset_codes 表的一行。库里只存重置码的哈希。
type ResetCode struct {
	CodeHash  []byte
	UserID    string
	CreatedAt int64
	ExpiresAt int64
	UsedAt    *int64
}

// ReplaceResetCode 删除该用户还没用过的旧重置码，再插入新的（同一时间只有一个有效）。
func ReplaceResetCode(ctx context.Context, q Querier, rc ResetCode) error {
	if _, err := q.ExecContext(ctx,
		`DELETE FROM password_reset_codes WHERE user_id = ? AND used_at IS NULL`, rc.UserID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO password_reset_codes (code_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		rc.CodeHash, rc.UserID, rc.CreatedAt, rc.ExpiresAt)
	return err
}

// GetResetCode 按哈希查重置码。
func GetResetCode(ctx context.Context, q Querier, codeHash []byte) (ResetCode, error) {
	var (
		rc     ResetCode
		usedAt sql.NullInt64
	)
	err := q.QueryRowContext(ctx,
		`SELECT code_hash, user_id, created_at, expires_at, used_at FROM password_reset_codes WHERE code_hash = ?`,
		codeHash).Scan(&rc.CodeHash, &rc.UserID, &rc.CreatedAt, &rc.ExpiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ResetCode{}, ErrNotFound
	}
	rc.UsedAt = ptrInt(usedAt)
	return rc, err
}

// UseResetCode 原子地把重置码标记为已用，返回是否成功（未用过且未过期）。
func UseResetCode(ctx context.Context, q Querier, codeHash []byte, now int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE password_reset_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, codeHash, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DeleteStaleResetCodes 删除过期或已用的重置码。
func DeleteStaleResetCodes(ctx context.Context, q Querier, before int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		`DELETE FROM password_reset_codes WHERE expires_at < ? OR (used_at IS NOT NULL AND used_at < ?)`, before, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
