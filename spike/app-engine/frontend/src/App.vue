<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { Events } from '@wailsio/runtime'
import { AppService, TunnelService } from '../bindings/spike/app-engine'

interface View {
  engine: { state: string; runID: string; logins: number; lastError: string }
  proxy: { name: string; phase: string; err: string; remoteAddr: string } | null
  node: string
  remote: number
}

const MAX_LINES = 2000
const startup = ref('...')
const info = ref<Record<string, string>>({})
const view = ref<View | null>(null)
const lines = ref<string[]>([])
const busy = ref(false)
const actionError = ref('')
const logBox = ref<HTMLElement | null>(null)

const state = computed(() => view.value?.engine.state ?? 'idle')
const running = computed(() => !['idle', 'failed'].includes(state.value))

function levelClass(line: string) {
  if (line.includes(' [E] ')) return 'lv-e'
  if (line.includes(' [W] ')) return 'lv-w'
  return ''
}

function appendLines(batch: string[]) {
  lines.value.push(...batch)
  if (lines.value.length > MAX_LINES) lines.value.splice(0, lines.value.length - MAX_LINES)
  nextTick(() => logBox.value?.scrollTo({ top: logBox.value.scrollHeight }))
}

async function toggle() {
  busy.value = true
  actionError.value = ''
  try {
    if (running.value) await TunnelService.Stop()
    else await TunnelService.Start()
  } catch (e) {
    actionError.value = String(e)
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  Events.On('tunnel:state', (ev: { data: View }) => (view.value = ev.data))
  Events.On('tunnel:logs', (ev: { data: string[] }) => appendLines(ev.data))
  view.value = (await TunnelService.View()) as unknown as View
  appendLines((await TunnelService.Logs()) ?? [])
  info.value = ((await AppService.Info()) ?? {}) as Record<string, string>
  startup.value = await AppService.Ready()
})
</script>

<template>
  <main>
    <header>
      <h1>MoeFrp Phase 0 · app-engine</h1>
      <span class="meta">frp {{ info.frp }} · Wails {{ info.wails }} · 启动 {{ startup }}</span>
    </header>

    <section class="bar">
      <button id="toggle" :disabled="busy" @click="toggle">{{ running ? '停止' : '启动' }}</button>
      <span id="state" :class="['badge', state]">{{ state }}</span>
      <span>节点 {{ view?.node }}</span>
      <span>远程端口 {{ view?.remote }}</span>
      <span id="runid">runID {{ view?.engine.runID || '-' }}</span>
      <span id="logins">logins {{ view?.engine.logins ?? 0 }}</span>
    </section>

    <section class="proxy">
      代理 echo：
      <span id="proxy-phase">{{ view?.proxy?.phase ?? '未运行' }}</span>
      <span v-if="view?.proxy?.remoteAddr"> · {{ view?.proxy?.remoteAddr }}</span>
      <span v-if="view?.proxy?.err" class="lv-e"> · {{ view?.proxy?.err }}</span>
    </section>
    <p v-if="view?.engine.lastError" id="last-error" class="lv-e">{{ view?.engine.lastError }}</p>
    <p v-if="actionError" class="lv-e">{{ actionError }}</p>

    <!-- 日志一律按纯文本插值渲染，不用 v-html -->
    <div ref="logBox" id="logs" class="logs">
      <div v-for="(line, i) in lines" :key="i" :class="levelClass(line)">{{ line }}</div>
    </div>
  </main>
</template>

<style>
body { margin: 0; background: #06070f; color: #ddd; font-family: system-ui, sans-serif; }
main { padding: 16px 20px; display: flex; flex-direction: column; gap: 10px; height: 100vh; box-sizing: border-box; }
header { display: flex; align-items: baseline; gap: 12px; }
h1 { font-size: 18px; margin: 0; }
.meta { color: #888; font-size: 12px; }
.bar { display: flex; align-items: center; gap: 14px; font-size: 13px; }
button { padding: 6px 18px; font-size: 14px; cursor: pointer; }
.badge { padding: 2px 8px; border-radius: 4px; background: #333; }
.badge.running { background: #1d6b35; }
.badge.connecting, .badge.reconnecting, .badge.stopping { background: #7a5b12; }
.badge.failed { background: #7a1d1d; }
.proxy { font-size: 13px; }
.logs { flex: 1; overflow: auto; background: #0d0f1a; border: 1px solid #222; padding: 8px; font: 12px/1.45 Consolas, monospace; white-space: pre-wrap; word-break: break-all; }
.lv-e { color: #ff7b7b; }
.lv-w { color: #e6c35c; }
</style>
