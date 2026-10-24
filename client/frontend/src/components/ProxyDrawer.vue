<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Drawer from '../ui/Drawer.vue'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import Check from '../ui/Check.vue'
import TagInput from '../ui/TagInput.vue'
import { message } from '../ui/feedback'
import type { Proxy } from '../api'
import { ProfileService } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { describeError, fieldErrorsFor, fieldErrorsOf, type AppError } from '../errors'
import { frpTypesOf, hasRemotePort, isDomainType, isP2PType, proxyTypeHints, proxyTypeLabel, proxyTypes } from '../labels'
import { saveFeedback } from './saveFeedback'
import { clone, portValue } from '../util'

const props = defineProps<{ show: boolean; index: number }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
// 选中的是官方节点时，按面板给这个账号的限额提示：不允许的类型不能选，端口范围写在提示里（面板改了实时更新）
const limitText = computed(() => {
  const l = tunnel.limits
  if (!l) return ''
  const parts = [
    l.types.length ? `只允许 ${l.types.join(' / ')}` : '',
    l.ports ? `远程端口 ${l.ports}` : '',
    l.maxProxies ? `最多同时 ${l.maxProxies} 个代理${form.value.withUdp ? '（TCP+UDP 算 2 个）' : ''}` : '',
  ]
  return parts.filter(Boolean).join(' · ')
})

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
  withUdp: false,
})

const form = ref<Proxy>(empty())

// 类型一般只能选一个；TCP 和 UDP 可以同时选（同一个端口两种协议都转发，很多游戏需要），存成 tcp + withUdp。
// 其它组合没有意义（http/https 要本地也是对应协议，P2P 类型还要访问方配套），不开放。
const chipOn = (t: string) => form.value.type === t || (t === 'udp' && form.value.type === 'tcp' && !!form.value.withUdp)
function pickType(t: string) {
  const f = form.value
  const both = f.type === 'tcp' && !!f.withUdp
  if (t === 'udp' && f.type === 'tcp') f.withUdp = !f.withUdp // 加上 / 去掉 UDP
  else if (t === 'tcp' && f.type === 'udp') [f.type, f.withUdp] = ['tcp', true] // 加上 TCP
  else if (t === 'tcp' && both) [f.type, f.withUdp] = ['udp', false] // 去掉 TCP
  else if (f.type !== t) [f.type, f.withUdp] = [t, false]
}
const typeLabel = computed(() => proxyTypeLabel(form.value))
const blockedTypes = computed(() => frpTypesOf(form.value).filter((t) => !tunnel.typeAllowed(t)))
const localPort = ref('')
const remotePort = ref('')
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
    localPort.value = form.value.localPort ? String(form.value.localPort) : ''
    remotePort.value = form.value.remotePort ? String(form.value.remotePort) : ''
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

async function genSecret() {
  try {
    form.value.secretKey = await ProfileService.GenerateSecret()
  } catch (e) {
    message.error(describeError(e))
  }
}

// 名称留空时自动起一个：类型-4 位随机字母数字（例如 tcp-k3f9），和这份配置里别的代理、访客都不重名
function autoName(type: string): string {
  const cur = profiles.current
  const taken = new Set<string>()
  ;(cur?.proxies ?? []).forEach((x, i) => {
    if (i === props.index) return // 正在编辑的这一项自己的旧名字不算
    taken.add(x.name)
    if (x.type === 'tcp' && x.withUdp) taken.add(`${x.name}-udp`)
  })
  ;(cur?.visitors ?? []).forEach((v) => taken.add(v.name))
  const chars = 'abcdefghijkmnpqrstuvwxyz23456789' // 去掉容易看错的 0 o 1 l
  for (;;) {
    let r = ''
    for (let i = 0; i < 4; i++) r += chars[Math.floor(Math.random() * chars.length)]
    const n = `${type}-${r}`
    if (!taken.has(n) && !taken.has(`${n}-udp`)) return n
  }
}

async function save() {
  saving.value = true
  error.value = null
  const p = clone(form.value)
  p.name = p.name.trim() || autoName(p.type)
  p.localPort = portValue(localPort.value)
  p.remotePort = portValue(remotePort.value)
  if (!hasRemotePort(p.type)) p.remotePort = 0
  if (p.type !== 'tcp') p.withUdp = false
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
  <Drawer
    :show="show"
    :title="index >= 0 ? '编辑代理' : '添加代理'"
    :subtitle="profiles.current ? `保存在「${profiles.current.name}」` : undefined"
    icon="tunnel"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <form @submit.prevent="save">
      <div v-if="otherError" class="alert bad" style="margin-bottom: 14px" data-testid="proxy-form-error"><Icon name="alert" size="s" /><span>{{ otherError }}</span></div>
      <div class="fld">
        <label for="px-name">名称</label>
        <input id="px-name" v-model="form.name" class="inp" :class="{ err: fieldErr.name }" placeholder="留空自动生成" maxlength="50" data-testid="proxy-form-name" />
        <span class="fld-help" :class="{ err: fieldErr.name }">{{ fieldErr.name ?? '留空时自动起一个短名字（例如 tcp-k3f9），同一份配置里不会重复' }}</span>
      </div>
      <div class="fld">
        <span class="lbl">类型</span>
        <div class="chips" role="group" aria-label="代理类型（TCP、UDP 可以同时选）" data-testid="proxy-form-type">
          <button
            v-for="t in proxyTypes"
            :key="t"
            type="button"
            class="chip-opt"
            :class="{ on: chipOn(t), na: !tunnel.typeAllowed(t) }"
            :aria-pressed="chipOn(t)"
            :disabled="!tunnel.typeAllowed(t) && !chipOn(t)"
            :title="tunnel.typeAllowed(t) ? undefined : `「${tunnel.limits?.node}」不允许你使用 ${t}，换其他节点可以用`"
            @click="pickType(t)"
          >
            {{ t }}
          </button>
        </div>
        <div v-if="limitText" class="type-hint limit" data-testid="proxy-form-limits"><Icon name="shield" size="xs" /><span>「{{ tunnel.limits?.node }}」对你的限制：{{ limitText }}</span></div>
        <div v-if="blockedTypes.length" class="type-hint bad"><Icon name="alert" size="xs" /><span>「{{ tunnel.limits?.node }}」不允许 {{ blockedTypes.join(' / ') }}，用这个节点时会被拒绝</span></div>
        <Transition name="fade" mode="out-in">
          <div :key="typeLabel" class="type-hint" data-testid="proxy-form-type-hint"><Icon name="info" size="xs" /><span>{{ proxyTypeHints[typeLabel] }}</span></div>
        </Transition>
      </div>
      <div class="grid2">
        <div class="fld">
          <label for="px-lip">本地地址</label>
          <input id="px-lip" v-model="form.localIP" class="inp mono" :class="{ err: fieldErr.localIP }" placeholder="127.0.0.1" data-testid="proxy-form-localIP" />
          <span v-if="fieldErr.localIP" class="fld-help err">{{ fieldErr.localIP }}</span>
        </div>
        <div class="fld">
          <label for="px-lport">本地端口</label>
          <input id="px-lport" v-model="localPort" class="inp mono" :class="{ err: fieldErr.localPort }" inputmode="numeric" placeholder="1–65535" data-testid="proxy-form-localPort" />
          <span v-if="fieldErr.localPort" class="fld-help err">{{ fieldErr.localPort }}</span>
        </div>
      </div>
      <Transition name="collapse">
        <div v-if="hasRemotePort(form.type)" class="fld">
          <label for="px-rport">远程端口</label>
          <input
            id="px-rport"
            v-model="remotePort"
            class="inp mono"
            :class="{ err: fieldErr.remotePort }"
            inputmode="numeric"
            :placeholder="form.withUdp ? (tunnel.limits?.ports ? `必填（可用 ${tunnel.limits.ports}）` : '必填') : tunnel.limits?.ports ? `留空自动分配（可用 ${tunnel.limits.ports}）` : '留空自动分配'"
            data-testid="proxy-form-remotePort"
          />
          <span class="fld-help" :class="{ err: fieldErr.remotePort }">{{ form.withUdp && fieldErr.remotePort && !remotePort ? '要填远程端口：TCP 和 UDP 用同一个端口' : (fieldErr.remotePort ?? (form.withUdp ? 'TCP 和 UDP 用同一个端口，所以要填' : '留空表示由节点分配')) }}</span>
        </div>
      </Transition>
      <Transition name="collapse">
        <div v-if="isDomainType(form.type)">
          <div class="fld">
            <span class="lbl">自定义域名</span>
            <TagInput v-model="form.customDomains as string[]" placeholder="输入域名后回车" label="自定义域名" :invalid="!!fieldErr.customDomains" data-testid="proxy-form-customDomains" />
            <span v-if="fieldErr.customDomains" class="fld-help err">{{ fieldErr.customDomains }}</span>
          </div>
          <div class="fld">
            <label for="px-sub">子域名</label>
            <input id="px-sub" v-model="form.subdomain" class="inp" :class="{ err: fieldErr.subdomain }" placeholder="可选" data-testid="proxy-form-subdomain" />
            <span class="fld-help" :class="{ err: fieldErr.subdomain }">{{ fieldErr.subdomain ?? '需要节点设置了 subDomainHost' }}</span>
          </div>
        </div>
      </Transition>
      <Transition name="collapse">
        <div v-if="isP2PType(form.type)">
          <div class="fld">
            <label for="px-key">密钥</label>
            <div class="inp-group">
              <input id="px-key" v-model="form.secretKey" class="inp mono" :class="{ err: fieldErr.secretKey }" type="password" placeholder="留空自动生成" data-testid="proxy-form-secretKey" />
              <button type="button" class="btn soft sm" style="height: 40px" @click="genSecret"><Icon name="key" size="xs" />生成</button>
            </div>
            <span class="fld-help" :class="{ err: fieldErr.secretKey }">{{ fieldErr.secretKey ?? '访问方要填同一个密钥；留空时自动生成' }}</span>
          </div>
          <div class="fld">
            <span class="lbl">允许的用户</span>
            <TagInput v-model="form.allowUsers as string[]" placeholder="输入用户名后回车" label="允许的用户" :invalid="!!fieldErr.allowUsers" />
            <span class="fld-help" :class="{ err: fieldErr.allowUsers }">{{ fieldErr.allowUsers ?? '填 * 允许所有用户；留空只允许自己' }}</span>
          </div>
        </div>
      </Transition>
      <div class="fld">
        <span class="lbl">传输</span>
        <div class="toggles">
          <Check v-model="form.useEncryption">加密</Check>
          <Check v-model="form.useCompression">压缩</Check>
        </div>
        <span v-if="form.type === 'xtcp' && (form.useEncryption || form.useCompression)" class="fld-help warn" data-testid="proxy-form-xtcp-hint">
          xtcp 打洞后两台电脑直连：访问方的访客也要勾上同样的“加密”“压缩”，否则连不上。通过 P2P 分享出去的会自动对齐
        </span>
      </div>
      <div class="sw-line">
        <div class="set-txt"><b>启用</b><small>关掉后保留配置，但不运行</small></div>
        <Switch v-model="form.enabled" label="启用" />
      </div>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <button type="button" class="btn quiet sm" @click="emit('update:show', false)">取消</button>
      <button type="button" class="btn pri sm" :disabled="saving" data-testid="proxy-form-save" @click="save">
        <Icon :name="saving ? 'refresh' : 'check'" size="s" :class="{ spin: saving }" />保存
      </button>
    </template>
  </Drawer>
</template>

<style>
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.chip-opt {
  height: 32px;
  padding: 0 12px;
  border-radius: 99px;
  border: 1.5px solid var(--line);
  font-family: var(--mono);
  font-size: 11.5px;
  font-weight: 600;
  text-transform: uppercase;
  color: var(--text-2);
  transition: background-color 0.2s, border-color 0.2s, color 0.2s;
}
.chip-opt:hover {
  border-color: color-mix(in oklab, var(--pri) 50%, var(--line));
  color: var(--text);
}
.chip-opt.on {
  background: var(--pri);
  border-color: var(--pri);
  color: #fff;
  animation: boing 0.45s var(--ease-spring);
}
.type-hint {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 4px;
  padding: 9px 12px;
  border-radius: 12px;
  background: var(--pri-soft);
  color: var(--pri-text);
  font-size: 12px;
  font-weight: 700;
  line-height: 1.5;
}
.type-hint .ic {
  margin-top: 2px;
}
/* 选中的官方节点不允许的类型：灰掉、带删除线，不能选 */
.chip-opt.na {
  border-style: dashed;
  color: var(--text-3);
  text-decoration: line-through;
}
.chip-opt.na:disabled {
  cursor: not-allowed;
}
.chip-opt.na.on {
  background: var(--bad-soft);
  border-color: var(--bad);
  color: var(--bad-text);
}
.type-hint.limit {
  background: var(--fill);
  color: var(--text-2);
}
.type-hint.bad {
  background: var(--bad-soft);
  color: var(--bad-text);
}
.toggles {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.toggles .check {
  padding: 7px 14px 7px 9px;
}
</style>
