package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"moefrp/server/internal/live"
	"moefrp/server/internal/ratelimit"
	"moefrp/shared/nodeticket"

	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// 联机房间（shared/api/vnet.go）：建房、邀请码加入、离开、房主踢人。
// 谁在线、公钥、打洞信息和中继都在实时通道里（live.Hub），这里只管名单。

func (s *Server) roomRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler, public func(http.HandlerFunc, *ratelimit.Limiter) http.Handler) {
	mux.Handle("POST "+api.RouteRoomGuest, public(s.handleGuestJoin, s.limRegister))
	mux.HandleFunc("GET "+api.RouteRoomGuest, s.handleGuestRoom)
	mux.HandleFunc("POST "+api.RouteRoomGuestLeave, s.handleGuestLeave)
	mux.HandleFunc("GET "+api.RouteRoomGuestLive, s.handleGuestLive)
	mux.Handle("PATCH "+api.RouteRoom, authed(s.handleRoomSettings))
	mux.Handle("GET "+api.RouteRoomRelays, authed(s.handleRoomRelays))
	mux.Handle("GET "+api.RouteRooms, authed(s.handleRooms))
	mux.Handle("POST "+api.RouteRooms, authed(s.handleCreateRoom))
	mux.Handle("POST "+api.RouteRoomJoin, authed(s.handleJoinRoom))
	mux.Handle("POST "+api.RouteRoomLeave, authed(s.handleLeaveRoom))
	mux.Handle("POST "+api.RouteRoomKick, authed(s.handleKickRoom))
	mux.Handle("POST "+api.RouteRoomCode, authed(s.handleRoomCode))
}

// roomRooms 实现 live.VnetRooms。
type roomRooms struct{ s *Server }

func (r roomRooms) Slot(ctx context.Context, roomID, userID string) (int, error) {
	if id, ok := strings.CutPrefix(userID, api.GuestPrefix); ok {
		return store.GuestSlot(ctx, r.s.st.DB(), roomID, id)
	}
	return store.RoomMemberSlot(ctx, r.s.st.DB(), roomID, userID)
}

// vnetTicketTTL 是中继票据的有效期：成员每次收到成员列表都会拿到新的。
const vnetTicketTTL = 24 * time.Hour

func (r roomRooms) RelayInfo(ctx context.Context, roomID string, slots []int) (api.Room, []api.VnetRelay, map[int]map[string]string) {
	row, err := store.GetRoom(ctx, r.s.st.DB(), roomID)
	if err != nil {
		return api.Room{}, nil, nil
	}
	room := api.Room{ID: row.ID, Name: row.Name, Code: row.Code, RoomSettings: row.RoomSettings}
	relays, keys, err := r.s.nodes.VnetRelays(ctx)
	if err != nil {
		r.s.log.Warn("vnet relays", "err", err)
		return room, nil, nil
	}
	now := r.s.now()
	tickets := map[int]map[string]string{}
	for _, slot := range slots {
		m := map[string]string{}
		for _, x := range relays {
			m[x.ID] = nodeticket.SignVnet(keys[x.ID], x.ID, roomID, slot, now.Unix(), now.Add(vnetTicketTTL).Unix())
		}
		tickets[slot] = m
	}
	return room, relays, tickets
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

// POST /api/v1/rooms/{id}/code：房主换新邀请码。旧码马上作废；已经在房间里的成员和游客不受影响，在线的成员收到新码。
func (s *Server) handleRoomCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := r.PathValue("id")
	room, err := store.GetRoom(r.Context(), s.st.DB(), id)
	if err != nil {
		s.writeError(w, r, roomNotFound(err))
		return
	}
	if room.OwnerID != p.User.ID {
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "只有房主能换邀请码"))
		return
	}
	err = s.st.Tx(r.Context(), func(tx *sql.Tx) error {
		for range 5 { // 邀请码撞了就换一个
			code := roomCode()
			if _, err := store.GetRoomByCode(r.Context(), tx, code); errors.Is(err, store.ErrNotFound) {
				room.Code = code
				return store.SetRoomCode(r.Context(), tx, id, code)
			}
		}
		return errors.New("room code collision")
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.live.VnetRoomChanged(context.Background(), id)
	writeJSON(w, http.StatusOK, api.Room{ID: room.ID, Name: room.Name, Code: room.Code, OwnerID: room.OwnerID, OwnerName: p.User.Nickname,
		CreatedAt: room.CreatedAt, RoomSettings: room.RoomSettings})
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
	var kerr error
	if gid, ok := strings.CutPrefix(req.UserID, api.GuestPrefix); ok {
		kerr = store.DeleteGuest(r.Context(), s.st.DB(), id, gid)
	} else {
		kerr = store.RemoveRoomMember(r.Context(), s.st.DB(), id, req.UserID)
	}
	if kerr != nil {
		s.writeError(w, r, roomNotFound(kerr))
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

// GET /api/v1/rooms/relays：能用的中继节点。
func (s *Server) handleRoomRelays(w http.ResponseWriter, r *http.Request) {
	relays, _, err := s.nodes.VnetRelays(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relays": relays})
}

// PATCH /api/v1/rooms/{id}：房主改设置（允许游客、默认中继、锁定中继）。
func (s *Server) handleRoomSettings(w http.ResponseWriter, r *http.Request) {
	// noVoice、muted 没带就保持原样（旧版本客户端改设置时不会把语音设置清掉）
	var body struct {
		api.RoomSettings
		NoVoice  *bool   `json:"noVoice"`
		NoDirect *bool   `json:"noDirect"`
		Muted    *[]int  `json:"muted"`
		Name     *string `json:"name"` // 改房间名（不带就不改）
	}
	if err := decodeJSON(r, &body); err != nil {
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
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "只有房主能改房间设置"))
		return
	}
	req := body.RoomSettings
	req.NoVoice, req.Muted, req.NoDirect = room.NoVoice, room.Muted, room.NoDirect
	if body.NoVoice != nil {
		req.NoVoice = *body.NoVoice
	}
	if body.NoDirect != nil {
		req.NoDirect = *body.NoDirect
	}
	if body.Muted != nil {
		req.Muted = []int{}
		for _, n := range *body.Muted {
			if n >= 1 && n <= 250 && !slices.Contains(req.Muted, n) {
				req.Muted = append(req.Muted, n)
			}
		}
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" || utf8.RuneCountInString(name) > 30 {
			s.writeError(w, r, fieldError("name", validate.ReasonInvalid, "房间名 1–30 个字"))
			return
		}
		if name != room.Name {
			if err := store.SetRoomName(r.Context(), s.st.DB(), id, name); err != nil {
				s.writeError(w, r, err)
				return
			}
		}
	}
	if req.Relay != "" {
		if _, err := store.GetNode(r.Context(), s.st.DB(), req.Relay); err != nil {
			s.writeError(w, r, fieldError("relay", validate.ReasonInvalid, "没有这个中继节点"))
			return
		}
	}
	if err := store.SetRoomSettings(r.Context(), s.st.DB(), id, req); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.live.VnetRoomChanged(context.Background(), id)
	w.WriteHeader(http.StatusNoContent)
}

// ---- 游客 ----

func guestTokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// guestFrom 用请求里的游客令牌找游客。
func (s *Server) guestFrom(r *http.Request) (store.GuestRow, error) {
	tok, ok := strings.CutPrefix(r.Header.Get(api.HeaderAuthorization), api.BearerPrefix)
	if !ok || tok == "" {
		return store.GuestRow{}, apierr.New(apierr.Unauthorized)
	}
	g, err := store.GuestByToken(r.Context(), s.st.DB(), guestTokenHash(tok))
	if errors.Is(err, store.ErrNotFound) {
		return g, apierr.Newf(apierr.Unauthorized, "游客身份已失效（离开了房间、被移出或房间已解散）")
	}
	return g, err
}

// roomView 返回房间和成员（含谁在线）。
func (s *Server) roomView(ctx context.Context, room store.RoomRow) (api.Room, error) {
	out := api.Room{ID: room.ID, Name: room.Name, Code: room.Code, OwnerID: room.OwnerID, CreatedAt: room.CreatedAt, RoomSettings: room.RoomSettings}
	ms, err := store.RoomMembers(ctx, s.st.DB(), room.ID)
	if err != nil {
		return out, err
	}
	online := s.live.VnetOnline(room.ID)
	for i := range ms {
		ms[i].Online = online[ms[i].Slot]
		if ms[i].UserID == room.OwnerID {
			out.OwnerName = ms[i].Nickname
		}
	}
	out.Members = ms
	return out, nil
}

// POST /api/v1/rooms/guest：游客用邀请码 + 昵称进房间（房主允许时）。
func (s *Server) handleGuestJoin(w http.ResponseWriter, r *http.Request) {
	var req api.GuestJoinRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Nickname)
	if name == "" || utf8.RuneCountInString(name) > 20 {
		s.writeError(w, r, fieldError("nickname", validate.ReasonInvalid, "昵称 1–20 个字"))
		return
	}
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(req.Code), "-", ""))
	room, err := store.GetRoomByCode(r.Context(), s.st.DB(), code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = fieldError("code", validate.ReasonInvalid, "没有这个房间，检查一下邀请码")
		}
		s.writeError(w, r, err)
		return
	}
	if !room.AllowGuests {
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "这个房间不允许游客加入：请登录后再加入，或者让房主打开「允许游客」"))
		return
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := "mfg_" + base64.RawURLEncoding.EncodeToString(b)
	g := store.GuestRow{ID: model.NewID(), RoomID: room.ID, Nickname: name}
	slot, err := store.InsertGuest(r.Context(), s.st.DB(), g, guestTokenHash(tok), s.now().Unix())
	if err == nil && slot == 0 {
		err = apierr.Newf(apierr.LimitExceeded, "房间满了（最多 %d 人）", api.VnetMaxMembers)
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	view, err := s.roomView(r.Context(), room)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.live.VnetRoomChanged(context.Background(), room.ID)
	writeJSON(w, http.StatusOK, api.GuestJoinResponse{Token: tok, Room: view})
}

// GET /api/v1/rooms/guest：游客看自己的房间。
func (s *Server) handleGuestRoom(w http.ResponseWriter, r *http.Request) {
	g, err := s.guestFrom(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	room, err := store.GetRoom(r.Context(), s.st.DB(), g.RoomID)
	if err != nil {
		s.writeError(w, r, roomNotFound(err))
		return
	}
	view, err := s.roomView(r.Context(), room)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// POST /api/v1/rooms/guest/leave：游客离开（令牌作废）。
func (s *Server) handleGuestLeave(w http.ResponseWriter, r *http.Request) {
	g, err := s.guestFrom(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := store.DeleteGuest(r.Context(), s.st.DB(), g.RoomID, g.ID); err != nil {
		s.writeError(w, r, roomNotFound(err))
		return
	}
	s.live.VnetRoomChanged(context.Background(), g.RoomID)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/rooms/guest/live：游客的实时通道，只用来收发联机房间的消息。
func (s *Server) handleGuestLive(w http.ResponseWriter, r *http.Request) {
	g, err := s.guestFrom(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	id := api.GuestPrefix + g.ID
	c := &live.Client{SessionID: id, UserID: id, Nickname: g.Nickname + "（游客）", Role: "guest", IP: s.clientIP(r)}
	s.live.Serve(context.WithoutCancel(r.Context()), ws, c)
}
