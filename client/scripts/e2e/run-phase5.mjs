// PLAN Phase 5（体验完善）的端到端验收，用 MoeFrp-e2e.exe（不登录）：
//
//   1. 公告、新版本提示（服务端配置 latest_version 高于客户端）；"关于"页检查更新、开源许可
//   2. 图片面板：没登录时不显示、不发图片请求；登录后图源由服务端下发，图片显示出来，右键菜单，"换一张"，关闭后面板消失
//   3. 自定义网络代理：地址不合法时报错；合法时图片请求走代理（只有代理能解析的域名也能取到图）
//   4. 隐藏到托盘时释放 WebView2：关窗后 msedgewebview2 退出、进程树内存下降、隧道照常运行；
//      再次启动 exe（单实例）重新创建窗口，状态正常
//   5. 开机自启：开关写入、删除 HKCU\...\Run 下的值（端到端构建的值名带数据目录哈希）
//   6. 启动后自动连接：重启后隧道自动运行
//   7. 以 --autostart 启动：只有托盘、不创建窗口、自动连接照常；再次启动 exe 才出现窗口
//
// 前提：已构建 bin\MoeFrp-e2e.exe。图片面板要登录，第 2 步起是登录状态（账号通过管理接口发邀请码注册）。
// 用法：node scripts/e2e/run-phase5.mjs [--keep]
// 占用 127.0.0.1 的 18082（服务端）、18095（图片）、18096（HTTP 代理）、17200–17202（frps、echo、远程端口）、9381（CDP）。
import { spawn, execFileSync } from 'node:child_process'
import fs from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import zlib from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { connect, sleep, tid, waitUntil } from './cdp.mjs'
import { ADMIN_SETUP_CODE, adminClient } from './adminapi.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
const client = path.resolve(here, '..', '..')
const gogogo = path.resolve(client, '..')
const keep = process.argv.includes('--keep')
const EXE = path.join(client, 'bin', 'MoeFrp-e2e.exe')
const OUT = path.join(here, 'out-phase5')
const T = path.join(os.tmpdir(), 'moefrp-e2e5-' + new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14))
const SERVER_PORT = 18082
const IMG_PORT = 18095
const PROXY_PORT = 18096
const FRPS_PORT = 17200
const ECHO_PORT = 17201
const REMOTE_PORT = 17202
const CDP = 9381
const TOKEN = 'e2e-phase5-token'
const NICK = 'phase5user'
const PASSWORD = 'phase5-password-1'
const ANNOUNCE = 'e2e 公告：服务器今晚维护'
const RUN_KEY = 'HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run'

fs.mkdirSync(OUT, { recursive: true })
for (const f of fs.readdirSync(OUT)) fs.rmSync(path.join(OUT, f), { force: true, recursive: true })
fs.mkdirSync(T, { recursive: true })

const result = { startedAt: new Date().toISOString(), dataRoot: T, checks: {}, measurements: {} }
let failures = 0
function check(name, ok, extra) {
  result.checks[name] = extra === undefined ? !!ok : { ok: !!ok, ...extra }
  if (!ok) failures++
  console.error(`${ok ? 'PASS' : 'FAIL'} ${name}${extra ? ' ' + JSON.stringify(extra) : ''}`)
}
const log = (...a) => console.error('--', ...a)

// ---------- 小 PNG ----------
function crc32(buf) {
  return zlib.crc32(buf) >>> 0
}
function chunk(type, data) {
  const len = Buffer.alloc(4)
  len.writeUInt32BE(data.length)
  const td = Buffer.concat([Buffer.from(type), data])
  const crc = Buffer.alloc(4)
  crc.writeUInt32BE(crc32(td))
  return Buffer.concat([len, td, crc])
}
function png(r, g, b) {
  const w = 32
  const h = 24
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0)
  ihdr.writeUInt32BE(h, 4)
  ihdr[8] = 8
  ihdr[9] = 2
  const raw = Buffer.alloc((w * 3 + 1) * h)
  for (let y = 0; y < h; y++) {
    raw[y * (w * 3 + 1)] = 0
    for (let x = 0; x < w; x++) raw.set([r, (g + x * 7) & 255, (b + y * 9) & 255], y * (w * 3 + 1) + 1 + x * 3)
  }
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

// ---------- 图片服务、HTTP 代理 ----------
let imgRequests = 0
const imgServer = http.createServer((req, res) => {
  imgRequests++
  if (req.url.startsWith('/api')) {
    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify({ data: [{ url: `http://127.0.0.1:${IMG_PORT}/rand.png?api` }] }))
    return
  }
  res.setHeader('Content-Type', 'image/png')
  res.end(png(Math.floor(Math.random() * 255), Math.floor(Math.random() * 255), Math.floor(Math.random() * 255)))
})
const proxied = []
const proxyServer = http.createServer((req, res) => {
  // 只处理普通 HTTP 代理请求（绝对 URI），把 *.e2e.test 解析到本机
  let u
  try {
    u = new URL(req.url)
  } catch {
    res.statusCode = 400
    return res.end()
  }
  proxied.push(u.host)
  const host = u.hostname.endsWith('.e2e.test') ? '127.0.0.1' : u.hostname
  const up = http.request({ host, port: u.port || 80, path: u.pathname + u.search, method: req.method, headers: req.headers }, (r) => {
    res.writeHead(r.statusCode, r.headers)
    r.pipe(res)
  })
  up.on('error', () => {
    res.statusCode = 502
    res.end()
  })
  req.pipe(up)
})

// ---------- 进程 ----------
const serverExe = path.join(T, 'moefrp-server.exe')
const localfrps = path.join(T, 'localfrps.exe')
const credtool = path.join(T, 'credtool.exe')
const CRED_PREFIX = 'MoeFrp/session/e2e-'
log('building server, credtool, localfrps')
execFileSync('go', ['build', '-o', credtool, './scripts/e2e/credtool'], { cwd: client, stdio: 'inherit' })
execFileSync('go', ['build', '-o', serverExe, './cmd/moefrp-server'], { cwd: path.join(gogogo, 'server'), stdio: 'inherit' })
execFileSync('go', ['build', '-o', localfrps, './cmd/localfrps'], { cwd: client, stdio: 'inherit' })
const cfgFile = path.join(T, 'server.toml')
fs.writeFileSync(
  cfgFile,
  `listen = "127.0.0.1:${SERVER_PORT}"
db_path = ${JSON.stringify(path.join(T, 'server', 'moefrp.db'))}
single_session = false
[client]
latest_version = "3.9.0"
min_version = "3.0.0-dev"
download_url = "https://example.invalid/MoeFrp-3.9.0.exe"
release_notes = "e2e 更新说明"
announcement = ${JSON.stringify(ANNOUNCE)}
[[client.image_sources]]
url = "http://img.e2e.test:${IMG_PORT}/rand.png?proxy"
weight = 100
[[client.image_sources]]
url = "http://127.0.0.1:${IMG_PORT}/rand.png"
weight = 1
[[client.image_sources]]
url = "http://127.0.0.1:${IMG_PORT}/api"
weight = 1
is_api = true
json_path = "data.0.url"
`,
)
let server = null
let frps = null

function portFree(port) {
  return new Promise((resolve) => {
    const s = net.createServer()
    s.once('error', () => resolve(false))
    s.listen(port, '127.0.0.1', () => s.close(() => resolve(true)))
  })
}
function alive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}
const diagFile = path.join(OUT, 'diag.log')
const diagLines = () => (fs.existsSync(diagFile) ? fs.readFileSync(diagFile, 'utf8').split(/\r?\n/) : [])
const diagCount = (re) => diagLines().filter((l) => re.test(l)).length
const appEnv = () => ({
  ...process.env,
  MOEFRP_HOME: path.join(T, 'A'),
  MOEFRP_API_BASE: `http://127.0.0.1:${SERVER_PORT}`,
  MOEFRP_DIAG: diagFile,
  MOEFRP_CDP_PORT: String(CDP),
})

let appProc = null
let page = null
function launch(args = []) {
  const p = spawn(EXE, args, { env: appEnv(), stdio: 'ignore', detached: true })
  p.unref()
  return p
}
async function startApp(args = []) {
  const ready = diagCount(/frontend ready/)
  appProc = launch(args)
  await waitUntil('frontend ready', () => diagCount(/frontend ready/) > ready, 30000, 100)
  page = await connect(CDP, 'A')
  await page.waitFor('account area', () => page.exists(tid('account-area')))
  return page
}
async function reconnect() {
  if (page) page.close()
  page = await connect(CDP, 'A')
  await page.waitFor('account area', () => page.exists(tid('account-area')))
  return page
}
async function quitApp() {
  if (page) page.close()
  page = null
  if (!appProc) return
  // 设置为"退出程序"后发 WM_CLOSE
  try {
    execFileSync('taskkill', ['/PID', String(appProc.pid)], { stdio: 'ignore' })
  } catch {}
  const ok = await waitUntil('app exit', () => !alive(appProc.pid), 10000, 100).then(() => true, () => false)
  if (!ok) {
    try {
      execFileSync('taskkill', ['/F', '/T', '/PID', String(appProc.pid)], { stdio: 'ignore' })
    } catch {}
  }
  appProc = null
}

/** 进程树（含 msedgewebview2 子进程）的工作集总和和子进程数。 */
function treeMemory(pid) {
  const ps = `$all = Get-CimInstance Win32_Process; $ids = New-Object System.Collections.Generic.List[int]; $ids.Add(${pid}); ` +
    `for ($i = 0; $i -lt $ids.Count; $i++) { $all | Where-Object { $_.ParentProcessId -eq $ids[$i] } | ForEach-Object { $ids.Add([int]$_.ProcessId) } }; ` +
    `$p = Get-Process -Id $ids -ErrorAction SilentlyContinue; ` +
    `@{ ws = ($p | Measure-Object WorkingSet64 -Sum).Sum; count = $p.Count; webview = @($p | Where-Object { $_.ProcessName -like 'msedgewebview2*' }).Count } | ConvertTo-Json -Compress`
  return JSON.parse(execFileSync('pwsh', ['-NoProfile', '-Command', ps], { encoding: 'utf8' }))
}

function runValues() {
  try {
    const out = execFileSync('reg', ['query', RUN_KEY], { encoding: 'utf8' })
    return out.split(/\r?\n/).filter((l) => /MoeFrp-e2e-/.test(l)).map((l) => l.trim())
  } catch {
    return []
  }
}

function echoOnce(port, msg) {
  return new Promise((resolve, reject) => {
    const s = net.connect(port, '127.0.0.1')
    let buf = ''
    s.setTimeout(3000, () => {
      s.destroy()
      reject(new Error('timeout'))
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
const echoEventually = (port, timeout = 15000) => waitUntil(`echo ${port}`, () => echoOk(port), timeout, 300).then(() => true, () => false)

const shot = async (name) => {
  await sleep(400)
  return page.shot(path.join(OUT, name))
}
const runState = () => page.attr(tid('run-state'), 'data-state')
async function setSetting(label) {
  await page.click(tid('nav-settings'))
  await page.clickText('.radio', label)
  await sleep(300)
}
const imgId = () => page.attr(tid('image-panel-img'), 'data-id')
const imgLoaded = () => page.evaluate(`(() => { const i = document.querySelector('[data-testid=image-panel-img]'); return !!i && i.complete && i.naturalWidth > 0 })()`)
const notificationTexts = () => page.evaluate(`[...document.querySelectorAll('.notice')].map(e => e.innerText)`)

// ---------- 场景 ----------
async function main() {
  for (const port of [SERVER_PORT, IMG_PORT, PROXY_PORT, FRPS_PORT, ECHO_PORT, REMOTE_PORT, CDP]) {
    if (!(await portFree(port))) throw new Error(`port ${port} is in use`)
  }
  if (runValues().length) throw new Error('leftover MoeFrp-e2e-* autostart values: ' + runValues().join('; '))
  await new Promise((r) => imgServer.listen(IMG_PORT, '127.0.0.1', r))
  await new Promise((r) => proxyServer.listen(PROXY_PORT, '127.0.0.1', r))
  execFileSync(serverExe, ['migrate', '-c', cfgFile], { stdio: 'ignore' })
  const logFile = fs.openSync(path.join(OUT, 'server.log'), 'a')
  server = spawn(serverExe, ['serve', '-c', cfgFile], { stdio: ['ignore', logFile, logFile], env: { ...process.env, MOEFRP_ADMIN_SETUP_CODE: ADMIN_SETUP_CODE } })
  await waitUntil('server', async () => (await fetch(`http://127.0.0.1:${SERVER_PORT}/healthz`)).ok, 15000, 100)
  frps = spawn(localfrps, ['-port', String(FRPS_PORT), '-token', TOKEN, '-echo', `127.0.0.1:${ECHO_PORT}`], { stdio: 'ignore' })
  await waitUntil('frps', async () => !(await portFree(FRPS_PORT)), 10000, 100)

  await startApp()

  // ---- 1. 公告、新版本提示、关于页 ----
  await page.waitFor('notifications', async () => {
    const t = (await notificationTexts()).join('\n')
    return t.includes(ANNOUNCE) && t.includes('3.9.0')
  }, 20000).catch(() => {})
  const notes = (await notificationTexts()).join('\n')
  await shot('01-notifications.png')
  check('announcement_and_update_notified', notes.includes(ANNOUNCE) && notes.includes('有新版本 3.9.0'), { notes: notes.slice(0, 200) })
  await page.evaluate(`document.querySelectorAll('.notice .notice-close').forEach(b => b.click())`)
  await page.click(tid('nav-about'))
  await page.click(tid('about-check-update'))
  await page.waitFor('update shown', () => page.exists(tid('about-update-available')), 10000)
  const licenses = await page.text(tid('about-licenses'))
  await shot('02-about.png')
  check('about_update_and_licenses', (await page.text(tid('about-update-available'))).includes('3.9.0') && /frp/.test(licenses) && /Apache/.test(licenses))

  // ---- 2. 图片面板（系统代理下 img.e2e.test 解析不了，靠回退到本机图源）----
  // 图源只给登录的用户：没登录时侧栏没有图片面板，也不发图片请求
  await page.click(tid('nav-tunnel'))
  const noPanelLoggedOut = !(await page.exists(tid('image-panel')))
  check('image_panel_needs_login', noPanelLoggedOut && imgRequests === 0, { noPanelLoggedOut, imgRequests })
  const [invite] = await adminClient(`http://127.0.0.1:${SERVER_PORT}`).createInvites(1)
  const reg = await fetch(`http://127.0.0.1:${SERVER_PORT}/api/v1/auth/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ nickname: NICK, password: PASSWORD, inviteCode: invite }),
  })
  if (!reg.ok) throw new Error('register: ' + reg.status + ' ' + (await reg.text()))
  await page.click(tid('account-login'))
  await page.waitFor('auth modal', () => page.exists(tid('login-nickname')))
  await page.clickText('.tab', '登录')
  await page.type(tid('login-nickname'), NICK)
  await page.type(tid('login-password'), PASSWORD)
  await page.click(tid('login-submit'))
  await page.waitFor('logged in', async () => (await page.attr(tid('account-area'), 'data-logged-in')) === '1', 15000)
  await page.waitFor('image shown', imgLoaded, 30000)
  const id1 = await imgId()
  check('image_panel_shows_server_sources', !!id1 && (await imgLoaded()), { id1, imgRequests })
  await page.evaluate(`document.querySelector('[data-testid=image-panel-img]').dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 80, clientY: 500 }))`)
  await page.waitFor('context menu', () => page.evaluate(`[...document.querySelectorAll('.menu-item')].some(e => e.offsetParent)`), 5000)
  const menuItems = await page.evaluate(`[...document.querySelectorAll('.menu-item')].filter(e => e.offsetParent).map(e => e.textContent.trim())`)
  await shot('03-image-menu.png')
  await page.clickText('.menu-item', '换一张')
  await page.waitFor('image changed', async () => (await imgId()) !== id1 && (await imgLoaded()), 15000).catch(() => {})
  const id2 = await imgId()
  check('image_context_menu_and_next', JSON.stringify(menuItems) === JSON.stringify(['换一张', '复制', '另存为…']) && id2 && id2 !== id1, { menuItems, id1, id2 })

  // ---- 3. 自定义网络代理 ----
  await page.click(tid('nav-settings'))
  await page.click(tid('settings-proxy-custom'))
  await page.type(tid('settings-proxy-url'), '127.0.0.1:18096')
  await page.click(tid('settings-proxy-apply'))
  await sleep(500)
  const badToast = (await page.toasts()).join(' ')
  await page.type(tid('settings-proxy-url'), `http://127.0.0.1:${PROXY_PORT}`)
  await page.click(tid('settings-proxy-apply'))
  await page.waitFor('proxy applied', async () => (await page.toasts()).some((t) => t.includes('自定义代理')), 5000).catch(() => {})
  await page.click(tid('nav-tunnel'))
  const before = proxied.length
  for (let i = 0; i < 4 && !proxied.slice(before).some((h) => h.startsWith('img.e2e.test')); i++) {
    const cur = await imgId()
    await page.evaluate(`document.querySelector('[data-testid=image-panel-img]').dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 80, clientY: 500 }))`)
    await page.clickText('.menu-item', '换一张')
    await page.waitFor('image changed via proxy', async () => (await imgId()) !== cur, 10000).catch(() => {})
  }
  const viaProxy = proxied.slice(before)
  check('custom_proxy_validated_and_used', /代理地址格式不对/.test(badToast) && viaProxy.some((h) => h.startsWith('img.e2e.test')), { badToast, viaProxy })
  // 改回不使用代理，避免影响后面
  await page.click(tid('nav-settings'))
  await page.clickText('.radio', '不使用')
  await sleep(300)

  // ---- 隧道：本地配置 + 节点 + 代理，启动 ----
  await page.click(tid('nav-tunnel'))
  await page.click(tid('profile-new'))
  await page.type(tid('profile-name-input'), '自动连接测试')
  if (await page.exists(tid('profile-origin-local'))) await page.click(tid('profile-origin-local'))
  await page.clickText('.dlg-actions button', '确定')
  await page.waitFor('profile', () => page.exists(tid('profile-item-自动连接测试')))
  await page.clickText('.tab', '节点')
  await page.click(tid('node-add'))
  await page.waitFor('node drawer', () => page.drawerOpen())
  await page.type(tid('node-form-remark'), '本机frps')
  await page.type(tid('node-form-serverAddr'), '127.0.0.1')
  await page.type(tid('node-form-serverPort'), FRPS_PORT)
  await page.type(tid('node-form-token'), TOKEN)
  await page.click(tid('node-form-save'))
  await page.waitFor('node saved', async () => !(await page.drawerOpen()))
  await page.clickText('.tab', '代理')
  await page.click(tid('proxy-add'))
  await page.waitFor('proxy drawer', () => page.drawerOpen())
  await page.type(tid('proxy-form-name'), 'echo')
  await page.type(tid('proxy-form-localPort'), ECHO_PORT)
  await page.type(tid('proxy-form-remotePort'), REMOTE_PORT)
  await page.click(tid('proxy-form-save'))
  await page.waitFor('proxy saved', async () => !(await page.drawerOpen()) && (await page.exists(tid('proxy-row-echo'))))
  await page.click(tid('run-toggle'))
  await page.waitFor('running', async () => (await runState()) === 'running', 20000)
  check('tunnel_running_before_hide', await echoEventually(REMOTE_PORT))

  // ---- 4. 隐藏到托盘时释放 WebView2 ----
  await setSetting('最小化') // 默认是每次询问；这里固定为最小化到托盘
  await sleep(1500)
  const memWith = treeMemory(appProc.pid)
  const releases = diagCount(/release WebView2/)
  page.close()
  page = null
  try {
    execFileSync('taskkill', ['/PID', String(appProc.pid)], { stdio: 'ignore' }) // WM_CLOSE → 关闭按钮的行为（上面设成了最小化到托盘，默认释放界面）
  } catch {}
  await waitUntil('window released', () => diagCount(/release WebView2/) > releases, 10000, 100)
  let memHidden = null
  await waitUntil('webview processes exit', () => {
    memHidden = treeMemory(appProc.pid)
    return memHidden.webview === 0
  }, 20000, 500).catch(() => {})
  const tunnelWhileHidden = await echoOk(REMOTE_PORT)
  result.measurements.memory = { withWindowMB: Math.round(memWith.ws / 1048576), hiddenMB: Math.round(memHidden.ws / 1048576), webviewBefore: memWith.webview, webviewAfter: memHidden.webview }
  check('hide_releases_webview2_tunnel_keeps_running', alive(appProc.pid) && memHidden.webview === 0 && memHidden.ws < memWith.ws / 2 && tunnelWhileHidden, result.measurements.memory)

  // 再次启动 exe（单实例）→ 重新创建窗口
  const recreated = diagCount(/window recreated/)
  const ready = diagCount(/frontend ready/)
  const t0 = Date.now()
  launch()
  await waitUntil('window recreated', () => diagCount(/window recreated/) > recreated && diagCount(/frontend ready/) > ready, 20000, 100)
  result.measurements.reopenMs = Date.now() - t0
  await reconnect()
  await page.waitFor('state restored', async () => (await runState()) === 'running' && (await page.exists(tid('profile-item-自动连接测试'))), 10000).catch(() => {})
  await shot('04-reopened.png')
  check('reopen_recreates_window_with_state', (await runState()) === 'running', { reopenMs: result.measurements.reopenMs })

  // ---- 5. 开机自启 ----
  await page.click(tid('nav-settings'))
  await page.click(tid('settings-autostart'))
  await sleep(500)
  const regOn = runValues()
  await page.click(tid('settings-autostart'))
  await sleep(500)
  const regOff = runValues()
  check('autostart_writes_and_removes_run_value', regOn.length === 1 && regOn[0].includes(EXE) && regOn[0].includes('--autostart') && regOff.length === 0, { regOn, regOff })

  // ---- 6. 启动后自动连接 ----
  await page.click(tid('settings-autoconnect'))
  await sleep(300)
  await setSetting('退出')
  await quitApp()
  check('quit_stops_tunnel', !(await echoOk(REMOTE_PORT)))
  await startApp()
  await page.waitFor('auto connected', async () => (await runState()) === 'running', 20000).catch(() => {})
  check('auto_connect_on_start', (await runState()) === 'running' && (await echoEventually(REMOTE_PORT)))

  // ---- 7. --autostart：只有托盘，自动连接照常，再次启动才出现窗口 ----
  await quitApp()
  const trayOnly = diagCount(/launched at boot: tray only/)
  const ready2 = diagCount(/frontend ready/)
  appProc = launch(['--autostart'])
  await waitUntil('tray only', () => diagCount(/launched at boot: tray only/) > trayOnly, 15000, 100)
  const echoNoWindow = await echoEventually(REMOTE_PORT, 20000)
  const noWindow = diagCount(/frontend ready/) === ready2
  const memTray = treeMemory(appProc.pid)
  result.measurements.autostartTrayOnlyMB = Math.round(memTray.ws / 1048576)
  check('autostart_launch_tray_only_and_connects', noWindow && echoNoWindow && memTray.webview === 0, { memTrayMB: result.measurements.autostartTrayOnlyMB })
  launch()
  await waitUntil('window on demand', () => diagCount(/frontend ready/) > ready2, 20000, 100)
  await reconnect()
  check('window_created_on_demand', (await runState()) === 'running')
  await shot('05-after-autostart.png')

  // ---- 2 续：关闭图片面板 ----
  await page.click(tid('nav-settings'))
  await page.click(tid('settings-image-panel'))
  await sleep(500)
  const panelGone = !(await page.exists(tid('image-panel')))
  const reqBefore = imgRequests
  await sleep(2000)
  check('image_panel_off_hides_and_stops_requests', panelGone && imgRequests === reqBefore)
}

let fatal = null
try {
  await main()
} catch (e) {
  fatal = e
  failures++
  console.error('FATAL', e)
  if (page) await shot('fatal.png').catch(() => {})
}
try {
  if (page) {
    await page.click(tid('nav-settings')).catch(() => {})
    await page.clickText('.radio', '退出').catch(() => {})
    await sleep(300)
  }
} catch {}
await quitApp()
for (const v of runValues()) {
  const name = v.split(/\s+/)[0]
  try {
    execFileSync('reg', ['delete', RUN_KEY, '/v', name, '/f'], { stdio: 'ignore' })
  } catch {}
}
try {
  execFileSync(credtool, ['delete', CRED_PREFIX], { stdio: 'ignore' }) // 登录时"保持登录"存的令牌
} catch {}
if (server) server.kill()
if (frps) frps.kill()
imgServer.close()
proxyServer.close()
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
