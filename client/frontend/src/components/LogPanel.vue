<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import { message } from '../ui/feedback'
import { useLogsStore } from '../stores/logs'
import { describeError } from '../errors'

// 日志一律按纯文本渲染（{{ }} 插值），不用 v-html（PLAN 6.3.6）。
// 行高固定，只渲染看得见的行（虚拟列表），5000 行也不卡。
const ROW = 20
const OVERSCAN = 10
const OPEN_KEY = 'moefrp.logs.open'

const logs = useLogsStore()
const body = ref<HTMLElement | null>(null)
const follow = ref(true)
const open = ref(readOpen())
const scrollTop = ref(0)
const viewH = ref(200)

function readOpen(): boolean {
  try {
    return localStorage.getItem(OPEN_KEY) !== '0'
  } catch {
    return true
  }
}
function toggleOpen() {
  open.value = !open.value
  try {
    localStorage.setItem(OPEN_KEY, open.value ? '1' : '0')
  } catch {}
  if (open.value) nextTick(() => setTimeout(toBottom, 50))
}

const total = computed(() => logs.lines.length * ROW)
const range = computed(() => {
  const first = Math.max(0, Math.floor(scrollTop.value / ROW) - OVERSCAN)
  const last = Math.min(logs.lines.length, Math.ceil((scrollTop.value + viewH.value) / ROW) + OVERSCAN)
  return { first, last }
})
const visible = computed(() => logs.lines.slice(range.value.first, range.value.last))
const last = computed(() => logs.lines[logs.lines.length - 1])

function onScroll() {
  if (body.value) scrollTop.value = body.value.scrollTop
}
function toBottom() {
  if (!follow.value || !body.value) return
  body.value.scrollTop = body.value.scrollHeight
  scrollTop.value = body.value.scrollTop
}

let ro: ResizeObserver | undefined
onMounted(() => {
  if (body.value) {
    ro = new ResizeObserver(() => {
      viewH.value = body.value?.clientHeight || 200
    })
    ro.observe(body.value)
  }
  nextTick(toBottom)
})
onBeforeUnmount(() => ro?.disconnect())

watch(
  () => logs.lines,
  () => nextTick(toBottom),
)
watch(follow, () => nextTick(toBottom))

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
  <section class="log-panel" :class="{ open }" data-testid="log-panel">
    <div class="logs-bar">
      <button type="button" class="logs-toggle" :aria-expanded="open" data-testid="toggle-logs" @click="toggleOpen">
        <Icon name="terminal" size="s" />
        <span>日志</span>
        <span class="logs-n">{{ logs.lines.length }}</span>
        <span v-if="!open" class="logs-peek mono" :class="last ? `lv-${last.level}` : ''">{{ last?.text ?? '还没有日志' }}</span>
        <span v-else class="grow" />
        <Icon name="chevUp" size="s" class="logs-chev" />
      </button>
      <Transition name="fade">
        <div v-if="open" class="logs-tools">
          <span class="follow">跟随最新<Switch v-model="follow" small label="跟随最新" /></span>
          <button type="button" class="btn quiet xs" @click="clear">清空</button>
          <button type="button" class="btn quiet xs" data-testid="log-export" @click="exportLogs"><Icon name="download" size="xs" />导出</button>
        </div>
      </Transition>
    </div>
    <div ref="body" class="logs-body mono" data-testid="log-lines" @scroll.passive="onScroll">
      <div class="logs-spacer" :style="{ height: `${total}px` }">
        <div class="logs-window" :style="{ transform: `translateY(${range.first * ROW}px)` }">
          <div v-for="item in visible" :key="item.seq" :class="['ll', `lv-${item.level}`]" :title="item.text" data-testid="log-line">{{ item.text }}</div>
        </div>
      </div>
    </div>
  </section>
</template>

<style>
.log-panel {
  position: relative;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  height: 44px;
  background: var(--log);
  border-top: 1.5px solid var(--line-2);
  transition: height 0.45s cubic-bezier(0.3, 1.15, 0.4, 1), background-color 0.35s;
}
.log-panel.open {
  height: clamp(150px, 30vh, 260px);
}
.logs-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 42px;
  flex-shrink: 0;
  padding: 0 12px 0 10px;
}
.logs-toggle {
  display: flex;
  align-items: center;
  gap: 9px;
  flex: 1;
  min-width: 0;
  height: 34px;
  padding: 0 8px;
  border-radius: 11px;
  font-weight: 800;
  font-size: 12.5px;
  color: var(--text-2);
}
.logs-toggle:hover {
  background: var(--fill);
  color: var(--text);
}
.logs-toggle .grow {
  flex: 1;
}
.logs-n {
  min-width: 18px;
  padding: 0 6px;
  border-radius: 99px;
  background: var(--fill);
  font-size: 11px;
  text-align: center;
}
.logs-peek {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
  color: var(--text-3);
  font-size: 12px;
}
.logs-chev {
  color: var(--text-3);
  transition: transform 0.3s;
}
.log-panel.open .logs-chev {
  transform: rotate(180deg);
}
.logs-tools {
  display: flex;
  align-items: center;
  gap: 4px;
}
.follow {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 0 6px;
  font-size: 12px;
  font-weight: 700;
  color: var(--text-2);
  white-space: nowrap;
}
.logs-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0 18px;
  font-size: 12px;
  line-height: 20px;
}
.logs-spacer {
  position: relative;
}
.logs-window {
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
}
.ll {
  height: 20px;
  white-space: pre;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text);
}
.lv-warn {
  color: var(--warn-text);
}
.lv-error {
  color: var(--bad-text);
}
.lv-debug,
.lv-trace {
  color: var(--text-3);
}
</style>
