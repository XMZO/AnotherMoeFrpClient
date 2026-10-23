package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"moefrp/client/internal/apperr"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
)

func TestErrorClassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.HeaderClientVersion) != "3.0.0" || r.Header.Get(api.HeaderInstallID) != "inst-1" {
			t.Errorf("missing headers: %v", r.Header)
		}
		if r.Header.Get(api.HeaderAuthorization) != "Bearer mfs_x" {
			t.Errorf("auth header: %q", r.Header.Get(api.HeaderAuthorization))
		}
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(apierr.Response{Error: apierr.New(apierr.SessionReplaced)})
	})
	mux.HandleFunc("/api/v1/auth/change-password", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(apierr.Response{Error: apierr.New(apierr.InvalidCredentials)})
	})
	mux.HandleFunc("/api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	})
	mux.HandleFunc("/api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.HeaderIfNoneMatch) == `"7"` {
			w.WriteHeader(304)
			return
		}
		_ = json.NewEncoder(w).Encode(api.StateResponse{StateVersion: 7, Profiles: []model.Profile{{ID: "p", Name: "x"}}})
	})
	mux.HandleFunc("/api/v1/profiles/p", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		cur := model.Profile{ID: "p", Name: "cloud", Revision: 5}
		_ = json.NewEncoder(w).Encode(apierr.Response{Error: apierr.New(apierr.RevisionConflict).With(apierr.DetailCurrent, cur)})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "3.0.0", "inst-1", ProxyNone)
	if err != nil {
		t.Fatal(err)
	}
	c.SetToken("mfs_x")
	ctx := context.Background()

	_, err = c.Me(ctx)
	if apierr.CodeOf(err) != apierr.SessionReplaced || !SessionEnded(err) {
		t.Fatalf("me: %v", err)
	}
	err = c.ChangePassword(ctx, api.ChangePasswordRequest{OldPassword: "a", NewPassword: "b"})
	if apierr.CodeOf(err) != apierr.InvalidCredentials || SessionEnded(err) {
		t.Fatalf("invalid_credentials is 401 but must not end the session: %v", err)
	}
	if err := c.Logout(ctx); !IsOffline(err) {
		t.Fatalf("502 html should count as unreachable: %v", err)
	}
	st, nm, err := c.State(ctx, -1)
	if err != nil || nm || st.StateVersion != 7 || st.Profiles[0].Nodes == nil {
		t.Fatalf("state: %+v %v %v", st, nm, err)
	}
	if _, nm, err := c.State(ctx, 7); err != nil || !nm {
		t.Fatalf("etag: %v %v", nm, err)
	}
	_, err = c.UpdateProfile(ctx, "p", api.UpdateProfileRequest{BaseRevision: 1})
	cur, ok := ConflictCurrent(err)
	if !ok || cur.Name != "cloud" || cur.Revision != 5 {
		t.Fatalf("conflict current: %+v %v %v", cur, ok, err)
	}

	srv.Close()
	if _, err := c.Me(ctx); !IsOffline(err) || apierr.CodeOf(err) != apperr.ServerUnreachable {
		t.Fatalf("closed server: %v", err)
	}
	if _, err := New("ftp://x", "", "", ProxyNone); err == nil {
		t.Fatal("bad base url accepted")
	}
}

func TestSystemProxyRules(t *testing.T) {
	old := readSystemProxy
	defer func() { readSystemProxy = old }()
	readSystemProxy = func() SystemProxySettings {
		return SystemProxySettings{Enabled: true, Server: "http=10.0.0.1:8080;https=10.0.0.2:8443;socks=10.0.0.3:1080", Override: "<local>;*.corp.example;192.168.*"}
	}
	f := proxyFunc(ProxySystem, "")
	get := func(raw string) string {
		u, _ := url.Parse(raw)
		p, err := f(&http.Request{URL: u})
		if err != nil {
			t.Fatal(err)
		}
		if p == nil {
			return ""
		}
		return p.String()
	}
	cases := map[string]string{
		"https://frplus.loli-con.cn/api": "http://10.0.0.2:8443",
		"http://example.com/":            "http://10.0.0.1:8080",
		"http://127.0.0.1:8080/":         "",
		"http://localhost/":              "",
		"http://intranet/":               "",
		"https://a.corp.example/":        "",
		"http://192.168.1.10/":           "",
	}
	for in, want := range cases {
		if got := get(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
	readSystemProxy = func() SystemProxySettings { return SystemProxySettings{Enabled: true, Server: "127.0.0.1:7890"} }
	if got := get("https://example.com/"); got != "http://127.0.0.1:7890" {
		t.Errorf("single proxy: %q", got)
	}
	readSystemProxy = func() SystemProxySettings { return SystemProxySettings{Enabled: true, Server: "socks=127.0.0.1:1080"} }
	if got := get("https://example.com/"); got != "socks5://127.0.0.1:1080" {
		t.Errorf("socks only: %q", got)
	}
	if proxyFunc(ProxyNone, "") != nil {
		t.Error("none mode must not use a proxy")
	}
}

func TestCustomProxy(t *testing.T) {
	f := proxyFunc(ProxyCustom, "socks5://127.0.0.1:1080")
	r, _ := http.NewRequest("GET", "https://frplus.example.com/api", nil)
	u, err := f(r)
	if err != nil || u == nil || u.String() != "socks5://127.0.0.1:1080" {
		t.Fatalf("custom proxy: %v %v", u, err)
	}
	r, _ = http.NewRequest("GET", "http://127.0.0.1:18080/api", nil)
	if u, _ := f(r); u != nil {
		t.Fatalf("loopback must be direct, got %v", u)
	}
	if proxyFunc(ProxyCustom, "::bad") != nil {
		t.Fatal("invalid custom url should mean no proxy")
	}
}
