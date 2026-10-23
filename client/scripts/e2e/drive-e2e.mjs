// 通过 WebView2 的 CDP 端口驱动真实的 MoeFrp.exe 界面，验证 PLAN Phase 2 的验收标准。
// 由 run-e2e.ps1 调用，前提：localfrps 已在 127.0.0.1:17000（token e2e-token）运行，echo 在 17001。
//
// 用法：node drive-e2e.mjs <cdpPort> <outDir> <scenario>
//   scenario = main      新建配置 → 节点 → 代理 → 启动 → 转发 → 热更新 → 重名 → 端口冲突 → 停止 → 再启动（保持运行）
//   scenario = relaunch  重启后检查配置还在 → 把节点端口改成没人监听的端口 → 启动失败原因
import net from 'node:net'
import fs from 'node:fs'
import path from 'node:path'

const [cdpPort = '9350', outDir = '.', scenario = 'main'] = process.argv.slice(2)
const FRPS_PORT = 17000
const ECHO_PORT = 17001
const R_ECHO = 17002
const R_ECHO2 = 17003
const R_BUSY = 17004
const DEAD_PORT = 17999

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const result = { scenario, checks: {}, timings: {} }
const check = (name, ok, extra) => {
  result.checks[name] = extra === undefined ? !!ok : { ok: !!ok, ...extra }
  console.error(`${ok ? 'PASS' : 'FAIL'} ${name}${extra ? ' ' + JSON.stringify(extra) : ''}`)
}

// ---------- CDP ----------
async function connect() {
  for (let i = 0; i < 200; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${cdpPort}/json/list`)).json()
      const page = list.find((t) => t.type === 'page')
      if (page) return page
    } catch {}
    await sleep(100)
  }
  throw new Error('CDP target not found')
}
const page = await connect()
const ws = new WebSocket(page.webSocketDebuggerUrl)
await new Promise((r, j) => {
  ws.onopen = r
  ws.onerror = j
})
let nextId = 1
const pending = new Map()
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data)
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg)
    pending.delete(msg.id)
  }
}
const send = (method, params = {}) =>
  new Promise((r) => {
    const id = nextId++
    pending.set(id, r)
    ws.send(JSON.stringify({ id, method, params }))
  })
async function evaluate(expr) {
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })
  if (r.result?.exceptionDetails) throw new Error('evaluate: ' + JSON.stringify(r.result.exceptionDetails).slice(0, 400))
  return r.result?.result?.value
}
const tid = (id) => `[data-testid=${JSON.stringify(id)}]`
const js = (s) => JSON.stringify(s)

async function waitFor(what, fn, timeout = 15000) {
  const t0 = Date.now()
  let last
  while (Date.now() - t0 < timeout) {
    last = await fn()
    if (last) return Date.now() - t0
    await sleep(50)
  }
  throw new Error(`timeout waiting for ${what} (last=${JSON.stringify(last)})`)
}
const exists = (sel) => evaluate(`!!document.querySelector(${js(sel)})`)
const text = (sel) => evaluate(`document.querySelector(${js(sel)})?.textContent?.trim() ?? null`)
const attr = (sel, a) => evaluate(`document.querySelector(${js(sel)})?.getAttribute(${js(a)}) ?? null`)
async function click(sel) {
  await waitFor(`element ${sel}`, () => exists(sel))
  await evaluate(`document.querySelector(${js(sel)}).click()`)
}
async function clickText(sel, txt) {
  await waitFor(`${sel} with text ${txt}`, () =>
    evaluate(`[...document.querySelectorAll(${js(sel)})].some(e => e.textContent.trim().startsWith(${js(txt)}))`),
  )
  await evaluate(`[...document.querySelectorAll(${js(sel)})].find(e => e.textContent.trim().startsWith(${js(txt)})).click()`)
}
// 像用户一样输入：聚焦、全选、插入文本（会触发 input 事件），再失焦
async function type(sel, value) {
  const inputSel = `${sel} input, ${sel} textarea, input${sel}`
  await waitFor(`input ${sel}`, () => exists(inputSel))
  await evaluate(`(() => { const el = document.querySelector(${js(inputSel)}); el.focus(); el.select(); })()`)
  await send('Input.insertText', { text: String(value) })
  await evaluate(`document.querySelector(${js(inputSel)}).blur()`)
}
async function shot(name) {
  const r = await send('Page.captureScreenshot', { format: 'png' })
  const file = path.join(outDir, name)
  fs.writeFileSync(file, Buffer.from(r.result.data, 'base64'))
  return file
}
const drawerOpen = () => exists('.n-drawer')
const runState = () => attr(tid('run-state'), 'data-state')
const proxyPhase = (name) => attr(tid(`proxy-status-${name}`), 'data-phase')
const proxyCode = (name) => attr(tid(`proxy-status-${name}`), 'data-code')

async function openTab(label) {
  await clickText('.n-tabs-tab', label)
  await sleep(150)
}

async function addNode(remark, port) {
  await openTab('节点')
  await click(tid('node-add'))
  await waitFor('node drawer', drawerOpen)
  await type(tid('node-form-remark'), remark)
  await type(tid('node-form-serverAddr'), '127.0.0.1')
  await type(tid('node-form-serverPort'), port)
  await type(tid('node-form-token'), 'e2e-token')
  await click(tid('node-form-save'))
  await waitFor('node drawer closed', async () => !(await drawerOpen()))
}

async function fillProxy(name, localPort, remotePort) {
  await click(tid('proxy-add'))
  await waitFor('proxy drawer', drawerOpen)
  await type(tid('proxy-form-name'), name)
  await type(tid('proxy-form-localPort'), localPort)
  if (remotePort) await type(tid('proxy-form-remotePort'), remotePort)
  await click(tid('proxy-form-save'))
}

async function addProxy(name, localPort, remotePort) {
  await openTab('代理')
  await fillProxy(name, localPort, remotePort)
  await waitFor(`proxy ${name} saved`, async () => !(await drawerOpen()) && (await exists(tid(`proxy-row-${name}`))))
}

// 最近一条消息提示（n-message）
const lastToast = () => evaluate(`[...document.querySelectorAll('.n-message__content')].map(e => e.textContent.trim()).pop() ?? ''`)

function echoOnce(port, msg) {
  return new Promise((resolve, reject) => {
    const s = net.connect(port, '127.0.0.1')
    let buf = ''
    s.setTimeout(3000, () => {
      s.destroy()
      reject(new Error('echo timeout'))
    })
    s.on('connect', () => s.write(msg + '\n'))
    s.on('data', (d) => {
      buf += d
      if (buf.includes('\n')) {
        s.end()
        resolve(buf.trim())
      }
    })
    s.on('error', reject)
  })
}
// 一条长连接，用来验证热更新时连接不断
function openLongConn(port) {
  return new Promise((resolve, reject) => {
    const s = net.connect(port, '127.0.0.1')
    let buf = ''
    let waiter = null
    s.on('data', (d) => {
      buf += d
      const i = buf.indexOf('\n')
      if (i >= 0 && waiter) {
        const line = buf.slice(0, i)
        buf = buf.slice(i + 1)
        const w = waiter
        waiter = null
        w(line)
      }
    })
    s.on('error', (e) => reject(e))
    s.on('connect', () =>
      resolve({
        closed: () => s.destroyed,
        ping: (msg) =>
          new Promise((res, rej) => {
            const t = setTimeout(() => rej(new Error('long conn timeout')), 3000)
            waiter = (line) => {
              clearTimeout(t)
              res(line)
            }
            s.write(msg + '\n')
          }),
        close: () => s.destroy(),
      }),
    )
  })
}
function portFree(port) {
  return new Promise((resolve) => {
    const srv = net
      .createServer()
      .once('error', () => resolve(false))
      .once('listening', () => srv.close(() => resolve(true)))
    srv.listen(port, '127.0.0.1')
  })
}
async function waitPortFree(port, timeout = 10000) {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) {
    if (await portFree(port)) return true
    await sleep(100)
  }
  return false
}

// ---------- 场景 ----------
async function mainScenario() {
  await waitFor('app mounted', () => exists(tid('profile-new')), 20000)

  // 1. 不登录、不连服务器：新建一个本地配置
  await click(tid('profile-new'))
  await waitFor('name modal', () => exists(tid('profile-name-input')))
  await type(tid('profile-name-input'), 'e2e')
  await clickText('.n-dialog .n-button', '确定')
  await waitFor('profile created', () => exists(tid('profile-item-e2e')))
  check('createLocalProfile', true)

  await addNode('local-frps', FRPS_PORT)
  check('addNode', await exists(tid('node-row-local-frps')))
  await addProxy('echo', ECHO_PORT, R_ECHO)
  check('addProxy', await exists(tid('proxy-row-echo')))

  // 2. 启动并真实转发
  await waitFor('run button enabled', () => evaluate(`!document.querySelector(${js(tid('run-toggle'))}).disabled`))
  const t0 = Date.now()
  await click(tid('run-toggle'))
  await waitFor('running', async () => (await runState()) === 'running')
  result.timings.clickToRunningMs = Date.now() - t0
  await waitFor('proxy echo running', async () => (await proxyPhase('echo')) === 'running')
  result.timings.clickToProxyRunningMs = Date.now() - t0
  const echoed = await echoOnce(R_ECHO, 'hello-from-e2e')
  check('realForwarding', echoed === 'hello-from-e2e', { port: R_ECHO, echoed })
  const runId = await attr(tid('run-state'), 'data-runid')
  const proxyStatusText = await text(tid('proxy-status-echo'))
  check('proxyStatusColumn', proxyStatusText.includes('运行中') && proxyStatusText.includes(String(R_ECHO)), { proxyStatusText })
  await shot('01-running.png')

  // 日志面板：纯文本渲染
  await sleep(400)
  const logInfo = await evaluate(`(() => {
    const ls = [...document.querySelectorAll(${js(tid('log-line'))})]
    return { count: ls.length, withChildren: ls.filter(d => d.children.length > 0).length,
             hasLogin: ls.some(d => d.textContent.includes('login to server success')) }
  })()`)
  check('logPanelPlainText', logInfo.count > 0 && logInfo.withChildren === 0 && logInfo.hasLogin, logInfo)

  // 3. 运行中改代理：热更新，连接不断
  const conn = await openLongConn(R_ECHO)
  check('longConnBefore', (await conn.ping('before')) === 'before')
  await addProxy('echo2', ECHO_PORT, R_ECHO2)
  const toast = await lastToast()
  await waitFor('proxy echo2 running', async () => (await proxyPhase('echo2')) === 'running')
  check('hotUpdateToast', toast.includes('热更新'), { toast })
  check('hotUpdateNewProxyForwards', (await echoOnce(R_ECHO2, 'hello-echo2')) === 'hello-echo2')
  let pingAfter = ''
  try {
    pingAfter = await conn.ping('after-hot-update')
  } catch (e) {
    pingAfter = 'ERR ' + e.message
  }
  const runIdAfter = await attr(tid('run-state'), 'data-runid')
  const logins = await attr(tid('run-state'), 'data-logins')
  check('hotUpdateConnectionKept', pingAfter === 'after-hot-update' && !conn.closed(), { pingAfter })
  check('hotUpdateNoReconnect', runIdAfter === runId && logins === '1', { runId, runIdAfter, logins })

  // 关掉 echo2 的开关：同样是热更新
  await click(tid('proxy-switch-echo2'))
  await waitFor('echo2 disabled', async () => (await text(tid('proxy-status-echo2'))) === '未启用')
  check('toggleOffReleasesPort', await waitPortFree(R_ECHO2))
  check('longConnAfterToggle', (await conn.ping('after-toggle')) === 'after-toggle')
  conn.close()

  // 4. 代理重名：保存被拦下，抽屉里显示原因
  await openTab('代理')
  await fillProxy('echo', ECHO_PORT, 0)
  await waitFor('duplicate feedback', () =>
    evaluate(`[...document.querySelectorAll('.n-drawer .n-form-item-feedback')].some(e => e.textContent.includes('重复'))`),
  )
  const dupText = await evaluate(
    `[...document.querySelectorAll('.n-drawer .n-form-item-feedback')].map(e => e.textContent.trim()).find(t => t.includes('重复'))`,
  )
  check('duplicateNameShown', !!dupText && (await drawerOpen()), { dupText })
  await shot('02-duplicate-name.png')
  await clickText('.n-drawer .n-button', '取消')
  await waitFor('drawer closed', async () => !(await drawerOpen()))
  check('duplicateNotSaved', (await evaluate(`document.querySelectorAll(${js(tid('proxy-row-echo'))}).length`)) === 1)

  // 5. 远程端口被占用：一个被别的程序占着，一个和 echo 冲突
  const blocker = net.createServer().listen(R_BUSY, '127.0.0.1')
  await new Promise((r) => blocker.once('listening', r))
  await addProxy('busy', ECHO_PORT, R_BUSY)
  await addProxy('clash', ECHO_PORT, R_ECHO)
  await waitFor('busy start error', async () => (await proxyPhase('busy')) === 'start error')
  await waitFor('clash start error', async () => (await proxyPhase('clash')) === 'start error')
  const busy = { code: await proxyCode('busy'), text: await text(tid('proxy-status-busy')) }
  const clash = { code: await proxyCode('clash'), text: await text(tid('proxy-status-clash')) }
  check('portOccupiedByOtherProgramShown', busy.code === 'proxy_port_unavailable' && busy.text.includes('远程端口'), busy)
  check('portConflictShown', clash.code === 'proxy_port_in_use' && clash.text.includes('远程端口已被占用'), clash)
  check('otherProxiesUnaffected', (await proxyPhase('echo')) === 'running' && (await echoOnce(R_ECHO, 'still')) === 'still')
  await shot('03-port-errors.png')
  blocker.close()

  // 删掉冲突的代理（热更新），echo 不受影响
  await click(tid('proxy-delete-clash'))
  await clickText('.n-popconfirm .n-button', '确认')
  await waitFor('clash removed', async () => !(await exists(tid('proxy-row-clash'))))
  check('deleteProxyHot', (await proxyPhase('echo')) === 'running' && (await echoOnce(R_ECHO, 'after-delete')) === 'after-delete')

  // 6. 停止：stopping → idle，frps 上的端口释放
  const t1 = Date.now()
  await click(tid('run-toggle'))
  await waitFor('idle', async () => (await runState()) === 'idle')
  result.timings.stopClickToIdleMs = Date.now() - t1
  check('stopReleasesPort', await waitPortFree(R_ECHO), { port: R_ECHO })
  check('statusColumnClearedAfterStop', (await text(tid('proxy-status-echo'))) === '—')

  // 7. 再启动（新的 Service），保持运行，交给 PowerShell 测"运行中从托盘退出"
  await click(tid('run-toggle'))
  await waitFor('running again', async () => (await runState()) === 'running')
  await waitFor('echo running again', async () => (await proxyPhase('echo')) === 'running')
  const runId2 = await attr(tid('run-state'), 'data-runid')
  check('restartNewService', runId2 && runId2 !== runId && (await echoOnce(R_ECHO, 'again')) === 'again', { runId, runId2 })
}

async function relaunchScenario() {
  await waitFor('app mounted', () => exists(tid('profile-item-e2e')), 20000)
  check('profilePersisted', true)
  await click(tid('profile-item-e2e'))
  await waitFor('proxies loaded', () => exists(tid('proxy-row-echo')))
  const rows = await evaluate(`document.querySelectorAll('[data-testid^="proxy-row-"]').length`)
  check('proxiesPersisted', rows === 3, { rows })
  check('idleAfterRelaunch', (await runState()) === 'idle')

  // 节点端口改成没人监听的端口：启动失败，原因是"连接被拒绝"（frp 原文是 session shutdown）
  await openTab('节点')
  await click(tid('node-edit-local-frps'))
  await waitFor('node drawer', drawerOpen)
  await type(tid('node-form-serverPort'), DEAD_PORT)
  await click(tid('node-form-save'))
  await waitFor('node drawer closed', async () => !(await drawerOpen()))
  await openTab('代理')
  await click(tid('run-toggle'))
  await waitFor('failed', async () => (await runState()) === 'failed', 20000)
  const detail = await text(tid('run-detail'))
  check('connectFailureExplained', detail.includes('连接被拒绝') && detail.includes(String(DEAD_PORT)), { detail })
  await sleep(600) // 等 Naive UI 的颜色过渡（0.3s）结束
  const colors = await evaluate(`(() => {
    const tag = document.querySelector(${js(tid('run-state'))}), btn = document.querySelector(${js(tid('run-toggle'))})
    return { tagBg: getComputedStyle(tag).backgroundColor, btnBg: getComputedStyle(btn).backgroundColor }
  })()`)
  // 失败：标签是红色系（error），按钮是"启动"的绿色（primary）
  check('failedStateColors', colors.tagBg.startsWith('rgba(208, 48, 80') && colors.btnBg === 'rgb(24, 160, 88)', colors)
  await shot('04-node-refused.png')

  // 测速：同样的原因
  await click(tid('run-ping'))
  await waitFor('ping result', () => evaluate(`document.querySelector(${js(tid('run-node'))}).textContent.includes('连接被拒绝')`))
  check('pingShowsReason', true)

  // 主题跟随系统：用 CDP 模拟系统切换到深色 / 浅色（设置默认是"跟随系统"）
  const bodyBg = () => evaluate(`getComputedStyle(document.body).backgroundColor`)
  const isDark = (rgb) => {
    const m = /rgba?\((\d+), (\d+), (\d+)/.exec(rgb)
    return m && Number(m[1]) + Number(m[2]) + Number(m[3]) < 200
  }
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] })
  await waitFor('dark theme', async () => isDark(await bodyBg()), 5000)
  const darkBg = await bodyBg()
  await sleep(400)
  await shot('05-dark-theme.png')
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] })
  await waitFor('light theme', async () => !isDark(await bodyBg()), 5000)
  check('themeFollowsSystem', true, { darkBg, lightBg: await bodyBg() })

  // 改回正确端口，启动成功后保持运行，交给 PowerShell 测关闭到托盘、单实例、托盘退出
  await openTab('节点')
  await click(tid('node-edit-local-frps'))
  await waitFor('node drawer', drawerOpen)
  await type(tid('node-form-serverPort'), FRPS_PORT)
  await click(tid('node-form-save'))
  await waitFor('node drawer closed', async () => !(await drawerOpen()))
  await openTab('代理')
  await click(tid('run-toggle'))
  await waitFor('running', async () => (await runState()) === 'running')
  check('runningAfterFix', (await echoOnce(R_ECHO, 'relaunch')) === 'relaunch')
}

let failed = false
try {
  if (scenario === 'main') await mainScenario()
  else await relaunchScenario()
} catch (e) {
  failed = true
  result.error = String(e?.stack ?? e)
  try {
    result.failureShot = await shot(`failure-${scenario}.png`)
  } catch {}
}
ws.close()
const allOk = !failed && Object.values(result.checks).every((v) => (typeof v === 'object' ? v.ok : v))
result.ok = allOk
console.log(JSON.stringify(result, null, 2))
process.exit(allOk ? 0 : 1)
