import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { Events, TunnelService, type OfficialNodeView, type PingResult, type ProxyStatus, type Status } from '../api'
import { toAppError } from '../errors'

export const useTunnelStore = defineStore('tunnel', () => {
  const status = ref<Status>({ state: 'idle', logins: 0, pending: false } as Status)
  const proxies = shallowRef<ProxyStatus[]>([])
  const pings = ref<Record<string, PingResult>>({})
  const pinging = ref(false)
  /** 面板下发的官方节点（订阅来的配置不能用）。 */
  const official = shallowRef<OfficialNodeView[]>([])
  const acting = ref(false) // 正在等 Start / Stop 调用返回
  /** 顶部节点选择框当前选中的节点（节点页的"用这个节点"也会改它）。 */
  const selectedNodeId = ref('')
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
    Events.On('official:nodes', (ev) => {
      official.value = ev.data ?? []
    })
    official.value = (await TunnelService.OfficialNodes()) ?? []
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

  /** 选节点：正在运行这份配置时换了节点，就按 6.3.5 停掉再用新节点重启。 */
  async function chooseNode(profileId: string, id: string) {
    selectedNodeId.value = id
    const s = status.value
    if (runningProfileId.value === profileId && s.state !== 'stopping' && id !== s.nodeId) await start(profileId, id)
  }

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

  return { status, proxies, proxyById, pings, pinging, official, acting, active, busy, runningProfileId, selectedNodeId, init, start, stop, chooseNode, ping }
})
