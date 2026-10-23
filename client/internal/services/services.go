// Package services 是绑定给前端调用的 Go 服务层（PLAN 6.4）。
//
// 所有方法返回的错误都是 *apierr.Error（前端从 CallError.cause 里读 code），
// 中文提示由前端的 errors.ts 统一映射。
package services

// 推送给前端的事件名（PLAN 6.4）。sync:*、auth:*、image:*、update:* 在之后的阶段加。
const (
	EventTunnelState = "tunnel:state" // engine.Status
	EventTunnelProxy = "tunnel:proxy" // []engine.ProxyStatus：当前全部启用代理的状态
	EventTunnelLogs  = "tunnel:logs"  // []engine.LogLine：一批新日志
)

// Emitter 把事件推给前端，由 main 在 Wails 应用创建后注入。
type Emitter func(name string, data any)
