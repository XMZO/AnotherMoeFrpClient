package store

import (
	"context"
	"database/sql"
	"errors"
)

// Invite 是 invite_codes 表的一行，UsedByNickname 由查询时关联得到。
type Invite struct {
	Code           string
	Note           string
	CreatedAt      int64
	UsedBy         string
	UsedByNickname string
	UsedAt         *int64
	RevokedAt      *int64
}

// InsertInvite 插入邀请码。重复时返回的错误满足 IsUniqueViolation。
func InsertInvite(ctx context.Context, q Querier, code, note string, now int64) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO invite_codes (code, note, created_at) VALUES (?, ?, ?)`, code, nullString(note), now)
	return err
}

// ClaimInvite 原子地占用邀请码，返回是否成功（PLAN 7.4）。
//
// 必须和创建用户放在同一个事务里，并检查影响的行数：并发注册时只有一个事务能把
// used_by 从 NULL 改成自己。在 PLAN 的条件之外还加了 used_at IS NULL：
// used_by 是 ON DELETE SET NULL，用户被删除后不能让邀请码重新变得可用。
func ClaimInvite(ctx context.Context, q Querier, code, userID string, now int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE invite_codes SET used_by = ?, used_at = ?
		  WHERE code = ? AND used_by IS NULL AND used_at IS NULL AND revoked_at IS NULL`,
		userID, now, code)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// GetInvite 查单个邀请码。
func GetInvite(ctx context.Context, q Querier, code string) (Invite, error) {
	rows, err := queryInvites(ctx, q, `WHERE i.code = ?`, code)
	if err != nil {
		return Invite{}, err
	}
	if len(rows) == 0 {
		return Invite{}, ErrNotFound
	}
	return rows[0], nil
}

// ListInvites 列出邀请码；unusedOnly 时只列出未使用且未撤销的。
func ListInvites(ctx context.Context, q Querier, unusedOnly bool) ([]Invite, error) {
	where := ``
	if unusedOnly {
		where = `WHERE i.used_at IS NULL AND i.revoked_at IS NULL`
	}
	return queryInvites(ctx, q, where)
}

func queryInvites(ctx context.Context, q Querier, where string, args ...any) ([]Invite, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT i.code, i.note, i.created_at, i.used_by, u.nickname, i.used_at, i.revoked_at
		   FROM invite_codes i LEFT JOIN users u ON u.id = i.used_by `+where+`
		  ORDER BY i.created_at, i.code`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var (
			inv              Invite
			note, by, byNick sql.NullString
			usedAt, revoked  sql.NullInt64
		)
		if err := rows.Scan(&inv.Code, &note, &inv.CreatedAt, &by, &byNick, &usedAt, &revoked); err != nil {
			return nil, err
		}
		inv.Note, inv.UsedBy, inv.UsedByNickname = note.String, by.String, byNick.String
		inv.UsedAt, inv.RevokedAt = ptrInt(usedAt), ptrInt(revoked)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvite 撤销未使用的邀请码。邀请码不存在时返回 ErrNotFound；
// 已使用或已撤销时返回 ErrInviteNotRevocable。
func RevokeInvite(ctx context.Context, q Querier, code string, now int64) error {
	res, err := q.ExecContext(ctx,
		`UPDATE invite_codes SET revoked_at = ? WHERE code = ? AND used_at IS NULL AND revoked_at IS NULL`, now, code)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, err := GetInvite(ctx, q, code); err != nil {
		return err
	}
	return ErrInviteNotRevocable
}

// ErrInviteNotRevocable 表示邀请码已使用或已撤销。
var ErrInviteNotRevocable = errors.New("store: invite already used or revoked")
