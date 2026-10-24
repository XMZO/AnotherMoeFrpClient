// Package admin 实现管理后台（/admin 网页）背后的操作：概览、邀请码、用户、审计日志、备份。
//
// 每个修改操作都写一条 audit_log，actor_id 为操作的管理员，detail.via = "web"。
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// MaxInvitesPerCall 是一次最多生成的邀请码数量。
const MaxInvitesPerCall = 200

// Actor 是执行操作的管理员。
type Actor struct {
	UserID string
	IP     string
}

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

func (a *Admin) audit(ctx context.Context, q store.Querier, by Actor, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["via"] = "web"
	return store.InsertAudit(ctx, q, store.AuditEntry{At: a.now().Unix(), ActorID: by.UserID, Action: action, Target: target, IP: by.IP, Detail: detail})
}

func badRequest(field, msg string) error {
	return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{Field: field, Reason: validate.ReasonInvalid, Message: msg}})
}

func unixPtr(p *int64) *time.Time {
	if p == nil || *p == 0 {
		return nil
	}
	t := time.Unix(*p, 0).UTC()
	return &t
}

func unixTime(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := time.Unix(v, 0).UTC()
	return &t
}

// ---- 概览 ----

// Overview 是概览页的统计数字。
type Overview struct {
	Users          int `json:"users"`
	Admins         int `json:"admins"`
	DisabledUsers  int `json:"disabledUsers"`
	OnlineUsers    int `json:"onlineUsers"` // 有未过期会话的用户
	ActiveToday    int `json:"activeToday"` // 24 小时内用过的用户
	Profiles       int `json:"profiles"`
	ActiveShares   int `json:"activeShares"`
	Subscriptions  int `json:"subscriptions"`
	InvitesUnused  int `json:"invitesUnused"`
	InvitesUsed    int `json:"invitesUsed"`
	RegisteredWeek int `json:"registeredWeek"` // 7 天内注册
}

// Overview 汇总统计数字。
func (a *Admin) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	now := a.now().Unix()
	err := a.st.ReadTx(ctx, func(tx *sql.Tx) error {
		queries := []struct {
			dst  *int
			sql  string
			args []any
		}{
			{&o.Users, `SELECT count(*) FROM users`, nil},
			{&o.Admins, `SELECT count(*) FROM users WHERE role = 'admin'`, nil},
			{&o.DisabledUsers, `SELECT count(*) FROM users WHERE status = 'disabled'`, nil},
			{&o.OnlineUsers, `SELECT count(DISTINCT user_id) FROM sessions WHERE revoked_at IS NULL AND expires_at > ? AND kind = 'client'`, []any{now}},
			{&o.ActiveToday, `SELECT count(DISTINCT user_id) FROM sessions WHERE last_seen_at > ? AND kind = 'client'`, []any{now - 86400}},
			{&o.Profiles, `SELECT count(*) FROM profiles`, nil},
			{&o.ActiveShares, `SELECT count(*) FROM shares WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, []any{now}},
			{&o.Subscriptions, `SELECT count(*) FROM subscriptions`, nil},
			{&o.InvitesUnused, `SELECT count(*) FROM invite_codes WHERE used_at IS NULL AND revoked_at IS NULL`, nil},
			{&o.InvitesUsed, `SELECT count(*) FROM invite_codes WHERE used_at IS NOT NULL`, nil},
			{&o.RegisteredWeek, `SELECT count(*) FROM users WHERE created_at > ?`, []any{now - 7*86400}},
		}
		for _, q := range queries {
			if err := tx.QueryRowContext(ctx, q.sql, q.args...).Scan(q.dst); err != nil {
				return err
			}
		}
		return nil
	})
	return o, err
}

// ---- 邀请码 ----

// Invite 是返回给网页的邀请码。
type Invite struct {
	Code      string     `json:"code"`
	Note      string     `json:"note"`
	Status    string     `json:"status"` // unused / used / revoked
	CreatedAt time.Time  `json:"createdAt"`
	UsedBy    string     `json:"usedBy,omitempty"` // 使用者昵称（用户已删除时为空）
	UsedAt    *time.Time `json:"usedAt,omitempty"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

func inviteStatus(inv store.Invite) string {
	switch {
	case inv.RevokedAt != nil:
		return "revoked"
	case inv.UsedAt != nil:
		return "used"
	}
	return "unused"
}

// CreateInvites 生成 n 个邀请码。
func (a *Admin) CreateInvites(ctx context.Context, by Actor, n int, note string) ([]string, error) {
	note = strings.TrimSpace(note)
	if n < 1 || n > MaxInvitesPerCall {
		return nil, badRequest("count", fmt.Sprintf("数量必须在 1–%d 之间", MaxInvitesPerCall))
	}
	if len([]rune(note)) > 200 {
		return nil, badRequest("note", "备注最多 200 个字符")
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
		return a.audit(ctx, tx, by, store.AuditAdminInviteCreate, "", map[string]any{"count": n, "note": note})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// ListInvites 列出邀请码，新的在前。status 为 unused / used / revoked，空串表示全部。
func (a *Admin) ListInvites(ctx context.Context, status string) ([]Invite, error) {
	switch status {
	case "", "all", "unused", "used", "revoked":
	default:
		return nil, badRequest("status", "status 只能是 unused、used、revoked 或 all")
	}
	rows, err := store.ListInvites(ctx, a.st.DB(), false)
	if err != nil {
		return nil, err
	}
	out := make([]Invite, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		inv := Invite{
			Code: r.Code, Note: r.Note, Status: inviteStatus(r), CreatedAt: time.Unix(r.CreatedAt, 0).UTC(),
			UsedBy: r.UsedByNickname, UsedAt: unixPtr(r.UsedAt), RevokedAt: unixPtr(r.RevokedAt),
		}
		if status == "" || status == "all" || status == inv.Status {
			out = append(out, inv)
		}
	}
	return out, nil
}

// RevokeInvite 撤销一个未使用的邀请码。
func (a *Admin) RevokeInvite(ctx context.Context, by Actor, code string) error {
	code = validate.NormalizeInviteCode(code)
	return a.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.RevokeInvite(ctx, tx, code, a.now().Unix()); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				return apierr.New(apierr.NotFound)
			case errors.Is(err, store.ErrInviteNotRevocable):
				return apierr.Newf(apierr.InviteUsed, "邀请码已被使用或已撤销")
			}
			return err
		}
		return a.audit(ctx, tx, by, store.AuditAdminInviteRevoke, code, nil)
	})
}

// ---- 用户 ----

// User 是返回给网页的用户信息。
type User struct {
	ID             string     `json:"id"`
	Nickname       string     `json:"nickname"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
	ActiveSessions int        `json:"activeSessions"` // 客户端会话
	LastSeen       *time.Time `json:"lastSeen,omitempty"`
	Profiles       int        `json:"profiles"`
	Shares         int        `json:"shares"`
	Subscriptions  int        `json:"subscriptions"`
}

// ListUsers 列出所有用户（按注册时间倒序）及其会话、配置数量。
func (a *Admin) ListUsers(ctx context.Context) ([]User, error) {
	now := a.now().Unix()
	rows, err := a.st.DB().QueryContext(ctx, `
		SELECT u.id, u.nickname, u.role, u.status, u.created_at,
		       (SELECT count(*) FROM sessions s WHERE s.user_id = u.id AND s.kind = 'client' AND s.revoked_at IS NULL AND s.expires_at > ?),
		       (SELECT coalesce(max(last_seen_at), 0) FROM sessions s WHERE s.user_id = u.id AND s.kind = 'client'),
		       (SELECT count(*) FROM profiles p WHERE p.owner_id = u.id),
		       (SELECT count(*) FROM shares sh WHERE sh.owner_id = u.id AND sh.revoked_at IS NULL),
		       (SELECT count(*) FROM subscriptions sb WHERE sb.user_id = u.id)
		  FROM users u ORDER BY u.created_at DESC, u.nickname`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var (
			u        User
			created  int64
			lastSeen int64
		)
		if err := rows.Scan(&u.ID, &u.Nickname, &u.Role, &u.Status, &created, &u.ActiveSessions, &lastSeen, &u.Profiles, &u.Shares, &u.Subscriptions); err != nil {
			return nil, err
		}
		u.CreatedAt = time.Unix(created, 0).UTC()
		u.LastSeen = unixTime(lastSeen)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (a *Admin) user(ctx context.Context, q store.Querier, nickname string) (store.User, error) {
	u, err := store.GetUserByNickname(ctx, q, strings.TrimSpace(nickname))
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, apierr.New(apierr.NotFound)
	}
	return u, err
}

// notSelf：管理员不能停用自己、降低自己的角色，避免把自己锁在门外。
func notSelf(by Actor, u store.User, what string) error {
	if by.UserID == u.ID {
		return apierr.Newf(apierr.Forbidden, "不能%s自己的账号", what)
	}
	return nil
}

// SetRole 修改用户角色（user / vip / admin）。
func (a *Admin) SetRole(ctx context.Context, by Actor, nickname, role string) error {
	if !model.ValidRole(role) {
		return badRequest("role", "角色只能是 user、vip 或 admin")
	}
	return a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		if u.Role == role {
			return nil
		}
		if err := notSelf(by, u, "修改"); err != nil {
			return err
		}
		if err := store.SetUserRole(ctx, tx, u.ID, role, a.now().Unix()); err != nil {
			return err
		}
		if u.Role == model.RoleAdmin {
			// 不再是管理员：管理后台的会话立即失效
			if _, err := store.RevokeUserSessionsOfKind(ctx, tx, u.ID, store.SessionAdminWeb, store.RevokeAdmin, a.now().Unix()); err != nil {
				return err
			}
		}
		return a.audit(ctx, tx, by, store.AuditAdminSetRole, u.Nickname, map[string]any{"from": u.Role, "to": role})
	})
}

// SetDisabled 停用（并吊销所有会话）或重新启用账号。返回吊销的会话数。
func (a *Admin) SetDisabled(ctx context.Context, by Actor, nickname string, disabled bool) (int64, error) {
	var n int64
	err := a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		now := a.now().Unix()
		if !disabled {
			if err := store.SetUserStatus(ctx, tx, u.ID, model.StatusActive, now); err != nil {
				return err
			}
			return a.audit(ctx, tx, by, store.AuditAdminEnable, u.Nickname, nil)
		}
		if err := notSelf(by, u, "停用"); err != nil {
			return err
		}
		if err := store.SetUserStatus(ctx, tx, u.ID, model.StatusDisabled, now); err != nil {
			return err
		}
		if n, err = store.RevokeUserSessions(ctx, tx, u.ID, "", store.RevokeAdmin, now); err != nil {
			return err
		}
		return a.audit(ctx, tx, by, store.AuditAdminDisable, u.Nickname, map[string]any{"revokedSessions": n})
	})
	return n, err
}

// Kick 吊销用户所有客户端会话（让桌面客户端下线）。返回吊销的会话数。
func (a *Admin) Kick(ctx context.Context, by Actor, nickname string) (int64, error) {
	var n int64
	err := a.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := a.user(ctx, tx, nickname)
		if err != nil {
			return err
		}
		if n, err = store.RevokeUserSessionsOfKind(ctx, tx, u.ID, store.SessionClient, store.RevokeAdmin, a.now().Unix()); err != nil {
			return err
		}
		return a.audit(ctx, tx, by, store.AuditAdminKick, u.Nickname, map[string]any{"revokedSessions": n})
	})
	return n, err
}

// ResetCode 生成一次性重置码，24 小时有效。明文只返回这一次。
func (a *Admin) ResetCode(ctx context.Context, by Actor, nickname string) (string, time.Time, error) {
	u, err := a.user(ctx, a.st.DB(), nickname)
	if err != nil {
		return "", time.Time{}, err
	}
	code, exp, err := a.auth.IssueResetCode(ctx, u.Nickname, by.UserID, by.IP)
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, apierr.New(apierr.NotFound)
	}
	return code, exp, err
}

// ---- 审计日志 ----

// AuditEntry 是返回给网页的审计记录。
type AuditEntry struct {
	ID     int64          `json:"id"`
	At     time.Time      `json:"at"`
	Actor  string         `json:"actor,omitempty"` // 操作者昵称（用户已删除或系统操作时为空）
	Action string         `json:"action"`
	Target string         `json:"target,omitempty"`
	IP     string         `json:"ip,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

// ListAudit 按时间倒序列出审计记录。action 为空表示全部，可以是前缀（例如 "admin." 或 "auth."）。
func (a *Admin) ListAudit(ctx context.Context, action string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT a.id, a.at, coalesce(u.nickname, ''), a.action, coalesce(a.target, ''), coalesce(a.ip, ''), coalesce(a.detail, '')
	            FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id`
	var args []any
	if action != "" {
		if strings.HasSuffix(action, ".") {
			query += ` WHERE a.action LIKE ? ESCAPE '\'`
			args = append(args, strings.ReplaceAll(strings.ReplaceAll(action, `_`, `\_`), `%`, `\%`)+"%")
		} else {
			query += ` WHERE a.action = ?`
			args = append(args, action)
		}
	}
	query += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := a.st.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var (
			e      AuditEntry
			at     int64
			detail string
		)
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Target, &e.IP, &detail); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		if detail != "" {
			_ = json.Unmarshal([]byte(detail), &e.Detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- 备份 ----

// Backup 用 VACUUM INTO 生成一份一致的数据库备份，写到 w。临时文件用完即删。
func (a *Admin) Backup(ctx context.Context, by Actor, w io.Writer) (int64, error) {
	dir, err := os.MkdirTemp(filepath.Dir(a.st.Path()), ".backup-")
	if err != nil {
		dir, err = os.MkdirTemp("", "moefrp-backup-")
		if err != nil {
			return 0, err
		}
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "moefrp.db")
	if err := a.st.Backup(ctx, tmp); err != nil {
		return 0, err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := store.InsertAudit(ctx, a.st.DB(), store.AuditEntry{At: a.now().Unix(), ActorID: by.UserID, Action: store.AuditAdminBackup, IP: by.IP,
		Detail: map[string]any{"via": "web"}}); err != nil {
		return 0, err
	}
	return io.Copy(w, f)
}
