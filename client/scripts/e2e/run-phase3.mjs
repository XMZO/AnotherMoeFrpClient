// PLAN Phase 3（账号与云同步）的端到端验收：在本机起 moefrp-server（临时数据库、127.0.0.1），
// 用两个 MoeFrp-e2e.exe 实例模拟两台机器（各自的 MOEFRP_HOME，单实例锁和凭据目标名也随之区分），
// 通过 WebView2 的 CDP 端口操作真实界面。
//
//   1. 两台机器登录同一个账号：A 上修改，B 在 60 秒内同步到（B 不做任何操作，靠 60 秒轮询）
//   2. 两台机器同时修改同一个配置：弹出冲突提示（含差异摘要），选"保留本机"后两边一致
//   3. 单设备模式：B 登录把 A 挤下线，A 有明确提示
//   另外：注册时前端校验邀请码、离线编辑和启动、恢复后推送、运行中的配置被云端修改时询问是否应用、
//   改密码（旧密码错误不下线）、重启后自动登录、退出登录后不自动登录、重置密码、版本过低、
//   凭据管理器里只有令牌没有密码、数据目录里找不到密码。
//
// 前提：已用 `wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e` 构建出 bin\MoeFrp-e2e.exe。
// 用法：node scripts/e2e/run-phase3.mjs [--keep]
// 会临时占用 127.0.0.1 的 18080（服务端）、17000–17010（localfrps 和远程端口）、9361/9362（CDP）。
// 结束后删除凭据管理器里的测试凭据（MoeFrp/session/e2e-*）和临时数据目录。
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
const OUT = path.join(here, 'out-phase3')
const T = path.join(os.tmpdir(), 'moefrp-e2e3-' + new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14))
const SERVER_PORT = 18080
const API = `http://127.0.0.1:${SERVER_PORT}`
const FRPS_PORT = 17000
const ECHO_PORT = 17001
const CRED_PREFIX = 'MoeFrp/session/e2e-'
const NICK = 'e2euser'
const PW1 = 'Pw-first-8chars'
const PW2 = 'Pw-second-8chars'
const PW3 = 'Pw-third-8chars'
const CLOUD = '我的云端配置'

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
const credtool = path.join(T, 'credtool.exe')
const localfrps = path.join(T, 'localfrps.exe')
log('building server, credtool, localfrps')
execFileSync('go', ['build', '-o', serverExe, './cmd/moefrp-server'], { cwd: path.join(gogogo, 'server'), stdio: 'inherit' })
execFileSync('go', ['build', '-o', credtool, './scripts/e2e/credtool'], { cwd: client, stdio: 'inherit' })
execFileSync('go', ['build', '-o', localfrps, './cmd/localfrps'], { cwd: client, stdio: 'inherit' })

const dbPath = path.join(T, 'server', 'moefrp.db')
fs.mkdirSync(path.dirname(dbPath), { recursive: true })
function serverEnv(opts = {}) {
  return {
    ...process.env,
    MOEFRP_LISTEN: `127.0.0.1:${SERVER_PORT}`,
    MOEFRP_DB_PATH: dbPath,
    MOEFRP_SINGLE_SESSION: opts.single ? 'true' : 'false',
    MOEFRP_CLIENT_LATEST_VERSION: opts.latest ?? '3.0.0',
    MOEFRP_CLIENT_MIN_VERSION: opts.min ?? '3.0.0-dev',
    MOEFRP_CLIENT_DOWNLOAD_URL: 'https://example.invalid/MoeFrp-new.exe',
    MOEFRP_LOG_LEVEL: 'info',
    MOEFRP_ADMIN_SETUP_CODE: ADMIN_SETUP_CODE,
  }
}
let server = null
async function startServer(opts) {
  const logFile = fs.openSync(path.join(OUT, 'server.log'), 'a')
  server = spawn(serverExe, ['serve', '--auto-migrate'], { env: serverEnv(opts), stdio: ['ignore', logFile, logFile] })
  await waitUntil('server healthz', async () => (await fetch(`${API}/healthz`)).ok, 15000, 100)
  log('server up', JSON.stringify(opts))
}
async function stopServer() {
  if (!server) return
  const p = server
  server = null
  p.kill()
  await new Promise((r) => (p.exitCode !== null ? r() : p.once('exit', r)))
  await waitUntil('server port free', () => portFree(SERVER_PORT), 10000, 100)
  log('server down')
}
const admin = adminClient(API)
function portFree(port) {
  return new Promise((resolve) => {
    const s = net.createServer()
    s.once('error', () => resolve(false))
    s.listen(port, '127.0.0.1', () => s.close(() => resolve(true)))
  })
}
function creds() {
  return JSON.parse(execFileSync(credtool, ['list', CRED_PREFIX], { encoding: 'utf8' }))
}
function credContains(target, secret) {
  return JSON.parse(execFileSync(credtool, ['check', target, secret], { encoding: 'utf8' }))
}

// ---------- 客户端实例 ----------
const apps = {}
function diagLines(name) {
  const f = path.join(OUT, `diag-${name}.log`)
  return fs.existsSync(f) ? fs.readFileSync(f, 'utf8').split(/\r?\n/) : []
}
const diagCount = (name, re) => diagLines(name).filter((l) => re.test(l)).length

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
  apps[name] = { proc, page, cdpPort }
  return page
}

async function quitApp(name) {
  const a = apps[name]
  if (!a) return
  a.page.close()
  // 不带 /F：向窗口发 WM_CLOSE；设置里已把关闭行为改成"退出程序"，走正常的退出流程
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
  return exited
}
function alive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

// ---------- 界面操作 ----------
const shot = async (p, name) => {
  await sleep(400) // 等弹窗的淡入动画结束
  return p.shot(path.join(OUT, name))
}
const syncState = (p) => p.attr(tid('sync-status'), 'data-state')
const loggedIn = async (p) => (await p.attr(tid('account-area'), 'data-logged-in')) === '1'

async function setCloseToQuit(p) {
  await p.click(tid('nav-settings'))
  await p.clickText('.radio', '退出程序')
  await sleep(300)
  await p.click(tid('nav-tunnel'))
}

async function openAuth(p, tab) {
  await p.click(tid('account-login'))
  await p.waitFor('auth modal', () => p.exists(tid('login-nickname')))
  await p.clickText('.tab', tab)
  await sleep(150)
}

async function setCheckbox(p, testid, want) {
  const checked = await p.evaluate(`document.querySelector('[data-testid=${testid}]')?.getAttribute('aria-checked') === 'true'`)
  if (!!checked !== want) await p.click(tid(testid))
}

async function login(p, pw, keepLogged = true) {
  await openAuth(p, '登录')
  await p.type(tid('login-nickname'), NICK)
  await p.type(tid('login-password'), pw)
  await setCheckbox(p, 'login-keep', keepLogged)
  await p.click(tid('login-submit'))
  await p.waitFor('logged in', () => loggedIn(p), 15000)
  await p.waitFor('synced after login', async () => (await syncState(p)) === 'synced', 20000)
}

async function logout(p) {
  await p.click(tid('account-menu'))
  await p.clickText('.menu-item', '退出登录')
  await p.clickText('.dlg-actions button', '退出登录')
  await p.waitFor('logged out', async () => !(await loggedIn(p)), 15000)
}

async function selectProfile(p, name) {
  await p.click(`${tid(`profile-item-${name}`)} .pitem-main`) // testid 在外层，点击处理在里面的按钮上
  await p.waitFor(`editor shows ${name}`, async () => (await p.text('.editor .title'))?.startsWith(name))
}

async function openTab(p, label) {
  await p.clickText('.tab', label)
  await sleep(150)
}

async function addNode(p, remark, port, token = 'e2e-token') {
  await openTab(p, '节点')
  await p.click(tid('node-add'))
  await p.waitFor('node drawer', () => p.drawerOpen())
  await p.type(tid('node-form-remark'), remark)
  await p.type(tid('node-form-serverAddr'), '127.0.0.1')
  await p.type(tid('node-form-serverPort'), port)
  await p.type(tid('node-form-token'), token)
  await p.click(tid('node-form-save'))
  await p.waitFor('node drawer closed', async () => !(await p.drawerOpen()))
}

async function addProxy(p, name, localPort, remotePort) {
  await openTab(p, '代理')
  await p.click(tid('proxy-add'))
  await p.waitFor('proxy drawer', () => p.drawerOpen())
  await p.type(tid('proxy-form-name'), name)
  await p.type(tid('proxy-form-localPort'), localPort)
  if (remotePort) await p.type(tid('proxy-form-remotePort'), remotePort)
  await p.click(tid('proxy-form-save'))
  await p.waitFor(`proxy ${name} saved`, async () => !(await p.drawerOpen()) && (await p.exists(tid(`proxy-row-${name}`))))
}

const proxyNames = (p) => p.evaluate(`[...document.querySelectorAll('[data-testid^="proxy-row-"]')].map(e => e.getAttribute('data-testid').slice(10))`)
const profileMeta = (p, name) => p.text(`${tid(`profile-item-${name}`)} .meta`)

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

/** 数据目录里有没有出现密码（UTF-8 或 UTF-16LE）。 */
function scanForSecrets(dir, secrets) {
  const hits = []
  const needles = secrets.flatMap((s) => [Buffer.from(s, 'utf8'), Buffer.from(s, 'utf16le')])
  let files = 0
  const walk = (d) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const f = path.join(d, e.name)
      if (e.isDirectory()) walk(f)
      else {
        let data
        try {
          data = fs.readFileSync(f)
        } catch {
          continue
        }
        files++
        if (needles.some((n) => data.includes(n))) hits.push(path.relative(dir, f))
      }
    }
  }
  walk(dir)
  return { files, hits }
}

// ---------- 场景 ----------
let frps = null
async function main() {
  for (const port of [SERVER_PORT, FRPS_PORT, ECHO_PORT, 9361, 9362]) {
    if (!(await portFree(port))) throw new Error(`port ${port} is in use`)
  }
  if (creds().length) throw new Error('leftover test credentials found: ' + JSON.stringify(creds()))

  execFileSync(serverExe, ['migrate'], { env: serverEnv(), stdio: 'ignore' })
  await startServer({ single: false })
  const invites = await admin.createInvites(2)
  check('admin_invite_created', invites.length === 2, { count: invites.length })

  frps = spawn(localfrps, ['-port', String(FRPS_PORT), '-token', 'e2e-token', '-echo', `127.0.0.1:${ECHO_PORT}`], { stdio: 'ignore' })
  await waitUntil('localfrps', async () => !(await portFree(FRPS_PORT)), 10000, 100)

  // ---- 两个实例同时运行（单实例锁按数据目录区分）----
  const A = await startApp('A', 9361)
  const B = await startApp('B', 9362)
  check('two_instances_run_side_by_side', alive(apps.A.proc.pid) && alive(apps.B.proc.pid))
  await setCloseToQuit(A)
  await setCloseToQuit(B)

  // ---- 注册：前端校验邀请码格式和校验位 ----
  await openAuth(A, '注册')
  await A.type(tid('reg-nickname'), NICK)
  await A.type(tid('reg-password'), PW1)
  await A.type(tid('reg-password2'), PW1)
  const good = invites[0]
  const badCheck = good.slice(0, 13) + (good[13] === 'A' ? 'B' : 'A')
  await A.type(tid('reg-invite'), badCheck)
  await sleep(150)
  const badState = await A.attr(tid('reg-invite-state'), 'data-state')
  await A.type(tid('reg-invite'), 'FRPT-12')
  await sleep(150)
  const fmtState = await A.attr(tid('reg-invite-state'), 'data-state')
  await A.type(tid('reg-invite'), badCheck)
  await A.click(tid('reg-submit'))
  await sleep(500)
  const blockedText = await A.text(tid('auth-error'))
  await shot(A, '01-register-invite-checksum.png')
  check('register_frontend_rejects_bad_checksum', badState === 'checksum' && fmtState === 'invalid' && /校验位/.test(blockedText ?? ''), {
    badState,
    fmtState,
    blockedText,
  })
  await A.type(tid('reg-invite'), good.toLowerCase())
  await sleep(150)
  check('register_frontend_accepts_valid_code', (await A.attr(tid('reg-invite-state'), 'data-state')) === '')
  await A.click(tid('reg-submit'))
  await A.waitFor('switched to login tab', async () => (await A.evaluate(`document.querySelector('input[data-testid=login-nickname]')?.value`)) === NICK)
  check('register_ok', true)
  // 关掉弹窗后从头走登录
  await A.evaluate(`document.querySelector('.modal-close')?.click()`)
  await sleep(300)

  // ---- A、B 登录（保持登录），A 上建本地配置 ----
  await login(A, PW1, true)
  await A.waitFor('default cloud profile', () => A.exists(tid(`profile-item-${CLOUD}`)), 10000)
  check('default_cloud_profile_created', (await A.attr(tid(`profile-item-${CLOUD}`), 'data-origin')) === 'cloud')
  const credA = creds()
  check('keep_logged_in_stores_token_only', credA.length === 1 && credA[0].tokenLike && credA[0].userName === NICK && !credContains(credA[0].target, PW1).containsSecret, {
    creds: credA,
  })

  await login(B, PW1, true)
  await B.waitFor('B sees cloud profile', () => B.exists(tid(`profile-item-${CLOUD}`)), 10000)
  check('two_devices_logged_in_same_account', (await loggedIn(A)) && (await loggedIn(B)) && creds().length === 2)

  // B 上建一个本地配置（之后验证退出登录时本地配置保留）
  await B.click(tid('profile-new'))
  await B.type(tid('profile-name-input'), 'B的本地配置')
  await B.clickText('.radio', '本地配置')
  await B.clickText('.dlg-actions button', '确定')
  await B.waitFor('local profile', () => B.exists(tid('profile-item-B的本地配置')))
  check('local_and_cloud_coexist', (await B.attr(tid('profile-item-B的本地配置'), 'data-origin')) === 'local' && (await B.exists(tid('profile-group-cloud'))))
  await selectProfile(B, CLOUD)
  await shot(B, '02-cloud-and-local.png')

  // ---- 验收 1：A 上修改，B 在 60 秒内同步到 ----
  await selectProfile(A, CLOUD)
  const tEdit = Date.now()
  await addNode(A, '本机frps', FRPS_PORT)
  await A.waitFor('A pushed', async () => (await syncState(A)) === 'synced' && !(await A.exists(tid('sync-pending'))) && (await A.attr(tid(`profile-item-${CLOUD}`), 'data-sync')) === 'synced', 15000)
  const tPushed = Date.now()
  const bPullsBefore = diagCount('B', /sync: pulled/)
  let tB = null
  try {
    await B.waitFor('B receives node', async () => /节点 1/.test((await profileMeta(B, CLOUD)) ?? ''), 75000)
    tB = Date.now()
  } catch {}
  const bPull = diagLines('B').filter((l) => /sync: pulled/.test(l)).slice(bPullsBefore)
  const trigger = (bPull[0]?.match(/trigger=(\w+)/) ?? [])[1]
  result.timings.acceptance1 = { editToPushedMs: tPushed - tEdit, pushedToBMs: tB ? tB - tPushed : null, editToBMs: tB ? tB - tEdit : null, bTrigger: trigger }
  check('acceptance1_edit_on_A_reaches_B_within_60s', tB !== null && tB - tPushed <= 60000, result.timings.acceptance1)
  // bTrigger=timer 表示靠 60 秒轮询；focus 表示 B 的窗口恰好获得了焦点（例如桌面上有人操作），两种都是设计行为
  await B.waitFor('B editor refreshed', async () => /节点（1）/.test((await B.text('.editor')) ?? ''), 5000).catch(() => {})
  await shot(B, '03-b-received.png')

  // ---- 验收 2：两边同时修改同一个配置 → 冲突提示 ----
  await selectProfile(A, CLOUD)
  await selectProfile(B, CLOUD)
  await Promise.all([addProxy(A, 'fromA', ECHO_PORT, 17002), addProxy(B, 'fromB', ECHO_PORT, 17003)])
  let loser = null
  await waitUntil('conflict dialog on A or B', async () => {
    if (await A.exists(tid('conflict-dialog'))) loser = 'A'
    else if (await B.exists(tid('conflict-dialog'))) loser = 'B'
    return loser
  }, 75000, 100)
  const L = loser === 'A' ? A : B
  const W = loser === 'A' ? B : A
  const mine = loser === 'A' ? 'fromA' : 'fromB'
  const theirs = loser === 'A' ? 'fromB' : 'fromA'
  const diffText = await L.text(tid('conflict-diff'))
  await shot(L, '04-conflict.png')
  const winnerHasDialog = await W.exists(tid('conflict-dialog'))
  check('acceptance2_concurrent_edit_shows_conflict', !!loser && !winnerHasDialog && diffText?.includes(`代理「${mine}」：只在本机`) && diffText?.includes(`代理「${theirs}」：只在云端`), {
    loser,
    diffText,
    conflictName: await L.text(tid('conflict-name')),
  })
  await L.click(tid('conflict-keep-local'))
  await L.waitFor('conflict closed', async () => !(await L.exists(tid('conflict-dialog'))))
  await L.waitFor('loser synced', async () => (await L.attr(tid(`profile-item-${CLOUD}`), 'data-sync')) === 'synced' && (await syncState(L)) === 'synced', 15000)
  const tResolved = Date.now()
  await W.waitFor('winner gets loser version', async () => {
    const names = await proxyNames(W)
    return names.includes(mine) && !names.includes(theirs)
  }, 75000).catch(() => {})
  const wNames = await proxyNames(W)
  result.timings.acceptance2 = { resolvedToWinnerMs: Date.now() - tResolved }
  check('acceptance2_keep_local_converges', wNames.includes(mine) && !wNames.includes(theirs) && (await proxyNames(L)).join() === wNames.join(), {
    winner: wNames,
    loser: await proxyNames(L),
  })

  // ---- 离线：编辑和启动都照常，恢复后推送 ----
  await stopServer()
  await selectProfile(A, CLOUD)
  await addProxy(A, 'offlineEdit', ECHO_PORT, 17004)
  await A.waitFor('A offline', async () => (await syncState(A)) === 'offline', 15000)
  const pendingBadge = await A.text(tid(`profile-sync-${CLOUD}`))
  await A.click(tid('run-toggle'))
  await A.waitFor('tunnel running offline', async () => (await A.attr(tid('run-state'), 'data-state')) === 'running', 15000)
  await A.waitFor('offlineEdit proxy running', async () => (await A.attr(tid('proxy-status-offlineEdit'), 'data-phase')) === 'running', 15000).catch(() => {})
  const offlineEcho = await echoOk(17004)
  await shot(A, '05-offline.png')
  check('offline_shows_status_and_allows_edit_and_start', pendingBadge === '待同步' && offlineEcho, { pendingBadge, offlineEcho, syncStatusText: await A.text(tid('sync-status')) })

  await startServer({ single: true }) // 之后都是单设备模式
  const tBack = Date.now()
  await A.waitFor('A pushes after reconnect', async () => (await syncState(A)) === 'synced' && (await A.attr(tid(`profile-item-${CLOUD}`), 'data-sync')) === 'synced', 40000).catch(() => {})
  result.timings.offlineRecoveryMs = Date.now() - tBack
  check('offline_edits_pushed_after_reconnect', (await syncState(A)) === 'synced', { ms: result.timings.offlineRecoveryMs })

  // ---- 运行中的配置被云端修改：询问是否应用（热更新）----
  await B.waitFor('B receives offline edit', async () => (await proxyNames(B)).includes('offlineEdit'), 75000)
  await addProxy(B, 'fromB2', ECHO_PORT, 17005)
  const runIdBefore = await A.evaluate(`document.querySelector('[data-testid=run-state]')?.getAttribute('data-run-id') ?? ''`)
  const loginsBefore = diagCount('A', /tunnel state: running.*logins=1/)
  await A.waitFor('remote update notice', () => A.exists(tid('remote-update-apply')), 75000)
  await shot(A, '06-remote-update.png')
  await A.click(tid('remote-update-apply'))
  await A.waitFor('fromB2 running on A', async () => (await A.attr(tid('proxy-status-fromB2'), 'data-phase')) === 'running', 15000).catch(() => {})
  const hotToast = (await A.toasts()).join(' | ')
  const echo5 = await echoOk(17005)
  check('running_profile_remote_update_asks_then_hot_updates', echo5 && /热更新/.test(hotToast) && !diagLines('A').some((l) => /logins=2/.test(l)), {
    echo5,
    hotToast,
    runIdBefore,
    loginsBefore,
  })

  // ---- 退出登录：清掉云端缓存、本地配置保留、凭据删除 ----
  await logout(B)
  await sleep(500)
  const bCloudGone = !(await B.exists(tid(`profile-item-${CLOUD}`))) && !(await B.exists(tid('profile-group-cloud')))
  check('logout_clears_cloud_cache_keeps_local', bCloudGone && (await B.exists(tid('profile-item-B的本地配置'))) && creds().length === 1, { creds: creds() })

  // ---- 验收 3：单设备模式下 B 登录挤掉 A ----
  await login(B, PW1, true)
  const tKick = Date.now()
  await A.waitFor('A kicked', () => A.exists(tid('session-ended-dialog')), 75000)
  result.timings.acceptance3 = { kickToNoticeMs: Date.now() - tKick }
  const endedCode = await A.attr(tid('session-ended-dialog'), 'data-code')
  const endedText = await A.text(tid('session-ended-text'))
  await shot(A, '07-session-replaced.png')
  const aCred = creds().filter((c) => c.userName === NICK)
  check('acceptance3_single_device_kicks_A_with_clear_notice', endedCode === 'session_replaced' && /其他设备登录/.test(endedText ?? '') && !(await loggedIn(A)), {
    endedCode,
    endedText,
    ...result.timings.acceptance3,
  })
  check('kicked_device_credential_deleted', aCred.length === 1, { creds: aCred })
  await A.click(tid('session-ended-ok'))
  const aStillRunning = (await A.attr(tid('run-state'), 'data-state')) === 'running' && (await echoOk(17004))
  check('kicked_device_keeps_running_cached_profile', aStillRunning && (await A.exists(tid(`profile-item-${CLOUD}`))), {
    hint: await A.text(tid('profile-group-hint-cloud')),
  })

  // ---- 改密码：旧密码错误不下线；正确后成功 ----
  await B.click(tid('account-menu'))
  await B.clickText('.menu-item', '修改密码')
  await B.type(tid('cp-old'), 'definitely-wrong')
  await B.type(tid('cp-new'), PW2)
  await B.type(tid('cp-new2'), PW2)
  await B.clickText('.dlg-actions button', '修改')
  await B.waitFor('cp error', () => B.exists(tid('cp-error')), 10000)
  const cpErr = await B.text(tid('cp-error'))
  check('change_password_wrong_old_stays_logged_in', /当前密码不正确/.test(cpErr ?? '') && (await loggedIn(B)), { cpErr })
  await B.type(tid('cp-old'), PW1)
  await B.clickText('.dlg-actions button', '修改')
  await B.waitFor('cp done', async () => !(await B.exists(tid('cp-old'))), 10000)
  check('change_password_ok', (await loggedIn(B)) && (await B.toasts()).some((t) => /密码已修改/.test(t)))

  // ---- 重启后自动登录 ----
  check('quit_B_gracefully', await quitApp('B'))
  const B2 = await startApp('B', 9362)
  await B2.waitFor('auto login', () => loggedIn(B2), 15000).catch(() => {})
  await B2.waitFor('synced', async () => (await syncState(B2)) === 'synced', 20000).catch(() => {})
  check('restart_auto_login', (await loggedIn(B2)) && (await B2.text(tid('account-nickname'))) === NICK && (await syncState(B2)) === 'synced')
  await shot(B2, '08-auto-login.png')

  // ---- 退出登录后重启：不自动登录 ----
  await logout(B2)
  check('logout_deletes_credential', creds().length === 0, { creds: creds() })
  await quitApp('B')
  const B3 = await startApp('B', 9362)
  await sleep(3000)
  check('no_auto_login_after_logout', !(await loggedIn(B3)) && (await B3.exists(tid('account-login'))) && (await B3.exists(tid('profile-item-B的本地配置'))))

  // ---- 重置密码 ----
  const resetCode = await admin.resetCode(NICK)
  await openAuth(B3, '重置密码')
  await B3.type(tid('reset-nickname'), NICK)
  await B3.type(tid('reset-code'), resetCode)
  await B3.type(tid('reset-password'), PW3)
  await B3.type(tid('reset-password2'), PW3)
  await B3.click(tid('reset-submit'))
  await B3.waitFor('reset done', async () => (await B3.toasts()).some((t) => /密码已重置/.test(t)), 10000)
  check('reset_password_ok', true, { codeFormat: resetCode.replace(/[A-Z0-9]/g, 'X') })
  await B3.evaluate(`document.querySelector('.modal-close')?.click()`)
  await sleep(300)
  await login(B3, PW3, false)
  check('reset_password_then_login', await loggedIn(B3))
  check('login_without_keep_stores_nothing', creds().length === 0, { creds: creds() })

  // ---- 版本过低：同步停止，提示下载；本机缓存的配置照样能启动 ----
  await stopServer()
  await startServer({ single: true, min: '9.0.0', latest: '9.0.0' })
  await B3.click(tid('account-menu'))
  await B3.clickText('.menu-item', '立即同步')
  await B3.waitFor('outdated dialog', () => B3.exists(tid('outdated-dialog')), 30000).catch(() => {})
  await shot(B3, '09-outdated.png')
  const outdatedShown = await B3.exists(tid('outdated-dialog'))
  await B3.click(tid('outdated-later')).catch(() => {})
  await selectProfile(B3, CLOUD)
  if ((await A.attr(tid('run-state'), 'data-state')) === 'running') {
    await A.click(tid('run-toggle')) // 先停掉 A，免得两边的代理在 frps 上撞名
    await A.waitFor('A stopped', async () => (await A.attr(tid('run-state'), 'data-state')) === 'idle', 10000)
  }
  await B3.click(tid('run-toggle'))
  await B3.waitFor('B runs while outdated', async () => (await B3.attr(tid('run-state'), 'data-state')) === 'running', 15000).catch(() => {})
  check('client_outdated_notice_and_local_start_still_works', outdatedShown && (await syncState(B3)) === 'outdated' && (await B3.attr(tid('run-state'), 'data-state')) === 'running', {
    sync: await syncState(B3),
  })
  await B3.click(tid('run-toggle'))

  // ---- 凭据管理器和数据目录里都没有密码 ----
  const scanA = scanForSecrets(path.join(T, 'A'), [PW1, PW2, PW3])
  const scanB = scanForSecrets(path.join(T, 'B'), [PW1, PW2, PW3])
  check('no_password_in_data_dirs', scanA.hits.length === 0 && scanB.hits.length === 0, { scanA, scanB })
}

async function cleanup() {
  for (const n of Object.keys(apps)) await quitApp(n).catch(() => {})
  await stopServer().catch(() => {})
  if (frps) frps.kill()
  await sleep(500)
  let left = []
  try {
    execFileSync(credtool, ['delete', CRED_PREFIX], { stdio: 'ignore' })
    left = creds()
  } catch (e) {
    left = ['credtool failed: ' + e]
  }
  result.cleanup = { credentialsLeft: left }
  check('cleanup_test_credentials_removed', left.length === 0, { left })
  if (!keep) {
    for (let i = 0; i < 10; i++) {
      try {
        fs.rmSync(T, { recursive: true, force: true })
        break
      } catch {
        await sleep(500)
      }
    }
    result.cleanup.dataRootRemoved = !fs.existsSync(T)
    check('cleanup_temp_data_removed', result.cleanup.dataRootRemoved)
  }
}

try {
  await main()
} catch (e) {
  failures++
  result.error = String(e?.stack ?? e)
  console.error('ERROR', e)
  for (const [n, a] of Object.entries(apps)) await shot(a.page, `error-${n}.png`).catch(() => {})
} finally {
  await cleanup()
  result.finishedAt = new Date().toISOString()
  result.ok = failures === 0
  fs.writeFileSync(path.join(OUT, 'phase3-result.json'), JSON.stringify(result, null, 2))
  console.log(JSON.stringify({ ok: result.ok, failures, timings: result.timings }, null, 2))
  process.exit(failures === 0 ? 0 : 1)
}
