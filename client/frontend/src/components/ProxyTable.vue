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
import { Clipboard } from '@wailsio/runtime'
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
  // 选中的官方节点不允许这种类型（用户组或单个用户的限制，面板改了实时更新）
  if (!tunnel.typeAllowed(p.type)) {
    const node = tunnel.limits?.node
    return { tone: 'bad', label: '节点不允许', text: '', title: `「${node}」不允许你使用 ${p.type}，换其他节点可以用` }
  }
  if (!isRunningHere.value) return { tone: '', label: '未运行', text: '', title: '' }
  const st = tunnel.proxyById[p.id]
  const phase = st?.phase ?? ''
  if (phase === 'running') return { tone: 'ok', label: '运行中', text: isP2PType(p.type) ? '等访问方连接' : '', title: '', phase }
  if (st?.err) {
    // 没有专门说明的错误（例如官方节点的限额拒绝）直接显示原因，不只显示"代理启动失败"
    const text = (st.code as string) === 'proxy_start_error' ? st.err : codeText(st.code, null, st.err)
    return { tone: 'bad', label: phaseLabels[phase] ?? phase, text, title: `frp 原文：${st.err}`, phase, code: (st.code as string) ?? '' }
  }
  return { tone: 'info', label: phaseLabels[phase] ?? phase, text: '', title: '', phase }
}
const typeTone = (t: string) => (isP2PType(t) ? 'p2p' : isDomainType(t) ? 'web' : '')

// 访问地址的主机部分：运行中看实际用的节点，否则看选中的节点。官方节点用面板给的访问地址，自己的节点用服务器地址
const accessHost = computed(() => {
  const id = (isRunningHere.value ? tunnel.status.nodeId : tunnel.selectedNodeId) ?? ''
  const o = tunnel.official.find((n) => n.id === id)
  if (o) return o.domain || o.ipv4 || (o.ipv6 ? `[${o.ipv6}]` : '')
  const n = profiles.current?.nodes?.find((x) => x.id === id)
  if (!n) return ''
  return n.serverAddr.includes(':') && !n.serverAddr.startsWith('[') ? `[${n.serverAddr}]` : n.serverAddr
})

// tcp / udp 的端口：运行中用节点实际分配的，否则用配置的；自动分配又没在运行时为 0
function port(p: Proxy): number {
  const st = isRunningHere.value ? tunnel.proxyById[p.id] : undefined
  const m = st?.phase === 'running' ? /:(\d+)$/.exec(st.remoteAddr ?? '') : null
  return m ? Number(m[1]) : p.remotePort || 0
}
const isPortType = (p: Proxy) => p.type === 'tcp' || p.type === 'udp'

// 远程一侧显示的文字：tcp / udp 是"访问地址:端口"，端口还不知道时是"地址:自动"
function remote(p: Proxy): { host: string; port: string; auto: boolean } {
  if (!isPortType(p)) return { host: proxyRemote(p), port: '', auto: false }
  const n = port(p)
  return { host: accessHost.value, port: n ? String(n) : '自动', auto: !n }
}

// 能复制的完整访问地址（地址和端口都知道时）
function accessAddr(p: Proxy): string {
  if (!isPortType(p) || !accessHost.value) return ''
  const n = port(p)
  return n ? `${accessHost.value}:${n}` : ''
}
async function copyAddr(text: string) {
  try {
    await Clipboard.SetText(text)
    message.success(`已复制 ${text}`)
  } catch {
    message.error('复制失败')
  }
}
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
        <div
          class="row-map mono"
          :title="`${row.localIP || '127.0.0.1'}:${row.localPort} → ${accessAddr(row) || remote(row).host + (remote(row).port ? ':' + remote(row).port : '')}${remote(row).auto ? '\n远程端口自动分配：每次连接可能不同，想固定就在代理里填远程端口' : ''}`"
          :data-testid="`proxy-map-${row.name}`"
        >
          <span class="loc">{{ row.localIP || '127.0.0.1' }}:{{ row.localPort }}</span>
          <Icon name="arrow" size="xs" />
          <span class="rem">{{ remote(row).host }}<template v-if="remote(row).port">:<span :class="{ auto: remote(row).auto }">{{ remote(row).port }}</span></template></span>
          <button
            v-if="accessAddr(row)"
            type="button"
            class="icon-btn sm copy-addr"
            :title="`复制访问地址 ${accessAddr(row)}`"
            :aria-label="`复制 ${row.name} 的访问地址`"
            :data-testid="`proxy-copy-${row.name}`"
            @click="copyAddr(accessAddr(row))"
          >
            <Icon name="copy" size="xs" />
          </button>
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
/* 列宽按内容：开关、名称、映射（不截断）、状态（占剩下的，错误原因放不下时省略、悬停看全文）、操作。
   窗口变窄时只收紧间距、名称可以省略，不换行、不折叠成多行。 */
.row {
  display: grid;
  grid-template-columns: auto minmax(64px, max-content) minmax(0, max-content) minmax(84px, 1fr) auto;
  grid-template-areas: 'sw name map st act';
  align-items: center;
  gap: 0 18px;
  padding: 10px 10px 10px 14px;
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
  white-space: nowrap;
}
.row-map .loc {
  flex-shrink: 0;
}
/* 域名很多时只有这一段会省略 */
.row-map .rem {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.row-map .rem .auto {
  color: var(--text-3);
  font-weight: 600;
  font-family: var(--font);
}
.copy-addr {
  flex-shrink: 0;
  margin-left: -3px;
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
.row-st .bdg {
  flex-shrink: 0;
}
.row-st .msg.bad {
  color: var(--bad-text);
}
.row-act {
  grid-area: act;
  display: flex;
  justify-content: flex-end;
  gap: 2px;
}
@container (max-width: 720px) {
  .row {
    gap: 0 12px;
    padding-left: 12px;
  }
}
@container (max-width: 560px) {
  .row {
    gap: 0 8px;
  }
  .row {
    padding: 9px 8px 9px 10px;
  }
  .row-map {
    gap: 5px;
    font-size: 12px;
  }
  /* 状态只留标签，原因悬停看 */
  .row-st .msg {
    display: none;
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
