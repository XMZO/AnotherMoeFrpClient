<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Drawer from '../ui/Drawer.vue'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import Options from '../ui/Options.vue'
import { message } from '../ui/feedback'
import type { Node } from '../api'
import { TunnelService } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { codeText, describeError, fieldErrorsFor, fieldErrorsOf, type AppError } from '../errors'
import { protocols } from '../labels'
import { saveFeedback } from './saveFeedback'
import { clone, portValue } from '../util'

const props = defineProps<{ show: boolean; index: number }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const profiles = useProfilesStore()

const empty = (): Node => ({ id: '', remark: '', serverAddr: '', serverPort: 7000, token: '', user: '', protocol: '', tls: null, proxyURL: '' })

const form = ref<Node>(empty())
const port = ref('7000')
const error = ref<AppError | null>(null)
const saving = ref(false)
const editIndex = ref(-1)
const probe = ref<{ ok: boolean; text: string } | null>(null)
const probing = ref(false)

watch(
  () => props.show,
  (show) => {
    if (!show) return
    error.value = null
    probe.value = null
    const src = props.index >= 0 ? profiles.current?.nodes?.[props.index] : null
    form.value = src ? { ...empty(), ...clone(src) } : empty()
    port.value = form.value.serverPort ? String(form.value.serverPort) : ''
    editIndex.value = props.index >= 0 ? props.index : profiles.indexForNew('nodes')
  },
)

const fieldErr = computed(() => fieldErrorsFor(error.value, 'nodes', editIndex.value))
const otherError = computed(() => {
  if (!error.value) return ''
  const fields = fieldErrorsOf(error.value)
  if (fields.length && Object.keys(fieldErr.value).length === fields.length) return ''
  return describeError(error.value)
})
const protocolOptions = protocols.map((p) => ({ value: p as string, label: p }))
const protocol = computed({
  get: () => form.value.protocol || 'tcp',
  set: (v: string) => (form.value.protocol = v === 'tcp' ? '' : v),
})
// TLS：nil 表示跟随 frp 默认（开启），所以开关打开时存 null，关闭时存 false
const tlsOn = computed({
  get: () => form.value.tls !== false,
  set: (v: boolean) => (form.value.tls = v ? null : false),
})

function current(): Node {
  const n = clone(form.value)
  n.serverPort = portValue(port.value)
  if (n.protocol === 'tcp') n.protocol = ''
  return n
}

async function test() {
  probing.value = true
  probe.value = null
  try {
    const r = await TunnelService.PingNode(current())
    probe.value = r.ok ? { ok: true, text: `TCP 连接成功，${r.latencyMs} ms` } : { ok: false, text: codeText(r.code, { target: r.target }, r.message) }
  } catch (e) {
    probe.value = { ok: false, text: describeError(e) }
  } finally {
    probing.value = false
  }
}

async function save() {
  saving.value = true
  error.value = null
  try {
    saveFeedback(message, await profiles.upsert('nodes', current(), props.index))
    emit('update:show', false)
  } catch (e) {
    error.value = e as AppError
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Drawer
    :show="show"
    :title="index >= 0 ? '编辑节点' : '添加节点'"
    :subtitle="profiles.current ? `保存在「${profiles.current.name}」` : undefined"
    icon="server"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <form @submit.prevent="save">
      <div v-if="otherError" class="alert bad" style="margin-bottom: 14px" data-testid="node-form-error"><Icon name="alert" size="s" /><span>{{ otherError }}</span></div>
      <div class="fld">
        <label for="nd-remark">备注</label>
        <input id="nd-remark" v-model="form.remark" class="inp" :class="{ err: fieldErr.remark }" placeholder="例如 香港节点" maxlength="50" data-testid="node-form-remark" />
        <span v-if="fieldErr.remark" class="fld-help err">{{ fieldErr.remark }}</span>
      </div>
      <div class="grid2">
        <div class="fld">
          <label for="nd-addr">服务器地址</label>
          <input id="nd-addr" v-model="form.serverAddr" class="inp mono" :class="{ err: fieldErr.serverAddr }" placeholder="IP 或域名" data-testid="node-form-serverAddr" />
          <span v-if="fieldErr.serverAddr" class="fld-help err">{{ fieldErr.serverAddr }}</span>
        </div>
        <div class="fld">
          <label for="nd-port">服务器端口</label>
          <input id="nd-port" v-model="port" class="inp mono" :class="{ err: fieldErr.serverPort }" inputmode="numeric" data-testid="node-form-serverPort" />
          <span v-if="fieldErr.serverPort" class="fld-help err">{{ fieldErr.serverPort }}</span>
        </div>
      </div>
      <div class="fld">
        <label for="nd-token">token</label>
        <input id="nd-token" v-model="form.token" class="inp mono" :class="{ err: fieldErr.token }" type="password" placeholder="frps 的 auth.token" autocomplete="off" data-testid="node-form-token" />
        <span v-if="fieldErr.token" class="fld-help err">{{ fieldErr.token }}</span>
      </div>
      <div class="fld">
        <label for="nd-user">user</label>
        <input id="nd-user" v-model="form.user" class="inp mono" :class="{ err: fieldErr.user }" placeholder="可选" />
        <span class="fld-help" :class="{ err: fieldErr.user }">{{ fieldErr.user ?? '可选，frp 会把它加在代理名前面' }}</span>
      </div>
      <div class="fld">
        <span class="lbl">协议</span>
        <Options v-model="protocol" :options="protocolOptions" mono label="协议" />
        <span v-if="fieldErr.protocol" class="fld-help err">{{ fieldErr.protocol }}</span>
      </div>
      <div class="sw-line">
        <div class="set-txt"><b>TLS</b><small>frp 默认开启</small></div>
        <Switch v-model="tlsOn" label="TLS" />
      </div>
      <div class="fld">
        <label for="nd-proxy">代理地址</label>
        <input id="nd-proxy" v-model="form.proxyURL" class="inp mono" :class="{ err: fieldErr.proxyURL }" placeholder="可选" />
        <span class="fld-help" :class="{ err: fieldErr.proxyURL }">{{ fieldErr.proxyURL ?? '可选，通过 http:// 或 socks5:// 代理连接节点' }}</span>
      </div>
      <div class="probe">
        <button type="button" class="btn ghost sm" :disabled="probing || !form.serverAddr || !port" @click="test">
          <Icon :name="probing ? 'refresh' : 'zap'" size="s" :class="{ spin: probing }" />测试连接
        </button>
        <Transition name="fade">
          <span v-if="probe" class="probe-res" :class="{ bad: !probe.ok }" data-testid="node-form-probe">
            <Icon :name="probe.ok ? 'check' : 'alert'" size="s" />{{ probe.text }}
          </span>
        </Transition>
      </div>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <button type="button" class="btn quiet sm" @click="emit('update:show', false)">取消</button>
      <button type="button" class="btn pri sm" :disabled="saving" data-testid="node-form-save" @click="save">
        <Icon :name="saving ? 'refresh' : 'check'" size="s" :class="{ spin: saving }" />保存
      </button>
    </template>
  </Drawer>
</template>

<style>
.probe {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.probe-res {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  font-weight: 800;
  color: var(--ok-text);
}
.probe-res.bad {
  color: var(--bad-text);
}
</style>
