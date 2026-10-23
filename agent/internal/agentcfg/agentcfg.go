// Package agentcfg 读写 /etc/moefrp-agent/agent.toml：面板地址、节点 ID、令牌、是否允许执行命令。
package agentcfg

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config 是 agent 的配置。令牌只存在这里（0600，root），不出现在命令行参数和 systemd 单元里。
type Config struct {
	Server string `toml:"server"`  // 面板地址，例如 https://frp.example.com
	NodeID string `toml:"node_id"` // 面板分配的节点 ID
	Token  string `toml:"token"`   // 长期节点令牌
	// Exec 为 false 时拒绝面板的执行命令请求。安装时用 --no-exec 设置，面板改不了。
	Exec bool `toml:"exec"`
}

// Load 读取配置。
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s：%w", path, err)
	}
	return c, c.Validate()
}

// Validate 检查必填项。
func (c Config) Validate() error {
	if _, err := NormalizeServer(c.Server); err != nil {
		return err
	}
	if c.NodeID == "" || c.Token == "" {
		return errors.New("agent.toml 缺少 node_id 或 token，请重新安装")
	}
	return nil
}

// Save 原子写入，权限 0600。
func Save(path string, c Config) error {
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	head := "# MoeFrp 节点 agent 配置。token 是这台节点连接面板的凭证，不要泄露。\n"
	return WriteFileAtomic(path, append([]byte(head), b...), 0o600)
}

// NormalizeServer 检查并规范面板地址：必须是 http(s)，去掉末尾的 /。
func NormalizeServer(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("面板地址 %q 不正确，应类似 https://frp.example.com", s)
	}
	return s, nil
}

// WriteFileAtomic 先写临时文件再改名，避免写到一半断电留下坏文件。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
