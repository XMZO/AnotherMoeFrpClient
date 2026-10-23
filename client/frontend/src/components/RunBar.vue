<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NSelect, NTag, NText, useMessage } from 'naive-ui'
import { useProfilesStore } from '../stores/profiles'
import { useSettingsStore } from '../stores/settings'
import { useTunnelStore } from '../stores/tunnel'
import { codeText, describeError } from '../errors'
import { stateLabels, stateTagTypes } from '../labels'
import { nodeLabel } from '../util'

const showLogs = defineModel<boolean>('showLogs', { default: true })

const profiles = useProfilesStore()
const settings = useSettingsStore()
const tunnel = useTunnelStore()
const message = useMessage()

const nodeId = ref('')
const st = computed(() => tunnel.status)
const runningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
const failedHere = computed(() => st.value.state === 'failed' && st.value.profileId === profiles.current?.id)

// 默认节点：正在运行的 → 上次给这份配置选的 → 第一个
watch(
  () => [profiles.current?.id, profiles.current?.nodes?.length, st.value.nodeId, runningHere.value] as const,
  () => {
    const p = profiles.current
    const ids = (p?.nodes ?? []).map((n) => n.id)
    if (!p || ids.length === 0) {
      nodeId.value = ''
      return
    }
    if (runningHere.value && st.value.nodeId && ids.includes(st.value.nodeId)) {
      nodeId.value = st.value.nodeId
      return
    }
    if (ids.includes(nodeId.value)) return
    const last = settings.settings?.lastNodeIds?.[p.id]
    const subDefault = profiles.currentSummary?.defaultNodeId // 订阅：订阅时选的节点
    nodeId.value = last && ids.includes(last) ? last : subDefault && ids.includes(subDefault) ? subDefault : ids[0]
  },
  { immediate: true },
)

const nodeOptions = computed(() =>
  (profiles.current?.nodes ?? []).map((n) => {
    const r = tunnel.pings[n.id]
    let suffix = ''
    if (r) suffix = r.ok ? `  ·  ${r.latencyMs} ms` : `  ·  ${codeText(r.code, { target: r.target }, '失败')}`
    return { label: nodeLabel(n) + suffix, value: n.id }
  }),
)

const buttonText = computed(() => {
  if (st.value.state === 'stopping') return st.value.pending ? '切换中…' : '停止中…'
  if (tunnel.active) return '停止'
  return '启动'
})

// 状态说明：失败原因、重连原因、运行的是别的配置
const detail = computed(() => {
  const s = st.value
  if ((s.state === 'failed' || s.state === 'reconnecting') && s.error) return describeError(s.error)
  if (tunnel.active && !runningHere.value && s.profileName) return `正在运行另一份配置：${s.profileName}`
  if (s.state === 'running' && runningHere.value) return `已连接 ${s.nodeLabel ?? ''}`
  return ''
})

async function toggle() {
  try {
    if (tunnel.active) {
      await tunnel.stop()
      return
    }
    if (!profiles.current) return
    if (!nodeId.value) {
      message.warning(codeText('node_required'))
      return
    }
    await tunnel.start(profiles.current.id, nodeId.value)
    await settings.load()
  } catch (e) {
    message.error(describeError(e), { duration: 6000 })
  }
}

// 运行中切换节点：按 6.3.5 停掉再重启（等旧的 Run 返回）
async function onNodeChange(id: string) {
  nodeId.value = id
  if (runningHere.value && st.value.state !== 'stopping' && id !== st.value.nodeId && profiles.current) {
    try {
      await tunnel.start(profiles.current.id, id)
    } catch (e) {
      message.error(describeError(e))
    }
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
  <div class="run-bar">
    <n-text depth="3">节点</n-text>
    <n-select
      class="node"
      size="small"
      :value="nodeId || null"
      :options="nodeOptions"
      :disabled="!profiles.current || tunnel.busy"
      placeholder="先在「节点」里添加"
      data-testid="run-node"
      @update:value="onNodeChange"
    />
    <n-button size="small" :loading="tunnel.pinging" :disabled="!profiles.current?.nodes?.length" data-testid="run-ping" @click="ping">测速</n-button>
    <n-button
      size="small"
      :type="tunnel.active ? 'error' : 'primary'"
      :disabled="tunnel.busy || (!tunnel.active && (!profiles.current || !nodeId))"
      :loading="tunnel.acting"
      data-testid="run-toggle"
      style="min-width: 76px"
      @click="toggle"
    >
      {{ buttonText }}
    </n-button>
    <n-tag :type="stateTagTypes[st.state] ?? 'default'" size="small" :bordered="false" data-testid="run-state" :data-state="st.state" :data-runid="st.runId ?? ''" :data-logins="st.logins">
      {{ stateLabels[st.state] ?? st.state }}
    </n-tag>
    <n-text
      class="detail"
      :type="failedHere || st.state === 'failed' ? 'error' : st.state === 'reconnecting' ? 'warning' : undefined"
      :depth="st.state === 'failed' || st.state === 'reconnecting' ? undefined : 3"
      :title="st.error?.message ? `${detail}\nfrp 原文：${st.error.message}` : detail"
      data-testid="run-detail"
    >
      {{ detail }}
    </n-text>
    <n-button size="small" quaternary data-testid="toggle-logs" @click="showLogs = !showLogs">{{ showLogs ? '收起日志' : '日志' }}</n-button>
  </div>
</template>

<style scoped>
.run-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-top: 1px solid rgba(128, 128, 128, 0.2);
}
.node {
  width: 260px;
}
.detail {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}
</style>
