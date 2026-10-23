package store

import (
	"context"
	"database/sql"
	"errors"
)

// User 是 users 表的一行。时间都是 Unix 秒。
type User struct {
	ID           string
	Nickname     string
	PasswordHash string
	Role         string
	Status       string
	StateVersion int64
	CreatedAt    int64
	UpdatedAt    int64
}

const userColumns = `id, nickname, password_hash, role, status, state_version, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Nickname, &u.PasswordHash, &u.Role, &u.Status, &u.StateVersion, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// InsertUser 插入用户。昵称冲突时返回的错误满足 IsUniqueViolation。
func InsertUser(ctx context.Context, q Querier, u User) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Nickname, u.PasswordHash, u.Role, u.Status, u.StateVersion, u.CreatedAt, u.UpdatedAt)
	return err
}

// GetUser 按 ID 查用户。
func GetUser(ctx context.Context, q Querier, id string) (User, error) {
	return scanUser(q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// GetUserByNickname 按昵称查用户，不区分大小写（列定义为 COLLATE NOCASE）。
func GetUserByNickname(ctx context.Context, q Querier, nickname string) (User, error) {
	return scanUser(q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE nickname = ?`, nickname))
}

// ListUsers 按注册时间列出所有用户。
func ListUsers(ctx context.Context, q Querier) ([]User, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at, nickname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePasswordHash 更新密码哈希。
func UpdatePasswordHash(ctx context.Context, q Querier, userID, hash string, now int64) error {
	return expectOne(q.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, now, userID))
}

// ReplacePasswordHash 只在哈希仍是 oldHash 时替换，用于登录后的自动重新哈希：
// 如果期间密码被改过，就不覆盖。返回是否替换成功。
func ReplacePasswordHash(ctx context.Context, q Querier, userID, oldHash, newHash string, now int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ? AND password_hash = ?`,
		newHash, now, userID, oldHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetUserRole 修改角色，同时让 stateVersion 加 1，客户端下次同步时就能看到新角色。
func SetUserRole(ctx context.Context, q Querier, userID, role string, now int64) error {
	return expectOne(q.ExecContext(ctx,
		`UPDATE users SET role = ?, state_version = state_version + 1, updated_at = ? WHERE id = ?`, role, now, userID))
}

// SetUserStatus 修改状态（active / disabled）。
func SetUserStatus(ctx context.Context, q Querier, userID, status string, now int64) error {
	return expectOne(q.ExecContext(ctx,
		`UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, now, userID))
}

// BumpStateVersion 让用户的 stateVersion 加 1 并返回新值。
func BumpStateVersion(ctx context.Context, q Querier, userID string) (int64, error) {
	var v int64
	err := q.QueryRowContext(ctx,
		`UPDATE users SET state_version = state_version + 1 WHERE id = ? RETURNING state_version`, userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return v, err
}

// expectOne 检查 UPDATE / DELETE 恰好影响一行，否则返回 ErrNotFound。
func expectOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
