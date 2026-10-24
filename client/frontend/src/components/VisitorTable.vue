<script setup lang="ts">
import { computed, ref } from 'vue'
import VisitorDrawer from './VisitorDrawer.vue'
import { saveFeedback } from './saveFeedback'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import Modal from '../ui/Modal.vue'
import { dialog, message } from '../ui/feedback'
import { ShareService, type Visitor } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { describeError } from '../errors'
import { stateLabels } from '../labels'
import { portValue } from '../util'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const drawer = ref<{ show: boolean; index: number }>({ show: false, index: -1 })
const rows = computed(() => (profiles.current?.visitors ?? []).map((v, index) => ({ ...v, index })))
const isRunningHere = computed(() => !!profiles.current && tunnel.runningProfileId === profiles.current.id)
// P2P 订阅：访客由分享生成，只能改启用开关和本地端口（作为订阅参数保存）
const p2pSub = computed(() => profiles.isSubscription && profiles.currentSummary?.shareKind === 'p2p')
const ro = computed(() => profiles.readonly('visitors'))
const portModal = ref<{ show: boolean; id: string; name: string; port: string; busy: boolean }>({ show: false, id: '', name: '', port: '', busy: false })
const toggling = ref('')

defineExpose({ openAdd: () => (drawer.value = { show: true, index: -1 }) })

async function updateBind(visitorId: string, patch: { enabled?: boolean; bindPort?: number }) {
  const id = profiles.currentId
  const cur = await ShareService.Subscription(id)
  const binds = (cur.params.binds ?? []).map((b) => (b.id === visitorId ? { ...b, ...patch } : b))
  return profiles.updateSubscription(id, { ...cur.params, proxies: [], binds })
}

async function savePort() {
  const m = portModal.value
  m.busy = true
  try {
    saveFeedback(message, await updateBind(m.id, { bindPort: portValue(m.port) }))
    m.show = false
  } catch (e) {
    message.error(describeError(e))
  } finally {
    m.busy = false
  }
}

async function toggle(row: Visitor & { index: number }, enabled: boolean) {
  toggling.value = row.id
  try {
    if (p2pSub.value) saveFeedback(message, await updateBind(row.id, { enabled }))
    else saveFeedback(message, await profiles.toggle('visitors', row.index, enabled))
  } catch (e) {
    message.error(describeError(e))
  } finally {
    toggling.value = ''
  }
}

function askRemove(v: Visitor & { index: number }) {
  dialog.warning({
    title: '删除访客',
    content: `删除访客 ${v.name}？`,
    icon: 'trash',
    danger: true,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        saveFeedback(message, await profiles.removeItem('visitors', v.index))
      } catch (e) {
        message.error(describeError(e))
      }
    },
  })
}

// frp 没有对外暴露访客的状态，只能显示隧道整体的状态
function status(v: Visitor): { tone: string; label: string } {
  if (!v.enabled) return { tone: '', label: '未启用' }
  if (!isRunningHere.value) return { tone: '', label: '未运行' }
  if (tunnel.status.state === 'running') return { tone: 'ok', label: '监听中' }
  return { tone: 'info', label: stateLabels[tunnel.status.state] ?? tunnel.status.state }
}
</script>

<template>
  <div class="rows">
    <TransitionGroup name="row">
      <div v-for="(row, i) in rows" :key="row.id" class="row" :class="{ off: !row.enabled }" :style="{ animationDelay: `${i * 0.04}s` }">
        <Switch
          :model-value="row.enabled"
          :disabled="ro && !p2pSub"
          :loading="toggling === row.id"
          :label="`启用 ${row.name}`"
          :data-testid="`visitor-switch-${row.name}`"
          @change="(v: boolean) => toggle(row, v)"
        />
        <div class="row-name">
          <b :title="row.name">{{ row.name }}</b>
          <span class="type p2p">{{ row.type }}</span>
        </div>
        <div class="row-map mono" :title="`${row.serverUser ? row.serverUser + '.' : ''}${row.serverName} → ${row.bindAddr || '127.0.0.1'}:${row.bindPort}`">
          <span>{{ row.serverUser ? `${row.serverUser}.${row.serverName}` : row.serverName }}</span>
          <Icon name="arrow" size="xs" />
          <span class="rem">{{ row.bindAddr || '127.0.0.1' }}:{{ row.bindPort }}</span>
        </div>
        <div class="row-st"><span class="bdg" :class="status(row).tone">{{ status(row).label }}</span></div>
        <div class="row-act">
          <button
            v-if="p2pSub"
            type="button"
            class="btn quiet xs"
            :data-testid="`visitor-port-${row.name}`"
            @click="portModal = { show: true, id: row.id, name: row.name, port: String(row.bindPort || ''), busy: false }"
          >
            改端口
          </button>
          <template v-else-if="!ro">
            <button type="button" class="icon-btn" title="编辑" :aria-label="`编辑 ${row.name}`" @click="drawer = { show: true, index: row.index }"><Icon name="edit" size="s" /></button>
            <button type="button" class="icon-btn del" title="删除" :aria-label="`删除 ${row.name}`" @click="askRemove(row)"><Icon name="trash" size="s" /></button>
          </template>
        </div>
      </div>
    </TransitionGroup>
    <div v-if="!rows.length" class="empty compact">
      <b>还没有访客</b>
      <p>别人把 stcp / xtcp / sudp 代理的名字和密钥给你后，在这里添加访客</p>
    </div>
    <VisitorDrawer v-model:show="drawer.show" :index="drawer.index" />
    <Modal v-model:show="portModal.show" :title="`修改 ${portModal.name} 的本地端口`" icon="radar" tone="gold" :width="400">
      <form @submit.prevent="savePort">
        <div class="fld">
          <label for="vp-port">本地端口</label>
          <input id="vp-port" v-model="portModal.port" class="inp mono" inputmode="numeric" placeholder="本地端口" data-testid="visitor-port-input" />
          <span class="fld-help">启动后在本机访问 127.0.0.1:这个端口</span>
        </div>
        <button type="submit" hidden />
      </form>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="portModal.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="portModal.busy" @click="savePort">保存</button>
      </template>
    </Modal>
  </div>
</template>
