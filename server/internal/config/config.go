// Package config 读取服务端配置：TOML 文件，再用环境变量覆盖（PLAN 7.1、7.11）。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"moefrp/shared/api"
)

// Config 是服务端的全部配置。
type Config struct {
	Listen         string   `toml:"listen"`
	DBPath         string   `toml:"db_path"`
	TrustedProxies []string `toml:"trusted_proxies"` // 只信任这些地址发来的 X-Forwarded-For
	SingleSession  bool     `toml:"single_session"`  // 单设备模式：新登录吊销该用户的其他会话
	SessionTTL     Duration `toml:"session_ttl"`     // 滑动过期：这么久没用才失效
	LogLevel       string   `toml:"log_level"`       // debug / info / warn / error

	Client Client `toml:"client"`
}

// Client 是 GET /api/v1/client/config 下发的内容。
type Client struct {
	LatestVersion string        `toml:"latest_version"`
	MinVersion    string        `toml:"min_version"` // 低于它的客户端收到 426 client_outdated；空表示不检查
	DownloadURL   string        `toml:"download_url"`
	SHA256        string        `toml:"sha256"`
	ReleaseNotes  string        `toml:"release_notes"`
	Announcement  string        `toml:"announcement"`
	ImageSources  []ImageSource `toml:"image_sources"`
}

// ImageSource 是一个图源。
type ImageSource struct {
	URL      string `toml:"url"`
	Weight   int    `toml:"weight"`
	IsAPI    bool   `toml:"is_api"`
	JSONPath string `toml:"json_path"`
}

// Duration 让 TOML 里可以写 "720h" 这样的字符串。
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// D 返回 time.Duration。
func (d Duration) D() time.Duration { return time.Duration(d) }

// Default 返回默认配置。
func Default() Config {
	return Config{
		Listen:         "127.0.0.1:8080",
		DBPath:         "moefrp.db",
		TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
		SingleSession:  true,
		SessionTTL:     Duration(30 * 24 * time.Hour),
		LogLevel:       "info",
	}
}

// Load 读取配置：先取默认值，再读 path（为空则跳过），最后应用环境变量，然后校验。
// TOML 里出现未知字段时报错，避免拼错的配置项被静默忽略。
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
		dec := toml.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			var sme *toml.StrictMissingError
			if errors.As(err, &sme) {
				return Config{}, fmt.Errorf("config: %s: unknown fields:\n%s", path, sme.String())
			}
			return Config{}, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	if err := applyEnv(&cfg, os.LookupEnv); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// EnvPrefix 是环境变量的前缀，例如 MOEFRP_LISTEN、MOEFRP_CLIENT_MIN_VERSION。
const EnvPrefix = "MOEFRP_"

// envVars 列出可以用环境变量覆盖的配置项。
var envVars = []struct {
	name string
	set  func(c *Config, v string) error
}{
	{"LISTEN", func(c *Config, v string) error { c.Listen = v; return nil }},
	{"DB_PATH", func(c *Config, v string) error { c.DBPath = v; return nil }},
	{"TRUSTED_PROXIES", func(c *Config, v string) error { c.TrustedProxies = splitList(v); return nil }},
	{"SINGLE_SESSION", func(c *Config, v string) error {
		b, err := strconv.ParseBool(v)
		c.SingleSession = b
		return err
	}},
	{"SESSION_TTL", func(c *Config, v string) error { return c.SessionTTL.UnmarshalText([]byte(v)) }},
	{"LOG_LEVEL", func(c *Config, v string) error { c.LogLevel = v; return nil }},
	{"CLIENT_LATEST_VERSION", func(c *Config, v string) error { c.Client.LatestVersion = v; return nil }},
	{"CLIENT_MIN_VERSION", func(c *Config, v string) error { c.Client.MinVersion = v; return nil }},
	{"CLIENT_DOWNLOAD_URL", func(c *Config, v string) error { c.Client.DownloadURL = v; return nil }},
	{"CLIENT_SHA256", func(c *Config, v string) error { c.Client.SHA256 = v; return nil }},
	{"CLIENT_RELEASE_NOTES", func(c *Config, v string) error { c.Client.ReleaseNotes = v; return nil }},
	{"CLIENT_ANNOUNCEMENT", func(c *Config, v string) error { c.Client.Announcement = v; return nil }},
}

// EnvNames 返回所有支持的环境变量名，供 --help 和文档使用。
func EnvNames() []string {
	out := make([]string, len(envVars))
	for i, e := range envVars {
		out[i] = EnvPrefix + e.name
	}
	return out
}

func applyEnv(c *Config, lookup func(string) (string, bool)) error {
	for _, e := range envVars {
		v, ok := lookup(EnvPrefix + e.name)
		if !ok {
			continue
		}
		if err := e.set(c, v); err != nil {
			return fmt.Errorf("config: %s%s: %w", EnvPrefix, e.name, err)
		}
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Validate 检查配置是否可用。
func (c Config) Validate() error {
	var errs []error
	if c.Listen == "" {
		errs = append(errs, errors.New("listen 不能为空"))
	}
	if c.DBPath == "" {
		errs = append(errs, errors.New("db_path 不能为空"))
	}
	if _, err := c.TrustedPrefixes(); err != nil {
		errs = append(errs, err)
	}
	if c.SessionTTL.D() < time.Hour {
		errs = append(errs, fmt.Errorf("session_ttl 至少 1h，当前 %s", c.SessionTTL.D()))
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log_level 只能是 debug/info/warn/error，当前 %q", c.LogLevel))
	}
	for _, f := range []struct{ name, v string }{
		{"client.latest_version", c.Client.LatestVersion},
		{"client.min_version", c.Client.MinVersion},
	} {
		if f.v != "" {
			if _, err := api.ParseVersion(f.v); err != nil {
				errs = append(errs, fmt.Errorf("%s 不是合法的版本号：%q", f.name, f.v))
			}
		}
	}
	if c.Client.LatestVersion != "" && c.Client.MinVersion != "" {
		if cmp, err := api.CompareVersions(c.Client.MinVersion, c.Client.LatestVersion); err == nil && cmp > 0 {
			errs = append(errs, errors.New("client.min_version 不能高于 client.latest_version"))
		}
	}
	for i, s := range c.Client.ImageSources {
		if s.URL == "" {
			errs = append(errs, fmt.Errorf("client.image_sources[%d].url 不能为空", i))
		}
		if s.Weight < 0 {
			errs = append(errs, fmt.Errorf("client.image_sources[%d].weight 不能为负数", i))
		}
		if s.IsAPI && s.JSONPath == "" {
			errs = append(errs, fmt.Errorf("client.image_sources[%d]：is_api = true 时需要 json_path", i))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

// TrustedPrefixes 把 trusted_proxies 解析成网段。单个 IP 视为 /32 或 /128。
func (c Config) TrustedPrefixes() ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, s := range c.TrustedProxies {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies 里的 %q 不是合法的 IP 或网段", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// ClientConfig 转成 API 响应。
func (c Client) ClientConfig() api.ClientConfigResponse {
	out := api.ClientConfigResponse{
		LatestVersion: c.LatestVersion,
		MinVersion:    c.MinVersion,
		DownloadURL:   c.DownloadURL,
		SHA256:        c.SHA256,
		ReleaseNotes:  c.ReleaseNotes,
		Announcement:  c.Announcement,
		ImageSources:  make([]api.ImageSource, 0, len(c.ImageSources)),
	}
	for _, s := range c.ImageSources {
		out.ImageSources = append(out.ImageSources, api.ImageSource{URL: s.URL, Weight: s.Weight, IsAPI: s.IsAPI, JSONPath: s.JSONPath})
	}
	return out
}
