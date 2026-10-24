import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { useTunnelStore } from './tunnel'
import { codeText } from '../errors'

// 运行事件：把隧道状态、代理状态的变化翻译成人话，给云端配置和订阅的"运行状态"面板用（它们不显示原始日志）。
// 只记在内存里，最多 MAX 条，新的在前。

export type EventTone = 'ok' | 'info' | 'warn' | 'bad' | ''
export interface RunEvent {
  id: number
  at: number // 毫秒时间戳
  tone: EventTone
  text: string
  detail?: string // 悬停显示（例如 frp 原文）
}

const MAX = 100

export const useEventsStore = defineStore('events', () => {
  const list = ref<RunEvent[]>([])
  let seq = 0
  let started = false

  function add(tone: EventTone, text: string, detail?: string) {
    list.value = [{ id: ++seq, at: Date.now(), tone, text, detail }, ...list.value].slice(0, MAX)
  }

  function init() {
    if (started) return
    started = true
    const tunnel = useTunnelStore()

    watch(
      () => [tunnel.status.state, tunnel.status.logins, tunnel.status.error?.message] as const,
      ([state, logins], old) => {
        const s = tunnel.status
        const node = s.nodeLabel || '节点'
        const why = s.error ? codeText(s.error.code, s.error.details as Record<string, unknown> | null, s.error.message) : ''
        const [prevState, prevLogins] = old ?? ['idle', 0]
        if (state === prevState && logins === prevLogins) {
          if (state === 'reconnecting' && why) add('warn', `重连失败：${why}`, s.error?.message)
          return
        }
        switch (state) {
          case 'connecting':
            add('info', `正在连接「${node}」`)
            break
          case 'running':
            add('ok', logins > 1 ? `已重新连接「${node}」（第 ${logins - 1} 次重连）` : `已连接「${node}」`)
            break
          case 'reconnecting':
            add('warn', why ? `连接断开，正在重连：${why}` : '连接断开，正在重连', s.error?.message)
            break
          case 'failed':
            add('bad', why ? `启动失败：${why}` : '启动失败', s.error?.message)
            break
          case 'stopping':
            break
          case 'idle':
            if (prevState !== 'idle') add('', '已停止')
            break
        }
      },
    )

    // 代理：上线、被拒绝
    const last: Record<string, string> = {}
    watch(
      () => tunnel.proxies,
      (ps) => {
        for (const p of ps) {
          const key = `${p.phase}|${p.err ?? ''}`
          if (last[p.id] === key) continue
          last[p.id] = key
          if (p.phase === 'running') add('ok', `代理 ${p.name} 已上线${p.remoteAddr ? ` → ${p.remoteAddr}` : ''}`)
          else if (p.err) add('bad', `代理 ${p.name} 没能启动：${(p.code as string) === 'proxy_start_error' ? p.err : codeText(p.code, null, p.err)}`, p.err)
        }
      },
    )
  }

  function clear() {
    list.value = []
  }

  return { list, init, add, clear }
})
