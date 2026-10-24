import type { Node, Proxy } from './api'

export function nodeLabel(n: Node | undefined | null): string {
  if (!n) return ''
  return n.remark?.trim() ? n.remark : `${n.serverAddr}:${n.serverPort}`
}

/** 官方节点限额的一句话说明。 */
export function quotaText(q: { maxProxies?: number; ports?: string; types?: string[] | null } | undefined): string {
  if (!q) return ''
  const parts = [q.maxProxies ? `最多 ${q.maxProxies} 个代理` : '代理数不限', q.ports ? `远程端口 ${q.ports}` : '远程端口不限']
  if (q.types?.length) parts.push(`类型 ${q.types.join('/')}`)
  return parts.join(' · ')
}

export function proxyRemote(p: Proxy): string {
  if (p.type === 'tcp' || p.type === 'udp') return p.remotePort ? `:${p.remotePort}` : '由节点分配'
  if (p.type === 'http' || p.type === 'https' || p.type === 'tcpmux') {
    const ds = [...(p.customDomains ?? [])]
    if (p.subdomain) ds.push(`${p.subdomain}.<子域名后缀>`)
    return ds.join(', ')
  }
  return '点对点（访问方用密钥连接）'
}

export function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T
}

/** 输入框里的端口：空或不是正整数时返回 0（交给保存时的校验报错）。 */
export function portValue(v: string | number | null | undefined): number {
  const n = Number(String(v ?? '').trim())
  return Number.isInteger(n) && n > 0 ? n : 0
}
