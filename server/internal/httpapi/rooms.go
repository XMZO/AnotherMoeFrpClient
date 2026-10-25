package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// 联机房间（shared/api/vnet.go）：建房、邀请码加入、离开、房主踢人。
// 谁在线、公钥、打洞信息和中继都在实时通道里（live.Hub），这里只管名单。

func (s *Server) roomRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET "+api.RouteRooms, authed(s.handleRooms))
	mux.Handle("POST "+api.RouteRooms, authed(s.handleCreateRoom))
	mux.Handle("POST "+api.RouteRoomJoin, authed(s.handleJoinRoom))
	mux.Handle("POST "+api.RouteRoomLeave, authed(s.handleLeaveRoom))
	mux.Handle("POST "+api.RouteRoomKick, authed(s.handleKickRoom))
}

// roomRooms 实现 live.VnetRooms。
type roomRooms struct{ s *Server }

func (r roomRooms) Slot(ctx context.Context, roomID, userID string) (int, error) {
	return store.RoomMemberSlot(ctx, r.s.st.DB(), roomID, userID)
}

func (r roomRooms) Members(ctx context.Context, roomID string) ([]api.RoomMember, error) {
	if _, err := store.GetRoom(ctx, r.s.st.DB(), roomID); err != nil {
		return nil, err
	}
	return store.RoomMembers(ctx, r.s.st.DB(), roomID)
}

// GET /api/v1/rooms
func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	rooms, err := store.UserRooms(r.Context(), s.st.DB(), p.User.ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	for i := range rooms {
		if rooms[i].Members, err = store.RoomMembers(r.Context(), s.st.DB(), rooms[i].ID); err != nil {
			s.writeError(w, r, err)
			return
		}
		online := s.live.VnetOnline(rooms[i].ID)
		for j := range rooms[i].Members {
			rooms[i].Members[j].Online = online[rooms[i].Members[j].Slot]
		}
	}
	writeJSON(w, http.StatusOK, api.RoomsResponse{Rooms: rooms})
}

// POST /api/v1/rooms
func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var req api.CreateRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 30 {
		s.writeError(w, r, fieldError("name", validate.ReasonInvalid, "房间名 1–30 个字"))
		return
	}
	p := principalFrom(r)
	room := store.RoomRow{ID: model.NewID(), Name: name, OwnerID: p.User.ID, CreatedAt: s.now().Unix()}
	err := s.st.Tx(r.Context(), func(tx *sql.Tx) error {
		if n, err := store.CountUserRooms(r.Context(), tx, p.User.ID); err != nil {
			return err
		} else if n >= api.VnetMaxRooms {
			return apierr.Newf(apierr.LimitExceeded, "最多加入 %d 个房间", api.VnetMaxRooms)
		}
		for range 5 { // 邀请码撞了就换一个
			room.Code = roomCode()
			if _, err := store.GetRoomByCode(r.Context(), tx, room.Code); errors.Is(err, store.ErrNotFound) {
				return store.InsertRoom(r.Context(), tx, room)
			}
		}
		return errors.New("room code collision")
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.Room{ID: room.ID, Name: room.Name, Code: room.Code, OwnerID: p.User.ID, OwnerName: p.User.Nickname,
		CreatedAt: room.CreatedAt, Members: []api.RoomMember{{UserID: p.User.ID, Nickname: p.User.Nickname, Slot: 1, IP: api.VnetIP(1)}}})
}

// POST /api/v1/rooms/join
func (s *Server) handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	var req api.JoinRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(req.Code), "-", ""))
	p := principalFrom(r)
	var room store.RoomRow
	err := s.st.Tx(r.Context(), func(tx *sql.Tx) error {
		var err error
		if room, err = store.GetRoomByCode(r.Context(), tx, code); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fieldError("code", validate.ReasonInvalid, "没有这个房间，检查一下邀请码")
			}
			return err
		}
		if _, err := store.RoomMemberSlot(r.Context(), tx, room.ID, p.User.ID); err == nil {
			return nil // 已经在房间里
		}
		if n, err := store.CountUserRooms(r.Context(), tx, p.User.ID); err != nil {
			return err
		} else if n >= api.VnetMaxRooms {
			return apierr.Newf(apierr.LimitExceeded, "最多加入 %d 个房间", api.VnetMaxRooms)
		}
		slot, err := store.AddRoomMember(r.Context(), tx, room.ID, p.User.ID, s.now().Unix())
		if err == nil && slot == 0 {
			return apierr.Newf(apierr.LimitExceeded, "房间满了（最多 %d 人）", api.VnetMaxMembers)
		}
		return err
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.live.VnetRoomChanged(context.Background(), room.ID)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/rooms/{id}/leave：房主离开等于解散。
func (s *Server) handleLeaveRoom(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	room, err := store.GetRoom(r.Context(), s.st.DB(), id)
	if err == nil {
		if room.OwnerID == p.User.ID {
			err = store.DeleteRoom(r.Context(), s.st.DB(), id)
		} else {
			err = store.RemoveRoomMember(r.Context(), s.st.DB(), id, p.User.ID)
		}
	}
	if err != nil {
		s.writeError(w, r, roomNotFound(err))
		return
	}
	s.live.VnetRoomChanged(context.Background(), id)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/rooms/{id}/kick
func (s *Server) handleKickRoom(w http.ResponseWriter, r *http.Request) {
	var req api.KickRoomRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	p := principalFrom(r)
	id := r.PathValue("id")
	room, err := store.GetRoom(r.Context(), s.st.DB(), id)
	if err != nil {
		s.writeError(w, r, roomNotFound(err))
		return
	}
	if room.OwnerID != p.User.ID {
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "只有房主能移出成员"))
		return
	}
	if req.UserID == p.User.ID {
		s.writeError(w, r, fieldError("userId", validate.ReasonInvalid, "不能移出自己，要解散请点离开"))
		return
	}
	if err := store.RemoveRoomMember(r.Context(), s.st.DB(), id, req.UserID); err != nil {
		s.writeError(w, r, roomNotFound(err))
		return
	}
	s.live.VnetRoomChanged(context.Background(), id)
	w.WriteHeader(http.StatusNoContent)
}

func roomNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return apierr.Newf(apierr.NotFound, "房间不存在，或你不在这个房间里")
	}
	return err
}

// roomCode 生成 8 位邀请码（去掉容易看错的 0 O 1 I L）。
func roomCode() string {
	const chars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
