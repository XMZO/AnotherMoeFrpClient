package engine

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	golog "github.com/fatedier/golib/log"

	frplog "github.com/fatedier/frp/pkg/util/log"
)

// DefaultLogLines 是日志环形缓冲默认保留的行数（PLAN 6.3.6）。
const DefaultLogLines = 5000

// LogLine 是一行日志。Text 是原文，前端一律按纯文本渲染。
type LogLine struct {
	Seq   int64  `json:"seq"`   // 单调递增，前端用作 key 和去重
	Level string `json:"level"` // trace debug info warn error
	Text  string `json:"text"`
	// Private 表示这行属于云端配置或订阅的运行（配置对用户是黑盒）：只给引擎判断状态用，
	// 不进缓冲（Lines、导出都拿不到），订阅者也不能转给界面。
	Private bool `json:"-"`
}

// LogSink 是装到 frp 全局 Logger 上的 io.Writer：保留最近 N 行，并把每一行转发给订阅者。
// golib 在 Write 返回后会复用传入的缓冲区，所以这里必须先拷贝。
type LogSink struct {
	mu      sync.Mutex
	lines   []LogLine
	max     int
	next    int
	full    bool
	seq     int64
	partial []byte

	subMu sync.RWMutex
	subs  map[int]func(LogLine)
	subID int

	hide concealer // 官方节点的地址换成节点名，见 conceal.go

	// 黑盒运行的日志不给界面看（见 LogLine.Private）：带 [run-N] 标签的按标签判断，
	// 不带标签的（frp 少数全局日志）按当前运行判断。
	private    map[string]bool
	privateCur bool
}

func NewLogSink(max int) *LogSink {
	if max <= 0 {
		max = DefaultLogLines
	}
	return &LogSink{lines: make([]LogLine, max), max: max, subs: map[int]func(LogLine){}, private: map[string]bool{}}
}

// BeginRun 在启动一次运行时调用：private 时这次运行（tag 为 [run-N] 里的 N 部分，可以为空）的日志都不给界面看。
func (s *LogSink) BeginRun(tag string, private bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.privateCur = private
	if private && tag != "" {
		s.private[tag] = true
	}
}

// Privatef 写一行只给引擎看的日志（黑盒运行相关、还没有 run 标签的行）。
func (s *LogSink) Privatef(level, format string, args ...any) {
	s.appendf(level, true, format, args...)
}

// isPrivateLocked 判断一行日志属不属于黑盒运行。
func (s *LogSink) isPrivateLocked(text string) bool {
	i := strings.Index(text, "[run-")
	if i < 0 {
		return s.privateCur
	}
	j := strings.IndexByte(text[i:], ']')
	if j < 0 {
		return s.privateCur
	}
	return s.private[text[i+1:i+j]]
}

func (s *LogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	data := append(s.partial, p...)
	var complete []LogLine
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		text := string(bytes.TrimRight(data[:i], "\r"))
		data = data[i+1:]
		complete = append(complete, s.storeLocked(text, parseLevel(text), s.isPrivateLocked(text)))
	}
	s.partial = append([]byte(nil), data...)
	s.mu.Unlock()

	s.publish(complete)
	return len(p), nil
}

// Appendf 写一行客户端自己的日志（例如连接失败的中文解释），格式和 frp 的日志行一致。
func (s *LogSink) Appendf(level, format string, args ...any) {
	s.appendf(level, false, format, args...)
}

func (s *LogSink) appendf(level string, private bool, format string, args ...any) {
	tag := map[string]string{"trace": "T", "debug": "D", "info": "I", "warn": "W", "error": "E"}[level]
	if tag == "" {
		tag, level = "I", "info"
	}
	text := fmt.Sprintf("%s [%s] [moefrp] %s", time.Now().Format("2006-01-02 15:04:05.000"), tag, fmt.Sprintf(format, args...))
	s.mu.Lock()
	line := s.storeLocked(text, level, private || s.isPrivateLocked(text))
	s.mu.Unlock()
	s.publish([]LogLine{line})
}

func (s *LogSink) storeLocked(text, level string, private bool) LogLine {
	text = s.hide.apply(text)
	s.seq++
	line := LogLine{Seq: s.seq, Level: level, Text: text, Private: private}
	if private {
		return line
	}
	s.lines[s.next] = line
	s.next = (s.next + 1) % s.max
	if s.next == 0 {
		s.full = true
	}
	return line
}

func (s *LogSink) publish(lines []LogLine) {
	if len(lines) == 0 {
		return
	}
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	for _, line := range lines {
		for _, fn := range s.subs {
			fn(line)
		}
	}
}

// Lines 按时间顺序返回缓冲里的全部行。
func (s *LogSink) Lines() []LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.full {
		return append([]LogLine(nil), s.lines[:s.next]...)
	}
	out := make([]LogLine, 0, s.max)
	out = append(out, s.lines[s.next:]...)
	return append(out, s.lines[:s.next]...)
}

// Clear 清空缓冲（序号继续递增，前端靠序号去重）。
func (s *LogSink) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next, s.full = 0, false
	clear(s.lines)
}

// Subscribe 注册逐行回调，返回取消函数。
//
// 回调在 frp 打日志的 goroutine 上同步执行，不能阻塞，也不能在持有"会调用 frp 方法"的锁时
// 调用取消函数，否则可能和正在执行的回调互相等待。
func (s *LogSink) Subscribe(fn func(LogLine)) func() {
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

// ParseLevel 把设置里的级别名转换成 golib 的级别，未知值按 info 处理。
func ParseLevel(level string) golog.Level {
	switch strings.ToLower(level) {
	case "trace":
		return golog.TraceLevel
	case "debug":
		return golog.DebugLevel
	case "warn":
		return golog.WarnLevel
	case "error":
		return golog.ErrorLevel
	default:
		return golog.InfoLevel
	}
}

// InstallGlobal 按 PLAN 6.3.6 的写法替换 frp 全局 Logger 的输出和级别。
// 不调用 frp 的 InitLogger，它只支持输出到控制台或文件。改级别时可以再调用一次。
func (s *LogSink) InstallGlobal(level string) {
	frplog.Logger = frplog.Logger.WithOptions(golog.WithOutput(s), golog.WithLevel(ParseLevel(level)))
}

// parseLevel 从 golib 的日志格式 "2026-10-18 19:10:48.763 [I] [client/service.go:335] ..." 里取级别。
func parseLevel(text string) string {
	if i := strings.Index(text, " ["); i >= 0 && len(text) >= i+4 && text[i+3] == ']' {
		switch text[i+2] {
		case 'T':
			return "trace"
		case 'D':
			return "debug"
		case 'W':
			return "warn"
		case 'E':
			return "error"
		}
	}
	return "info"
}
