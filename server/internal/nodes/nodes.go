// Package nodes 管理 frps 节点：注册、agent 长连接、配置下发、执行命令、升级、卸载（docs/node-agent.md）。
package nodes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"moefrp/server/internal/store"
	"moefrp/shared/agentproto"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

const (
	tokenPrefix   = "mfn_"
	enrollTTL     = 24 * time.Hour
	maxNameLen    = 40
	maxConfigSize = 64 << 10
	keepTasks     = 100
	callTimeout   = 20 * time.Second // 要小于 http.Server 的 WriteTimeout（30 秒）
)

// Actor 是发起操作的管理员。
type Actor struct {
	UserID string
	IP     string
}

// Options 是创建 Service 的参数。
type Options struct {
	Store *store.Store
	Log   *slog.Logger
	Now   func() time.Time
	// Source 提供可安装、升级的 agent 版本（GitHub Release 或本地目录）。nil 时不能安装和升级
	Source Source
}

// Service 管理所有节点。
type Service struct {
	st  *store.Store
	log *slog.Logger
	now func() time.Time
	src Source

	mu          sync.Mutex
	conns       map[string]*agentConn
	live        map[string]*liveState
	pushed      map[string]string // 节点 ID → 上次推送的授权表哈希
	gone        map[string]*goneState // 刚断开、还在宽限期内的节点（见 OfflineGrace）
	drops       map[string][]time.Time // 节点最近 24 小时的断线时间
	accessTimer *time.Timer

	keyMu sync.Mutex
	key   []byte // 节点凭证的面板密钥
}

// New 创建 Service，并把服务端重启前没结束的命令任务标记为失败。
func New(o Options) *Service {
	s := &Service{st: o.Store, log: o.Log, now: o.Now, src: o.Source,
		conns: map[string]*agentConn{}, live: map[string]*liveState{}, pushed: map[string]string{},
		gone: map[string]*goneState{}, drops: map[string][]time.Time{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if err := store.FailStaleTasks(context.Background(), s.st.DB(), s.now().Unix()); err != nil {
		s.log.Error("nodes: fail stale tasks", "err", err)
	}
	return s
}

// Source 返回 agent 来源（可能为 nil）。
func (s *Service) Source() Source { return s.src }

// Dist 返回本地目录来源；来源是 GitHub 或没有配置时返回 nil。
func (s *Service) Dist() *Dist {
	d, _ := s.src.(*Dist)
	return d
}

// cachedBuild 立即返回已知的最新版本（不等网络），没有时为 nil。
func (s *Service) cachedBuild() *Build {
	if s.src == nil {
		return nil
	}
	b, _ := s.src.Cached()
	return b
}

// ---- 视图 ----

// ClientInfo 是客户端连接这个节点需要的信息（从 frps 配置里解析）。
type ClientInfo struct {
	Addr  string `json:"addr"`
	Port  int    `json:"port"`
	Token string `json:"token,omitempty"`
}

// View 是返回给管理后台的节点信息。
type View struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	PublicAddr       string             `json:"publicAddr"`
	DownloadMirror   string             `json:"downloadMirror"` // 安装和升级下载 agent 用的代理前缀，空表示直连
	Status           string             `json:"status"`         // pending / online / offline
	Reconnecting     bool               `json:"reconnecting,omitempty"` // 刚断开，宽限期内还算在线（status 为 online）
	Drops24h         int                `json:"drops24h"`               // 最近 24 小时断线次数，看节点稳不稳
	Enrolled         bool               `json:"enrolled"`
	EnrollExpiresAt  *time.Time         `json:"enrollExpiresAt,omitempty"`
	ConfigRev        int64              `json:"configRev"`
	AppliedRev       int64              `json:"appliedRev"`
	ApplyError       string             `json:"applyError,omitempty"`
	PendingUninstall bool               `json:"pendingUninstall"`
	Info             *agentproto.Hello  `json:"info,omitempty"`
	LastIP           string             `json:"lastIp,omitempty"`
	LastSeen         *time.Time         `json:"lastSeen,omitempty"`
	ConnectedAt      *time.Time         `json:"connectedAt,omitempty"`
	CreatedAt        time.Time          `json:"createdAt"`
	Report           *agentproto.Report `json:"report,omitempty"`
	NetRxRate        float64            `json:"netRxRate"` // 字节/秒
	NetTxRate        float64            `json:"netTxRate"`
	Client           *ClientInfo        `json:"client,omitempty"`
	UpgradeTo        string             `json:"upgradeTo,omitempty"`   // 有新版本可升级时为目标版本
	FrpsConfig       *string            `json:"frpsConfig,omitempty"`  // 只在详情里返回
	Official         bool               `json:"official"`              // 下发给用户
	Roles            []string           `json:"roles"`                 // 开放给哪些角色
	Description      string             `json:"description,omitempty"` // 给用户看的说明
	AccessSupported  bool               `json:"accessSupported"`       // agent 支持官方节点的登录票据（不知道版本时为 false）
}

// 节点状态。
const (
	StatusPending = "pending" // 还没安装（没注册）
	StatusOnline  = "online"
	StatusOffline = "offline"
)

func unixTime(p *int64) *time.Time {
	if p == nil || *p == 0 {
		return nil
	}
	t := time.Unix(*p, 0).UTC()
	return &t
}

func (s *Service) view(n store.Node, withConfig bool) View {
	v := View{
		ID: n.ID, Name: n.Name, PublicAddr: n.PublicAddr, DownloadMirror: n.DownloadMirror, Enrolled: n.Enrolled,
		EnrollExpiresAt: unixTime(n.EnrollExpiresAt), ConfigRev: n.ConfigRev, AppliedRev: n.AppliedRev,
		ApplyError: n.ApplyError, PendingUninstall: n.PendingUninstall, LastIP: n.LastIP,
		LastSeen: unixTime(n.LastSeenAt), CreatedAt: time.Unix(n.CreatedAt, 0).UTC(),
	}
	if n.Info != "" && n.Info != "{}" {
		var h agentproto.Hello
		if json.Unmarshal([]byte(n.Info), &h) == nil {
			v.Info = &h
		}
	}
	switch {
	case !n.Enrolled:
		v.Status = StatusPending
	default:
		v.Status = StatusOffline
	}
	s.mu.Lock()
	if c := s.conns[n.ID]; c != nil {
		v.Status = StatusOnline
		t := c.connectedAt.UTC()
		v.ConnectedAt = &t
		h := c.hello
		v.Info = &h
	} else if g := s.graceLocked(n.ID); g != nil {
		// 刚断开：多半几秒内就重连上了，先不显示离线
		v.Status, v.Reconnecting = StatusOnline, true
		t := g.connectedAt.UTC()
		v.ConnectedAt = &t
		h := g.hello
		v.Info = &h
	}
	v.Drops24h = s.dropsLocked(n.ID)
	if l := s.live[n.ID]; l != nil && v.Status == StatusOnline {
		r := l.report
		v.Report, v.NetRxRate, v.NetTxRate = &r, l.rxRate, l.txRate
	}
	s.mu.Unlock()
	if ci := clientInfo(n.FrpsConfig); ci != nil {
		ci.Addr = n.PublicAddr
		if ci.Addr == "" {
			ci.Addr = n.LastIP
		}
		v.Client = ci
	}
	if b := s.cachedBuild(); b != nil && v.Info != nil && v.Info.AgentVersion != "" && v.Info.OS == "linux" &&
		b.Has(v.Info.Arch) && newer(b.Version, v.Info.AgentVersion) {
		v.UpgradeTo = b.Version
	}
	v.Official, v.Roles, v.Description = n.Official, parseRoles(n.Roles), n.Description
	v.AccessSupported = hasFeature(v.Info, agentproto.FeatureTickets)
	if withConfig {
		c := n.FrpsConfig
		v.FrpsConfig = &c
	}
	return v
}

// clientInfo 从 frps 配置里取出 bindPort 和 auth.token。解析失败时返回 nil。
func clientInfo(cfg string) *ClientInfo {
	if strings.TrimSpace(cfg) == "" {
		return nil
	}
	var c struct {
		BindPort int `toml:"bindPort"`
		Auth     struct {
			Token string `toml:"token"`
		} `toml:"auth"`
	}
	if toml.Unmarshal([]byte(cfg), &c) != nil {
		return nil
	}
	if c.BindPort == 0 {
		c.BindPort = 7000
	}
	return &ClientInfo{Port: c.BindPort, Token: c.Auth.Token}
}

// ---- 管理操作 ----

func (s *Service) audit(ctx context.Context, q store.Querier, by Actor, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["via"] = "web"
	return store.InsertAudit(ctx, q, store.AuditEntry{At: s.now().Unix(), ActorID: by.UserID, Action: action, Target: target, IP: by.IP, Detail: detail})
}

func badRequest(field, msg string) error {
	return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{Field: field, Reason: validate.ReasonInvalid, Message: msg}})
}

func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return apierr.Newf(apierr.NotFound, "节点不存在")
	}
	return err
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", badRequest("name", fmt.Sprintf("名称不能为空，最多 %d 个字符", maxNameLen))
	}
	return name, nil
}

func randomB64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("nodes: crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashSecret(kind, v string) []byte {
	sum := sha256.Sum256([]byte("moefrp-node-" + kind + ":" + v))
	return sum[:]
}

func newEnrollCode() string {
	r := validate.RandomString(validate.InviteCharset, 12)
	return "MFN-" + r[:4] + "-" + r[4:8] + "-" + r[8:]
}

func normalizeEnrollCode(c string) string { return strings.ToUpper(strings.TrimSpace(c)) }

// DefaultFrpsConfig 是新节点的初始 frps 配置。
func DefaultFrpsConfig() string {
	return `# frps 配置（TOML），说明见 https://gofrp.org/zh-cn/docs/reference/server-configures/
bindPort = 7000
auth.token = "` + randomB64(18) + `"

# 允许客户端使用的远程端口
allowPorts = [{ start = 10000, end = 60000 }]
# 每个客户端最多占用的端口数，0 表示不限
maxPortsPerClient = 0

# 让客户端通过域名访问内网网站时取消注释（需要把域名解析到这台机器）：
# vhostHTTPPort = 80
# vhostHTTPSPort = 443
# subDomainHost = "frp.example.com"
`
}

// Created 是新建节点的结果：安装码只返回这一次。
type Created struct {
	Node       View      `json:"node"`
	EnrollCode string    `json:"enrollCode"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Create 新建节点并生成安装码。mirror 是这个节点下载 agent 用的代理前缀（安装和以后升级都用），空表示直连。
func (s *Service) Create(ctx context.Context, by Actor, name, mirror string) (Created, error) {
	name, err := checkName(name)
	if err != nil {
		return Created{}, err
	}
	if mirror, err = NormalizeMirror(mirror); err != nil {
		return Created{}, badRequest("downloadMirror", err.Error())
	}
	now := s.now()
	code := newEnrollCode()
	exp := now.Add(enrollTTL)
	n := store.Node{ID: model.NewID(), Name: name, DownloadMirror: mirror, FrpsConfig: DefaultFrpsConfig(), ConfigRev: 1,
		CreatedAt: now.Unix(), UpdatedAt: now.Unix()}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.InsertNode(ctx, tx, n, hashSecret("enroll", code), exp.Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeCreate, name, map[string]any{"node": n.ID, "mirror": mirror})
	})
	if err != nil {
		return Created{}, err
	}
	n, err = store.GetNode(ctx, s.st.DB(), n.ID)
	if err != nil {
		return Created{}, err
	}
	return Created{Node: s.view(n, false), EnrollCode: code, ExpiresAt: exp.UTC()}, nil
}

// List 列出所有节点。
func (s *Service) List(ctx context.Context) ([]View, error) {
	ns, err := store.ListNodes(ctx, s.st.DB())
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(ns))
	for _, n := range ns {
		out = append(out, s.view(n, false))
	}
	return out, nil
}

// Get 返回节点详情（包括 frps 配置）。
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return View{}, notFound(err)
	}
	return s.view(n, true), nil
}

// Update 修改名称、客户端连接地址和下载代理。mirror 为 nil 时不改下载代理。
func (s *Service) Update(ctx context.Context, by Actor, id, name, publicAddr string, mirror *string) error {
	name, err := checkName(name)
	if err != nil {
		return err
	}
	publicAddr = strings.TrimSpace(publicAddr)
	if len(publicAddr) > 253 || strings.ContainsAny(publicAddr, " /:") {
		return badRequest("publicAddr", "地址只填域名或 IP，不要带协议和端口")
	}
	return notFound(s.st.Tx(ctx, func(tx *sql.Tx) error {
		n, err := store.GetNode(ctx, tx, id)
		if err != nil {
			return err
		}
		m := n.DownloadMirror
		if mirror != nil {
			if m, err = NormalizeMirror(*mirror); err != nil {
				return badRequest("downloadMirror", err.Error())
			}
		}
		if err := store.UpdateNodeMeta(ctx, tx, id, name, publicAddr, m, s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeUpdate, name, map[string]any{"node": id, "publicAddr": publicAddr, "mirror": m})
	}))
}

// ApplyOutcome 是保存配置后的结果。
type ApplyOutcome struct {
	Rev    int64                   `json:"rev"`
	Pushed bool                    `json:"pushed"` // 节点在线，已下发
	Result *agentproto.ApplyResult `json:"result,omitempty"`
}

// SetConfig 保存期望配置；节点在线时立即下发并等待结果。
func (s *Service) SetConfig(ctx context.Context, by Actor, id, cfg string) (ApplyOutcome, error) {
	if len(cfg) > maxConfigSize {
		return ApplyOutcome{}, badRequest("config", "配置太长")
	}
	if strings.TrimSpace(cfg) == "" {
		return ApplyOutcome{}, badRequest("config", "配置不能为空")
	}
	var rev int64
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if rev, err = store.SetNodeConfig(ctx, tx, id, cfg, s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeConfig, id, map[string]any{"rev": rev})
	})
	if err != nil {
		return ApplyOutcome{}, notFound(err)
	}
	out := ApplyOutcome{Rev: rev}
	if !s.Online(id) {
		return out, nil
	}
	res, err := s.push(ctx, id, rev, cfg)
	if err != nil {
		return out, err
	}
	out.Pushed, out.Result = true, &res
	return out, nil
}

// push 下发配置并记录结果。
func (s *Service) push(ctx context.Context, id string, rev int64, cfg string) (agentproto.ApplyResult, error) {
	var res agentproto.ApplyResult
	if err := s.Call(ctx, id, agentproto.MethodFrpsApply, agentproto.ApplyRequest{Rev: rev, Config: cfg}, &res, callTimeout); err != nil {
		return res, err
	}
	applied := s.appliedRevOf(id)
	if res.Applied {
		applied = res.Rev
	}
	if err := store.SetNodeApplied(ctx, s.st.DB(), id, applied, res.Error); err != nil {
		s.log.Error("nodes: set applied", "node", id, "err", err)
	}
	return res, nil
}

func (s *Service) appliedRevOf(id string) int64 {
	n, err := store.GetNode(context.Background(), s.st.DB(), id)
	if err != nil {
		return 0
	}
	return n.AppliedRev
}

// ResetToken 作废节点令牌并生成新的安装码，在线的 agent 立即断开。
func (s *Service) ResetToken(ctx context.Context, by Actor, id string) (Created, error) {
	now := s.now()
	code := newEnrollCode()
	exp := now.Add(enrollTTL)
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.ResetNodeEnrollment(ctx, tx, id, hashSecret("enroll", code), exp.Unix(), now.Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeResetToken, id, nil)
	})
	if err != nil {
		return Created{}, notFound(err)
	}
	s.disconnect(id, "token reset")
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return Created{}, err
	}
	return Created{Node: s.view(n, false), EnrollCode: code, ExpiresAt: exp.UTC()}, nil
}

// Delete 从面板删除节点（不卸载）。令牌立即作废，agent 下次连接会被拒绝。
func (s *Service) Delete(ctx context.Context, by Actor, id string) error {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		n, err := store.GetNode(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteNode(ctx, tx, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeDelete, n.Name, map[string]any{"node": id})
	})
	if err != nil {
		return notFound(err)
	}
	s.disconnect(id, "node deleted")
	s.mu.Lock()
	delete(s.live, id)
	delete(s.gone, id)
	delete(s.drops, id)
	s.mu.Unlock()
	return nil
}

// Uninstall 让节点卸载 agent 并从面板删除。节点离线时先标记，上线后执行；返回 true 表示已执行。
func (s *Service) Uninstall(ctx context.Context, by Actor, id string) (done bool, err error) {
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return false, notFound(err)
	}
	if !n.Enrolled {
		// 还没安装过：直接删除
		return true, s.Delete(ctx, by, id)
	}
	if err := s.audit(ctx, s.st.DB(), by, store.AuditNodeUninstall, n.Name, map[string]any{"node": id}); err != nil {
		return false, err
	}
	if !s.Online(id) {
		return false, store.SetNodePendingUninstall(ctx, s.st.DB(), id, true)
	}
	if err := s.uninstallNow(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) uninstallNow(ctx context.Context, id string) error {
	if err := s.Call(ctx, id, agentproto.MethodUninstall, agentproto.UninstallRequest{Purge: true}, nil, callTimeout); err != nil {
		return err
	}
	// agent 已经开始卸载：删除节点、作废令牌
	if err := store.DeleteNode(ctx, s.st.DB(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	s.disconnect(id, "uninstalled")
	s.mu.Lock()
	delete(s.live, id)
	delete(s.gone, id)
	delete(s.drops, id)
	s.mu.Unlock()
	return nil
}

// CancelUninstall 取消离线节点的待卸载标记。
func (s *Service) CancelUninstall(ctx context.Context, id string) error {
	return notFound(store.SetNodePendingUninstall(ctx, s.st.DB(), id, false))
}

// RestartFrps 重启节点上的 frps。
func (s *Service) RestartFrps(ctx context.Context, by Actor, id string) error {
	if err := s.Call(ctx, id, agentproto.MethodFrpsRestart, struct{}{}, nil, callTimeout); err != nil {
		return err
	}
	return s.audit(ctx, s.st.DB(), by, store.AuditNodeRestart, id, nil)
}

// Proxies 返回节点上的代理列表。
func (s *Service) Proxies(ctx context.Context, id string) ([]agentproto.ProxyInfo, error) {
	var res agentproto.ProxiesResult
	if err := s.Call(ctx, id, agentproto.MethodFrpsProxies, struct{}{}, &res, callTimeout); err != nil {
		return nil, err
	}
	return res.Proxies, nil
}

// Logs 返回节点最近的日志。
func (s *Service) Logs(ctx context.Context, id string, lines int) ([]string, error) {
	var res agentproto.LogsResult
	if err := s.Call(ctx, id, agentproto.MethodLogs, agentproto.LogsRequest{Lines: lines}, &res, callTimeout); err != nil {
		return nil, err
	}
	return res.Lines, nil
}

// ---- 任务 ----

// Task 是返回给管理后台的任务。
type Task struct {
	ID         string     `json:"id"`
	NodeID     string     `json:"nodeId"`
	Kind       string     `json:"kind"`
	Command    string     `json:"command,omitempty"`
	Status     string     `json:"status"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

func taskView(t store.NodeTask) Task {
	return Task{ID: t.ID, NodeID: t.NodeID, Kind: t.Kind, Command: t.Command, Status: t.Status, ExitCode: t.ExitCode,
		Output: t.Output, Error: t.Error, CreatedAt: time.Unix(t.CreatedAt, 0).UTC(), FinishedAt: unixTime(t.FinishedAt)}
}

// Exec 在一个或多个节点上执行命令，返回每个节点的任务 ID。命令在后台执行，用 Task 查询结果。
func (s *Service) Exec(ctx context.Context, by Actor, ids []string, command string, timeoutSec int) ([]Task, error) {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > 16<<10 {
		return nil, badRequest("command", "命令不能为空，最长 16KB")
	}
	if len(ids) == 0 || len(ids) > 200 {
		return nil, badRequest("nodes", "请选择 1–200 个节点")
	}
	if timeoutSec <= 0 {
		timeoutSec = agentproto.ExecDefaultTimeout
	}
	if timeoutSec > agentproto.ExecMaxTimeout {
		timeoutSec = agentproto.ExecMaxTimeout
	}
	now := s.now().Unix()
	var tasks []Task
	for _, id := range ids {
		n, err := store.GetNode(ctx, s.st.DB(), id)
		if err != nil {
			return nil, notFound(err)
		}
		t := store.NodeTask{ID: model.NewID(), NodeID: id, Kind: "exec", Command: command, CreatedBy: by.UserID, CreatedAt: now}
		if err := s.st.Tx(ctx, func(tx *sql.Tx) error {
			if err := store.InsertNodeTask(ctx, tx, t); err != nil {
				return err
			}
			return s.audit(ctx, tx, by, store.AuditNodeExec, n.Name, map[string]any{"node": id, "command": command, "task": t.ID})
		}); err != nil {
			return nil, err
		}
		t.Status = "running"
		tasks = append(tasks, taskView(t))
		go s.runExec(t.ID, id, command, timeoutSec)
	}
	_ = store.PruneNodeTasks(ctx, s.st.DB(), keepTasks)
	return tasks, nil
}

func (s *Service) runExec(taskID, nodeID, command string, timeoutSec int) {
	ctx := context.Background()
	var res agentproto.ExecResult
	err := s.Call(ctx, nodeID, agentproto.MethodExec, agentproto.ExecRequest{Command: command, TimeoutSec: timeoutSec},
		&res, time.Duration(timeoutSec)*time.Second+30*time.Second)
	now := s.now().Unix()
	if err != nil {
		e, _ := apierr.As(err)
		msg := err.Error()
		if e != nil {
			msg = e.Message
		}
		_ = store.FinishNodeTask(ctx, s.st.DB(), taskID, "failed", nil, "", msg, now)
		return
	}
	status := "done"
	if res.ExitCode != 0 {
		status = "failed"
	}
	if res.Truncated {
		res.Output += "\n…（输出超过 1MB，后面的内容已截断）"
	}
	code := res.ExitCode
	_ = store.FinishNodeTask(ctx, s.st.DB(), taskID, status, &code, res.Output, res.Error, now)
}

// Task 返回一个任务（含输出）。
func (s *Service) Task(ctx context.Context, id string) (Task, error) {
	t, err := store.GetNodeTask(ctx, s.st.DB(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Task{}, apierr.Newf(apierr.NotFound, "任务不存在")
		}
		return Task{}, err
	}
	return taskView(t), nil
}

// Tasks 返回节点最近的任务（不含输出）。
func (s *Service) Tasks(ctx context.Context, nodeID string) ([]Task, error) {
	ts, err := store.ListNodeTasks(ctx, s.st.DB(), nodeID, 50)
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(ts))
	for _, t := range ts {
		out = append(out, taskView(t))
	}
	return out, nil
}

// Upgrade 让节点升级到最新版本（GitHub Release 或本地目录）。结果在 agent 重启后上报，任务一直是 running 直到那时。
func (s *Service) Upgrade(ctx context.Context, by Actor, id string) (Task, error) {
	n, err := store.GetNode(ctx, s.st.DB(), id)
	if err != nil {
		return Task{}, notFound(err)
	}
	v := s.view(n, false)
	if v.Status != StatusOnline || v.Info == nil {
		return Task{}, apierr.New(apierr.NodeOffline)
	}
	if s.src == nil {
		return Task{}, apierr.Newf(apierr.NodeFailed, "面板没有配置 agent 来源（agent_repo / agent_dist_dir）")
	}
	lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	b, err := s.src.Latest(lctx)
	cancel()
	if err == nil && b == nil {
		err = errors.New("还没有查到")
	}
	if err != nil {
		return Task{}, apierr.Newf(apierr.NodeFailed, "获取 agent 最新版本失败：%v", err)
	}
	if v.Info.OS != "linux" || !b.Has(v.Info.Arch) {
		return Task{}, apierr.Newf(apierr.NodeFailed, "%s 没有 %s/%s 的 agent", b.Version, v.Info.OS, v.Info.Arch)
	}
	if !newer(b.Version, v.Info.AgentVersion) {
		return Task{}, apierr.Newf(apierr.ValidationFailed, "已经是最新版本（当前 %s，最新 %s）", v.Info.AgentVersion, b.Version)
	}
	if running, err := store.HasRunningUpgrade(ctx, s.st.DB(), id); err != nil {
		return Task{}, err
	} else if running {
		return Task{}, apierr.Newf(apierr.ValidationFailed, "这个节点正在升级")
	}
	f := b.Files[v.Info.Arch]
	t := store.NodeTask{ID: model.NewID(), NodeID: id, Kind: "upgrade",
		Command: v.Info.AgentVersion + " → " + b.Version, CreatedBy: by.UserID, CreatedAt: s.now().Unix()}
	if err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.InsertNodeTask(ctx, tx, t); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeUpgrade, n.Name, map[string]any{"node": id, "from": v.Info.AgentVersion, "to": b.Version, "source": b.Source, "mirror": n.DownloadMirror})
	}); err != nil {
		return Task{}, err
	}
	// agent 下载、校验、预检都通过后才响应，可能要几分钟，所以在后台等。
	// URL 是 GitHub 的完整地址；本地目录时是相对路径，agent 用自己配置的面板地址补全
	// 节点设置了下载代理时经代理下载；校验和来自面板，代理改不了内容
	req := agentproto.UpgradeRequest{Version: b.Version, URL: WithMirror(f.URL, n.DownloadMirror), SHA256: f.SHA256}
	go func() {
		bg := context.Background()
		if err := s.Call(bg, id, agentproto.MethodUpgrade, req, nil, 11*time.Minute); err != nil {
			msg := err.Error()
			if e, ok := apierr.As(err); ok {
				msg = e.Message
			}
			_ = store.FinishNodeTask(bg, s.st.DB(), t.ID, "failed", nil, "", msg, s.now().Unix())
		}
	}()
	t.Status = "running"
	return taskView(t), nil
}

// ---- 注册和认证 ----

// Enroll 用安装码换取长期令牌。安装码只能用一次。
func (s *Service) Enroll(ctx context.Context, code, hostname, ip string) (agentproto.EnrollResponse, error) {
	code = normalizeEnrollCode(code)
	if len(code) != len("MFN-XXXX-XXXX-XXXX") || !strings.HasPrefix(code, "MFN-") {
		return agentproto.EnrollResponse{}, apierr.Newf(apierr.Forbidden, "安装码不正确")
	}
	token := tokenPrefix + randomB64(32)
	var out agentproto.EnrollResponse
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		n, err := store.GetNodeByEnrollHash(ctx, tx, hashSecret("enroll", code))
		if errors.Is(err, store.ErrNotFound) {
			return apierr.Newf(apierr.Forbidden, "安装码不正确或已经用过")
		}
		if err != nil {
			return err
		}
		now := s.now().Unix()
		if n.EnrollExpiresAt == nil || *n.EnrollExpiresAt < now {
			return apierr.Newf(apierr.Forbidden, "安装码已过期，请在面板上重新生成")
		}
		if err := store.SetNodeEnrolled(ctx, tx, n.ID, hashSecret("token", token), now); err != nil {
			return err
		}
		out = agentproto.EnrollResponse{NodeID: n.ID, Name: n.Name, Token: token}
		return store.InsertAudit(ctx, tx, store.AuditEntry{At: now, Action: store.AuditNodeEnroll, Target: n.Name, IP: ip,
			Detail: map[string]any{"node": n.ID, "hostname": hostname}})
	})
	return out, err
}

// Authenticate 校验 agent 的长期令牌。
func (s *Service) Authenticate(ctx context.Context, token string) (store.Node, error) {
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok || len(rest) != base64.RawURLEncoding.EncodedLen(32) {
		return store.Node{}, apierr.New(apierr.Unauthorized)
	}
	// 令牌是 256 位随机数，库里只存哈希，按哈希查找不会泄露令牌内容
	n, err := store.GetNodeByTokenHash(ctx, s.st.DB(), hashSecret("token", token))
	if errors.Is(err, store.ErrNotFound) {
		return store.Node{}, apierr.New(apierr.Unauthorized)
	}
	return n, err
}
