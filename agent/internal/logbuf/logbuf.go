// Package logbuf 是日志的内存环形缓冲：同时写到 stderr（交给 journald）并保留最近若干行，供面板查看。
package logbuf

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
)

// Buffer 实现 io.Writer，按行保存。
type Buffer struct {
	mu      sync.Mutex
	lines   []string
	next    int
	full    bool
	partial []byte
	out     io.Writer
}

// New 创建保存 n 行的缓冲，写入的内容同时转发到 out（nil 时用 os.Stderr）。
func New(n int, out io.Writer) *Buffer {
	if out == nil {
		out = os.Stderr
	}
	return &Buffer{lines: make([]string, n), out: out}
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.out.Write(p)
	data := append(b.partial, p...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		b.add(strings.TrimRight(string(data[:i]), "\r"))
		data = data[i+1:]
	}
	b.partial = append([]byte(nil), data...)
	return len(p), nil
}

func (b *Buffer) add(line string) {
	b.lines[b.next] = line
	b.next = (b.next + 1) % len(b.lines)
	if b.next == 0 {
		b.full = true
	}
}

// Tail 返回最近 n 行（按时间顺序）。
func (b *Buffer) Tail(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var all []string
	if b.full {
		all = append(all, b.lines[b.next:]...)
	}
	all = append(all, b.lines[:b.next]...)
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return append([]string(nil), all...)
}
