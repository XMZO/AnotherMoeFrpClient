package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	l := New(Rule{Limit: 10, Window: time.Minute})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow("1.2.3.4", now); !ok {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	ok, wait := l.Allow("1.2.3.4", now)
	if ok || wait <= 0 || wait > 6*time.Second {
		t.Fatalf("第 11 次应拒绝并给出等待时间（约 6 秒）：%v %v", ok, wait)
	}
	if ok, _ := l.Allow("5.6.7.8", now); !ok {
		t.Fatal("不同的 key 互不影响")
	}
	// 被拒绝的请求不消耗配额：6 秒后恰好补回一个
	if ok, _ := l.Allow("1.2.3.4", now.Add(6*time.Second)); !ok {
		t.Fatal("6 秒后应补回一个配额")
	}
	if ok, _ := l.Allow("1.2.3.4", now.Add(6*time.Second)); ok {
		t.Fatal("只补回了一个")
	}
}

func TestSweepAndDisabled(t *testing.T) {
	l := New(Rule{Limit: 2, Window: time.Second})
	now := time.Now()
	l.Allow("a", now)
	l.Allow("b", now)
	l.Sweep(now)
	if l.Len() != 2 {
		t.Fatal("还没补满的桶不能清理")
	}
	l.Sweep(now.Add(2 * time.Second))
	if l.Len() != 0 {
		t.Fatalf("补满的桶应被清理，剩 %d", l.Len())
	}
	off := New(Rule{})
	for i := 0; i < 1000; i++ {
		if ok, _ := off.Allow("x", now); !ok {
			t.Fatal("Limit=0 表示不限流")
		}
	}
}

func TestMaxKeys(t *testing.T) {
	l := New(Rule{Limit: 1, Window: time.Hour})
	l.SetMaxKeys(10)
	now := time.Now()
	for i := 0; i < 100; i++ {
		l.Allow(string(rune('a'+i%26))+string(rune('a'+i/26)), now)
	}
	if l.Len() > 10 {
		t.Fatalf("key 数应被限制在 10 以内，实际 %d", l.Len())
	}
}
