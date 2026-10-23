package engine

import (
	"bytes"
	"sync"

	golog "github.com/fatedier/golib/log"

	frplog "github.com/fatedier/frp/pkg/util/log"
)

// LogSink 是装到 frp 全局 Logger 上的 io.Writer：保留最近 N 行，并把每一行转发给订阅者。
// golib 会在 Write 返回后复用传入的缓冲区，所以这里必须先拷贝。
type LogSink struct {
	mu      sync.Mutex
	lines   []string
	max     int
	next    int
	full    bool
	partial []byte

	subMu sync.RWMutex
	subs  map[int]func(string)
	subID int
}

func NewLogSink(max int) *LogSink {
	return &LogSink{lines: make([]string, max), max: max, subs: map[int]func(string){}}
}

func (s *LogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	data := append(s.partial, p...)
	var complete []string
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(data[:i], "\r"))
		data = data[i+1:]
		complete = append(complete, line)
		s.lines[s.next] = line
		s.next = (s.next + 1) % s.max
		if s.next == 0 {
			s.full = true
		}
	}
	s.partial = append([]byte(nil), data...)
	s.mu.Unlock()

	s.subMu.RLock()
	for _, line := range complete {
		for _, fn := range s.subs {
			fn(line)
		}
	}
	s.subMu.RUnlock()
	return len(p), nil
}

// Lines 按时间顺序返回缓冲里的全部行。
func (s *LogSink) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.full {
		return append([]string(nil), s.lines[:s.next]...)
	}
	out := make([]string, 0, s.max)
	out = append(out, s.lines[s.next:]...)
	return append(out, s.lines[:s.next]...)
}

// Subscribe 注册逐行回调，返回取消函数。回调在 frp 打日志的 goroutine 上同步执行，不能阻塞。
func (s *LogSink) Subscribe(fn func(string)) func() {
	s.subMu.Lock()
	s.subID++
	id := s.subID
	s.subs[id] = fn
	s.subMu.Unlock()
	return func() {
		s.subMu.Lock()
		delete(s.subs, id)
		s.subMu.Unlock()
	}
}

// InstallGlobal 按 PLAN 6.3.6 的写法替换 frp 全局 Logger 的输出；进程内只应调用一次。
func (s *LogSink) InstallGlobal(level golog.Level) {
	frplog.Logger = frplog.Logger.WithOptions(golog.WithOutput(s), golog.WithLevel(level))
}
