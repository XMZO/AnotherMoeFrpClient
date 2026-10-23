// Package auth 实现注册、登录、会话、改密码和重置密码（PLAN 7.4）。
//
// 日志和审计里任何时候都不出现密码、令牌和重置码。
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"moefrp/server/internal/store"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// Options 是认证服务的可调参数，零值字段使用默认值。
type Options struct {
	SingleSession bool          // 单设备模式：新登录吊销该用户的其他会话
	SessionTTL    time.Duration // 滑动过期，默认 30 天
	Params        Params        // 新哈希使用的 argon2 参数，默认 DefaultParams
	Lockout       LockoutPolicy // 默认 DefaultLockout
	ResetCodeTTL  time.Duration // 默认 24 小时
	TouchInterval time.Duration // last_seen_at 最多多久写一次库，默认 10 分钟
	MaxHashes     int           // 同时进行的 argon2 计算上限（每个约 64MB 内存），默认 4
	Now           func() time.Time
	Logger        *slog.Logger
}

func (o *Options) setDefaults() {
	if o.SessionTTL == 0 {
		o.SessionTTL = 30 * 24 * time.Hour
	}
	if o.Params == (Params{}) {
		o.Params = DefaultParams
	}
	if o.Lockout == (LockoutPolicy{}) {
		o.Lockout = DefaultLockout
	}
	if o.ResetCodeTTL == 0 {
		o.ResetCodeTTL = 24 * time.Hour
	}
	if o.TouchInterval == 0 {
		o.TouchInterval = 10 * time.Minute
	}
	if o.MaxHashes <= 0 {
		o.MaxHashes = 4
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Service 是认证服务。
type Service struct {
	st        *store.Store
	opt       Options
	lockout   *Lockout
	dummyHash string
	hashSem   chan struct{}

	// afterVerify 只在测试里设置：密码校验通过之后、写事务开始之前调用，用来制造竞态。
	afterVerify func()
}

// New 创建认证服务。会预先计算一个假哈希，供不存在的用户登录时使用。
func New(st *store.Store, opt Options) (*Service, error) {
	opt.setDefaults()
	dummy, err := HashPassword("moefrp-dummy-password", opt.Params)
	if err != nil {
		return nil, err
	}
	return &Service{
		st:        st,
		opt:       opt,
		lockout:   NewLockout(opt.Lockout),
		dummyHash: dummy,
		hashSem:   make(chan struct{}, opt.MaxHashes),
	}, nil
}

// Lockout 返回锁定表（测试和后台清理使用）。
func (s *Service) Lockout() *Lockout { return s.lockout }

// SingleSession 返回是否开启了单设备模式。
func (s *Service) SingleSession() bool { return s.opt.SingleSession }

// ClientMeta 是请求方的信息，用于会话和审计。
type ClientMeta struct {
	IP         string
	InstallID  string
	DeviceName string
}

// Principal 是通过认证的请求方。
type Principal struct {
	User    store.User
	Session store.Session
}

// LoginResult 是登录成功的结果。Token 只在这里出现一次。
type LoginResult struct {
	Token   string
	Session store.Session
	User    store.User
}

func (s *Service) now() time.Time { return s.opt.Now() }

// withHash 限制同时进行的 argon2 计算数量。
func (s *Service) withHash(ctx context.Context, fn func() error) error {
	select {
	case s.hashSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.hashSem }()
	return fn()
}

func (s *Service) hash(ctx context.Context, password string) (string, error) {
	var h string
	err := s.withHash(ctx, func() (err error) {
		h, err = HashPassword(password, s.opt.Params)
		return err
	})
	return h, err
}

func (s *Service) verify(ctx context.Context, password, encoded string) (ok, rehash bool, err error) {
	err = s.withHash(ctx, func() error {
		ok, rehash, err = VerifyPassword(password, encoded, s.opt.Params)
		return err
	})
	return ok, rehash, err
}

func (s *Service) audit(ctx context.Context, q store.Querier, e store.AuditEntry) error {
	if e.At == 0 {
		e.At = s.now().Unix()
	}
	return store.InsertAudit(ctx, q, e)
}

// auditBestEffort 在事务之外写审计，失败只记日志。
func (s *Service) auditBestEffort(ctx context.Context, e store.AuditEntry) {
	if err := s.audit(ctx, s.st.DB(), e); err != nil {
		s.opt.Logger.ErrorContext(ctx, "write audit log", "action", e.Action, "err", err)
	}
}

func validationError(err error) error {
	var es validate.Errors
	if errors.As(err, &es) {
		return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, es)
	}
	return err
}

func lockedError(d time.Duration) error {
	secs := int64((d + time.Second - 1) / time.Second)
	return apierr.New(apierr.AccountLocked).With(apierr.DetailRetryAfter, secs)
}

// ---- 注册 ----

// Register 注册新用户。邀请码在创建用户的同一个事务里原子地占用（PLAN 7.4）。
// 注册不会自动登录。
func (s *Service) Register(ctx context.Context, nickname, password, inviteCode string, meta ClientMeta) (store.User, error) {
	nickname = strings.TrimSpace(nickname)
	inviteCode = validate.NormalizeInviteCode(inviteCode)

	var c validate.Errors
	for _, err := range []error{validate.Nickname(nickname), validate.Password("password", password)} {
		var es validate.Errors
		if errors.As(err, &es) {
			c = append(c, es...)
		}
	}
	if len(c) > 0 {
		return store.User{}, validationError(c)
	}
	if err := validate.InviteCode(inviteCode); err != nil {
		var es validate.Errors
		errors.As(err, &es)
		return store.User{}, apierr.New(apierr.InviteInvalid).With(apierr.DetailFields, es)
	}

	// 哈希很慢（约 64MB 内存），放在事务外面算，避免长时间持有写锁。
	hash, err := s.hash(ctx, password)
	if err != nil {
		return store.User{}, err
	}

	now := s.now().Unix()
	u := store.User{
		ID:           model.NewID(),
		Nickname:     nickname,
		PasswordHash: hash,
		Role:         model.RoleUser,
		Status:       model.StatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		// 先看邀请码，再看昵称：没有有效邀请码的人不能借注册接口探测昵称是否存在。
		if err := inviteUsable(ctx, tx, inviteCode); err != nil {
			return err
		}
		if _, err := store.GetUserByNickname(ctx, tx, nickname); err == nil {
			return apierr.New(apierr.NicknameTaken)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.InsertUser(ctx, tx, u); err != nil {
			if store.IsUniqueViolation(err) {
				return apierr.New(apierr.NicknameTaken)
			}
			return err
		}
		ok, err := store.ClaimInvite(ctx, tx, inviteCode, u.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			// 并发注册时，另一个事务先占用了这个邀请码
			return apierr.New(apierr.InviteUsed)
		}
		return s.audit(ctx, tx, store.AuditEntry{
			At: now, ActorID: u.ID, Action: store.AuditRegister, Target: u.Nickname, IP: meta.IP,
			Detail: map[string]any{"invite": inviteCode},
		})
	})
	if err != nil {
		return store.User{}, err
	}
	return u, nil
}

func inviteUsable(ctx context.Context, q store.Querier, code string) error {
	inv, err := store.GetInvite(ctx, q, code)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apierr.New(apierr.InviteInvalid)
	case err != nil:
		return err
	case inv.RevokedAt != nil:
		return apierr.New(apierr.InviteInvalid)
	case inv.UsedAt != nil || inv.UsedBy != "":
		return apierr.New(apierr.InviteUsed)
	}
	return nil
}

// ---- 登录 ----

// Login 校验昵称和密码，成功后创建会话。
//
//   - 用户不存在时也做一次假的哈希计算，响应时间与密码错误相同。
//   - 按昵称失败锁定（不区分昵称是否存在）。
//   - 单设备模式下吊销该用户的其他会话，原因记为 replaced。
//   - 旧哈希的参数与当前参数不同时，登录成功后自动重新哈希。
func (s *Service) Login(ctx context.Context, nickname, password string, meta ClientMeta) (LoginResult, error) {
	nickname = strings.TrimSpace(nickname)
	var c validate.Errors
	if nickname == "" {
		c = append(c, validate.FieldError{Field: "nickname", Reason: validate.ReasonRequired, Message: "昵称不能为空"})
	}
	if password == "" {
		c = append(c, validate.FieldError{Field: "password", Reason: validate.ReasonRequired, Message: "密码不能为空"})
	}
	if len(meta.DeviceName) > 0 {
		if err := checkDeviceName(meta.DeviceName); err != nil {
			c = append(c, *err)
		}
	}
	if len(c) > 0 {
		return LoginResult{}, validationError(c)
	}
	// 登录不校验密码长度：旧库里可能有短于 8 位的密码。过长的输入由请求体上限兜住。

	now := s.now()
	if !validate.IsNicknameFormat(nickname) {
		// 不可能存在的昵称（旧版注册用的是同一个正则）：照样做一次假哈希，但不进锁定表，
		// 否则任意长度的昵称会把内存里的锁定表撑爆。
		_, _, _ = s.verify(ctx, password, s.dummyHash)
		s.auditBestEffort(ctx, store.AuditEntry{At: now.Unix(), Action: store.AuditLoginFailed, IP: meta.IP,
			Detail: map[string]any{"reason": "unknown_user"}})
		return LoginResult{}, apierr.New(apierr.InvalidCredentials)
	}

	// 先预占一次尝试，再校验密码：检查和计数是原子的，并发请求绕不过失败上限。
	att, denial, wait := s.lockout.Begin(nickname, now)
	switch denial {
	case Locked:
		return LoginResult{}, lockedError(wait)
	case Busy:
		return LoginResult{}, busyError()
	}
	defer att.Cancel() // 中途出错时释放预占；已经 Succeed / Fail 的是空操作

	u, err := store.GetUserByNickname(ctx, s.st.DB(), nickname)
	if errors.Is(err, store.ErrNotFound) {
		// 假哈希：让"用户不存在"和"密码错误"耗时相同
		_, _, _ = s.verify(ctx, password, s.dummyHash)
		return LoginResult{}, s.loginFailed(ctx, att, nickname, "", meta, now)
	}
	if err != nil {
		return LoginResult{}, err
	}

	ok, rehash, err := s.verify(ctx, password, u.PasswordHash)
	if err != nil {
		s.opt.Logger.ErrorContext(ctx, "verify password hash", "user_id", u.ID, "err", err)
		return LoginResult{}, fmt.Errorf("auth: verify password: %w", err)
	}
	if !ok {
		return LoginResult{}, s.loginFailed(ctx, att, nickname, u.ID, meta, now)
	}
	att.Succeed()
	if s.afterVerify != nil {
		s.afterVerify()
	}

	if u.Status != model.StatusActive {
		s.auditBestEffort(ctx, store.AuditEntry{
			ActorID: u.ID, Action: store.AuditLoginFailed, Target: u.Nickname, IP: meta.IP,
			Detail: map[string]any{"reason": "disabled"},
		})
		return LoginResult{}, apierr.New(apierr.AccountDisabled)
	}

	var newHash string
	if rehash {
		if newHash, err = s.hash(ctx, password); err != nil {
			return LoginResult{}, err
		}
	}

	token, tokenHash := NewToken()
	sess := store.Session{
		ID:         model.NewID(),
		UserID:     u.ID,
		TokenHash:  tokenHash,
		InstallID:  meta.InstallID,
		DeviceName: meta.DeviceName,
		CreatedAt:  now.Unix(),
		LastSeenAt: now.Unix(),
		ExpiresAt:  now.Add(s.opt.SessionTTL).Unix(),
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		// 校验密码是在事务外做的。期间如果密码被重置或修改、账号被停用，就不能再发令牌：
		// 否则重置密码吊销会话之后，用旧密码换来的会话会在它之后插进来。
		cur, err := store.GetUser(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if cur.PasswordHash != u.PasswordHash {
			return apierr.New(apierr.InvalidCredentials)
		}
		if cur.Status != model.StatusActive {
			return apierr.New(apierr.AccountDisabled)
		}
		u = cur

		detail := map[string]any{}
		if newHash != "" {
			if err := store.UpdatePasswordHash(ctx, tx, u.ID, newHash, now.Unix()); err != nil {
				return err
			}
			u.PasswordHash = newHash
			detail["rehashed"] = true
		}
		if s.opt.SingleSession {
			n, err := store.RevokeUserSessions(ctx, tx, u.ID, "", store.RevokeReplaced, now.Unix())
			if err != nil {
				return err
			}
			if n > 0 {
				detail["replaced"] = n
			}
		}
		if err := store.InsertSession(ctx, tx, sess); err != nil {
			return err
		}
		detail["session"] = sess.ID
		if meta.InstallID != "" {
			detail["installId"] = meta.InstallID
		}
		return s.audit(ctx, tx, store.AuditEntry{
			At: now.Unix(), ActorID: u.ID, Action: store.AuditLogin, Target: u.Nickname, IP: meta.IP, Detail: detail,
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, Session: sess, User: u}, nil
}

// busyError：同一个昵称已有足够多的尝试在进行中，让客户端稍后再试。
func busyError() error {
	return apierr.New(apierr.RateLimited).With(apierr.DetailRetryAfter, int64(1))
}

// loginFailed 记录失败、写审计并返回给客户端的错误。
// 不存在的昵称不写进审计的 target：用户有时会把密码误填进昵称框。
func (s *Service) loginFailed(ctx context.Context, att *Attempt, nickname, userID string, meta ClientMeta, now time.Time) error {
	d, locked := att.Fail(now)
	e := store.AuditEntry{At: now.Unix(), ActorID: userID, Action: store.AuditLoginFailed, IP: meta.IP}
	if userID != "" {
		e.Target = nickname
		e.Detail = map[string]any{"reason": "bad_password"}
	} else {
		e.Detail = map[string]any{"reason": "unknown_user"}
	}
	if locked {
		e.Detail["locked"] = int64(d / time.Second)
	}
	s.auditBestEffort(ctx, e)
	if locked {
		return lockedError(d)
	}
	return apierr.New(apierr.InvalidCredentials)
}

func checkDeviceName(name string) *validate.FieldError {
	if err := validate.ProfileName(name); err != nil {
		var es validate.Errors
		if errors.As(err, &es) && len(es) > 0 {
			fe := es[0]
			fe.Field = "deviceName"
			return &fe
		}
	}
	return nil
}

// ---- 会话 ----

// Authenticate 校验 Authorization 头里的令牌。
//
// 已吊销的会话按原因区分：被挤下线返回 session_replaced；退出登录返回 unauthorized；
// 改密码、重置密码、管理员踢下线以及过期返回 session_expired。
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if !ValidTokenFormat(token) {
		return Principal{}, apierr.New(apierr.Unauthorized)
	}
	r, err := store.GetSessionByTokenHash(ctx, s.st.DB(), HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return Principal{}, apierr.New(apierr.Unauthorized)
	}
	if err != nil {
		return Principal{}, err
	}
	sess := r.Session
	if sess.RevokedAt != nil {
		switch sess.RevokeReason {
		case store.RevokeReplaced:
			return Principal{}, apierr.New(apierr.SessionReplaced)
		case store.RevokeLogout:
			return Principal{}, apierr.New(apierr.Unauthorized)
		default:
			return Principal{}, apierr.New(apierr.SessionExpired)
		}
	}
	now := s.now()
	if now.Unix() >= sess.ExpiresAt {
		return Principal{}, apierr.New(apierr.SessionExpired)
	}
	if r.User.Status != model.StatusActive {
		return Principal{}, apierr.New(apierr.AccountDisabled)
	}
	// 滑动过期：最多每 TouchInterval 写一次库
	if now.Sub(time.Unix(sess.LastSeenAt, 0)) >= s.opt.TouchInterval {
		sess.LastSeenAt = now.Unix()
		sess.ExpiresAt = now.Add(s.opt.SessionTTL).Unix()
		if err := store.TouchSession(ctx, s.st.DB(), sess.ID, sess.LastSeenAt, sess.ExpiresAt); err != nil {
			// 写失败不影响本次请求，下次再写
			s.opt.Logger.WarnContext(ctx, "touch session", "session_id", sess.ID, "err", err)
		}
	}
	return Principal{User: r.User, Session: sess}, nil
}

// Logout 吊销当前会话。
func (s *Service) Logout(ctx context.Context, p Principal) error {
	_, err := store.RevokeSession(ctx, s.st.DB(), p.Session.ID, store.RevokeLogout, s.now().Unix())
	return err
}

// ---- 改密码与重置密码 ----

// ChangePassword 校验旧密码后修改密码，并吊销该用户的其他会话（当前会话保留）。
// 旧密码校验失败同样计入按昵称的失败锁定。
func (s *Service) ChangePassword(ctx context.Context, p Principal, oldPassword, newPassword string, meta ClientMeta) error {
	var c validate.Errors
	if oldPassword == "" {
		c = append(c, validate.FieldError{Field: "oldPassword", Reason: validate.ReasonRequired, Message: "旧密码不能为空"})
	}
	var es validate.Errors
	if errors.As(validate.Password("newPassword", newPassword), &es) {
		c = append(c, es...)
	}
	if len(c) > 0 {
		return validationError(c)
	}

	now := s.now()
	att, denial, wait := s.lockout.Begin(p.User.Nickname, now)
	switch denial {
	case Locked:
		return lockedError(wait)
	case Busy:
		return busyError()
	}
	defer att.Cancel()

	ok, _, err := s.verify(ctx, oldPassword, p.User.PasswordHash)
	if err != nil {
		return fmt.Errorf("auth: verify password: %w", err)
	}
	if !ok {
		d, locked := att.Fail(now)
		s.auditBestEffort(ctx, store.AuditEntry{
			ActorID: p.User.ID, Action: store.AuditChangePassword, Target: p.User.Nickname, IP: meta.IP,
			Detail: map[string]any{"ok": false},
		})
		if locked {
			return lockedError(d)
		}
		return apierr.New(apierr.InvalidCredentials)
	}
	att.Succeed()
	if s.afterVerify != nil {
		s.afterVerify()
	}

	hash, err := s.hash(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.st.Tx(ctx, func(tx *sql.Tx) error {
		// 旧密码是对认证时读到的哈希校验的。期间会话被吊销（例如另一台设备重置了密码），
		// 或者密码已被改过，都不能再用旧密码覆盖新密码。
		revoked, err := store.SessionRevoked(ctx, tx, p.Session.ID)
		if err != nil {
			return err
		}
		if revoked {
			return apierr.New(apierr.SessionExpired)
		}
		replaced, err := store.ReplacePasswordHash(ctx, tx, p.User.ID, p.User.PasswordHash, hash, now.Unix())
		if err != nil {
			return err
		}
		if !replaced {
			return apierr.New(apierr.InvalidCredentials)
		}
		n, err := store.RevokeUserSessions(ctx, tx, p.User.ID, p.Session.ID, store.RevokePasswordChanged, now.Unix())
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, store.AuditEntry{
			At: now.Unix(), ActorID: p.User.ID, Action: store.AuditChangePassword, Target: p.User.Nickname, IP: meta.IP,
			Detail: map[string]any{"ok": true, "revokedSessions": n},
		})
	})
}

// ResetPassword 用管理员发放的一次性重置码设置新密码，成功后吊销该用户的所有会话。
// 重置码不存在、与昵称不匹配、已用过、已过期，一律返回 reset_code_invalid。
func (s *Service) ResetPassword(ctx context.Context, nickname, code, newPassword string, meta ClientMeta) error {
	nickname = strings.TrimSpace(nickname)
	code = NormalizeResetCode(code)
	var c validate.Errors
	if nickname == "" {
		c = append(c, validate.FieldError{Field: "nickname", Reason: validate.ReasonRequired, Message: "昵称不能为空"})
	}
	if code == "" {
		c = append(c, validate.FieldError{Field: "code", Reason: validate.ReasonRequired, Message: "重置码不能为空"})
	} else if len(code) > 64 {
		c = append(c, validate.FieldError{Field: "code", Reason: validate.ReasonTooLong, Message: "重置码格式不正确"})
	}
	var es validate.Errors
	if errors.As(validate.Password("newPassword", newPassword), &es) {
		c = append(c, es...)
	}
	if len(c) > 0 {
		return validationError(c)
	}

	now := s.now().Unix()
	codeHash := HashResetCode(code)
	rc, err := store.GetResetCode(ctx, s.st.DB(), codeHash)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.ResetCodeInvalid)
	}
	if err != nil {
		return err
	}
	u, err := store.GetUser(ctx, s.st.DB(), rc.UserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !strings.EqualFold(u.Nickname, nickname)) {
		return apierr.New(apierr.ResetCodeInvalid)
	}
	if err != nil {
		return err
	}
	if rc.UsedAt != nil || now >= rc.ExpiresAt {
		return apierr.New(apierr.ResetCodeInvalid)
	}

	hash, err := s.hash(ctx, newPassword)
	if err != nil {
		return err
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		ok, err := store.UseResetCode(ctx, tx, codeHash, now)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.ResetCodeInvalid) // 并发使用同一个重置码
		}
		if err := store.UpdatePasswordHash(ctx, tx, u.ID, hash, now); err != nil {
			return err
		}
		n, err := store.RevokeUserSessions(ctx, tx, u.ID, "", store.RevokePasswordChanged, now)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, store.AuditEntry{
			At: now, ActorID: u.ID, Action: store.AuditResetPassword, Target: u.Nickname, IP: meta.IP,
			Detail: map[string]any{"revokedSessions": n},
		})
	})
	if err != nil {
		return err
	}
	s.lockout.Reset(u.Nickname)
	return nil
}

// IssueResetCode 为用户生成一次性重置码（管理命令使用），同时作废该用户之前未用的重置码。
// 返回的明文重置码只打印一次，库里只存哈希。
func (s *Service) IssueResetCode(ctx context.Context, nickname string) (code string, expiresAt time.Time, err error) {
	now := s.now()
	expiresAt = now.Add(s.opt.ResetCodeTTL)
	code = NewResetCode()
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		u, err := store.GetUserByNickname(ctx, tx, strings.TrimSpace(nickname))
		if err != nil {
			return err
		}
		if err := store.ReplaceResetCode(ctx, tx, store.ResetCode{
			CodeHash: HashResetCode(code), UserID: u.ID, CreatedAt: now.Unix(), ExpiresAt: expiresAt.Unix(),
		}); err != nil {
			return err
		}
		return s.audit(ctx, tx, store.AuditEntry{
			At: now.Unix(), Action: store.AuditAdminResetCode, Target: u.Nickname,
			Detail: map[string]any{"via": "cli", "expiresAt": expiresAt.Unix()},
		})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return code, expiresAt, nil
}

// Cleanup 删除早已过期或吊销的会话和重置码，并清理内存里的锁定记录。由 serve 定期调用。
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now()
	// 吊销的会话保留 30 天，让被挤下线的客户端仍能收到 session_replaced
	if _, err := store.DeleteStaleSessions(ctx, s.st.DB(), now.Add(-7*24*time.Hour).Unix(), now.Add(-30*24*time.Hour).Unix()); err != nil {
		return err
	}
	if _, err := store.DeleteStaleResetCodes(ctx, s.st.DB(), now.Add(-7*24*time.Hour).Unix()); err != nil {
		return err
	}
	s.lockout.Sweep(now)
	return nil
}
