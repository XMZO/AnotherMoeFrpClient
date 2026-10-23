<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NDrawer,
  NDrawerContent,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NText,
  useMessage,
} from 'naive-ui'
import type { Node } from '../api'
import { TunnelService } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { codeText, describeError, fieldErrorsFor, fieldErrorsOf, type AppError } from '../errors'
import { protocols } from '../labels'
import { saveFeedback } from './saveFeedback'
import { clone } from '../util'

const props = defineProps<{ show: boolean; index: number }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const profiles = useProfilesStore()
const message = useMessage()

const empty = (): Node => ({ id: '', remark: '', serverAddr: '', serverPort: 7000, token: '', user: '', protocol: '', tls: null, proxyURL: '' })

const form = ref<Node>(empty())
const error = ref<AppError | null>(null)
const saving = ref(false)
const editIndex = ref(-1)
const probe = ref('')
const probing = ref(false)

watch(
  () => props.show,
  (show) => {
    if (!show) return
    error.value = null
    probe.value = ''
    const src = props.index >= 0 ? profiles.current?.nodes?.[props.index] : null
    form.value = src ? { ...empty(), ...clone(src) } : empty()
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
const status = (k: string) => (fieldErr.value[k] ? 'error' : undefined)
const protocolOptions = protocols.map((p) => ({ label: p === 'tcp' ? 'tcp（默认）' : p, value: p }))
// TLS：nil 表示跟随 frp 默认（开启），所以开关打开时存 null，关闭时存 false
const tlsOn = computed({
  get: () => form.value.tls !== false,
  set: (v: boolean) => (form.value.tls = v ? null : false),
})

async function test() {
  probing.value = true
  probe.value = ''
  try {
    const r = await TunnelService.PingNode(clone(form.value))
    probe.value = r.ok ? `TCP 连接成功，${r.latencyMs} ms` : codeText(r.code, { target: r.target }, r.message)
  } catch (e) {
    probe.value = describeError(e)
  } finally {
    probing.value = false
  }
}

async function save() {
  saving.value = true
  error.value = null
  const n = clone(form.value)
  if (n.protocol === 'tcp') n.protocol = ''
  try {
    saveFeedback(message, await profiles.upsert('nodes', n, props.index))
    emit('update:show', false)
  } catch (e) {
    error.value = e as AppError
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-drawer :show="show" :width="460" placement="right" @update:show="(v: boolean) => emit('update:show', v)">
    <n-drawer-content :title="index >= 0 ? '编辑节点' : '添加节点'" closable>
      <n-alert v-if="otherError" type="error" :show-icon="false" style="margin-bottom: 12px" data-testid="node-form-error">
        {{ otherError }}
      </n-alert>
      <n-form label-placement="left" label-width="90" size="small">
        <n-form-item label="备注" :validation-status="status('remark')" :feedback="fieldErr.remark">
          <n-input v-model:value="form.remark" placeholder="例如 香港节点" maxlength="50" data-testid="node-form-remark" />
        </n-form-item>
        <n-form-item label="服务器地址" :validation-status="status('serverAddr')" :feedback="fieldErr.serverAddr">
          <n-input v-model:value="form.serverAddr" placeholder="IP 或域名" data-testid="node-form-serverAddr" />
        </n-form-item>
        <n-form-item label="服务器端口" :validation-status="status('serverPort')" :feedback="fieldErr.serverPort">
          <n-input-number
            :value="form.serverPort || null"
            :min="1"
            :max="65535"
            :show-button="false"
            style="width: 100%"
            data-testid="node-form-serverPort"
            @update:value="(v: number | null) => (form.serverPort = v ?? 0)"
          />
        </n-form-item>
        <n-form-item label="token" :validation-status="status('token')" :feedback="fieldErr.token">
          <n-input v-model:value="form.token" type="password" show-password-on="click" placeholder="frps 的 auth.token" data-testid="node-form-token" />
        </n-form-item>
        <n-form-item label="user" :validation-status="status('user')" :feedback="fieldErr.user ?? '可选，frp 会把它加在代理名前面'">
          <n-input v-model:value="form.user" placeholder="可选" />
        </n-form-item>
        <n-form-item label="协议" :validation-status="status('protocol')" :feedback="fieldErr.protocol">
          <n-select
            :value="form.protocol || 'tcp'"
            :options="protocolOptions"
            @update:value="(v: string) => (form.protocol = v === 'tcp' ? '' : v)"
          />
        </n-form-item>
        <n-form-item label="TLS">
          <n-switch v-model:value="tlsOn" />
          <n-text depth="3" style="margin-left: 8px; font-size: 12px">frp 默认开启</n-text>
        </n-form-item>
        <n-form-item label="代理地址" :validation-status="status('proxyURL')" :feedback="fieldErr.proxyURL ?? '可选，通过 http:// 或 socks5:// 代理连接节点'">
          <n-input v-model:value="form.proxyURL" placeholder="可选" />
        </n-form-item>
        <n-form-item label=" ">
          <n-button size="small" :loading="probing" :disabled="!form.serverAddr || !form.serverPort" @click="test">测试连接</n-button>
          <n-text style="margin-left: 8px; font-size: 12px" data-testid="node-form-probe">{{ probe }}</n-text>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space>
          <n-button @click="emit('update:show', false)">取消</n-button>
          <n-button type="primary" :loading="saving" data-testid="node-form-save" @click="save">保存</n-button>
        </n-space>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>
