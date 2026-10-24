<script setup lang="ts">
import { computed, ref } from 'vue'
import ProxyDrawer from './ProxyDrawer.vue'
import { saveFeedback } from './saveFeedback'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import { dialog, message } from '../ui/feedback'
import type { Proxy } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { codeText, describeError } from '../errors'
import { isDomainType, isP2PType, phaseLabels } from '../labels'
import { proxyRemote } from '../util'
import mascot from '../assets/mascot.png'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()

const drawer = ref<{ show: boolean; index: number }>({ show: false, index: -1 })
const rows = computed(() => (profiles.current?.proxies ?? []).map((p, index) => ({ ...p, index })))
const isRunningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
const toggling = ref<string>('')
const ro = computed(() => profiles.readonly('proxies'))

defineExpose({ openAdd: () => (drawer.value = { show: true, index: -1 }) })

async function toggle(index: number, enabled: boolean) {
  toggling.value = rows.value[index].id
  try {
    saveFeedback(message, await profiles.toggle('proxies', index, enabled))
  } catch (e) {
    message.error(describeError(e))
  } finally {
    toggling.value = ''
  }
}

function askRemove(p: Proxy & { index: number }) {
  dialog.warning({
    title: '删除代理',
    content: `删除代理 ${p.name}？`,
    icon: 'trash',
    danger: true,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        saveFeedback(message, await profiles.removeItem('proxies', p.index))
      } catch (e) {
        message.error(describeError(e))
      }
    },
  })
}

// 运行状态：运行中 + 远程地址 / 错误原因 / 阶段（PLAN 6.7）
interface StatusView {
  tone: string
  label: string
  text: string
  title: string
  phase?: string
  code?: string
}
function status(p: Proxy): StatusView {
  if (!p.enabled) return { tone: '', label: '未启用', text: '', title: '' }
  if (!isRunningHere.value) return { tone: '', label: '未运行', text: '', title: '' }
  const st = tunnel.proxyById[p.id]
  const phase = st?.phase ?? ''
  if (phase === 'running') return { tone: 'ok', label: '运行中', text: st?.remoteAddr ?? (isP2PType(p.type) ? '等访问方连接' : ''), title: '', phase }
  if (st?.err) {
    // 没有专门说明的错误（例如官方节点的限额拒绝）直接显示原因，不只显示"代理启动失败"
    const text = (st.code as string) === 'proxy_start_error' ? st.err : codeText(st.code, null, st.err)
    return { tone: 'bad', label: phaseLabels[phase] ?? phase, text, title: `frp 原文：${st.err}`, phase, code: (st.code as string) ?? '' }
  }
  return { tone: 'info', label: phaseLabels[phase] ?? phase, text: '', title: '', phase }
}
const typeTone = (t: string) => (isP2PType(t) ? 'p2p' : isDomainType(t) ? 'web' : '')
</script>

<template>
  <div class="rows" data-testid="proxy-table">
    <TransitionGroup name="row">
      <div v-for="(row, i) in rows" :key="row.id" class="row" :class="{ off: !row.enabled, err: status(row).tone === 'bad' }" :style="{ animationDelay: `${i * 0.04}s` }">
        <Switch
          :model-value="row.enabled"
          :disabled="ro"
          :loading="toggling === row.id"
          :label="`启用 ${row.name}`"
          class="row-sw"
          :data-testid="`proxy-switch-${row.name}`"
          @change="(v: boolean) => toggle(row.index, v)"
        />
        <div class="row-name">
          <b :title="row.name" :data-testid="`proxy-row-${row.name}`">{{ row.name }}</b>
          <span class="type" :class="typeTone(row.type)">{{ row.type }}</span>
          <span v-if="row.useEncryption" class="enc" title="已开启加密"><Icon name="lock" size="xs" /></span>
        </div>
        <div class="row-map mono" :title="`${row.localIP || '127.0.0.1'}:${row.localPort} → ${proxyRemote(row)}`">
          <span>{{ row.localIP || '127.0.0.1' }}:{{ row.localPort }}</span>
          <Icon name="arrow" size="xs" />
          <span class="rem">{{ proxyRemote(row) }}</span>
        </div>
        <div
          class="row-st"
          :title="status(row).title || undefined"
          :data-testid="`proxy-status-${row.name}`"
          :data-phase="status(row).phase"
          :data-code="status(row).code"
        >
          <span class="bdg" :class="status(row).tone">{{ status(row).label }}</span>
          <span v-if="status(row).text" class="msg" :class="{ bad: status(row).tone === 'bad' }">{{ status(row).text }}</span>
        </div>
        <div class="row-act">
          <template v-if="!ro">
            <button type="button" class="icon-btn" title="编辑" :aria-label="`编辑 ${row.name}`" :data-testid="`proxy-edit-${row.name}`" @click="drawer = { show: true, index: row.index }">
              <Icon name="edit" size="s" />
            </button>
            <button type="button" class="icon-btn del" title="删除" :aria-label="`删除 ${row.name}`" :data-testid="`proxy-delete-${row.name}`" @click="askRemove(row)">
              <Icon name="trash" size="s" />
            </button>
          </template>
        </div>
      </div>
    </TransitionGroup>
    <div v-if="!rows.length" class="empty">
      <img :src="mascot" alt="" />
      <b>还没有代理</b>
      <p>{{ ro ? '分享者还没有设置代理' : '点右上角的“添加代理”，把本机的一个端口映射出去' }}</p>
    </div>
    <ProxyDrawer v-model:show="drawer.show" :index="drawer.index" />
  </div>
</template>

<style>
.rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.row {
  display: grid;
  grid-template-columns: 44px minmax(110px, 0.9fr) minmax(170px, 1.3fr) minmax(170px, 1.5fr) 76px;
  grid-template-areas: 'sw name map st act';
  align-items: center;
  gap: 6px 14px;
  padding: 11px 12px 11px 14px;
  border-radius: 17px;
  background: var(--surface);
  border: 1.5px solid var(--line-2);
  box-shadow: var(--shadow-sm);
  animation: rowIn 0.45s var(--ease-out) backwards;
  transition: border-color 0.2s, box-shadow 0.25s, background-color 0.35s;
}
.row:hover {
  border-color: var(--line);
  box-shadow: var(--shadow);
}
.row.err {
  border-color: color-mix(in oklab, var(--bad) 30%, var(--line-2));
}
.row > .sw,
.row > .row-sw {
  grid-area: sw;
}
.row-name {
  grid-area: name;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.row-name b {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  font-weight: 800;
}
.row.off .row-name b,
.row.off .row-map,
.row.off .row-map .rem {
  color: var(--text-3);
}
.enc {
  color: var(--ok-text);
  flex-shrink: 0;
}
.row-map {
  grid-area: map;
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  color: var(--text-2);
}
.row-map span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.row-map .rem {
  color: var(--text);
  font-weight: 600;
}
.row-map .ic {
  color: var(--text-3);
}
.row-st {
  grid-area: st;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 12.5px;
  font-weight: 600;
}
.row-st .msg {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-2);
}
.row-st .msg.bad {
  color: var(--bad-text);
  white-space: normal;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  line-height: 1.4;
}
.row-act {
  grid-area: act;
  display: flex;
  justify-content: flex-end;
  gap: 2px;
}
@container (max-width: 720px) {
  .row {
    grid-template-columns: 44px minmax(0, 1fr) minmax(0, 1.15fr) 72px;
    grid-template-areas: 'sw name st act' 'sw map map act';
    row-gap: 3px;
  }
}
@container (max-width: 470px) {
  .row {
    grid-template-columns: 44px minmax(0, 1fr) 72px;
    grid-template-areas: 'sw name act' 'sw map act' 'sw st act';
  }
}
.row-enter-from {
  opacity: 0;
  transform: translateY(8px);
}
.row-enter-active {
  transition: opacity 0.35s, transform 0.4s var(--ease-out);
}
.row-leave-active {
  transition: opacity 0.2s, transform 0.25s;
}
.row-leave-to {
  opacity: 0;
  transform: translateX(16px);
}
.row-move {
  transition: transform 0.35s var(--ease-out);
}
</style>
