<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { NButton, NSwitch, NText, NVirtualList, useMessage } from 'naive-ui'
import { useLogsStore } from '../stores/logs'
import { describeError } from '../errors'

// 日志一律按纯文本渲染（{{ }} 插值），不用 v-html（PLAN 6.3.6）。
// 行高固定，用虚拟列表，5000 行也不卡。

const ROW = 20
const logs = useLogsStore()
const message = useMessage()
const list = ref<InstanceType<typeof NVirtualList> | null>(null)
const follow = ref(true)

function toBottom() {
  if (!follow.value) return
  nextTick(() => list.value?.scrollTo({ position: 'bottom' }))
}

watch(() => logs.lines, toBottom)
watch(follow, toBottom)

async function exportLogs() {
  try {
    const path = await logs.exportToFile()
    if (path) message.success(`已导出到 ${path}`)
  } catch (e) {
    message.error(describeError(e))
  }
}

async function clear() {
  try {
    await logs.clear()
  } catch (e) {
    message.error(describeError(e))
  }
}
</script>

<template>
  <div class="log-panel" data-testid="log-panel">
    <div class="bar">
      <n-text depth="3" style="font-size: 12px">日志（{{ logs.lines.length }} 行）</n-text>
      <span style="flex: 1" />
      <n-text depth="3" style="font-size: 12px">跟随最新</n-text>
      <n-switch v-model:value="follow" size="small" />
      <n-button size="tiny" quaternary @click="clear">清空</n-button>
      <n-button size="tiny" quaternary data-testid="log-export" @click="exportLogs">导出</n-button>
    </div>
    <n-virtual-list ref="list" class="lines mono" :items="logs.lines" :item-size="ROW" key-field="seq" data-testid="log-lines">
      <template #default="{ item }">
        <div :class="['line', `lv-${item.level}`]" :title="item.text" data-testid="log-line">{{ item.text }}</div>
      </template>
    </n-virtual-list>
  </div>
</template>

<style scoped>
.log-panel {
  display: flex;
  flex-direction: column;
  border-top: 1px solid rgba(128, 128, 128, 0.2);
  min-height: 0;
}
.bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 12px;
}
.lines {
  flex: 1;
  min-height: 0;
  font-size: 12px;
  padding: 0 12px 4px;
}
.line {
  height: 20px;
  line-height: 20px;
  white-space: pre;
  overflow: hidden;
  text-overflow: ellipsis;
}
.lv-warn {
  color: #d58f00;
}
.lv-error {
  color: #d03050;
}
.lv-debug,
.lv-trace {
  opacity: 0.6;
}
</style>
