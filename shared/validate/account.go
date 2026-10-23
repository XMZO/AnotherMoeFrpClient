package validate

import (
	"crypto/rand"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 账号相关的长度限制（PLAN 7.4）。
const (
	PasswordMinLen = 8
	PasswordMaxLen = 128
)

var nicknameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`)

// ReservedNicknames 是不允许注册的昵称（不区分大小写）。
// 旧版只保留了 all；这里按 PLAN 7.4 补上 admin 等。
var ReservedNicknames = []string{
	"all", "admin", "administrator", "root", "system", "moefrp",
	"null", "undefined", "anonymous", "everyone",
}

// Nickname 校验注册用的昵称：^[a-zA-Z0-9_]{3,20}$，且不是保留词。
func Nickname(s string) error {
	var c collector
	switch {
	case s == "":
		c.add("nickname", ReasonRequired, "昵称不能为空")
	case !nicknameRe.MatchString(s):
		c.add("nickname", ReasonInvalid, "昵称只能是 3–20 位的字母、数字和下划线")
	case IsReservedNickname(s):
		c.add("nickname", ReasonReserved, "昵称 %q 是保留词", s)
	}
	return c.err()
}

// IsNicknameFormat 只检查昵称的格式（不检查保留词）。旧版注册时用的是同一个正则，
// 所以任何已存在的账号都满足它；登录时据此直接拒绝不可能存在的昵称。
func IsNicknameFormat(s string) bool { return nicknameRe.MatchString(s) }

// IsReservedNickname 判断昵称是否是保留词。
func IsReservedNickname(s string) bool {
	for _, r := range ReservedNicknames {
		if strings.EqualFold(s, r) {
			return true
		}
	}
	return false
}

// Password 校验密码长度：8–128 个字符。field 是出错时报告的字段名。
func Password(field, s string) error {
	var c collector
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0:
		c.add(field, ReasonRequired, "密码不能为空")
	case n < PasswordMinLen:
		c.add(field, ReasonTooShort, "密码至少 %d 位", PasswordMinLen)
	case n > PasswordMaxLen:
		c.add(field, ReasonTooLong, "密码最多 %d 位", PasswordMaxLen)
	}
	return c.err()
}

// ---- 邀请码 ----

// InviteCharset 是邀请码使用的字符集：去掉 I、L、O 的大写字母加 2–9，共 31 个字符。
// 与旧版 server.py / generate_invite_code.py 的 CUSTOM_CHARSET 相同。
const InviteCharset = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// InvitePrefix 是邀请码的固定前缀。
const InvitePrefix = "FRPT-"

var errInviteBody = errors.New("validate: invite body must be 7 characters from InviteCharset")

// NormalizeInviteCode 去掉首尾空白并转成大写，与旧版 strip().upper() 一致。
func NormalizeInviteCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// InviteCode 校验邀请码的格式和校验位，调用前应先 NormalizeInviteCode。
// 格式为 FRPT-XXXX-XXXX，前 7 个字符是正文，最后一个字符是校验位。
func InviteCode(code string) error {
	var c collector
	if code == "" {
		c.add("inviteCode", ReasonRequired, "邀请码不能为空")
		return c.err()
	}
	body, ok := inviteBody(code)
	if !ok {
		c.add("inviteCode", ReasonInvalid, "邀请码格式不正确")
		return c.err()
	}
	check, _ := InviteCheckChar(body[:7])
	if body[7] != check {
		c.add("inviteCode", ReasonChecksum, "邀请码校验失败，请检查是否输入错误")
	}
	return c.err()
}

// inviteBody 检查 FRPT-XXXX-XXXX 的形状并返回去掉前缀和连字符的 8 个字符。
func inviteBody(code string) (string, bool) {
	if len(code) != len("FRPT-XXXX-XXXX") || !strings.HasPrefix(code, InvitePrefix) || code[9] != '-' {
		return "", false
	}
	body := code[5:9] + code[10:]
	for i := 0; i < len(body); i++ {
		if strings.IndexByte(InviteCharset, body[i]) < 0 {
			return "", false
		}
	}
	return body, true
}

// InviteCheckChar 计算 7 个字符正文的校验位（旧版的 Luhn mod 31 变体）。
func InviteCheckChar(body string) (byte, error) {
	if len(body) != 7 {
		return 0, errInviteBody
	}
	n := len(InviteCharset)
	parity := len(body) % 2
	sum := 0
	for i := 0; i < len(body); i++ {
		d := strings.IndexByte(InviteCharset, body[i])
		if d < 0 {
			return 0, errInviteBody
		}
		if i%2 == parity {
			d *= 2
			if d >= n {
				d = d%n + d/n
			}
		}
		sum += d
	}
	return InviteCharset[(sum*9)%n], nil
}

// MakeInviteCode 用 7 个字符的正文拼出完整邀请码。
func MakeInviteCode(body string) (string, error) {
	check, err := InviteCheckChar(body)
	if err != nil {
		return "", err
	}
	full := body + string(check)
	return InvitePrefix + full[:4] + "-" + full[4:], nil
}

// NewInviteCode 用 crypto/rand 生成一个新的邀请码。
func NewInviteCode() string {
	code, err := MakeInviteCode(RandomString(InviteCharset, 7))
	if err != nil {
		panic(err) // RandomString 只会产生字符集内的字符
	}
	return code
}

// RandomString 从 charset 里均匀随机地取 n 个字符（拒绝采样，无取模偏差）。
func RandomString(charset string, n int) string {
	if len(charset) == 0 || len(charset) > 256 {
		panic("validate: charset size must be 1..256")
	}
	limit := 256 - 256%len(charset)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic("validate: crypto/rand failed: " + err.Error())
		}
		for _, b := range buf {
			if int(b) < limit {
				out = append(out, charset[int(b)%len(charset)])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out)
}
