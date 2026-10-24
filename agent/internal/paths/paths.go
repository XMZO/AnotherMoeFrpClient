// Package paths 列出 agent 在机器上创建的所有文件。卸载时按这里的清单删除，
// 所以新增任何落盘的东西都必须先加到这里（见 docs/node-agent.md 第 7 节）。
package paths

import (
	"os"
	"path/filepath"
)

const (
	ServiceName = "moefrp-agent"
	unitName    = ServiceName + ".service"
)

// Paths 是 agent 用到的路径。测试时用 MOEFRP_AGENT_PREFIX 把它们都放到临时目录下。
type Paths struct {
	Bin      string // /usr/local/bin/moefrp-agent
	ConfDir  string // /etc/moefrp-agent
	StateDir string // /var/lib/moefrp-agent
	Unit     string // /etc/systemd/system/moefrp-agent.service
}

// Default 返回正式路径；设置了 MOEFRP_AGENT_PREFIX 时都放到这个目录下（只用于测试）。
func Default() Paths {
	prefix := os.Getenv("MOEFRP_AGENT_PREFIX")
	j := func(p string) string { return filepath.Join(prefix, filepath.FromSlash(p)) }
	return Paths{
		Bin:      j("/usr/local/bin/" + ServiceName),
		ConfDir:  j("/etc/" + ServiceName),
		StateDir: j("/var/lib/" + ServiceName),
		Unit:     j("/etc/systemd/system/" + unitName),
	}
}

// UnitName 是 systemd 单元名。
func (Paths) UnitName() string { return unitName }

// AgentConfig 保存面板地址和节点令牌（0600）。
func (p Paths) AgentConfig() string { return filepath.Join(p.ConfDir, "agent.toml") }

// FrpsConfig 是当前生效的 frps 配置。
func (p Paths) FrpsConfig() string { return filepath.Join(p.ConfDir, "frps.toml") }

// Access 是官方节点的授权用户表（只有凭证哈希）。
func (p Paths) Access() string { return filepath.Join(p.ConfDir, "access.json") }

// NewBin 是升级时下载的新版本。
func (p Paths) NewBin() string { return filepath.Join(p.StateDir, ServiceName+".new") }

// BackupBin 是升级前的旧版本，新版本确认能用后删除。
func (p Paths) BackupBin() string { return filepath.Join(p.StateDir, ServiceName+".bak") }

// UpgradeState 记录进行中的升级，用于失败回滚。
func (p Paths) UpgradeState() string { return filepath.Join(p.StateDir, "upgrade.json") }

// All 是卸载时要删除的全部内容（目录整个删除）。程序本身放在最后。
func (p Paths) All() []string {
	return []string{p.Unit, p.ConfDir, p.StateDir, p.Bin}
}
