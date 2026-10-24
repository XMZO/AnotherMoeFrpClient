package services

import (
	"sync"

	"moefrp/shared/api"
)

// EventNotice 是管理员推送的通知（docs/client-live.md）：前端收到后调用 AppService.TakeNotices 取走全部还没显示的。
// 窗口隐藏到托盘、WebView2 释放时通知先存着，窗口打开后一并显示。
const EventNotice = "app:notice"

// maxPendingNotices：还没显示的通知最多留这么多条，多了丢最旧的。
const maxPendingNotices = 20

// Notices 是还没显示的通知。
type Notices struct {
	mu      sync.Mutex
	pending []api.LiveNoticeBody
	emit    Emitter
}

// SetEmitter 设置事件出口。
func (n *Notices) SetEmitter(emit Emitter) {
	n.mu.Lock()
	n.emit = emit
	n.mu.Unlock()
}

// Push 收到一条通知。
func (n *Notices) Push(b api.LiveNoticeBody) {
	n.mu.Lock()
	n.pending = append(n.pending, b)
	if len(n.pending) > maxPendingNotices {
		n.pending = n.pending[len(n.pending)-maxPendingNotices:]
	}
	emit := n.emit
	n.mu.Unlock()
	if emit != nil {
		emit(EventNotice, nil)
	}
}

// Take 取走全部还没显示的通知。
func (n *Notices) Take() []api.LiveNoticeBody {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.pending
	n.pending = nil
	if out == nil {
		out = []api.LiveNoticeBody{}
	}
	return out
}

// TakeNotices 取走管理员推送的、还没显示的通知（前端收到 app:notice 或启动时调用）。
func (s *AppService) TakeNotices() []api.LiveNoticeBody {
	if s.Notices == nil {
		return []api.LiveNoticeBody{}
	}
	return s.Notices.Take()
}
