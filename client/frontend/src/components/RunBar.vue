<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Menu from '../ui/Menu.vue'
import { message } from '../ui/feedback'
import { useProfilesStore } from '../stores/profiles'
import { useSettingsStore } from '../stores/settings'
import { useTunnelStore } from '../stores/tunnel'
import { codeText, describeError } from '../errors'
import { stateLabels } from '../labels'
import { nodeLabel } from '../util'

// 运行控制：节点选择、测速、启动 / 停止，以及下面一行的运行状态。标题由父组件放进 #title 插槽。
const emit = defineEmits<{ 'manage-nodes': [] }>()
const profiles = useProfilesStore()
const settings = useSettingsStore()
const tunnel = useTunnelStore()

const st = computed(() => tunnel.status)
const runningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
const failedHere = computed(() => st.value.state === 'failed' && st.value.profileId === profiles.current?.id)

// 官方节点只给云端配置用（和云端配置一样是黑盒）；本地配置、订阅来的配置都不能用
const officialNodes = computed(() => (profiles.isCloud ? tunnel.official : []))

// 默认节点：正在运行的 → 上次给这份配置选的 → 第一个（先自己的节点，再官方节点）
watch(
  () => [profiles.current?.id, profiles.current?.nodes?.length, officialNodes.value.length, st.value.nodeId, runningHere.value] as const,
  () => {
    const p = profiles.current
    const ids = [...(p?.nodes ?? []).map((n) => n.id), ...officialNodes.value.map((n) => n.id)]
    if (!p || ids.length === 0) {
      tunnel.selectedNodeId = ''
      return
    }
    if (runningHere.value && st.value.nodeId && ids.includes(st.value.nodeId)) {
      tunnel.selectedNodeId = st.value.nodeId
      return
    }
    if (ids.includes(tunnel.selectedNodeId)) return
    const last = settings.settings?.lastNodeIds?.[p.id]
    const subDefault = profiles.currentSummary?.defaultNodeId // 订阅：订阅时选的节点
    tunnel.selectedNodeId = last && ids.includes(last) ? last : subDefault && ids.includes(subDefault) ? subDefault : ids[0]
  },
  { immediate: true },
)

function pingView(id: string): { cls: string; text: string } | null {
  const r = tunnel.pings[id]
  if (!r) return null
  if (!r.ok) return { cls: 'bad', text: codeText(r.code, { target: r.target }, '失败') }
  return { cls: r.latencyMs < 80 ? 'ok' : r.latencyMs < 200 ? 'warn' : 'bad', text: `${r.latencyMs} ms` }
}

interface NodeOpt {
  id: string
  label: string
  sub: string
  online: boolean
  official: boolean
}
const mine = computed<NodeOpt[]>(() =>
  (profiles.current?.nodes ?? []).map((n) => ({ id: n.id, label: nodeLabel(n), sub: `${n.serverAddr}:${n.serverPort}`, online: true, official: false })),
)
const official = computed<NodeOpt[]>(() =>
  officialNodes.value.map((n) => ({ id: n.id, label: n.name, sub: n.online ? (n.description || '托管节点') : '离线', online: n.online, official: true })),
)
const selected = computed(() => [...mine.value, ...official.value].find((n) => n.id === tunnel.selectedNodeId) ?? null)
const hasNodes = computed(() => mine.value.length + official.value.length > 0)

const btn = computed(() => {
  const s = st.value.state
  if (s === 'stopping') return { cls: 'busy', text: st.value.pending ? '切换中…' : '停止中…', icon: 'refresh' as const, spin: true }
  if (tunnel.active) {
    const connecting = s === 'connecting' || s === 'reconnecting'
    return { cls: 'stop', text: '停止', icon: connecting ? ('refresh' as const) : ('stop' as const), spin: connecting }
  }
  return { cls: 'idle', text: '启动', icon: 'play' as const, spin: false }
})

// 状态说明：失败原因、重连原因、运行的是别的配置
const detail = computed(() => {
  const s = st.value
  if ((s.state === 'failed' || s.state === 'reconnecting') && s.error) return describeError(s.error)
  if (tunnel.active && !runningHere.value && s.profileName) return `正在运行另一份配置：${s.profileName}`
  if (s.state === 'connecting' && runningHere.value) return `正在连接 ${s.nodeLabel ?? ''}…`
  if (s.state === 'running' && runningHere.value) {
    const ps = tunnel.proxies
    const ok = ps.filter((p) => p.phase === 'running').length
    return `已连接 ${s.nodeLabel ?? ''}` + (ps.length ? ` · ${ok}/${ps.length} 个代理在运行` : '')
  }
  if (!profiles.current) return ''
  if (!hasNodes.value) return '先在“节点”里添加一个节点'
  if (s.state === 'idle' && selected.value) return `点“启动”，通过「${selected.value.label}」连接`
  return ''
})
const detailTone = computed(() => (failedHere.value || st.value.state === 'failed' ? 'bad' : st.value.state === 'reconnecting' ? 'warn' : ''))

// 连上的那一刻放一下小星星
const sparkle = ref(0)
watch(
  () => st.value.state === 'running' && runningHere.value,
  (now, before) => {
    if (now && !before) sparkle.value++
  },
)

async function toggle() {
  try {
    if (tunnel.active) {
      await tunnel.stop()
      return
    }
    if (!profiles.current) return
    if (!tunnel.selectedNodeId) {
      message.warning(codeText('node_required'))
      return
    }
    await tunnel.start(profiles.current.id, tunnel.selectedNodeId)
    await settings.load()
  } catch (e) {
    message.error(describeError(e), { duration: 6000 })
  }
}

async function choose(id: string, close: () => void) {
  close()
  if (!profiles.current) return
  try {
    await tunnel.chooseNode(profiles.current.id, id)
  } catch (e) {
    message.error(describeError(e))
  }
}

async function ping() {
  if (!profiles.current) return
  try {
    await tunnel.ping(profiles.current.id)
  } catch (e) {
    message.error(describeError(e))
  }
}
</script>

<template>
  <header class="runhead">
    <slot name="title" />
    <div class="rh-ctrl">
      <Menu placement="bottom-end" :width="300" label="选择节点">
        <template #trigger="{ toggle: toggleMenu, open }">
          <button
            type="button"
            class="nodesel"
            :class="{ open }"
            :disabled="!profiles.current || tunnel.busy"
            aria-haspopup="listbox"
            :aria-expanded="open"
            data-testid="run-node"
            @click="toggleMenu"
          >
            <span class="ns-ic"><Icon name="server" size="s" /></span>
            <span class="ns-lbl">
              <small>{{ selected?.official ? '官方节点' : '节点' }}</small>
              <b class="ns-name">{{ selected ? selected.label + (selected.online ? '' : '（离线）') : '先在“节点”里添加' }}</b>
            </span>
            <span v-if="selected && pingView(selected.id)" class="ping" :class="pingView(selected.id)!.cls" :title="pingView(selected.id)!.text">{{ pingView(selected.id)!.text }}</span>
            <Icon name="chev" size="s" class="ns-chev" />
          </button>
        </template>
        <template #default="{ close }">
          <template v-for="g in [{ title: profiles.isSubscription ? '分享者提供的节点' : '我的节点', items: mine }, { title: '官方节点', items: official }]" :key="g.title">
            <template v-if="g.items.length">
              <div class="menu-lbl">{{ g.title }}</div>
              <button
                v-for="n in g.items"
                :key="n.id"
                type="button"
                role="option"
                class="menu-item node-opt"
                :class="{ sel: n.id === tunnel.selectedNodeId, off: !n.online }"
                :aria-selected="n.id === tunnel.selectedNodeId"
                @click="choose(n.id, close)"
              >
                <span class="nm-dot" :class="n.id === tunnel.selectedNodeId ? 'pri' : n.online ? 'ok' : ''" />
                <span class="nm-txt"><b>{{ n.label }}</b><small>{{ n.sub }}</small></span>
                <span v-if="pingView(n.id)" class="ping" :class="pingView(n.id)!.cls" :title="pingView(n.id)!.text">{{ pingView(n.id)!.text }}</span>
              </button>
            </template>
          </template>
          <div v-if="!hasNodes" class="menu-lbl">还没有节点</div>
          <div class="menu-sep" />
          <button type="button" class="menu-item" @click="close(), emit('manage-nodes')"><Icon name="edit" size="s" />管理节点</button>
        </template>
      </Menu>
      <button type="button" class="btn ghost ping-btn" :disabled="!profiles.current || !hasNodes || tunnel.pinging" title="测试所有节点的延迟" aria-label="测速" data-testid="run-ping" @click="ping">
        <Icon :name="tunnel.pinging ? 'refresh' : 'zap'" size="s" :class="{ spin: tunnel.pinging }" /><span class="btn-label">{{ tunnel.pinging ? '测速中' : '测速' }}</span>
      </button>
      <button
        type="button"
        class="btn run"
        :class="btn.cls"
        :disabled="tunnel.busy || (!tunnel.active && (!profiles.current || !tunnel.selectedNodeId))"
        data-testid="run-toggle"
        @click="toggle"
      >
        <Icon :name="btn.icon" :fill="!btn.spin" :class="{ spin: btn.spin || tunnel.acting }" />
        <span>{{ btn.text }}</span>
        <span v-if="sparkle" :key="sparkle" class="sparks" aria-hidden="true"><Icon v-for="i in 7" :key="i" name="sparkle" fill /></span>
      </button>
    </div>
  </header>
  <div class="statusline" role="status">
    <span
      class="state"
      :class="st.state"
      data-testid="run-state"
      :data-state="st.state"
      :data-runid="st.runId ?? ''"
      :data-run-id="st.runId ?? ''"
      :data-logins="st.logins"
    ><span class="d" />{{ stateLabels[st.state] ?? st.state }}</span>
    <span
      class="detail"
      :class="detailTone"
      :title="st.error?.message ? `${detail}\nfrp 原文：${st.error.message}` : detail"
      data-testid="run-detail"
    >{{ detail }}</span>
  </div>
</template>

<style>
.rh-ctrl {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  gap: 8px;
  margin-left: auto;
}
.nodesel {
  display: flex;
  align-items: center;
  gap: 9px;
  width: clamp(180px, 21vw, 280px);
  height: 42px;
  padding: 0 10px 0 6px;
  border-radius: 14px;
  background: var(--surface);
  border: 1.5px solid var(--line);
  box-shadow: var(--shadow-sm);
  text-align: left;
  transition: border-color 0.2s, box-shadow 0.2s, background-color 0.35s;
}
.nodesel:hover:not(:disabled),
.nodesel.open {
  border-color: color-mix(in oklab, var(--pri) 55%, var(--line));
}
.nodesel.open {
  box-shadow: 0 0 0 4px var(--pri-soft);
}
.nodesel:disabled {
  opacity: 0.6;
}
.ns-ic {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 10px;
  background: var(--pri-soft);
  color: var(--pri-text);
  flex-shrink: 0;
}
.ns-lbl {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  flex: 1;
  min-width: 0;
  line-height: 1.15;
}
.ns-lbl small {
  font-size: 10.5px;
  font-weight: 800;
  color: var(--text-3);
}
.ns-lbl b {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13.5px;
  font-weight: 800;
}
.nodesel .ping {
  max-width: 96px;
}
.ns-chev {
  color: var(--text-3);
  transition: transform 0.25s;
}
.nodesel.open .ns-chev {
  transform: rotate(180deg);
}
.node-opt {
  gap: 10px;
}
.node-opt.sel {
  background: var(--pri-soft);
}
.nm-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--text-3);
}
.nm-dot.ok {
  background: var(--ok);
}
.nm-dot.pri {
  background: var(--pri);
}
.nm-txt {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  line-height: 1.25;
}
.nm-txt b {
  overflow: hidden;
  text-overflow: ellipsis;
  font-weight: 800;
}
.nm-txt small {
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 600;
}
.node-opt.off .nm-txt b {
  color: var(--text-3);
}
.btn.run {
  position: relative;
  min-width: 112px;
  height: 42px;
  font-size: 14.5px;
}
.btn.run.idle {
  background: var(--pri);
  color: #fff;
  box-shadow: 0 10px 22px -8px color-mix(in oklab, var(--pri) 90%, transparent);
}
.btn.run.idle:hover:not(:disabled) {
  background: var(--pri-hover);
  transform: translateY(-1px);
}
.btn.run.busy {
  background: var(--pri-soft-2);
  color: var(--pri-text);
  opacity: 1;
}
.btn.run.stop {
  background: var(--bad-soft);
  color: var(--bad-text);
  box-shadow: inset 0 0 0 1.5px color-mix(in oklab, var(--bad) 35%, transparent);
}
.btn.run.stop:hover:not(:disabled) {
  background: color-mix(in oklab, var(--bad) 22%, var(--surface));
}
.sparks {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.sparks .ic {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 12px;
  height: 12px;
  color: var(--gold);
  opacity: 0;
  animation: spark 0.95s cubic-bezier(0.2, 0.8, 0.3, 1) forwards;
}
.sparks .ic:nth-child(1) { --a: -70deg; }
.sparks .ic:nth-child(2) { --a: -20deg; width: 9px; height: 9px; animation-delay: 0.04s; }
.sparks .ic:nth-child(3) { --a: 30deg; animation-delay: 0.08s; }
.sparks .ic:nth-child(4) { --a: 110deg; width: 9px; height: 9px; }
.sparks .ic:nth-child(5) { --a: 160deg; animation-delay: 0.06s; }
.sparks .ic:nth-child(6) { --a: 220deg; width: 10px; height: 10px; animation-delay: 0.03s; }
.sparks .ic:nth-child(7) { --a: 270deg; animation-delay: 0.1s; }
@keyframes spark {
  0% {
    opacity: 1;
    transform: translate(-50%, -50%) rotate(var(--a)) translateY(-8px) scale(0.4);
  }
  100% {
    opacity: 0;
    transform: translate(-50%, -50%) rotate(var(--a)) translateY(-40px) scale(1.1);
  }
}
.statusline {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 30px;
  padding: 10px 20px 0;
  font-size: 12.5px;
  color: var(--text-2);
  font-weight: 600;
}
.state {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  flex-shrink: 0;
  padding: 3px 11px 3px 9px;
  border-radius: 99px;
  font-weight: 800;
  font-size: 12px;
  background: var(--fill);
  color: var(--text-2);
  transition: background-color 0.3s, color 0.3s;
}
.state .d {
  position: relative;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
}
.state.running {
  background: var(--ok-soft);
  color: var(--ok-text);
}
.state.running .d::after {
  content: '';
  position: absolute;
  inset: -4px;
  border-radius: 50%;
  border: 2px solid currentColor;
  animation: ring 1.8s ease-out infinite;
}
.state.connecting {
  background: var(--info-soft);
  color: var(--info-text);
}
.state.connecting .d,
.state.reconnecting .d {
  animation: blink 0.9s ease-in-out infinite;
}
.state.reconnecting,
.state.stopping {
  background: var(--warn-soft);
  color: var(--warn-text);
}
.state.failed {
  background: var(--bad-soft);
  color: var(--bad-text);
}
.detail {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.detail.bad {
  color: var(--bad-text);
}
.detail.warn {
  color: var(--warn-text);
}
/* 窄窗口：测速只留图标，节点框收窄，让标题和按钮尽量排在一行 */
@media (max-width: 1000px) {
  .ping-btn {
    width: 42px;
    padding: 0;
  }
  .ping-btn .btn-label {
    display: none;
  }
  .btn.run {
    min-width: 96px;
  }
}
</style>
