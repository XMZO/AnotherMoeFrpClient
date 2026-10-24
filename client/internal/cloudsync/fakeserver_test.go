package cloudsync

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

// fakeServer 按 docs/phase1.md 描述的服务端行为实现同步用到的接口（单个用户）：
// ETag/304、baseRevision 冲突（409 带 details.current）、单设备模式、426、会话失效错误码。
type fakeServer struct {
	*httptest.Server
	down atomic.Bool // true 时直接断开连接，模拟断网

	mu           sync.Mutex
	user         api.UserInfo
	password     string
	stateVersion int64
	profiles     map[string]model.Profile
	order        []string
	tokens       map[string]apierr.Code // token → "" 有效，否则是失效原因
	seq          int
	outdated     bool
	headers      []http.Header // 收到的请求头
	puts         int

	subs         []api.SubscriptionSummary
	runtimes     map[string]api.SubscriptionRuntime
	runtimeCalls int

	official      []api.OfficialNode // nil 表示服务端没有这个接口（旧版本，返回 404）
	officialCalls int

	images      []api.ImageSource // nil 表示服务端没有这个接口（旧版本，返回 404）
	imagesCalls int
}

func newFakeServer() *fakeServer {
	f := &fakeServer{
		user:     api.UserInfo{ID: "u1", Nickname: "alice", Role: "user"},
		password: "correct-horse",
		profiles: map[string]model.Profile{},
		tokens:   map[string]apierr.Code{},
		runtimes: map[string]api.SubscriptionRuntime{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func writeErr(w http.ResponseWriter, e *apierr.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status())
	_ = json.NewEncoder(w).Encode(apierr.Response{Error: e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeServer) bump() { f.stateVersion++ }

// revokeAll 吊销全部会话（模拟在别处登录被挤下线等）。
func (f *fakeServer) revokeAll(reason apierr.Code) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for t := range f.tokens {
		f.tokens[t] = reason
	}
}

func (f *fakeServer) setOutdated(v bool) {
	f.mu.Lock()
	f.outdated = v
	f.mu.Unlock()
}

func (f *fakeServer) profile(id string) (model.Profile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	return p, ok
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.profiles)
}

// editCloud 模拟另一台设备修改了配置。
func (f *fakeServer) editCloud(id string, fn func(*model.Profile)) model.Profile {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[id]
	fn(&p)
	p.Revision++
	p.UpdatedAt = time.Now().UTC()
	f.profiles[id] = p
	f.bump()
	return p
}

func (f *fakeServer) deleteCloud(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.profiles, id)
	f.bump()
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	if f.down.Load() {
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
				return
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headers = append(f.headers, r.Header.Clone())
	if f.outdated {
		writeErr(w, apierr.New(apierr.ClientOutdated).With(apierr.DetailLatestVersion, "9.0.0").With(apierr.DetailDownloadURL, "https://example.com/new.exe"))
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == api.RouteLogin:
		var req api.LoginRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Nickname != f.user.Nickname || req.Password != f.password {
			writeErr(w, apierr.New(apierr.InvalidCredentials))
			return
		}
		for t := range f.tokens { // 单设备模式
			if f.tokens[t] == "" {
				f.tokens[t] = apierr.SessionReplaced
			}
		}
		f.seq++
		tok := fmt.Sprintf("mfs_tok%d", f.seq)
		f.tokens[tok] = ""
		writeJSON(w, 200, api.LoginResponse{Token: tok, ExpiresAt: time.Now().Add(time.Hour), User: f.user})
		return
	case r.Method == http.MethodPost && r.URL.Path == api.RouteRegister:
		writeJSON(w, 201, api.RegisterResponse{User: f.user})
		return
	}

	tok := strings.TrimPrefix(r.Header.Get(api.HeaderAuthorization), api.BearerPrefix)
	reason, ok := f.tokens[tok]
	if !ok {
		writeErr(w, apierr.New(apierr.Unauthorized))
		return
	}
	if reason != "" {
		writeErr(w, apierr.New(reason))
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == api.RouteClientImages:
		f.imagesCalls++
		if f.images == nil {
			writeErr(w, apierr.New(apierr.NotFound))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ClientImagesResponse{ImageSources: f.images})
	case r.Method == http.MethodGet && r.URL.Path == api.RouteOfficialNodes:
		if f.official == nil {
			writeErr(w, apierr.New(apierr.NotFound))
			return
		}
		f.officialCalls++
		b, _ := json.Marshal(api.OfficialNodesResponse{Nodes: f.official})
		etag := fmt.Sprintf(`"%x"`, sha256.Sum256(b))
		w.Header().Set("ETag", etag)
		if r.Header.Get(api.HeaderIfNoneMatch) == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	case r.Method == http.MethodGet && r.URL.Path == api.RouteMe:
		writeJSON(w, 200, api.MeResponse{User: f.user})
	case r.Method == http.MethodPost && r.URL.Path == api.RouteLogout:
		f.tokens[tok] = apierr.Unauthorized
		w.WriteHeader(204)
	case r.Method == http.MethodPost && r.URL.Path == api.RouteChangePassword:
		var req api.ChangePasswordRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.OldPassword != f.password {
			writeErr(w, apierr.New(apierr.InvalidCredentials))
			return
		}
		f.password = req.NewPassword
		w.WriteHeader(204)
	case r.Method == http.MethodGet && r.URL.Path == api.RouteState:
		etag := api.StateETag(f.stateVersion)
		if r.Header.Get(api.HeaderIfNoneMatch) == etag {
			w.Header().Set(api.HeaderETag, etag)
			w.WriteHeader(304)
			return
		}
		st := api.StateResponse{StateVersion: f.stateVersion, User: f.user, Profiles: []model.Profile{}, Subscriptions: append([]api.SubscriptionSummary{}, f.subs...)}
		for _, id := range f.order {
			if p, ok := f.profiles[id]; ok {
				st.Profiles = append(st.Profiles, p)
			}
		}
		w.Header().Set(api.HeaderETag, etag)
		writeJSON(w, 200, st)
	case r.Method == http.MethodPost && r.URL.Path == api.RouteProfiles:
		var req api.CreateProfileRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == "" {
			req.ID = model.NewID()
		}
		if cur, ok := f.profiles[req.ID]; ok {
			writeErr(w, apierr.New(apierr.RevisionConflict).With(apierr.DetailCurrent, cur))
			return
		}
		p := model.Profile{ID: req.ID, Name: req.Name, Nodes: req.Nodes, Proxies: req.Proxies, Visitors: req.Visitors, Revision: 1, UpdatedAt: time.Now().UTC()}
		p.Normalize()
		f.profiles[p.ID] = p
		f.order = append(f.order, p.ID)
		f.bump()
		writeJSON(w, 201, p)
	case strings.HasPrefix(r.URL.Path, api.RouteProfiles+"/"):
		id := strings.TrimPrefix(r.URL.Path, api.RouteProfiles+"/")
		cur, ok := f.profiles[id]
		if !ok {
			writeErr(w, apierr.New(apierr.NotFound))
			return
		}
		switch r.Method {
		case http.MethodPut:
			var req api.UpdateProfileRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.puts++
			if req.BaseRevision != cur.Revision {
				writeErr(w, apierr.New(apierr.RevisionConflict).With(apierr.DetailCurrent, cur))
				return
			}
			p := model.Profile{ID: id, Name: req.Name, Nodes: req.Nodes, Proxies: req.Proxies, Visitors: req.Visitors, Revision: cur.Revision + 1, UpdatedAt: time.Now().UTC()}
			p.Normalize()
			f.profiles[id] = p
			f.bump()
			writeJSON(w, 200, p)
		case http.MethodDelete:
			delete(f.profiles, id)
			f.bump()
			w.WriteHeader(204)
		default:
			writeErr(w, apierr.New(apierr.MethodNotAllowed))
		}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, api.RouteSubscriptions+"/") && strings.HasSuffix(r.URL.Path, "/runtime"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, api.RouteSubscriptions+"/"), "/runtime")
		f.runtimeCalls++
		for _, sm := range f.subs {
			if sm.ID != id {
				continue
			}
			if sm.Revoked {
				writeErr(w, apierr.New(apierr.ShareRevoked))
				return
			}
			writeJSON(w, 200, f.runtimes[id])
			return
		}
		writeErr(w, apierr.New(apierr.NotFound))
	default:
		writeErr(w, apierr.New(apierr.NotFound))
	}
}

// setSub 新增或替换一个订阅（和它的 runtime），stateVersion 加 1。
func (f *fakeServer) setSub(sum api.SubscriptionSummary, rt api.SubscriptionRuntime) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.subs {
		if f.subs[i].ID == sum.ID {
			f.subs[i] = sum
			f.runtimes[sum.ID] = rt
			f.bump()
			return
		}
	}
	f.subs = append(f.subs, sum)
	f.runtimes[sum.ID] = rt
	f.bump()
}

func (f *fakeServer) removeSub(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.subs {
		if f.subs[i].ID == id {
			f.subs = append(f.subs[:i], f.subs[i+1:]...)
			break
		}
	}
	f.bump()
}
