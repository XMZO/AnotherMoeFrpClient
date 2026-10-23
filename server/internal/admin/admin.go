// Package admin 实现服务器上的管理命令（PLAN 7.10）：邀请码和用户管理。
//
// 这些操作直接改数据库，不经过 API；在服务器上用 SSH 登录后执行。
// 每个操作都写一条 audit_log（actor_id 为空，detail.via = "cli"）。
package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// MaxInvitesPerCall 是一次最多生成的邀请码数量。
const MaxInvitesPerCall = 1000

// ErrUserNotFound 表示昵称不存在。
var ErrUserNotFound = errors.New("用户不存在")

// Admin 封装管理操作。
type Admin struct {
	st   *store.Store
	auth *auth.Service
	now  func() time.Time
}

// New 创建 Admin。now 为 nil 时用 time.Now。
func New(st *store.Store, a *auth.Service, now func() time.Time) *Admin {
	if now == nil {
		now = time.Now
	}
	return &Admin{st: st, auth: a, now: now}
}

func (a *Admin) audit(ctx context.Context, q store.Querier, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["via"] = "cli"
	return store.InsertAudit(ctx, q, store.AuditEntry{At: a.now().Unix(), Action: action, Target: target, Detail: detail})
}

// ---- 邀请码 ----

// CreateInvites 生成 n 个邀请码。
func (a *Admin) CreateInvites(ctx context.Context, n int, note string) ([]string, error) {
	if n < 1 || n > MaxInvitesPerCall {
		return nil, fmt.Errorf("数量必须在 1–%d 之间", MaxInvitesPerCall)
	}
	if len([]rune(note)) > 200 {
		return nil, errors.New("备注最多 200 个字符")
	}
	codes := make([]string, 0, n)
	err := a.st.Tx(ctx, func(tx *sql.Tx) error {
		now := a.now().Unix()
		for len(codes) < n {
			code := validate.NewInviteCode()
			if err := store.InsertInvite(ctx, tx, code, note, now); err != nil {
				if store.IsUniqueViolation(err) {
					continue // 31^7 的空间里撞上的概率极小，撞了就重新生成
				}
				return err
			}
			codes = append(codes, code)
		}
		return a.audit(ctx, tx, store.AuditAdminInviteCreate, "", map[string]any{"count": n, "note": note})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// ListInvites 列出邀请码。
func (a *Admin) ListInvites(ctx context.Context, unusedOnly bool) ([]store.Invite, error) {
	return store.ListInvites(ctx, a.st.DB(), unusedOnly)
}

// RevokeInvite 撤销一个未使用的邀请码。
func (a *Admin) RevokeInvite(ctx context.Context, code string) error {
	code = validate.NormalizeInviteCode(code)
	return a.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.RevokeInvite(ctx, tx, code, a.now().Unix()); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				return fmt.Errorf("邀请码 %s 不存在", code)
			case errors.Is(err, store.ErrInviteNotRevocable):
				return fmt.Errorf("邀请码 %s 已被使用或已撤销", code)
			}
			return err
		}
		return a.audit(ctx, tx, store.AuditAdminInviteRevoke, code, nil)
	})
}

// ---- 用户 ----

// UserRow 是 user list 的一行。
type UserRow struct {
	store.User
	ActiveSessions int
	LastSeen       int64 // 0 表示从未登录
}

// ListUsers 列出所有用户及其会话情况。
func (a *Admin) ListUsers(ctx context.Context) ([]UserRow, error) {
	users, err := store.ListUsers(ctx, a.st.DB())
	if err != nil {
		return nil, err
	}
	now := a.now().Unix()
	out := make([]UserRow, 0, len(users))
	for _, u := range users {
		n, err := store.CountActiveSessions(ctx, a.st.DB(), u.ID, now)
		if err != nil {
			return nil, err
		}
		last, err := store.LastSeen(ctx, a.st.DB(), u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, UserRow{User: u, ActiveSessions: n, LastSeen: last})
	}
	return out, nil
}

func (a *Admin) user(ctx context.Context, q store.Querier, nickname string) (store.User, error) {
	u, err := store.GetUserByNickname(ctx, q, strings.TrimSpace(nickname))
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, fmt.Errorf("%w：%s", ErrUserNotFound, nickname)
	}
	return u, err
}

// SetRole 修改用户角色（user / vip / admin）。
func (a *Admin) SetRole(ctx context.Context, nickname, role string) error {
	if !model.ValidRole(role) {
		return fmt.Errorf("角色只能是 user、vip 或 admin，不能是 %q", role)
	}
	return a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		if err := store.SetUserRole(ctx, tx, u.ID, role, a.now().Unix()); err != nil {
			return err
		}
		return a.audit(ctx, tx, store.AuditAdminSetRole, u.Nickname, map[string]any{"from": u.Role, "to": role})
	})
}

// Disable 停用账号并吊销其所有会话。返回吊销的会话数。
func (a *Admin) Disable(ctx context.Context, nickname string) (int64, error) {
	var n int64
	err := a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		now := a.now().Unix()
		if err := store.SetUserStatus(ctx, tx, u.ID, model.StatusDisabled, now); err != nil {
			return err
		}
		if n, err = store.RevokeUserSessions(ctx, tx, u.ID, "", store.RevokeAdmin, now); err != nil {
			return err
		}
		return a.audit(ctx, tx, store.AuditAdminDisable, u.Nickname, map[string]any{"revokedSessions": n})
	})
	return n, err
}

// Enable 重新启用账号。
func (a *Admin) Enable(ctx context.Context, nickname string) error {
	return a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		if err := store.SetUserStatus(ctx, tx, u.ID, model.StatusActive, a.now().Unix()); err != nil {
			return err
		}
		return a.audit(ctx, tx, store.AuditAdminEnable, u.Nickname, nil)
	})
}

// Kick 吊销用户的所有会话。返回吊销的会话数。
func (a *Admin) Kick(ctx context.Context, nickname string) (int64, error) {
	var n int64
	err := a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		if n, err = store.RevokeUserSessions(ctx, tx, u.ID, "", store.RevokeAdmin, a.now().Unix()); err != nil {
			return err
		}
		return a.audit(ctx, tx, store.AuditAdminKick, u.Nickname, map[string]any{"revokedSessions": n})
	})
	return n, err
}

// ResetCode 生成一次性重置码，24 小时有效。明文只返回这一次。
func (a *Admin) ResetCode(ctx context.Context, nickname string) (string, time.Time, error) {
	code, exp, err := a.auth.IssueResetCode(ctx, nickname)
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, fmt.Errorf("%w：%s", ErrUserNotFound, nickname)
	}
	return code, exp, err
}
