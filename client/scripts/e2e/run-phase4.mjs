// PLAN Phase 4（分享与订阅）的端到端验收：本机起 moefrp-server（临时数据库）和 localfrps，
// 两个账号各用一个 MoeFrp-e2e.exe 实例（A = 分享者，B = 订阅者），通过 CDP 操作真实界面。
//
//   1. A 在"分享"页用向导创建 full、template、p2p 三种分享，拿到分享 ID
//   2. B 在"订阅"里粘贴 ID → 预览（不能出现 token、地址）→ 订阅
//      - full：原样运行，经 frps 的远程端口真实转发到 echo
//      - template：B 添加自己的代理（保存为订阅参数），用分享的节点真实转发
//      - p2p：A 运行 stcp 代理，B 的 visitor 在本机监听端口，真实连到 A 的服务
//   3. A 撤销 p2p 分享：B 在下一次同步（60 秒轮询）时停止隧道，并显示"已撤销"
//   另外：订阅的只读限制（full 不能加代理、菜单只有"取消订阅"）、订阅人数显示、取消订阅。
//
// 前提：已构建 bin\MoeFrp-e2e.exe（wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e）。
// 用法：node scripts/e2e/run-phase4.mjs [--keep]
// 占用 127.0.0.1 的 18081（服务端）、17100–17104（frps、echo、远程端口、本地绑定）、9371/9372（CDP）。
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
const OUT = path.join(here, 'out-phase4')
const T = path.join(os.tmpdir(), 'moefrp-e2e4-' + new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14))
const SERVER_PORT = 18081
const API = `http://127.0.0.1:${SERVER_PORT}`
const FRPS_PORT = 17100
const ECHO_PORT = 17101
const FULL_REMOTE = 17102
const TPL_REMOTE = 17103
const P2P_BIND = 17104
const NODE_TOKEN = 'e2e-node-token-phase4'
const OWNER = 'owner4'
const SUBBER = 'subber4'
const PW = 'Pw-phase4-8chars'
const SRC = '分享源'
const FULL = '完整分享'
const TPL = '模板分享'
const P2P = 'P2P分享'

fs.mkdirSync(OUT, { recursive: true })
for (const f of fs.readdirSync(OUT)) fs.rmSync(path.join(OUT, f), { force: true, recursive: true })
fs.mkdirSync(T, { recursive: true })

const result = { startedAt: new Date().toISOString(), dataRoot: T, checks: {}, timings: {} }
let failures = 0
function check(name, ok, extra) {
  result.checks[name] = extra === undefined ? !!ok : { ok: !!ok, ...extra }
  if (!ok) failures++
  console.error(`${ok ? 'PASS' : 'FAIL'} ${name}${extra ? ' ' + JSON.stringify(extra) : ''}`)
}
const log = (...a) => console.error('--', ...a)

// ---------- 进程 ----------
const serverExe = path.join(T, 'moefrp-server.exe')
const localfrps = path.join(T, 'localfrps.exe')
log('building server, localfrps')
execFileSync('go', ['build', '-o', serverExe, './cmd/moefrp-server'], { cwd: path.join(gogogo, 'server'), stdio: 'inherit' })
execFileSync('go', ['build', '-o', localfrps, './cmd/localfrps'], { cwd: client, stdio: 'inherit' })

const dbPath = path.join(T, 'server', 'moefrp.db')
fs.mkdirSync(path.dirname(dbPath), { recursive: true })
const serverEnv = {
  ...process.env,
  MOEFRP_LISTEN: `127.0.0.1:${SERVER_PORT}`,
  MOEFRP_DB_PATH: dbPath,
  MOEFRP_SINGLE_SESSION: 'false',
  MOEFRP_CLIENT_LATEST_VERSION: '3.0.0',
  MOEFRP_CLIENT_MIN_VERSION: '3.0.0-dev',
  MOEFRP_LOG_LEVEL: 'info',
  MOEFRP_ADMIN_SETUP_CODE: ADMIN_SETUP_CODE,
}
let server = null
let frps = null
const admin = adminClient(API)
function portFree(port) {
  return new Promise((resolve) => {
    const s = net.createServer()
    s.once('error', () => resolve(false))
    s.listen(port, '127.0.0.1', () => s.close(() => resolve(true)))
  })
}

async function api(method, route, body, token) {
  const headers = { 'Content-Type': 'application/json', 'X-Client-Version': '3.0.0', 'X-Install-Id': 'e2e-phase4' }
  if (token) headers.Authorization = 'Bearer ' + token
  const r = await fetch(API + route, { method, headers, body: body ? JSON.stringify(body) : undefined })
  const text = await r.text()
  if (!r.ok) throw new Error(`${method} ${route}: ${r.status} ${text}`)
  return text ? JSON.parse(text) : null
}

// ---------- 客户端实例 ----------
const apps = {}
function diagLines(name) {
  const f = path.join(OUT, `diag-${name}.log`)
  return fs.existsSync(f) ? fs.readFileSync(f, 'utf8').split(/\r?\n/) : []
}
const diagCount = (name, re) => diagLines(name).filter((l) => re.test(l)).length
function alive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

async function startApp(name, cdpPort) {
  const ready = diagCount(name, /frontend ready/)
  const proc = spawn(EXE, [], {
    env: {
      ...process.env,
      MOEFRP_HOME: path.join(T, name),
      MOEFRP_API_BASE: API,
      MOEFRP_DIAG: path.join(OUT, `diag-${name}.log`),
      MOEFRP_CDP_PORT: String(cdpPort),
    },
    stdio: 'ignore',
    detached: true,
  })
  proc.unref()
  await waitUntil(`${name} ready`, () => diagCount(name, /frontend ready/) > ready, 30000, 100)
  const page = await connect(cdpPort, name)
  await page.waitFor('account area', () => page.exists(tid('account-area')))
  apps[name] = { proc, page }
  return page
}

async function quitApp(name) {
  const a = apps[name]
  if (!a) return
  a.page.close()
  try {
    execFileSync('taskkill', ['/PID', String(a.proc.pid)], { stdio: 'ignore' })
  } catch {}
  const exited = await waitUntil(`${name} exit`, () => !alive(a.proc.pid), 10000, 100).then(() => true, () => false)
  if (!exited) {
    try {
      execFileSync('taskkill', ['/F', '/PID', String(a.proc.pid)], { stdio: 'ignore' })
    } catch {}
  }
  delete apps[name]
}

// ---------- 界面操作 ----------
const shot = async (p, name) => {
  await sleep(400)
  return p.shot(path.join(OUT, name))
}
const syncState = (p) => p.attr(tid('sync-status'), 'data-state')
const loggedIn = async (p) => (await p.attr(tid('account-area'), 'data-logged-in')) === '1'
const runState = (p) => p.attr(tid('run-state'), 'data-state')

async function setCloseToQuit(p) {
  await p.click(tid('nav-settings'))
  await p.clickText('.radio', '退出')
  await sleep(300)
  await p.click(tid('nav-tunnel'))
}

async function login(p, nick) {
  await p.click(tid('account-login'))
  await p.waitFor('auth modal', () => p.exists(tid('login-nickname')))
  await p.clickText('.tab', '登录')
  await sleep(150)
  await p.type(tid('login-nickname'), nick)
  await p.type(tid('login-password'), PW)
  const checked = await p.evaluate(`document.querySelector('[data-testid=login-keep]')?.getAttribute('aria-checked') === 'true'`)
  if (checked) await p.click(tid('login-keep')) // 不保存令牌，测试结束不用清理凭据管理器
  await p.click(tid('login-submit'))
  await p.waitFor('logged in', () => loggedIn(p), 15000)
  await p.waitFor('synced after login', async () => (await syncState(p)) === 'synced', 20000)
}

async function selectProfile(p, name) {
  await p.click(`${tid(`profile-item-${name}`)} .pitem-main`) // testid 在外层，点击处理在里面的按钮上
  await p.waitFor(`editor shows ${name}`, async () => (await p.text('.editor .title'))?.startsWith(name))
}

async function startTunnel(p, what) {
  await p.click(tid('run-toggle'))
  await p.waitFor(`${what} running`, async () => (await runState(p)) === 'running', 20000)
}

async function stopTunnel(p, what) {
  if ((await runState(p)) === 'idle') return
  await p.click(tid('run-toggle'))
  await p.waitFor(`${what} stopped`, async () => (await runState(p)) === 'idle', 15000)
}

function echoOnce(port, msg) {
  return new Promise((resolve, reject) => {
    const s = net.connect(port, '127.0.0.1')
    let buf = ''
    s.setTimeout(3000, () => {
      s.destroy()
      reject(new Error('echo timeout'))
    })
    s.on('connect', () => s.write(msg))
    s.on('data', (d) => {
      buf += d
      if (buf.length >= msg.length) {
        s.end()
        resolve(buf)
      }
    })
    s.on('error', reject)
  })
}
const echoOk = (port) => echoOnce(port, 'ping-' + port).then((r) => r === 'ping-' + port, () => false)
const echoEventually = (port, timeout = 15000) => waitUntil(`echo via ${port}`, () => echoOk(port), timeout, 300).then(() => true, () => false)

async function createShare(p, { name, kind, uncheck = [] }) {
  await p.click(tid('share-create'))
  await p.waitFor('create modal', () => p.exists(tid('share-create-name')))
  await p.waitFor('profile loaded', () => p.evaluate(`document.querySelectorAll('[data-testid^="share-create-proxy-"]').length > 0 || ${kind === 'template'}`))
  if (kind !== 'full') {
    await p.click(tid(`share-kind-${kind}`))
    await sleep(300)
  }
  for (const proxy of uncheck) await p.click(tid(`share-create-proxy-${proxy}`))
  await p.type(tid('share-create-name'), name)
  await p.click(tid('share-create-submit'))
  await p.waitFor('share created', () => p.exists(tid('share-created-id')), 15000)
  const id = await p.text(tid('share-created-id'))
  await shot(p, `share-created-${kind}.png`)
  await p.clickText('.dlg-actions button', '关闭')
  await sleep(300)
  return id
}

async function subscribe(p, shareId, { port } = {}) {
  await p.click(tid('nav-tunnel'))
  await p.click(tid('subscription-add'))
  await p.waitFor('subscribe modal', () => p.exists(tid('subscribe-id')))
  await p.type(tid('subscribe-id'), shareId)
  await p.click(tid('subscribe-preview'))
  await p.waitFor('preview loaded', () => p.exists(tid('subscribe-preview-name')), 10000)
  const modalText = await p.evaluate(`[...document.querySelectorAll('.modal')].map(e => e.innerText).join('\\n')`)
  if (port) {
    const portTid = await p.evaluate(`document.querySelector('[data-testid^="subscribe-port-"]')?.getAttribute('data-testid')`)
    await p.type(tid(portTid), port)
  }
  const name = await p.text(tid('subscribe-preview-name'))
  await shot(p, `subscribe-preview-${name}.png`)
  await p.click(tid('subscribe-submit'))
  await p.waitFor(`subscription ${name} listed`, () => p.exists(tid(`profile-item-${name}`)), 15000)
  await p.waitFor('modal closed', async () => !(await p.exists(tid('subscribe-id'))))
  return { name, modalText }
}

// ---------- 场景 ----------
async function main() {
  for (const port of [SERVER_PORT, FRPS_PORT, ECHO_PORT, FULL_REMOTE, TPL_REMOTE, P2P_BIND, 9371, 9372]) {
    if (!(await portFree(port))) throw new Error(`port ${port} is in use`)
  }
  execFileSync(serverExe, ['migrate'], { env: serverEnv, stdio: 'ignore' })
  const logFile = fs.openSync(path.join(OUT, 'server.log'), 'a')
  server = spawn(serverExe, ['serve'], { env: serverEnv, stdio: ['ignore', logFile, logFile] })
  await waitUntil('server healthz', async () => (await fetch(`${API}/healthz`)).ok, 15000, 100)
  const invites = await admin.createInvites(2)
  frps = spawn(localfrps, ['-port', String(FRPS_PORT), '-token', NODE_TOKEN, '-echo', `127.0.0.1:${ECHO_PORT}`], { stdio: 'ignore' })
  await waitUntil('localfrps', async () => !(await portFree(FRPS_PORT)), 10000, 100)

  // 两个账号；分享源配置用 API 准备（这部分界面在 Phase 2/3 已验收）
  await api('POST', '/api/v1/auth/register', { nickname: OWNER, password: PW, inviteCode: invites[0] })
  await api('POST', '/api/v1/auth/register', { nickname: SUBBER, password: PW, inviteCode: invites[1] })
  const ownerTok = (await api('POST', '/api/v1/auth/login', { nickname: OWNER, password: PW, deviceName: 'setup' })).token
  await api('POST', '/api/v1/profiles', {
    name: SRC,
    nodes: [{ id: 'node-local', remark: '本机frps', serverAddr: '127.0.0.1', serverPort: FRPS_PORT, token: NODE_TOKEN }],
    proxies: [
      { id: 'px-full', name: 'full-echo', type: 'tcp', enabled: true, localIP: '127.0.0.1', localPort: ECHO_PORT, remotePort: FULL_REMOTE },
      { id: 'px-p2p', name: 'p2p-echo', type: 'stcp', enabled: true, localIP: '127.0.0.1', localPort: ECHO_PORT, secretKey: 'stcp-secret-phase4' },
    ],
    visitors: [],
  }, ownerTok)

  const A = await startApp('A', 9371)
  const B = await startApp('B', 9372)
  await setCloseToQuit(A)
  await setCloseToQuit(B)
  await login(A, OWNER)
  await login(B, SUBBER)
  await A.waitFor('A sees source profile', () => A.exists(tid(`profile-item-${SRC}`)), 10000)
  check('logged_in_two_accounts', (await loggedIn(A)) && (await loggedIn(B)))

  // ---- 1. A 创建三种分享 ----
  await selectProfile(A, SRC)
  await A.click(tid('nav-shares'))
  await A.waitFor('shares page', () => A.exists(tid('share-create')))
  const fullId = await createShare(A, { name: FULL, kind: 'full', uncheck: ['p2p-echo'] })
  const tplId = await createShare(A, { name: TPL, kind: 'template' })
  const p2pId = await createShare(A, { name: P2P, kind: 'p2p' })
  check('owner_creates_three_kinds', [fullId, tplId, p2pId].every((id) => /^[0-9A-Za-z]{22}$/.test(id ?? '')), { fullId, tplId, p2pId })
  await A.waitFor('share rows', async () => (await A.exists(tid(`share-row-${FULL}`))) && (await A.exists(tid(`share-row-${P2P}`))))
  await shot(A, '01-my-shares.png')

  // ---- 2a. B 订阅 full，真实转发 ----
  const full = await subscribe(B, fullId)
  check('preview_has_no_secrets', !full.modalText.includes(NODE_TOKEN) && !full.modalText.includes('127.0.0.1:' + FRPS_PORT) && full.modalText.includes(OWNER), {
    sample: full.modalText.slice(0, 200),
  })
  await selectProfile(B, FULL)
  check('full_subscription_is_read_only', (await B.attr(tid(`profile-item-${FULL}`), 'data-origin')) === 'subscription' && !(await B.exists(tid('proxy-add'))) && !(await B.exists(tid('node-add'))))
  await B.click(tid(`profile-more-${FULL}`))
  await sleep(300)
  const menu = await B.evaluate(`[...document.querySelectorAll('.menu-item')].filter(e => e.offsetParent).map(e => e.textContent.trim())`)
  await B.click(tid(`profile-more-${FULL}`)) // 再点一次收起菜单
  await B.waitFor('menu closed', async () => !(await B.evaluate(`[...document.querySelectorAll('.menu-item')].some(e => e.offsetParent)`)), 5000)
  check('subscription_menu_only_unsubscribe', JSON.stringify(menu) === JSON.stringify(['取消订阅']), { menu })
  await startTunnel(B, 'B full')
  const fullOk = await echoEventually(FULL_REMOTE)
  await shot(B, '02-full-running.png')
  check('acceptance_full_subscription_forwards', fullOk)
  await stopTunnel(B, 'B full')

  // ---- 2b. B 订阅 template，加自己的代理后真实转发 ----
  await subscribe(B, tplId)
  await selectProfile(B, TPL)
  await B.clickText('.tab', '代理')
  await B.click(tid('proxy-add'))
  await B.waitFor('proxy drawer', () => B.drawerOpen())
  await B.type(tid('proxy-form-name'), 'tpl-echo')
  await B.type(tid('proxy-form-localPort'), ECHO_PORT)
  await B.type(tid('proxy-form-remotePort'), TPL_REMOTE)
  await B.click(tid('proxy-form-save'))
  await B.waitFor('template proxy saved', async () => !(await B.drawerOpen()) && (await B.exists(tid('proxy-row-tpl-echo'))), 15000)
  const tplParams = await api('GET', '/api/v1/state', null, (await api('POST', '/api/v1/auth/login', { nickname: SUBBER, password: PW, deviceName: 'check' })).token)
  const tplSub = tplParams.subscriptions.find((s) => s.shareName === TPL)
  check('template_proxy_saved_as_subscription_params', tplSub?.params?.proxies?.[0]?.name === 'tpl-echo', { params: tplSub?.params })
  await startTunnel(B, 'B template')
  const tplOk = await echoEventually(TPL_REMOTE)
  await shot(B, '03-template-running.png')
  check('acceptance_template_subscription_forwards', tplOk)
  await stopTunnel(B, 'B template')

  // ---- 2c. A 运行 stcp，B 订阅 p2p，visitor 真实连通 ----
  await A.click(tid('nav-tunnel'))
  await selectProfile(A, SRC)
  await startTunnel(A, 'A source')
  await A.waitFor('A stcp running', async () => (await A.attr(tid('proxy-status-p2p-echo'), 'data-phase')) === 'running', 15000).catch(() => {})
  await subscribe(B, p2pId, { port: P2P_BIND })
  await selectProfile(B, P2P)
  await startTunnel(B, 'B p2p')
  const p2pOk = await echoEventually(P2P_BIND, 20000)
  await shot(B, '04-p2p-running.png')
  check('acceptance_p2p_visitor_connects_to_owner_stcp', p2pOk)

  // ---- 订阅人数 ----
  await A.click(tid('nav-shares'))
  await A.click('.shares-page .head button') // 刷新
  await A.waitFor('subscriber counts', async () => (await A.text(tid(`share-subs-${FULL}`))) === '1' && (await A.text(tid(`share-subs-${P2P}`))) === '1', 10000).catch(() => {})
  check('owner_sees_subscriber_counts', (await A.text(tid(`share-subs-${FULL}`))) === '1' && (await A.text(tid(`share-subs-${TPL}`))) === '1' && (await A.text(tid(`share-subs-${P2P}`))) === '1')

  // ---- 3. A 撤销 p2p，B 下一次同步时停止并显示"已撤销" ----
  await A.click(tid(`share-revoke-${P2P}`))
  await A.clickText('.dlg-actions button', '撤销')
  await A.waitFor('revoked state', async () => (await A.text(tid(`share-state-${P2P}`))) === '已撤销', 10000)
  const tRevoke = Date.now()
  let tStopped = null
  try {
    await B.waitFor('B stops revoked p2p', async () => (await B.exists(tid(`profile-revoked-${P2P}`))) && (await runState(B)) === 'idle', 80000)
    tStopped = Date.now()
  } catch {}
  await sleep(500)
  const echoAfter = await echoOk(P2P_BIND)
  await shot(B, '05-p2p-revoked.png')
  result.timings.revokeToSubscriberStoppedMs = tStopped ? tStopped - tRevoke : null
  check('acceptance_revoke_stops_subscriber_and_shows_revoked', tStopped !== null && !echoAfter && (await B.exists(tid('editor-sub-inactive'))), {
    ms: result.timings.revokeToSubscriberStoppedMs,
    echoAfter,
  })
  // 已撤销的不能再启动
  await B.click(tid('run-toggle'))
  await sleep(800)
  check('revoked_subscription_cannot_start', (await runState(B)) !== 'running')

  // ---- 取消订阅 ----
  await B.click(tid(`profile-more-${FULL}`))
  await B.waitFor('menu open', () => B.evaluate(`[...document.querySelectorAll('.menu-item')].some(e => e.offsetParent)`), 5000)
  await B.clickText('.menu-item', '取消订阅')
  await B.clickText('.dlg-actions button', '取消订阅')
  await B.waitFor('full unsubscribed', async () => !(await B.exists(tid(`profile-item-${FULL}`))), 10000)
  check('unsubscribe_removes_entry', true)

  await stopTunnel(A, 'A source')
}

let fatal = null
try {
  await main()
} catch (e) {
  fatal = e
  failures++
  console.error('FATAL', e)
  for (const [name, a] of Object.entries(apps)) await shot(a.page, `fatal-${name}.png`).catch(() => {})
}
for (const name of Object.keys(apps)) await quitApp(name)
if (server) server.kill()
if (frps) frps.kill()
result.finishedAt = new Date().toISOString()
result.failures = failures
result.fatal = fatal ? String(fatal) : null
fs.writeFileSync(path.join(OUT, 'result.json'), JSON.stringify(result, null, 2))
if (!keep) {
  await sleep(500)
  fs.rmSync(T, { recursive: true, force: true })
}
console.error(failures ? `${failures} FAILED` : 'ALL PASSED')
process.exit(failures ? 1 : 0)
