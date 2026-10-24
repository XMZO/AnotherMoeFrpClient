package nodes

import (
	"time"

	"moefrp/shared/agentproto"
)

// Hooks 把节点这边会影响客户端的变化告诉外面（实时通道推给客户端，docs/client-live.md）。
// 都在锁外调用，可以是任意 goroutine。
type Hooks struct {
	// OfficialChanged：官方节点列表、在线状态、限额、权限、访问地址可能变了。客户端收到后带 ETag 拉一次。
	OfficialChanged func()
	// TicketsInvalid：已签发的登录票据作废了。userID 为空表示所有人。
	TicketsInvalid func(userID string)
}

// officialDebounce：一次批量操作只通知一次。
const officialDebounce = 300 * time.Millisecond

// SetHooks 设置回调。
func (s *Service) SetHooks(h Hooks) {
	s.mu.Lock()
	s.hooks = h
	s.mu.Unlock()
}

// officialChanged 稍等片刻后通知 OfficialChanged。
func (s *Service) officialChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hooks.OfficialChanged == nil {
		return
	}
	if s.officialTimer != nil {
		s.officialTimer.Reset(officialDebounce)
		return
	}
	s.officialTimer = time.AfterFunc(officialDebounce, func() {
		s.mu.Lock()
		s.officialTimer = nil
		fn := s.hooks.OfficialChanged
		s.mu.Unlock()
		if fn != nil {
			fn()
		}
	})
}

// ticketsInvalid 通知 TicketsInvalid。
func (s *Service) ticketsInvalid(userID string) {
	s.mu.Lock()
	fn := s.hooks.TicketsInvalid
	s.mu.Unlock()
	if fn != nil {
		fn(userID)
	}
}

// offlineLater 在宽限期过后（节点没重连上，客户端里要显示离线）通知一次。
func (s *Service) offlineLater() {
	time.AfterFunc(agentproto.OfflineGrace*time.Second+time.Second, s.officialChanged)
}
