package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"moefrp/shared/agentproto"
	"moefrp/shared/api"
)

// Build 是可以安装或升级到的 agent 版本。
type Build struct {
	Version string
	Source  string               // github / local
	Tag     string               // GitHub Release 的标签，本地目录时为空
	Files   map[string]BuildFile // 键是架构：amd64、arm64
}

// BuildFile 是一个架构的程序。URL 以 / 开头时是面板上的相对路径（本地目录），否则是完整地址。
type BuildFile struct {
	URL    string
	SHA256 string
}

// Has 判断有没有某个架构的程序。
func (b *Build) Has(arch string) bool {
	if b == nil {
		return false
	}
	_, ok := b.Files[arch]
	return ok
}

// ArchList 返回已有的架构。
func (b *Build) ArchList() []string {
	var out []string
	for _, a := range Arches {
		if b.Has(a) {
			out = append(out, a)
		}
	}
	return out
}

// Source 提供最新的 agent 版本：GitHub Release（默认）或本地目录。
type Source interface {
	// Latest 返回最新版本，可能要访问网络。
	Latest(ctx context.Context) (*Build, error)
	// Cached 立即返回上次查到的结果（可能为 nil），过期时在后台刷新。用在列表页这类不能等网络的地方。
	Cached() (*Build, error)
	// Kind 是 github 或 local。
	Kind() string
}

// newer 判断 latest 是否比 current 新：都是语义化版本时按版本号比较，否则只要不同就算新
// （例如开发版 dev、CI 构建 main-abc1234）。
func newer(latest, current string) bool {
	if latest == "" || latest == current {
		return false
	}
	lv, err1 := api.ParseVersion(latest)
	cv, err2 := api.ParseVersion(current)
	if err1 == nil && err2 == nil {
		return lv.Compare(cv) > 0
	}
	return true
}

// ---- 本地目录 ----

// Latest 实现 Source。
func (d *Dist) Latest(context.Context) (*Build, error) { return d.build(), nil }

// Cached 实现 Source。
func (d *Dist) Cached() (*Build, error) { return d.build(), nil }

// Kind 实现 Source。
func (d *Dist) Kind() string { return "local" }

func (d *Dist) build() *Build {
	b := &Build{Version: d.version, Source: "local", Files: map[string]BuildFile{}}
	for arch, f := range d.files {
		b.Files[arch] = BuildFile{URL: agentproto.RouteDownload + f.Name, SHA256: f.SHA256}
	}
	return b
}

// ---- GitHub Release ----

// TagPrefix 是 agent 发布的标签前缀：agent-v1.2.3 → 版本 1.2.3。
const TagPrefix = "agent-v"

const (
	githubTTL      = 10 * time.Minute
	githubRetryTTL = time.Minute // 查询失败后多久重试
)

// GitHub 从 GitHub Release 获取 agent：取标签以 agent-v 开头、版本号最大的正式发布，
// 校验和来自同一个发布里的 SHA256SUMS。
type GitHub struct {
	Repo   string       // owner/name
	API    string       // 默认 https://api.github.com，测试时替换
	Client *http.Client // nil 时用 30 秒超时的默认客户端
	Log    *slog.Logger

	mu         sync.Mutex
	build      *Build
	err        error
	fetchedAt  time.Time
	refreshing bool
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidRepo 检查 owner/name 格式。
func ValidRepo(s string) bool { return repoRe.MatchString(s) }

// Kind 实现 Source。
func (g *GitHub) Kind() string { return "github" }

func (g *GitHub) stale(now time.Time) bool {
	if g.fetchedAt.IsZero() {
		return true
	}
	ttl := githubTTL
	if g.err != nil {
		ttl = githubRetryTTL
	}
	return now.Sub(g.fetchedAt) > ttl
}

// Latest 实现 Source。查询失败但以前查到过时，返回旧结果。
func (g *GitHub) Latest(ctx context.Context) (*Build, error) {
	g.mu.Lock()
	if !g.stale(time.Now()) {
		defer g.mu.Unlock()
		return g.result()
	}
	g.mu.Unlock()
	b, err := g.fetch(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.store(b, err)
	return g.result()
}

// Cached 实现 Source。
func (g *GitHub) Cached() (*Build, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stale(time.Now()) && !g.refreshing {
		g.refreshing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b, err := g.fetch(ctx)
			g.mu.Lock()
			g.store(b, err)
			g.refreshing = false
			g.mu.Unlock()
		}()
	}
	return g.result()
}

func (g *GitHub) store(b *Build, err error) {
	g.fetchedAt = time.Now()
	g.err = err
	if err == nil {
		g.build = b
	} else if g.Log != nil {
		g.Log.Warn("fetch agent release from github failed", "repo", g.Repo, "err", err)
	}
}

func (g *GitHub) result() (*Build, error) {
	if g.build != nil {
		return g.build, nil // 查询失败时继续用上次的结果
	}
	if g.err != nil {
		return nil, g.err
	}
	return nil, nil // 还没查过（Cached 刚触发了后台查询）
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (g *GitHub) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "moefrp-server")
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s：HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// safeURL 防止奇怪的字符进入安装脚本（脚本里用单引号包起来）。
var safeURL = regexp.MustCompile(`^https://[A-Za-z0-9._~:/?#\[\]@!$&()*+,;=%-]+$`)

func (g *GitHub) fetch(ctx context.Context) (*Build, error) {
	base := g.API
	if base == "" {
		base = "https://api.github.com"
	}
	body, err := g.get(ctx, base+"/repos/"+g.Repo+"/releases?per_page=50", 4<<20)
	if err != nil {
		return nil, fmt.Errorf("查询 GitHub Release：%w", err)
	}
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, fmt.Errorf("解析 GitHub Release：%w", err)
	}
	var best *ghRelease
	var bestV api.Version
	for i := range rels {
		r := &rels[i]
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, TagPrefix) {
			continue
		}
		v, err := api.ParseVersion(strings.TrimPrefix(r.Tag, TagPrefix))
		if err != nil {
			continue
		}
		if best == nil || v.Compare(bestV) > 0 {
			best, bestV = r, v
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%s 还没有发布过 agent（标签 %s1.0.0 这样的正式 Release）", g.Repo, TagPrefix)
	}
	assets := map[string]string{}
	for _, a := range best.Assets {
		assets[a.Name] = a.URL
	}
	sumsURL, ok := assets["SHA256SUMS"]
	if !ok {
		return nil, fmt.Errorf("%s 里没有 SHA256SUMS", best.Tag)
	}
	sums, err := g.get(ctx, sumsURL, 64<<10)
	if err != nil {
		return nil, fmt.Errorf("下载 SHA256SUMS：%w", err)
	}
	hashes := parseSums(string(sums))
	b := &Build{Version: strings.TrimPrefix(best.Tag, TagPrefix), Source: "github", Tag: best.Tag, Files: map[string]BuildFile{}}
	for _, arch := range Arches {
		name := "moefrp-agent-linux-" + arch
		u, ok := assets[name]
		sum := hashes[name]
		if !ok || sum == "" {
			continue
		}
		if !safeURL.MatchString(u) {
			return nil, fmt.Errorf("下载地址 %q 含有不支持的字符", u)
		}
		b.Files[arch] = BuildFile{URL: u, SHA256: sum}
	}
	if len(b.Files) == 0 {
		return nil, fmt.Errorf("%s 里没有可用的 agent 程序", best.Tag)
	}
	return b, nil
}

// NormalizeMirror 检查下载代理前缀（gh-proxy 这类"代理地址 + GitHub 地址"的服务）：
// 空表示直连；必须是 https，自动补上末尾的 /。
func NormalizeMirror(m string) (string, error) {
	m = strings.TrimSpace(m)
	if m == "" {
		return "", nil
	}
	if !strings.HasSuffix(m, "/") {
		m += "/"
	}
	u, err := url.Parse(m)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		len(m) > 200 || !safeURL.MatchString(m) {
		return "", errors.New("下载代理应是 https 地址，例如 https://ghproxy.example.com/")
	}
	return m, nil
}

// WithMirror 给 GitHub 的完整下载地址加上代理前缀；面板上的相对地址（本地目录）不需要代理。
func WithMirror(u, mirror string) string {
	if mirror == "" || strings.HasPrefix(u, "/") {
		return u
	}
	return mirror + u
}

var hexRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// parseSums 解析 sha256sum 的输出："<hex>  <文件名>"（文件名前可能有 *）。
func parseSums(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !hexRe.MatchString(f[0]) {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return out
}
