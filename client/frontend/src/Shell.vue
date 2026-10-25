<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import TunnelPage from './pages/TunnelPage.vue'
import SettingsPage from './pages/SettingsPage.vue'
import SharesPage from './pages/SharesPage.vue'
import RoomsPage from './pages/RoomsPage.vue'
import AboutPage from './pages/AboutPage.vue'
import AccountArea from './components/AccountArea.vue'
import AuthModal from './components/AuthModal.vue'
import ConflictDialog from './components/ConflictDialog.vue'
import CloseAsk from './components/CloseAsk.vue'
import RoomChat from './components/RoomChat.vue'
import { initVoice } from './voice'
import SyncNotices from './components/SyncNotices.vue'
import ImagePanel from './components/ImagePanel.vue'
import TitleBar from './components/TitleBar.vue'
import Icon from './ui/Icon.vue'
import type { IconName } from './ui/icons'
import { dialog, notify } from './ui/feedback'
import { AppService, Events, VnetService, type UpdateInfo } from './api'
import { useSettingsStore } from './stores/settings'
import { useTunnelStore } from './stores/tunnel'
import { useLogsStore } from './stores/logs'
import { useAccountStore } from './stores/account'
import { useEventsStore } from './stores/events'
import { describeError } from './errors'
import { message } from './ui/feedback'
import mascot from './assets/mascot.png'

type Page = 'tunnel' | 'shares' | 'rooms' | 'settings' | 'about'
const page = ref<Page>('tunnel')
const nav: { key: Page; label: string; icon: IconName }[] = [
  { key: 'tunnel', label: '隧道', icon: 'tunnel' },
  { key: 'shares', label: '分享', icon: 'share' },
  { key: 'rooms', label: '联机', icon: 'users' },
  { key: 'settings', label: '设置', icon: 'settings' },
  { key: 'about', label: '关于', icon: 'star' },
]

const tunnel = useTunnelStore()
const logs = useLogsStore()
const account = useAccountStore()
const settingsStore = useSettingsStore()
const runEvents = useEventsStore()

// 侧栏的主题按钮：跟随系统 → 浅色 → 深色 → 跟随系统
const themes = { system: { label: '跟随系统', icon: 'monitor', next: 'light' }, light: { label: '浅色', icon: 'sun', next: 'dark' }, dark: { label: '深色', icon: 'moon', next: 'system' } } as const
const theme = computed(() => themes[(settingsStore.settings?.theme as keyof typeof themes) ?? 'system'] ?? themes.system)
function cycleTheme() {
  settingsStore.update({ theme: theme.value.next }).catch((e) => message.error(describeError(e)))
}

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
  const n = notify({
    type: u.required ? 'warning' : 'info',
    title: `有新版本 ${u.latest}`,
    content: u.required ? '当前版本已低于最低要求，账号和同步已停止（本地配置仍可启动），请尽快更新。' : u.releaseNotes || '可以在“关于”页查看详情。',
    actions: [
      { label: '查看详情', onClick: () => ((page.value = 'about'), n.destroy()) },
      ...(u.downloadUrl ? [{ label: '下载', primary: true, onClick: () => (AppService.OpenURL(u.downloadUrl).catch(() => {}), n.destroy()) }] : []),
    ],
  })
}
function notifyAnnouncement(text: string) {
  if (!text || seen('moefrp.announcement', text)) return
  notify({ title: '公告', content: text })
}

// 管理员推送的通知（docs/client-live.md）：右上角通知或弹窗，可以带一个打开链接的按钮
type AdminNotice = { id: string; level: string; display: string; title: string; content?: string; linkText?: string; linkUrl?: string; sticky?: boolean; from?: string }
function showAdminNotice(n: AdminNotice) {
  const type = (['info', 'success', 'warning', 'error'].includes(n.level) ? n.level : 'info') as 'info' | 'success' | 'warning' | 'error'
  const content = [n.content, n.from ? `—— 管理员 ${n.from}` : ''].filter(Boolean).join('\n')
  const open = () => n.linkUrl && AppService.OpenURL(n.linkUrl).catch(() => {})
  if (n.display === 'dialog') {
    dialog[type]({
      title: n.title,
      content,
      positiveText: n.linkUrl ? n.linkText || '打开链接' : '知道了',
      negativeText: n.linkUrl ? '知道了' : undefined,
      onPositiveClick: () => {
        open()
      },
    })
    return
  }
  const h = notify({
    type,
    title: n.title,
    content,
    duration: n.sticky ? 0 : 12000,
    actions: n.linkUrl ? [{ label: n.linkText || '打开链接', primary: true, onClick: () => (open(), h.destroy()) }] : [],
  })
}
async function takeNotices() {
  try {
    for (const n of (await AppService.TakeNotices()) as AdminNotice[]) showAdminNotice(n)
  } catch {}
}

onMounted(async () => {
  initVoice()
  VnetService.OpenRoomsOnStart().then((v) => v && (page.value = 'rooms')).catch(() => {}) // "以管理员身份重启"后回到联机页
  runEvents.init()
  await Promise.all([tunnel.init(), logs.init(), account.init(), settingsStore.load()]).catch(() => {})
  Events.On('update:available', (ev: any) => notifyUpdate(ev.data as UpdateInfo))
  Events.On('app:announcement', (ev: any) => notifyAnnouncement(ev.data as string))
  Events.On('app:notice', () => takeNotices())
  takeNotices()
  AppService.UpdateInfo()
    .then((u) => {
      notifyUpdate(u)
      notifyAnnouncement(u.announcement)
    })
    .catch(() => {})
  try {
    const info = await AppService.Startup()
    for (const w of info.warnings ?? []) notify({ type: 'warning', title: '启动时发现问题', content: w })
  } catch {}
  AppService.Ready().catch(() => {})
})
</script>

<template>
  <div class="shell">
    <TitleBar />
    <div class="shell-body">
      <nav class="rail" aria-label="主导航">
        <div class="brand">
          <img class="brand-img" :src="mascot" alt="MoeFrp" />
          <div class="brand-txt"><b>MoeFrp</b><small>萌！FRP 客户端</small></div>
        </div>
        <div class="nav">
          <button
            v-for="n in nav"
            :key="n.key"
            type="button"
            class="nav-btn"
            :class="{ on: page === n.key }"
            :aria-current="page === n.key ? 'page' : undefined"
            :data-testid="`nav-${n.key}`"
            @click="page = n.key"
          >
            <Icon :name="n.icon" />
            <span>{{ n.label }}</span>
            <span v-if="n.key === 'tunnel' && tunnel.active" class="nav-dot" title="隧道运行中" />
          </button>
        </div>
        <div class="rail-fill" />
        <ImagePanel v-if="account.loggedIn" />
        <button type="button" class="theme-btn" :title="`主题：${theme.label}（点击切换）`" :aria-label="`切换主题，当前${theme.label}`" @click="cycleTheme">
          <span class="theme-ic"><Transition name="theme-ic" mode="out-in"><Icon :key="theme.icon" :name="theme.icon" size="s" /></Transition></span>
          <span class="theme-lbl">{{ theme.label }}</span>
        </button>
        <AccountArea />
      </nav>

      <main class="main">
        <TunnelPage v-show="page === 'tunnel'" />
        <SharesPage v-if="page === 'shares'" />
        <RoomsPage v-if="page === 'rooms'" @back="page = 'tunnel'" />
        <SettingsPage v-if="page === 'settings'" />
        <AboutPage v-if="page === 'about'" />
      </main>
    </div>

    <AuthModal />
    <ConflictDialog />
    <CloseAsk />
    <RoomChat />
    <SyncNotices />
  </div>
</template>

<style>
/* 标题栏和侧栏是同一块底色，内容区是嵌在里面的圆角卡片 */
.shell {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--rail);
  transition: background-color 0.35s;
}
.shell-body {
  flex: 1;
  min-height: 0;
  display: flex;
}
.rail {
  position: relative;
  z-index: 5;
  width: 78px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 4px 10px 12px;
}
.brand {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 0 0 12px;
}
.brand-img {
  width: 44px;
  height: 44px;
  border-radius: 15px;
  display: block;
  box-shadow: 0 0 0 2.5px var(--surface), 0 8px 18px -6px color-mix(in oklab, var(--pri) 70%, transparent);
  animation: floaty 5s ease-in-out infinite;
}
.brand-txt {
  display: none;
  flex-direction: column;
  line-height: 1.15;
  min-width: 0;
}
.brand-txt b {
  font-size: 17px;
  font-weight: 900;
}
.brand-txt small {
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 700;
}
.nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.nav-btn {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  width: 100%;
  padding: 9px 2px 7px;
  border-radius: 16px;
  color: var(--text-2);
  font-size: 11.5px;
  font-weight: 800;
  transition: background-color 0.2s, color 0.2s, box-shadow 0.2s;
}
.nav-btn:hover {
  background: color-mix(in oklab, var(--pri) 8%, transparent);
  color: var(--text);
}
.nav-btn.on {
  background: var(--surface);
  color: var(--pri-text);
  box-shadow: var(--shadow-sm);
}
.nav-btn .ic {
  width: 22px;
  height: 22px;
}
.nav-btn.on .ic {
  animation: boing 0.55s var(--ease-spring);
}
.nav-dot {
  position: absolute;
  top: 7px;
  right: 16px;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--ok);
  box-shadow: 0 0 0 2.5px var(--rail);
}
.nav-btn.on .nav-dot {
  box-shadow: 0 0 0 2.5px var(--surface);
}
.rail-fill {
  flex: 1;
}
.theme-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  width: 100%;
  height: 40px;
  border-radius: 14px;
  color: var(--text-2);
  font-weight: 800;
  font-size: 12.5px;
  transition: background-color 0.2s, color 0.2s;
}
.theme-btn:hover {
  background: color-mix(in oklab, var(--pri) 8%, transparent);
  color: var(--text);
}
.theme-ic {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 10px;
  background: var(--surface);
  color: var(--gold-text);
  box-shadow: var(--shadow-sm);
}
.theme-lbl {
  display: none;
}
.theme-ic-enter-active {
  transition: transform 0.45s var(--ease-spring), opacity 0.2s;
}
.theme-ic-leave-active {
  transition: transform 0.15s, opacity 0.15s;
}
.theme-ic-enter-from {
  transform: scale(0.3) rotate(-90deg);
  opacity: 0;
}
.theme-ic-leave-to {
  transform: scale(0.5) rotate(60deg);
  opacity: 0;
}
.main {
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg);
  border: 1px solid var(--line-2);
  border-right: 0;
  border-bottom: 0;
  border-top-left-radius: 16px;
  transition: background-color 0.35s, border-color 0.35s;
}
.main > * {
  flex: 1;
  min-height: 0;
  /* 只做 CSS 入场动画，切走时立即隐藏或卸载，避免旧页面滞留。 */
  animation: pageIn 0.45s var(--ease-out);
}

@media (min-width: 1360px) {
  .rail {
    width: 220px;
    padding: 8px 14px 14px;
  }
  .brand {
    justify-content: flex-start;
    padding: 0 6px 16px;
  }
  .brand-txt {
    display: flex;
  }
  .nav-btn {
    flex-direction: row;
    gap: 12px;
    padding: 10px 14px;
    font-size: 14px;
    border-radius: 14px;
  }
  .nav-btn .ic {
    width: 20px;
    height: 20px;
  }
  .nav-dot {
    position: static;
    margin-left: auto;
    box-shadow: none !important;
  }
  .theme-btn {
    justify-content: flex-start;
    padding: 0 8px;
  }
  .theme-lbl {
    display: block;
  }
}
</style>
