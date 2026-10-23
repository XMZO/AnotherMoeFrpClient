<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NDatePicker,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NPopconfirm,
  NSpace,
  NTag,
  NText,
  useDialog,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { Clipboard } from '@wailsio/runtime'
import CreateShareModal from '../components/CreateShareModal.vue'
import { ShareService, type Share } from '../api'
import { useAccountStore } from '../stores/account'
import { useProfilesStore } from '../stores/profiles'
import { describeError, toAppError } from '../errors'
import { shareKindLabels } from '../labels'
import { formatTime } from '../components/shareFormat'

// 我的分享（PLAN 6.7）：订阅人数、过期时间、撤销、重新发布、复制 ID。
const account = useAccountStore()
const profiles = useProfilesStore()
const message = useMessage()
const dialog = useDialog()

const shares = ref<Share[]>([])
const loading = ref(false)
const createShow = ref(false)
const busy = ref('')

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
    message.success('分享 ID 已复制，发给对方，对方在"订阅"里粘贴即可')
  } catch {
    message.info(`分享 ID：${id}`)
  }
}

function onCreated(sh: Share) {
  shares.value = [sh, ...shares.value]
  dialog.success({
    title: '分享已创建',
    content: () =>
      h('div', [
        h('p', '把下面的分享 ID 发给对方：'),
        h(NText, { code: true, 'data-testid': 'share-created-id' }, () => sh.id),
      ]),
    positiveText: '复制 ID',
    negativeText: '关闭',
    onPositiveClick: () => copyId(sh.id),
  })
}

async function republish(sh: Share) {
  busy.value = sh.id
  try {
    const next = await ShareService.Republish(sh.id, sh.sourceProfileId ?? '')
    shares.value = shares.value.map((x) => (x.id === sh.id ? next : x))
    message.success(`"${sh.name}"已按来源配置的当前内容重新发布，订阅者下次同步时更新`)
  } catch (e) {
    message.error(describeError(toAppError(e)))
  } finally {
    busy.value = ''
  }
}

async function revoke(sh: Share) {
  busy.value = sh.id
  try {
    await ShareService.Revoke(sh.id)
    message.success(`已撤销"${sh.name}"，订阅者会在下次同步时停止`)
    await load()
  } catch (e) {
    message.error(describeError(toAppError(e)))
  } finally {
    busy.value = ''
  }
}

// ---- 修改名称、过期时间、人数上限 ----
const edit = ref<{ show: boolean; id: string; name: string; expiresAt: number | null; maxSubscribers: number | null; error: string }>({
  show: false,
  id: '',
  name: '',
  expiresAt: null,
  maxSubscribers: 0,
  error: '',
})

function openEdit(sh: Share) {
  edit.value = {
    show: true,
    id: sh.id,
    name: sh.name,
    expiresAt: sh.expiresAt ? new Date(sh.expiresAt).getTime() : null,
    maxSubscribers: sh.maxSubscribers ?? 0,
    error: '',
  }
}

async function saveEdit() {
  const e = edit.value
  try {
    const next = await ShareService.Update(e.id, {
      name: e.name.trim(),
      expiresAt: e.expiresAt ? new Date(e.expiresAt).toISOString() : null,
      clearExpiry: !e.expiresAt,
      maxSubscribers: e.maxSubscribers ?? 0,
    })
    shares.value = shares.value.map((x) => (x.id === e.id ? next : x))
    edit.value.show = false
  } catch (err) {
    edit.value.error = describeError(toAppError(err))
  }
  return false
}

function sourceName(id?: string) {
  if (!id) return '（已删除）'
  return profiles.list.find((p) => p.id === id)?.name ?? '（不在本机）'
}

function stateTag(sh: Share) {
  if (sh.revoked) return h(NTag, { size: 'small', type: 'error', bordered: false }, () => '已撤销')
  if (sh.expired) return h(NTag, { size: 'small', type: 'warning', bordered: false }, () => '已过期')
  return h(NTag, { size: 'small', type: 'success', bordered: false }, () => '有效')
}

const columns = computed<DataTableColumns<Share>>(() => [
  { title: '名称', key: 'name', minWidth: 120, ellipsis: { tooltip: true }, render: (r) => h('span', { 'data-testid': `share-row-${r.name}` }, r.name) },
  { title: '类型', key: 'kind', width: 70, render: (r) => h(NTag, { size: 'small', bordered: false }, () => shareKindLabels[r.kind] ?? r.kind) },
  { title: '来源配置', key: 'source', minWidth: 110, ellipsis: { tooltip: true }, render: (r) => sourceName(r.sourceProfileId) },
  {
    title: '订阅',
    key: 'subs',
    width: 70,
    render: (r) => h('span', { 'data-testid': `share-subs-${r.name}` }, `${r.subscribers}${r.maxSubscribers ? ` / ${r.maxSubscribers}` : ''}`),
  },
  { title: '过期时间', key: 'expires', width: 130, render: (r) => (r.expiresAt ? formatTime(r.expiresAt) : '不过期') },
  { title: '状态', key: 'state', width: 76, render: (r) => h('span', { 'data-testid': `share-state-${r.name}` }, [stateTag(r)]) },
  {
    title: '操作',
    key: 'actions',
    width: 250,
    render: (r) =>
      r.revoked
        ? h(NText, { depth: 3 }, () => '—')
        : h(NSpace, { size: 2, wrap: false }, () => [
            h(NButton, { size: 'tiny', quaternary: true, 'data-testid': `share-copy-${r.name}`, onClick: () => copyId(r.id) }, () => '复制 ID'),
            h(
              NButton,
              { size: 'tiny', quaternary: true, loading: busy.value === r.id, disabled: !r.sourceProfileId, onClick: () => republish(r) },
              () => '重新发布',
            ),
            h(NButton, { size: 'tiny', quaternary: true, onClick: () => openEdit(r) }, () => '设置'),
            h(
              NPopconfirm,
              { onPositiveClick: () => revoke(r) },
              {
                trigger: () => h(NButton, { size: 'tiny', quaternary: true, type: 'error', 'data-testid': `share-revoke-${r.name}` }, () => '撤销'),
                default: () => `撤销"${r.name}"？所有订阅者都会停止使用，无法恢复。`,
              },
            ),
          ]),
  },
])
</script>

<template>
  <div class="shares-page">
    <div class="head">
      <n-text strong style="font-size: 16px">我的分享</n-text>
      <n-space>
        <n-button size="small" :disabled="!account.loggedIn" :loading="loading" @click="load">刷新</n-button>
        <n-button size="small" type="primary" :disabled="!account.loggedIn" data-testid="share-create" @click="createShow = true">创建分享</n-button>
      </n-space>
    </div>
    <n-empty v-if="!account.loggedIn" description="登录后可以把云端配置分享给别人" style="margin-top: 80px" />
    <template v-else>
      <n-text depth="3" style="font-size: 12px; display: block; margin-bottom: 8px">
        分享保存的是创建时的快照；改了来源配置后点"重新发布"，订阅者下次同步时就会更新。要订阅别人的分享，在"隧道"页左侧的"订阅"里添加。
      </n-text>
      <n-data-table size="small" :columns="columns" :data="shares" :row-key="(r: Share) => r.id" :loading="loading" :bordered="false" data-testid="share-table" />
    </template>

    <CreateShareModal v-model:show="createShow" :profile-id="profiles.currentSummary?.origin === 'cloud' ? profiles.currentId : ''" @created="onCreated" />

    <n-modal v-model:show="edit.show" preset="dialog" title="分享设置" positive-text="保存" negative-text="取消" :on-positive-click="saveEdit">
      <n-form label-placement="left" label-width="80" size="small">
        <n-form-item label="名称"><n-input v-model:value="edit.name" maxlength="50" /></n-form-item>
        <n-form-item label="过期时间"><n-date-picker v-model:value="edit.expiresAt" type="datetime" clearable placeholder="不过期" /></n-form-item>
        <n-form-item label="人数上限">
          <n-input-number v-model:value="edit.maxSubscribers" :min="0" :max="10000" style="width: 140px" />
          <n-text depth="3" style="margin-left: 8px; font-size: 12px">0 表示不限</n-text>
        </n-form-item>
      </n-form>
      <n-text v-if="edit.error" type="error">{{ edit.error }}</n-text>
    </n-modal>
  </div>
</template>

<style scoped>
.shares-page {
  padding: 12px 16px;
  overflow: auto;
  height: 100%;
}
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}
</style>
