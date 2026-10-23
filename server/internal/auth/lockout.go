package auth

import (
	"strings"
	"sync"
	"time"
)

// LockoutPolicy 是按昵称的失败锁定规则（PLAN 7.4）。
type LockoutPolicy struct {
	MaxFailures int           // 连续失败这么多次后锁定
	BaseLock    time.Duration // 第一次锁定的时长
	MaxLock     time.Duration // 锁定时长上限
	Forget      time.Duration // 最后一次失败之后这么久没有新的失败，就清零重新计数
	MaxEntries  int           // 内存里最多记录多少个昵称，防止被刷爆
}

// DefaultLockout：连续失败 5 次锁定 15 分钟，之后每再失败一次锁定时间翻倍，最长 24 小时。
var DefaultLockout = LockoutPolicy{
	MaxFailures: 5,
	BaseLock:    15 * time.Minute,
	MaxLock:     24 * time.Hour,
	Forget:      24 * time.Hour,
	MaxEntries:  100_000,
}

type lockState struct {
	failures    int
	level       int // 已经锁定过几次
	lockedUntil time.Time
	lastFailure time.Time
	inflight    int // 已预占、还没有结果的尝试
}

// Lockout 在内存里记录每个昵称的失败次数。
//
// 不区分昵称是否存在：不存在的昵称同样会被锁定，避免通过"是否会被锁"判断昵称是否注册过。
// 服务端是单进程，重启后清零可以接受（PLAN 7.8）。
//
// 校验密码之前必须先用 Begin 预占一次尝试：检查和计数在同一把锁里完成，
// 并发的请求不能绕过"最多失败 MaxFailures 次"的限制。
type Lockout struct {
	policy LockoutPolicy
	mu     sync.Mutex
	states map[string]*lockState
}

// NewLockout 创建锁定表。
func NewLockout(p LockoutPolicy) *Lockout {
	if p.MaxEntries <= 0 {
		p.MaxEntries = DefaultLockout.MaxEntries
	}
	return &Lockout{policy: p, states: map[string]*lockState{}}
}

func lockKey(nickname string) string { return strings.ToLower(nickname) }

// Denial 是 Begin 拒绝的原因。
type Denial int

const (
	Allowed Denial = iota
	Locked         // 已锁定，wait 是剩余时间
	Busy           // 已有足够多的尝试在进行中，稍后再试
)

// Attempt 是一次已预占的尝试，必须以 Succeed、Fail 或 Cancel 之一结束。
type Attempt struct {
	l    *Lockout
	key  string
	done bool
}

// Begin 预占一次尝试。
//
// 从未锁定过时，已失败次数加上进行中的尝试最多 MaxFailures 个；锁定过期后再试，
// 任何一次失败都会重新锁定，所以同一时间只允许 1 个尝试。
func (l *Lockout) Begin(nickname string, now time.Time) (*Attempt, Denial, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := lockKey(nickname)
	st := l.state(key, now)
	if now.Before(st.lockedUntil) {
		return nil, Locked, st.lockedUntil.Sub(now)
	}
	limit := l.policy.MaxFailures - st.failures
	if st.level > 0 {
		limit = 1
	}
	if st.inflight >= limit {
		return nil, Busy, time.Second
	}
	st.inflight++
	return &Attempt{l: l, key: key}, Allowed, 0
}

// Succeed 结束尝试并清除该昵称的失败记录。
func (a *Attempt) Succeed() {
	if a == nil || a.done {
		return
	}
	a.done = true
	l := a.l
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[a.key]
	if !ok {
		return
	}
	if st.inflight > 0 {
		st.inflight--
	}
	if st.inflight == 0 {
		delete(l.states, a.key)
		return
	}
	st.failures, st.level, st.lockedUntil = 0, 0, time.Time{}
}

// Fail 结束尝试并记录一次失败，返回是否因此进入锁定以及锁定时长。
func (a *Attempt) Fail(now time.Time) (time.Duration, bool) {
	if a == nil || a.done {
		return 0, false
	}
	a.done = true
	l := a.l
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.state(a.key, now)
	if st.inflight > 0 {
		st.inflight--
	}
	return l.recordFailure(st, now)
}

// Cancel 结束尝试但不计数（例如服务端内部错误）。重复调用和在 Succeed / Fail 之后调用都是空操作。
func (a *Attempt) Cancel() {
	if a == nil || a.done {
		return
	}
	a.done = true
	l := a.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, ok := l.states[a.key]; ok && st.inflight > 0 {
		st.inflight--
	}
}

// state 返回（必要时创建）昵称的记录，并清掉已经可以遗忘的失败。调用方持有 l.mu。
func (l *Lockout) state(key string, now time.Time) *lockState {
	st, ok := l.states[key]
	if !ok {
		if len(l.states) >= l.policy.MaxEntries {
			l.evict(now)
		}
		st = &lockState{}
		l.states[key] = st
	}
	if st.failures > 0 && !now.Before(st.lockedUntil) && now.Sub(st.lastFailure) > l.policy.Forget {
		st.failures, st.level = 0, 0
	}
	return st
}

func (l *Lockout) recordFailure(st *lockState, now time.Time) (time.Duration, bool) {
	st.failures++
	st.lastFailure = now
	var d time.Duration
	switch {
	case st.level > 0:
		// 锁定过期后再失败：直接再锁，时长翻倍
		st.level++
		d = l.lockFor(st.level)
	case st.failures >= l.policy.MaxFailures:
		st.level = 1
		d = l.lockFor(1)
	default:
		return 0, false
	}
	st.lockedUntil = now.Add(d)
	return d, true
}

func (l *Lockout) lockFor(level int) time.Duration {
	d := l.policy.BaseLock
	for i := 1; i < level; i++ {
		d *= 2
		if d >= l.policy.MaxLock {
			return l.policy.MaxLock
		}
	}
	return min(d, l.policy.MaxLock)
}

// evict 在记录数达到上限时腾出空间：先清理可以遗忘的，再随机丢掉没在锁定、没有进行中尝试的。
// 正在锁定的记录不丢，否则攻击者刷满表就能解除锁定。调用方持有 l.mu。
func (l *Lockout) evict(now time.Time) {
	l.sweep(now)
	target := l.policy.MaxEntries * 9 / 10
	for k, st := range l.states {
		if len(l.states) <= target {
			return
		}
		if st.inflight == 0 && !now.Before(st.lockedUntil) {
			delete(l.states, k)
		}
	}
}

// Check 返回昵称当前是否被锁定，以及还要等多久。
func (l *Lockout) Check(nickname string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[lockKey(nickname)]
	if !ok || !now.Before(st.lockedUntil) {
		return 0, false
	}
	return st.lockedUntil.Sub(now), true
}

// Fail 直接记录一次失败（不经过 Begin），返回是否因此进入锁定以及锁定时长。
func (l *Lockout) Fail(nickname string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recordFailure(l.state(lockKey(nickname), now), now)
}

// Reset 清除昵称的失败记录（重置密码成功时调用）。
func (l *Lockout) Reset(nickname string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := lockKey(nickname)
	if st, ok := l.states[key]; ok {
		if st.inflight > 0 {
			st.failures, st.level, st.lockedUntil = 0, 0, time.Time{}
			return
		}
		delete(l.states, key)
	}
}

// Sweep 删除已经可以遗忘的记录，防止内存无限增长。
func (l *Lockout) Sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
}

func (l *Lockout) sweep(now time.Time) {
	for k, st := range l.states {
		if st.inflight == 0 && !now.Before(st.lockedUntil) && now.Sub(st.lastFailure) > l.policy.Forget {
			delete(l.states, k)
		}
	}
}

// Len 返回记录数，供测试使用。
func (l *Lockout) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.states)
}
