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

// apperr 码：建虚拟网卡要管理员权限。
const codeNeedAdmin apierr.Code = "vnet_need_admin"

// VnetLive 是实时通道里联机房间要用的部分（live.Client）。
type VnetLive interface {
	SendVnet(api.LiveVnetBody) error
	SendPacket(to int, payload []byte) error
}

// VnetState 是界面上的联机状态。
type VnetState struct {
	Admin bool             `json:"admin"`           // 有管理员权限（没有就不能进房间，界面提示重启为管理员）
	Room  string           `json:"room"`            // 正在哪个房间（空表示没在）
	Self  int              `json:"self"`            // 自己的座位号（0 表示还没收到面板的成员列表）
	IP    string           `json:"ip"`              // 自己的虚拟 IP
	Peers []vnet.PeerState `json:"peers"`           // 其他成员和连接方式
	Error string           `json:"error,omitempty"` // 最近一次出错
}

// VnetService 绑定给前端：联机房间（虚拟局域网）。
type VnetService struct {
	api *apiclient.Client
	eng *vnet.Engine

	mu   sync.Mutex
	live VnetLive
	emit Emitter
	err  string
	quit func()
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
	return s.live
}

// Rooms 返回我加入的房间（含成员和谁在线）。
func (s *VnetService) Rooms() ([]api.Room, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiclient.DefaultTimeout)
	defer cancel()
	r, err := s.api.Rooms(ctx)
	return r, apperr.Normalize(err)
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

// LeaveRoom 离开房间（房主离开等于解散）；正在这个房间里的话先断开。
func (s *VnetService) LeaveRoom(id string) error {
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
	s.changed()
}

// State 返回当前联机状态。
func (s *VnetService) State() VnetState {
	room, self, peers := s.eng.State()
	st := VnetState{Admin: vnet.IsAdmin(), Room: room, Self: self, Peers: peers}
	if st.Peers == nil {
		st.Peers = []vnet.PeerState{}
	}
	if self > 0 {
		st.IP = api.VnetIP(self)
	}
	s.mu.Lock()
	st.Error = s.err
	s.mu.Unlock()
	return st
}

// RestartAsAdmin 以管理员身份重新启动程序（弹 UAC），成功后退出当前这个。
func (s *VnetService) RestartAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return apperr.Normalize(err)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--elevated-from=" + strconv.Itoa(os.Getpid()))
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
		if err := s.eng.SetPeers(b.Self, b.Peers); err != nil {
			s.setErr(err.Error())
		}
		s.changed()
	case api.VnetSignal:
		s.eng.OnSignal(b.From, b.Data)
	case api.VnetClosed:
		s.eng.Stop()
		s.setErr(b.Reason)
		s.changed()
	}
}

// OnPacket 收到面板中继过来的数据包。
func (s *VnetService) OnPacket(from int, payload []byte) { s.eng.OnPacket(from, payload) }

// OnLiveOnline：实时通道重新连上时，正在房间里的话重新进（面板那边的状态在断开时清掉了）。
func (s *VnetService) OnLiveOnline(on bool) {
	if !on {
		return
	}
	if room := s.eng.Room(); room != "" {
		if l := s.liveConn(); l != nil {
			_ = l.SendVnet(api.LiveVnetBody{Op: api.VnetJoin, Room: room, Key: s.eng.PublicKey()})
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
