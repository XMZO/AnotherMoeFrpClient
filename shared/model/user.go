package model

// 用户角色。原来还有 VIP，已经去掉（迁移 0006 把 VIP 用户改成了普通用户）。
const (
	RoleUser  = "user"
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
	case RoleUser, RoleAdmin:
		return true
	}
	return false
}
