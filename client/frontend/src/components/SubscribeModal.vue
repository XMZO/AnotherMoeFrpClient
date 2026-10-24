<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import Check from '../ui/Check.vue'
import { message } from '../ui/feedback'
import { ShareService, type SharePreview, type P2PBind } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, toAppError } from '../errors'
import { shareKindHints, shareKindLabels } from '../labels'
import { formatTime } from './shareFormat'
import { portValue } from '../util'

// 添加订阅（PLAN 6.7）：输入分享 ID → 预览（不含机密）→ 按类型填参数 → 订阅。
const show = defineModel<boolean>('show', { default: false })

const profiles = useProfilesStore()

const shareId = ref('')
const preview = ref<SharePreview | null>(null)
const loading = ref(false)
const error = ref('')
const nodeId = ref('')
type BindRow = { targetId: string; serverName: string; type: string; on: boolean; name: string; port: string }
const binds = ref<BindRow[]>([])

watch(show, (v) => {
  if (!v) return
  shareId.value = ''
  preview.value = null
  error.value = ''
})

const kind = computed(() => preview.value?.kind ?? '')

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
      port: t.localPort ? String(Math.min(t.localPort + 10000, 65535)) : '',
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
    if (chosen.some((b) => !portValue(b.port))) {
      error.value = '请为选中的目标填写本地端口'
      return
    }
    params.binds = chosen.map((b) => ({ id: '', targetId: b.targetId, name: b.name.trim(), enabled: true, bindAddr: '127.0.0.1', bindPort: portValue(b.port) }))
  }
  loading.value = true
  try {
    const sum = await ShareService.Subscribe(pv.id, params as any)
    message.success(`已订阅“${sum.shareName}”`)
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
  <Modal v-model:show="show" title="添加订阅" subtitle="输入别人给你的分享 ID，先预览再订阅" icon="link" tone="gold" :width="620" :mask-closable="!loading" data-testid="subscribe-modal">
    <form class="inp-group" style="margin-bottom: 12px" @submit.prevent="load">
      <label for="sb-id" class="sr-only">分享 ID</label>
      <input id="sb-id" v-model="shareId" class="inp mono" placeholder="粘贴别人给你的分享 ID" data-testid="subscribe-id" />
      <button type="submit" class="btn ghost sm" style="height: 40px" :disabled="loading && !preview" data-testid="subscribe-preview">
        <Icon :name="loading && !preview ? 'refresh' : 'eye'" size="s" :class="{ spin: loading && !preview }" />预览
      </button>
    </form>

    <Transition name="collapse">
      <div v-if="preview">
        <div class="pv-grid">
          <div class="stat"><small>名称</small><b data-testid="subscribe-preview-name">{{ preview.name }}</b></div>
          <div class="stat"><small>类型</small><b><span class="bdg gold" data-testid="subscribe-preview-kind">{{ shareKindLabels[preview.kind] ?? preview.kind }}</span></b></div>
          <div class="stat"><small>分享者</small><b>{{ preview.ownerNickname }}</b></div>
          <div class="stat"><small>订阅人数</small><b>{{ preview.subscribers }}{{ preview.maxSubscribers ? ` / ${preview.maxSubscribers}` : '' }}</b></div>
          <div v-if="preview.expiresAt" class="stat wide"><small>过期时间</small><b>{{ formatTime(preview.expiresAt) }}</b></div>
        </div>
        <div class="type-hint" style="margin: 0 0 14px"><Icon name="info" size="xs" /><span>{{ shareKindHints[preview.kind] }}</span></div>

        <div v-if="preview.subscription" class="alert info">
          <Icon name="info" size="s" />
          <span>你已经订阅过这个分享。<button type="button" class="link-btn" @click="openExisting">查看</button></span>
        </div>

        <template v-else>
          <div v-if="kind !== 'p2p'" class="fld">
            <label for="sb-node">节点</label>
            <select id="sb-node" v-model="nodeId" class="inp" data-testid="subscribe-node">
              <option v-if="kind !== 'template'" value="">默认第一个节点，启动时也可以换</option>
              <option v-for="n in preview.nodes ?? []" :key="n.id" :value="n.id">{{ n.remark || n.id }}</option>
            </select>
          </div>

          <div v-if="kind === 'full'" class="fld">
            <span class="lbl">会运行的代理</span>
            <div class="pv-list">
              <div v-for="p in preview.proxies ?? []" :key="p.name" class="pv-item">
                <b>{{ p.name }}</b><span class="type">{{ p.type }}</span>
                <span class="mono pv-remote">{{ p.remotePort ? `:${p.remotePort}` : (p.customDomains ?? []).join(', ') || p.subdomain || '' }}</span>
              </div>
            </div>
          </div>

          <p v-if="kind === 'template'" class="fld-help">订阅后，在编辑区添加你自己的代理规则。</p>

          <div v-if="kind === 'p2p'" class="fld">
            <span class="lbl">要访问的目标</span>
            <div class="pv-binds">
              <div class="pv-bind head"><span /><span>目标</span><span>本机访问名</span><span>本地端口</span></div>
              <div v-for="b in binds" :key="b.targetId" class="pv-bind">
                <Check v-model="b.on" plain :aria-label="`访问 ${b.serverName}`" />
                <span class="pv-target"><b>{{ b.serverName }}</b><span class="type p2p">{{ b.type }}</span></span>
                <input v-model="b.name" class="inp mono" :disabled="!b.on" maxlength="50" :aria-label="`${b.serverName} 的本机访问名`" />
                <input
                  v-model="b.port"
                  class="inp mono"
                  :disabled="!b.on"
                  inputmode="numeric"
                  placeholder="例如 13389"
                  :aria-label="`${b.serverName} 的本地端口`"
                  :data-testid="`subscribe-port-${b.serverName}`"
                />
              </div>
            </div>
            <span class="fld-help">启动后，在本机访问 127.0.0.1:本地端口 就能连到分享者的服务。</span>
          </div>
        </template>
      </div>
    </Transition>

    <div v-if="error" class="alert bad" style="margin-top: 12px" data-testid="subscribe-error"><Icon name="alert" size="s" /><span>{{ error }}</span></div>
    <template #footer>
      <button type="button" class="btn quiet sm" @click="show = false">取消</button>
      <button type="button" class="btn pri sm" :disabled="!preview || !!preview.subscription || loading" data-testid="subscribe-submit" @click="subscribe">
        <Icon v-if="loading && preview" name="refresh" size="s" class="spin" />订阅
      </button>
    </template>
  </Modal>
</template>

<style>
.pv-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 10px;
}
.pv-grid .wide {
  grid-column: span 2;
}
.pv-grid .stat b {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pv-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pv-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: 12px;
  background: var(--fill);
}
.pv-remote {
  margin-left: auto;
  color: var(--text-2);
}
.pv-binds {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pv-bind {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr) minmax(0, 1fr) 120px;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 12px;
  background: var(--fill);
}
.pv-bind.head {
  background: none;
  padding: 0 8px;
  font-size: 11.5px;
  font-weight: 800;
  color: var(--text-3);
}
.pv-bind .inp {
  height: 34px;
  border-radius: 10px;
}
.pv-target {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.pv-target b {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.link-btn {
  color: var(--pri-text);
  font-weight: 800;
  text-decoration: underline;
  text-underline-offset: 3px;
}
</style>
