package store

import (
	"context"
	"database/sql"
	"errors"
)

// 会话吊销原因（sessions.revoke_reason）。
const (
	RevokeLogout          = "logout"
	RevokeReplaced        = "replaced"
	RevokePasswordChanged = "password_changed"
	RevokeAdmin           = "admin"
)

// 会话类型（sessions.kind）。
const (
	SessionClient   = "client"    // 桌面客户端，Authorization: Bearer
	SessionAdminWeb = "admin_web" // 管理后台网页，HttpOnly cookie
)

// Session 是 sessions 表的一行。
type Session struct {
	ID           string
	Kind         string // 空串按 client 处理
	UserID       string
	TokenHash    []byte
	InstallID    string
	DeviceName   string
	CreatedAt    int64
	LastSeenAt   int64
	ExpiresAt    int64
	RevokedAt    *int64
	RevokeReason string
}

// InsertSession 插入会话。
func InsertSession(ctx context.Context, q Querier, s Session) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, install_id, device_name, created_at, last_seen_at, expires_at, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.TokenHash, s.InstallID, nullString(s.DeviceName), s.CreatedAt, s.LastSeenAt, s.ExpiresAt, sessionKind(s.Kind))
	return err
}

// SessionWithUser 是按令牌查到的会话及其用户。
type SessionWithUser struct {
	Session Session
	User    User
}

// GetSessionByTokenHash 按令牌哈希查会话和用户（包括已吊销、已过期的会话，由调用方判断）。
func GetSessionByTokenHash(ctx context.Context, q Querier, tokenHash []byte) (SessionWithUser, error) {
	var (
		r          SessionWithUser
		deviceName sql.NullString
		revokedAt  sql.NullInt64
		reason     sql.NullString
	)
	err := q.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, s.token_hash, s.install_id, s.device_name, s.created_at, s.last_seen_at, s.expires_at,
		        s.revoked_at, s.revoke_reason, s.kind,
		        u.id, u.nickname, u.password_hash, u.role, u.status, u.state_version, u.created_at, u.updated_at
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ?`, tokenHash).Scan(
		&r.Session.ID, &r.Session.UserID, &r.Session.TokenHash, &r.Session.InstallID, &deviceName,
		&r.Session.CreatedAt, &r.Session.LastSeenAt, &r.Session.ExpiresAt, &revokedAt, &reason, &r.Session.Kind,
		&r.User.ID, &r.User.Nickname, &r.User.PasswordHash, &r.User.Role, &r.User.Status,
		&r.User.StateVersion, &r.User.CreatedAt, &r.User.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionWithUser{}, ErrNotFound
	}
	if err != nil {
		return SessionWithUser{}, err
	}
	r.Session.DeviceName = deviceName.String
	r.Session.RevokedAt = ptrInt(revokedAt)
	r.Session.RevokeReason = reason.String
	return r, nil
}

// TouchSession 更新最后使用时间并顺延过期时间（滑动过期）。已吊销的会话不更新。
func TouchSession(ctx context.Context, q Querier, sessionID string, lastSeen, expiresAt int64) error {
	_, err := q.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ? AND revoked_at IS NULL`,
		lastSeen, expiresAt, sessionID)
	return err
}

// RevokeSession 吊销单个会话，返回是否确实吊销了（已吊销的不重复处理）。
func RevokeSession(ctx context.Context, q Querier, sessionID, reason string, now int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?, revoke_reason = ? WHERE id = ? AND revoked_at IS NULL`,
		now, reason, sessionID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RevokeUserSessions 吊销用户的所有有效会话，exceptID 非空时保留该会话。返回吊销的数量。
func RevokeUserSessions(ctx context.Context, q Querier, userID, exceptID, reason string, now int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?, revoke_reason = ?
		  WHERE user_id = ? AND revoked_at IS NULL AND id != ?`,
		now, reason, userID, exceptID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RevokeUserSessionsOfKind 吊销用户某一类型的所有有效会话（单设备模式用）。返回吊销的数量。
func RevokeUserSessionsOfKind(ctx context.Context, q Querier, userID, kind, reason string, now int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?, revoke_reason = ?
		  WHERE user_id = ? AND kind = ? AND revoked_at IS NULL`,
		now, reason, userID, sessionKind(kind))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func sessionKind(k string) string {
	if k == "" {
		return SessionClient
	}
	return k
}

// CountActiveSessions 统计用户未吊销、未过期的会话数。
func CountActiveSessions(ctx context.Context, q Querier, userID string, now int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?`,
		userID, now).Scan(&n)
	return n, err
}

// LastSeen 返回用户所有会话里最近一次使用的时间，没有会话时返回 0。
func LastSeen(ctx context.Context, q Querier, userID string) (int64, error) {
	var v sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT max(last_seen_at) FROM sessions WHERE user_id = ?`, userID).Scan(&v)
	return v.Int64, err
}

// DeleteStaleSessions 删除早已过期或吊销的会话，返回删除的数量。
//
// 吊销的会话保留一段时间，让被挤下线的客户端还能收到 session_replaced，而不是 unauthorized。
func DeleteStaleSessions(ctx context.Context, q Querier, expiredBefore, revokedBefore int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)`,
		expiredBefore, revokedBefore)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SessionRevoked 判断会话是否已被吊销或已不存在。
func SessionRevoked(ctx context.Context, q Querier, sessionID string) (bool, error) {
	var revokedAt sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT revoked_at FROM sessions WHERE id = ?`, sessionID).Scan(&revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return revokedAt.Valid, nil
}
