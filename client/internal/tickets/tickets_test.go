package tickets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"moefrp/shared/api"
)

type fakePanel struct {
	mu    sync.Mutex
	now   time.Time
	seq   int
	calls atomic.Int32
	fail  error
	ttl   time.Duration
	block chan struct{}
}

func (f *fakePanel) fetch(_ context.Context, node string, n int) ([]api.NodeTicket, error) {
	f.calls.Add(1)
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	out := make([]api.NodeTicket, n)
	for i := range out {
		f.seq++
		out[i] = api.NodeTicket{Ticket: fmt.Sprintf("%s-%d", node, f.seq), ExpiresAt: f.now.Add(f.ttl).Unix()}
	}
	return out, nil
}

func newPool(f *fakePanel) *Pool {
	p := New(f.fetch)
	p.now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
	return p
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("超时：%s", what)
}

func TestPool(t *testing.T) {
	f := &fakePanel{now: time.Unix(1_000_000, 0), ttl: 15 * time.Minute}
	p := newPool(f)
	ctx := context.Background()

	// 空池：当场领满 Target 张，Peek 不取走
	t1, err := p.Peek(ctx, "n1")
	if err != nil || t1 != "n1-1" || f.calls.Load() != 1 {
		t.Fatalf("%q %v calls=%d", t1, err, f.calls.Load())
	}
	if t2, _ := p.Peek(ctx, "n1"); t2 != t1 {
		t.Fatal("没用掉之前 Peek 应返回同一张")
	}
	// 用掉一张：下一次换一张，后台补足到 Target
	p.Used("n1", t1)
	if t2, _ := p.Peek(ctx, "n1"); t2 == t1 {
		t.Fatal("用过的票据不能再给")
	}
	eventually(t, "后台补足", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.tickets["n1"]) == Target })

	// 快过期的不用：时间往前推到只剩不到 margin
	f.mu.Lock()
	f.now = f.now.Add(15*time.Minute - margin + time.Second)
	f.mu.Unlock()
	t3, err := p.Peek(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if p.validLocked("n1") != Target || t3 == t1 {
		t.Fatalf("快过期的票据应扔掉重领：%q", t3)
	}

	// 面板不可用：池子里还有就照样能用（预取的意义），没有了才报错
	f.mu.Lock()
	f.fail = errors.New("面板挂了")
	f.mu.Unlock()
	for i := 0; i < Target; i++ {
		tk, err := p.Peek(ctx, "n1")
		if err != nil {
			t.Fatalf("第 %d 张：%v", i, err)
		}
		p.Used("n1", tk)
	}
	if _, err := p.Peek(ctx, "n1"); err == nil {
		t.Fatal("池子空了、面板不可用时应报错")
	}

	// 清空：退出登录后不留票据；清空前发出的领取结果丢掉
	f.mu.Lock()
	f.fail = nil
	f.mu.Unlock()
	p.Peek(ctx, "n2")
	p.Clear()
	if p.validLocked("n2") != 0 {
		t.Fatal("Clear 后应为空")
	}
	f.block = make(chan struct{})
	go p.Peek(ctx, "n3")
	eventually(t, "开始领取", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.filling["n3"] })
	p.Clear()
	close(f.block)
	eventually(t, "领取结束", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return !p.filling["n3"] })
	if p.validLocked("n3") != 0 {
		t.Fatal("退出登录期间领到的票据应丢掉")
	}
	f.block = nil

	// Retain：不再能用的节点的票据去掉
	p.Peek(ctx, "a")
	p.Peek(ctx, "b")
	p.Retain([]string{"a"})
	if p.validLocked("a") == 0 || p.validLocked("b") != 0 {
		t.Fatal("Retain")
	}
}

// 并发 Peek 同一个节点只领一次。
func TestPoolSingleFlight(t *testing.T) {
	f := &fakePanel{now: time.Unix(1_000_000, 0), ttl: 15 * time.Minute, block: make(chan struct{})}
	p := newPool(f)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Peek(context.Background(), "n1") }()
	}
	eventually(t, "开始领取", func() bool { return f.calls.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	close(f.block)
	wg.Wait()
	if f.calls.Load() != 1 {
		t.Fatalf("领取了 %d 次", f.calls.Load())
	}
}
