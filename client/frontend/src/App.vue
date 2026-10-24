<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import Shell from './Shell.vue'
import UiHost from './ui/UiHost.vue'
import { useSettingsStore } from './stores/settings'

const settings = useSettingsStore()

// 主题：跟随系统 / 浅色 / 深色（PLAN 6.7），默认跟随系统。颜色变量挂在 <html data-theme> 上。
const mq = window.matchMedia('(prefers-color-scheme: dark)')
const sysDark = ref(mq.matches)
const onChange = (e: MediaQueryListEvent) => (sysDark.value = e.matches)
const dark = computed(() => {
  const t = settings.settings?.theme ?? 'system'
  return t === 'dark' || (t === 'system' && sysDark.value)
})
watchEffect(() => {
  document.documentElement.dataset.theme = dark.value ? 'dark' : 'light'
})

onMounted(() => {
  mq.addEventListener('change', onChange)
  settings.load().catch(() => {})
})
onBeforeUnmount(() => mq.removeEventListener('change', onChange))
</script>

<template>
  <Shell />
  <UiHost />
</template>
