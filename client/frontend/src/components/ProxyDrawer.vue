<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCheckbox,
  NDrawer,
  NDrawerContent,
  NDynamicTags,
  NForm,
  NFormItem,
  NInput,
  NInputGroup,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  useMessage,
} from 'naive-ui'
import type { Proxy } from '../api'
import { ProfileService } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, fieldErrorsFor, fieldErrorsOf, type AppError } from '../errors'
import { hasRemotePort, isDomainType, isP2PType, proxyTypeHints, proxyTypes } from '../labels'
import { saveFeedback } from './saveFeedback'
import { clone } from '../util'

const props = defineProps<{ show: boolean; index: number }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const profiles = useProfilesStore()
const message = useMessage()

const empty = (): Proxy => ({
  id: '',
  name: '',
  type: 'tcp',
  enabled: true,
  localIP: '127.0.0.1',
  localPort: 0,
  remotePort: 0,
  customDomains: [],
  subdomain: '',
  secretKey: '',
  allowUsers: [],
  useEncryption: false,
  useCompression: false,
})

const form = ref<Proxy>(empty())
const error = ref<AppError | null>(null)
const saving = ref(false)
const editIndex = ref(-1) // 保存时这一项在列表里的下标，用来对应字段错误

watch(
  () => props.show,
  (show) => {
    if (!show) return
    error.value = null
    const src = props.index >= 0 ? profiles.current?.proxies?.[props.index] : null
    form.value = src ? { ...empty(), ...clone(src) } : empty()
    editIndex.value = props.index >= 0 ? props.index : profiles.indexForNew('proxies')
  },
)

const fieldErr = computed(() => fieldErrorsFor(error.value, 'proxies', editIndex.value))
// 不能挂到本表单某一项上的错误（例如其他代理的问题），放到顶部提示
const otherError = computed(() => {
  if (!error.value) return ''
  const fields = fieldErrorsOf(error.value)
  if (fields.length && Object.keys(fieldErr.value).length === fields.length) return ''
  return describeError(error.value)
})
const status = (k: string) => (fieldErr.value[k] ? 'error' : undefined)
const typeOptions = proxyTypes.map((t) => ({ label: t, value: t }))
const port = (v: number | null) => (v ?? 0) as number

async function genSecret() {
  form.value.secretKey = await ProfileService.GenerateSecret()
}

async function save() {
  saving.value = true
  error.value = null
  const p = clone(form.value)
  if (!hasRemotePort(p.type)) p.remotePort = 0
  if (!isDomainType(p.type)) {
    p.customDomains = []
    p.subdomain = ''
  }
  if (!isP2PType(p.type)) {
    p.secretKey = ''
    p.allowUsers = []
  }
  try {
    const res = await profiles.upsert('proxies', p, props.index)
    saveFeedback(message, res)
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
    <n-drawer-content :title="index >= 0 ? '编辑代理' : '添加代理'" closable>
      <n-alert v-if="otherError" type="error" :show-icon="false" style="margin-bottom: 12px" data-testid="proxy-form-error">
        {{ otherError }}
      </n-alert>
      <n-form label-placement="left" label-width="90" :show-require-mark="false" size="small">
        <n-form-item label="名称" :validation-status="status('name')" :feedback="fieldErr.name">
          <n-input v-model:value="form.name" placeholder="例如 ssh、web" maxlength="50" data-testid="proxy-form-name" />
        </n-form-item>
        <n-form-item label="类型" :feedback="proxyTypeHints[form.type]">
          <n-select v-model:value="form.type" :options="typeOptions" data-testid="proxy-form-type" />
        </n-form-item>
        <n-form-item label="本地地址" :validation-status="status('localIP')" :feedback="fieldErr.localIP">
          <n-input v-model:value="form.localIP" placeholder="127.0.0.1" data-testid="proxy-form-localIP" />
        </n-form-item>
        <n-form-item label="本地端口" :validation-status="status('localPort')" :feedback="fieldErr.localPort">
          <n-input-number
            :value="form.localPort || null"
            :min="1"
            :max="65535"
            :show-button="false"
            placeholder="1–65535"
            style="width: 100%"
            data-testid="proxy-form-localPort"
            @update:value="(v: number | null) => (form.localPort = port(v))"
          />
        </n-form-item>
        <n-form-item
          v-if="hasRemotePort(form.type)"
          label="远程端口"
          :validation-status="status('remotePort')"
          :feedback="fieldErr.remotePort ?? '留空表示由节点分配'"
        >
          <n-input-number
            :value="form.remotePort || null"
            :min="1"
            :max="65535"
            :show-button="false"
            placeholder="留空自动分配"
            style="width: 100%"
            data-testid="proxy-form-remotePort"
            @update:value="(v: number | null) => (form.remotePort = port(v))"
          />
        </n-form-item>
        <template v-if="isDomainType(form.type)">
          <n-form-item label="自定义域名" :validation-status="status('customDomains')" :feedback="fieldErr.customDomains">
            <n-dynamic-tags v-model:value="form.customDomains as string[]" data-testid="proxy-form-customDomains" />
          </n-form-item>
          <n-form-item label="子域名" :validation-status="status('subdomain')" :feedback="fieldErr.subdomain ?? '需要节点设置了 subDomainHost'">
            <n-input v-model:value="form.subdomain" placeholder="可选" data-testid="proxy-form-subdomain" />
          </n-form-item>
        </template>
        <template v-if="isP2PType(form.type)">
          <n-form-item label="密钥" :validation-status="status('secretKey')" :feedback="fieldErr.secretKey ?? '访问方要填同一个密钥；留空时自动生成'">
            <n-input-group>
              <n-input v-model:value="form.secretKey" type="password" show-password-on="click" placeholder="留空自动生成" data-testid="proxy-form-secretKey" />
              <n-button @click="genSecret">生成</n-button>
            </n-input-group>
          </n-form-item>
          <n-form-item label="允许的用户" :validation-status="status('allowUsers')" :feedback="fieldErr.allowUsers ?? '填 * 允许所有用户；留空只允许自己'">
            <n-dynamic-tags v-model:value="form.allowUsers as string[]" />
          </n-form-item>
        </template>
        <n-form-item label="传输">
          <n-space>
            <n-checkbox v-model:checked="form.useEncryption">加密</n-checkbox>
            <n-checkbox v-model:checked="form.useCompression">压缩</n-checkbox>
          </n-space>
        </n-form-item>
        <n-form-item label="启用">
          <n-switch v-model:value="form.enabled" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space>
          <n-button @click="emit('update:show', false)">取消</n-button>
          <n-button type="primary" :loading="saving" data-testid="proxy-form-save" @click="save">保存</n-button>
        </n-space>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>
