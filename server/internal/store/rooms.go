package store

import (
	"context"
	"database/sql"
	"errors"

	"moefrp/shared/api"
)

// 联机房间（0012）：房间和成员。成员的座位号决定虚拟 IP（api.VnetIP）。

// RoomRow 是 rooms 表的一行。
type RoomRow struct {
	ID, Name, Code, OwnerID string
	CreatedAt               int64
}

// InsertRoom 建房间并把房主放进 1 号座位。
func InsertRoom(ctx context.Context, q Querier, r RoomRow) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO rooms (id, name, code, owner_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.Code, r.OwnerID, r.CreatedAt); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO room_members (room_id, user_id, slot, joined_at) VALUES (?, ?, 1, ?)`,
		r.ID, r.OwnerID, r.CreatedAt)
	return err
}

// GetRoom 按 ID 查房间。
func GetRoom(ctx context.Context, q Querier, id string) (RoomRow, error) {
	return scanRoom(q.QueryRowContext(ctx, `SELECT id, name, code, owner_id, created_at FROM rooms WHERE id = ?`, id))
}

// GetRoomByCode 按邀请码查房间。
func GetRoomByCode(ctx context.Context, q Querier, code string) (RoomRow, error) {
	return scanRoom(q.QueryRowContext(ctx, `SELECT id, name, code, owner_id, created_at FROM rooms WHERE code = ?`, code))
}

func scanRoom(row *sql.Row) (RoomRow, error) {
	var r RoomRow
	err := row.Scan(&r.ID, &r.Name, &r.Code, &r.OwnerID, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RoomRow{}, ErrNotFound
	}
	return r, err
}

// DeleteRoom 解散房间（成员一起删）。
func DeleteRoom(ctx context.Context, q Querier, id string) error {
	return execOne(ctx, q, `DELETE FROM rooms WHERE id = ?`, id)
}

// CountUserRooms 返回用户加入的房间数。
func CountUserRooms(ctx context.Context, q Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// AddRoomMember 把用户放进最小的空座位，返回座位号；已经在房间里时返回原来的座位。
// 满员（api.VnetMaxMembers）时返回 0。
func AddRoomMember(ctx context.Context, q Querier, roomID, userID string, now int64) (int, error) {
	if slot, err := RoomMemberSlot(ctx, q, roomID, userID); err == nil {
		return slot, nil
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT slot FROM room_members WHERE room_id = ?`, roomID)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for rows.Next() {
		var s int
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return 0, err
		}
		used[s] = true
	}
	rows.Close()
	if len(used) >= api.VnetMaxMembers {
		return 0, nil
	}
	slot := 1
	for used[slot] {
		slot++
	}
	_, err = q.ExecContext(ctx, `INSERT INTO room_members (room_id, user_id, slot, joined_at) VALUES (?, ?, ?, ?)`, roomID, userID, slot, now)
	return slot, err
}

// RoomMemberSlot 返回用户在房间里的座位号，不在房间里时返回 ErrNotFound。
func RoomMemberSlot(ctx context.Context, q Querier, roomID, userID string) (int, error) {
	var slot int
	err := q.QueryRowContext(ctx, `SELECT slot FROM room_members WHERE room_id = ? AND user_id = ?`, roomID, userID).Scan(&slot)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return slot, err
}

// RemoveRoomMember 把用户移出房间。
func RemoveRoomMember(ctx context.Context, q Querier, roomID, userID string) error {
	return execOne(ctx, q, `DELETE FROM room_members WHERE room_id = ? AND user_id = ?`, roomID, userID)
}

// RoomMembers 返回房间成员（按座位号）。Online 由调用方填。
func RoomMembers(ctx context.Context, q Querier, roomID string) ([]api.RoomMember, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.user_id, u.nickname, m.slot FROM room_members m JOIN users u ON u.id = m.user_id
		WHERE m.room_id = ? ORDER BY m.slot`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.RoomMember{}
	for rows.Next() {
		var m api.RoomMember
		if err := rows.Scan(&m.UserID, &m.Nickname, &m.Slot); err != nil {
			return nil, err
		}
		m.IP = api.VnetIP(m.Slot)
		out = append(out, m)
	}
	return out, rows.Err()
}

// UserRooms 返回用户加入的房间（不含成员）。
func UserRooms(ctx context.Context, q Querier, userID string) ([]api.Room, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.id, r.name, r.code, r.owner_id, u.nickname, r.created_at
		FROM rooms r JOIN room_members m ON m.room_id = r.id JOIN users u ON u.id = r.owner_id
		WHERE m.user_id = ? ORDER BY m.joined_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Room{}
	for rows.Next() {
		var r api.Room
		if err := rows.Scan(&r.ID, &r.Name, &r.Code, &r.OwnerID, &r.OwnerName, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
