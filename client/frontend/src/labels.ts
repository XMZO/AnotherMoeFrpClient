// 界面上用的各种标签（状态、类型）。错误提示不在这里，在 errors.ts。

export const stateLabels: Record<string, string> = {
  idle: '未运行',
  connecting: '连接中',
  running: '运行中',
  reconnecting: '重连中',
  failed: '启动失败',
  stopping: '停止中',
}

export type TagType = 'default' | 'info' | 'success' | 'warning' | 'error'

export const stateTagTypes: Record<string, TagType> = {
  idle: 'default',
  connecting: 'info',
  running: 'success',
  reconnecting: 'warning',
  failed: 'error',
  stopping: 'warning',
}

export const proxyTypes = ['tcp', 'udp', 'http', 'https', 'tcpmux', 'stcp', 'xtcp', 'sudp'] as const
export const visitorTypes = ['stcp', 'xtcp', 'sudp'] as const
export const protocols = ['tcp', 'kcp', 'quic', 'websocket', 'wss'] as const

export const proxyTypeHints: Record<string, string> = {
  tcp: '把本地 TCP 端口映射到节点的远程端口；游戏同时要 UDP 的话，把 UDP 也点上',
  udp: '把本地 UDP 端口映射到节点的远程端口；同时要 TCP 的话，把 TCP 也点上',
  'tcp+udp': '同一个本地端口、同一个远程端口同时转发 TCP 和 UDP（很多游戏需要）；远程端口要填，在节点上算两个代理',
  http: '按域名转发 HTTP（需要节点开启 vhostHTTPPort）',
  https: '按域名转发 HTTPS（需要节点开启 vhostHTTPSPort）',
  tcpmux: '按域名复用一个端口转发 TCP（HTTP CONNECT）',
  stcp: '点对点加密访问，访问方需要相同的密钥（流量经节点中转）',
  xtcp: '点对点打洞直连，访问方需要相同的密钥',
  sudp: '点对点访问 UDP 服务，访问方需要相同的密钥',
}

/** 官方节点的地区分类，按这个顺序分组显示；没分类的（旧面板）放在最后。 */
export const regionLabels: Record<string, string> = { cn: '中国大陆', hmt: '港澳台', intl: '境外' }
const regionOrder = ['cn', 'hmt', 'intl']
export function groupByRegion<T extends { region?: string }>(list: T[]): { region: string; label: string; items: T[] }[] {
  const key = (n: T) => (regionOrder.includes(n.region ?? '') ? (n.region as string) : '')
  return [...regionOrder, '']
    .map((r) => ({ region: r, label: regionLabels[r] ?? '其他', items: list.filter((n) => key(n) === r) }))
    .filter((g) => g.items.length)
}

export const isDomainType = (t: string) => t === 'http' || t === 'https' || t === 'tcpmux'
export const isP2PType = (t: string) => t === 'stcp' || t === 'xtcp' || t === 'sudp'
export const hasRemotePort = (t: string) => t === 'tcp' || t === 'udp'
/** 显示用的类型：tcp 代理勾了同时转发 UDP 时是 tcp+udp。 */
export const proxyTypeLabel = (p: { type: string; withUdp?: boolean }) => (p.type === 'tcp' && p.withUdp ? 'tcp+udp' : p.type)
/** 代理在 frp 里实际用到的类型：tcp+udp 是两种。 */
export const frpTypesOf = (p: { type: string; withUdp?: boolean }) => (p.type === 'tcp' && p.withUdp ? ['tcp', 'udp'] : [p.type])

/** frp 代理 Phase（client/proxy/proxy_wrapper.go）→ 中文。空串表示还没在 frp 里注册（多半还在连接）。 */
export const phaseLabels: Record<string, string> = {
  new: '准备中',
  'wait start': '等待节点确认',
  'start error': '启动失败',
  running: '运行中',
  'check failed': '健康检查失败',
  closed: '已关闭',
  '': '等待连接',
}

/** 同步状态指示（PLAN 6.6）。 */
export const syncStateLabels: Record<string, string> = {
  off: '未登录',
  syncing: '同步中…',
  synced: '已同步',
  offline: '离线',
  outdated: '版本过低，已停止同步',
  error: '同步出错，稍后重试',
}

/** 列表里单个配置的同步标记。 */
export const profileSyncLabels: Record<string, string> = {
  synced: '已同步',
  pending: '待同步',
  conflict: '冲突',
  error: '同步失败',
}

/** 分享类型（PLAN 7.6）。 */
export const shareKindLabels: Record<string, string> = {
  template: '模板',
  full: '完整',
  p2p: 'P2P',
}

export const shareKindHints: Record<string, string> = {
  template: '只分享节点：对方选一个节点，用自己的代理规则',
  full: '分享节点和代理：对方原样运行你的代理（例如帮你在另一台机器上开端口）',
  p2p: '分享 stcp/xtcp/sudp 代理：对方在本机开一个端口，点对点访问你的服务',
}

export const diffSectionLabels: Record<string, string> = {
  profile: '配置',
  nodes: '节点',
  proxies: '代理',
  visitors: '访客',
}
