package store

import (
	"context"
	"database/sql"
	"errors"
)

// ProfileRow 是 profiles 表的一行。Data 是 {nodes, proxies, visitors} 的 JSON。
type ProfileRow struct {
	ID            string
	OwnerID       string
	Name          string
	Data          string
	SchemaVersion int
	Revision      int64
	CreatedAt     int64
	UpdatedAt     int64
}

// CurrentProfileSchema 是 profiles.data 当前的结构版本。
const CurrentProfileSchema = 1

const profileColumns = `id, owner_id, name, data, schema_version, revision, created_at, updated_at`

func scanProfile(row interface{ Scan(...any) error }) (ProfileRow, error) {
	var p ProfileRow
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Data, &p.SchemaVersion, &p.Revision, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileRow{}, ErrNotFound
	}
	return p, err
}

// InsertProfile 插入配置。ID 冲突时返回的错误满足 IsUniqueViolation。
func InsertProfile(ctx context.Context, q Querier, p ProfileRow) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO profiles (`+profileColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.OwnerID, p.Name, p.Data, p.SchemaVersion, p.Revision, p.CreatedAt, p.UpdatedAt)
	return err
}

// GetProfile 按 ID 查配置，不限定所有者（调用方自己检查）。
func GetProfile(ctx context.Context, q Querier, id string) (ProfileRow, error) {
	return scanProfile(q.QueryRowContext(ctx, `SELECT `+profileColumns+` FROM profiles WHERE id = ?`, id))
}

// GetOwnedProfile 查属于 ownerID 的配置；不属于时同样返回 ErrNotFound。
func GetOwnedProfile(ctx context.Context, q Querier, ownerID, id string) (ProfileRow, error) {
	return scanProfile(q.QueryRowContext(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE id = ? AND owner_id = ?`, id, ownerID))
}

// ListProfiles 按创建顺序列出用户的全部配置。
func ListProfiles(ctx context.Context, q Querier, ownerID string) ([]ProfileRow, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE owner_id = ? ORDER BY created_at, id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileRow
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountProfiles 统计用户的配置数。
func CountProfiles(ctx context.Context, q Querier, ownerID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM profiles WHERE owner_id = ?`, ownerID).Scan(&n)
	return n, err
}

// UpdateProfileIfRevision 只在云端版本仍是 baseRevision 时更新，并让 revision 加 1。
// 返回更新后的行；版本不一致或不存在时 ok 为 false。
func UpdateProfileIfRevision(ctx context.Context, q Querier, ownerID, id string, baseRevision int64, name, data string, now int64) (ProfileRow, bool, error) {
	p, err := scanProfile(q.QueryRowContext(ctx,
		`UPDATE profiles SET name = ?, data = ?, schema_version = ?, revision = revision + 1, updated_at = ?
		  WHERE id = ? AND owner_id = ? AND revision = ?
		 RETURNING `+profileColumns,
		name, data, CurrentProfileSchema, now, id, ownerID, baseRevision))
	if errors.Is(err, ErrNotFound) {
		return ProfileRow{}, false, nil
	}
	if err != nil {
		return ProfileRow{}, false, err
	}
	return p, true, nil
}

// DeleteProfile 删除属于 ownerID 的配置，不存在时返回 ErrNotFound。
func DeleteProfile(ctx context.Context, q Querier, ownerID, id string) error {
	return expectOne(q.ExecContext(ctx, `DELETE FROM profiles WHERE id = ? AND owner_id = ?`, id, ownerID))
}
