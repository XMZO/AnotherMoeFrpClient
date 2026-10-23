<script setup lang="ts">
import { computed, onMounted } from 'vue'
import {
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NMessageProvider,
  NNotificationProvider,
  darkTheme,
  dateZhCN,
  useOsTheme,
  zhCN,
} from 'naive-ui'
import Shell from './Shell.vue'
import { useSettingsStore } from './stores/settings'

const settings = useSettingsStore()
const osTheme = useOsTheme()

// 主题：跟随系统 / 浅色 / 深色（PLAN 6.7），默认跟随系统
const theme = computed(() => {
  const t = settings.settings?.theme ?? 'system'
  const dark = t === 'dark' || (t === 'system' && osTheme.value === 'dark')
  return dark ? darkTheme : null
})

onMounted(() => {
  settings.load().catch(() => {})
})
</script>

<template>
  <n-config-provider :theme="theme" :locale="zhCN" :date-locale="dateZhCN" style="height: 100%">
    <n-global-style />
    <n-message-provider :max="3">
      <n-notification-provider>
        <n-dialog-provider>
          <Shell :dark="theme !== null" />
        </n-dialog-provider>
      </n-notification-provider>
    </n-message-provider>
  </n-config-provider>
</template>
