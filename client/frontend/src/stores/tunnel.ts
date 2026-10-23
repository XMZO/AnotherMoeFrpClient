import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { Events, TunnelService, type PingResult, type ProxyStatus, type Status } from '../api'
import { toAppError } from '../errors'

export const useTunnelStore = defineStore('tunnel', () => {
  const status = ref<Status>({ state: 'idle', logins: 0, pending: false } as Status)
  const proxies = shallowRef<ProxyStatus[]>([])
  const pings = ref<Record<string, PingResult>>({})
  const pinging = ref(false)
  const acting = ref(false) // 正在等 Start / Stop 调用返回
  let started = false

  const active = computed(() => ['connecting', 'running', 'reconnecting'].includes(status.value.state))
  const busy = computed(() => status.value.state === 'stopping' || acting.value)
  /** 正在运行（含连接中、停止中）的配置 ID。 */
  const runningProfileId = computed(() =>
    status.value.state === 'idle' || status.value.state === 'failed' ? '' : (status.value.profileId ?? ''),
  )
  const proxyById = computed(() => {
    const m: Record<string, ProxyStatus> = {}
    for (const p of proxies.value) m[p.id] = p
    return m
  })

  async function init() {
    if (started) return
    started = true
    Events.On('tunnel:state', (ev) => {
      if (ev.data) status.value = ev.data
    })
    Events.On('tunnel:proxy', (ev) => {
      proxies.value = ev.data ?? []
    })
    status.value = await TunnelService.State()
    proxies.value = (await TunnelService.ProxyStatuses()) ?? []
  }

  async function run(fn: () => Promise<void>) {
    acting.value = true
    try {
      await fn()
    } catch (e) {
      throw toAppError(e)
    } finally {
      acting.value = false
    }
  }

  const start = (profileId: string, nodeId: string) => run(() => TunnelService.Start(profileId, nodeId))
  const stop = () => run(() => TunnelService.Stop())

  async function ping(profileId: string) {
    pinging.value = true
    try {
      const res = (await TunnelService.PingNodes(profileId)) ?? []
      const m: Record<string, PingResult> = {}
      for (const r of res) m[r.nodeId] = r
      pings.value = m
    } catch (e) {
      throw toAppError(e)
    } finally {
      pinging.value = false
    }
  }

  return { status, proxies, proxyById, pings, pinging, acting, active, busy, runningProfileId, init, start, stop, ping }
})
