package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params 是 argon2 的参数。Memory 的单位是 KiB。
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultParams 与旧版 server.py 的 PasswordHasher 完全一致：
// time_cost=3, memory_cost=65536, parallelism=2, hash_len=32, salt_len=16。
// 旧库的哈希因此不需要重新计算，也不会在登录时被重新哈希。
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}

// 解析存量哈希时的上限，防止异常数据让一次校验耗尽内存或 CPU。
const (
	maxMemory  = 1 << 20 // 1 GiB
	maxTime    = 64
	maxKeyLen  = 1024
	maxSaltLen = 1024
)

// ErrBadHash 表示存储的哈希无法解析。
var ErrBadHash = errors.New("auth: malformed password hash")

// HashPassword 用 argon2id 计算 PHC 格式的哈希：
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
//
// salt 和 hash 用不带填充的标准 base64，与 argon2-cffi 一致。
func HashPassword(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return encodeHash("argon2id", p, salt, key), nil
}

func encodeHash(variant string, p Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		variant, argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

type decodedHash struct {
	variant string // argon2id / argon2i
	version int
	params  Params
	salt    []byte
	key     []byte
}

func decodeHash(encoded string) (decodedHash, error) {
	// "", variant, "v=19", "m=..,t=..,p=..", salt, hash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return decodedHash{}, ErrBadHash
	}
	var d decodedHash
	d.variant = parts[1]
	if d.variant != "argon2id" && d.variant != "argon2i" {
		return decodedHash{}, fmt.Errorf("%w: unsupported variant %q", ErrBadHash, d.variant)
	}
	v, ok := strings.CutPrefix(parts[2], "v=")
	if !ok {
		return decodedHash{}, ErrBadHash
	}
	ver, err := strconv.Atoi(v)
	if err != nil {
		return decodedHash{}, ErrBadHash
	}
	if ver != argon2.Version {
		return decodedHash{}, fmt.Errorf("%w: unsupported version %d", ErrBadHash, ver)
	}
	d.version = ver

	var gotM, gotT, gotP bool
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return decodedHash{}, ErrBadHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return decodedHash{}, ErrBadHash
		}
		switch k {
		case "m":
			d.params.Memory, gotM = uint32(n), true
		case "t":
			d.params.Time, gotT = uint32(n), true
		case "p":
			if n == 0 || n > 255 {
				return decodedHash{}, ErrBadHash
			}
			d.params.Threads, gotP = uint8(n), true
		default:
			return decodedHash{}, ErrBadHash
		}
	}
	if !gotM || !gotT || !gotP || d.params.Time == 0 || d.params.Time > maxTime ||
		d.params.Memory > maxMemory || d.params.Memory < 8*uint32(d.params.Threads) {
		return decodedHash{}, ErrBadHash
	}

	if d.salt, err = decodeB64(parts[4]); err != nil || len(d.salt) == 0 || len(d.salt) > maxSaltLen {
		return decodedHash{}, ErrBadHash
	}
	if d.key, err = decodeB64(parts[5]); err != nil || len(d.key) < 4 || len(d.key) > maxKeyLen {
		return decodedHash{}, ErrBadHash
	}
	d.params.SaltLen = uint32(len(d.salt))
	d.params.KeyLen = uint32(len(d.key))
	return d, nil
}

// decodeB64 兼容带填充和不带填充两种写法。
func decodeB64(s string) ([]byte, error) {
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
}

// VerifyPassword 校验密码。needsRehash 表示哈希的算法或参数与 current 不同，
// 校验通过后应该用 current 重新哈希（对应 argon2-cffi 的 check_needs_rehash）。
func VerifyPassword(password, encoded string, current Params) (ok, needsRehash bool, err error) {
	d, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}
	var key []byte
	switch d.variant {
	case "argon2id":
		key = argon2.IDKey([]byte(password), d.salt, d.params.Time, d.params.Memory, d.params.Threads, d.params.KeyLen)
	case "argon2i":
		key = argon2.Key([]byte(password), d.salt, d.params.Time, d.params.Memory, d.params.Threads, d.params.KeyLen)
	}
	if subtle.ConstantTimeCompare(key, d.key) != 1 {
		return false, false, nil
	}
	return true, d.variant != "argon2id" || d.params != current, nil
}
