<script setup lang="ts">
// 自绘标题栏（窗口是无边框的，见 main.go 的 Frameless）。拖动、贴边、改大小由 Wails 运行时处理：
// 带 --wails-draggable: drag 的区域能拖动窗口，窗口边缘能拖动改大小。关闭按钮和系统的一样走 WindowClosing（默认弹窗询问最小化到托盘还是退出，可在设置里固定）。
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'

const maximised = ref(false)
const sync = () => Window.IsMaximised().then((v) => (maximised.value = v)).catch(() => {})
onMounted(() => {
  sync()
  window.addEventListener('resize', sync)
})
onBeforeUnmount(() => window.removeEventListener('resize', sync))

const run = (p: Promise<void>) => p.catch(() => {})
function toggle() {
  run(Window.ToggleMaximise())
}
// 双击空白处最大化 / 还原（按钮上的双击不算）
function onDblClick(e: MouseEvent) {
  if (!(e.target as HTMLElement).closest('.tb-btns')) toggle()
}
</script>

<template>
  <header class="titlebar" @dblclick="onDblClick">
    <span class="tb-title">萌！FRP 高级客户端</span>
    <div class="tb-btns">
      <button type="button" class="tb-btn" title="最小化" aria-label="最小化" @click="run(Window.Minimise())">
        <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M0 5h10" /></svg>
      </button>
      <button type="button" class="tb-btn" :title="maximised ? '还原' : '最大化'" :aria-label="maximised ? '还原' : '最大化'" @click="toggle">
        <svg v-if="maximised" viewBox="0 0 10 10" aria-hidden="true"><path d="M2.5 2.5V.5h7v7h-2M.5 2.5h7v7h-7z" /></svg>
        <svg v-else viewBox="0 0 10 10" aria-hidden="true"><path d="M.5 .5h9v9h-9z" /></svg>
      </button>
      <button type="button" class="tb-btn close" title="关闭" aria-label="关闭" data-testid="window-close" @click="run(Window.Close())">
        <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M.5 .5l9 9M9.5 .5l-9 9" /></svg>
      </button>
    </div>
  </header>
</template>

<style>
.titlebar {
  --wails-draggable: drag;
  position: relative;
  z-index: 6;
  flex: none;
  display: flex;
  align-items: center;
  height: var(--titlebar-h);
  padding-left: 16px;
  user-select: none;
}
.tb-title {
  font-size: 12px;
  font-weight: 700;
  color: var(--text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tb-btns {
  --wails-draggable: no-drag;
  display: flex;
  height: 100%;
  margin-left: auto;
}
.tb-btn {
  display: grid;
  place-items: center;
  width: 46px;
  height: 100%;
  color: var(--text-2);
  transition: background-color 0.15s, color 0.15s;
}
.tb-btn svg {
  width: 10px;
  height: 10px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1;
  shape-rendering: crispEdges;
}
.tb-btn.close svg {
  shape-rendering: geometricPrecision;
}
.tb-btn:hover {
  background: color-mix(in oklab, var(--text) 9%, transparent);
  color: var(--text);
}
.tb-btn:active {
  background: color-mix(in oklab, var(--text) 15%, transparent);
}
.tb-btn.close:hover {
  background: #c42b1c;
  color: #fff;
}
.tb-btn.close:active {
  background: #b22a1b;
  color: #fff;
}
</style>
