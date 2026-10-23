package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// 审计动作（audit_log.action），PLAN 7.9。
const (
	AuditRegister       = "auth.register"
	AuditLogin          = "auth.login"
	AuditLoginFailed    = "auth.login_failed"
	AuditChangePassword = "auth.change_password"
	AuditResetPassword  = "auth.reset_password"

	AuditAdminInviteCreate = "admin.invite_create"
	AuditAdminInviteRevoke = "admin.invite_revoke"
	AuditAdminSetRole      = "admin.set_role"
	AuditAdminDisable      = "admin.disable"
	AuditAdminEnable       = "admin.enable"
	AuditAdminResetCode    = "admin.reset_code"
	AuditAdminKick         = "admin.kick"
	AuditAdminSetup        = "admin.setup"  // 初始化页面创建管理员
	AuditAdminLogin        = "admin.login"  // 预留
	AuditAdminBackup       = "admin.backup" // 下载数据库备份

	AuditShareCreate    = "share.create"
	AuditShareRepublish = "share.republish"
	AuditShareRevoke    = "share.revoke"
)

// AuditEntry 是一条审计记录。Detail 会被序列化成 JSON，绝不能放密码、令牌、重置码和节点 token。
type AuditEntry struct {
	At      int64
	ActorID string
	Action  string
	Target  string
	IP      string
	Detail  map[string]any
}

// InsertAudit 写一条审计记录。
func InsertAudit(ctx context.Context, q Querier, e AuditEntry) error {
	var detail sql.NullString
	if len(e.Detail) > 0 {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = sql.NullString{String: string(b), Valid: true}
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO audit_log (at, actor_id, action, target, ip, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At, nullString(e.ActorID), e.Action, nullString(e.Target), nullString(e.IP), detail)
	return err
}

// AuditRow 是读出来的审计记录，Detail 保持 JSON 原文。
type AuditRow struct {
	ID      int64
	At      int64
	ActorID string
	Action  string
	Target  string
	IP      string
	Detail  string
}

// ListAudit 按时间倒序列出最近的审计记录；action 非空时只列该动作。
func ListAudit(ctx context.Context, q Querier, action string, limit int) ([]AuditRow, error) {
	query := `SELECT id, at, actor_id, action, target, ip, detail FROM audit_log`
	var args []any
	if action != "" {
		query += ` WHERE action = ?`
		args = append(args, action)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var (
			r                         AuditRow
			actor, target, ip, detail sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.At, &actor, &r.Action, &target, &ip, &detail); err != nil {
			return nil, err
		}
		r.ActorID, r.Target, r.IP, r.Detail = actor.String, target.String, ip.String, detail.String
		out = append(out, r)
	}
	return out, rows.Err()
}
