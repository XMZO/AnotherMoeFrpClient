package nodes

// 面板主密钥的保存（docs/node-agent.md 第 4 节）。
//
// 主密钥（32 字节）派生出所有节点的票据密钥和存储密钥，是整个托管体系的根。数据库里存的是用
// "密钥加密密钥"（KEK）加密后的密文（AES-256-GCM），KEK 不在数据库里：来自配置 secret_key /
// 环境变量 MOEFRP_SECRET_KEY，或者 secret.key 文件（默认在数据库旁边，第一次启动时生成，0600）。
// 这样数据库备份、迁移前的快照、管理后台下载的备份泄露出去，也拿不到主密钥。
//
// 指纹是 HMAC(主密钥, 固定串) 的前 10 字节，按 base32 分组显示（例如 K7QF-2M9X-AB3D-PQ7Z），
// 不暴露主密钥本身的任何哈希；另有版本号，每轮换一次加一，一眼能看出换没换。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sealedPrefix = "v2:" // 加密保存的主密钥；没有这个前缀的是旧版明文（base64），读到后自动改成加密保存
	sealAAD      = "moefrp-node-cred-key"
)

// ErrKeyUnreadable 表示库里的主密钥解不开（KEK 丢了或换了）。只能轮换一个新的。
var ErrKeyUnreadable = errors.New("面板主密钥解不开：加密它的 secret.key（或 MOEFRP_SECRET_KEY）丢失或被换过。找回原来的文件后重启面板；找不回只能轮换密钥（所有节点会自动换用新密钥）")

// LoadKEK 读取加密主密钥用的 KEK：inline（base64）优先，否则读 file，file 不存在时生成。
func LoadKEK(inline, file string) ([]byte, string, error) {
	if inline != "" {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(inline))
		if err != nil || len(k) != 32 {
			return nil, "", errors.New("secret_key 要是 base64 编码的 32 字节（例如 openssl rand -base64 32）")
		}
		return k, "MOEFRP_SECRET_KEY", nil
	}
	b, err := os.ReadFile(file)
	if err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, "", fmt.Errorf("%s 内容不对：要是 base64 编码的 32 字节", file)
		}
		return k, file, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, "", err
	}
	// O_EXCL：两个进程同时启动时不互相覆盖
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(k) + "\n"); err != nil {
		f.Close()
		return nil, "", err
	}
	if err := f.Close(); err != nil {
		return nil, "", err
	}
	return k, file, nil
}

func sealKey(kek, key []byte) (string, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, key, []byte(sealAAD))
	return sealedPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// openKey 解出库里保存的主密钥。legacy 表示是旧版明文，调用方应改成加密保存。
func openKey(kek []byte, v string) (key []byte, legacy bool, err error) {
	rest, sealed := strings.CutPrefix(v, sealedPrefix)
	if !sealed {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(key) < 32 {
			return nil, false, errors.New("nodes: bad node_cred_key setting")
		}
		return key, true, nil
	}
	b, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return nil, false, ErrKeyUnreadable
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, false, err
	}
	if len(b) < gcm.NonceSize() {
		return nil, false, ErrKeyUnreadable
	}
	key, err = gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte(sealAAD))
	if err != nil {
		return nil, false, ErrKeyUnreadable
	}
	return key, false, nil
}

func newGCM(kek []byte) (cipher.AEAD, error) {
	if len(kek) != 32 {
		return nil, errors.New("nodes: KEK must be 32 bytes")
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// fingerprint 是主密钥的指纹，例如 K7QF-2M9X-AB3D-PQ7Z。
func fingerprint(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("moefrp-key-fingerprint"))
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(m.Sum(nil)[:10])
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}
