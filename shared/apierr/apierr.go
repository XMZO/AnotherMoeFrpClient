// Package apierr 定义 API 的错误码和统一的错误响应格式（PLAN 5.3、5.4）。
//
// 响应体：
//
//	{ "error": { "code": "revision_conflict", "message": "配置已在其他设备修改", "details": {} } }
//
// 客户端只看 code；message 只用于日志和调试，前端的中文提示在映射表里集中维护。
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是机器可读的错误码。
type Code string

const (
	Unauthorized       Code = "unauthorized"
	SessionExpired     Code = "session_expired"
	SessionReplaced    Code = "session_replaced"
	Forbidden          Code = "forbidden"
	NotFound           Code = "not_found"
	ValidationFailed   Code = "validation_failed"
	InvalidCredentials Code = "invalid_credentials"
	AccountLocked      Code = "account_locked"
	AccountDisabled    Code = "account_disabled"
	InviteInvalid      Code = "invite_invalid"
	InviteUsed         Code = "invite_used"
	NicknameTaken      Code = "nickname_taken"
	ResetCodeInvalid   Code = "reset_code_invalid"
	RevisionConflict   Code = "revision_conflict"
	ShareRevoked       Code = "share_revoked"
	ShareExpired       Code = "share_expired"
	ShareFull          Code = "share_full"
	AlreadySubscribed  Code = "already_subscribed"
	LimitExceeded      Code = "limit_exceeded"
	RateLimited        Code = "rate_limited"
	ClientOutdated     Code = "client_outdated"
	Internal           Code = "internal"

	// 以下两个不在 PLAN 5.4 的初版表里，是实现时补充的通用错误。
	MethodNotAllowed Code = "method_not_allowed"
	PayloadTooLarge  Code = "payload_too_large"
)

type codeInfo struct {
	status  int
	message string
}

var codes = map[Code]codeInfo{
	Unauthorized:       {http.StatusUnauthorized, "未登录或令牌无效"},
	SessionExpired:     {http.StatusUnauthorized, "会话已过期，请重新登录"},
	SessionReplaced:    {http.StatusUnauthorized, "账号已在其他设备登录"},
	Forbidden:          {http.StatusForbidden, "没有权限"},
	NotFound:           {http.StatusNotFound, "资源不存在"},
	ValidationFailed:   {http.StatusBadRequest, "参数不合法"},
	InvalidCredentials: {http.StatusUnauthorized, "昵称或密码错误"},
	AccountLocked:      {http.StatusTooManyRequests, "失败次数过多，账号已暂时锁定"},
	AccountDisabled:    {http.StatusForbidden, "账号已被停用"},
	InviteInvalid:      {http.StatusBadRequest, "邀请码无效"},
	InviteUsed:         {http.StatusConflict, "邀请码已被使用"},
	NicknameTaken:      {http.StatusConflict, "昵称已被注册"},
	ResetCodeInvalid:   {http.StatusBadRequest, "重置码无效或已过期"},
	RevisionConflict:   {http.StatusConflict, "配置已在其他设备修改"},
	ShareRevoked:       {http.StatusGone, "分享已撤销"},
	ShareExpired:       {http.StatusGone, "分享已过期"},
	ShareFull:          {http.StatusConflict, "订阅人数已满"},
	AlreadySubscribed:  {http.StatusConflict, "已经订阅过"},
	LimitExceeded:      {http.StatusConflict, "超出数量上限"},
	RateLimited:        {http.StatusTooManyRequests, "请求过于频繁"},
	ClientOutdated:     {http.StatusUpgradeRequired, "客户端版本过低，请升级"},
	Internal:           {http.StatusInternalServerError, "服务器内部错误"},
	MethodNotAllowed:   {http.StatusMethodNotAllowed, "不支持的请求方法"},
	PayloadTooLarge:    {http.StatusRequestEntityTooLarge, "请求体过大"},
}

// Codes 返回全部已定义的错误码，供测试和前端映射表核对。
func Codes() []Code {
	out := make([]Code, 0, len(codes))
	for c := range codes {
		out = append(out, c)
	}
	return out
}

// Status 返回错误码对应的 HTTP 状态码；未知的错误码按 500 处理。
func (c Code) Status() int {
	if info, ok := codes[c]; ok {
		return info.status
	}
	return http.StatusInternalServerError
}

// DefaultMessage 返回错误码的默认说明。
func (c Code) DefaultMessage() string {
	if info, ok := codes[c]; ok {
		return info.message
	}
	return string(c)
}

// Known 判断是否是已定义的错误码。
func (c Code) Known() bool {
	_, ok := codes[c]
	return ok
}

// Error 是 API 错误，同时实现 error 接口，服务端可以直接 return 它。
type Error struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Response 是错误响应的外层结构。
type Response struct {
	Error *Error `json:"error"`
}

// New 用默认说明创建错误。
func New(code Code) *Error {
	return &Error{Code: code, Message: code.DefaultMessage()}
}

// Newf 用自定义说明创建错误。
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// With 追加一项 details，返回自身便于链式调用。
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// Status 返回对应的 HTTP 状态码。
func (e *Error) Status() int { return e.Code.Status() }

func (e *Error) Error() string {
	if e.Message == "" || e.Message == e.Code.DefaultMessage() {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Is 让 errors.Is(err, apierr.New(code)) 按错误码比较。
func (e *Error) Is(target error) bool {
	var t *Error
	if errors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

// As 从任意 error 里取出 *Error。
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// CodeOf 返回 err 的错误码；不是 *Error 时返回 Internal。
func CodeOf(err error) Code {
	if e, ok := As(err); ok {
		return e.Code
	}
	return Internal
}

// 常用 details 的键名。
const (
	DetailFields        = "fields"        // validation_failed：[]validate.FieldError
	DetailRetryAfter    = "retryAfter"    // account_locked / rate_limited：等待秒数
	DetailCurrent       = "current"       // revision_conflict：云端当前版本
	DetailLatestVersion = "latestVersion" // client_outdated
	DetailMinVersion    = "minVersion"    // client_outdated
	DetailDownloadURL   = "downloadUrl"   // client_outdated
	DetailLimit         = "limit"         // limit_exceeded：上限
)
