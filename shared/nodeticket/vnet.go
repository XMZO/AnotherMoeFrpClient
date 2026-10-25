package nodeticket

import (
	"errors"
	"strconv"
	"strings"
)

// 联机房间的中继票据（docs/vnet.md）：同一个节点密钥签名，Session 固定是 "vnet"，User 是 "vnet:<房间>:<座位号>"。
// User 不是任何用户 ID，所以拿它登录 frps 会被准入插件拒绝（找不到用户）。

const vnetSession = "vnet"

// SignVnet 签一张联机中继票据。
func SignVnet(key []byte, node, room string, slot int, issued, expires int64) string {
	return Sign(key, Claims{User: "vnet:" + room + ":" + strconv.Itoa(slot), Session: vnetSession, Node: node,
		Issued: issued, Expires: expires, Nonce: NewNonce()})
}

// VerifyVnet 校验联机中继票据，返回房间和座位号。
func VerifyVnet(key []byte, node, ticket string, now int64) (room string, slot int, err error) {
	c, err := Verify(key, ticket)
	if err != nil {
		return "", 0, err
	}
	if c.Session != vnetSession || c.Node != node || c.Expires < now {
		return "", 0, errors.New("ticket not for this relay or expired")
	}
	rest, ok := strings.CutPrefix(c.User, "vnet:")
	i := strings.LastIndexByte(rest, ':')
	if !ok || i <= 0 {
		return "", 0, errors.New("bad vnet ticket")
	}
	slot, err = strconv.Atoi(rest[i+1:])
	if err != nil || slot < 1 || slot > 250 {
		return "", 0, errors.New("bad vnet ticket")
	}
	return rest[:i], slot, nil
}
