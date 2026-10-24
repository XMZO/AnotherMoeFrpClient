package httpapi

// 节点管理（docs/node-agent.md）：
//   - agent 接口：/api/v1/agent/enroll、/api/v1/agent/ws，以及安装脚本和程序下载 /agent/*；
//   - 管理接口：/api/v1/admin/nodes/*，执行命令、卸载、删除前要在 15 分钟内重新输入过密码。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/nodes"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/frpsset"
	"moefrp/shared/quota"
)

const elevateTTL = 15 * time.Minute

// elevation 记录哪些管理后台会话最近重新输入过密码。只放内存，服务重启后要重新输入。
type elevation struct {
	mu    sync.Mutex
	until map[string]time.Time // 会话 ID → 到期时间
}

func (e *elevation) set(sessionID string, until time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.until == nil {
		e.until = map[string]time.Time{}
	}
	e.until[sessionID] = until
}

func (e *elevation) get(sessionID string, now time.Time) (time.Time, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.until[sessionID]
	if ok && now.After(t) {
		delete(e.until, sessionID)
		return time.Time{}, false
	}
	return t, ok
}

func (s *Server) requireElevated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.elevated.get(principalFrom(r).Session.ID, s.now()); !ok {
			s.writeError(w, r, apierr.New(apierr.ReauthRequired))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func nodeMeta(r *http.Request) auth.ClientMeta {
	return auth.ClientMeta{IP: infoFrom(r.Context()).IP, DeviceName: "管理后台"}
}

func nodeActor(r *http.Request) nodes.Actor {
	return nodes.Actor{UserID: principalFrom(r).User.ID, IP: infoFrom(r.Context()).IP}
}

func (s *Server) nodeRoutes(mux *http.ServeMux) {
	// agent
	mux.Handle("POST "+agentproto.RouteEnroll, chain(http.HandlerFunc(s.handleAgentEnroll), s.limit(s.limReset, byIP)))
	mux.HandleFunc("GET "+agentproto.RouteWS, s.handleAgentWS)
	mux.HandleFunc("GET "+agentproto.RouteInstallScript, s.handleInstallScript)
	mux.HandleFunc("GET "+agentproto.RouteDownload+"{name}", s.handleAgentDownload)

	// 管理后台
	authed := func(h http.HandlerFunc) http.Handler {
		return chain(h, s.adminCSRF, s.requireAdmin, s.limit(s.limUser, byUser))
	}
	elevated := func(h http.HandlerFunc) http.Handler {
		return chain(h, s.adminCSRF, s.requireAdmin, s.limit(s.limUser, byUser), s.requireElevated)
	}
	p := adminAPIPrefix
	mux.Handle("GET "+p+"/elevate", authed(s.handleElevateState))
	mux.Handle("POST "+p+"/elevate", chain(http.HandlerFunc(s.handleElevate), s.adminCSRF, s.requireAdmin, s.limit(s.limLogin, byIP)))
	mux.Handle("GET "+p+"/nodes", authed(s.handleNodes))
	mux.Handle("POST "+p+"/nodes", authed(s.handleCreateNode))
	mux.Handle("POST "+p+"/nodes/exec", elevated(s.handleNodeExec))
	mux.Handle("GET "+p+"/nodes/{id}", authed(s.handleNode))
	mux.Handle("PATCH "+p+"/nodes/{id}", authed(s.handleUpdateNode))
	mux.Handle("DELETE "+p+"/nodes/{id}", elevated(s.handleDeleteNode))
	mux.Handle("PUT "+p+"/nodes/{id}/settings", authed(s.handleNodeSettings))
	mux.Handle("GET "+p+"/frps-schema", authed(s.handleFrpsSchema))
	mux.Handle("GET "+p+"/node-secret", authed(s.handleNodeSecret))
	mux.Handle("POST "+p+"/node-secret/rotate", elevated(s.handleRotateNodeSecret))
	mux.Handle("POST "+p+"/nodes/{id}/reset-token", elevated(s.handleNodeResetToken))
	mux.Handle("POST "+p+"/nodes/{id}/uninstall", elevated(s.handleNodeUninstall))
	mux.Handle("POST "+p+"/nodes/{id}/cancel-uninstall", authed(s.handleNodeCancelUninstall))
	mux.Handle("POST "+p+"/nodes/{id}/restart", authed(s.handleNodeRestart))
	mux.Handle("POST "+p+"/nodes/{id}/upgrade", authed(s.handleNodeUpgrade))
	mux.Handle("GET "+p+"/nodes/{id}/proxies", authed(s.handleNodeProxies))
	mux.Handle("GET "+p+"/nodes/{id}/logs", authed(s.handleNodeLogs))
	mux.Handle("GET "+p+"/nodes/{id}/tasks", authed(s.handleNodeTasks))
	mux.Handle("GET "+p+"/tasks/{id}", authed(s.handleTask))
	mux.Handle("PUT "+p+"/nodes/{id}/official", authed(s.handleNodeOfficial))
	mux.Handle("PUT "+p+"/nodes/{id}/access", authed(s.handleNodeAccess))
	mux.Handle("GET "+p+"/quotas", authed(s.handleGetQuotas))
	mux.Handle("PUT "+p+"/quotas", authed(s.handleSetQuotas))
	mux.Handle("GET "+p+"/users/{nickname}/nodes", authed(s.handleGetUserNodes))
	mux.Handle("PUT "+p+"/users/{nickname}/nodes", authed(s.handleSetUserNodes))
	mux.Handle("POST "+p+"/users/{nickname}/reset-node-credential", authed(s.handleResetNodeCredential))
}

// ---- 面板地址 ----

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-\[\]:]+$`)

// publicURL 是写进安装命令的面板地址：优先用配置的 public_url，否则按这次请求的地址生成。
func (s *Server) publicURL(r *http.Request) (string, bool) {
	if u := s.nodeCfg().PublicURL; u != "" {
		return strings.TrimRight(u, "/"), true
	}
	if !hostRe.MatchString(r.Host) {
		return "", false
	}
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host, true
}

// installCommand 生成安装命令。节点设置了下载代理时，安装脚本的地址带上 mirror 参数，
// 脚本里的下载地址就会加上代理前缀。地址用单引号包起来（里面有 ? 和 &）。
func installCommand(base, code, mirror string) string {
	u := base + agentproto.RouteInstallScript
	if mirror != "" {
		u += "?mirror=" + url.QueryEscape(mirror)
	}
	return fmt.Sprintf("curl -fsSL '%s' | sudo sh -s -- %s", u, code)
}

// ---- agent 接口 ----

func (s *Server) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	var req agentproto.EnrollRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.nodes.Enroll(r.Context(), req.Code, req.Hostname, infoFrom(r.Context()).IP)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		s.writeError(w, r, apierr.New(apierr.Unauthorized))
		return
	}
	node, err := s.nodes.Authenticate(r.Context(), token)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	// 长连接不受 http.Server 的读写超时限制，存活由 nodes.Serve 自己判断
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{agentproto.Subprotocol}})
	if err != nil {
		return // Accept 已经写了错误响应
	}
	if ws.Subprotocol() != agentproto.Subprotocol {
		ws.Close(websocket.StatusPolicyViolation, "unsupported protocol version")
		return
	}
	s.nodes.Serve(context.WithoutCancel(r.Context()), ws, node, infoFrom(r.Context()).IP)
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	base, ok := s.publicURL(r)
	if !ok {
		s.writeError(w, r, apierr.Newf(apierr.ValidationFailed, "无法确定面板地址，请在配置里设置 public_url"))
		return
	}
	// 脚本里写好下载地址和校验和：查一次最新版本（GitHub 有缓存，一般不用等）。
	// 查不到时仍然返回脚本，卸载照常可用，安装时报出原因
	var build *nodes.Build
	unavailable := ""
	mirror, err := nodes.NormalizeMirror(r.URL.Query().Get("mirror"))
	if err != nil {
		unavailable = "安装命令里的下载代理不正确：" + err.Error()
	} else if src := s.nodes.Source(); src == nil {
		unavailable = "面板没有配置 agent 来源（agent_repo / agent_dist_dir）"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		b, err := src.Latest(ctx)
		cancel()
		if err != nil {
			unavailable = "面板获取 agent 版本失败：" + err.Error()
		}
		build = b
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(nodes.InstallScript(base, build, unavailable, mirror)))
}

// agentStatus 是列表页和新建节点时显示的 agent 来源和版本（不等网络）。
func (s *Server) agentStatus() map[string]any {
	src := s.nodes.Source()
	if src == nil {
		return map[string]any{"error": "面板没有配置 agent 来源（agent_repo / agent_dist_dir）"}
	}
	b, err := src.Cached()
	out := map[string]any{"source": src.Kind()}
	if g, ok := src.(*nodes.GitHub); ok {
		out["repo"] = g.Repo
	}
	if b != nil {
		out["version"], out["arches"] = b.Version, b.ArchList()
		if b.Tag != "" {
			out["tag"] = b.Tag
		}
	} else if err != nil {
		out["error"] = err.Error()
	}
	return out
}

func (s *Server) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	name, wantSum := strings.CutSuffix(name, ".sha256")
	f, ok := s.nodes.Dist().ByName(name)
	if !ok {
		s.writeError(w, r, apierr.Newf(apierr.NotFound, "面板不提供这个 agent 的下载（agent 从 GitHub Release 下载，或者本地目录里没有这个架构）"))
		return
	}
	if wantSum {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s  moefrp-agent\n", f.SHA256)
		return
	}
	file, err := os.Open(f.Path)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer file.Close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="moefrp-agent"`)
	w.Header().Set("X-Agent-Version", s.nodes.Dist().Version())
	http.ServeContent(w, r, "", time.Time{}, file)
}

// ---- 二次验证 ----

type elevateRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleElevateState(w http.ResponseWriter, r *http.Request) {
	until, ok := s.elevated.get(principalFrom(r).Session.ID, s.now())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"elevated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"elevated": true, "until": until.UTC()})
}

func (s *Server) handleElevate(w http.ResponseWriter, r *http.Request) {
	var req elevateRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	p := principalFrom(r)
	if err := s.auth.ConfirmPassword(r.Context(), p, req.Password, nodeMeta(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	until := s.now().Add(elevateTTL)
	s.elevated.set(p.Session.ID, until)
	writeJSON(w, http.StatusOK, map[string]any{"elevated": true, "until": until.UTC()})
}

// ---- 节点 ----

type createNodeRequest struct {
	Name           string `json:"name"`
	DownloadMirror string `json:"downloadMirror"`
}

type updateNodeRequest struct {
	Name           string  `json:"name"`
	PublicAddr     string  `json:"publicAddr"`
	DownloadMirror *string `json:"downloadMirror,omitempty"` // 不传表示不改
}

func mirrorsOf(list []nodes.View) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.DownloadMirror)
	}
	return out
}

type nodeExecRequest struct {
	Nodes      []string `json:"nodes"`
	Command    string   `json:"command"`
	TimeoutSec int      `json:"timeoutSec"`
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.List(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	resp := map[string]any{"nodes": list, "agent": s.agentStatus(), "defaultMirror": s.nodeCfg().AgentMirror}
	// 用过的下载代理，添加节点时可以直接选
	seen := map[string]bool{}
	mirrors := []string{}
	for _, m := range append([]string{s.nodeCfg().AgentMirror}, mirrorsOf(list)...) {
		if m != "" && !seen[m] {
			seen[m] = true
			mirrors = append(mirrors, m)
		}
	}
	resp["mirrors"] = mirrors
	if base, ok := s.publicURL(r); ok {
		resp["publicUrl"] = base
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createdResponse(r *http.Request, c nodes.Created) map[string]any {
	resp := map[string]any{"node": c.Node, "enrollCode": c.EnrollCode, "expiresAt": c.ExpiresAt}
	if base, ok := s.publicURL(r); ok {
		resp["installCommand"] = installCommand(base, c.EnrollCode, c.Node.DownloadMirror)
	}
	if st := s.agentStatus(); st["error"] != nil && st["version"] == nil {
		resp["warning"] = fmt.Sprintf("暂时拿不到 agent 程序：%v。安装命令现在执行会失败", st["error"])
	}
	return resp
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.nodes.Create(r.Context(), nodeActor(r), req.Name, req.DownloadMirror)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.createdResponse(r, c))
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	v, err := s.nodes.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	var req updateNodeRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.nodes.Update(r.Context(), nodeActor(r), r.PathValue("id"), req.Name, req.PublicAddr, req.DownloadMirror); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	if err := s.nodes.Delete(r.Context(), nodeActor(r), r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/admin/nodes/{id}/settings：保存节点的 frps 配置（frpsset.Settings），在线时立即下发。
func (s *Server) handleNodeSettings(w http.ResponseWriter, r *http.Request) {
	var req frpsset.Settings
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	out, err := s.nodes.SetSettings(r.Context(), nodeActor(r), r.PathValue("id"), req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/admin/frps-schema：frps 配置表单的描述（分组、字段、默认值）。
func (s *Server) handleFrpsSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nodes.SettingsSchema())
}

// GET /api/v1/admin/node-secret：面板主密钥的指纹和上次轮换时间（不返回密钥）。
func (s *Server) handleNodeSecret(w http.ResponseWriter, r *http.Request) {
	out, err := s.nodes.Secret(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/admin/node-secret/rotate：轮换面板主密钥（需要重新输入密码）。
func (s *Server) handleRotateNodeSecret(w http.ResponseWriter, r *http.Request) {
	out, err := s.nodes.RotateSecret(r.Context(), nodeActor(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleNodeResetToken(w http.ResponseWriter, r *http.Request) {
	c, err := s.nodes.ResetToken(r.Context(), nodeActor(r), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.createdResponse(r, c))
}

func (s *Server) handleNodeUninstall(w http.ResponseWriter, r *http.Request) {
	done, err := s.nodes.Uninstall(r.Context(), nodeActor(r), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"done": done})
}

func (s *Server) handleNodeCancelUninstall(w http.ResponseWriter, r *http.Request) {
	if err := s.nodes.CancelUninstall(r.Context(), r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNodeRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.nodes.RestartFrps(r.Context(), nodeActor(r), r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNodeUpgrade(w http.ResponseWriter, r *http.Request) {
	t, err := s.nodes.Upgrade(r.Context(), nodeActor(r), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleNodeProxies(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.Proxies(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxies": list})
}

func (s *Server) handleNodeLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	lines, err := s.nodes.Logs(r.Context(), r.PathValue("id"), n)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) handleNodeTasks(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.Tasks(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": list})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.nodes.Task(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleNodeExec(w http.ResponseWriter, r *http.Request) {
	var req nodeExecRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	tasks, err := s.nodes.Exec(r.Context(), nodeActor(r), req.Nodes, req.Command, req.TimeoutSec)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"tasks": tasks})
}

// ---- 官方节点 ----

type nodeOfficialRequest struct {
	Official    bool     `json:"official"`
	Roles       []string `json:"roles"`
	Description string   `json:"description"`
}

type userNodesRequest struct {
	Override *quota.Override  `json:"override"`
	Grants   map[string]*bool `json:"grants"` // 节点 ID → true 允许 / false 禁止 / null 按角色
}

func (s *Server) handleNodeOfficial(w http.ResponseWriter, r *http.Request) {
	var req nodeOfficialRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.nodes.SetOfficial(r.Context(), nodeActor(r), r.PathValue("id"), req.Roles, req.Description); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetQuotas(w http.ResponseWriter, r *http.Request) {
	q, err := s.nodes.RoleQuotas(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleSetQuotas(w http.ResponseWriter, r *http.Request) {
	var q quota.Roles
	if err := decodeJSON(r, &q); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.nodes.SetRoleQuotas(r.Context(), nodeActor(r), q); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetUserNodes(w http.ResponseWriter, r *http.Request) {
	out, err := s.nodes.GetUserNodes(r.Context(), r.PathValue("nickname"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetUserNodes(w http.ResponseWriter, r *http.Request) {
	var req userNodesRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.nodes.SetUserNodes(r.Context(), nodeActor(r), r.PathValue("nickname"), req.Override, req.Grants); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetNodeCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.nodes.ResetUserCredential(r.Context(), nodeActor(r), r.PathValue("nickname")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/official-nodes：当前用户能用的官方节点。内容不变时返回 304（客户端每分钟同步一次）。
func (s *Server) handleOfficialNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.OfficialFor(r.Context(), principalFrom(r).User.ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	resp := api.OfficialNodesResponse{Nodes: list}
	b, _ := json.Marshal(resp)
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/v1/official-nodes/{id}/tickets：给当前客户端会话签发登录票据（docs/node-agent.md 第 12 节）。
func (s *Server) handleNodeTickets(w http.ResponseWriter, r *http.Request) {
	var req api.NodeTicketsRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	p := principalFrom(r)
	// 设备绑定：请求带的设备指纹必须和这个会话登录时的一致，登录凭据被拷到别的机器上就领不到票据
	dev := deviceID(r)
	switch {
	case p.Session.DeviceID == "":
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "这次登录没有记下设备指纹（客户端版本太旧），请升级客户端后重新登录"))
		return
	case dev != p.Session.DeviceID:
		s.writeError(w, r, apierr.Newf(apierr.Forbidden, "这台设备和登录时的不是同一台，请在这台设备上重新登录"))
		return
	}
	list, err := s.nodes.IssueTickets(r.Context(), p.User.ID, p.Session.ID, dev, r.PathValue("id"), req.Count)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.NodeTicketsResponse{Tickets: list})
}

// PUT /api/v1/admin/nodes/{id}/access：用户访问地址（域名；IPv4、IPv6 留空自动检测，"-" 不显示）。
func (s *Server) handleNodeAccess(w http.ResponseWriter, r *http.Request) {
	var req nodes.Access
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.nodes.SetAccess(r.Context(), nodeActor(r), r.PathValue("id"), req); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
