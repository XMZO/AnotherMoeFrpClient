<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import Icon from './Icon.vue'
import type { IconName } from './icons'

// 居中弹窗。内容放默认插槽，按钮放 footer 插槽（会带上 .dlg-actions，e2e 用它找按钮）。
defineOptions({ inheritAttrs: false })
const show = defineModel<boolean>('show', { default: false })
const props = withDefaults(
  defineProps<{
    title?: string
    subtitle?: string
    icon?: IconName
    tone?: '' | 'gold' | 'info' | 'ok' | 'warn' | 'bad'
    width?: number
    closable?: boolean
    maskClosable?: boolean
    center?: boolean
  }>(),
  { width: 480, closable: true, maskClosable: true, tone: '' },
)
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement | null>(null)

function close() {
  if (!props.closable) return
  show.value = false
  emit('close')
}
function onMask() {
  if (props.maskClosable) close()
}
watch(show, async (v) => {
  if (!v) return
  await nextTick()
  if (panel.value && !panel.value.contains(document.activeElement)) panel.value.focus()
}, { immediate: true })
</script>

<template>
  <Teleport to="body">
    <Transition name="ov" appear :duration="{ enter: 500, leave: 220 }">
      <div v-if="show" class="ov-wrap" @keydown.esc.stop="close">
        <div class="scrim" @click="onMask" />
        <div
          ref="panel"
          class="modal"
          :class="{ center }"
          role="dialog"
          aria-modal="true"
          :aria-label="title"
          tabindex="-1"
          :style="{ width: `min(${width}px, 100%)` }"
          v-bind="$attrs"
        >
          <header v-if="title && !center" class="ov-head">
            <span v-if="icon" class="tile-ic lg" :class="tone"><Icon :name="icon" /></span>
            <h2>{{ title }}<small v-if="subtitle">{{ subtitle }}</small></h2>
            <button v-if="closable" type="button" class="icon-btn modal-close" aria-label="关闭" @click="close"><Icon name="x" /></button>
          </header>
          <button v-else-if="closable" type="button" class="icon-btn modal-close floating" aria-label="关闭" @click="close"><Icon name="x" /></button>
          <div class="ov-body"><slot /></div>
          <footer v-if="$slots.footer" class="ov-foot dlg-actions"><slot name="footer" /></footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style>
.modal-close.floating {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 1;
}
</style>
