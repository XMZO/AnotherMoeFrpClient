package nodes

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DistFile 是一个打包的 agent 程序。
type DistFile struct {
	Name   string // moefrp-agent-linux-amd64
	Path   string
	SHA256 string
	Size   int64
}

// Dist 是面板打包的 agent 程序（服务端镜像里的 /usr/share/moefrp/agent）。
type Dist struct {
	dir     string
	version string
	files   map[string]DistFile // 键是架构：amd64、arm64
}

// Arches 是支持的架构。
var Arches = []string{"amd64", "arm64"}

// LoadDist 读取目录里的 agent 程序并计算哈希。目录不存在或一个程序都没有时返回 nil。
// 版本号来自目录里的 VERSION 文件，没有时用 fallback（服务端自己的版本）。
func LoadDist(dir, fallback string) (*Dist, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	d := &Dist{dir: dir, version: fallback, files: map[string]DistFile{}}
	if b, err := os.ReadFile(filepath.Join(dir, "VERSION")); err == nil && strings.TrimSpace(string(b)) != "" {
		d.version = strings.TrimSpace(string(b))
	}
	for _, arch := range Arches {
		name := "moefrp-agent-linux-" + arch
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		d.files[arch] = DistFile{Name: "linux-" + arch, Path: path, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
	}
	if len(d.files) == 0 {
		return nil, nil
	}
	return d, nil
}

// Version 是打包的 agent 版本。
func (d *Dist) Version() string { return d.version }

// Has 判断有没有某个架构的程序。
func (d *Dist) Has(arch string) bool {
	if d == nil {
		return false
	}
	_, ok := d.files[arch]
	return ok
}

// File 返回某个架构的程序。
func (d *Dist) File(arch string) DistFile { return d.files[arch] }

// ByName 按下载名（linux-amd64）查找。
func (d *Dist) ByName(name string) (DistFile, bool) {
	if d == nil {
		return DistFile{}, false
	}
	arch, ok := strings.CutPrefix(name, "linux-")
	if !ok {
		return DistFile{}, false
	}
	f, ok := d.files[arch]
	return f, ok
}

// Arches 返回已打包的架构。
func (d *Dist) ArchList() []string {
	var out []string
	for _, a := range Arches {
		if d.Has(a) {
			out = append(out, a)
		}
	}
	return out
}

//go:embed install.sh
var installScript string

// InstallScript 返回写好面板地址的安装脚本。server 必须已经校验过（只含 URL 安全字符）。
func InstallScript(server string) string {
	return strings.ReplaceAll(installScript, "__SERVER__", server)
}
