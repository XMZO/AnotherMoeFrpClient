package model

// 用户角色。
const (
	RoleUser  = "user"
	RoleVIP   = "vip"
	RoleAdmin = "admin"
)

// 用户状态。
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// 分享类型（PLAN 7.6），在创建时显式保存。
const (
	ShareTemplate = "template"
	ShareFull     = "full"
	ShareP2P      = "p2p"
)

// ValidRole 判断角色名是否合法。
func ValidRole(role string) bool {
	switch role {
	case RoleUser, RoleVIP, RoleAdmin:
		return true
	}
	return false
}
