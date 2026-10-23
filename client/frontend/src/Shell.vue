<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { NButton, NLayout, NLayoutSider, NMenu, useNotification, type MenuOption } from 'naive-ui'
import TunnelPage from './pages/TunnelPage.vue'
import SettingsPage from './pages/SettingsPage.vue'
import SharesPage from './pages/SharesPage.vue'
import AboutPage from './pages/AboutPage.vue'
import AccountArea from './components/AccountArea.vue'
import AuthModal from './components/AuthModal.vue'
import ConflictDialog from './components/ConflictDialog.vue'
import SyncNotices from './components/SyncNotices.vue'
import ImagePanel from './components/ImagePanel.vue'
import { AppService, Events, type UpdateInfo } from './api'
import { useSettingsStore } from './stores/settings'
import { useTunnelStore } from './stores/tunnel'
import { useLogsStore } from './stores/logs'
import { useAccountStore } from './stores/account'

defineProps<{ dark: boolean }>()

const page = ref<'tunnel' | 'shares' | 'settings' | 'about'>('tunnel')
const menu: MenuOption[] = [
  { key: 'tunnel', label: () => h('span', { 'data-testid': 'nav-tunnel' }, '隧道') },
  { key: 'shares', label: () => h('span', { 'data-testid': 'nav-shares' }, '分享') },
  { key: 'settings', label: () => h('span', { 'data-testid': 'nav-settings' }, '设置') },
  { key: 'about', label: () => h('span', { 'data-testid': 'nav-about' }, '关于') },
]

const notification = useNotification()
const tunnel = useTunnelStore()
const logs = useLogsStore()
const account = useAccountStore()
const settingsStore = useSettingsStore()

// 新版本、公告（PLAN 6.12）：同一个版本、同一条公告只提示一次（记在 localStorage）
function seen(key: string, value: string): boolean {
  try {
    if (localStorage.getItem(key) === value) return true
    localStorage.setItem(key, value)
  } catch {}
  return false
}
function notifyUpdate(u: UpdateInfo) {
  if (!u.available || seen('moefrp.update.notified', u.latest)) return
  const n = notification.info({
    title: `有新版本 ${u.latest}`,
    content: u.required ? '当前版本已低于最低要求，账号和同步已停止（本地配置仍可启动），请尽快更新。' : u.releaseNotes || '可以在"关于"页查看详情。',
    duration: 0,
    action: () =>
      u.downloadUrl
        ? h(NButton, { size: 'small', type: 'primary', onClick: () => (AppService.OpenURL(u.downloadUrl).catch(() => {}), n.destroy()) }, () => '下载')
        : null,
  })
}
function notifyAnnouncement(text: string) {
  if (!text || seen('moefrp.announcement', text)) return
  notification.info({ title: '公告', content: text, duration: 0 })
}

onMounted(async () => {
  await Promise.all([tunnel.init(), logs.init(), account.init(), settingsStore.load()]).catch(() => {})
  Events.On('update:available', (ev: any) => notifyUpdate(ev.data as UpdateInfo))
  Events.On('app:announcement', (ev: any) => notifyAnnouncement(ev.data as string))
  AppService.UpdateInfo()
    .then((u) => {
      notifyUpdate(u)
      notifyAnnouncement(u.announcement)
    })
    .catch(() => {})
  try {
    const info = await AppService.Startup()
    for (const w of info.warnings ?? []) {
      notification.warning({ title: '启动时发现问题', content: w, duration: 0 })
    }
  } catch {}
  AppService.Ready().catch(() => {})
})
</script>

<template>
  <n-layout has-sider style="height: 100%">
    <n-layout-sider bordered :width="170" content-style="padding-top: 12px; display: flex; flex-direction: column; height: 100%;">
      <div class="brand">MoeFrp</div>
      <n-menu :value="page" :options="menu" @update:value="(k: string) => (page = k as any)" />
      <div style="flex: 1" />
      <ImagePanel />
      <AccountArea />
    </n-layout-sider>
    <n-layout content-style="height: 100%; display: flex; flex-direction: column;">
      <TunnelPage v-show="page === 'tunnel'" />
      <SharesPage v-if="page === 'shares'" />
      <SettingsPage v-if="page === 'settings'" />
      <AboutPage v-if="page === 'about'" />
    </n-layout>
    <AuthModal />
    <ConflictDialog />
    <SyncNotices />
  </n-layout>
</template>

<style scoped>
.brand {
  font-weight: 600;
  font-size: 16px;
  padding: 4px 20px 12px;
  opacity: 0.85;
}
</style>
