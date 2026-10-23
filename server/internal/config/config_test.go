package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || !cfg.SingleSession || cfg.SessionTTL.D() != 720*time.Hour {
		t.Fatalf("默认值不对：%+v", cfg)
	}
}

// 与 PLAN 7.11 的示例一致的配置必须能读进来。
func TestLoadExample(t *testing.T) {
	p := writeFile(t, `
listen          = "127.0.0.1:9090"
db_path         = "/var/lib/moefrp/moefrp.db"
trusted_proxies = ["127.0.0.1/32", "::1/128", "10.0.0.1"]
single_session  = false
session_ttl     = "48h"

[client]
latest_version = "3.1.0"
min_version    = "3.0.0"
download_url   = "https://example.com/MoeFrp-3.1.0-windows-amd64.exe"
sha256         = ""
announcement   = "公告"

[[client.image_sources]]
url    = "https://umi.li/random?dir=lucky&type=img"
weight = 5
is_api = false

[[client.image_sources]]
url       = "https://api.example.com/img"
weight    = 1
is_api    = true
json_path = "data.url"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9090" || cfg.SingleSession || cfg.SessionTTL.D() != 48*time.Hour {
		t.Fatalf("读取不对：%+v", cfg)
	}
	if len(cfg.Client.ImageSources) != 2 || !cfg.Client.ImageSources[1].IsAPI || cfg.Client.ImageSources[1].JSONPath != "data.url" {
		t.Fatalf("图源不对：%+v", cfg.Client.ImageSources)
	}
	prefixes, err := cfg.TrustedPrefixes()
	if err != nil || len(prefixes) != 3 || prefixes[2].String() != "10.0.0.1/32" {
		t.Fatalf("可信代理解析不对：%v %v", prefixes, err)
	}
	cc := cfg.Client.ClientConfig()
	if cc.LatestVersion != "3.1.0" || cc.Announcement != "公告" || len(cc.ImageSources) != 2 {
		t.Fatalf("ClientConfig 转换不对：%+v", cc)
	}
}

// deploy/config.example.toml 必须与配置结构保持一致。
func TestDeployExample(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "deploy", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || cfg.DBPath != "/var/lib/moefrp/moefrp.db" || !cfg.SingleSession ||
		cfg.SessionTTL.D() != 720*time.Hour || cfg.Client.MinVersion != "3.0.0" || len(cfg.Client.ImageSources) != 1 {
		t.Fatalf("示例配置与 PLAN 7.11 不一致：%+v", cfg)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	p := writeFile(t, "listen = \"127.0.0.1:1\"\nsingle_sesion = true\n")
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "single_sesion") {
		t.Fatalf("拼错的字段应报错：%v", err)
	}
}

func TestEnvOverride(t *testing.T) {
	p := writeFile(t, "listen = \"127.0.0.1:1\"\n")
	t.Setenv("MOEFRP_LISTEN", "0.0.0.0:2")
	t.Setenv("MOEFRP_SINGLE_SESSION", "false")
	t.Setenv("MOEFRP_SESSION_TTL", "2h")
	t.Setenv("MOEFRP_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.1")
	t.Setenv("MOEFRP_CLIENT_MIN_VERSION", "3.0.0")
	t.Setenv("MOEFRP_CLIENT_LATEST_VERSION", "3.0.1")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:2" || cfg.SingleSession || cfg.SessionTTL.D() != 2*time.Hour ||
		len(cfg.TrustedProxies) != 2 || cfg.Client.MinVersion != "3.0.0" {
		t.Fatalf("环境变量没有覆盖：%+v", cfg)
	}
	t.Setenv("MOEFRP_SINGLE_SESSION", "maybe")
	if _, err := Load(p); err == nil {
		t.Fatal("非法的布尔值应报错")
	}
	if len(EnvNames()) == 0 || EnvNames()[0] != "MOEFRP_LISTEN" {
		t.Fatal("EnvNames 不对")
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]func(c *Config){
		"empty listen":    func(c *Config) { c.Listen = "" },
		"bad proxy":       func(c *Config) { c.TrustedProxies = []string{"localhost"} },
		"short ttl":       func(c *Config) { c.SessionTTL = Duration(time.Minute) },
		"bad level":       func(c *Config) { c.LogLevel = "verbose" },
		"bad version":     func(c *Config) { c.Client.MinVersion = "abc" },
		"min > latest":    func(c *Config) { c.Client.MinVersion, c.Client.LatestVersion = "3.1.0", "3.0.0" },
		"image no url":    func(c *Config) { c.Client.ImageSources = []ImageSource{{Weight: 1}} },
		"api no jsonpath": func(c *Config) { c.Client.ImageSources = []ImageSource{{URL: "u", IsAPI: true}} },
	}
	for name, mutate := range cases {
		c := Default()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s：应校验失败", name)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}
