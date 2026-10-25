package services

import (
	"context"
	"os"
	"strconv"
	"sync"

	"golang.org/x/sys/windows"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/vnet"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

// EventVnet：联机房间的状态变了（进出房间、成员上下线、直连 / 中继切换），数据是 VnetState。
const EventVnet = "vnet:state"

// EventVnetChat：房间聊天有新消息，数据是 VnetChatEvent。
const EventVnetChat = "vnet:chat"

// VnetChatEvent 是聊天推送：全部消息（最多 vnetChatKeep 条）和新来的那条（进房间收到历史时为空）。
type VnetChatEvent struct {
	Msgs []api.VnetChatMsg `json:"msgs"`
	New  *api.VnetChatMsg  `json:"new,omitempty"`
}

const vnetChatKeep = 100

// apperr 码：建虚拟网卡要管理员权限。
const codeNeedAdmin apierr.Code = "vnet_need_admin"

// VnetLive 是实时通道里联机房间要用的部分（live.Client）。
type VnetLive interface {
	SendVnet(api.LiveVnetBody) error
	SendPacket(to int, payload []byte) error
}

// VnetState 是界面上的联机状态。
type VnetState struct {
	vnet.Snapshot
	Admin  bool   `json:"admin"`           // 有管理员权限（没有就不能进房间，界面提示重启为管理员）
	Driver bool   `json:"driver"`          // 虚拟网卡驱动已经装好
	IP     string `json:"ip"`              // 自己的虚拟 IP
	Guest  string `json:"guest,omitempty"` // 以游客身份在哪个房间（没登录时）
	Error  string `json:"error,omitempty"` // 最近一次出错
}

// VnetService 绑定给前端：联机房间（虚拟局域网）。
type VnetService struct {
	api *apiclient.Client
	eng *vnet.Engine

	mu    sync.Mutex
	live  VnetLive // 登录用户的实时通道
	emit  Emitter
	err   string
	quit  func()
	guest *guestLive // 游客模式（没登录，用游客令牌）
	drvOK bool       // 这次运行里装过驱动或建过网卡
	chat  []api.VnetChatMsg
}

// NewVnetService：live 在 main 里建好实时通道后用 SetLive 注入。
func NewVnetService(c *apiclient.Client, logf func(string, ...any)) *VnetService {
	s := &VnetService{api: c}
	s.eng = vnet.New(vnetTransport{s}, logf, s.changed)
	return s
}

// SetLive 注入实时通道；SetEmitter 注入事件推送；SetQuit 注入退出程序（重启为管理员时用）。
func (s *VnetService) SetLive(l VnetLive)   { s.mu.Lock(); s.live = l; s.mu.Unlock() }
func (s *VnetService) SetEmitter(e Emitter) { s.mu.Lock(); s.emit = e; s.mu.Unlock() }
func (s *VnetService) SetQuit(q func())     { s.mu.Lock(); s.quit = q; s.mu.Unlock() }

type vnetTransport struct{ s *VnetService }

func (t vnetTransport) Signal(room string, to int, data string) error {
	l := t.s.liveConn()
	if l == nil {
		return apperr.New(apperr.ServerUnreachable, "live channel not connected")
	}
	return l.SendVnet(api.LiveVnetBody{Op: api.VnetSignal, Room: room, To: to, Data: data})
}

func (t vnetTransport) SetRelay(room, relay string) error {
	l := t.s.liveConn()
	if l == nil {
		return nil // 连上后重新进房间时会再告诉面板
	}
	return l.SendVnet(api.LiveVnetBody{Op: api.VnetRelayOp, Room: room, Relay: relay})
}

func (t vnetTransport) Relay(to int, payload []byte) error {
	l := t.s.liveConn()
	if l == nil {
		return nil // 没连上就丢，WireGuard 会重传
	}
	_ = l.SendPacket(to, payload)
	return nil
}

func (s *VnetService) liveConn() VnetLive {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.guest != nil {
		return s.guest
	}
	return s.live
}

// Rooms 返回我加入的房间（含成员和谁在线）；游客模式下是游客所在的那一个。
func (s *VnetService) Rooms() ([]api.Room, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	if g := s.guestConn(); g != nil {
		r, err := s.api.GuestRoom(ctx, g.token)
		if err != nil {
			if apierr.CodeOf(err) == apierr.Unauthorized {
				s.dropGuest("游客身份已失效（离开了房间、被移出或房间已解散）")
			}
			return []api.Room{}, apperr.Normalize(err)
		}
		return []api.Room{r}, nil
	}
	r, err := s.api.Rooms(ctx)
	return r, apperr.Normalize(err)
}

// RoomRelays 返回能用的中继节点（房主选默认中继用）。
func (s *VnetService) RoomRelays() ([]api.VnetRelay, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	r, err := s.api.RoomRelays(ctx)
	return r, apperr.Normalize(err)
}

// OpenRoomsOnStart：这次是"以管理员身份重启"起来的（带 --open=rooms），界面直接打开联机页。
func (s *VnetService) OpenRoomsOnStart() bool {
	for _, a := range os.Args[1:] {
		if a == "--open=rooms" {
			return true
		}
	}
	return false
}

// UpdateRoom 房主改房间设置（允许游客、默认中继、锁定中继）。
func (s *VnetService) UpdateRoom(id string, st api.RoomSettings) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	return apperr.Normalize(s.api.UpdateRoom(ctx, id, st))
}

// SetRelay 选我的收件中继：auto（自动）、panel（面板）或节点 ID。房主锁定时不生效。
func (s *VnetService) SetRelay(pref string) {
	s.eng.SetRelayPref(pref)
	s.changed()
}

// GuestJoin 没登录时用邀请码 + 昵称以游客身份进房间（房主允许时），之后用 Connect 进虚拟网。
func (s *VnetService) GuestJoin(code, nickname string) (api.Room, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	res, err := s.api.GuestJoin(ctx, code, nickname)
	if err != nil {
		return api.Room{}, apperr.Normalize(err)
	}
	s.dropGuest("")
	g := newGuestLive(s, res.Token, res.Room.ID)
	s.mu.Lock()
	s.guest = g
	s.mu.Unlock()
	go g.run()
	s.changed()
	return res.Room, nil
}

func (s *VnetService) guestConn() *guestLive {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.guest
}

// dropGuest 退出游客模式（断开虚拟网和游客通道）。why 不为空时记成错误显示。
func (s *VnetService) dropGuest(why string) {
	s.mu.Lock()
	g := s.guest
	s.guest = nil
	s.mu.Unlock()
	if g == nil {
		return
	}
	if s.eng.Room() == g.room {
		s.eng.Stop()
	}
	g.close()
	if why != "" {
		s.setErr(why)
	}
	s.changed()
}

// CreateRoom 新建房间（自己是房主）。
func (s *VnetService) CreateRoom(name string) (api.Room, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	r, err := s.api.CreateRoom(ctx, name)
	return r, apperr.Normalize(err)
}

// JoinRoom 用邀请码加入房间。
func (s *VnetService) JoinRoom(code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	return apperr.Normalize(s.api.JoinRoom(ctx, code))
}

// LeaveRoom 离开房间（房主离开等于解散）；正在这个房间里的话先断开。游客离开后游客身份作废。
func (s *VnetService) LeaveRoom(id string) error {
	if g := s.guestConn(); g != nil && g.room == id {
		ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
		defer cancel()
		err := s.api.GuestLeave(ctx, g.token)
		s.dropGuest("")
		if apierr.CodeOf(err) == apierr.Unauthorized {
			err = nil
		}
		return apperr.Normalize(err)
	}
	if s.eng.Room() == id {
		s.Disconnect()
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	return apperr.Normalize(s.api.LeaveRoom(ctx, id))
}

// KickMember 房主把成员移出房间。
func (s *VnetService) KickMember(roomID, userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	return apperr.Normalize(s.api.KickRoom(ctx, roomID, userID))
}

// Connect 进入房间的虚拟局域网：建虚拟网卡，告诉面板自己的公钥，之后面板推成员列表过来。
func (s *VnetService) Connect(roomID string) error {
	if !vnet.IsAdmin() {
		return apperr.New(codeNeedAdmin, "need administrator")
	}
	if cur := s.eng.Room(); cur != "" && cur != roomID {
		s.Disconnect()
	}
	key, err := s.eng.Start(roomID)
	if err != nil {
		s.setErr(err.Error())
		return apperr.Normalize(err)
	}
	s.setErr("")
	if l := s.liveConn(); l != nil {
		if err := l.SendVnet(api.LiveVnetBody{Op: api.VnetJoin, Room: roomID, Key: key}); err != nil {
			s.setErr("和面板的实时连接断了，连上后会自动进房间")
		}
	}
	s.changed()
	return nil
}

// SendChat 在当前房间里发一条聊天消息。
func (s *VnetService) SendChat(text string) error {
	room := s.eng.Room()
	if room == "" {
		return apperr.New(apperr.InvalidArg, "not in a room")
	}
	l := s.liveConn()
	if l == nil {
		return apperr.New(apperr.ServerUnreachable, "live channel not connected")
	}
	return apperr.Normalize(l.SendVnet(api.LiveVnetBody{Op: api.VnetChat, Room: room, Text: text}))
}

// ChatHistory 返回当前房间的聊天（打开聊天窗口时用）。
func (s *VnetService) ChatHistory() []api.VnetChatMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.VnetChatMsg{}, s.chat...)
}

func (s *VnetService) onChat(b api.LiveVnetBody) {
	s.mu.Lock()
	if b.History != nil {
		s.chat = append([]api.VnetChatMsg(nil), b.History...)
	}
	if b.Chat != nil {
		s.chat = append(s.chat, *b.Chat)
	}
	if len(s.chat) > vnetChatKeep {
		s.chat = s.chat[len(s.chat)-vnetChatKeep:]
	}
	ev := VnetChatEvent{Msgs: append([]api.VnetChatMsg{}, s.chat...), New: b.Chat}
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit(EventVnetChat, ev)
	}
}

// Shutdown 在程序退出时调用：断开虚拟网，清掉 wintun 的回调。
func (s *VnetService) Shutdown() {
	s.Disconnect()
	s.dropGuest("")
	vnet.Shutdown()
}

// Disconnect 离开虚拟局域网（不退出房间）。
func (s *VnetService) Disconnect() {
	room := s.eng.Room()
	if room == "" {
		return
	}
	if l := s.liveConn(); l != nil {
		_ = l.SendVnet(api.LiveVnetBody{Op: api.VnetLeave, Room: room})
	}
	s.eng.Stop()
	s.mu.Lock()
	s.chat = nil // 聊天跟着房间走
	s.mu.Unlock()
	s.changed()
}

// State 返回当前联机状态。
func (s *VnetService) State() VnetState {
	st := VnetState{Snapshot: s.eng.State(), Admin: vnet.IsAdmin()}
	st.Driver = st.Room != "" || vnet.DriverInstalled()
	if st.Self > 0 {
		st.IP = api.VnetIP(st.Self)
	}
	s.mu.Lock()
	st.Error = s.err
	st.Driver = st.Driver || s.drvOK
	if s.guest != nil {
		st.Guest = s.guest.room
	}
	s.mu.Unlock()
	return st
}

// InstallDriver 装虚拟网卡驱动（建一块网卡再关掉；要管理员权限）。进联机页时没装就调用。
func (s *VnetService) InstallDriver() error {
	if !vnet.IsAdmin() {
		return apperr.New(codeNeedAdmin, "need administrator")
	}
	if err := vnet.InstallDriver(); err != nil {
		return apperr.Normalize(err)
	}
	s.mu.Lock()
	s.drvOK = true
	s.mu.Unlock()
	s.changed()
	return nil
}

// RestartAsAdmin 以管理员身份重新启动程序（弹 UAC），成功后退出当前这个。
func (s *VnetService) RestartAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return apperr.Normalize(err)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--elevated-from=" + strconv.Itoa(os.Getpid()) + " --open=rooms")
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		return apperr.Wrap(apperr.InvalidArg, err) // 多半是在 UAC 里点了"否"
	}
	s.mu.Lock()
	q := s.quit
	s.mu.Unlock()
	if q != nil {
		go q()
	}
	return nil
}

// ---- main 用的回调 ----

// OnLive 处理实时通道里的 vnet 消息。
func (s *VnetService) OnLive(b api.LiveVnetBody) {
	if b.Room == "" || b.Room != s.eng.Room() {
		return
	}
	switch b.Op {
	case api.VnetPeers:
		if err := s.eng.SetPeers(b); err != nil {
			s.setErr(err.Error())
		}
		s.changed()
	case api.VnetSignal:
		s.eng.OnSignal(b.From, b.Data)
	case api.VnetChat:
		s.onChat(b)
	case api.VnetClosed:
		s.eng.Stop()
		if g := s.guestConn(); g != nil && g.room == b.Room {
			s.dropGuest(b.Reason) // 游客被移出或房间解散：游客身份也作废了
			return
		}
		s.setErr(b.Reason)
		s.changed()
	}
}

// OnPacket 收到面板中继过来的数据包。
func (s *VnetService) OnPacket(from int, payload []byte) { s.eng.OnPacket(from, payload) }

// OnLiveOnline：实时通道重新连上时，正在房间里的话重新进（面板那边的状态在断开时清掉了），收件中继也再报一次。
func (s *VnetService) OnLiveOnline(on bool) {
	if !on {
		return
	}
	if room := s.eng.Room(); room != "" {
		if l := s.liveConn(); l != nil {
			_ = l.SendVnet(api.LiveVnetBody{Op: api.VnetJoin, Room: room, Key: s.eng.PublicKey()})
			if h := s.eng.State().Home; h != "" {
				_ = l.SendVnet(api.LiveVnetBody{Op: api.VnetRelayOp, Room: room, Relay: h})
			}
		}
	}
}

func (s *VnetService) setErr(msg string) {
	s.mu.Lock()
	s.err = msg
	s.mu.Unlock()
}

func (s *VnetService) changed() {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit(EventVnet, s.State())
	}
}
