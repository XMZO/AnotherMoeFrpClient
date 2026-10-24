<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Clipboard } from '@wailsio/runtime'
import Icon from '../ui/Icon.vue'
import { message } from '../ui/feedback'
import { useTunnelStore } from '../stores/tunnel'
import { useProfilesStore } from '../stores/profiles'
import { useEventsStore } from '../stores/events'
import { stateLabels } from '../labels'

// 运行状态面板：云端配置和订阅用它代替原始日志（配置细节对用户是黑盒，也更好懂）。
// 标题栏是一行连接状态（状态、节点、延迟、在线时长、重连次数），收起时也看得到；
// 展开后两栏：访问地址（域名 / IPv4 / IPv6，可复制）· 运行事件。
// 窗口变窄时次要的几项先隐藏，不换行、不折叠；访问地址固定三行，没有的显示"无"，不会让布局跳动。
const OPEN_KEY = 'moefrp.status.open'

const tunnel = useTunnelStore()
const profiles = useProfilesStore()
const events = useEventsStore()
const open = ref(readOpen())
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
let pingTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => (now.value = Date.now()), 1000)
  pingTimer = setInterval(pingNow, 60_000) // 运行中每分钟测一次延迟
})
onBeforeUnmount(() => {
  clearInterval(timer)
  clearInterval(pingTimer)
})

function readOpen(): boolean {
  try {
    return localStorage.getItem(OPEN_KEY) !== '0'
  } catch {
    return true
  }
}
function toggleOpen() {
  open.value = !open.value
  try {
    localStorage.setItem(OPEN_KEY, open.value ? '1' : '0')
  } catch {}
}

const st = computed(() => tunnel.status)
const runningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
// 运行中看实际用的节点，否则看选中的节点
const nodeId = computed(() => (runningHere.value ? st.value.nodeId : tunnel.selectedNodeId) ?? '')
const official = computed(() => tunnel.official.find((n) => n.id === nodeId.value) ?? null)
const ownNode = computed(() => profiles.current?.nodes?.find((n) => n.id === nodeId.value) ?? null)
const nodeName = computed(() => official.value?.name ?? (ownNode.value ? ownNode.value.remark || ownNode.value.serverAddr : ''))
const state = computed(() => (runningHere.value ? st.value.state : 'idle'))

// 连上（包括重连上）后测一次延迟，之后每分钟一次；测的是到节点的 TCP 连接时间
function pingNow() {
  const p = profiles.current
  if (p && runningHere.value && st.value.state === 'running' && !tunnel.pinging) tunnel.ping(p.id).catch(() => {})
}
watch(
  () => [state.value, st.value.logins] as const,
  ([s]) => {
    if (s === 'running') pingNow()
  },
  { immediate: true },
)

function fmtDur(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d) return `${d} 天 ${h} 小时`
  if (h) return `${h} 小时 ${m} 分`
  if (m) return `${m} 分 ${s % 60} 秒`
  return `${s} 秒`
}

const conn = computed(() => {
  const s = st.value
  const here = runningHere.value
  const ping = tunnel.pings[nodeId.value]
  const since = here && s.state === 'running' && s.connectedAt ? now.value - new Date(s.connectedAt).getTime() : -1
  return [
    { k: '节点', v: nodeName.value || '未选择', tone: '', cls: 'node' },
    { k: '延迟', v: ping ? (ping.ok ? `${ping.latencyMs} ms` : '不通') : '—', tone: ping && !ping.ok ? 'failed' : '', cls: 'lat' },
    { k: '在线', v: since >= 0 ? fmtDur(since) : '—', tone: '', cls: 'up' },
    { k: '重连', v: here && s.logins > 0 ? `${s.logins - 1} 次` : '—', tone: here && s.logins > 1 ? 'reconnecting' : '', cls: 'rc' },
  ]
})

// 访问地址：官方节点用面板给的；自己的节点按服务器地址归类
const addrs = computed(() => {
  const o = official.value
  let domain = o?.domain ?? ''
  let v4 = o?.ipv4 ?? ''
  let v6 = o?.ipv6 ?? ''
  if (!o && ownNode.value) {
    const a = ownNode.value.serverAddr.replace(/^\[|\]$/g, '')
    if (/^\d{1,3}(\.\d{1,3}){3}$/.test(a)) v4 = a
    else if (a.includes(':')) v6 = a
    else domain = a
  }
  return [
    { k: '域名', v: domain, copy: domain },
    { k: 'IPv4', v: v4, copy: v4 },
    { k: 'IPv6', v: v6, copy: v6 ? `[${v6}]` : '' },
  ]
})

async function copy(text: string) {
  if (!text) return
  try {
    await Clipboard.SetText(text)
    message.success(`已复制 ${text}`)
  } catch {
    message.error('复制失败')
  }
}

const timeFmt = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
</script>

<template>
  <section class="log-panel status-panel" :class="{ open }" data-testid="status-panel">
    <div class="logs-bar">
      <button type="button" class="logs-toggle sp-bar" :aria-expanded="open" data-testid="toggle-status" @click="toggleOpen">
        <Icon name="radar" size="s" />
        <span class="sp-title">运行状态</span>
        <span class="state sm" :class="state" data-testid="status-state"><span class="d" />{{ stateLabels[state] ?? state }}</span>
        <span class="sp-conn">
          <span v-for="r in conn" :key="r.k" :class="['sp-c', r.cls]"><small>{{ r.k }}</small><b :class="r.tone">{{ r.v }}</b></span>
        </span>
        <Icon name="chevUp" size="s" class="logs-chev" />
      </button>
    </div>
    <div class="sp-body">
      <div class="sp-card">
        <h4 title="代理的访问地址 = 这里的地址 + 代理的远程端口（代理行上的复制按钮直接复制完整地址）">访问地址</h4>
        <dl class="sp-kv">
          <template v-for="a in addrs" :key="a.k">
            <dt>{{ a.k }}</dt>
            <dd class="mono" :class="{ none: !a.v }" :title="a.v || undefined" :data-testid="`status-addr-${a.k}`">{{ a.v || '无' }}</dd>
            <button type="button" class="icon-btn sm" :disabled="!a.v" :title="a.v ? `复制 ${a.copy}` : undefined" :aria-label="`复制${a.k}`" @click="copy(a.copy)">
              <Icon name="copy" size="xs" />
            </button>
          </template>
        </dl>
      </div>
      <div class="sp-card">
        <h4>运行事件<button v-if="events.list.length" type="button" class="sp-clear" @click="events.clear()">清空</button></h4>
        <ol class="sp-events" data-testid="status-events">
          <li v-for="e in events.list" :key="e.id" :class="e.tone" :title="e.detail || e.text">
            <time>{{ timeFmt.format(e.at) }}</time><span>{{ e.text }}</span>
          </li>
          <li v-if="!events.list.length" class="empty-ev">启动后，连接和代理的变化会显示在这里</li>
        </ol>
      </div>
    </div>
  </section>
</template>

<style>
.status-panel {
  container-type: inline-size;
}
.status-panel.open {
  height: clamp(150px, 27vh, 210px);
}
.sp-bar {
  gap: 10px;
}
.sp-title {
  flex-shrink: 0;
}
.state.sm {
  padding: 2px 9px 2px 8px;
  font-size: 11.5px;
}
/* 连接状态：一行，窄了先藏在线时长和重连次数 */
.sp-conn {
  display: flex;
  align-items: baseline;
  gap: 16px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}
.sp-c {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
  min-width: 0;
}
.sp-c small {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-3);
}
.sp-c b {
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 12.5px;
  font-weight: 800;
  color: var(--text);
}
.sp-c b.failed {
  color: var(--bad-text);
}
.sp-c b.reconnecting {
  color: var(--warn-text);
}
@container (max-width: 640px) {
  .sp-c.up,
  .sp-c.rc {
    display: none;
  }
}
.sp-body {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.6fr);
  gap: 10px;
  padding: 0 12px 12px;
}
.sp-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: 9px 12px 10px;
  border-radius: 14px;
  background: var(--surface);
  border: 1.5px solid var(--line-2);
}
.sp-card h4 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 6px;
  font-size: 12px;
  font-weight: 900;
  color: var(--text-2);
  white-space: nowrap;
}
.sp-kv {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) auto;
  align-items: center;
  gap: 1px 10px;
  margin: 0;
  font-size: 12.5px;
}
.sp-kv dt {
  color: var(--text-3);
  font-weight: 700;
}
.sp-kv dd {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 700;
  color: var(--text);
}
.sp-kv dd.none {
  color: var(--text-3);
  font-weight: 600;
  font-family: var(--font);
}
.sp-clear {
  margin-left: auto;
  font-size: 11px;
  font-weight: 700;
  color: var(--text-3);
}
.sp-clear:hover {
  color: var(--text);
}
.sp-events {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 0;
  overflow: auto;
  list-style: none;
  font-size: 12px;
  line-height: 1.6;
}
.sp-events li {
  display: flex;
  gap: 8px;
  min-width: 0;
  color: var(--text-2);
  font-weight: 600;
}
.sp-events li span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sp-events time {
  flex-shrink: 0;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--text-3);
}
.sp-events li.ok span {
  color: var(--ok-text);
}
.sp-events li.warn span {
  color: var(--warn-text);
}
.sp-events li.bad span {
  color: var(--bad-text);
}
.sp-events li.empty-ev {
  color: var(--text-3);
}
</style>
