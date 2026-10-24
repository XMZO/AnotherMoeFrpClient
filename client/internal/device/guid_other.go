//go:build !windows

package device

import (
	"os"
	"strings"
)

// machineGUID 在非 Windows 上读 /etc/machine-id（只用于开发和测试），读不到时返回空串。
func machineGUID() string {
	b, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
