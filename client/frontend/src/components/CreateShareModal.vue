<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Select from '../ui/Select.vue'
import Modal from '../ui/Modal.vue'
import Check from '../ui/Check.vue'
import type { IconName } from '../ui/icons'
import { ProfileService, ShareService, type Profile, type Share } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, toAppError } from '../errors'
import { isDomainType, isP2PType, proxyTypeLabel, shareKindHints, shareKindLabels } from '../labels'
import { nodeLabel, portValue } from '../util'

// 创建分享向导（PLAN 6.7）：选配置 → 选类型 → 选节点和代理 → 过期时间、人数上限。
const show = defineModel<boolean>('show', { default: false })
const props = defineProps<{ profileId?: string }>()
const emit = defineEmits<{ created: [Share] }>()

const profiles = useProfilesStore()
const cloudProfiles = computed(() => profiles.list.filter((p) => p.origin === 'cloud'))

type Kind = 'template' | 'full' | 'p2p'
const kinds: { value: Kind; icon: IconName; tone: string }[] = [
  { value: 'full', icon: 'layers', tone: '' },
  { value: 'template', icon: 'server', tone: 'info' },
  { value: 'p2p', icon: 'radar', tone: 'gold' },
]

const profileId = ref('')
const profile = ref<Profile | null>(null)
const kind = ref<Kind>('full')
const name = ref('')
const nodeIds = ref<string[]>([])
const p2pNode = ref('')
const proxyIds = ref<string[]>([])
const expiresAt = ref('')
const maxSubscribers = ref('0')
const error = ref('')
const saving = ref(false)

watch(show, (v) => {
  if (!v) return
  error.value = ''
  expiresAt.value = ''
  maxSubscribers.value = '0'
  kind.value = 'full'
  const pick = props.profileId && cloudProfiles.value.some((p) => p.id === props.profileId) ? props.profileId : cloudProfiles.value[0]?.id
  profileId.value = pick ?? ''
  void loadProfile()
})

watch(profileId, () => void loadProfile())

async function loadProfile() {
  profile.value = null
  if (!profileId.value) return
  try {
    const p = await ProfileService.Get(profileId.value)
    profile.value = p
    name.value = p.name
    resetSelection()
  } catch (e) {
    error.value = describeError(toAppError(e))
  }
}

function resetSelection() {
  const p = profile.value
  if (!p) return
  const nodes = p.nodes ?? []
  nodeIds.value = nodes.map((n) => n.id)
  p2pNode.value = nodes[0]?.id ?? ''
  proxyIds.value = (p.proxies ?? []).filter((x) => (kind.value === 'p2p' ? isP2PType(x.type) : true)).map((x) => x.id)
}

watch(kind, () => resetSelection())

const candidateProxies = computed(() => (profile.value?.proxies ?? []).filter((x) => (kind.value === 'p2p' ? isP2PType(x.type) : true)))

// 代理多的时候：列表限高、内部滚动，超过 8 个出现搜索框（选中状态不受筛选影响）
const SEARCH_FROM = 8
const proxyQuery = ref('')
watch(profileId, () => (proxyQuery.value = ''))
const shownProxies = computed(() => {
  const q = proxyQuery.value.trim().toLowerCase()
  return q ? candidateProxies.value.filter((x) => x.name.toLowerCase().includes(q) || x.type.includes(q)) : candidateProxies.value
})
const allNodes = computed(() => (profile.value?.nodes ?? []).length > 0 && nodeIds.value.length === (profile.value?.nodes ?? []).length)
const allProxies = computed(() => candidateProxies.value.length > 0 && proxyIds.value.length === candidateProxies.value.length)
function selectAll(which: 'nodes' | 'proxies', on: boolean) {
  if (which === 'nodes') nodeIds.value = on ? (profile.value?.nodes ?? []).map((n) => n.id) : []
  else proxyIds.value = on ? candidateProxies.value.map((x) => x.id) : []
}

function toggleIn(which: 'nodes' | 'proxies', id: string, on: boolean) {
  const list = which === 'nodes' ? nodeIds : proxyIds
  list.value = on ? [...list.value.filter((x) => x !== id), id] : list.value.filter((x) => x !== id)
}

// 今天之前的日期不能选
const minDate = (() => {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T00:00`
})()

async function submit() {
  error.value = ''
  if (!profileId.value) {
    error.value = '请先选择一份云端配置'
    return
  }
  if (kind.value === 'template' && nodeIds.value.length === 0) {
    error.value = '模板分享至少选择一个节点'
    return
  }
  if (kind.value !== 'template' && proxyIds.value.length === 0) {
    error.value = kind.value === 'p2p' ? '至少选择一个 stcp/xtcp/sudp 代理' : '至少选择一个代理'
    return
  }
  saving.value = true
  try {
    const sh = await ShareService.Create({
      profileId: profileId.value,
      name: name.value.trim(),
      kind: kind.value,
      nodeIds: kind.value === 'p2p' ? [p2pNode.value] : nodeIds.value,
      proxyIds: kind.value === 'template' ? [] : proxyIds.value,
      expiresAt: expiresAt.value ? new Date(expiresAt.value).toISOString() : null,
      maxSubscribers: portValue(maxSubscribers.value),
    })
    show.value = false
    emit('created', sh)
  } catch (e) {
    error.value = describeError(toAppError(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Modal v-model:show="show" title="创建分享" subtitle="只能分享云端配置" icon="share" :width="660" :mask-closable="!saving" data-testid="share-create-modal">
    <div v-if="!cloudProfiles.length" class="alert gold" style="margin-bottom: 14px">
      <Icon name="info" size="s" /><span>只能分享云端配置。先在“隧道”页新建云端配置，或把本地配置上传到云端。</span>
    </div>
    <div class="grid2">
      <div class="fld">
        <label for="cs-src">来源配置</label>
        <Select id="cs-src" v-model="profileId" :options="cloudProfiles.map((p) => ({ value: p.id, label: p.name }))" placeholder="没有云端配置" data-testid="share-create-profile" />
      </div>
      <div class="fld">
        <label for="cs-name">名称</label>
        <input id="cs-name" v-model="name" class="inp" maxlength="50" placeholder="对方看到的分享名称" data-testid="share-create-name" />
      </div>
    </div>
    <div class="fld">
      <span class="lbl">分享类型</span>
      <div class="kinds" role="radiogroup" aria-label="分享类型" data-testid="share-create-kind">
        <button
          v-for="k in kinds"
          :key="k.value"
          type="button"
          role="radio"
          class="kind radio"
          :class="{ on: kind === k.value }"
          :aria-checked="kind === k.value"
          :data-testid="`share-kind-${k.value}`"
          @click="kind = k.value"
        >
          <span class="tile-ic" :class="k.tone"><Icon :name="k.icon" size="s" /></span>
          <b>{{ shareKindLabels[k.value] }}</b>
          <small>{{ shareKindHints[k.value] }}</small>
        </button>
      </div>
    </div>
    <div v-if="profile" class="grid2 eq">
      <div class="fld">
        <span class="lbl pick-lbl">
          节点<small v-if="kind !== 'p2p'">{{ nodeIds.length }} / {{ (profile.nodes ?? []).length }}</small>
          <button v-if="kind !== 'p2p' && (profile.nodes ?? []).length > 1" type="button" class="pick-all" @click="selectAll('nodes', !allNodes)">{{ allNodes ? '全不选' : '全选' }}</button>
        </span>
        <div class="checks pick-list">
          <template v-if="kind === 'p2p'">
            <Check v-for="n in profile.nodes ?? []" :key="n.id" radio :model-value="p2pNode === n.id" @update:model-value="p2pNode = n.id">
              <span class="ck-t">{{ nodeLabel(n) }}</span>
            </Check>
          </template>
          <template v-else>
            <Check v-for="n in profile.nodes ?? []" :key="n.id" :model-value="nodeIds.includes(n.id)" @update:model-value="(v: boolean) => toggleIn('nodes', n.id, v)">
              <span class="ck-t">{{ nodeLabel(n) }}</span>
            </Check>
          </template>
          <span v-if="!(profile.nodes ?? []).length" class="fld-help">这份配置还没有节点</span>
        </div>
      </div>
      <div v-if="kind !== 'template'" class="fld">
        <span class="lbl pick-lbl">
          {{ kind === 'p2p' ? 'P2P 目标' : '代理' }}<small>{{ proxyIds.length }} / {{ candidateProxies.length }}</small>
          <button v-if="candidateProxies.length > 1" type="button" class="pick-all" data-testid="share-create-proxy-all" @click="selectAll('proxies', !allProxies)">{{ allProxies ? '全不选' : '全选' }}</button>
        </span>
        <input v-if="candidateProxies.length > SEARCH_FROM" v-model="proxyQuery" class="inp pick-search" placeholder="搜索名称或类型" aria-label="搜索代理" />
        <div class="checks pick-list">
          <Check
            v-for="x in shownProxies"
            :key="x.id"
            :model-value="proxyIds.includes(x.id)"
            :data-testid="`share-create-proxy-${x.name}`"
            @update:model-value="(v: boolean) => toggleIn('proxies', x.id, v)"
          >
            <span class="ck-t">{{ x.name }}</span>
            <span class="type" :class="isP2PType(x.type) ? 'p2p' : isDomainType(x.type) ? 'web' : ''">{{ proxyTypeLabel(x) }}</span>
          </Check>
          <span v-if="candidateProxies.length && !shownProxies.length" class="fld-help">没有匹配的代理</span>
          <span v-if="!candidateProxies.length" class="fld-help">{{ kind === 'p2p' ? '这份配置里没有 stcp/xtcp/sudp 代理' : '这份配置还没有代理' }}</span>
        </div>
      </div>
    </div>
    <div class="grid2 eq">
      <div class="fld">
        <label for="cs-exp">过期时间</label>
        <input id="cs-exp" v-model="expiresAt" class="inp" type="datetime-local" :min="minDate" />
        <span class="fld-help">留空表示不过期</span>
      </div>
      <div class="fld">
        <label for="cs-max">人数上限</label>
        <input id="cs-max" v-model="maxSubscribers" class="inp" inputmode="numeric" />
        <span class="fld-help">0 表示不限</span>
      </div>
    </div>
    <div class="alert gold">
      <Icon name="alert" size="s" />
      <span>注意：订阅者运行隧道时一定能拿到节点的 token（frp 的设计如此）。分享就等于允许对方使用你的节点；撤销后对方会停止，但已经拿到的 token 收不回来，必要时请更换节点 token。</span>
    </div>
    <div v-if="error" class="alert bad" style="margin-top: 10px" data-testid="share-create-error"><Icon name="alert" size="s" /><span>{{ error }}</span></div>
    <template #footer>
      <button type="button" class="btn quiet sm" @click="show = false">取消</button>
      <button type="button" class="btn pri sm" :disabled="saving || !profileId" data-testid="share-create-submit" @click="submit">
        <Icon :name="saving ? 'refresh' : 'share'" size="s" :class="{ spin: saving }" />创建
      </button>
    </template>
  </Modal>
</template>

<style>
.kinds {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}
.kind {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 12px;
  border-radius: 16px;
  border: 2px solid var(--line-2);
  text-align: left;
  transition: border-color 0.2s, box-shadow 0.2s, transform 0.2s var(--ease-spring);
}
.kind:hover {
  transform: translateY(-2px);
}
.kind b {
  font-size: 13.5px;
  font-weight: 900;
}
.kind small {
  font-size: 11.5px;
  line-height: 1.45;
  color: var(--text-3);
  font-weight: 600;
}
.kind.on {
  border-color: var(--pri);
  box-shadow: 0 0 0 4px var(--pri-soft);
}
.checks {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
/* 节点、代理很多时列表限高滚动，弹窗不会越拉越长 */
.pick-lbl {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.pick-lbl small {
  font-size: 11.5px;
  font-weight: 700;
  color: var(--text-3);
}
.pick-all {
  margin-left: auto;
  font-size: 12px;
  font-weight: 800;
  color: var(--pri-text);
}
.pick-search {
  margin-bottom: 6px;
}
.pick-list {
  max-height: 190px;
  overflow-y: auto;
  padding-right: 4px;
}
.ck-t {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 620px) {
  .kinds {
    grid-template-columns: 1fr;
  }
}
</style>
