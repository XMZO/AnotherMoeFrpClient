// 官方节点端到端（docs/node-agent.md 第 12 节）：面板 + 真实 agent（Windows 版，文件放在临时目录）+ 真实客户端界面。
//
//   管理员把节点设为官方节点、开放给普通用户、设限额 → 用户在客户端登录后看到官方节点 → 选它启动隧道 →
//   代理名带上昵称前缀、流量能通 → 超过数量时代理被拒绝并显示原因 → 节点 frps 重启后客户端带新的登录票据重连 →
//   管理员踢下线后节点拒绝这台设备的新连接。
//
//   node scripts/e2e/run-official.mjs [--keep]
//
// 需要先构建端到端测试版客户端：wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e
import { spawn, execFileSync } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { connect, sleep, tid, waitUntil } from './cdp.mjs'
import { ADMIN_SETUP_CODE, adminClient } from './adminapi.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
const client = path.resolve(here, '..', '..')
const gogogo = path.resolve(client, '..')
const keep = process.argv.includes('--keep')
const EXE = path.join(client, 'bin', 'MoeFrp-e2e.exe')
const OUT = path.join(here, 'out-official')
const T = path.join(os.tmpdir(), 'moefrp-e2eo-' + new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14))
const SERVER_PORT = 18090
const API = `http://127.0.0.1:${SERVER_PORT}`
const FRPS_PORT = 17100
const ECHO_PORT = 17101
const NICK = 'alice'
const PW = 'alice-password-1'

fs.mkdirSync(OUT, { recursive: true })
for (const f of fs.readdirSync(OUT)) fs.rmSync(path.join(OUT, f), { force: true, recursive: true })
fs.mkdirSync(T, { recursive: true })
const result = { startedAt: new Date().toISOString(), dataRoot: T, checks: {} }
let failures = 0
const log = (...a) => console.error('--', ...a)
function check(name, ok, extra) {
  result.checks[name] = { ok: !!ok, ...(extra === undefined ? {} : { extra }) }
  console.log((ok ? 'PASS ' : 'FAIL ') + name + (extra === undefined ? '' : ' ' + JSON.stringify(extra)))
  if (!ok) failures++
}

const serverExe = path.join(T, 'moefrp-server.exe')
const agentExe = path.join(T, 'moefrp-agent.exe')
const credtool = path.join(T, 'credtool.exe')
// 端到端构建把登录令牌存在凭据管理器的 MoeFrp/session/e2e-<数据目录哈希> 下，结束时要删掉，否则 run-phase3 开头会报残留
const CRED_PREFIX = 'MoeFrp/session/e2e-'
log('building server, agent, credtool')
execFileSync('go', ['build', '-o', credtool, './scripts/e2e/credtool'], { cwd: client, stdio: 'inherit' })
execFileSync('go', ['build', '-o', serverExe, './cmd/moefrp-server'], { cwd: path.join(gogogo, 'server'), stdio: 'inherit' })
execFileSync('go', ['build', '-ldflags', '-X main.version=e2e', '-o', agentExe, './cmd/moefrp-agent'], { cwd: path.join(gogogo, 'agent'), stdio: 'inherit' })

const procs = []
function start(exe, args, env, logName) {
  const fd = fs.openSync(path.join(OUT, logName), 'a')
  const p = spawn(exe, args, { env: { ...process.env, ...env }, stdio: ['ignore', fd, fd], detached: keep })
  procs.push(p)
  return p
}
const listening = (port) => new Promise((res) => { const c = net.connect(port, '127.0.0.1').once('connect', () => { c.destroy(); res(true) }).once('error', () => res(false)) })

// echo 服务：代理的本地目标
const echo = net.createServer((s) => s.pipe(s)).listen(ECHO_PORT, '127.0.0.1')
function echoOnce(port, msg) {
  return new Promise((resolve) => {
    const s = net.connect(port, '127.0.0.1')
    let buf = ''
    s.setTimeout(3000, () => { s.destroy(); resolve('') })
    s.on('connect', () => s.write(msg))
    s.on('data', (d) => { buf += d; if (buf.includes(msg)) { s.destroy(); resolve(buf) } })
    s.on('error', () => resolve(''))
    s.on('close', () => resolve(buf))
  })
}

let app = null
async function main() {
  // ---- 面板和 agent ----
  start(serverExe, ['serve', '--auto-migrate'], {
    MOEFRP_LISTEN: `127.0.0.1:${SERVER_PORT}`, MOEFRP_DB_PATH: path.join(T, 'moefrp.db'), MOEFRP_CONFIG: '',
    MOEFRP_ADMIN_SETUP_CODE: ADMIN_SETUP_CODE, MOEFRP_AGENT_REPO: '', MOEFRP_LOG_LEVEL: 'info',
  }, 'server.log')
  await waitUntil('server', async () => (await fetch(`${API}/healthz`)).ok, 15000, 100)
  const admin = adminClient(API)
  const created = await admin.request('POST', '/nodes', { name: '东京' })
  const nodeId = created.node.id
  const agentEnv = { MOEFRP_AGENT_PREFIX: path.join(T, 'agent') }
  execFileSync(agentExe, ['install', '--server', API, '--code', created.enrollCode], { env: { ...process.env, ...agentEnv }, stdio: 'ignore' })
  start(agentExe, ['run'], agentEnv, 'agent.log')
  await waitUntil('agent online', async () => (await admin.request('GET', `/nodes/${nodeId}`)).status === 'online', 15000, 200)
  // 结构化配置，节点没有 token（只认登录票据）
  const { defaults } = await admin.request('GET', '/frps-schema')
  const cfg = await admin.request('PUT', `/nodes/${nodeId}/settings`, { ...defaults, bindAddr: '127.0.0.1', bindPort: FRPS_PORT, allowPorts: '20000-20100' })
  check('frps_config_applied', cfg.result?.applied, cfg.result)
  await admin.request('PATCH', `/nodes/${nodeId}`, { name: '东京', publicAddr: '127.0.0.1' })
  await admin.request('PUT', '/quotas', {
    user: { maxProxies: 1, ports: '20000-20050', types: ['tcp'] }, admin: { maxProxies: 0, ports: '', types: [] },
  })
  await admin.request('PUT', `/nodes/${nodeId}/official`, { roles: ['user'], description: '端到端测试节点' })
  await waitUntil('frps listening', async () => listening(FRPS_PORT), 10000, 200)
  check('node_official', (await admin.request('GET', `/nodes/${nodeId}`)).official === true)

  // 不带登录票据、直接用 token 的 frpc 会被拒绝（agent/internal/access 的 TestWithFrps 用真的 frpc 测过）
  const [invite] = await admin.createInvites(1)
  const reg = await fetch(`${API}/api/v1/auth/register`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ nickname: NICK, password: PW, inviteCode: invite }) })
  check('user_registered', reg.status === 201)

  // ---- 客户端 ----
  const cdpPort = 9391
  const proc = spawn(EXE, [], {
    env: { ...process.env, MOEFRP_HOME: path.join(T, 'client'), MOEFRP_API_BASE: API, MOEFRP_DIAG: path.join(OUT, 'diag.log'), MOEFRP_CDP_PORT: String(cdpPort) },
    stdio: 'ignore', detached: true,
  })
  proc.unref()
  app = { proc }
  await waitUntil('client ready', () => fs.existsSync(path.join(OUT, 'diag.log')) && /frontend ready/.test(fs.readFileSync(path.join(OUT, 'diag.log'), 'utf8')), 30000, 100)
  const p = await connect(cdpPort, 'client')
  app.page = p
  await p.waitFor('account area', () => p.exists(tid('account-area')))

  // 登录
  await p.click(tid('account-login'))
  await p.waitFor('auth modal', () => p.exists(tid('login-nickname')))
  await p.clickText('.tab', '登录')
  await sleep(150)
  await p.type(tid('login-nickname'), NICK)
  await p.type(tid('login-password'), PW)
  await p.click(tid('login-submit'))
  await p.waitFor('logged in', async () => (await p.attr(tid('account-area'), 'data-logged-in')) === '1', 15000)
  await p.waitFor('synced', async () => (await p.attr(tid('sync-status'), 'data-state')) === 'synced', 20000)

  // 官方节点出现在节点页
  await p.clickText('.tab', '节点')
  await p.waitFor('official node listed', () => p.exists(tid('official-node-东京')), 15000)
  check('official_node_listed', true)
  await p.shot(path.join(OUT, '01-official-node.png'))

  // 添加两个代理：一个在范围内，一个超过数量
  async function addProxy(name, remotePort) {
    await p.clickText('.tab', '代理')
    await sleep(150)
    await p.click(tid('proxy-add'))
    await p.waitFor('proxy drawer', () => p.drawerOpen())
    await p.type(tid('proxy-form-name'), name)
    await p.type(tid('proxy-form-localPort'), String(ECHO_PORT))
    await p.type(tid('proxy-form-remotePort'), String(remotePort))
    await p.click(tid('proxy-form-save'))
    await p.waitFor(`proxy ${name}`, async () => !(await p.drawerOpen()) && (await p.exists(tid(`proxy-row-${name}`))))
  }
  await addProxy('web', 20010)

  // 运行栏选中的应是官方节点（配置里没有自己的节点）
  const runNode = await p.text(`${tid('run-node')} .ns-name`)
  check('run_bar_selects_official', (runNode || '').includes('东京'), runNode)
  await p.click(tid('run-toggle'))
  await p.waitFor('running', async () => (await p.attr(tid('run-state'), 'data-state')) === 'running', 20000)
  await p.waitFor('proxy running', async () => (await p.attr(tid('proxy-status-web'), 'data-phase')) === 'running', 15000)
  check('tunnel_running_on_official', true)
  check('traffic_through_official', (await echoOnce(20010, 'hello-official')).includes('hello-official'))
  const px = await admin.request('GET', `/nodes/${nodeId}/proxies`)
  check('proxy_name_prefixed', px.proxies.some((x) => x.name === `${NICK}.web` && x.user === NICK && x.online), px.proxies.map((x) => x.name))

  // 第二个代理：超过"最多 1 个"
  await addProxy('web2', 20011)
  await p.waitFor('web2 rejected', async () => ((await p.text(tid('proxy-status-web2'))) || '').includes('最多同时运行 1'), 15000)
  check('quota_max_proxies_enforced', true, await p.text(tid('proxy-status-web2')))
  await p.shot(path.join(OUT, '02-quota.png'))

  // 节点上的 frps 重启：客户端自动重连。每次登录都要一张新票据（旧的已经用过），重连成功说明换票据生效。
  // 重连后 web 和 web2 重新注册的先后不定，限额只有 1 个，哪个先注册上哪个能通
  await admin.request('POST', `/nodes/${nodeId}/restart`)
  await p.waitFor('reconnecting', async () => ['reconnecting', 'connecting'].includes(await p.attr(tid('run-state'), 'data-state')), 10000).catch(() => {})
  const echoAny = async (msg) => (await echoOnce(20010, msg)).includes(msg) || (await echoOnce(20011, msg)).includes(msg)
  await waitUntil('reconnected with a fresh ticket', async () => (await p.attr(tid('run-state'), 'data-state')) === 'running' &&
    (await echoAny('after-restart')), 30000, 300).then(
    () => check('reconnect_with_fresh_ticket', true),
    () => check('reconnect_with_fresh_ticket', false),
  )

  // 管理员踢下线：这台设备的会话被撤销，节点马上拒绝它的新连接（以前踢下线不影响节点上的连接）
  await admin.request('POST', `/users/${NICK}/kick`)
  await waitUntil('kicked session refused', async () => !(await echoOnce(20010, 'after-kick')).includes('after-kick'), 10000, 300).then(
    () => check('kicked_session_connections_refused', true),
    () => check('kicked_session_connections_refused', false),
  )
  await p.shot(path.join(OUT, '03-kicked.png'))
}

try {
  await main()
} catch (e) {
  console.error('FATAL', e)
  failures++
} finally {
  fs.writeFileSync(path.join(OUT, 'result.json'), JSON.stringify(result, null, 2))
  if (!keep) {
    if (app?.page) app.page.close()
    if (app?.proc) { try { execFileSync('taskkill', ['/F', '/PID', String(app.proc.pid)], { stdio: 'ignore' }) } catch {} }
    for (const pr of procs) pr.kill()
    echo.close()
    try {
      execFileSync(credtool, ['delete', CRED_PREFIX], { stdio: 'ignore' })
      const left = JSON.parse(execFileSync(credtool, ['list', CRED_PREFIX], { encoding: 'utf8' }))
      check('cleanup_test_credentials_removed', left.length === 0, { left: left.length })
    } catch (e) {
      check('cleanup_test_credentials_removed', false, String(e))
    }
  }
  console.log(failures ? `${failures} FAILED` : 'ALL PASSED')
  process.exit(failures ? 1 : 0)
}
