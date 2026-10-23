package store

import (
	"context"
	"database/sql"
	"errors"
)

// ShareRow 是 shares 表的一行。Payload 是 model.SharePayload 的 JSON（含机密）。
type ShareRow struct {
	ID              string
	OwnerID         string
	Name            string
	Kind            string
	Payload         string
	SourceProfileID string
	Revision        int64
	ExpiresAt       *int64
	MaxSubscribers  int // 0 表示不限
	RevokedAt       *int64
	CreatedAt       int64
	UpdatedAt       int64
}

// Expired 判断分享在 now 时是否已过期。
func (s ShareRow) Expired(now int64) bool { return s.ExpiresAt != nil && *s.ExpiresAt <= now }

// Revoked 判断分享是否已撤销。
func (s ShareRow) Revoked() bool { return s.RevokedAt != nil }

const shareColumns = `id, owner_id, name, kind, payload, source_profile_id, revision, expires_at, max_subscribers, revoked_at, created_at, updated_at`

func scanShare(row interface{ Scan(...any) error }) (ShareRow, error) {
	var (
		s       ShareRow
		src     sql.NullString
		exp     sql.NullInt64
		max     sql.NullInt64
		revoked sql.NullInt64
	)
	err := row.Scan(&s.ID, &s.OwnerID, &s.Name, &s.Kind, &s.Payload, &src, &s.Revision, &exp, &max, &revoked, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ShareRow{}, ErrNotFound
	}
	s.SourceProfileID = src.String
	s.ExpiresAt = ptrInt(exp)
	s.MaxSubscribers = int(max.Int64)
	s.RevokedAt = ptrInt(revoked)
	return s, err
}

func nullPositive(n int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(n), Valid: n > 0}
}

// InsertShare 插入分享。ID 冲突时返回的错误满足 IsUniqueViolation。
func InsertShare(ctx context.Context, q Querier, s ShareRow) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO shares (`+shareColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.OwnerID, s.Name, s.Kind, s.Payload, nullString(s.SourceProfileID), s.Revision,
		nullInt(s.ExpiresAt), nullPositive(s.MaxSubscribers), nullInt(s.RevokedAt), s.CreatedAt, s.UpdatedAt)
	return err
}

// GetShare 按 ID 查分享（不限所有者）。
func GetShare(ctx context.Context, q Querier, id string) (ShareRow, error) {
	return scanShare(q.QueryRowContext(ctx, `SELECT `+shareColumns+` FROM shares WHERE id = ?`, id))
}

// ListShares 列出用户创建的分享（含已撤销的），新的在前。
func ListShares(ctx context.Context, q Querier, ownerID string) ([]ShareRow, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+shareColumns+` FROM shares WHERE owner_id = ? ORDER BY created_at DESC, id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShareRow
	for rows.Next() {
		s, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountActiveShares 统计用户未撤销的分享数（数量上限按它算）。
func CountActiveShares(ctx context.Context, q Querier, ownerID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM shares WHERE owner_id = ? AND revoked_at IS NULL`, ownerID).Scan(&n)
	return n, err
}

// UpdateShareMeta 修改名称、过期时间、人数上限。
func UpdateShareMeta(ctx context.Context, q Querier, s ShareRow, now int64) error {
	return expectOne(q.ExecContext(ctx,
		`UPDATE shares SET name = ?, expires_at = ?, max_subscribers = ?, updated_at = ? WHERE id = ? AND owner_id = ?`,
		s.Name, nullInt(s.ExpiresAt), nullPositive(s.MaxSubscribers), now, s.ID, s.OwnerID))
}

// UpdateSharePayload 替换快照并让 revision 加 1，返回新的 revision。
func UpdateSharePayload(ctx context.Context, q Querier, id, ownerID, payload string, now int64) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx,
		`UPDATE shares SET payload = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND owner_id = ? RETURNING revision`,
		payload, now, id, ownerID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return rev, err
}

// RevokeShare 软删除分享，并让 revision 加 1（订阅者据此发现变化）。已撤销时返回 ErrNotFound。
func RevokeShare(ctx context.Context, q Querier, id, ownerID string, now int64) error {
	return expectOne(q.ExecContext(ctx,
		`UPDATE shares SET revoked_at = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND owner_id = ? AND revoked_at IS NULL`,
		now, now, id, ownerID))
}

// ---------- subscriptions ----------

// SubscriptionRow 是 subscriptions 表的一行。Params 是 model.SubscriptionParams 的 JSON。
type SubscriptionRow struct {
	ID        string
	UserID    string
	ShareID   string
	Params    string
	Revision  int64
	CreatedAt int64
	UpdatedAt int64
}

const subscriptionColumns = `id, user_id, share_id, params, revision, created_at, updated_at`

func scanSubscription(row interface{ Scan(...any) error }) (SubscriptionRow, error) {
	var s SubscriptionRow
	err := row.Scan(&s.ID, &s.UserID, &s.ShareID, &s.Params, &s.Revision, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionRow{}, ErrNotFound
	}
	return s, err
}

// InsertSubscription 插入订阅。同一用户重复订阅同一分享时返回的错误满足 IsUniqueViolation。
func InsertSubscription(ctx context.Context, q Querier, s SubscriptionRow) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO subscriptions (`+subscriptionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.ShareID, s.Params, s.Revision, s.CreatedAt, s.UpdatedAt)
	return err
}

// GetOwnedSubscription 查属于 userID 的订阅。
func GetOwnedSubscription(ctx context.Context, q Querier, userID, id string) (SubscriptionRow, error) {
	return scanSubscription(q.QueryRowContext(ctx,
		`SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = ? AND user_id = ?`, id, userID))
}

// FindSubscription 查 userID 对 shareID 的订阅。
func FindSubscription(ctx context.Context, q Querier, userID, shareID string) (SubscriptionRow, error) {
	return scanSubscription(q.QueryRowContext(ctx,
		`SELECT `+subscriptionColumns+` FROM subscriptions WHERE user_id = ? AND share_id = ?`, userID, shareID))
}

// ListSubscriptions 列出用户的全部订阅。
func ListSubscriptions(ctx context.Context, q Querier, userID string) ([]SubscriptionRow, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+subscriptionColumns+` FROM subscriptions WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscriptionRow
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountSubscriptions 统计用户的订阅数。
func CountSubscriptions(ctx context.Context, q Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM subscriptions WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// CountShareSubscribers 统计分享的订阅人数。
func CountShareSubscribers(ctx context.Context, q Querier, shareID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM subscriptions WHERE share_id = ?`, shareID).Scan(&n)
	return n, err
}

// ListShareSubscriberIDs 返回订阅了分享的用户 ID，用于分享变化时让他们的 stateVersion 加 1。
func ListShareSubscriberIDs(ctx context.Context, q Querier, shareID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id FROM subscriptions WHERE share_id = ?`, shareID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpdateSubscriptionIfRevision 只在 revision 仍是 baseRevision 时更新参数。
func UpdateSubscriptionIfRevision(ctx context.Context, q Querier, userID, id string, baseRevision int64, params string, now int64) (SubscriptionRow, bool, error) {
	s, err := scanSubscription(q.QueryRowContext(ctx,
		`UPDATE subscriptions SET params = ?, revision = revision + 1, updated_at = ?
		  WHERE id = ? AND user_id = ? AND revision = ?
		 RETURNING `+subscriptionColumns,
		params, now, id, userID, baseRevision))
	if errors.Is(err, ErrNotFound) {
		return SubscriptionRow{}, false, nil
	}
	if err != nil {
		return SubscriptionRow{}, false, err
	}
	return s, true, nil
}

// DeleteSubscription 删除属于 userID 的订阅。
func DeleteSubscription(ctx context.Context, q Querier, userID, id string) error {
	return expectOne(q.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = ? AND user_id = ?`, id, userID))
}
