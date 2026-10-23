<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NDropdown, NModal, NSpin, NText, useMessage } from 'naive-ui'
import { AppService, ImageService, type ImageInfo } from '../api'
import { useSettingsStore } from '../stores/settings'
import { codeText, describeError, toAppError } from '../errors'

// 图片面板（PLAN 6.9）：图源由服务端下发，图片由 Go 端下载后从本地地址显示。
// 每 60 秒换一张（窗口不可见时不换）；右键：换一张、复制、另存为、查看原图。
const REFRESH_MS = 60_000

const settings = useSettingsStore()
const message = useMessage()
const img = ref<ImageInfo | null>(null)
const loading = ref(false)
const error = ref('')
const viewer = ref(false)
const zoom = ref(1)
const menu = ref({ show: false, x: 0, y: 0 })
let timer: ReturnType<typeof setInterval> | undefined
let retry: ReturnType<typeof setTimeout> | undefined

const on = computed(() => settings.settings?.imagePanel ?? false)

async function next(manual = false) {
  if (loading.value) return
  if (!on.value) return
  loading.value = true
  try {
    const enabled = await ImageService.Enabled()
    if (!enabled) {
      // 图源还没下发（启动后要先拉一次客户端配置），过一会再试
      clearTimeout(retry)
      retry = setTimeout(() => next(), 5000)
      return
    }
    const got = await ImageService.Next()
    if (got) {
      img.value = got
      error.value = ''
    }
  } catch (e) {
    const err = toAppError(e)
    if (err.code === 'image_busy') return
    error.value = codeText(err.code, err.details, err.message)
    if (manual) message.error(describeError(err))
  } finally {
    loading.value = false
  }
}

function start() {
  stop()
  if (!on.value) return
  ImageService.Current()
    .then((cur) => {
      if (cur) img.value = cur
      else next()
    })
    .catch(() => next())
  timer = setInterval(() => {
    if (!document.hidden) next()
  }, REFRESH_MS)
}

function stop() {
  clearInterval(timer)
  clearTimeout(retry)
}

onMounted(start)
onBeforeUnmount(stop)
watch(on, (v) => (v ? start() : (stop(), (img.value = null))))

const options = computed(() => [
  { label: '换一张', key: 'next' },
  { label: '复制', key: 'copy', disabled: !img.value },
  { label: '另存为…', key: 'save', disabled: !img.value },
  { label: '在浏览器打开原图', key: 'source', disabled: !img.value?.source },
])

async function onMenu(key: string) {
  menu.value.show = false
  const cur = img.value
  try {
    if (key === 'next') await next(true)
    else if (key === 'copy' && cur) {
      await ImageService.CopyToClipboard(cur.id)
      message.success('图片已复制')
    } else if (key === 'save' && cur) {
      const path = await ImageService.SaveAs(cur.id)
      if (path) message.success(`已保存到 ${path}`)
    } else if (key === 'source' && cur?.source) await AppService.OpenURL(cur.source)
  } catch (e) {
    message.error(describeError(toAppError(e)))
  }
}

async function contextMenu(e: MouseEvent) {
  e.preventDefault()
  menu.value.show = false
  await nextTick()
  menu.value = { show: true, x: e.clientX, y: e.clientY }
}

function wheel(e: WheelEvent) {
  e.preventDefault()
  zoom.value = Math.min(5, Math.max(0.2, zoom.value * (e.deltaY < 0 ? 1.15 : 1 / 1.15)))
}
</script>

<template>
  <div v-if="on" class="image-panel" data-testid="image-panel" @contextmenu="contextMenu">
    <img
      v-if="img"
      :src="img.url"
      class="thumb"
      alt=""
      title="点击查看大图，右键更多操作"
      data-testid="image-panel-img"
      :data-id="img.id"
      @click="
        zoom = 1;
        viewer = true
      "
    />
    <div v-else class="placeholder" @click="next(true)">
      <n-spin v-if="loading" size="small" />
      <n-text v-else depth="3" style="font-size: 12px">{{ error || '图片加载中…' }}</n-text>
    </div>
    <n-dropdown placement="bottom-start" trigger="manual" :x="menu.x" :y="menu.y" :options="options" :show="menu.show" @clickoutside="menu.show = false" @select="onMenu" />
    <n-modal v-model:show="viewer" :mask-closable="true">
      <div class="viewer" @wheel="wheel" @contextmenu="contextMenu" @click.self="viewer = false">
        <img v-if="img" :src="img.url" :style="{ transform: `scale(${zoom})` }" alt="" />
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.image-panel {
  margin: 8px 10px 4px;
}
.thumb {
  width: 100%;
  max-height: 180px;
  object-fit: cover;
  border-radius: 8px;
  cursor: zoom-in;
  display: block;
}
.placeholder {
  height: 90px;
  border-radius: 8px;
  border: 1px dashed rgba(128, 128, 128, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 6px;
  cursor: pointer;
}
.viewer {
  width: 90vw;
  height: 88vh;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
.viewer img {
  max-width: 100%;
  max-height: 100%;
  transition: transform 0.1s;
}
</style>
