<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCheckbox,
  NCheckboxGroup,
  NDatePicker,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NRadio,
  NRadioGroup,
  NSelect,
  NSpace,
  NTag,
  NText,
} from 'naive-ui'
import { ProfileService, ShareService, type Profile, type Share } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, toAppError } from '../errors'
import { isP2PType, shareKindHints, shareKindLabels } from '../labels'
import { nodeLabel } from '../util'

// 创建分享向导（PLAN 6.7）：选配置 → 选类型 → 选节点和代理 → 过期时间、人数上限。
const show = defineModel<boolean>('show', { default: false })
const props = defineProps<{ profileId?: string }>()
const emit = defineEmits<{ created: [Share] }>()

const profiles = useProfilesStore()
const cloudProfiles = computed(() => profiles.list.filter((p) => p.origin === 'cloud'))
const profileOptions = computed(() => cloudProfiles.value.map((p) => ({ label: p.name, value: p.id })))

const profileId = ref('')
const profile = ref<Profile | null>(null)
const kind = ref<'template' | 'full' | 'p2p'>('full')
const name = ref('')
const nodeIds = ref<string[]>([])
const p2pNode = ref('')
const proxyIds = ref<string[]>([])
const expiresAt = ref<number | null>(null)
const maxSubscribers = ref<number | null>(0)
const error = ref('')
const saving = ref(false)

watch(show, (v) => {
  if (!v) return
  error.value = ''
  expiresAt.value = null
  maxSubscribers.value = 0
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
      maxSubscribers: maxSubscribers.value ?? 0,
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
  <n-modal v-model:show="show" preset="card" title="创建分享" style="width: 640px" :mask-closable="!saving" data-testid="share-create-modal">
    <n-alert v-if="!cloudProfiles.length" type="warning" :show-icon="false" style="margin-bottom: 12px">
      只能分享云端配置。先在"隧道"页新建云端配置，或把本地配置上传到云端。
    </n-alert>
    <n-form label-placement="left" label-width="90" size="small">
      <n-form-item label="来源配置">
        <n-select v-model:value="profileId" :options="profileOptions" placeholder="选择一份云端配置" data-testid="share-create-profile" />
      </n-form-item>
      <n-form-item label="分享类型">
        <n-space vertical :size="2">
          <n-radio-group v-model:value="kind" data-testid="share-create-kind">
            <n-space>
              <n-radio value="full" data-testid="share-kind-full">{{ shareKindLabels.full }}</n-radio>
              <n-radio value="template" data-testid="share-kind-template">{{ shareKindLabels.template }}</n-radio>
              <n-radio value="p2p" data-testid="share-kind-p2p">{{ shareKindLabels.p2p }}</n-radio>
            </n-space>
          </n-radio-group>
          <n-text depth="3" style="font-size: 12px">{{ shareKindHints[kind] }}</n-text>
        </n-space>
      </n-form-item>
      <n-form-item label="名称">
        <n-input v-model:value="name" maxlength="50" placeholder="对方看到的分享名称" data-testid="share-create-name" />
      </n-form-item>
      <n-form-item v-if="profile" label="节点">
        <n-radio-group v-if="kind === 'p2p'" v-model:value="p2pNode">
          <n-space vertical :size="4">
            <n-radio v-for="n in profile.nodes ?? []" :key="n.id" :value="n.id">{{ nodeLabel(n) }}</n-radio>
          </n-space>
        </n-radio-group>
        <n-checkbox-group v-else v-model:value="nodeIds">
          <n-space vertical :size="4">
            <n-checkbox v-for="n in profile.nodes ?? []" :key="n.id" :value="n.id" :label="nodeLabel(n)" />
          </n-space>
        </n-checkbox-group>
        <n-text v-if="!(profile.nodes ?? []).length" depth="3">这份配置还没有节点</n-text>
      </n-form-item>
      <n-form-item v-if="profile && kind !== 'template'" :label="kind === 'p2p' ? 'P2P 目标' : '代理'">
        <n-checkbox-group v-model:value="proxyIds">
          <n-space vertical :size="4">
            <n-checkbox v-for="x in candidateProxies" :key="x.id" :value="x.id" :data-testid="`share-create-proxy-${x.name}`">
              {{ x.name }} <n-tag size="tiny" :bordered="false">{{ x.type }}</n-tag>
            </n-checkbox>
          </n-space>
        </n-checkbox-group>
        <n-text v-if="!candidateProxies.length" depth="3">{{ kind === 'p2p' ? '这份配置里没有 stcp/xtcp/sudp 代理' : '这份配置还没有代理' }}</n-text>
      </n-form-item>
      <n-form-item label="过期时间">
        <n-date-picker v-model:value="expiresAt" type="datetime" clearable placeholder="不过期" :is-date-disabled="(ts: number) => ts < Date.now() - 86400000" />
      </n-form-item>
      <n-form-item label="人数上限">
        <n-input-number v-model:value="maxSubscribers" :min="0" :max="10000" style="width: 160px" />
        <n-text depth="3" style="margin-left: 8px; font-size: 12px">0 表示不限</n-text>
      </n-form-item>
    </n-form>
    <n-text depth="3" style="font-size: 12px; display: block">
      注意：订阅者运行隧道时一定能拿到节点的 token（frp 的设计如此）。分享就等于允许对方使用你的节点；撤销后对方会停止，但已经拿到的 token 收不回来，必要时请更换节点 token。
    </n-text>
    <n-text v-if="error" type="error" style="display: block; margin-top: 8px" data-testid="share-create-error">{{ error }}</n-text>
    <template #footer>
      <n-space justify="end">
        <n-button @click="show = false">取消</n-button>
        <n-button type="primary" :loading="saving" :disabled="!profileId" data-testid="share-create-submit" @click="submit">创建</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
