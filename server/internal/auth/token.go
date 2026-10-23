package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"moefrp/shared/api"
	"moefrp/shared/validate"
)

// tokenBytes 是会话令牌的随机部分长度（PLAN 7.4：32 字节）。
const tokenBytes = 32

// NewToken 生成会话令牌：mfs_ + base64url(32 字节随机数)。返回令牌和它的 SHA-256。
func NewToken() (token string, hash []byte) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	token = api.TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// HashToken 计算令牌的 SHA-256，数据库里只存这个值。
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ValidTokenFormat 粗略检查令牌格式，格式不对的直接拒绝，不查库。
func ValidTokenFormat(token string) bool {
	rest, ok := strings.CutPrefix(token, api.TokenPrefix)
	if !ok || len(rest) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil
}

// 重置码：RST-XXXXX-XXXXX-XXXXX，字符集同邀请码（去掉易混淆的 I、L、O、0、1），
// 15 个字符约 74 位熵，24 小时有效，库里只存哈希。
const (
	resetCodePrefix = "RST-"
	resetGroups     = 3
	resetGroupLen   = 5
)

// NewResetCode 生成一个重置码。
func NewResetCode() string {
	raw := validate.RandomString(validate.InviteCharset, resetGroups*resetGroupLen)
	var b strings.Builder
	b.WriteString(resetCodePrefix)
	for i := 0; i < resetGroups; i++ {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(raw[i*resetGroupLen : (i+1)*resetGroupLen])
	}
	return b.String()
}

// NormalizeResetCode 去掉首尾空白并转大写，用户手抄时大小写不敏感。
func NormalizeResetCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// HashResetCode 计算重置码的 SHA-256。
func HashResetCode(code string) []byte {
	sum := sha256.Sum256([]byte("moefrp-reset:" + NormalizeResetCode(code)))
	return sum[:]
}
