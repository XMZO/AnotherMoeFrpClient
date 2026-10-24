<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Clipboard } from '@wailsio/runtime'
import CreateShareModal from '../components/CreateShareModal.vue'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import type { IconName } from '../ui/icons'
import { dialog, message } from '../ui/feedback'
import { ShareService, type Share } from '../api'
import { useAccountStore } from '../stores/account'
import { useProfilesStore } from '../stores/profiles'
import { describeError, toAppError } from '../errors'
import { shareKindLabels } from '../labels'
import { formatTime } from '../components/shareFormat'
import { portValue } from '../util'
import mascot from '../assets/mascot.png'

// 我的分享（PLAN 6.7）：订阅人数、过期时间、撤销、重新发布、复制 ID。
const account = useAccountStore()
const profiles = useProfilesStore()

const shares = ref<Share[]>([])
const loading = ref(false)
const createShow = ref(false)
const busy = ref('')
const created = ref<Share | null>(null)

async function load() {
  if (!account.loggedIn) {
    shares.value = []
    return
  }
  loading.value = true
  try {
    shares.value = (await ShareService.ListMine()) ?? []
  } catch (e) {
    message.error(describeError(toAppError(e)))
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => account.loggedIn, load)

async function copyId(id: string) {
  try {
    await Clipboard.SetText(id)
    message.success('分享 ID 已复制，发给对方，对方在“订阅”里粘贴即可')
  } catch {
    message.info(`分享 ID：${id}`)
  }
}

function onCreated(sh: Share) {
  shares.value = [sh, ...shares.value]
  created.value = sh
}

async function republish(sh: Share) {
  busy.value = sh.id
  try {
    const next = await ShareService.Republish(sh.id, sh.sourceProfileId ?? '')
    shares.value = shares.value.map((x) => (x.id === sh.id ? next : x))
    message.success(`“${sh.name}”已按来源配置的当前内容重新发布，订阅者下次同步时更新`)
  } catch (e) {
    message.error(describeError(toAppError(e)))
  } finally {
    busy.value = ''
  }
}

function askRevoke(sh: Share) {
  dialog.warning({
    title: '撤销分享',
    content: `撤销“${sh.name}”？所有订阅者都会停止使用，无法恢复。`,
    icon: 'alert',
    danger: true,
    positiveText: '撤销',
    negativeText: '取消',
    onPositiveClick: async () => {
      busy.value = sh.id
      try {
        await ShareService.Revoke(sh.id)
        message.success(`已撤销“${sh.name}”，订阅者会在下次同步时停止`)
        await load()
      } catch (e) {
        message.error(describeError(toAppError(e)))
      } finally {
        busy.value = ''
      }
    },
  })
}

// ---- 修改名称、过期时间、人数上限 ----
const edit = ref<{ show: boolean; id: string; name: string; expiresAt: string; maxSubscribers: string; error: string; busy: boolean }>({
  show: false,
  id: '',
  name: '',
  expiresAt: '',
  maxSubscribers: '0',
  error: '',
  busy: false,
})

/** RFC 3339 → datetime-local 输入框的值（本地时间）。 */
function toLocalInput(t?: string | null): string {
  if (!t) return ''
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function openEdit(sh: Share) {
  edit.value = {
    show: true,
    id: sh.id,
    name: sh.name,
    expiresAt: toLocalInput(sh.expiresAt),
    maxSubscribers: String(sh.maxSubscribers ?? 0),
    error: '',
    busy: false,
  }
}

async function saveEdit() {
  const e = edit.value
  e.busy = true
  try {
    const next = await ShareService.Update(e.id, {
      name: e.name.trim(),
      expiresAt: e.expiresAt ? new Date(e.expiresAt).toISOString() : null,
      clearExpiry: !e.expiresAt,
      maxSubscribers: portValue(e.maxSubscribers),
    })
    shares.value = shares.value.map((x) => (x.id === e.id ? next : x))
    e.show = false
  } catch (err) {
    e.error = describeError(toAppError(err))
  } finally {
    e.busy = false
  }
}

function sourceName(id?: string) {
  if (!id) return '（已删除）'
  return profiles.list.find((p) => p.id === id)?.name ?? '（不在本机）'
}

const kindIcon: Record<string, IconName> = { full: 'layers', template: 'server', p2p: 'radar' }
const view = computed(() =>
  shares.value.map((s, i) => ({
    s,
    i,
    state: s.revoked ? { text: '已撤销', tone: 'bad' } : s.expired ? { text: '已过期', tone: 'warn' } : { text: '有效', tone: 'ok' },
    subs: `${s.subscribers}${s.maxSubscribers ? ` / ${s.maxSubscribers}` : ''}`,
    pct: s.maxSubscribers ? Math.min(100, Math.round((s.subscribers / s.maxSubscribers) * 100)) : 0,
  })),
)
</script>

<template>
  <div class="page dotted-bg">
    <div class="inner shares-page">
      <div class="pg-head head">
        <div>
          <h1>我的分享</h1>
          <p>分享保存的是创建时的快照；改了来源配置后点“重新发布”，订阅者下次同步时就会更新。要订阅别人的分享，在“隧道”页左侧的“订阅”里添加。</p>
        </div>
        <div class="pg-actions">
          <button type="button" class="btn ghost sm" :disabled="!account.loggedIn || loading" @click="load"><Icon name="refresh" size="s" :class="{ spin: loading }" />刷新</button>
          <button type="button" class="btn pri sm" :disabled="!account.loggedIn" data-testid="share-create" @click="createShow = true"><Icon name="plus" size="s" />创建分享</button>
        </div>
      </div>

      <div v-if="!account.loggedIn" class="empty" style="padding-top: 60px">
        <img :src="mascot" alt="" />
        <b>登录后可以把云端配置分享给别人</b>
        <p>分享出去的节点和代理，对方订阅后就能直接启动</p>
        <button type="button" class="btn pri sm" @click="account.openAuth('login')">登录 / 注册</button>
      </div>
      <template v-else>
        <div v-if="!shares.length && !loading" class="empty">
          <img :src="mascot" alt="" />
          <b>还没有分享</b>
          <p>点右上角的“创建分享”，把一份云端配置分享给朋友</p>
        </div>
        <div class="share-grid" data-testid="share-table">
          <article v-for="{ s, i, state, subs, pct } in view" :key="s.id" class="scard" :class="{ dim: s.revoked }" :style="{ animationDelay: `${i * 0.05}s` }">
            <div class="scard-top">
              <span class="tile-ic lg" :class="{ template: 'info', p2p: 'gold' }[s.kind] ?? ''"><Icon :name="kindIcon[s.kind] ?? 'share'" /></span>
              <div class="scard-t">
                <b :title="s.name" :data-testid="`share-row-${s.name}`">{{ s.name }}</b>
                <small>{{ shareKindLabels[s.kind] ?? s.kind }}分享 · 来自 {{ sourceName(s.sourceProfileId) }}</small>
              </div>
              <span class="bdg" :class="state.tone" :data-testid="`share-state-${s.name}`">{{ state.text }}</span>
            </div>
            <div class="scard-stats">
              <div class="stat">
                <small><Icon name="users" size="xs" />订阅</small>
                <b><span :data-testid="`share-subs-${s.name}`">{{ subs }}</span></b>
                <div v-if="s.maxSubscribers" class="meter"><i :style="{ width: `${pct}%` }" /></div>
              </div>
              <div class="stat">
                <small><Icon name="calendar" size="xs" />过期时间</small>
                <b>{{ s.expiresAt ? formatTime(s.expiresAt) : '不过期' }}</b>
              </div>
            </div>
            <template v-if="!s.revoked">
              <div class="idbox">
                <span class="mono" :title="s.id">{{ s.id }}</span>
                <button type="button" class="btn soft xs" :data-testid="`share-copy-${s.name}`" @click="copyId(s.id)"><Icon name="copy" size="xs" />复制 ID</button>
              </div>
              <div class="scard-acts">
                <button type="button" class="btn quiet xs" :disabled="!s.sourceProfileId || busy === s.id" @click="republish(s)">
                  <Icon :name="busy === s.id ? 'refresh' : 'upload'" size="xs" :class="{ spin: busy === s.id }" />重新发布
                </button>
                <button type="button" class="btn quiet xs" @click="openEdit(s)"><Icon name="settings" size="xs" />设置</button>
                <span class="grow" />
                <button type="button" class="btn danger xs" :data-testid="`share-revoke-${s.name}`" @click="askRevoke(s)">撤销</button>
              </div>
            </template>
            <p v-else class="fld-help">已撤销，订阅者已停止使用</p>
          </article>
        </div>
      </template>
    </div>

    <CreateShareModal v-model:show="createShow" :profile-id="profiles.currentSummary?.origin === 'cloud' ? profiles.currentId : ''" @created="onCreated" />

    <Modal :show="!!created" center :width="420" title="分享已创建" @update:show="created = null">
      <div class="big-ic success"><Icon name="check" /></div>
      <h2 class="dlg-title">分享已创建</h2>
      <p class="dlg-text">把下面的分享 ID 发给对方，对方在“订阅”里粘贴即可</p>
      <div class="idbox big"><span class="mono" data-testid="share-created-id">{{ created?.id }}</span></div>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="created = null">关闭</button>
        <button type="button" class="btn pri sm" @click="created && copyId(created.id)"><Icon name="copy" size="s" />复制 ID</button>
      </template>
    </Modal>

    <Modal v-model:show="edit.show" title="分享设置" icon="settings" :width="420" :mask-closable="!edit.busy">
      <form @submit.prevent="saveEdit">
        <div class="fld"><label for="se-name">名称</label><input id="se-name" v-model="edit.name" class="inp" maxlength="50" /></div>
        <div class="fld">
          <label for="se-exp">过期时间</label>
          <input id="se-exp" v-model="edit.expiresAt" class="inp" type="datetime-local" />
          <span class="fld-help">清空表示不过期</span>
        </div>
        <div class="fld">
          <label for="se-max">人数上限</label>
          <input id="se-max" v-model="edit.maxSubscribers" class="inp" inputmode="numeric" />
          <span class="fld-help">0 表示不限</span>
        </div>
        <div v-if="edit.error" class="alert bad">{{ edit.error }}</div>
        <button type="submit" hidden />
      </form>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="edit.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="edit.busy" @click="saveEdit">保存</button>
      </template>
    </Modal>
  </div>
</template>

<style>
.page {
  height: 100%;
  min-height: 0;
  overflow: auto;
  padding: 24px 28px 32px;
}
.inner {
  max-width: 1120px;
  margin: 0 auto;
}
.pg-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 12px 16px;
  margin-bottom: 18px;
}
.pg-head h1 {
  font-size: 24px;
  font-weight: 900;
  letter-spacing: -0.01em;
}
.pg-head p {
  margin-top: 5px;
  max-width: 640px;
  font-size: 12.5px;
  color: var(--text-2);
  font-weight: 600;
  line-height: 1.6;
}
.pg-actions {
  display: flex;
  gap: 8px;
  margin-left: auto;
}
.share-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(320px, 100%), 1fr));
  gap: 14px;
}
.scard {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border-radius: 21px;
  background: var(--surface);
  border: 1.5px solid var(--line-2);
  box-shadow: var(--shadow-sm);
  animation: rowIn 0.5s var(--ease-out) backwards;
  transition: box-shadow 0.25s, transform 0.25s, background-color 0.35s;
}
.scard:hover {
  box-shadow: var(--shadow);
  transform: translateY(-2px);
}
.scard.dim {
  background: color-mix(in oklab, var(--surface) 55%, transparent);
  box-shadow: none;
}
.scard.dim:hover {
  transform: none;
}
.scard-top {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.scard-t {
  flex: 1;
  min-width: 0;
}
.scard-t b {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15.5px;
  font-weight: 900;
}
.scard-t small {
  display: block;
  margin-top: 1px;
  color: var(--text-3);
  font-size: 12px;
  font-weight: 700;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.scard-stats {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.stat {
  padding: 9px 11px;
  border-radius: 13px;
  background: var(--fill);
}
.stat small {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  font-weight: 800;
  color: var(--text-3);
}
.stat b {
  display: block;
  margin-top: 2px;
  font-size: 13.5px;
  font-weight: 800;
}
.meter {
  height: 5px;
  margin-top: 6px;
  border-radius: 99px;
  background: color-mix(in oklab, var(--text-3) 18%, transparent);
  overflow: hidden;
}
.meter i {
  display: block;
  height: 100%;
  border-radius: 99px;
  background: var(--pri);
  transform-origin: left;
  animation: grow 1s var(--ease-out) backwards 0.2s;
  transition: width 0.4s;
}
.idbox {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 6px 6px 12px;
  border-radius: 12px;
  border: 1.5px dashed var(--line);
  font-size: 12px;
}
.idbox span {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-2);
}
.idbox.big {
  margin-top: 14px;
  justify-content: center;
  padding: 12px;
}
.idbox.big span {
  flex: none;
  font-size: 15px;
  color: var(--text);
  font-weight: 600;
  user-select: all;
}
.scard-acts {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.scard-acts .grow {
  flex: 1;
}
</style>
