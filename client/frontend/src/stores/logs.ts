import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import { Events, TunnelService, type LogLine } from '../api'
import { toAppError } from '../errors'
import { mergeLogs } from '../logmerge'

export { MAX_LOG_LINES } from '../logmerge'

export const useLogsStore = defineStore('logs', () => {
  const lines = shallowRef<LogLine[]>([])
  let started = false

  async function init() {
    if (started) return
    started = true
    Events.On('tunnel:logs', (ev) => {
      lines.value = mergeLogs(lines.value, ev.data)
    })
    lines.value = mergeLogs(lines.value, await TunnelService.Logs())
  }

  async function clear() {
    await TunnelService.ClearLogs()
    lines.value = []
  }

  /** 弹出"另存为"并导出，返回保存的路径（取消时为空串）。 */
  async function exportToFile(): Promise<string> {
    try {
      return await TunnelService.ExportLogsDialog()
    } catch (e) {
      throw toAppError(e)
    }
  }

  return { lines, init, clear, exportToFile }
})
