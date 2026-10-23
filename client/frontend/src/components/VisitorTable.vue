<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { NButton, NDataTable, NInputNumber, NModal, NPopconfirm, NSpace, NSwitch, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import VisitorDrawer from './VisitorDrawer.vue'
import { saveFeedback } from './saveFeedback'
import { ShareService, type Visitor } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { describeError } from '../errors'
import { stateLabels } from '../labels'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const message = useMessage()
const drawer = ref<{ show: boolean; index: number }>({ show: false, index: -1 })
const rows = computed(() => (profiles.current?.visitors ?? []).map((v, index) => ({ ...v, index })))
const isRunningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
// P2P 订阅：访客由分享生成，只能改启用开关和本地端口（作为订阅参数保存）
const p2pSub = computed(() => profiles.isSubscription && profiles.currentSummary?.shareKind === 'p2p')
const ro = computed(() => profiles.readonly('visitors'))
const portModal = ref<{ show: boolean; id: string; name: string; port: number | null }>({ show: false, id: '', name: '', port: null })

async function updateBind(visitorId: string, patch: { enabled?: boolean; bindPort?: number }) {
  const id = profiles.currentId
  const cur = await ShareService.Subscription(id)
  const binds = (cur.params.binds ?? []).map((b) => (b.id === visitorId ? { ...b, ...patch } : b))
  return profiles.updateSubscription(id, { ...cur.params, proxies: [], binds })
}

async function savePort() {
  const m = portModal.value
  try {
    saveFeedback(message, await updateBind(m.id, { bindPort: m.port ?? 0 }))
    portModal.value.show = false
  } catch (e) {
    message.error(describeError(e))
  }
  return false
}

async function toggle(index: number, enabled: boolean) {
  try {
    if (p2pSub.value) {
      saveFeedback(message, await updateBind(rows.value[index].id, { enabled }))
      return
    }
    saveFeedback(message, await profiles.toggle('visitors', index, enabled))
  } catch (e) {
    message.error(describeError(e))
  }
}

async function remove(index: number) {
  try {
    saveFeedback(message, await profiles.removeItem('visitors', index))
  } catch (e) {
    message.error(describeError(e))
  }
}

// frp 没有对外暴露访客的状态，只能显示隧道整体的状态
function renderStatus(v: Visitor) {
  if (!v.enabled) return h(NText, { depth: 3 }, () => '未启用')
  if (!isRunningHere.value) return h(NText, { depth: 3 }, () => '—')
  if (tunnel.status.state === 'running') return h(NText, {}, () => `监听 ${v.bindAddr || '127.0.0.1'}:${v.bindPort}`)
  return h(NText, { depth: 2 }, () => stateLabels[tunnel.status.state] ?? tunnel.status.state)
}

const columns = computed<DataTableColumns<Visitor & { index: number }>>(() => [
  {
    title: '启用',
    key: 'enabled',
    width: 64,
    render: (row) =>
      ro.value && !p2pSub.value
        ? h(NText, {}, () => (row.enabled ? '是' : '否'))
        : h(NSwitch, { size: 'small', value: row.enabled, 'data-testid': `visitor-switch-${row.name}`, onUpdateValue: (val: boolean) => toggle(row.index, val) }),
  },
  { title: '名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '类型', key: 'type', width: 80, render: (row) => h(NTag, { size: 'small', bordered: false }, () => row.type) },
  { title: '目标', key: 'server', ellipsis: { tooltip: true }, render: (row) => (row.serverUser ? `${row.serverUser}.${row.serverName}` : row.serverName) },
  { title: '本地监听', key: 'bind', width: 160, render: (row) => `${row.bindAddr || '127.0.0.1'}:${row.bindPort}` },
  { title: '状态', key: 'status', width: 180, render: (row) => renderStatus(row) },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render: (row) =>
      p2pSub.value
        ? h(
            NButton,
            { size: 'tiny', quaternary: true, 'data-testid': `visitor-port-${row.name}`, onClick: () => (portModal.value = { show: true, id: row.id, name: row.name, port: row.bindPort }) },
            () => '改端口',
          )
        : ro.value
          ? null
          : h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => (drawer.value = { show: true, index: row.index }) }, () => '编辑'),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(row.index) },
          {
            trigger: () => h(NButton, { size: 'tiny', quaternary: true, type: 'error' }, () => '删除'),
            default: () => `删除访客 ${row.name}？`,
          },
        ),
      ]),
  },
])
</script>

<template>
  <div>
    <div class="toolbar">
      <n-button v-if="!ro" size="small" type="primary" data-testid="visitor-add" @click="drawer = { show: true, index: -1 }">添加访客</n-button>
      <n-text depth="3" :style="{ marginLeft: ro ? '0' : '12px', fontSize: '12px' }">{{
        p2pSub ? '启动后，在本机访问"本地监听"的地址就能连到分享者的服务' : '访问别人提供的 stcp / xtcp / sudp 代理，在本机监听一个端口'
      }}</n-text>
    </div>
    <n-data-table size="small" :columns="columns" :data="rows" :row-key="(r: any) => r.id" :bordered="false" />
    <VisitorDrawer v-model:show="drawer.show" :index="drawer.index" />
    <n-modal v-model:show="portModal.show" preset="dialog" :title="`修改 ${portModal.name} 的本地端口`" positive-text="保存" negative-text="取消" :on-positive-click="savePort">
      <n-input-number v-model:value="portModal.port" :min="1" :max="65535" :show-button="false" placeholder="本地端口" data-testid="visitor-port-input" />
    </n-modal>
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 8px;
  display: flex;
  align-items: center;
}
</style>
