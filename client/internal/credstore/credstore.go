// Package credstore 把"保持登录"的会话令牌存进 Windows 凭据管理器（PLAN 6.5）。
//
// 只保存令牌和昵称，从不保存密码。凭据只有当前 Windows 用户能读。
package credstore

import (
	"errors"
	"sync"
)

// DefaultTarget 是正式构建使用的凭据目标名。
const DefaultTarget = "MoeFrp/session"

// Cred 是保存的内容。
type Cred struct {
	Nickname string // 凭据的用户名字段，只用于在凭据管理器里辨认
	Token    string // 会话令牌（mfs_ 开头）
}

// Store 读写一条凭据。
type Store interface {
	// Load 读取凭据，不存在时 ok 为 false 且 err 为 nil。
	Load() (c Cred, ok bool, err error)
	Save(c Cred) error
	// Delete 删除凭据，不存在时不报错。
	Delete() error
}

// ErrEmptyToken 表示要保存的令牌为空。
var ErrEmptyToken = errors.New("credstore: empty token")

// Memory 是内存里的实现，供测试使用。
type Memory struct {
	mu   sync.Mutex
	cred *Cred
}

func (m *Memory) Load() (Cred, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cred == nil {
		return Cred{}, false, nil
	}
	return *m.cred, true, nil
}

func (m *Memory) Save(c Cred) error {
	if c.Token == "" {
		return ErrEmptyToken
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred = &c
	return nil
}

func (m *Memory) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred = nil
	return nil
}
