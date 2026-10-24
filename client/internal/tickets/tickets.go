// Package tickets 为官方节点预取登录票据（docs/node-agent.md 第 12 节）。
//
// 每次登录官方节点（包括断线重连）都要用一张新票据，票据只能用一次、十几分钟后过期。
// 池子给每个节点备几张：面板短暂不可用（重启、升级）时还能重连；用掉一张后在后台补上。
// 票据只放在内存里，退出登录或换账号时清空。
package tickets

import (
	"context"
	"sync"
	"time"

	"moefrp/shared/api"
)

const (
	// Target 是每个节点备用的票据张数。
	Target = 3
	// margin：离过期不到这么久的票据不再用（要留出连上节点的时间，也容忍一点时钟误差）。
	margin = 90 * time.Second
	// fetchTimeout 是一次领取的超时。
	fetchTimeout = 15 * time.Second
)

// Fetch 向面板领取 n 张某个节点的票据（nodeID 是面板上的节点 ID）。
type Fetch func(ctx context.Context, nodeID string, n int) ([]api.NodeTicket, error)

// Pool 见包注释。
type Pool struct {
	fetch Fetch
	now   func() time.Time

	mu      sync.Mutex
	gen     uint64 // Clear 一次加 1，丢掉清空前发出的领取结果
	tickets map[string][]api.NodeTicket
	filling map[string]bool
}

// New 创建票据池。
func New(fetch Fetch) *Pool {
	return &Pool{fetch: fetch, now: time.Now, tickets: map[string][]api.NodeTicket{}, filling: map[string]bool{}}
}

// Peek 返回一张还能用的票据但不取走：连接真正建立、登录消息发出去之后再调用 Used。
// 这样连不上节点、反复重试时不会白白用掉票据。池子空了就当场向面板领。
func (p *Pool) Peek(ctx context.Context, nodeID string) (string, error) {
	if t, ok := p.first(nodeID); ok {
		return t, nil
	}
	if err := p.fill(ctx, nodeID); err != nil {
		return "", err
	}
	if t, ok := p.first(nodeID); ok {
		return t, nil
	}
	return "", context.DeadlineExceeded // 面板给的票据已经过期（时钟差太多），按超时处理
}

// Used 标记票据已经用掉，后台补足。
func (p *Pool) Used(nodeID, ticket string) {
	p.mu.Lock()
	list := p.tickets[nodeID]
	for i, t := range list {
		if t.Ticket == ticket {
			p.tickets[nodeID] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	p.Prefetch(nodeID)
}

// Prefetch 在后台把某个节点的票据补足到 Target 张。已经在补的不重复发起；失败了等下次。
func (p *Pool) Prefetch(nodeID string) {
	p.mu.Lock()
	if p.filling[nodeID] || p.validLocked(nodeID) >= Target {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	go func() { _ = p.fill(context.Background(), nodeID) }()
}

// Clear 清空所有票据（退出登录、换账号）。
func (p *Pool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen++
	p.tickets = map[string][]api.NodeTicket{}
}

// Discard 扔掉一个节点的全部票据（节点说票据无效：密钥轮换、凭证重置），下次登录前重新领。
func (p *Pool) Discard(nodeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen++ // 正在领的那批也不要了（可能是用旧密钥签的）
	delete(p.tickets, nodeID)
}

// Retain 只保留这些节点的票据（官方节点列表变化后，去掉不再能用的节点）。
func (p *Pool) Retain(nodeIDs []string) {
	keep := map[string]bool{}
	for _, id := range nodeIDs {
		keep[id] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id := range p.tickets {
		if !keep[id] {
			delete(p.tickets, id)
		}
	}
}

// first 返回最早过期的那张还能用的票据（先用快过期的），顺便扔掉不能用的。
func (p *Pool) first(nodeID string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.validLocked(nodeID) == 0 {
		return "", false
	}
	return p.tickets[nodeID][0].Ticket, true
}

// validLocked 扔掉快过期的票据，返回剩下的张数。调用方持有锁。
func (p *Pool) validLocked(nodeID string) int {
	deadline := p.now().Add(margin).Unix()
	list := p.tickets[nodeID][:0]
	for _, t := range p.tickets[nodeID] {
		if t.ExpiresAt > deadline {
			list = append(list, t)
		}
	}
	p.tickets[nodeID] = list
	return len(list)
}

// fill 领取票据补足到 Target 张。同一节点同时只有一个领取在进行，其他调用者等它结束。
func (p *Pool) fill(ctx context.Context, nodeID string) error {
	for {
		p.mu.Lock()
		if !p.filling[nodeID] {
			break
		}
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	n := Target - p.validLocked(nodeID)
	if n <= 0 {
		p.mu.Unlock()
		return nil
	}
	p.filling[nodeID] = true
	gen := p.gen
	p.mu.Unlock()

	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	got, err := p.fetch(fctx, nodeID, n)
	cancel()

	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.filling, nodeID)
	if err != nil {
		return err
	}
	if gen != p.gen {
		return nil // 领取期间退出了登录
	}
	p.tickets[nodeID] = append(p.tickets[nodeID], got...)
	// 先用快过期的
	list := p.tickets[nodeID]
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].ExpiresAt < list[j-1].ExpiresAt; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	return nil
}
