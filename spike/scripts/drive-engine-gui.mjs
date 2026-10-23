// 通过 WebView2 的 CDP 端口驱动 app-engine 界面：点"启动"→ 等 running → 经 frps 远程端口做 echo → 点"停止"。
// 用法：node drive-engine-gui.mjs <cdpPort> <remotePort> <screenshotDir>
import net from 'node:net'
import fs from 'node:fs'
import path from 'node:path'

const [cdpPort = '9333', remotePort = '17002', shotDir = '.', mode = ''] = process.argv.slice(2)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const result = {}

async function connect() {
  for (let i = 0; i < 100; i++) {
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
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j })
let nextId = 1
const pending = new Map()
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data)
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id) }
}
const send = (method, params = {}) => new Promise((r) => { const id = nextId++; pending.set(id, r); ws.send(JSON.stringify({ id, method, params })) })
const evaluate = async (expr) => (await send('Runtime.evaluate', { expression: expr, returnByValue: true })).result?.result?.value
const text = (sel) => evaluate(`document.querySelector(${JSON.stringify(sel)})?.textContent?.trim() ?? null`)
async function waitText(sel, want, timeout = 15000) {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) {
    if ((await text(sel)) === want) return Date.now() - t0
    await sleep(50)
  }
  throw new Error(`timeout: ${sel} != ${want} (got ${await text(sel)})`)
}
async function shot(name) {
  const r = await send('Page.captureScreenshot', { format: 'png' })
  const file = path.join(shotDir, name)
  fs.writeFileSync(file, Buffer.from(r.result.data, 'base64'))
  return file
}
function echo(port, msg) {
  return new Promise((resolve, reject) => {
    const s = net.connect(Number(port), '127.0.0.1')
    let buf = ''
    s.setTimeout(3000, () => { s.destroy(); reject(new Error('echo timeout')) })
    s.on('connect', () => s.write(msg + '\n'))
    s.on('data', (d) => { buf += d; if (buf.includes('\n')) { s.end(); resolve(buf.trim()) } })
    s.on('error', reject)
  })
}
function portFree(port) {
  return new Promise((resolve) => {
    const srv = net.createServer().once('error', () => resolve(false)).once('listening', () => srv.close(() => resolve(true)))
    srv.listen(Number(port), '127.0.0.1')
  })
}

await waitText('#toggle', '启动', 15000)
result.initialState = await text('#state')

// 点"启动"
await evaluate(`document.querySelector('#toggle').click()`)
result.msToRunningState = await waitText('#state', 'running')
result.msToProxyRunning = await waitText('#proxy-phase', 'running')
result.runIDShown = await text('#runid')
result.echoViaFrps = await echo(remotePort, 'hello-from-gui-test')
await sleep(400) // 等一批日志推送（200ms 批次）
result.logLineCount = await evaluate(`document.querySelectorAll('#logs > div').length`)
result.logHasLoginSuccess = await evaluate(`document.querySelector('#logs').textContent.includes('login to server success')`)
// 纯文本渲染：日志行内不应出现任何子元素
result.logLinesWithChildElements = await evaluate(`[...document.querySelectorAll('#logs > div')].filter(d => d.children.length > 0).length`)
result.screenshotRunning = await shot('app-engine-running.png')

if (mode === 'leave-running') {
  ws.close()
  console.log(JSON.stringify(result, null, 2))
  process.exit(0)
}

// 点"停止"
const t0 = Date.now()
await evaluate(`document.querySelector('#toggle').click()`)
result.msToIdle = await waitText('#state', 'idle')
result.stopClickToIdleMs = Date.now() - t0
let freed = false
for (let i = 0; i < 100 && !freed; i++) { freed = await portFree(remotePort); if (!freed) await sleep(100) }
result.remotePortFreeAfterStop = freed
result.proxyPhaseAfterStop = await text('#proxy-phase')
result.screenshotStopped = await shot('app-engine-stopped.png')

ws.close()
console.log(JSON.stringify(result, null, 2))
