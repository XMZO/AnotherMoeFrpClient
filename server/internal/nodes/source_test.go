package nodes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"1.2.0", "1.1.9", true},
		{"1.2.0", "1.2.0", false},
		{"1.2.0", "1.3.0", false}, // 节点上的更新（例如手动装了测试版），不提示降级
		{"1.2.0", "dev", true},
		{"1.2.0", "main-abc1234", true},
		{"", "1.0.0", false},
	} {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

const sumA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const sumB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

func fakeGitHub(t *testing.T, calls *atomic.Int32) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			calls.Add(1)
			dl := srv.URL + "/dl/"
			fmt.Fprintf(w, `[
			  {"tag_name":"v3.0.0","assets":[]},
			  {"tag_name":"agent-v1.3.0","prerelease":true,"assets":[]},
			  {"tag_name":"agent-v1.4.0","draft":true,"assets":[]},
			  {"tag_name":"agent-v1.1.0","assets":[{"name":"SHA256SUMS","browser_download_url":"%[1]sold/SHA256SUMS"}]},
			  {"tag_name":"agent-v1.2.0","assets":[
			    {"name":"moefrp-agent-linux-amd64","browser_download_url":"%[1]s1.2.0/moefrp-agent-linux-amd64"},
			    {"name":"moefrp-agent-linux-arm64","browser_download_url":"%[1]s1.2.0/moefrp-agent-linux-arm64"},
			    {"name":"SHA256SUMS","browser_download_url":"%[1]s1.2.0/SHA256SUMS"}]}
			]`, dl)
		case "/dl/1.2.0/SHA256SUMS":
			// arm64 没有校验和：不能用
			fmt.Fprintf(w, "%s  moefrp-agent-linux-amd64\n%s *other-file\n", sumA, sumB)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubSource(t *testing.T) {
	var calls atomic.Int32
	srv := fakeGitHub(t, &calls)
	g := &GitHub{Repo: "o/r", API: srv.URL, Client: srv.Client()}
	b, err := g.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != "1.2.0" || b.Tag != "agent-v1.2.0" || b.Source != "github" {
		t.Fatalf("应选正式发布里版本最大的：%+v", b)
	}
	if !b.Has("amd64") || b.Has("arm64") {
		t.Fatalf("没有校验和的架构不能用：%+v", b.Files)
	}
	if f := b.Files["amd64"]; f.SHA256 != sumA || f.URL != srv.URL+"/dl/1.2.0/moefrp-agent-linux-amd64" {
		t.Fatalf("amd64：%+v", f)
	}
	// 缓存：10 分钟内不再查询
	g.Latest(context.Background())
	if c, _ := g.Cached(); c == nil || calls.Load() != 1 {
		t.Fatalf("应使用缓存，查询了 %d 次", calls.Load())
	}

	// 下载地址前缀（镜像）
	g2 := &GitHub{Repo: "o/r", API: srv.URL, Client: srv.Client(), Mirror: "https://mirror.example.com/"}
	b2, err := g2.Latest(context.Background())
	if err != nil || !strings.HasPrefix(b2.Files["amd64"].URL, "https://mirror.example.com/https://") {
		t.Fatalf("镜像前缀：%+v %v", b2, err)
	}

	// 仓库里没有 agent 发布
	g3 := &GitHub{Repo: "x/y", API: srv.URL, Client: srv.Client()}
	if _, err := g3.Latest(context.Background()); err == nil {
		t.Fatal("没有发布时应报错")
	}
}

func TestInstallScriptValues(t *testing.T) {
	b := &Build{Version: "1.2.0", Files: map[string]BuildFile{
		"amd64": {URL: "https://github.com/o/r/releases/download/agent-v1.2.0/moefrp-agent-linux-amd64", SHA256: sumA},
		"arm64": {URL: "/agent/download/linux-arm64", SHA256: sumA},
	}}
	s := InstallScript("https://frp.example.com", b, "")
	for _, want := range []string{
		"SERVER='https://frp.example.com'",
		"VERSION='1.2.0'",
		"URL_AMD64='https://github.com/o/r/releases/download/agent-v1.2.0/moefrp-agent-linux-amd64'",
		"URL_ARM64='https://frp.example.com/agent/download/linux-arm64'", // 本地目录的相对地址补全
		"SHA_AMD64='" + sumA + "'",
		"UNAVAILABLE=''",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("脚本里缺少 %s", want)
		}
	}
	if strings.Contains(s, "__") {
		t.Error("还有没替换的占位符")
	}
	s = InstallScript("https://frp.example.com", nil, "查不到 it's broken")
	if !strings.Contains(s, "UNAVAILABLE='查不到 its broken'") || !strings.Contains(s, "URL_AMD64=''") {
		t.Error("拿不到版本时脚本仍可用于卸载，安装报原因（单引号被去掉）")
	}
}
