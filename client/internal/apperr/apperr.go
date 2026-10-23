// Package apperr 是客户端本地的错误码。
//
// 对前端统一返回 *apierr.Error（{code, message, details}），和服务端的错误格式一致，
// 前端只看 code，中文提示集中在 frontend/src/errors.ts 里维护。
// 这里的 code 只在客户端内部产生，服务端不会返回。
package apperr

import (
	"errors"

	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

const (
	// 配置
	ProfileNotFound apierr.Code = "profile_not_found"
	ProfileInUse    apierr.Code = "profile_in_use" // 正在运行的配置不能删除
	NodeNotFound    apierr.Code = "node_not_found"
	NodeRequired    apierr.Code = "node_required"      // 没有选节点，或配置里没有节点
	FrpConfig       apierr.Code = "frp_config_invalid" // frp 自带的 ValidateAllClientConfig 不通过

	// 隧道
	TunnelBusy       apierr.Code = "tunnel_busy"        // 正在停止或重启，稍后再试
	TunnelNotRunning apierr.Code = "tunnel_not_running" // 没有运行中的隧道
	TunnelStart      apierr.Code = "tunnel_start_failed"

	// 连接节点（首次登录失败时由引擎给出）
	NodeTokenInvalid    apierr.Code = "node_token_invalid"
	NodeRefused         apierr.Code = "node_refused"
	NodeTimeout         apierr.Code = "node_timeout"
	NodeDNS             apierr.Code = "node_dns"
	NodeUnreachable     apierr.Code = "node_unreachable"
	NodeHandshakeFailed apierr.Code = "node_handshake_failed" // TCP 能连上，但 frp 登录失败（不是 frps、TLS 不匹配等）
	NodeUDPUnprobed     apierr.Code = "node_udp_unprobed"     // kcp / quic 走 UDP，没法用 TCP 探测

	// 代理状态（frps 拒绝了某个代理）
	ProxyPortInUse      apierr.Code = "proxy_port_in_use"
	ProxyPortNotAllowed apierr.Code = "proxy_port_not_allowed"
	ProxyPortUnavail    apierr.Code = "proxy_port_unavailable"
	ProxyNameTaken      apierr.Code = "proxy_name_taken" // 同名代理已在 frps 上（通常是别的客户端在用）
	ProxyDomainConflict apierr.Code = "proxy_domain_conflict"
	ProxyTypeNotAllowed apierr.Code = "proxy_type_unsupported"
	ProxyStartError     apierr.Code = "proxy_start_error"

	// 账号与同步
	ServerUnreachable apierr.Code = "server_unreachable" // 连不上服务端（断网、DNS、超时、网关错误），界面显示"离线"
	BadResponse       apierr.Code = "bad_response"       // 服务端返回了看不懂的内容
	NotLoggedIn       apierr.Code = "not_logged_in"      // 需要先登录的操作
	ConflictGone      apierr.Code = "conflict_gone"      // 冲突已经不存在（已在别处解决）

	// 分享与订阅
	SubscriptionReadOnly apierr.Code = "subscription_read_only" // 订阅的内容由分享决定，不能直接编辑或复制
	ShareNeedsCloud      apierr.Code = "share_needs_cloud"      // 只能分享已同步到云端的云端配置
	ShareNotSynced       apierr.Code = "share_not_synced"       // 配置还有没推送的修改，等同步完成再分享
	SubscriptionInactive apierr.Code = "subscription_inactive"  // 分享已撤销或过期，不能启动

	// 图片面板
	ImageDisabled   apierr.Code = "image_disabled"  // 设置里关闭了图片面板
	ImageNoSource   apierr.Code = "image_no_source" // 服务端没有下发图源（或还没拿到）
	ImageBusy       apierr.Code = "image_busy"      // 正在获取上一张
	ImageFetch      apierr.Code = "image_fetch_failed"
	ImageNotFound   apierr.Code = "image_not_found" // 缓存里没有这张图（可能已被清理）
	ImageDecode     apierr.Code = "image_decode_failed"
	ClipboardFailed apierr.Code = "clipboard_failed"

	// 设置
	AutoStartFailed apierr.Code = "autostart_failed"
	ProxyURLInvalid apierr.Code = "proxy_url_invalid" // 自定义代理地址格式不对

	// 本地
	StorageFailed apierr.Code = "storage_failed"
	ExportFailed  apierr.Code = "export_failed"
	InvalidArg    apierr.Code = "invalid_argument"
)

// New 构造一个错误，message 是给日志和调试看的，界面上显示的是前端按 code 映射的中文。
func New(code apierr.Code, message string) *apierr.Error {
	return &apierr.Error{Code: code, Message: message}
}

// Wrap 把底层错误包成指定 code，原文放在 message 里。
func Wrap(code apierr.Code, err error) *apierr.Error {
	if err == nil {
		return nil
	}
	return &apierr.Error{Code: code, Message: err.Error()}
}

// FromValidate 把 shared/validate 的字段错误转换成 validation_failed，字段列表放在 details.fields，
// 和服务端返回的格式相同。
func FromValidate(err error) *apierr.Error {
	if err == nil {
		return nil
	}
	var fe validate.Errors
	if errors.As(err, &fe) {
		return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, []validate.FieldError(fe))
	}
	return apierr.Newf(apierr.ValidationFailed, "%s", err.Error())
}

// Normalize 保证返回给前端的一定是 *apierr.Error：已经是的原样返回，其余包成 internal。
func Normalize(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := apierr.As(err); ok {
		return e
	}
	var fe validate.Errors
	if errors.As(err, &fe) {
		return FromValidate(err)
	}
	return &apierr.Error{Code: apierr.Internal, Message: err.Error()}
}
