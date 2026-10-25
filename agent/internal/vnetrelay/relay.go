// Package vnetrelay 是联机房间的中继（docs/vnet.md）：打洞打不通的成员连到这里，
// 按房间和座位号互相转发 WireGuard 数据包（已经加密，这里看不到内容）。
//
// 每个成员连自己的"收件中继"接收，发给别人时连对方的收件中继。连接用面板签的票据
// （nodeticket.SignVnet：只能用于这个节点、这个房间的这个座位，有效期内）。
package vnetrelay

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/shared/api"
	"moefrp/shared/nodeticket"
)

const (
	sendQueue    = 256
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
)

// Server 是中继服务，端口由 frps 配置里的 VnetRelayPort 决定（SetPort）。
type Server struct {
	NodeID string
	Key    func() []byte // 节点票据密钥（准入表里的），还没收到时为空
	Log    *slog.Logger

	mu    sync.Mutex
	port  int
	srv   *http.Server
	rooms map[string]map[int]*conn // 房间 → 座位号 → 连接
}

type conn struct {
	room string
	slot int
	send chan []byte
}

// SetPort 按配置开、关或换中继端口（0 关闭）。
func (s *Server) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port == s.port {
		return
	}
	if s.srv != nil {
		_ = s.srv.Close()
		s.srv = nil
	}
	s.port = port
	if port <= 0 {
		return
	}
	if s.rooms == nil {
		s.rooms = map[string]map[int]*conn{}
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		s.Log.Error("联机中继端口打不开", "port", port, "err", err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.RouteVnetRelay, s.handle)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	s.srv = srv
	go func() { _ = srv.Serve(ln) }()
	s.Log.Info("联机中继已启动", "port", port)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	key := s.Key()
	ticket, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(key) == 0 {
		http.Error(w, "relay not ready", http.StatusServiceUnavailable)
		return
	}
	room, slot, err := nodeticket.VerifyVnet(key, s.NodeID, ticket, time.Now().Unix())
	if err != nil {
		http.Error(w, "bad ticket", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &conn{room: room, slot: slot, send: make(chan []byte, sendQueue)}

	s.mu.Lock()
	if s.rooms[room] == nil {
		s.rooms[room] = map[int]*conn{}
	}
	s.rooms[room][slot] = c // 同一座位新连接顶掉旧的（旧的写不到了，读到错误自己退出）
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.rooms[room][slot] == c {
			delete(s.rooms[room], slot)
			if len(s.rooms[room]) == 0 {
				delete(s.rooms, room)
			}
		}
		s.mu.Unlock()
		ws.CloseNow()
	}()

	go func() {
		defer cancel()
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-c.send:
				wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
				err := ws.Write(wctx, websocket.MessageBinary, b)
				wcancel()
				if err != nil {
					return
				}
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil {
					return
				}
			}
		}
	}()

	for {
		typ, b, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary || len(b) < 2 {
			continue
		}
		s.mu.Lock()
		to := s.rooms[room][int(b[0])]
		s.mu.Unlock()
		if to == nil || to == c {
			continue
		}
		b[0] = byte(slot) // 转发时第一个字节换成发送方的座位号
		select {
		case to.send <- b:
		default: // 对方收不过来就丢，WireGuard 会重传
		}
	}
}

// ErrDisabled 表示中继没开。
var ErrDisabled = errors.New("vnet relay disabled")
