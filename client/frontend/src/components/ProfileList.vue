<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from '../ui/Icon.vue'
import Menu, { type MenuItem } from '../ui/Menu.vue'
import Modal from '../ui/Modal.vue'
import Check from '../ui/Check.vue'
import { dialog, message } from '../ui/feedback'
import type { IconName } from '../ui/icons'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { useAccountStore } from '../stores/account'
import { codeText, describeError, toAppError } from '../errors'
import { profileSyncLabels, shareKindLabels } from '../labels'
import SubscribeModal from './SubscribeModal.vue'
import type { ProfileSummary } from '../api'

const emit = defineEmits<{ picked: [] }>()
const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const account = useAccountStore()

// 云端配置和本地配置分两组显示（PLAN 6.6：两者并存，界面上要区分）
const cloudList = computed(() => profiles.list.filter((p) => p.origin === 'cloud'))
const localList = computed(() => profiles.list.filter((p) => p.origin === 'local'))
const subList = computed(() => profiles.list.filter((p) => p.origin === 'subscription'))
const subscribeShow = ref(false)
const collapsed = ref<Record<string, boolean>>({})
const groups = computed(() => {
  const g: { key: string; title: string; icon: IconName; items: ProfileSummary[]; hint: string }[] = []
  if (account.loggedIn || cloudList.value.length) {
    let hint = ''
    const owner = account.account.cacheOwner
    if (!account.loggedIn) hint = `未登录：这些是${owner ? ` ${owner} 的` : ''}云端配置缓存，可以照常启动，登录后继续同步`
    else if (!cloudList.value.length) hint = '还没有云端配置'
    g.push({ key: 'cloud', title: '云端配置', icon: 'cloud', items: cloudList.value, hint })
  }
  g.push({ key: 'local', title: '本地配置', icon: 'laptop', items: localList.value, hint: localList.value.length ? '' : '只保存在本机、不同步。点右上角的 + 新建' })
  if (account.loggedIn || subList.value.length) {
    let hint = ''
    if (!account.loggedIn) hint = '未登录：订阅的内容可以照常启动，登录后继续同步'
    else if (!subList.value.length) hint = '还没有订阅。输入别人给你的分享 ID 即可订阅'
    g.push({ key: 'subscription', title: '订阅', icon: 'link', items: subList.value, hint })
  }
  return g
})

type Mode = 'create' | 'rename' | 'duplicate'
const modal = ref<{ show: boolean; mode: Mode; id: string; name: string; error: string; origin: 'local' | 'cloud'; busy: boolean }>({
  show: false,
  mode: 'create',
  id: '',
  name: '',
  error: '',
  origin: 'local',
  busy: false,
})
const modalTitle = computed(() => ({ create: '新建配置', rename: '重命名配置', duplicate: '复制为本地配置' })[modal.value.mode])
const modalIcon = computed<IconName>(() => ({ create: 'plus', rename: 'edit', duplicate: 'copy' } as const)[modal.value.mode])

function open(mode: Mode, id = '', name = '') {
  modal.value = { show: true, mode, id, name, error: '', origin: account.loggedIn ? 'cloud' : 'local', busy: false }
}

async function submit() {
  const m = modal.value
  if (m.busy) return
  m.busy = true
  try {
    if (m.mode === 'create') await profiles.create(m.name, account.loggedIn ? m.origin : 'local')
    else if (m.mode === 'duplicate') await profiles.duplicate(m.id, m.name)
    else {
      if (profiles.currentId !== m.id) await profiles.select(m.id)
      await profiles.rename(m.name)
    }
    m.show = false
  } catch (e) {
    m.error = describeError(e)
  } finally {
    m.busy = false
  }
}

function menuItems(p: ProfileSummary): MenuItem[] {
  if (p.origin === 'subscription') return [{ key: 'delete', label: '取消订阅', icon: 'trash', danger: true }]
  const opts: MenuItem[] = [{ key: 'rename', label: '重命名', icon: 'edit' }]
  if (p.origin !== 'cloud' && account.loggedIn) opts.push({ key: 'upload', label: '上传到云端', icon: 'upload' })
  opts.push({ key: 'duplicate', label: p.origin === 'cloud' ? '复制为本地配置' : '复制', icon: 'copy' })
  opts.push({ key: 'd', divider: true })
  opts.push({ key: 'delete', label: '删除', icon: 'trash', danger: true })
  return opts
}

async function upload(id: string, name: string) {
  try {
    await profiles.upload(id)
    message.success(`“${name}”已转为云端配置，正在上传`)
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
    const sub = p.origin === 'subscription'
    dialog.warning({
      title: sub ? '取消订阅' : '删除配置',
      content: sub
        ? `确定取消订阅“${name}”吗？正在运行的话会先停止。`
        : p.origin === 'cloud'
          ? `确定删除云端配置“${name}”吗？\n其他设备上的这份配置也会被删除。`
          : `确定删除“${name}”吗？删除后无法恢复。`,
      icon: 'trash',
      danger: true,
      positiveText: sub ? '取消订阅' : '删除',
      negativeText: sub ? '再想想' : '取消',
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
  emit('picked')
  try {
    await profiles.select(id)
  } catch (e) {
    message.error(describeError(e))
  }
}

function meta(p: ProfileSummary) {
  if (p.origin === 'subscription') return `来自 ${p.ownerNickname} · 节点 ${p.nodeCount} · ${p.shareKind === 'p2p' ? `访客 ${p.visitorCount}` : `代理 ${p.proxyCount}`}`
  return `节点 ${p.nodeCount} · 代理 ${p.proxyCount} · 访客 ${p.visitorCount}`
}
const syncTone: Record<string, string> = { pending: 'info', conflict: 'bad', error: 'bad' }
</script>

<template>
  <aside class="profile-list" aria-label="配置列表">
    <div class="pl-head">
      <h2>配置</h2>
      <button type="button" class="add-btn" title="新建配置" aria-label="新建配置" data-testid="profile-new" @click="open('create', '', '新配置')">
        <Icon name="plus" />
      </button>
    </div>
    <div class="pl-body">
      <template v-for="g in groups" :key="g.key">
        <div class="grp" :data-testid="`profile-group-${g.key}`">
          <button type="button" class="grp-head" :class="{ closed: collapsed[g.key] }" :aria-expanded="!collapsed[g.key]" @click="collapsed[g.key] = !collapsed[g.key]">
            <Icon :name="g.icon" size="xs" />
            <span>{{ g.title }}</span>
            <span class="grp-n">{{ g.items.length }}</span>
            <Icon name="chev" size="xs" class="grp-chev" />
          </button>
          <button v-if="g.key === 'subscription' && account.loggedIn" type="button" class="grp-add" data-testid="subscription-add" @click="subscribeShow = true">
            <Icon name="plus" size="xs" />添加订阅
          </button>
        </div>
        <Transition name="collapse">
          <div v-show="!collapsed[g.key]" class="grp-items">
            <p v-if="g.hint" class="grp-hint" :data-testid="`profile-group-hint-${g.key}`">{{ g.hint }}</p>
            <TransitionGroup name="pitem">
              <div
                v-for="p in g.items"
                :key="p.id"
                :class="['pitem', { active: p.id === profiles.currentId }]"
                :data-testid="`profile-item-${p.name}`"
                :data-origin="p.origin"
                :data-sync="p.sync"
              >
                <button type="button" class="pitem-main" :aria-current="p.id === profiles.currentId ? 'true' : undefined" @click="pick(p.id)">
                  <span class="pitem-row">
                    <span class="pitem-name" :title="p.name">{{ p.name }}</span>
                    <span v-if="p.id === tunnel.runningProfileId" class="live-dot" title="正在运行" />
                    <span class="grow" />
                    <span v-if="p.origin === 'subscription'" class="bdg gold" :data-testid="`profile-kind-${p.name}`">{{ shareKindLabels[p.shareKind ?? ''] ?? p.shareKind }}</span>
                    <span v-if="p.revoked" class="bdg bad" :data-testid="`profile-revoked-${p.name}`">已撤销</span>
                    <span v-else-if="p.expired" class="bdg bad" :data-testid="`profile-expired-${p.name}`">已过期</span>
                    <span
                      v-if="p.origin === 'cloud' && p.sync !== 'synced'"
                      :class="['bdg', syncTone[p.sync] ?? '']"
                      :title="p.syncError ? codeText(p.syncError.code, p.syncError.details, p.syncError.message) : undefined"
                      :data-testid="`profile-sync-${p.name}`"
                    >{{ profileSyncLabels[p.sync] ?? p.sync }}</span>
                  </span>
                  <span class="meta">{{ meta(p) }}</span>
                </button>
                <Menu class="pitem-more-wrap" :items="menuItems(p)" :label="`${p.name} 的操作`" @select="(k: string) => onMenu(k, p)">
                  <template #trigger="{ toggle, open: isOpen }">
                    <button
                      type="button"
                      class="pitem-more"
                      :class="{ open: isOpen }"
                      :aria-label="`${p.name} 的更多操作`"
                      aria-haspopup="menu"
                      :aria-expanded="isOpen"
                      :data-testid="`profile-more-${p.name}`"
                      @click.stop="toggle"
                    >
                      <Icon name="more" style="stroke-width: 3" />
                    </button>
                  </template>
                </Menu>
              </div>
            </TransitionGroup>
          </div>
        </Transition>
      </template>
    </div>

    <Modal v-model:show="modal.show" :title="modalTitle" :icon="modalIcon" :width="420" :mask-closable="!modal.busy">
      <form @submit.prevent="submit">
        <div class="fld">
          <label for="profile-name">配置名称</label>
          <input id="profile-name" v-model="modal.name" class="inp" placeholder="配置名称" maxlength="50" data-testid="profile-name-input" />
        </div>
        <div v-if="modal.mode === 'create' && account.loggedIn" class="origin-pick" role="radiogroup" aria-label="配置类型" data-testid="profile-origin">
          <Check :model-value="modal.origin === 'cloud'" radio class="radio" data-testid="profile-origin-cloud" @update:model-value="modal.origin = 'cloud'">
            <span class="op-t"><b>云端配置</b><small>同步到账号，其他设备也能用</small></span>
          </Check>
          <Check :model-value="modal.origin === 'local'" radio class="radio" data-testid="profile-origin-local" @update:model-value="modal.origin = 'local'">
            <span class="op-t"><b>本地配置</b><small>只在本机，不同步</small></span>
          </Check>
        </div>
        <div v-if="modal.error" class="alert bad" style="margin-top: 12px">{{ modal.error }}</div>
        <button type="submit" hidden />
      </form>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="modal.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="modal.busy" @click="submit">确定</button>
      </template>
    </Modal>
    <SubscribeModal v-model:show="subscribeShow" />
  </aside>
</template>

<style>
.profile-list {
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--panel);
  border-right: 1px solid var(--line-2);
  transition: background-color 0.35s;
}
.pl-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 14px 8px 20px;
}
.pl-head h2 {
  font-size: 15.5px;
  font-weight: 900;
  flex: 1;
}
.add-btn {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 11px;
  background: var(--pri);
  color: #fff;
  box-shadow: 0 6px 14px -6px color-mix(in oklab, var(--pri) 80%, transparent);
  transition: transform 0.3s var(--ease-spring), background-color 0.2s;
}
.add-btn:hover {
  transform: rotate(90deg) scale(1.06);
  background: var(--pri-hover);
}
.pl-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0 10px 14px;
}
.grp {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 12px 4px 4px 6px;
}
.grp-head {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  padding: 3px 4px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 800;
  color: var(--text-3);
}
.grp-head:hover {
  color: var(--text-2);
}
.grp-n {
  min-width: 18px;
  padding: 0 6px;
  border-radius: 99px;
  background: var(--fill);
  font-size: 11px;
  text-align: center;
}
.grp-chev {
  margin-left: 2px;
  transition: transform 0.25s;
}
.grp-head.closed .grp-chev {
  transform: rotate(-90deg);
}
.grp-add {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 3px 8px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 800;
  color: var(--pri-text);
}
.grp-add:hover {
  background: var(--pri-soft);
}
.grp-hint {
  margin: 0 8px 6px 10px;
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-3);
}
.pitem {
  position: relative;
  margin: 2px 0;
  border-radius: 15px;
  transition: background-color 0.2s, box-shadow 0.2s;
}
.pitem:hover {
  background: color-mix(in oklab, var(--pri) 6%, transparent);
}
.pitem.active {
  background: var(--pri-soft);
  box-shadow: inset 0 0 0 1.5px color-mix(in oklab, var(--pri) 28%, transparent);
}
.pitem-main {
  display: block;
  width: 100%;
  padding: 9px 36px 9px 12px;
  border-radius: 15px;
}
.pitem-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.pitem-row .grow {
  flex: 1;
}
.pitem-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13.5px;
  font-weight: 800;
}
.pitem .meta {
  display: block;
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 600;
}
.pitem-more-wrap {
  position: absolute;
  top: 7px;
  right: 5px;
}
.pitem-more {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 9px;
  color: var(--text-2);
  opacity: 0;
  transition: opacity 0.15s, background-color 0.2s;
}
.pitem:hover .pitem-more,
.pitem.active .pitem-more,
.pitem-more:focus-visible,
.pitem-more.open {
  opacity: 1;
}
.pitem-more:hover,
.pitem-more.open {
  background: var(--surface);
}
.live-dot {
  position: relative;
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--ok);
}
.live-dot::after {
  content: '';
  position: absolute;
  inset: -4px;
  border-radius: 50%;
  border: 2px solid var(--ok);
  animation: ring 1.8s ease-out infinite;
}
.pitem-enter-active {
  transition: opacity 0.3s, transform 0.4s var(--ease-out);
}
.pitem-enter-from {
  opacity: 0;
  transform: translateX(-10px);
}
.pitem-leave-active {
  transition: opacity 0.2s;
}
.pitem-leave-to {
  opacity: 0;
}
.origin-pick {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.origin-pick .check {
  align-items: flex-start;
  padding: 10px 12px;
}
.op-t b {
  display: block;
  font-size: 13.5px;
  font-weight: 900;
}
.op-t small {
  display: block;
  margin-top: 2px;
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 600;
  line-height: 1.45;
}
</style>
