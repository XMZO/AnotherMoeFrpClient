<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NSwitch, useMessage } from 'naive-ui'
import type { Visitor } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, fieldErrorsFor, fieldErrorsOf, type AppError } from '../errors'
import { visitorTypes } from '../labels'
import { saveFeedback } from './saveFeedback'
import { clone } from '../util'

const props = defineProps<{ show: boolean; index: number }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const profiles = useProfilesStore()
const message = useMessage()

const empty = (): Visitor => ({
  id: '',
  name: '',
  type: 'stcp',
  enabled: true,
  serverName: '',
  serverUser: '',
  secretKey: '',
  bindAddr: '127.0.0.1',
  bindPort: 0,
  fallbackTo: '',
})

const form = ref<Visitor>(empty())
const error = ref<AppError | null>(null)
const saving = ref(false)
const editIndex = ref(-1)

watch(
  () => props.show,
  (show) => {
    if (!show) return
    error.value = null
    const src = props.index >= 0 ? profiles.current?.visitors?.[props.index] : null
    form.value = src ? { ...empty(), ...clone(src) } : empty()
    editIndex.value = props.index >= 0 ? props.index : profiles.indexForNew('visitors')
  },
)

const fieldErr = computed(() => fieldErrorsFor(error.value, 'visitors', editIndex.value))
const otherError = computed(() => {
  if (!error.value) return ''
  const fields = fieldErrorsOf(error.value)
  if (fields.length && Object.keys(fieldErr.value).length === fields.length) return ''
  return describeError(error.value)
})
const status = (k: string) => (fieldErr.value[k] ? 'error' : undefined)
const typeOptions = visitorTypes.map((t) => ({ label: t, value: t }))
// xtcp 打洞失败时可以回退到同一配置里的某个 stcp 访客
const fallbackOptions = computed(() => [
  { label: '不回退', value: '' },
  ...(profiles.current?.visitors ?? [])
    .filter((v) => v.type === 'stcp' && v.name !== form.value.name)
    .map((v) => ({ label: v.name, value: v.name })),
])

async function save() {
  saving.value = true
  error.value = null
  const v = clone(form.value)
  if (v.type !== 'xtcp') v.fallbackTo = ''
  try {
    saveFeedback(message, await profiles.upsert('visitors', v, props.index))
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
    <n-drawer-content :title="index >= 0 ? '编辑访客' : '添加访客'" closable>
      <n-alert v-if="otherError" type="error" :show-icon="false" style="margin-bottom: 12px">{{ otherError }}</n-alert>
      <n-form label-placement="left" label-width="90" size="small">
        <n-form-item label="名称" :validation-status="status('name')" :feedback="fieldErr.name">
          <n-input v-model:value="form.name" maxlength="50" placeholder="本机上的名字，例如 visit-rdp" />
        </n-form-item>
        <n-form-item label="类型">
          <n-select v-model:value="form.type" :options="typeOptions" />
        </n-form-item>
        <n-form-item label="目标代理名" :validation-status="status('serverName')" :feedback="fieldErr.serverName ?? '对方 stcp / xtcp / sudp 代理的名称'">
          <n-input v-model:value="form.serverName" />
        </n-form-item>
        <n-form-item label="目标用户" :validation-status="status('serverUser')" :feedback="fieldErr.serverUser ?? '对方节点设置里的 user，留空表示和自己相同'">
          <n-input v-model:value="form.serverUser" placeholder="可选" />
        </n-form-item>
        <n-form-item label="密钥" :validation-status="status('secretKey')" :feedback="fieldErr.secretKey ?? '与对方代理的密钥相同'">
          <n-input v-model:value="form.secretKey" type="password" show-password-on="click" />
        </n-form-item>
        <n-form-item label="绑定地址" :validation-status="status('bindAddr')" :feedback="fieldErr.bindAddr">
          <n-input v-model:value="form.bindAddr" placeholder="127.0.0.1" />
        </n-form-item>
        <n-form-item label="绑定端口" :validation-status="status('bindPort')" :feedback="fieldErr.bindPort">
          <n-input-number
            :value="form.bindPort || null"
            :min="1"
            :max="65535"
            :show-button="false"
            style="width: 100%"
            @update:value="(v: number | null) => (form.bindPort = v ?? 0)"
          />
        </n-form-item>
        <n-form-item v-if="form.type === 'xtcp'" label="回退到" :validation-status="status('fallbackTo')" :feedback="fieldErr.fallbackTo ?? '打洞失败时改用这个 stcp 访客'">
          <n-select v-model:value="form.fallbackTo" :options="fallbackOptions" />
        </n-form-item>
        <n-form-item label="启用">
          <n-switch v-model:value="form.enabled" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space>
          <n-button @click="emit('update:show', false)">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </n-space>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>
