package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

func userInfo(u store.User) api.UserInfo {
	return api.UserInfo{ID: u.ID, Nickname: u.Nickname, Role: u.Role, CreatedAt: time.Unix(u.CreatedAt, 0).UTC()}
}

// clientMeta 从请求里取出 IP 和 X-Install-Id。
func (s *Server) clientMeta(r *http.Request, deviceName string) (auth.ClientMeta, error) {
	installID := strings.TrimSpace(r.Header.Get(api.HeaderInstallID))
	if installID != "" {
		if err := validate.ID(api.HeaderInstallID, installID); err != nil {
			var es validate.Errors
			errors.As(err, &es)
			return auth.ClientMeta{}, apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, es)
		}
	}
	return auth.ClientMeta{IP: infoFrom(r.Context()).IP, InstallID: installID, DeviceName: deviceName, DeviceID: deviceID(r)}, nil
}

// deviceID 读取客户端的设备指纹（32 位十六进制），格式不对时当作没有。
func deviceID(r *http.Request) string {
	v := strings.ToLower(strings.TrimSpace(r.Header.Get(api.HeaderDeviceID)))
	if len(v) != 32 || strings.Trim(v, "0123456789abcdef") != "" {
		return ""
	}
	return v
}

// GET /healthz
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Ping(r.Context()); err != nil {
		s.log.ErrorContext(r.Context(), "healthz: database unavailable", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, api.HealthResponse{Status: "unavailable", Version: s.version})
		return
	}
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: "ok", Version: s.version})
}

// GET /api/v1/client/config
func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.publicClientConfig())
}

// publicClientConfig 是不需要登录就能拿的客户端配置：不带图源（图源只给登录的用户）。
func (s *Server) publicClientConfig() api.ClientConfigResponse {
	cc := s.clientCfg().ClientConfig()
	cc.ImageSources = []api.ImageSource{}
	return cc
}

// GET /api/v1/client/images
func (s *Server) handleClientImages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.ClientImagesResponse{ImageSources: s.clientCfg().ClientConfig().ImageSources})
}

// POST /api/v1/auth/register
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req api.RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	meta, err := s.clientMeta(r, "")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	u, err := s.auth.Register(r.Context(), req.Nickname, req.Password, req.InviteCode, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	infoFrom(r.Context()).UserID = u.ID
	s.nodes.AccessChanged() // 新用户可能能用官方节点
	writeJSON(w, http.StatusCreated, api.RegisterResponse{User: userInfo(u)})
}

// POST /api/v1/auth/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	meta, err := s.clientMeta(r, req.DeviceName)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.auth.Login(r.Context(), req.Nickname, req.Password, meta)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	infoFrom(r.Context()).UserID = res.User.ID
	s.sessionsChanged() // 单设备模式下旧会话被挤下线，它在官方节点上的票据和连接要失效
	writeJSON(w, http.StatusOK, api.LoginResponse{
		Token:     res.Token,
		ExpiresAt: time.Unix(res.Session.ExpiresAt, 0).UTC(),
		User:      userInfo(res.User),
	})
}

// POST /api/v1/auth/logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), principalFrom(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.sessionsChanged() // 吊销的会话在官方节点上失效
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/change-password
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req api.ChangePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	meta, err := s.clientMeta(r, "")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.auth.ChangePassword(r.Context(), principalFrom(r), req.OldPassword, req.NewPassword, meta); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.sessionsChanged() // 其他设备的会话被吊销
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/reset-password
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req api.ResetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	meta, err := s.clientMeta(r, "")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.auth.ResetPassword(r.Context(), req.Nickname, req.Code, req.NewPassword, meta); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.sessionsChanged() // 所有会话被吊销
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/me
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.MeResponse{User: userInfo(principalFrom(r).User)})
}

// GET /api/v1/state：带 If-None-Match 且 stateVersion 没变时返回 304。
// 这个请求同时充当会话检查：会话失效时认证中间件已经返回 401。
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if inm := r.Header.Get(api.HeaderIfNoneMatch); inm != "" && etagMatch(inm, api.StateETag(p.User.StateVersion)) {
		w.Header().Set(api.HeaderETag, api.StateETag(p.User.StateVersion))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	st, err := s.profiles.State(r.Context(), p.User.ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set(api.HeaderETag, api.StateETag(st.User.StateVersion))
	writeJSON(w, http.StatusOK, api.StateResponse{
		StateVersion:  st.User.StateVersion,
		User:          userInfo(st.User),
		Profiles:      st.Profiles,
		Subscriptions: st.Subscriptions,
	})
}

// etagMatch 按 RFC 9110 的弱比较处理 If-None-Match（可能是逗号分隔的列表或 *）。
func etagMatch(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		if p == "*" || strings.TrimPrefix(p, "W/") == etag {
			return true
		}
	}
	return false
}

// POST /api/v1/profiles
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req api.CreateProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	p, err := s.profiles.Create(r.Context(), principalFrom(r).User.ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", api.ProfilePath(p.ID))
	writeJSON(w, http.StatusCreated, p)
}

// GET /api/v1/profiles/{id}
func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.profiles.Get(r.Context(), principalFrom(r).User.ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// PUT /api/v1/profiles/{id}
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateProfileRequest
	if err := decodeJSONRequired(r, &req, "baseRevision", "name", "nodes", "proxies", "visitors"); err != nil {
		s.writeError(w, r, err)
		return
	}
	p, err := s.profiles.Update(r.Context(), principalFrom(r).User.ID, r.PathValue("id"), req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// DELETE /api/v1/profiles/{id}
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.profiles.Delete(r.Context(), principalFrom(r).User.ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
