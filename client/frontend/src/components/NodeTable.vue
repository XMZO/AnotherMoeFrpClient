<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { NButton, NDataTable, NPopconfirm, NSpace, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import NodeDrawer from './NodeDrawer.vue'
import { saveFeedback } from './saveFeedback'
import type { Node } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { codeText, describeError } from '../errors'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const message = useMessage()
const drawer = ref<{ show: boolean; index: number }>({ show: false, index: -1 })
const rows = computed(() => (profiles.current?.nodes ?? []).map((n, index) => ({ ...n, index })))
const ro = computed(() => profiles.readonly('nodes'))

async function remove(index: number) {
  try {
    saveFeedback(message, await profiles.removeItem('nodes', index))
  } catch (e) {
    message.error(describeError(e))
  }
}

function renderPing(n: Node) {
  const r = tunnel.pings[n.id]
  if (!r) return h(NText, { depth: 3 }, () => '—')
  if (r.ok) return h(NText, { type: 'success' }, () => `${r.latencyMs} ms`)
  return h(NText, { type: 'error', title: r.message }, () => codeText(r.code, { target: r.target }, r.message))
}

const columns = computed<DataTableColumns<Node & { index: number }>>(() => [
  {
    title: '备注',
    key: 'remark',
    ellipsis: { tooltip: true },
    render: (row) => h('span', { 'data-testid': `node-row-${row.remark || row.serverAddr}` }, row.remark || '（无备注）'),
  },
  { title: '地址', key: 'addr', ellipsis: { tooltip: true }, render: (row) => `${row.serverAddr}:${row.serverPort}` },
  { title: '协议', key: 'protocol', width: 90, render: (row) => h(NTag, { size: 'small', bordered: false }, () => row.protocol || 'tcp') },
  { title: 'user', key: 'user', width: 100, ellipsis: { tooltip: true }, render: (row) => row.user || '—' },
  { title: '延迟', key: 'ping', width: 200, render: (row) => renderPing(row) },
  ...(ro.value ? [] : [actionsColumn]),
])

const actionsColumn: DataTableColumns<Node & { index: number }>[number] = {
    title: '操作',
    key: 'actions',
    width: 120,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(
          NButton,
          { size: 'tiny', quaternary: true, 'data-testid': `node-edit-${row.remark || row.serverAddr}`, onClick: () => (drawer.value = { show: true, index: row.index }) },
          () => '编辑',
        ),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(row.index) },
          {
            trigger: () => h(NButton, { size: 'tiny', quaternary: true, type: 'error' }, () => '删除'),
            default: () => `删除节点 ${row.remark || row.serverAddr}？`,
          },
        ),
      ]),
}
</script>

<template>
  <div>
    <div class="toolbar">
      <n-button v-if="!ro" size="small" type="primary" data-testid="node-add" @click="drawer = { show: true, index: -1 }">添加节点</n-button>
      <n-text depth="3" :style="{ marginLeft: ro ? '0' : '12px', fontSize: '12px' }">{{
        ro ? '节点由分享者提供；运行时通过底部选中的节点连接' : '运行时，配置里的所有代理和访客都通过底部选中的那个节点连接'
      }}</n-text>
    </div>
    <n-data-table size="small" :columns="columns" :data="rows" :row-key="(r: any) => r.id" :bordered="false" />
    <NodeDrawer v-model:show="drawer.show" :index="drawer.index" />
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 8px;
  display: flex;
  align-items: center;
}
</style>
