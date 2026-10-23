package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/apperr"
	"moefrp/client/internal/autostart"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

func TestEvaluateUpdate(t *testing.T) {
	cfg := api.ClientConfigResponse{LatestVersion: "3.1.0", MinVersion: "3.0.5", DownloadURL: "https://x/y.exe"}
	if u := evaluate("3.0.0", cfg); !u.Available || !u.Required {
		t.Fatalf("3.0.0: %+v", u)
	}
	if u := evaluate("3.1.0", cfg); u.Available || u.Required {
		t.Fatalf("3.1.0: %+v", u)
	}
	if u := evaluate("dev", cfg); u.Available || u.Required {
		t.Fatalf("dev builds must not prompt: %+v", u)
	}
}

func TestUpdaterEmitsOnceAndFeedsSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ClientConfigResponse{
			LatestVersion: "3.2.0", MinVersion: "3.0.0", Announcement: "维护通知",
			ImageSources: []api.ImageSource{{URL: "https://img.example/a", Weight: 1}},
		})
	}))
	defer srv.Close()
	c, _ := apiclient.New(srv.URL, "3.0.0", "inst", apiclient.ProxyNone)
	u := NewUpdater(c, "3.0.0")
	var events []string
	u.SetEmitter(func(name string, data any) { events = append(events, name) })
	var sources []api.ImageSource
	u.OnConfig(func(cfg api.ClientConfigResponse) { sources = cfg.ImageSources })
	for range 2 {
		if _, err := u.Check(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(events, ",") != EventUpdateAvailable+","+EventAnnouncement {
		t.Fatalf("events: %v", events)
	}
	if len(sources) != 1 || !u.Info().Available || u.Info().CheckedAt == nil {
		t.Fatalf("info: %+v sources=%v", u.Info(), sources)
	}
}

func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageServiceFetchAPIAndDirect(t *testing.T) {
	data := pngBytes(t)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			fmt.Fprintf(w, `{"data":[{"urls":{"regular":"%s/pic.png"}}]}`, srvURL)
		case "/pic.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(data)
		case "/notimage":
			w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	dir := t.TempDir()
	settings, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc, ctl := NewImageService(filepath.Join(dir, "images"), settings, func() *http.Client { return srv.Client() })
	if svc.Enabled() {
		t.Fatal("no sources yet: should be disabled")
	}
	ctl.SetSources([]api.ImageSource{{URL: srv.URL + "/api", Weight: 5, IsAPI: true, JSONPath: "data.0.urls.regular"}})
	info, err := svc.Next()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mime != "image/png" || !strings.HasPrefix(info.URL, ImagePathPrefix) || info.Source != srv.URL+"/pic.png" {
		t.Fatalf("info: %+v", info)
	}

	// 本地地址能取回图片；其他路径交给前端资源；非法 ID 返回 404
	h := ctl.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("asset")) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", info.URL, nil))
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("serve image: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", ImagePathPrefix+"..%5c..%5csettings.json", nil))
	if rec.Code != 404 {
		t.Fatalf("path traversal: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/index.html", nil))
	if rec.Body.String() != "asset" {
		t.Fatal("non-image paths must fall through")
	}

	// 第一个图源失败时换下一个
	ctl.SetSources([]api.ImageSource{{URL: srv.URL + "/notimage", Weight: 100}, {URL: srv.URL + "/pic.png", Weight: 1}})
	if _, err := svc.Next(); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	// 全部失败
	ctl.SetSources([]api.ImageSource{{URL: srv.URL + "/missing"}})
	if _, err := svc.Next(); apierr.CodeOf(err) != apperr.ImageFetch {
		t.Fatalf("all fail: %v", err)
	}
	// 关闭面板后不发请求
	if _, err := settings.Update(func(s *store.Settings) error { s.ImagePanel = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Next(); apierr.CodeOf(err) != apperr.ImageDisabled {
		t.Fatalf("disabled: %v", err)
	}
}

func TestJSONPathAndWeights(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`[{"url":"a"},{"url":"b"}]`), &doc)
	if jsonPath(doc, "1.url") != "b" || jsonPath(doc, "5.url") != nil || jsonPath(doc, "x") != nil {
		t.Fatal("jsonPath")
	}
	src := []api.ImageSource{{URL: "a", Weight: 1}, {URL: "b", Weight: 0}, {URL: "c", Weight: 3}}
	if got := weightedOrder(src); len(got) != 3 {
		t.Fatalf("order: %v", got)
	}
}

func TestSettingsServicePhase5(t *testing.T) {
	dir := t.TempDir()
	settings, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	cur := settings.Get()
	if !cur.ImagePanel || !cur.ReleaseOnHide || cur.AutoStart {
		t.Fatalf("defaults: %+v", cur)
	}
	sink := engine.NewLogSink(10)
	svc := NewSettingsService(settings, sink)
	var gotMode, gotURL string
	svc.OnNetworkProxy = func(m, u string) { gotMode, gotURL = m, u }

	bad := cur
	bad.NetworkProxy, bad.ProxyURL = store.NetProxyCustom, "127.0.0.1:7890"
	if _, err := svc.Update(bad); apierr.CodeOf(err) != apperr.ProxyURLInvalid {
		t.Fatalf("invalid proxy url: %v", err)
	}
	good := cur
	good.NetworkProxy, good.ProxyURL = store.NetProxyCustom, " socks5://127.0.0.1:1080 "
	out, err := svc.Update(good)
	if err != nil || out.ProxyURL != "socks5://127.0.0.1:1080" || gotMode != store.NetProxyCustom || gotURL != out.ProxyURL {
		t.Fatalf("custom proxy: %+v %v %s %s", out, err, gotMode, gotURL)
	}

	// 开机自启：写注册表（测试专用的值名），再关掉
	entry := &autostart.Entry{Name: fmt.Sprintf("MoeFrp-unittest-%d", time.Now().UnixNano()), Exe: `C:\x\MoeFrp.exe`}
	t.Cleanup(func() { _ = entry.Set(false) })
	svc.AutoStart = entry
	on := settings.Get()
	on.AutoStart = true
	if _, err := svc.Update(on); err != nil {
		t.Fatal(err)
	}
	if exists, current, _ := entry.State(); !exists || !current || !settings.Get().AutoStart {
		t.Fatal("autostart not written")
	}
	// exe 换了位置：启动时核对并重写
	entry.Exe = `D:\moved\MoeFrp.exe`
	if err := svc.ReconcileAutoStart(); err != nil {
		t.Fatal(err)
	}
	if _, current, _ := entry.State(); !current {
		t.Fatal("reconcile should rewrite the path")
	}
	off := settings.Get()
	off.AutoStart = false
	if _, err := svc.Update(off); err != nil {
		t.Fatal(err)
	}
	if exists, _, _ := entry.State(); exists {
		t.Fatal("autostart not removed")
	}
	_ = os.Remove(filepath.Join(dir, "settings.json"))
}
