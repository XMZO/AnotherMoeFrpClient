<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import Icon from './Icon.vue'
import type { IconName } from './icons'

// 弹出菜单。两种用法：
//   1. 点按钮弹出：<Menu :items><template #trigger="{ toggle, open }">…</template></Menu>
//   2. 右键菜单：<Menu :items :at="{ x, y }" @close="at = null" />
// 菜单 Teleport 到 body，用 fixed 定位，不会被滚动容器裁掉。菜单项是 .menu-item（e2e 用它）。
export interface MenuItem {
  key: string
  label?: string
  icon?: IconName
  danger?: boolean
  disabled?: boolean
  divider?: boolean
}
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    items?: MenuItem[]
    placement?: 'bottom-end' | 'bottom-start' | 'right-end' | 'top-start'
    at?: { x: number; y: number } | null
    width?: number
    label?: string
  }>(),
  { items: () => [], placement: 'bottom-end', at: null },
)
const emit = defineEmits<{ select: [string]; close: []; open: [] }>()

const open = ref(false)
const anchor = ref<HTMLElement | null>(null)
const menuEl = ref<HTMLElement | null>(null)
const pos = ref<Record<string, string>>({ left: '-9999px', top: '0px' })
const visible = computed(() => open.value || !!props.at)

function toggle() {
  open.value = !open.value
}
function close() {
  if (!visible.value) return
  open.value = false
  emit('close')
}
function pick(it: MenuItem) {
  if (it.disabled) return
  close()
  emit('select', it.key)
}

async function place() {
  await nextTick()
  const m = menuEl.value
  if (!m) return
  const mw = m.offsetWidth
  const mh = m.offsetHeight
  const vw = window.innerWidth
  const vh = window.innerHeight
  let x = 0
  let y = 0
  let origin = 'top left'
  if (props.at) {
    x = props.at.x
    y = props.at.y
    if (y + mh > vh - 8) {
      y = y - mh
      origin = 'bottom left'
    }
  } else if (anchor.value) {
    const r = anchor.value.getBoundingClientRect()
    if (props.placement === 'right-end') {
      x = r.right + 10
      y = r.bottom - mh
      origin = 'bottom left'
    } else if (props.placement === 'top-start') {
      x = r.left
      y = r.top - mh - 6
      origin = 'bottom left'
    } else {
      x = props.placement === 'bottom-start' ? r.left : r.right - mw
      y = r.bottom + 6
      origin = props.placement === 'bottom-start' ? 'top left' : 'top right'
      if (y + mh > vh - 8 && r.top - mh - 6 > 8) {
        y = r.top - mh - 6
        origin = origin.replace('top', 'bottom')
      }
    }
  }
  x = Math.min(Math.max(8, x), vw - mw - 8)
  y = Math.min(Math.max(8, y), vh - mh - 8)
  pos.value = { left: `${x}px`, top: `${y}px`, transformOrigin: origin }
}

function onDocClick(e: MouseEvent) {
  const t = e.target as Node
  if (menuEl.value?.contains(t) || anchor.value?.contains(t)) return
  close()
}
function onDocContext(e: MouseEvent) {
  if (props.at && !menuEl.value?.contains(e.target as Node)) close()
}
function onWindowChange() {
  close()
}

function listen(on: boolean) {
  if (on) {
    document.addEventListener('click', onDocClick, true)
    document.addEventListener('contextmenu', onDocContext, true)
    window.addEventListener('resize', onWindowChange)
    window.addEventListener('blur', onWindowChange)
  } else {
    document.removeEventListener('click', onDocClick, true)
    document.removeEventListener('contextmenu', onDocContext, true)
    window.removeEventListener('resize', onWindowChange)
    window.removeEventListener('blur', onWindowChange)
  }
}

watch(
  () => [visible.value, props.at?.x, props.at?.y] as const,
  ([v], old) => {
    if (v) {
      if (!old?.[0]) {
        // 打开时注册：放到下一轮，避免打开菜单的那次点击立刻把它关掉
        setTimeout(() => visible.value && listen(true))
        emit('open')
      }
      place()
    } else listen(false)
  },
)
onBeforeUnmount(() => listen(false))

defineExpose({ close, open: () => (open.value = true) })
</script>

<template>
  <span ref="anchor" class="menu-anchor" v-bind="$attrs"><slot name="trigger" :toggle="toggle" :open="visible" /></span>
  <Teleport to="body">
    <Transition name="menu">
      <div
        v-if="visible"
        ref="menuEl"
        class="menu"
        role="menu"
        :aria-label="label"
        :style="{ ...pos, minWidth: width ? `${width}px` : undefined }"
        @keydown.esc.stop="close"
      >
        <slot :close="close">
          <template v-for="it in items" :key="it.key">
            <div v-if="it.divider" class="menu-sep" />
            <button v-else type="button" role="menuitem" class="menu-item" :class="{ danger: it.danger }" :disabled="it.disabled" @click="pick(it)">
              <Icon v-if="it.icon" :name="it.icon" size="s" />{{ it.label }}
            </button>
          </template>
        </slot>
      </div>
    </Transition>
  </Teleport>
</template>

<style>
.menu-anchor {
  display: inline-flex;
}
</style>
