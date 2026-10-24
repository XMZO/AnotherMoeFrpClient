<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Drawer from '../ui/Drawer.vue'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import Options from '../ui/Options.vue'
import { message } from '../ui/feedback'
import type { Visitor } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { describeError, fieldErrorsFor, fieldErrorsOf, type AppError } from '../errors'
import { visitorTypes } from '../labels'
import { saveFeedback } from './saveFeedback'
import { clone, portValue } from '../util'

const props = defineProps<{ show: boolean; index: number }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const profiles = useProfilesStore()

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
const bindPort = ref('')
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
    bindPort.value = form.value.bindPort ? String(form.value.bindPort) : ''
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
const typeOptions = visitorTypes.map((t) => ({ value: t as string, label: t }))
// xtcp 打洞失败时可以回退到同一配置里的某个 stcp 访客
const fallbackOptions = computed(() => (profiles.current?.visitors ?? []).filter((v) => v.type === 'stcp' && v.name !== form.value.name).map((v) => v.name))

async function save() {
  saving.value = true
  error.value = null
  const v = clone(form.value)
  v.bindPort = portValue(bindPort.value)
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
  <Drawer
    :show="show"
    :title="index >= 0 ? '编辑访客' : '添加访客'"
    subtitle="在本机监听一个端口，访问别人的 P2P 代理"
    icon="radar"
    tone="gold"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <form @submit.prevent="save">
      <div v-if="otherError" class="alert bad" style="margin-bottom: 14px"><Icon name="alert" size="s" /><span>{{ otherError }}</span></div>
      <div class="fld">
        <label for="vs-name">名称</label>
        <input id="vs-name" v-model="form.name" class="inp" :class="{ err: fieldErr.name }" maxlength="50" placeholder="本机上的名字，例如 visit-rdp" />
        <span v-if="fieldErr.name" class="fld-help err">{{ fieldErr.name }}</span>
      </div>
      <div class="fld">
        <span class="lbl">类型</span>
        <Options v-model="form.type" :options="typeOptions" mono label="访客类型" />
      </div>
      <div class="grid2 eq">
        <div class="fld">
          <label for="vs-sn">目标代理名</label>
          <input id="vs-sn" v-model="form.serverName" class="inp mono" :class="{ err: fieldErr.serverName }" placeholder="对方的代理名" />
        </div>
        <div class="fld">
          <label for="vs-su">目标用户</label>
          <input id="vs-su" v-model="form.serverUser" class="inp mono" :class="{ err: fieldErr.serverUser }" placeholder="可选" />
        </div>
      </div>
      <p class="fld-help" :class="{ err: fieldErr.serverName || fieldErr.serverUser }" style="margin: -8px 0 14px">
        {{ fieldErr.serverName ?? fieldErr.serverUser ?? '目标代理名是对方 stcp / xtcp / sudp 代理的名称；目标用户是对方节点设置里的 user，留空表示和自己相同' }}
      </p>
      <div class="fld">
        <label for="vs-key">密钥</label>
        <input id="vs-key" v-model="form.secretKey" class="inp mono" :class="{ err: fieldErr.secretKey }" type="password" placeholder="与对方代理的密钥相同" autocomplete="off" />
        <span v-if="fieldErr.secretKey" class="fld-help err">{{ fieldErr.secretKey }}</span>
      </div>
      <div class="grid2">
        <div class="fld">
          <label for="vs-ba">绑定地址</label>
          <input id="vs-ba" v-model="form.bindAddr" class="inp mono" :class="{ err: fieldErr.bindAddr }" placeholder="127.0.0.1" />
          <span v-if="fieldErr.bindAddr" class="fld-help err">{{ fieldErr.bindAddr }}</span>
        </div>
        <div class="fld">
          <label for="vs-bp">绑定端口</label>
          <input id="vs-bp" v-model="bindPort" class="inp mono" :class="{ err: fieldErr.bindPort }" inputmode="numeric" placeholder="例如 13389" />
          <span v-if="fieldErr.bindPort" class="fld-help err">{{ fieldErr.bindPort }}</span>
        </div>
      </div>
      <Transition name="collapse">
        <div v-if="form.type === 'xtcp'" class="fld">
          <label for="vs-fb">回退到</label>
          <select id="vs-fb" v-model="form.fallbackTo" class="inp">
            <option value="">不回退</option>
            <option v-for="n in fallbackOptions" :key="n" :value="n">{{ n }}</option>
          </select>
          <span class="fld-help" :class="{ err: fieldErr.fallbackTo }">{{ fieldErr.fallbackTo ?? '打洞失败时改用同一配置里的这个 stcp 访客' }}</span>
        </div>
      </Transition>
      <div class="sw-line">
        <div class="set-txt"><b>启用</b></div>
        <Switch v-model="form.enabled" label="启用" />
      </div>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <button type="button" class="btn quiet sm" @click="emit('update:show', false)">取消</button>
      <button type="button" class="btn pri sm" :disabled="saving" @click="save"><Icon :name="saving ? 'refresh' : 'check'" size="s" :class="{ spin: saving }" />保存</button>
    </template>
  </Drawer>
</template>
