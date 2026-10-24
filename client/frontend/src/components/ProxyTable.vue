<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { NButton, NDataTable, NPopconfirm, NSpace, NSwitch, NTag, NText, NTooltip, useMessage, type DataTableColumns } from 'naive-ui'
import ProxyDrawer from './ProxyDrawer.vue'
import { saveFeedback } from './saveFeedback'
import type { Proxy } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { codeText, describeError } from '../errors'
import { phaseLabels } from '../labels'
import { proxyRemote } from '../util'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const message = useMessage()

const drawer = ref<{ show: boolean; index: number }>({ show: false, index: -1 })
const rows = computed(() => (profiles.current?.proxies ?? []).map((p, index) => ({ ...p, index })))
const isRunningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
const toggling = ref<string>('')
const ro = computed(() => profiles.readonly('proxies'))

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

async function remove(index: number) {
  try {
    saveFeedback(message, await profiles.removeItem('proxies', index))
  } catch (e) {
    message.error(describeError(e))
  }
}

// 运行状态列：running / 错误原因 / 远程地址（PLAN 6.7）
function renderStatus(p: Proxy) {
  const testid = `proxy-status-${p.name}`
  if (!p.enabled) return h(NText, { depth: 3, 'data-testid': testid }, () => '未启用')
  if (!isRunningHere.value) return h(NText, { depth: 3, 'data-testid': testid }, () => '—')
  const st = tunnel.proxyById[p.id]
  const phase = st?.phase ?? ''
  if (phase === 'running') {
    return h('span', { class: 'status', 'data-testid': testid, 'data-phase': phase }, [
      h(NTag, { type: 'success', size: 'small', bordered: false }, () => '运行中'),
      st?.remoteAddr ? h(NText, { depth: 3 }, () => st.remoteAddr) : null,
    ])
  }
  if (st?.err) {
    // 没有专门说明的错误（例如官方节点的限额拒绝）直接显示原因，不只显示"代理启动失败"
    const text = (st.code as string) === 'proxy_start_error' ? st.err : codeText(st.code, null, st.err)
    return h(
      NTooltip,
      {},
      {
        trigger: () =>
          h('span', { class: 'status', 'data-testid': testid, 'data-phase': phase, 'data-code': st.code ?? '' }, [
            h(NTag, { type: 'error', size: 'small', bordered: false }, () => phaseLabels[phase] ?? phase),
            h(NText, { type: 'error' }, () => text),
          ]),
        default: () => `frp 原文：${st.err}`,
      },
    )
  }
  return h(NText, { depth: 2, 'data-testid': testid, 'data-phase': phase }, () => phaseLabels[phase] ?? phase)
}

const columns = computed<DataTableColumns<Proxy & { index: number }>>(() => [
  {
    title: '启用',
    key: 'enabled',
    width: 64,
    render: (row) =>
      ro.value
        ? h(NText, { depth: row.enabled ? 1 : 3 }, () => (row.enabled ? '是' : '否'))
        : h(NSwitch, {
        size: 'small',
        value: row.enabled,
        loading: toggling.value === row.id,
        'data-testid': `proxy-switch-${row.name}`,
        onUpdateValue: (v: boolean) => toggle(row.index, v),
      }),
  },
  { title: '名称', key: 'name', minWidth: 100, ellipsis: { tooltip: true }, render: (row) => h('span', { 'data-testid': `proxy-row-${row.name}` }, row.name) },
  { title: '类型', key: 'type', width: 72, render: (row) => h(NTag, { size: 'small', bordered: false }, () => row.type) },
  {
    title: '本地 → 远程',
    key: 'mapping',
    minWidth: 170,
    ellipsis: { tooltip: true },
    render: (row) => `${row.localIP || '127.0.0.1'}:${row.localPort} → ${proxyRemote(row)}`,
  },
  { title: '运行状态', key: 'status', width: 210, render: (row) => renderStatus(row) },
  ...(ro.value ? [] : [actionsColumn]),
])

const actionsColumn: DataTableColumns<Proxy & { index: number }>[number] = {
    key: 'actions',
    width: 104,
    render: (row) =>
      h(NSpace, { size: 4, wrap: false }, () => [
        h(
          NButton,
          { size: 'tiny', quaternary: true, 'data-testid': `proxy-edit-${row.name}`, onClick: () => (drawer.value = { show: true, index: row.index }) },
          () => '编辑',
        ),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(row.index) },
          {
            trigger: () => h(NButton, { size: 'tiny', quaternary: true, type: 'error', 'data-testid': `proxy-delete-${row.name}` }, () => '删除'),
            default: () => `删除代理 ${row.name}？`,
          },
        ),
      ]),
}
</script>

<template>
  <div>
    <div v-if="!ro" class="toolbar">
      <n-button size="small" type="primary" data-testid="proxy-add" @click="drawer = { show: true, index: -1 }">添加代理</n-button>
    </div>
    <n-data-table size="small" :columns="columns" :data="rows" :row-key="(r: any) => r.id" :bordered="false" data-testid="proxy-table" />
    <ProxyDrawer v-model:show="drawer.show" :index="drawer.index" />
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 8px;
}
:deep(.status) {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
}
</style>
