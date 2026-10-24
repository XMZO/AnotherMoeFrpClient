<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import Check from '../ui/Check.vue'
import Tabs from '../ui/Tabs.vue'
import { message } from '../ui/feedback'
import { useAccountStore, type AuthTab } from '../stores/account'
import { describeError, fieldErrorsOf, reasonMessages, toAppError } from '../errors'
import { checkInviteCode, checkNickname, checkPassword, normalizeInviteCode } from '../invite'
import mascot from '../assets/mascot.png'

const account = useAccountStore()
const busy = ref(false)
const error = ref('')

const login = reactive({ nickname: '', password: '', keep: true })
const reg = reactive({ nickname: '', password: '', password2: '', invite: '' })
const reset = reactive({ nickname: '', code: '', password: '', password2: '' })
const regTouched = ref(false)

const tab = computed({
  get: () => account.authDialog.tab,
  set: (v: AuthTab) => {
    account.authDialog.tab = v
    error.value = ''
  },
})
const tabs: { value: AuthTab; label: string }[] = [
  { value: 'login', label: '登录' },
  { value: 'register', label: '注册' },
  { value: 'reset', label: '重置密码' },
]
const hero = computed(
  () =>
    ({
      login: ['欢迎回来', '登录后同步配置、使用官方节点和分享'],
      register: ['加入 MoeFrp', '注册需要一个邀请码'],
      reset: ['重置密码', '用管理员给你的重置码设置新密码'],
    })[tab.value],
)

watch(
  () => account.authDialog.show,
  (show) => {
    if (!show) return
    error.value = ''
    regTouched.value = false
    login.password = reg.password = reg.password2 = reset.password = reset.password2 = reset.code = ''
    if (account.authDialog.nickname) login.nickname = reset.nickname = account.authDialog.nickname
  },
)

const nicknameText: Record<string, string> = { required: '请输入昵称', invalid: '昵称只能是 3–20 位字母、数字或下划线' }
const passwordText: Record<string, string> = { required: '请输入密码', too_short: '密码至少 8 位', too_long: '密码最多 128 位' }
const inviteText: Record<string, string> = {
  required: '请输入邀请码',
  invalid: '格式不正确，应为 FRPT-XXXX-XXXX（不含 I、L、O、0、1）',
  checksum: '校验位不正确，请检查是否输错',
}

// 注册表单的前端校验（邀请码边输边检查，其余提交时检查）
const inviteState = computed(() => checkInviteCode(reg.invite))
const regErrors = computed(() => {
  const e: Record<string, string> = {}
  const n = checkNickname(reg.nickname.trim())
  if (n) e.nickname = nicknameText[n]
  const p = checkPassword(reg.password)
  if (p) e.password = passwordText[p]
  if (!e.password && reg.password !== reg.password2) e.password2 = '两次输入的密码不一致'
  if (inviteState.value) e.invite = inviteText[inviteState.value]
  return e
})
const inviteFeedback = computed(() => {
  if (!reg.invite.trim()) return regTouched.value ? inviteText.required : ''
  return inviteState.value ? inviteText[inviteState.value] : ''
})

const resetErrors = computed(() => {
  const e: Record<string, string> = {}
  const p = checkPassword(reset.password)
  if (p) e.password = passwordText[p]
  if (!e.password && reset.password !== reset.password2) e.password2 = '两次输入的密码不一致'
  return e
})

// 服务端返回的字段错误（validation_failed）合成一句话
function explain(e: unknown): string {
  const ae = toAppError(e)
  const fields = fieldErrorsOf(ae)
  if (ae.code === 'validation_failed' && fields.length) {
    return fields.map((f) => reasonMessages[f.reason] ?? f.message).join('；')
  }
  return describeError(ae)
}

async function run(fn: () => Promise<void>) {
  busy.value = true
  error.value = ''
  try {
    await fn()
  } catch (e) {
    error.value = explain(e)
  } finally {
    busy.value = false
  }
}

function doLogin() {
  if (!login.nickname.trim() || !login.password) {
    error.value = '请输入昵称和密码'
    return
  }
  return run(async () => {
    await account.login(login.nickname.trim(), login.password, login.keep)
    login.password = ''
    account.authDialog.show = false
    message.success(`已登录：${account.nickname}`)
  })
}

function doRegister() {
  regTouched.value = true
  if (Object.keys(regErrors.value).length) {
    error.value = Object.values(regErrors.value)[0]
    return
  }
  return run(async () => {
    const user = await account.register(reg.nickname.trim(), reg.password, normalizeInviteCode(reg.invite))
    message.success('注册成功，请登录')
    login.nickname = user.nickname
    login.password = ''
    reg.password = reg.password2 = reg.invite = ''
    tab.value = 'login'
  })
}

function doReset() {
  if (!reset.nickname.trim() || !reset.code.trim()) {
    error.value = '请输入昵称和重置码'
    return
  }
  if (Object.keys(resetErrors.value).length) {
    error.value = Object.values(resetErrors.value)[0]
    return
  }
  return run(async () => {
    await account.resetPassword(reset.nickname.trim(), reset.code.trim(), reset.password)
    message.success('密码已重置，请用新密码登录')
    login.nickname = reset.nickname.trim()
    tab.value = 'login'
  })
}
</script>

<template>
  <Modal v-model:show="account.authDialog.show" :width="420" :mask-closable="!busy" :closable="!busy" aria-label="账号" data-testid="auth-modal">
    <div class="auth-hero">
      <img :src="mascot" alt="" />
      <Transition name="fade" mode="out-in">
        <div :key="tab">
          <h2>{{ hero[0] }}</h2>
          <p>{{ hero[1] }}</p>
        </div>
      </Transition>
    </div>
    <Tabs v-model="tab" :options="tabs" full class="auth-tabs" />

    <form v-if="tab === 'login'" class="auth-form" @submit.prevent="doLogin">
      <div class="fld"><label for="lg-nick">昵称</label><input id="lg-nick" v-model="login.nickname" class="inp" placeholder="昵称" autocomplete="username" data-testid="login-nickname" /></div>
      <div class="fld"><label for="lg-pw">密码</label><input id="lg-pw" v-model="login.password" class="inp" type="password" placeholder="密码" autocomplete="current-password" data-testid="login-password" /></div>
      <Check v-model="login.keep" plain data-testid="login-keep">保持登录</Check>
      <p class="fld-help" style="margin: 4px 0 16px">只在 Windows 凭据管理器里保存登录令牌，不保存密码。</p>
      <button type="submit" class="btn pri block" :disabled="busy" data-testid="login-submit"><Icon v-if="busy" name="refresh" size="s" class="spin" />登录</button>
    </form>

    <form v-else-if="tab === 'register'" class="auth-form" @submit.prevent="doRegister">
      <div class="fld">
        <label for="rg-nick">昵称</label>
        <input id="rg-nick" v-model="reg.nickname" class="inp" :class="{ err: regTouched && regErrors.nickname }" placeholder="3–20 位字母、数字或下划线" maxlength="20" autocomplete="username" data-testid="reg-nickname" />
        <span v-if="regTouched && regErrors.nickname" class="fld-help err">{{ regErrors.nickname }}</span>
      </div>
      <div class="grid2 eq">
        <div class="fld">
          <label for="rg-pw">密码</label>
          <input id="rg-pw" v-model="reg.password" class="inp" :class="{ err: regTouched && regErrors.password }" type="password" placeholder="至少 8 位" autocomplete="new-password" data-testid="reg-password" />
        </div>
        <div class="fld">
          <label for="rg-pw2">确认密码</label>
          <input id="rg-pw2" v-model="reg.password2" class="inp" :class="{ err: regTouched && regErrors.password2 }" type="password" autocomplete="new-password" data-testid="reg-password2" />
        </div>
      </div>
      <p v-if="regTouched && (regErrors.password || regErrors.password2)" class="fld-help err" style="margin: -8px 0 12px">{{ regErrors.password ?? regErrors.password2 }}</p>
      <div class="fld">
        <label for="rg-inv">邀请码</label>
        <input
          id="rg-inv"
          v-model="reg.invite"
          class="inp mono"
          :class="{ err: !!inviteFeedback, good: reg.invite.trim() && !inviteState }"
          placeholder="FRPT-XXXX-XXXX"
          maxlength="20"
          data-testid="reg-invite"
        />
        <span class="fld-help" :class="inviteFeedback ? 'err' : reg.invite.trim() ? 'ok' : ''">
          {{ inviteFeedback || (reg.invite.trim() ? '格式正确' : '格式 FRPT-XXXX-XXXX，边输边检查') }}
        </span>
      </div>
      <div class="invite-state" :data-state="inviteState" data-testid="reg-invite-state" />
      <button type="submit" class="btn pri block" :disabled="busy" data-testid="reg-submit"><Icon v-if="busy" name="refresh" size="s" class="spin" />注册</button>
    </form>

    <form v-else class="auth-form" @submit.prevent="doReset">
      <div class="alert info" style="margin-bottom: 14px"><Icon name="info" size="s" /><span>重置码由管理员提供，24 小时内有效。重置后所有设备都需要重新登录。</span></div>
      <div class="fld"><label for="rs-nick">昵称</label><input id="rs-nick" v-model="reset.nickname" class="inp" autocomplete="username" data-testid="reset-nickname" /></div>
      <div class="fld"><label for="rs-code">重置码</label><input id="rs-code" v-model="reset.code" class="inp mono" placeholder="RST-XXXXX-XXXXX-XXXXX" data-testid="reset-code" /></div>
      <div class="grid2 eq">
        <div class="fld">
          <label for="rs-pw">新密码</label>
          <input id="rs-pw" v-model="reset.password" class="inp" :class="{ err: reset.password && resetErrors.password }" type="password" placeholder="至少 8 位" autocomplete="new-password" data-testid="reset-password" />
        </div>
        <div class="fld">
          <label for="rs-pw2">确认新密码</label>
          <input id="rs-pw2" v-model="reset.password2" class="inp" :class="{ err: reset.password2 && resetErrors.password2 }" type="password" autocomplete="new-password" data-testid="reset-password2" />
        </div>
      </div>
      <p v-if="(reset.password && resetErrors.password) || (reset.password2 && resetErrors.password2)" class="fld-help err" style="margin: -8px 0 12px">
        {{ resetErrors.password ?? resetErrors.password2 }}
      </p>
      <button type="submit" class="btn pri block" :disabled="busy" data-testid="reset-submit"><Icon v-if="busy" name="refresh" size="s" class="spin" />重置密码</button>
    </form>

    <Transition name="fade">
      <div v-if="error" class="alert bad" style="margin-top: 12px" data-testid="auth-error"><Icon name="alert" size="s" /><span>{{ error }}</span></div>
    </Transition>
  </Modal>
</template>

<style>
.auth-hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 14px 0 12px;
  text-align: center;
}
.auth-hero img {
  width: 68px;
  height: 68px;
  border-radius: 22px;
  box-shadow: 0 0 0 3px var(--surface), 0 12px 28px -10px color-mix(in oklab, var(--pri) 70%, transparent);
  animation: floaty 4s ease-in-out infinite;
}
.auth-hero h2 {
  font-size: 20px;
  font-weight: 900;
}
.auth-hero p {
  font-size: 12.5px;
  color: var(--text-3);
  font-weight: 700;
}
.auth-tabs {
  margin-bottom: 16px;
}
.auth-form {
  animation: fade 0.3s;
}
</style>
