package sysinfo

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"moefrp/agent/internal/agentcfg"
)

// MachineID 返回机器指纹：/etc/machine-id（没有时 /var/lib/dbus/machine-id）加盐哈希，不上报原值。
// 都读不到时（容器、测试环境）在 fallback 文件里生成一个随机值并保存，之后一直用它。
func MachineID(fallback string) string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return hashID(id)
			}
		}
	}
	if b, err := os.ReadFile(fallback); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return hashID(id)
		}
	}
	r := make([]byte, 16)
	_, _ = rand.Read(r)
	id := hex.EncodeToString(r)
	_ = agentcfg.WriteFileAtomic(fallback, []byte(id+"\n"), 0o600)
	return hashID(id)
}

func hashID(id string) string {
	sum := sha256.Sum256([]byte("moefrp-node-machine:" + id))
	return hex.EncodeToString(sum[:16])
}
