package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHealthCommand(t *testing.T) {
	c := newCLI(t)
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	// 容器里 listen 一般是 0.0.0.0:8080，health 总是请求 127.0.0.1 上的同一端口
	t.Setenv("MOEFRP_LISTEN", "0.0.0.0:"+u.Port())
	if code, out, errs := c.run("health", "-c", c.cfg); code != 0 || out == "" {
		t.Fatalf("healthy: code=%d out=%q err=%q", code, out, errs)
	}
	status = http.StatusServiceUnavailable
	if code, _, _ := c.run("health", "-c", c.cfg); code != 1 {
		t.Fatalf("unhealthy should exit 1, got %d", code)
	}
}
