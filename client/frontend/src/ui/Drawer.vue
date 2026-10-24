<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import Icon from './Icon.vue'
import type { IconName } from './icons'

// 右侧抽屉（添加 / 编辑代理、节点、访客）。按钮放 footer 插槽。
defineOptions({ inheritAttrs: false })
const show = defineModel<boolean>('show', { default: false })
defineProps<{ title: string; subtitle?: string; icon?: IconName; tone?: '' | 'gold' | 'info' }>()
const panel = ref<HTMLElement | null>(null)

function close() {
  show.value = false
}
watch(show, async (v) => {
  if (!v) return
  await nextTick()
  panel.value?.focus()
}, { immediate: true })
</script>

<template>
  <Teleport to="body">
    <Transition name="ov" appear :duration="{ enter: 500, leave: 260 }">
      <div v-if="show" class="ov-wrap drawer-wrap" @keydown.esc.stop="close">
        <div class="scrim" @click="close" />
        <aside ref="panel" class="drawer" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1" v-bind="$attrs">
          <header class="ov-head">
            <span v-if="icon" class="tile-ic lg" :class="tone"><Icon :name="icon" /></span>
            <h2>{{ title }}<small v-if="subtitle">{{ subtitle }}</small></h2>
            <button type="button" class="icon-btn" aria-label="关闭" @click="close"><Icon name="x" /></button>
          </header>
          <div class="ov-body"><slot /></div>
          <footer v-if="$slots.footer" class="ov-foot"><slot name="footer" /></footer>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>
