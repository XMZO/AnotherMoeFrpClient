<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Menu from '../ui/Menu.vue'
import Modal from '../ui/Modal.vue'
import { message } from '../ui/feedback'
import { ImageService, type ImageInfo } from '../api'
import { useSettingsStore } from '../stores/settings'
import { codeText, describeError, toAppError } from '../errors'

// 图片面板（PLAN 6.9）：图源由服务端下发，登录后才有（没登录时侧栏不显示这个面板）；图片由 Go 端下载后从本地地址显示。
// 每 60 秒换一张（窗口不可见时不换）；右键：换一张、复制、另存为（随机图接口每次返回的都不一样，所以没有"打开原图"）。
const REFRESH_MS = 60_000

const settings = useSettingsStore()
const img = ref<ImageInfo | null>(null)
const loading = ref(false)
const error = ref('')
const viewer = ref(false)
const zoom = ref(1)
const menuAt = ref<{ x: number; y: number } | null>(null)
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
      // 图源还没拿到（登录后第一次同步时拉取），过一会再试
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

const items = computed(() => [
  { key: 'next', label: '换一张', icon: 'refresh' as const },
  { key: 'copy', label: '复制', icon: 'copy' as const, disabled: !img.value },
  { key: 'save', label: '另存为…', icon: 'save' as const, disabled: !img.value },
])

async function act(key: string) {
  menuAt.value = null
  const cur = img.value
  try {
    if (key === 'next') await next(true)
    else if (key === 'copy' && cur) {
      await ImageService.CopyToClipboard(cur.id)
      message.success('图片已复制')
    } else if (key === 'save' && cur) {
      const path = await ImageService.SaveAs(cur.id)
      if (path) message.success(`已保存到 ${path}`)
    }
  } catch (e) {
    message.error(describeError(toAppError(e)))
  }
}

function contextMenu(e: MouseEvent) {
  e.preventDefault()
  menuAt.value = { x: e.clientX, y: e.clientY }
}

function openViewer() {
  zoom.value = 1
  viewer.value = true
}
function wheel(e: WheelEvent) {
  zoom.value = Math.min(5, Math.max(0.2, zoom.value * (e.deltaY < 0 ? 1.15 : 1 / 1.15)))
}
</script>

<template>
  <div v-if="on" class="image-panel" data-testid="image-panel" @contextmenu="contextMenu">
    <Transition name="img-swap" mode="out-in">
      <button v-if="img" :key="img.id" type="button" class="img-card" title="点击查看大图，右键更多操作" @click="openViewer">
        <img :src="img.url" alt="图片面板" data-testid="image-panel-img" :data-id="img.id" />
        <span class="img-cap">点击放大 · 右键更多</span>
      </button>
      <button v-else type="button" class="img-card placeholder" :title="error || '点击重试'" @click="next(true)">
        <Icon v-if="loading" name="refresh" class="spin" />
        <Icon v-else :name="error ? 'alert' : 'image'" />
        <span class="img-ph-text">{{ error || '图片加载中…' }}</span>
      </button>
    </Transition>
    <Menu :items="items" :at="menuAt" label="图片操作" @select="act" @close="menuAt = null" />

    <Modal v-model:show="viewer" :width="760" class="img-viewer" title="查看图片">
      <div class="viewer-stage" @wheel.prevent="wheel" @contextmenu="contextMenu">
        <img v-if="img" :src="img.url" :style="{ transform: `scale(${zoom})` }" alt="图片面板的大图" />
      </div>
      <template #footer>
        <span class="grow fld-help">滚轮缩放 · 右键也能操作</span>
        <button type="button" class="btn quiet xs" @click="act('next')"><Icon name="refresh" size="xs" />换一张</button>
        <button type="button" class="btn quiet xs" :disabled="!img" @click="act('copy')"><Icon name="copy" size="xs" />复制</button>
        <button type="button" class="btn quiet xs" :disabled="!img" @click="act('save')"><Icon name="save" size="xs" />另存为…</button>
      </template>
    </Modal>
  </div>
</template>

<style>
.image-panel {
  display: flex;
}
.img-card {
  position: relative;
  display: grid;
  place-items: center;
  width: 100%;
  aspect-ratio: 1 / 1;
  border-radius: 16px;
  overflow: hidden;
  background: var(--fill);
  color: var(--text-3);
  box-shadow: var(--shadow-sm);
  transition: transform 0.25s var(--ease-spring);
}
.img-card:hover {
  transform: translateY(-2px) rotate(-1deg);
}
.img-card img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.img-cap {
  display: none;
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 16px 10px 7px;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
  background: linear-gradient(transparent, rgba(20, 10, 40, 0.6));
  text-align: left;
}
.img-card.placeholder {
  border: 1.5px dashed var(--line);
  box-shadow: none;
}
.img-ph-text {
  display: none;
  padding: 0 10px;
  font-size: 11.5px;
  font-weight: 700;
  text-align: center;
}
.img-swap-enter-active,
.img-swap-leave-active {
  transition: opacity 0.35s, transform 0.35s var(--ease-out);
}
.img-swap-enter-from {
  opacity: 0;
  transform: scale(0.94);
}
.img-swap-leave-to {
  opacity: 0;
}
.viewer-stage {
  display: grid;
  place-items: center;
  height: min(62vh, 560px);
  overflow: hidden;
  border-radius: 16px;
  background: var(--fill);
}
/* 铺满舞台再按比例缩进去：横图竖图都完整显示（max-height 的百分比在网格里算不出来，会被裁掉） */
.viewer-stage img {
  width: 100%;
  height: 100%;
  object-fit: contain;
  transition: transform 0.15s;
}
@media (min-width: 1360px) {
  .img-card {
    aspect-ratio: 4 / 3;
  }
  .img-cap,
  .img-ph-text {
    display: block;
  }
}
@media (max-height: 640px) {
  .img-card {
    aspect-ratio: 16 / 10;
  }
}
</style>
