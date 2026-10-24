<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, onUpdated, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import { message } from '../ui/feedback'
import { useLogsStore } from '../stores/logs'
import { describeError } from '../errors'

// 日志一律按纯文本渲染（{{ }} 插值），不用 v-html（PLAN 6.3.6）。
// 只渲染看得见的行（虚拟列表），5000 行也不卡。长行自动换行，所以行高不固定：
// 没渲染过的行按字数和面板宽度估算高度，渲染后量出实际高度记下来（宽度变了重新量）。
const ROW = 20 // 一个显示行的高度，和 .logs-body 的 line-height 一致
const OVERSCAN = 200 // 上下多渲染的像素
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

const viewW = ref(600)
const charW = ref(7.2) // 等宽字体一个字符的宽度，挂载后实测
const measured = new Map<number, number>() // seq → 实际高度
const measuredVer = ref(0) // measured 变了就加一，触发重新计算
const cols = computed(() => Math.max(20, Math.floor((viewW.value - 36 - 2 * charW.value) / charW.value)))

// 估算一行占几个显示行：中日韩等全角字符按两个字符宽
function estimate(text: string): number {
  let w = 0
  for (const ch of text) w += ch.charCodeAt(0) > 0x2e7f ? 2 : 1
  return Math.max(1, Math.ceil(w / cols.value)) * ROW
}
// offsets[i] 是第 i 行的顶部位置，offsets[n] 是总高度
const offsets = computed(() => {
  void measuredVer.value
  const lines = logs.lines
  const out = new Float64Array(lines.length + 1)
  for (let i = 0; i < lines.length; i++) out[i + 1] = out[i] + (measured.get(lines[i].seq) ?? estimate(lines[i].text))
  return out
})
const total = computed(() => offsets.value[logs.lines.length])
// 第一个底边在 y 以下的行
function rowAt(y: number): number {
  const o = offsets.value
  let lo = 0
  let hi = logs.lines.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (o[mid + 1] <= y) lo = mid + 1
    else hi = mid
  }
  return lo
}
const range = computed(() => {
  const first = rowAt(Math.max(0, scrollTop.value - OVERSCAN))
  const last = Math.min(logs.lines.length, rowAt(scrollTop.value + viewH.value + OVERSCAN) + 1)
  return { first, last }
})
const visible = computed(() => logs.lines.slice(range.value.first, range.value.last))
const win = ref<HTMLElement | null>(null)

// 量出刚渲染的行的实际高度。视口上方的行高度变了，滚动位置跟着补偿，看着的内容不跳
function measure() {
  const el = win.value
  if (!el) return
  let changed = false
  let shift = 0
  const first = range.value.first
  const o = offsets.value
  Array.from(el.children).forEach((node, i) => {
    const row = node as HTMLElement
    const seq = Number(row.dataset.seq)
    const h = row.offsetHeight
    const line = logs.lines[first + i]
    if (!h || !line || line.seq !== seq) return
    const before = measured.get(seq) ?? estimate(line.text)
    if (h !== measured.get(seq)) {
      measured.set(seq, h)
      changed = true
      if (o[first + i] < scrollTop.value) shift += h - before
    }
  })
  if (measured.size > logs.lines.length * 2 + 100) {
    const keep = new Set(logs.lines.map((l) => l.seq))
    for (const k of measured.keys()) if (!keep.has(k)) measured.delete(k)
  }
  if (changed) {
    measuredVer.value++
    if (shift && !pinned && body.value) {
      body.value.scrollTop += shift
      scrollTop.value = body.value.scrollTop
    }
  }
}
onUpdated(measure)
const last = computed(() => logs.lines[logs.lines.length - 1])

// pinned：现在停在最底部。行高重新计算、窗口改大小时，停在底部的保持在底部
let pinned = true
function onScroll() {
  const b = body.value
  if (!b) return
  scrollTop.value = b.scrollTop
  pinned = b.scrollHeight - b.clientHeight - b.scrollTop < 4
}
function toBottom() {
  if (!follow.value || !body.value) return
  body.value.scrollTop = body.value.scrollHeight
  scrollTop.value = body.value.scrollTop
  pinned = true
}

let ro: ResizeObserver | undefined
let rowsRo: ResizeObserver | undefined
onMounted(() => {
  if (body.value) {
    ro = new ResizeObserver(() => {
      viewH.value = body.value?.clientHeight || 200
      const w = body.value?.clientWidth || 600
      if (w !== viewW.value) {
        viewW.value = w
        measured.clear() // 宽度变了，换行位置跟着变
        measuredVer.value++
      }
      if (pinned) nextTick(toBottom) // 面板变矮时总高度没变，也要贴住底部
    })
    ro.observe(body.value)
  }
  // 只是重新排版（字体加载完、宽度变化）时 Vue 不会重新渲染，靠这个重新量行高
  if (win.value) {
    rowsRo = new ResizeObserver(() => measure())
    rowsRo.observe(win.value)
  }
  measureChar()
  document.fonts?.ready.then(measureChar).catch(() => {})
  nextTick(toBottom)
})
onBeforeUnmount(() => {
  ro?.disconnect()
  rowsRo?.disconnect()
})

// 量等宽字体的字符宽度，用来估算没渲染过的行有多高
function measureChar() {
  if (!body.value) return
  const probe = document.createElement('span')
  probe.style.cssText = 'position:absolute;visibility:hidden;white-space:pre'
  probe.textContent = '0'.repeat(100)
  body.value.appendChild(probe)
  const w = probe.getBoundingClientRect().width / 100
  probe.remove()
  if (w && w !== charW.value) {
    charW.value = w
    measuredVer.value++
  }
}

watch(
  () => logs.lines,
  () => nextTick(toBottom),
)
watch(follow, () => nextTick(toBottom))
watch(total, () => {
  if (pinned) nextTick(toBottom)
})

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
        <div ref="win" class="logs-window" :style="{ transform: `translateY(${offsets[range.first]}px)` }">
          <div v-for="item in visible" :key="item.seq" :class="['ll', `lv-${item.level}`]" :data-seq="item.seq" data-testid="log-line">{{ item.text }}</div>
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
/* 长行换行，接着的行缩进两格，一眼能看出是同一条 */
.ll {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  padding-left: 2ch;
  text-indent: -2ch;
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
