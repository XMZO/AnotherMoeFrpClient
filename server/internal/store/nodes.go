package store

import (
	"context"
	"database/sql"
	"errors"
)

// Node 是 nodes 表的一行。
type Node struct {
	ID               string
	Name             string
	PublicAddr       string
	Enrolled         bool // token_hash 不为空
	EnrollExpiresAt  *int64
	FrpsConfig       string
	ConfigRev        int64
	AppliedRev       int64
	ApplyError       string
	PendingUninstall bool
	Info             string // JSON
	LastIP           string
	LastSeenAt       *int64
	CreatedAt        int64
	UpdatedAt        int64
}

const nodeColumns = `id, name, public_addr, token_hash IS NOT NULL, enroll_expires_at, frps_config, config_rev,
	applied_rev, apply_error, pending_uninstall, info, last_ip, last_seen_at, created_at, updated_at`

func scanNode(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.Name, &n.PublicAddr, &n.Enrolled, &n.EnrollExpiresAt, &n.FrpsConfig, &n.ConfigRev,
		&n.AppliedRev, &n.ApplyError, &n.PendingUninstall, &n.Info, &n.LastIP, &n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	return n, err
}

// InsertNode 新建节点，同时写入安装码的哈希。
func InsertNode(ctx context.Context, q Querier, n Node, enrollHash []byte, enrollExpires int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO nodes (id, name, public_addr, enroll_hash, enroll_expires_at, frps_config,
		config_rev, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Name, n.PublicAddr, enrollHash, enrollExpires, n.FrpsConfig, n.ConfigRev, n.CreatedAt, n.UpdatedAt)
	return err
}

// GetNode 按 ID 查节点。
func GetNode(ctx context.Context, q Querier, id string) (Node, error) {
	return scanNode(q.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id))
}

// GetNodeByTokenHash 按长期令牌查节点。
func GetNodeByTokenHash(ctx context.Context, q Querier, h []byte) (Node, error) {
	return scanNode(q.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE token_hash = ?`, h))
}

// GetNodeByEnrollHash 按安装码查节点（不检查过期）。
func GetNodeByEnrollHash(ctx context.Context, q Querier, h []byte) (Node, error) {
	return scanNode(q.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE enroll_hash = ?`, h))
}

// ListNodes 按创建时间列出节点。
func ListNodes(ctx context.Context, q Querier) ([]Node, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY created_at, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func execOne(ctx context.Context, q Querier, query string, args ...any) error {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetNodeEnrolled 注册完成：写入令牌哈希，清掉安装码。
func SetNodeEnrolled(ctx context.Context, q Querier, id string, tokenHash []byte, now int64) error {
	return execOne(ctx, q, `UPDATE nodes SET token_hash = ?, enroll_hash = NULL, enroll_expires_at = NULL, updated_at = ?
		WHERE id = ?`, tokenHash, now, id)
}

// ResetNodeEnrollment 作废令牌并生成新的安装码（重置令牌 / 重新安装）。
func ResetNodeEnrollment(ctx context.Context, q Querier, id string, enrollHash []byte, expires, now int64) error {
	return execOne(ctx, q, `UPDATE nodes SET token_hash = NULL, enroll_hash = ?, enroll_expires_at = ?, updated_at = ?
		WHERE id = ?`, enrollHash, expires, now, id)
}

// UpdateNodeMeta 修改名称和客户端连接地址。
func UpdateNodeMeta(ctx context.Context, q Querier, id, name, publicAddr string, now int64) error {
	return execOne(ctx, q, `UPDATE nodes SET name = ?, public_addr = ?, updated_at = ? WHERE id = ?`, name, publicAddr, now, id)
}

// SetNodeConfig 保存新的期望配置，版本号 +1，返回新版本号。
func SetNodeConfig(ctx context.Context, q Querier, id, config string, now int64) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx, `UPDATE nodes SET frps_config = ?, config_rev = config_rev + 1, apply_error = '', updated_at = ?
		WHERE id = ? RETURNING config_rev`, config, now, id).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return rev, err
}

// SetNodeApplied 记录 agent 的配置生效情况。
func SetNodeApplied(ctx context.Context, q Querier, id string, appliedRev int64, applyErr string) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET applied_rev = ?, apply_error = ? WHERE id = ?`, appliedRev, applyErr, id)
	return err
}

// SetNodeSeen 记录 hello 信息和最后在线时间。
func SetNodeSeen(ctx context.Context, q Querier, id, info, ip string, now int64) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET info = ?, last_ip = ?, last_seen_at = ? WHERE id = ?`, info, ip, now, id)
	return err
}

// TouchNode 只更新最后在线时间。
func TouchNode(ctx context.Context, q Querier, id string, now int64) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET last_seen_at = ? WHERE id = ?`, now, id)
	return err
}

// SetNodePendingUninstall 标记离线节点上线后卸载。
func SetNodePendingUninstall(ctx context.Context, q Querier, id string, v bool) error {
	return execOne(ctx, q, `UPDATE nodes SET pending_uninstall = ? WHERE id = ?`, v, id)
}

// DeleteNode 删除节点（任务随外键级联删除）。
func DeleteNode(ctx context.Context, q Querier, id string) error {
	return execOne(ctx, q, `DELETE FROM nodes WHERE id = ?`, id)
}

// ---- 任务 ----

// NodeTask 是 node_tasks 表的一行。
type NodeTask struct {
	ID         string
	NodeID     string
	Kind       string
	Command    string
	Status     string
	ExitCode   *int
	Output     string
	Error      string
	CreatedBy  string
	CreatedAt  int64
	FinishedAt *int64
}

const taskColumns = `id, node_id, kind, command, status, exit_code, output, error, COALESCE(created_by, ''), created_at, finished_at`

func scanTask(row interface{ Scan(...any) error }) (NodeTask, error) {
	var t NodeTask
	err := row.Scan(&t.ID, &t.NodeID, &t.Kind, &t.Command, &t.Status, &t.ExitCode, &t.Output, &t.Error, &t.CreatedBy,
		&t.CreatedAt, &t.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeTask{}, ErrNotFound
	}
	return t, err
}

// InsertNodeTask 新建任务（状态 running）。
func InsertNodeTask(ctx context.Context, q Querier, t NodeTask) error {
	_, err := q.ExecContext(ctx, `INSERT INTO node_tasks (id, node_id, kind, command, status, created_by, created_at)
		VALUES (?, ?, ?, ?, 'running', NULLIF(?, ''), ?)`, t.ID, t.NodeID, t.Kind, t.Command, t.CreatedBy, t.CreatedAt)
	return err
}

// FinishNodeTask 写入任务结果。
func FinishNodeTask(ctx context.Context, q Querier, id, status string, exitCode *int, output, errMsg string, now int64) error {
	_, err := q.ExecContext(ctx, `UPDATE node_tasks SET status = ?, exit_code = ?, output = ?, error = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`, status, exitCode, output, errMsg, now, id)
	return err
}

// FinishRunningUpgrade 把节点上进行中的升级任务标记为结束（新版本连上来报告结果时调用）。
func FinishRunningUpgrade(ctx context.Context, q Querier, nodeID, status, output, errMsg string, now int64) error {
	_, err := q.ExecContext(ctx, `UPDATE node_tasks SET status = ?, output = ?, error = ?, finished_at = ?
		WHERE node_id = ? AND kind = 'upgrade' AND status = 'running'`, status, output, errMsg, now, nodeID)
	return err
}

// HasRunningUpgrade 判断节点是否有进行中的升级。
func HasRunningUpgrade(ctx context.Context, q Querier, nodeID string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM node_tasks WHERE node_id = ? AND kind = 'upgrade' AND status = 'running'`, nodeID).Scan(&n)
	return n > 0, err
}

// FailStaleTasks 把服务重启前没结束的任务标记为失败。
func FailStaleTasks(ctx context.Context, q Querier, now int64) error {
	_, err := q.ExecContext(ctx, `UPDATE node_tasks SET status = 'failed', error = '服务端重启，任务结果丢失', finished_at = ?
		WHERE status = 'running' AND kind = 'exec'`, now)
	return err
}

// GetNodeTask 按 ID 查任务。
func GetNodeTask(ctx context.Context, q Querier, id string) (NodeTask, error) {
	return scanTask(q.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM node_tasks WHERE id = ?`, id))
}

// ListNodeTasks 列出节点最近的任务（新的在前），不含输出。
func ListNodeTasks(ctx context.Context, q Querier, nodeID string, limit int) ([]NodeTask, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, node_id, kind, command, status, exit_code, '', error, COALESCE(created_by, ''),
		created_at, finished_at FROM node_tasks WHERE node_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneNodeTasks 每个节点只保留最近 keep 条任务。
func PruneNodeTasks(ctx context.Context, q Querier, keep int) error {
	_, err := q.ExecContext(ctx, `DELETE FROM node_tasks WHERE status != 'running' AND id IN (
		SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY created_at DESC, rowid DESC) AS rn
		FROM node_tasks) WHERE rn > ?)`, keep)
	return err
}
