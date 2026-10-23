<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCheckbox,
  NDescriptions,
  NDescriptionsItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpace,
  NTable,
  NTag,
  NText,
  useMessage,
} from 'naive-ui'
import { ShareService, type SharePreview, type P2PBind } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, toAppError } from '../errors'
import { shareKindHints, shareKindLabels } from '../labels'
import { formatTime } from './shareFormat'

// 添加订阅（PLAN 6.7）：输入分享 ID → 预览（不含机密）→ 按类型填参数 → 订阅。
const show = defineModel<boolean>('show', { default: false })

const profiles = useProfilesStore()
const message = useMessage()

const shareId = ref('')
const preview = ref<SharePreview | null>(null)
const loading = ref(false)
const error = ref('')
const nodeId = ref('')
type BindRow = { targetId: string; serverName: string; type: string; on: boolean; name: string; port: number | null }
const binds = ref<BindRow[]>([])

watch(show, (v) => {
  if (!v) return
  shareId.value = ''
  preview.value = null
  error.value = ''
})

const kind = computed(() => preview.value?.kind ?? '')
const nodeOptions = computed(() => (preview.value?.nodes ?? []).map((n) => ({ label: n.remark || n.id, value: n.id })))

async function load() {
  error.value = ''
  preview.value = null
  const id = shareId.value.trim()
  if (!id) {
    error.value = '请输入分享 ID'
    return
  }
  loading.value = true
  try {
    const pv = await ShareService.Preview(id)
    preview.value = pv
    nodeId.value = pv.nodes?.[0]?.id ?? ''
    binds.value = (pv.targets ?? []).map((t) => ({
      targetId: t.id,
      serverName: t.serverName,
      type: t.type,
      on: true,
      name: `p2p-${t.serverName}`,
      port: t.localPort ? Math.min(t.localPort + 10000, 65535) : null,
    }))
  } catch (e) {
    error.value = describeError(toAppError(e))
  } finally {
    loading.value = false
  }
}

async function openExisting() {
  const id = preview.value?.subscription?.id
  if (!id) return
  show.value = false
  await profiles.loadList()
  await profiles.select(id)
}

async function subscribe() {
  const pv = preview.value
  if (!pv) return
  error.value = ''
  const params: { nodeId?: string; proxies: any[]; binds: P2PBind[] } = { proxies: [], binds: [] }
  if (pv.kind === 'template') {
    if (!nodeId.value) {
      error.value = '请选择一个节点'
      return
    }
    params.nodeId = nodeId.value
  } else if (pv.kind === 'full') {
    params.nodeId = nodeId.value
  } else {
    const chosen = binds.value.filter((b) => b.on)
    if (!chosen.length) {
      error.value = '至少选择一个要访问的目标'
      return
    }
    if (chosen.some((b) => !b.port)) {
      error.value = '请为选中的目标填写本地端口'
      return
    }
    params.binds = chosen.map((b) => ({ id: '', targetId: b.targetId, name: b.name.trim(), enabled: true, bindAddr: '127.0.0.1', bindPort: b.port ?? 0 }))
  }
  loading.value = true
  try {
    const sum = await ShareService.Subscribe(pv.id, params as any)
    message.success(`已订阅"${sum.shareName}"`)
    show.value = false
    await profiles.loadList()
    await profiles.select(sum.id)
  } catch (e) {
    error.value = describeError(toAppError(e))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <n-modal v-model:show="show" preset="card" title="添加订阅" style="width: 620px" :mask-closable="!loading" data-testid="subscribe-modal">
    <n-space vertical :size="12">
      <n-space :wrap="false">
        <n-input
          v-model:value="shareId"
          placeholder="粘贴别人给你的分享 ID"
          clearable
          style="width: 420px"
          data-testid="subscribe-id"
          @keyup.enter="load"
        />
        <n-button :loading="loading && !preview" data-testid="subscribe-preview" @click="load">预览</n-button>
      </n-space>

      <template v-if="preview">
        <n-descriptions :column="2" size="small" label-placement="left" bordered>
          <n-descriptions-item label="名称">
            <span data-testid="subscribe-preview-name">{{ preview.name }}</span>
          </n-descriptions-item>
          <n-descriptions-item label="类型">
            <n-tag size="small" :bordered="false" type="success" data-testid="subscribe-preview-kind">{{ shareKindLabels[preview.kind] ?? preview.kind }}</n-tag>
          </n-descriptions-item>
          <n-descriptions-item label="分享者">{{ preview.ownerNickname }}</n-descriptions-item>
          <n-descriptions-item label="订阅人数">
            {{ preview.subscribers }}{{ preview.maxSubscribers ? ` / ${preview.maxSubscribers}` : '' }}
          </n-descriptions-item>
          <n-descriptions-item v-if="preview.expiresAt" label="过期时间" :span="2">{{ formatTime(preview.expiresAt) }}</n-descriptions-item>
        </n-descriptions>
        <n-text depth="3" style="font-size: 12px">{{ shareKindHints[preview.kind] }}</n-text>

        <n-alert v-if="preview.subscription" type="info" :show-icon="false">
          你已经订阅过这个分享。
          <n-button text type="primary" @click="openExisting">查看</n-button>
        </n-alert>

        <template v-else>
          <div v-if="kind !== 'p2p'">
            <n-text strong>节点</n-text>
            <n-select
              v-model:value="nodeId"
              :options="nodeOptions"
              style="margin-top: 6px"
              :placeholder="kind === 'template' ? '选择一个节点' : '默认第一个节点，启动时也可以换'"
              data-testid="subscribe-node"
            />
          </div>

          <div v-if="kind === 'full'">
            <n-text strong>会运行的代理</n-text>
            <n-table size="small" :bordered="false" style="margin-top: 6px">
              <tbody>
                <tr v-for="p in preview.proxies ?? []" :key="p.name">
                  <td>{{ p.name }}</td>
                  <td><n-tag size="small" :bordered="false">{{ p.type }}</n-tag></td>
                  <td>{{ p.remotePort ? `:${p.remotePort}` : (p.customDomains ?? []).join(', ') || p.subdomain || '' }}</td>
                </tr>
              </tbody>
            </n-table>
          </div>

          <n-text v-if="kind === 'template'" depth="3" style="font-size: 12px">订阅后，在编辑区添加你自己的代理规则。</n-text>

          <div v-if="kind === 'p2p'">
            <n-text strong>要访问的目标</n-text>
            <n-table size="small" :bordered="false" style="margin-top: 6px">
              <thead>
                <tr><th style="width: 40px" /><th>目标</th><th>本机访问名</th><th style="width: 130px">本地端口</th></tr>
              </thead>
              <tbody>
                <tr v-for="b in binds" :key="b.targetId">
                  <td><n-checkbox v-model:checked="b.on" /></td>
                  <td>{{ b.serverName }} <n-tag size="tiny" :bordered="false">{{ b.type }}</n-tag></td>
                  <td><n-input v-model:value="b.name" size="small" :disabled="!b.on" maxlength="50" /></td>
                  <td>
                    <n-input-number
                      v-model:value="b.port"
                      size="small"
                      :min="1"
                      :max="65535"
                      :show-button="false"
                      :disabled="!b.on"
                      placeholder="例如 13389"
                      :data-testid="`subscribe-port-${b.serverName}`"
                    />
                  </td>
                </tr>
              </tbody>
            </n-table>
            <n-text depth="3" style="font-size: 12px">启动后，在本机访问 127.0.0.1:本地端口 就能连到分享者的服务。</n-text>
          </div>
        </template>
      </template>

      <n-text v-if="error" type="error" data-testid="subscribe-error">{{ error }}</n-text>
    </n-space>
    <template #footer>
      <n-space justify="end">
        <n-button @click="show = false">取消</n-button>
        <n-button
          type="primary"
          :disabled="!preview || !!preview.subscription"
          :loading="loading && !!preview"
          data-testid="subscribe-submit"
          @click="subscribe"
        >订阅</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
