<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NDropdown, NInput, NModal, NRadio, NRadioGroup, NScrollbar, NSpace, NText, NTooltip, useDialog, useMessage } from 'naive-ui'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { useAccountStore } from '../stores/account'
import { codeText, describeError, toAppError } from '../errors'
import { profileSyncLabels, shareKindLabels } from '../labels'
import SubscribeModal from './SubscribeModal.vue'
import type { ProfileSummary } from '../api'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const account = useAccountStore()
const message = useMessage()
const dialog = useDialog()

// 云端配置和本地配置分两组显示（PLAN 6.6：两者并存，界面上要区分）
const cloudList = computed(() => profiles.list.filter((p) => p.origin === 'cloud'))
const localList = computed(() => profiles.list.filter((p) => p.origin === 'local'))
const subList = computed(() => profiles.list.filter((p) => p.origin === 'subscription'))
const subscribeShow = ref(false)
const groups = computed(() => {
  const g: { key: string; title: string; items: ProfileSummary[]; hint: string }[] = []
  if (account.loggedIn || cloudList.value.length) {
    let hint = ''
    const owner = account.account.cacheOwner
    if (!account.loggedIn) hint = `未登录：这些是${owner ? ` ${owner} 的` : ''}云端配置缓存，可以照常启动，登录后继续同步`
    else if (!cloudList.value.length) hint = '还没有云端配置'
    g.push({ key: 'cloud', title: '云端配置', items: cloudList.value, hint })
  }
  g.push({ key: 'local', title: '本地配置', items: localList.value, hint: localList.value.length ? '' : '只保存在本机、不同步。点"新建"开始' })
  if (account.loggedIn || subList.value.length) {
    let hint = ''
    if (!account.loggedIn) hint = '未登录：订阅的内容可以照常启动，登录后继续同步'
    else if (!subList.value.length) hint = '还没有订阅。输入别人给你的分享 ID 即可订阅'
    g.push({ key: 'subscription', title: '订阅', items: subList.value, hint })
  }
  return g
})

type Mode = 'create' | 'rename' | 'duplicate'
const modal = ref<{ show: boolean; mode: Mode; id: string; name: string; error: string; origin: 'local' | 'cloud' }>({
  show: false,
  mode: 'create',
  id: '',
  name: '',
  error: '',
  origin: 'local',
})
const modalTitle = computed(() => ({ create: '新建配置', rename: '重命名配置', duplicate: '复制为本地配置' })[modal.value.mode])

function open(mode: Mode, id = '', name = '') {
  modal.value = { show: true, mode, id, name, error: '', origin: account.loggedIn ? 'cloud' : 'local' }
}

async function submit() {
  const m = modal.value
  try {
    if (m.mode === 'create') await profiles.create(m.name, account.loggedIn ? m.origin : 'local')
    else if (m.mode === 'duplicate') await profiles.duplicate(m.id, m.name)
    else {
      if (profiles.currentId !== m.id) await profiles.select(m.id)
      await profiles.rename(m.name)
    }
    modal.value.show = false
  } catch (e) {
    modal.value.error = describeError(e)
  }
  return false
}

function menuOptions(p: ProfileSummary) {
  if (p.origin === 'subscription') return [{ label: '取消订阅', key: 'delete' }]
  const opts: any[] = [{ label: '重命名', key: 'rename' }]
  if (p.origin !== 'cloud' && account.loggedIn) opts.push({ label: '上传到云端', key: 'upload' })
  opts.push({ label: p.origin === 'cloud' ? '复制为本地配置' : '复制', key: 'duplicate' })
  opts.push({ label: '删除', key: 'delete' })
  return opts
}

async function upload(id: string, name: string) {
  try {
    await profiles.upload(id)
    message.success(`"${name}"已转为云端配置，正在上传`)
  } catch (e) {
    message.error(describeError(e))
  }
}

function onMenu(key: string, p: ProfileSummary) {
  const { id, name } = p
  if (key === 'rename') open('rename', id, name)
  else if (key === 'upload') upload(id, name)
  else if (key === 'duplicate') open('duplicate', id, `${name} 副本`)
  else if (key === 'delete') {
    dialog.warning({
      title: '删除配置',
      content:
        p.origin === 'subscription'
          ? `确定取消订阅"${name}"吗？正在运行的话会先停止。`
          : p.origin === 'cloud'
            ? `确定删除云端配置"${name}"吗？其他设备上的这份配置也会被删除。`
            : `确定删除"${name}"吗？删除后无法恢复。`,
      positiveText: p.origin === 'subscription' ? '取消订阅' : '删除',
      negativeText: '取消',
      onPositiveClick: async () => {
        try {
          await profiles.remove(id)
        } catch (e) {
          message.error(describeError(toAppError(e)))
        }
      },
    })
  }
}

async function pick(id: string) {
  try {
    await profiles.select(id)
  } catch (e) {
    message.error(describeError(e))
  }
}
</script>

<template>
  <div class="profile-list">
    <div class="head">
      <n-text strong>配置</n-text>
      <n-button size="small" type="primary" secondary data-testid="profile-new" @click="open('create', '', '新配置')">新建</n-button>
    </div>
    <n-scrollbar class="body">
      <template v-for="g in groups" :key="g.key">
        <div class="group" :data-testid="`profile-group-${g.key}`">
          <svg v-if="g.key === 'subscription'" class="gicon" viewBox="0 0 24 24"><path fill="currentColor" d="M3.9 12c0-1.71 1.39-3.1 3.1-3.1h4V7H7c-2.76 0-5 2.24-5 5s2.24 5 5 5h4v-1.9H7c-1.71 0-3.1-1.39-3.1-3.1M8 13h8v-2H8zm9-6h-4v1.9h4c1.71 0 3.1 1.39 3.1 3.1s-1.39 3.1-3.1 3.1h-4V17h4c2.76 0 5-2.24 5-5s-2.24-5-5-5" /></svg>
          <svg v-else-if="g.key === 'cloud'" class="gicon" viewBox="0 0 24 24"><path fill="currentColor" d="M19.35 10.04A7.49 7.49 0 0 0 12 4C9.11 4 6.6 5.64 5.35 8.04A5.994 5.994 0 0 0 0 14c0 3.31 2.69 6 6 6h13c2.76 0 5-2.24 5-5c0-2.64-2.05-4.78-4.65-4.96" /></svg>
          <svg v-else class="gicon" viewBox="0 0 24 24"><path fill="currentColor" d="M20 18c1.1 0 1.99-.9 1.99-2L22 6c0-1.1-.9-2-2-2H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2H0v2h24v-2zM4 6h16v10H4z" /></svg>
          <span>{{ g.title }}</span>
          <n-button
            v-if="g.key === 'subscription' && account.loggedIn"
            text
            size="tiny"
            class="gadd"
            data-testid="subscription-add"
            @click="subscribeShow = true"
          >+ 添加订阅</n-button>
        </div>
        <div v-if="g.hint" class="ghint" :data-testid="`profile-group-hint-${g.key}`">{{ g.hint }}</div>
        <div
          v-for="p in g.items"
          :key="p.id"
          :class="['item', { active: p.id === profiles.currentId }]"
          :data-testid="`profile-item-${p.name}`"
          :data-origin="p.origin"
          :data-sync="p.sync"
          @click="pick(p.id)"
        >
          <div class="row">
            <span class="name" :title="p.name">{{ p.name }}</span>
            <span v-if="p.id === tunnel.runningProfileId" class="dot" title="正在运行" />
            <span v-if="p.origin === 'subscription'" class="badge kind" :data-testid="`profile-kind-${p.name}`">{{ shareKindLabels[p.shareKind ?? ''] ?? p.shareKind }}</span>
            <span v-if="p.revoked" class="badge error" :data-testid="`profile-revoked-${p.name}`">已撤销</span>
            <span v-else-if="p.expired" class="badge error" :data-testid="`profile-expired-${p.name}`">已过期</span>
            <n-tooltip v-if="p.origin === 'cloud' && p.sync !== 'synced'" placement="right" :disabled="!p.syncError">
              <template #trigger>
                <span :class="['badge', p.sync]" :data-testid="`profile-sync-${p.name}`">{{ profileSyncLabels[p.sync] ?? p.sync }}</span>
              </template>
              {{ p.syncError ? codeText(p.syncError.code, p.syncError.details, p.syncError.message) : '' }}
            </n-tooltip>
            <n-dropdown trigger="click" :options="menuOptions(p)" @select="(k: string) => onMenu(k, p)">
              <n-button text size="tiny" class="more" :data-testid="`profile-more-${p.name}`" @click.stop>···</n-button>
            </n-dropdown>
          </div>
          <div v-if="p.origin === 'subscription'" class="meta">来自 {{ p.ownerNickname }} · 节点 {{ p.nodeCount }} · {{ p.shareKind === 'p2p' ? `访客 ${p.visitorCount}` : `代理 ${p.proxyCount}` }}</div>
          <div v-else class="meta">节点 {{ p.nodeCount }} · 代理 {{ p.proxyCount }} · 访客 {{ p.visitorCount }}</div>
        </div>
      </template>
    </n-scrollbar>

    <n-modal
      v-model:show="modal.show"
      preset="dialog"
      :title="modalTitle"
      positive-text="确定"
      negative-text="取消"
      :on-positive-click="submit"
    >
      <n-input
        v-model:value="modal.name"
        placeholder="配置名称"
        maxlength="50"
        data-testid="profile-name-input"
        @keyup.enter="submit"
      />
      <n-radio-group
        v-if="modal.mode === 'create' && account.loggedIn"
        v-model:value="modal.origin"
        style="margin-top: 10px"
        data-testid="profile-origin"
      >
        <n-space>
          <n-radio value="cloud" data-testid="profile-origin-cloud">云端配置（同步到账号）</n-radio>
          <n-radio value="local" data-testid="profile-origin-local">本地配置（只在本机）</n-radio>
        </n-space>
      </n-radio-group>
      <n-text v-if="modal.error" type="error" depth="1" style="display: block; margin-top: 8px">{{ modal.error }}</n-text>
    </n-modal>
    <SubscribeModal v-model:show="subscribeShow" />
  </div>
</template>

<style scoped>
.profile-list {
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.2));
  border-right-color: rgba(128, 128, 128, 0.2);
  min-height: 0;
}
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 12px 8px;
}
.body {
  flex: 1;
  min-height: 0;
}
.item {
  margin: 2px 8px;
  padding: 8px 10px;
  border-radius: 6px;
  cursor: pointer;
}
.item:hover {
  background: rgba(128, 128, 128, 0.1);
}
.item.active {
  background: rgba(24, 160, 88, 0.14);
}
.row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #18a058;
}
.meta {
  font-size: 12px;
  opacity: 0.6;
  margin-top: 2px;
}
.more {
  opacity: 0.6;
}
.group {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  opacity: 0.7;
  padding: 10px 14px 4px;
}
.gicon {
  width: 14px;
  height: 14px;
}
.gadd {
  margin-left: auto;
  font-size: 12px;
}
.badge.kind {
  background: rgba(24, 160, 88, 0.15);
  color: #18a058;
}
.ghint {
  font-size: 12px;
  opacity: 0.55;
  padding: 0 14px 4px;
}
.badge {
  font-size: 11px;
  padding: 0 5px;
  border-radius: 8px;
  line-height: 16px;
  background: rgba(128, 128, 128, 0.15);
  white-space: nowrap;
}
.badge.pending {
  background: rgba(32, 128, 240, 0.15);
  color: #2080f0;
}
.badge.conflict,
.badge.error {
  background: rgba(208, 48, 80, 0.15);
  color: #d03050;
}
</style>
