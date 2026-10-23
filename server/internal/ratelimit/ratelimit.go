// Package ratelimit 是按 key（IP 或用户 ID）的内存限流（PLAN 7.8）。
//
// 服务端是单进程，限流状态放在内存里即可。底层是 golang.org/x/time/rate 的令牌桶：
// "每分钟 10 次"表示桶容量 10，每 6 秒补充一个。
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Rule 是一条限流规则：Window 时间内最多 Limit 次。
type Rule struct {
	Limit  int
	Window time.Duration
}

// Disabled 表示不限流。
func (r Rule) Disabled() bool { return r.Limit <= 0 || r.Window <= 0 }

type entry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// DefaultMaxKeys 是一个限流器最多记录的 key 数。
const DefaultMaxKeys = 100_000

// Limiter 为每个 key 维护一个令牌桶。
type Limiter struct {
	rule    Rule
	maxKeys int
	mu      sync.Mutex
	entries map[string]*entry
}

// New 创建限流器。
func New(rule Rule) *Limiter {
	return &Limiter{rule: rule, maxKeys: DefaultMaxKeys, entries: map[string]*entry{}}
}

// SetMaxKeys 修改 key 数上限（测试用）。
func (l *Limiter) SetMaxKeys(n int) { l.maxKeys = n }

// Rule 返回限流规则。
func (l *Limiter) Rule() Rule { return l.rule }

// Allow 消耗 key 的一个配额。不允许时返回还要等多久。
func (l *Limiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if l.rule.Disabled() {
		return true, 0
	}
	l.mu.Lock()
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.maxKeys {
			l.evict(now)
		}
		e = &entry{lim: rate.NewLimiter(rate.Every(l.rule.Window/time.Duration(l.rule.Limit)), l.rule.Limit)}
		l.entries[key] = e
	}
	e.lastSeen = now
	l.mu.Unlock()

	r := e.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, l.rule.Window
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// evict 在 key 数达到上限时腾出空间：先清理补满的桶，不够再随机丢掉一些
// （被丢掉的 key 相当于桶被重新装满）。只有在被大量不同来源刷的时候才会走到这里，
// 这时宁可放宽限流，也不能让内存无限增长。调用方持有 l.mu。
func (l *Limiter) evict(now time.Time) {
	l.sweep(now)
	target := l.maxKeys * 9 / 10
	for k := range l.entries {
		if len(l.entries) <= target {
			return
		}
		delete(l.entries, k)
	}
}

// Sweep 删除桶已经补满的 key（等价于从未访问过），防止内存无限增长。
func (l *Limiter) Sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
}

func (l *Limiter) sweep(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.lastSeen) >= l.rule.Window && e.lim.TokensAt(now) >= float64(l.rule.Limit) {
			delete(l.entries, k)
		}
	}
}

// Len 返回当前记录的 key 数。
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
