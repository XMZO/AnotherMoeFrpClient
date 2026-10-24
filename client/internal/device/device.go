// Package device 算出这台电脑的设备指纹（docs/node-agent.md 第 12 节）：
// 登录时报给面板记在会话上，领官方节点票据时要对得上，frpc 登录节点时也要带上，节点核对票据里的设备绑定。
// 登录凭据或票据被拷到别的电脑上就用不了。
//
// 指纹 = SHA-256(机器 GUID) 的前 16 字节（十六进制），不上报原值。机器 GUID 是 Windows 安装时生成的
// （HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid），重装系统才会变。读不到时退回到客户端的安装 ID。
// 指纹由客户端自报，只能提高门槛，挡不住改过客户端的人。
package device

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

var (
	once sync.Once
	id   string
)

// ID 返回设备指纹（32 位十六进制）。fallback 是读不到机器 GUID 时用的值（客户端的安装 ID）。
func ID(fallback string) string {
	once.Do(func() {
		src := machineGUID()
		if src == "" {
			src = "install:" + fallback
		}
		sum := sha256.Sum256([]byte("moefrp-device:" + strings.ToLower(strings.TrimSpace(src))))
		id = hex.EncodeToString(sum[:16])
	})
	return id
}
