package services

import (
	"context"
	"strings"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/cloudsync"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/validate"
)

// AuthService：登录、注册、退出、改密码、重置密码、当前用户和同步状态（PLAN 6.4）。
// 网络请求可能要几秒，前端调用本身是异步的，不会卡界面。
type AuthService struct {
	sync     *cloudsync.Manager
	profiles *store.ProfileStore
	eng      *engine.Engine
}

func NewAuthService(sync *cloudsync.Manager, profiles *store.ProfileStore, eng *engine.Engine) *AuthService {
	return &AuthService{sync: sync, profiles: profiles, eng: eng}
}

// Login 登录。keepLoggedIn 为 true 时令牌存进 Windows 凭据管理器（不保存密码），下次启动自动登录。
func (s *AuthService) Login(nickname, password string, keepLoggedIn bool) (cloudsync.AccountState, error) {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" || password == "" {
		return s.sync.Account(), apperr.New(apperr.InvalidArg, "nickname and password are required")
	}
	return s.sync.Login(context.Background(), nickname, password, keepLoggedIn)
}

// Register 注册。先在本地按服务端同样的规则校验昵称、密码、邀请码（含校验位），通过后再请求服务端。
// 注册成功不会自动登录。
func (s *AuthService) Register(nickname, password, inviteCode string) (api.UserInfo, error) {
	nickname = strings.TrimSpace(nickname)
	inviteCode = validate.NormalizeInviteCode(inviteCode)
	if err := joinValidate(validate.Nickname(nickname), validate.Password("password", password), validate.InviteCode(inviteCode)); err != nil {
		return api.UserInfo{}, apperr.FromValidate(err)
	}
	return s.sync.Register(context.Background(), nickname, password, inviteCode)
}

// ResetPassword 用管理员在服务器上生成的重置码设置新密码。
func (s *AuthService) ResetPassword(nickname, code, newPassword string) error {
	if err := validate.Password("newPassword", newPassword); err != nil {
		return apperr.FromValidate(err)
	}
	return s.sync.ResetPassword(context.Background(), strings.TrimSpace(nickname), strings.TrimSpace(code), newPassword)
}

// ChangePassword 修改密码。旧密码错误返回 invalid_credentials，不会下线。
func (s *AuthService) ChangePassword(oldPassword, newPassword string) error {
	if err := validate.Password("newPassword", newPassword); err != nil {
		return apperr.FromValidate(err)
	}
	return s.sync.ChangePassword(context.Background(), oldPassword, newPassword)
}

// LogoutInfo 是退出登录前给用户确认的信息。
type LogoutInfo struct {
	Pending      int    `json:"pending"`      // 还没推送到云端的修改（退出后会丢失）
	RunningCloud string `json:"runningCloud"` // 正在运行的云端配置名（退出会停止隧道），没有为空
}

// LogoutInfo 返回退出登录会造成的影响。
func (s *AuthService) LogoutInfo() LogoutInfo {
	var info LogoutInfo
	runID, _, running := s.eng.RunningProfile()
	for _, c := range s.profiles.All() {
		if !c.IsCloud() {
			continue
		}
		if c.Dirty {
			info.Pending++
		}
		if running && c.Profile.ID == runID && !c.Deleted {
			info.RunningCloud = c.Profile.Name
		}
	}
	return info
}

// Logout 退出登录：清掉本机的云端配置缓存，本地配置保留，下次启动不会自动登录。
func (s *AuthService) Logout() error {
	return s.sync.Logout(context.Background())
}

// Me 向服务端刷新当前用户。
func (s *AuthService) Me() (cloudsync.AccountState, error) {
	return s.sync.Me(context.Background())
}

// Account 返回当前登录状态（不发请求）。
func (s *AuthService) Account() cloudsync.AccountState {
	return s.sync.Account()
}

// SyncStatus 返回同步状态（不发请求）。
func (s *AuthService) SyncStatus() cloudsync.Status {
	return s.sync.Status()
}

// SyncNow 立即同步一次。
func (s *AuthService) SyncNow() {
	s.sync.SyncNow()
}

func joinValidate(errs ...error) error {
	var all validate.Errors
	for _, err := range errs {
		if fe, ok := err.(validate.Errors); ok {
			all = append(all, fe...)
		} else if err != nil {
			return err
		}
	}
	if len(all) == 0 {
		return nil
	}
	return all
}
