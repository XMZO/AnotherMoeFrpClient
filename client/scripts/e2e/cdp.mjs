// 通过 WebView2 的 CDP 端口操作 MoeFrp 界面的小工具（Phase 3 端到端测试用，可以同时连多个实例）。
import fs from 'node:fs'

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
export const tid = (id) => `[data-testid=${JSON.stringify(id)}]`
const js = (s) => JSON.stringify(s)

export async function waitUntil(what, fn, timeout = 15000, interval = 50) {
  const t0 = Date.now()
  let last
  while (Date.now() - t0 < timeout) {
    try {
      last = await fn()
    } catch (e) {
      last = String(e)
    }
    if (last) return Date.now() - t0
    await sleep(interval)
  }
  throw new Error(`timeout waiting for ${what} (last=${JSON.stringify(last)?.slice(0, 300)})`)
}

/** 连接某个 CDP 端口上的页面，返回操作界面的方法。 */
export async function connect(port, label = String(port), timeout = 30000) {
  let target
  await waitUntil(`CDP target on ${port}`, async () => {
    const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    target = list.find((t) => t.type === 'page')
    return !!target
  }, timeout, 100)
  const ws = new WebSocket(target.webSocketDebuggerUrl)
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

  const p = {
    label,
    port,
    send,
    close: () => ws.close(),
    async evaluate(expr) {
      const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })
      if (r.result?.exceptionDetails) throw new Error(`[${label}] evaluate: ` + JSON.stringify(r.result.exceptionDetails).slice(0, 400))
      return r.result?.result?.value
    },
    exists: (sel) => p.evaluate(`!!document.querySelector(${js(sel)})`),
    text: (sel) => p.evaluate(`document.querySelector(${js(sel)})?.textContent?.trim() ?? null`),
    attr: (sel, a) => p.evaluate(`document.querySelector(${js(sel)})?.getAttribute(${js(a)}) ?? null`),
    waitFor: (what, fn, timeout) => waitUntil(`[${label}] ${what}`, fn, timeout),
    async click(sel) {
      await p.waitFor(`element ${sel}`, () => p.exists(sel))
      await p.evaluate(`document.querySelector(${js(sel)}).click()`)
    },
    async clickText(sel, txt) {
      const find = `[...document.querySelectorAll(${js(sel)})].find(e => e.textContent.trim().startsWith(${js(txt)}) && e.offsetParent !== null)`
      await p.waitFor(`${sel} with text ${txt}`, () => p.evaluate(`!!(${find})`))
      await p.evaluate(`${find}.click()`)
    },
    /** 像用户一样输入：聚焦、全选、插入文本（触发 input 事件），再失焦。 */
    async type(sel, value) {
      const inputSel = `${sel} input, ${sel} textarea, input${sel}`
      await p.waitFor(`input ${sel}`, () => p.exists(inputSel))
      await p.evaluate(`(() => { const el = document.querySelector(${js(inputSel)}); el.focus(); el.select(); })()`)
      if (String(value) === '') await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Delete', code: 'Delete', windowsVirtualKeyCode: 46 })
      else await send('Input.insertText', { text: String(value) })
      await p.evaluate(`document.querySelector(${js(inputSel)}).blur()`)
    },
    async shot(file) {
      const r = await send('Page.captureScreenshot', { format: 'png' })
      fs.writeFileSync(file, Buffer.from(r.result.data, 'base64'))
      return file
    },
    /** 最近的消息提示（n-message）。 */
    toasts: () => p.evaluate(`[...document.querySelectorAll('.n-message__content')].map(e => e.textContent.trim())`),
    drawerOpen: () => p.exists('.n-drawer'),
  }
  return p
}
