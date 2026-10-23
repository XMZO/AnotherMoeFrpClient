import type { Node, Proxy } from './api'

export function nodeLabel(n: Node | undefined | null): string {
  if (!n) return ''
  return n.remark?.trim() ? n.remark : `${n.serverAddr}:${n.serverPort}`
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
