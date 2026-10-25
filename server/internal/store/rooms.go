package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"moefrp/shared/api"
)

// 联机房间（0012）：房间和成员。成员的座位号决定虚拟 IP（api.VnetIP）。

// RoomRow 是 rooms 表的一行。
type RoomRow struct {
	ID, Name, Code, OwnerID string
	CreatedAt               int64
	api.RoomSettings
}

const roomColumns = `id, name, code, owner_id, created_at, allow_guests, relay, relay_locked, no_voice, voice_muted`

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
	return scanRoom(q.QueryRowContext(ctx, `SELECT `+roomColumns+` FROM rooms WHERE id = ?`, id))
}

// GetRoomByCode 按邀请码查房间。
func GetRoomByCode(ctx context.Context, q Querier, code string) (RoomRow, error) {
	return scanRoom(q.QueryRowContext(ctx, `SELECT `+roomColumns+` FROM rooms WHERE code = ?`, code))
}

func scanRoom(row *sql.Row) (RoomRow, error) {
	var r RoomRow
	var muted string
	err := row.Scan(&r.ID, &r.Name, &r.Code, &r.OwnerID, &r.CreatedAt, &r.AllowGuests, &r.Relay, &r.RelayLocked, &r.NoVoice, &muted)
	if errors.Is(err, sql.ErrNoRows) {
		return RoomRow{}, ErrNotFound
	}
	r.Muted = parseSlots(muted)
	return r, err
}

// parseSlots / joinSlots：座位号列表和 "3,5" 互转。
func parseSlots(s string) []int {
	out := []int{}
	for _, f := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func joinSlots(slots []int) string {
	parts := make([]string, 0, len(slots))
	for _, n := range slots {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
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
	slot, err := freeSlot(ctx, q, roomID)
	if err != nil || slot == 0 {
		return 0, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO room_members (room_id, user_id, slot, joined_at) VALUES (?, ?, ?, ?)`, roomID, userID, slot, now)
	return slot, err
}

// freeSlot 返回最小的空座位（成员和游客共用），满员时返回 0。
func freeSlot(ctx context.Context, q Querier, roomID string) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT slot FROM room_members WHERE room_id = ? UNION ALL SELECT slot FROM room_guests WHERE room_id = ?`, roomID, roomID)
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
	return slot, nil
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

// RoomMembers 返回房间成员和游客（按座位号；游客的 UserID 是 api.GuestPrefix + 游客 ID）。Online 由调用方填。
func RoomMembers(ctx context.Context, q Querier, roomID string) ([]api.RoomMember, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.user_id, u.nickname, m.slot FROM room_members m JOIN users u ON u.id = m.user_id
		WHERE m.room_id = ?
		UNION ALL SELECT ? || g.id, g.nickname || '（游客）', g.slot FROM room_guests g WHERE g.room_id = ?
		ORDER BY 3`, roomID, api.GuestPrefix, roomID)
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
	rows, err := q.QueryContext(ctx, `SELECT r.id, r.name, r.code, r.owner_id, u.nickname, r.created_at, r.allow_guests, r.relay, r.relay_locked, r.no_voice, r.voice_muted
		FROM rooms r JOIN room_members m ON m.room_id = r.id JOIN users u ON u.id = r.owner_id
		WHERE m.user_id = ? ORDER BY m.joined_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Room{}
	for rows.Next() {
		var r api.Room
		var muted string
		if err := rows.Scan(&r.ID, &r.Name, &r.Code, &r.OwnerID, &r.OwnerName, &r.CreatedAt, &r.AllowGuests, &r.Relay, &r.RelayLocked, &r.NoVoice, &muted); err != nil {
			return nil, err
		}
		r.Muted = parseSlots(muted)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRoomCode 换邀请码。
func SetRoomCode(ctx context.Context, q Querier, id, code string) error {
	return execOne(ctx, q, `UPDATE rooms SET code = ? WHERE id = ?`, code, id)
}

// SetRoomName 改房间名。
func SetRoomName(ctx context.Context, q Querier, id, name string) error {
	return execOne(ctx, q, `UPDATE rooms SET name = ? WHERE id = ?`, name, id)
}

// SetRoomSettings 保存房主的设置。
func SetRoomSettings(ctx context.Context, q Querier, id string, st api.RoomSettings) error {
	return execOne(ctx, q, `UPDATE rooms SET allow_guests = ?, relay = ?, relay_locked = ?, no_voice = ?, voice_muted = ? WHERE id = ?`,
		st.AllowGuests, st.Relay, st.RelayLocked, st.NoVoice, joinSlots(st.Muted), id)
}

// GuestRow 是一个游客。
type GuestRow struct {
	ID, RoomID, Nickname string
	Slot                 int
}

// InsertGuest 把游客放进最小的空座位，返回座位号；满员时返回 0（不插入）。
func InsertGuest(ctx context.Context, q Querier, g GuestRow, tokenHash string, now int64) (int, error) {
	slot, err := freeSlot(ctx, q, g.RoomID)
	if err != nil || slot == 0 {
		return 0, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO room_guests (id, room_id, nickname, token_hash, slot, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		g.ID, g.RoomID, g.Nickname, tokenHash, slot, now)
	return slot, err
}

// GuestByToken 按令牌哈希找游客。
func GuestByToken(ctx context.Context, q Querier, tokenHash string) (GuestRow, error) {
	var g GuestRow
	err := q.QueryRowContext(ctx, `SELECT id, room_id, nickname, slot FROM room_guests WHERE token_hash = ?`, tokenHash).
		Scan(&g.ID, &g.RoomID, &g.Nickname, &g.Slot)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestRow{}, ErrNotFound
	}
	return g, err
}

// GuestSlot 返回游客在房间里的座位号。
func GuestSlot(ctx context.Context, q Querier, roomID, guestID string) (int, error) {
	var slot int
	err := q.QueryRowContext(ctx, `SELECT slot FROM room_guests WHERE room_id = ? AND id = ?`, roomID, guestID).Scan(&slot)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return slot, err
}

// DeleteGuest 删掉游客（离开、被踢）。
func DeleteGuest(ctx context.Context, q Querier, roomID, guestID string) error {
	return execOne(ctx, q, `DELETE FROM room_guests WHERE room_id = ? AND id = ?`, roomID, guestID)
}
