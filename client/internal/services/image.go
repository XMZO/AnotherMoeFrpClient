package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.design/x/clipboard"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
)

// 图片面板（PLAN 6.9）：图源由服务端下发（GET /api/v1/client/config 的 imageSources）。
// 图片由 Go 端下载（走应用的代理设置），存进 %LOCALAPPDATA%\MoeFrp\images\，
// 前端通过本地地址 /moefrp-img/<id> 显示，不受 CORS 限制。设置里关闭后不发任何图片请求。

// ImagePathPrefix 是前端显示图片用的本地地址前缀，由 ImageControl.Handler 提供。
const ImagePathPrefix = "/moefrp-img/"

const (
	maxImageBytes  = 15 << 20
	maxAPIBytes    = 1 << 20
	keepImages     = 30
	imageTimeout   = 30 * time.Second
	imageUserAgent = "MoeFrp-Client/3 (+https://github.com/fatedier/frp)"
)

// ImageInfo 是一张已下载的图片。
type ImageInfo struct {
	ID     string `json:"id"`
	URL    string `json:"url"` // 前端用的本地地址
	Mime   string `json:"mime"`
	Size   int64  `json:"size"`
	Source string `json:"source"` // 图片的原始地址（右键"查看原图"可以在浏览器打开）
}

// ImageService 绑定给前端。
type ImageService struct {
	dir      string
	settings *store.SettingsStore
	client   func() *http.Client

	mu         sync.Mutex
	sources    []api.ImageSource
	current    *ImageInfo
	fetching   bool
	saveDialog func(defaultName string) (string, error)
}

// ImageControl 是给 main 用的控制接口（不绑定给前端）。
type ImageControl struct{ s *ImageService }

// NewImageService：client 返回带当前代理设置的 http.Client。
func NewImageService(dir string, settings *store.SettingsStore, client func() *http.Client) (*ImageService, *ImageControl) {
	s := &ImageService{dir: dir, settings: settings, client: client}
	return s, &ImageControl{s: s}
}

// SetSources 更新图源（拿到服务端的客户端配置后调用）。
func (c *ImageControl) SetSources(src []api.ImageSource) {
	c.s.mu.Lock()
	c.s.sources = slices.Clone(src)
	c.s.mu.Unlock()
}

// SetSaveDialog 注入"另存为"对话框。
func (c *ImageControl) SetSaveDialog(fn func(defaultName string) (string, error)) {
	c.s.mu.Lock()
	c.s.saveDialog = fn
	c.s.mu.Unlock()
}

// Handler 在 next（前端资源）前面拦下 /moefrp-img/<id>，从图片缓存目录返回文件。
func (c *ImageControl) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, ImagePathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, ImagePathPrefix)
		f, ok := c.s.find(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, f)
	})
}

// Enabled 返回图片面板是否开启，以及有没有图源。
func (s *ImageService) Enabled() bool {
	s.mu.Lock()
	n := len(s.sources)
	s.mu.Unlock()
	return s.settings.Get().ImagePanel && n > 0
}

// Current 返回当前显示的图片（没有时为 nil）。
func (s *ImageService) Current() *ImageInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	c := *s.current
	return &c
}

// Next 换一张图：按权重随机选图源，失败时依次换下一个图源。
func (s *ImageService) Next() (*ImageInfo, error) {
	if !s.settings.Get().ImagePanel {
		return nil, apperr.New(apperr.ImageDisabled, "image panel is off")
	}
	s.mu.Lock()
	if s.fetching {
		s.mu.Unlock()
		return nil, apperr.New(apperr.ImageBusy, "already fetching")
	}
	order := weightedOrder(s.sources)
	s.fetching = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.fetching = false
		s.mu.Unlock()
	}()
	if len(order) == 0 {
		return nil, apperr.New(apperr.ImageNoSource, "no image sources")
	}

	var lastErr error
	for _, src := range order {
		ctx, cancel := context.WithTimeout(context.Background(), imageTimeout)
		info, err := s.fetch(ctx, src)
		cancel()
		if err == nil {
			s.mu.Lock()
			s.current = info
			s.mu.Unlock()
			s.prune()
			c := *info
			return &c, nil
		}
		lastErr = err
	}
	return nil, apperr.Wrap(apperr.ImageFetch, lastErr)
}

// CopyToClipboard 把图片转成 PNG 放进剪贴板。
func (s *ImageService) CopyToClipboard(id string) error {
	f, ok := s.find(id)
	if !ok {
		return apperr.New(apperr.ImageNotFound, id)
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return apperr.Wrap(apperr.ImageNotFound, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return apperr.Wrap(apperr.ImageDecode, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return apperr.Wrap(apperr.ImageDecode, err)
	}
	if err := clipboard.Init(); err != nil {
		return apperr.Wrap(apperr.ClipboardFailed, err)
	}
	if _, err := clipboard.Write(context.Background(), clipboard.FmtImage, buf.Bytes()); err != nil {
		return apperr.Wrap(apperr.ClipboardFailed, err)
	}
	return nil
}

// SaveAs 弹出"另存为"对话框保存原图，返回保存的路径；取消时返回空串。
func (s *ImageService) SaveAs(id string) (string, error) {
	f, ok := s.find(id)
	if !ok {
		return "", apperr.New(apperr.ImageNotFound, id)
	}
	s.mu.Lock()
	dialog := s.saveDialog
	s.mu.Unlock()
	if dialog == nil {
		return "", apperr.New(apperr.ExportFailed, "dialog not available")
	}
	path, err := dialog(time.Now().Format("20060102-150405") + filepath.Ext(f))
	if err != nil {
		return "", apperr.Wrap(apperr.ExportFailed, err)
	}
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(f)
	if err == nil {
		err = os.WriteFile(path, data, 0o644)
	}
	if err != nil {
		return "", apperr.Wrap(apperr.ExportFailed, err)
	}
	return path, nil
}

// ---- 内部 ----

// find 按 ID 找缓存里的文件。ID 只允许十六进制，防止路径穿越。
func (s *ImageService) find(id string) (string, bool) {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", false
	}
	matches, _ := filepath.Glob(filepath.Join(s.dir, id+".*"))
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

func (s *ImageService) fetch(ctx context.Context, src api.ImageSource) (*ImageInfo, error) {
	hc := s.client()
	imageURL := src.URL
	if src.IsAPI {
		body, _, err := get(ctx, hc, src.URL, maxAPIBytes, "application/json")
		if err != nil {
			return nil, err
		}
		var doc any
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("image api %s: %w", src.URL, err)
		}
		u, ok := jsonPath(doc, src.JSONPath).(string)
		if !ok || !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
			return nil, fmt.Errorf("image api %s: no url at %q", src.URL, src.JSONPath)
		}
		imageURL = u
	}
	body, ctype, err := get(ctx, hc, imageURL, maxImageBytes, "image/*")
	if err != nil {
		return nil, err
	}
	mt, _, _ := mime.ParseMediaType(ctype)
	if !strings.HasPrefix(mt, "image/") {
		mt = http.DetectContentType(body)
	}
	if !strings.HasPrefix(mt, "image/") {
		return nil, fmt.Errorf("image %s: not an image (%s)", imageURL, mt)
	}
	sum := sha256.Sum256(body)
	id := hex.EncodeToString(sum[:16])
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(s.dir, id+extFor(mt))
	if err := os.WriteFile(file, body, 0o644); err != nil {
		return nil, err
	}
	return &ImageInfo{ID: id, URL: ImagePathPrefix + id, Mime: mt, Size: int64(len(body)), Source: imageURL}, nil
}

func get(ctx context.Context, hc *http.Client, url string, limit int64, accept string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", imageUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > limit {
		return nil, "", fmt.Errorf("%s: larger than %d bytes", url, limit)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// jsonPath 按点分隔的路径取值，数组用数字下标，例如 "data.0.urls.regular"。
func jsonPath(doc any, path string) any {
	cur := doc
	for _, key := range strings.Split(path, ".") {
		if key == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			cur = v[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	return cur
}

// weightedOrder 按权重随机排出尝试顺序：第一个按权重抽，失败后依次尝试其余的。
func weightedOrder(src []api.ImageSource) []api.ImageSource {
	pool := slices.Clone(src)
	var out []api.ImageSource
	for len(pool) > 0 {
		total := 0
		for _, s := range pool {
			total += max(s.Weight, 1)
		}
		r := rand.IntN(total)
		for i, s := range pool {
			r -= max(s.Weight, 1)
			if r < 0 {
				out = append(out, s)
				pool = slices.Delete(pool, i, i+1)
				break
			}
		}
	}
	return out
}

func extFor(mt string) string {
	switch mt {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	}
	return ".jpg"
}

// prune 只保留最新的 30 张，当前显示的那张不删。
func (s *ImageService) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	type fi struct {
		name string
		mod  time.Time
	}
	var files []fi
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			files = append(files, fi{e.Name(), info.ModTime()})
		}
	}
	if len(files) <= keepImages {
		return
	}
	slices.SortFunc(files, func(a, b fi) int { return b.mod.Compare(a.mod) })
	s.mu.Lock()
	cur := ""
	if s.current != nil {
		cur = s.current.ID
	}
	s.mu.Unlock()
	for _, f := range files[keepImages:] {
		if cur != "" && strings.HasPrefix(f.name, cur) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, f.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
	}
}
