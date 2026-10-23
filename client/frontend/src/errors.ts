// 错误码 → 中文提示，全部集中在这里（PLAN 5.3：客户端只看 code，中文提示在前端的一个映射表里维护）。
//
// 错误来自三处，格式都是 { code, message, details }：
//   1. 服务端 API（shared/apierr，Phase 3 起）；
//   2. 客户端 Go 端（internal/apperr），例如连接节点失败的原因；
//   3. 代理状态里的 code（frps 拒绝某个代理的原因）。
// message 是给日志看的原文，不直接显示；没有映射的 code 才退回显示 message。

export interface AppError {
  code: string
  message: string
  details?: Record<string, any> | null
}

export interface FieldError {
  field: string
  reason: string
  message: string
}

/** code → 中文。带 {target} 之类占位符的会用 details 里的同名字段替换。 */
export const codeMessages: Record<string, string> = {
  // ---- 服务端 API ----
  unauthorized: '请先登录',
  session_expired: '登录已过期，请重新登录',
  session_replaced: '账号已在其他设备登录，当前设备已下线',
  forbidden: '没有权限',
  not_found: '内容不存在',
  validation_failed: '填写的内容有误',
  invalid_credentials: '昵称或密码错误',
  account_locked: '失败次数过多，请 {retryAfter} 秒后再试',
  account_disabled: '账号已被停用',
  invite_invalid: '邀请码无效',
  invite_used: '邀请码已被使用',
  nickname_taken: '昵称已被注册',
  reset_code_invalid: '重置码无效或已过期',
  revision_conflict: '配置已在其他设备修改',
  share_revoked: '分享已撤销',
  share_expired: '分享已过期',
  share_full: '订阅人数已满',
  already_subscribed: '已经订阅过',
  limit_exceeded: '超出数量上限（最多 {limit} 个）',
  rate_limited: '操作太频繁，请稍后再试',
  client_outdated: '客户端版本过低，请下载新版',
  internal: '程序内部错误',
  method_not_allowed: '请求方式不支持',
  payload_too_large: '内容太大',

  // ---- 客户端：配置 ----
  profile_not_found: '配置不存在，可能已被删除',
  profile_in_use: '这份配置正在运行，请先停止隧道',
  node_not_found: '选中的节点已不存在，请重新选择',
  node_required: '请先添加并选择一个节点',
  frp_config_invalid: 'frp 拒绝了这份配置',

  // ---- 客户端：隧道 ----
  tunnel_busy: '隧道正在停止，请稍候',
  tunnel_not_running: '隧道没有在运行',
  tunnel_start_failed: '隧道启动失败',

  // ---- 客户端：连接节点 ----
  node_token_invalid: '节点 token 错误',
  node_refused: '无法连接节点：连接被拒绝（{target}），请检查地址和端口，或节点是否在运行',
  node_timeout: '无法连接节点：连接超时（{target}），请检查网络或防火墙',
  node_dns: '无法连接节点：域名解析失败（{target}）',
  node_unreachable: '无法连接节点（{target}）',
  node_handshake_failed: '能连上 {target}，但登录失败：对方可能不是 frps，或协议、TLS 设置不一致',
  node_udp_unprobed: 'kcp / quic 走 UDP，无法测速',

  // ---- 代理状态：frps 拒绝了某个代理 ----
  proxy_port_in_use: '远程端口已被占用',
  proxy_port_not_allowed: '节点不允许使用这个远程端口',
  proxy_port_unavailable: '远程端口不可用（可能被节点上的其他程序占用）',
  proxy_name_taken: '代理名已被占用（同一节点上有同名代理在运行）',
  proxy_domain_conflict: '域名已被其他代理使用',
  proxy_type_unsupported: '节点不支持这种代理类型',
  proxy_start_error: '代理启动失败',

  // ---- 客户端：账号与同步 ----
  server_unreachable: '无法连接服务器，已切换到离线模式（本机的配置仍可编辑和启动）',
  bad_response: '服务器返回了无法识别的内容',
  not_logged_in: '请先登录',
  conflict_gone: '这个冲突已经处理过了',

  // ---- 客户端：分享与订阅 ----
  subscription_read_only: '订阅的内容由分享者决定，不能直接修改、复制或上传',
  share_needs_cloud: '只能分享云端配置：先把这份配置上传到云端',
  share_not_synced: '这份配置还有没同步到云端的修改，请等同步完成后再试',
  subscription_inactive: '分享已撤销或已过期，无法启动',

  // ---- 客户端：图片面板、设置 ----
  image_disabled: '图片面板已关闭',
  image_no_source: '暂时没有可用的图源',
  image_busy: '正在获取图片，请稍候',
  image_fetch_failed: '获取图片失败，稍后再试',
  image_not_found: '图片已被清理，请换一张',
  image_decode_failed: '这张图片的格式无法复制',
  clipboard_failed: '写入剪贴板失败',
  autostart_failed: '设置开机自启失败',
  proxy_url_invalid: '代理地址格式不对，例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080',

  // ---- 本地 ----
  storage_failed: '保存到本地失败',
  export_failed: '导出失败',
  invalid_argument: '参数不正确',

  // ---- 前端自己的 ----
  network: '无法与程序后台通信',
}

/** shared/validate 的 reason → 中文。 */
export const reasonMessages: Record<string, string> = {
  required: '必填',
  too_long: '太长了',
  too_short: '太短了',
  invalid: '格式不正确',
  range: '超出范围',
  duplicate: '与已有的名称重复（代理和访客的名称不能相同）',
  too_many: '数量超出上限',
  unknown: '不支持的取值',
  reserved: '是保留词，不能使用',
  reference: '引用的对象不存在或类型不对',
  checksum: '校验位不正确',
  type: '类型不正确',
  syntax: '格式错误',
  unknown_field: '未知字段',
}

/** 字段名（路径的最后一段）→ 中文，用于没有表单项可以挂靠的字段错误。 */
export const fieldLabels: Record<string, string> = {
  id: 'ID',
  name: '名称',
  type: '类型',
  remark: '备注',
  serverAddr: '服务器地址',
  serverPort: '服务器端口',
  token: 'token',
  user: 'user',
  protocol: '协议',
  proxyURL: '代理地址',
  localIP: '本地地址',
  localPort: '本地端口',
  remotePort: '远程端口',
  customDomains: '自定义域名',
  subdomain: '子域名',
  secretKey: '密钥',
  allowUsers: '允许的用户',
  serverName: '目标代理名',
  serverUser: '目标用户',
  bindAddr: '绑定地址',
  bindPort: '绑定端口',
  fallbackTo: '回退访客',
  nodes: '节点',
  proxies: '代理',
  visitors: '访客',
  nickname: '昵称',
  password: '密码',
  newPassword: '新密码',
  inviteCode: '邀请码',
  code: '重置码',
  profileId: '配置',
  kind: '分享类型',
  nodeIds: '节点',
  proxyIds: '代理',
  expiresAt: '过期时间',
  maxSubscribers: '人数上限',
  nodeId: '节点',
  binds: '本地绑定',
  targetId: '目标',
  shareId: '分享 ID',
  payload: '分享内容',
  targets: 'P2P 目标',
  sourceProfileId: '来源配置',
}

const collectionLabels: Record<string, string> = { nodes: '节点', proxies: '代理', visitors: '访客' }

/** 把任意异常（Wails 调用失败、普通 Error、字符串）规整成 AppError。 */
export function toAppError(e: unknown): AppError {
  if (e && typeof e === 'object') {
    const anyE = e as any
    const cause = anyE.cause
    if (cause && typeof cause === 'object' && typeof cause.code === 'string') {
      return { code: cause.code, message: cause.message ?? '', details: cause.details ?? null }
    }
    if (typeof anyE.code === 'string' && typeof anyE.message === 'string') {
      return { code: anyE.code, message: anyE.message, details: anyE.details ?? null }
    }
    if (typeof anyE.message === 'string') {
      return { code: 'internal', message: anyE.message }
    }
  }
  return { code: 'internal', message: String(e) }
}

function fill(template: string, details?: Record<string, any> | null): string {
  return template.replace(/\{(\w+)\}/g, (_, k) => {
    const v = details?.[k]
    return v === undefined || v === null || v === '' ? '?' : String(v)
  })
}

/** 一个 code 的中文提示（没有映射时返回 fallback 或 code 本身）。 */
export function codeText(code: string | undefined | null, details?: Record<string, any> | null, fallback?: string): string {
  if (!code) return fallback ?? ''
  const t = codeMessages[code]
  if (t) return fill(t, details)
  return fallback || code
}

/** 字段错误列表（validation_failed 的 details.fields）。 */
export function fieldErrorsOf(e: AppError | null | undefined): FieldError[] {
  const f = e?.details?.fields
  return Array.isArray(f) ? (f as FieldError[]) : []
}

/** 一条字段错误的中文说明，例如"代理 #2 · 名称：与已有的名称重复"。 */
export function describeField(f: FieldError): string {
  const m = /^(nodes|proxies|visitors)\[(\d+)\]\.(\w+)/.exec(f.field)
  const reason = reasonMessages[f.reason] ?? f.message
  if (m) {
    return `${collectionLabels[m[1]]} #${Number(m[2]) + 1} · ${fieldLabels[m[3]] ?? m[3]}：${reason}`
  }
  const last = f.field.split('.').pop() ?? f.field
  return f.field ? `${fieldLabels[last] ?? last}：${reason}` : reason
}

/** 被动下线的原因（auth:expired / AccountState.ended）→ 给用户的明确说明。 */
export const sessionEndedMessages: Record<string, string> = {
  session_replaced: '你的账号已在其他设备登录，本机已退出登录。',
  session_expired: '登录已失效（可能是密码已修改、被管理员下线或长时间未使用），请重新登录。',
  account_disabled: '账号已被停用，已退出登录。如有疑问请联系管理员。',
  unauthorized: '登录已失效，请重新登录。',
}

/** 给用户看的完整一句话。 */
export function describeError(e: unknown): string {
  const ae = toAppError(e)
  if (ae.code === 'validation_failed') {
    const fields = fieldErrorsOf(ae)
    if (fields.length) return `${codeMessages.validation_failed}：${fields.map(describeField).join('；')}`
  }
  if (ae.code === 'frp_config_invalid' && ae.message) {
    return `${codeMessages.frp_config_invalid}：${stripCode(ae.message)}`
  }
  return codeText(ae.code, ae.details, ae.message)
}

/**
 * 某个列表项（例如 proxies[3]）的字段错误：返回 { name: '与已有的名称重复…' }，给抽屉里的表单项显示。
 */
export function fieldErrorsFor(e: AppError | null | undefined, collection: string, index: number): Record<string, string> {
  const out: Record<string, string> = {}
  const prefix = `${collection}[${index}].`
  for (const f of fieldErrorsOf(e)) {
    if (!f.field.startsWith(prefix)) continue
    const key = f.field.slice(prefix.length).replace(/\[\d+\]$/, '')
    if (!out[key]) out[key] = reasonMessages[f.reason] ?? f.message
  }
  return out
}

function stripCode(msg: string): string {
  // apierr.Error() 的格式是 "code: message"
  return msg.replace(/^[a-z_]+: /, '')
}
