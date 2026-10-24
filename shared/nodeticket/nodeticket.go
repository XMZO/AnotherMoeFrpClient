// Package nodeticket 是官方节点的登录票据（docs/node-agent.md 第 12 节）。
//
// 面板为"用户的某个客户端会话 + 某个节点"签发票据，有效期很短（几分钟到十几分钟），每张只能登录一次。
// 客户端每次登录（包括断线重连）都换一张新的放进 frpc 的 metadatas，节点上的 agent 用面板下发的
// 节点密钥在本地校验，不需要每次问面板。节点密钥由面板主密钥按节点派生，一个节点泄露不影响其他节点。
//
// 格式：mft_<base64url(JSON 声明)>.<base64url(HMAC-SHA256(节点密钥, JSON 声明))>
package nodeticket

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Prefix 是票据的前缀。
const Prefix = "mft_"

// Claims 是票据里的声明。
type Claims struct {
	User    string `json:"u"` // 用户 ID
	Session string `json:"s"` // 客户端会话 ID：会话被撤销后，它的票据和用票据登录的连接都会失效
	Node    string `json:"n"` // 节点 ID
	Gen     int64  `json:"g"` // 用户的节点凭证代数：管理员"重置节点凭证"后旧票据全部失效
	Issued  int64  `json:"i"` // 签发时间（Unix 秒）
	Expires int64  `json:"e"` // 过期时间（Unix 秒）：过期后不能再用来登录
	Nonce   string `json:"r"` // 随机数：节点记住用过的，同一张票据只能登录一次
}

// NodeKey 由面板主密钥派生出某个节点的票据密钥。
func NodeKey(master []byte, nodeID string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("moefrp-node-ticket:" + nodeID))
	return m.Sum(nil)
}

// NewNonce 生成票据随机数。
func NewNonce() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Sign 签发一张票据。
func Sign(key []byte, c Claims) string {
	payload, _ := json.Marshal(c)
	return Prefix + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac(key, payload))
}

// ErrInvalid 表示票据格式不对或签名不对。
var ErrInvalid = errors.New("无效的节点票据")

// Verify 校验签名并解出声明。不检查过期时间和是否用过，由调用方按场景判断。
func Verify(key []byte, ticket string) (Claims, error) {
	rest, ok := strings.CutPrefix(ticket, Prefix)
	if !ok {
		return Claims{}, ErrInvalid
	}
	p, s, ok := strings.Cut(rest, ".")
	if !ok {
		return Claims{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || !hmac.Equal(sig, mac(key, payload)) {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil || c.User == "" || c.Session == "" || c.Node == "" || c.Nonce == "" {
		return Claims{}, ErrInvalid
	}
	return c, nil
}

func mac(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)
}
