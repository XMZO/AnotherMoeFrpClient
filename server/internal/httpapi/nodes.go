package httpapi

// 节点管理（docs/node-agent.md）：
//   - agent 接口：/api/v1/agent/enroll、/api/v1/agent/ws，以及安装脚本和程序下载 /agent/*；
//   - 管理接口：/api/v1/admin/nodes/*，执行命令、卸载、删除前要在 15 分钟内重新输入过密码。

import (
	"context"
	"fmt"
	"net/http"
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
	"moefrp/shared/apierr"
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
	mux.Handle("PUT "+p+"/nodes/{id}/config", authed(s.handleNodeConfig))
	mux.Handle("POST "+p+"/nodes/{id}/reset-token", elevated(s.handleNodeResetToken))
	mux.Handle("POST "+p+"/nodes/{id}/uninstall", elevated(s.handleNodeUninstall))
	mux.Handle("POST "+p+"/nodes/{id}/cancel-uninstall", authed(s.handleNodeCancelUninstall))
	mux.Handle("POST "+p+"/nodes/{id}/restart", authed(s.handleNodeRestart))
	mux.Handle("POST "+p+"/nodes/{id}/upgrade", authed(s.handleNodeUpgrade))
	mux.Handle("GET "+p+"/nodes/{id}/proxies", authed(s.handleNodeProxies))
	mux.Handle("GET "+p+"/nodes/{id}/logs", authed(s.handleNodeLogs))
	mux.Handle("GET "+p+"/nodes/{id}/tasks", authed(s.handleNodeTasks))
	mux.Handle("GET "+p+"/tasks/{id}", authed(s.handleTask))
}

// ---- 面板地址 ----

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-\[\]:]+$`)

// publicURL 是写进安装命令的面板地址：优先用配置的 public_url，否则按这次请求的地址生成。
func (s *Server) publicURL(r *http.Request) (string, bool) {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/"), true
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

func installCommand(base, code string) string {
	return fmt.Sprintf("curl -fsSL %s%s | sudo sh -s -- %s", base, agentproto.RouteInstallScript, code)
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
	if src := s.nodes.Source(); src == nil {
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
	_, _ = w.Write([]byte(nodes.InstallScript(base, build, unavailable)))
}

// agentStatus 是列表页和新建节点时显示的 agent 来源和版本（不等网络）。
func (s *Server) agentStatus() map[string]any {
	src := s.nodes.Source()
	if src == nil {
		return map[string]any{"error": "面板没有配置 agent 来源，不能安装和升级节点"}
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
	Name string `json:"name"`
}

type updateNodeRequest struct {
	Name       string `json:"name"`
	PublicAddr string `json:"publicAddr"`
}

type nodeConfigRequest struct {
	Config string `json:"config"`
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
	resp := map[string]any{"nodes": list, "agent": s.agentStatus()}
	if base, ok := s.publicURL(r); ok {
		resp["publicUrl"] = base
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createdResponse(r *http.Request, c nodes.Created) map[string]any {
	resp := map[string]any{"node": c.Node, "enrollCode": c.EnrollCode, "expiresAt": c.ExpiresAt}
	if base, ok := s.publicURL(r); ok {
		resp["installCommand"] = installCommand(base, c.EnrollCode)
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
	c, err := s.nodes.Create(r.Context(), nodeActor(r), req.Name)
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
	if err := s.nodes.Update(r.Context(), nodeActor(r), r.PathValue("id"), req.Name, req.PublicAddr); err != nil {
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

func (s *Server) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
	var req nodeConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	out, err := s.nodes.SetConfig(r.Context(), nodeActor(r), r.PathValue("id"), req.Config)
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
