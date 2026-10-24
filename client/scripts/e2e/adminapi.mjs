// 端到端测试里调用服务端管理后台接口（代替已移除的 moefrp-server admin 命令行）。
// 服务端用 MOEFRP_ADMIN_SETUP_CODE 固定初始化码，第一次调用时创建管理员并拿到 cookie。

export const ADMIN_SETUP_CODE = 'E2E-ADMIN-SETUP-CODE'

export function adminClient(base) {
  let cookie = ''
  async function call(method, p, body, withCookie = true) {
    const headers = { 'X-MoeFrp-Admin': '1' }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (withCookie) headers.Cookie = cookie
    const r = await fetch(`${base}/api/v1/admin${p}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    if (!r.ok) throw new Error(`admin ${method} ${p}: ${r.status} ${await r.text()}`)
    return r
  }
  async function login() {
    if (cookie) return
    const r = await call('POST', '/setup', { code: ADMIN_SETUP_CODE, nickname: 'e2eadmin', password: 'e2e-admin-password' }, false)
    cookie = r.headers.getSetCookie().map((c) => c.split(';')[0]).find((c) => c.startsWith('moefrp_admin=')) ?? ''
    if (!cookie) throw new Error('admin setup: no cookie')
  }
  return {
    async createInvites(count) {
      await login()
      return (await (await call('POST', '/invites', { count, note: 'e2e' })).json()).codes
    },
    // request 调任意管理接口，返回解析后的 JSON（204 时为 null）
    async request(method, p, body) {
      await login()
      const r = await call(method, p, body)
      return r.status === 204 ? null : r.json()
    },
    password: 'e2e-admin-password',
    async resetCode(nickname) {
      await login()
      return (await (await call('POST', `/users/${encodeURIComponent(nickname)}/reset-code`)).json()).code
    },
  }
}
