<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import { message } from '../ui/feedback'
import { useAccountStore } from '../stores/account'
import { describeError, fieldLabels } from '../errors'
import { diffSectionLabels } from '../labels'
import type { DiffItem } from '../api'
import { formatTime } from './shareFormat'

// 同步冲突弹窗（PLAN 6.6）：本机和云端都改了同一份配置，让用户选"保留本机"或"使用云端"，并显示差异摘要。
const account = useAccountStore()
const busy = ref<'' | 'local' | 'cloud'>('')
// 用户点了"稍后处理"的冲突（按 ID + 云端版本记），出现新冲突或云端又变了才再弹
const deferred = ref(new Set<string>())

const key = (c: { profileId: string; cloudRevision: number; cloudDeleted: boolean; localDeleted: boolean }) =>
  `${c.profileId}:${c.cloudRevision}:${c.cloudDeleted}:${c.localDeleted}`
const current = computed(() => account.conflicts.find((c) => !deferred.value.has(key(c))) ?? null)
const show = computed(() => !!current.value)

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
  if (!c || busy.value) return
  busy.value = keep
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
    busy.value = ''
  }
}

function later() {
  if (current.value) deferred.value = new Set([...deferred.value, key(current.value)])
}
</script>

<template>
  <Modal :show="show" :closable="false" :mask-closable="false" :width="580" title="同步冲突" :subtitle="current?.name" icon="alert" tone="gold">
    <div v-if="current" data-testid="conflict-dialog" :data-profile-id="current.profileId">
      <b class="sr-only" data-testid="conflict-name">{{ current.name }}</b>
      <p class="dlg-text">{{ summary }}</p>
      <div v-if="current.diff?.length" class="diff" data-testid="conflict-diff">
        <b>差异摘要</b>
        <div v-for="(d, i) in current.diff" :key="i"><Icon name="arrow" size="xs" /><span>{{ describe(d) }}</span></div>
      </div>
      <p v-if="current.cloudUpdatedAt && !current.cloudDeleted" class="fld-help">
        云端版本 {{ current.cloudRevision }}，修改于 {{ formatTime(current.cloudUpdatedAt) }}
      </p>
      <div class="choices">
        <button type="button" class="choice pri" :disabled="!!busy" data-testid="conflict-keep-local" @click="resolve('local')">
          <b><Icon :name="busy === 'local' ? 'refresh' : 'laptop'" size="s" :class="{ spin: busy === 'local' }" />保留本机</b>
          <small>{{ localText }}</small>
        </button>
        <button type="button" class="choice" :disabled="!!busy" data-testid="conflict-keep-cloud" @click="resolve('cloud')">
          <b><Icon :name="busy === 'cloud' ? 'refresh' : 'cloud'" size="s" :class="{ spin: busy === 'cloud' }" />使用云端</b>
          <small>{{ cloudText }}</small>
        </button>
      </div>
    </div>
    <template #footer>
      <button type="button" class="btn quiet xs" data-testid="conflict-later" @click="later">稍后处理</button>
    </template>
  </Modal>
</template>

<style>
.diff {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 12px 0;
  padding: 12px 14px;
  border-radius: 14px;
  background: var(--fill);
  font-size: 12.5px;
  font-weight: 700;
}
.diff b {
  font-size: 11.5px;
  color: var(--text-3);
}
.diff div {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.diff div .ic {
  margin-top: 3px;
  color: var(--warn-text);
}
.choices {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-top: 14px;
}
.choice {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  padding: 14px;
  border-radius: 18px;
  border: 2px solid var(--line-2);
  text-align: left;
  transition: border-color 0.2s, box-shadow 0.2s, transform 0.2s var(--ease-spring);
}
.choice:hover:not(:disabled) {
  transform: translateY(-2px);
  border-color: var(--pri);
  box-shadow: 0 0 0 4px var(--pri-soft);
}
.choice:disabled {
  opacity: 0.7;
}
.choice b {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 900;
}
.choice small {
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
  line-height: 1.5;
}
.choice.pri {
  background: var(--pri-soft);
  border-color: color-mix(in oklab, var(--pri) 35%, var(--line-2));
}
</style>
