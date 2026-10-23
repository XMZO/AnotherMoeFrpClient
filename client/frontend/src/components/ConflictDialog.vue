<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NModal, NSpace, NText, useMessage } from 'naive-ui'
import { useAccountStore } from '../stores/account'
import { describeError, fieldLabels } from '../errors'
import { diffSectionLabels } from '../labels'
import type { DiffItem } from '../api'

// 同步冲突弹窗（PLAN 6.6）：本机和云端都改了同一份配置，让用户选"保留本机"或"使用云端"，并显示差异摘要。
const account = useAccountStore()
const message = useMessage()
const busy = ref(false)
// 用户点了"稍后处理"的冲突（按 ID + 云端版本记），出现新冲突或云端又变了才再弹
const deferred = ref(new Set<string>())

const key = (c: { profileId: string; cloudRevision: number; cloudDeleted: boolean; localDeleted: boolean }) =>
  `${c.profileId}:${c.cloudRevision}:${c.cloudDeleted}:${c.localDeleted}`
const current = computed(() => account.conflicts.find((c) => !deferred.value.has(key(c))) ?? null)
const show = computed(() => !!current.value)

// 状态栏上点"N 个冲突"可以重新打开
watch(
  () => account.sync.conflicts,
  (n, old) => {
    if (n > (old ?? 0)) deferred.value = new Set()
  },
)

const summary = computed(() => {
  const c = current.value
  if (!c) return ''
  if (c.cloudDeleted && c.running) return '这份配置已在云端（其他设备上）被删除，而它正在本机运行。'
  if (c.cloudDeleted) return '这份配置已在云端（其他设备上）被删除，而本机还有没同步的修改。'
  if (c.localDeleted) return '你在本机删除了这份配置，但它在云端（其他设备上）又被修改了。'
  return '这份配置在本机和云端（其他设备上）都被修改了，两边的内容不一致。'
})

const localText = computed(() => {
  const c = current.value
  if (!c) return ''
  return c.localDeleted ? '在云端也删除这份配置' : '用本机的内容覆盖云端'
})
const cloudText = computed(() => {
  const c = current.value
  if (!c) return ''
  if (c.cloudDeleted) return c.running ? '停止隧道并删除本机的这份配置' : '删除本机的这份配置'
  if (c.localDeleted) return '恢复云端的这份配置'
  return c.running ? '用云端的内容覆盖本机，并应用到运行中的隧道' : '用云端的内容覆盖本机'
})

function describe(d: DiffItem): string {
  const sec = diffSectionLabels[d.section] ?? d.section
  if (d.section === 'profile') return `名称：本机「${d.local}」，云端「${d.cloud}」`
  const name = d.name ? `「${d.name}」` : ''
  if (d.change === 'local_only') return `${sec}${name}：只在本机`
  if (d.change === 'cloud_only') return `${sec}${name}：只在云端`
  const fields = (d.fields ?? []).map((f) => fieldLabels[f] ?? f).join('、')
  return `${sec}${name}：两边不同（${fields}）`
}

async function resolve(keep: 'local' | 'cloud') {
  const c = current.value
  if (!c) return
  busy.value = true
  try {
    const res = await account.resolve(c.profileId, keep)
    if (res.applyError) message.warning(`已使用云端的内容，但没能应用到运行中的隧道：${describeError(res.applyError)}`, { duration: 6000 })
    else if (res.applied === 'hot') message.success('已使用云端的内容，并已热更新到运行中的隧道')
    else if (res.applied === 'restart') message.info('已使用云端的内容，节点连接参数有变化，正在重启隧道')
    else if (res.stopped) message.info('隧道已停止，配置已删除')
    else message.success(keep === 'local' ? '已保留本机的内容，正在同步到云端' : '已使用云端的内容')
  } catch (e) {
    message.error(describeError(e))
    account.refreshConflicts().catch(() => {})
  } finally {
    busy.value = false
  }
}

function later() {
  if (current.value) deferred.value = new Set([...deferred.value, key(current.value)])
}
</script>

<template>
  <n-modal :show="show" preset="card" :closable="false" :mask-closable="false" style="width: 520px" title="同步冲突">
    <div v-if="current" data-testid="conflict-dialog" :data-profile-id="current.profileId">
      <n-text strong style="font-size: 15px" data-testid="conflict-name">{{ current.name }}</n-text>
      <p style="margin: 8px 0">{{ summary }}</p>
      <div v-if="current.diff?.length" class="diff" data-testid="conflict-diff">
        <div class="diff-title">差异摘要</div>
        <div v-for="(d, i) in current.diff" :key="i" class="diff-row">{{ describe(d) }}</div>
      </div>
      <n-text v-if="current.cloudUpdatedAt && !current.cloudDeleted" depth="3" style="font-size: 12px">
        云端版本 {{ current.cloudRevision }}，修改于 {{ new Date(current.cloudUpdatedAt).toLocaleString() }}
      </n-text>
      <div class="choices">
        <div class="choice">
          <n-button type="primary" block :loading="busy" data-testid="conflict-keep-local" @click="resolve('local')">保留本机</n-button>
          <n-text depth="3" class="hint">{{ localText }}</n-text>
        </div>
        <div class="choice">
          <n-button block :loading="busy" data-testid="conflict-keep-cloud" @click="resolve('cloud')">使用云端</n-button>
          <n-text depth="3" class="hint">{{ cloudText }}</n-text>
        </div>
      </div>
      <n-space justify="end" style="margin-top: 8px">
        <n-button text size="small" data-testid="conflict-later" @click="later">稍后处理</n-button>
      </n-space>
    </div>
  </n-modal>
</template>

<style scoped>
.diff {
  max-height: 200px;
  overflow: auto;
  padding: 8px 10px;
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.08);
  margin-bottom: 8px;
  font-size: 13px;
}
.diff-title {
  font-weight: 500;
  margin-bottom: 4px;
}
.diff-row {
  line-height: 1.7;
}
.choices {
  display: flex;
  gap: 12px;
  margin-top: 16px;
}
.choice {
  flex: 1;
}
.hint {
  display: block;
  font-size: 12px;
  margin-top: 4px;
}
</style>
