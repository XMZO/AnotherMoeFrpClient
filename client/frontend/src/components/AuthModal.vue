<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NButton, NCheckbox, NForm, NFormItem, NInput, NModal, NTabPane, NTabs, NText, useMessage } from 'naive-ui'
import { useAccountStore, type AuthTab } from '../stores/account'
import { describeError, fieldErrorsOf, reasonMessages, toAppError } from '../errors'
import { checkInviteCode, checkNickname, checkPassword, normalizeInviteCode } from '../invite'

const account = useAccountStore()
const message = useMessage()
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
  <n-modal
    v-model:show="account.authDialog.show"
    preset="card"
    title="账号"
    style="width: 420px"
    :mask-closable="!busy"
    data-testid="auth-modal"
  >
    <n-tabs v-model:value="tab" type="line">
      <n-tab-pane name="login" tab="登录">
        <n-form label-placement="top" @submit.prevent="doLogin">
          <n-form-item label="昵称">
            <n-input v-model:value="login.nickname" placeholder="昵称" data-testid="login-nickname" @keyup.enter="doLogin" />
          </n-form-item>
          <n-form-item label="密码">
            <n-input
              v-model:value="login.password"
              type="password"
              show-password-on="click"
              placeholder="密码"
              data-testid="login-password"
              @keyup.enter="doLogin"
            />
          </n-form-item>
          <n-checkbox v-model:checked="login.keep" data-testid="login-keep">保持登录</n-checkbox>
          <n-text depth="3" style="display: block; font-size: 12px; margin-top: 4px">
            只在 Windows 凭据管理器里保存登录令牌，不保存密码。
          </n-text>
        </n-form>
        <n-button type="primary" block style="margin-top: 16px" :loading="busy" data-testid="login-submit" @click="doLogin">登录</n-button>
      </n-tab-pane>

      <n-tab-pane name="register" tab="注册">
        <n-form label-placement="top">
          <n-form-item label="昵称" :feedback="regTouched ? regErrors.nickname : ''" :validation-status="regTouched && regErrors.nickname ? 'error' : undefined">
            <n-input v-model:value="reg.nickname" placeholder="3–20 位字母、数字或下划线" maxlength="20" data-testid="reg-nickname" />
          </n-form-item>
          <n-form-item label="密码" :feedback="regTouched ? regErrors.password : ''" :validation-status="regTouched && regErrors.password ? 'error' : undefined">
            <n-input v-model:value="reg.password" type="password" show-password-on="click" placeholder="至少 8 位" data-testid="reg-password" />
          </n-form-item>
          <n-form-item label="确认密码" :feedback="regTouched ? regErrors.password2 : ''" :validation-status="regTouched && regErrors.password2 ? 'error' : undefined">
            <n-input v-model:value="reg.password2" type="password" show-password-on="click" data-testid="reg-password2" />
          </n-form-item>
          <n-form-item
            label="邀请码"
            :feedback="inviteFeedback"
            :validation-status="inviteFeedback ? 'error' : reg.invite.trim() ? 'success' : undefined"
          >
            <n-input v-model:value="reg.invite" placeholder="FRPT-XXXX-XXXX" maxlength="20" data-testid="reg-invite" @keyup.enter="doRegister" />
          </n-form-item>
          <div class="invite-state" :data-state="inviteState" data-testid="reg-invite-state" />
        </n-form>
        <n-button type="primary" block :loading="busy" data-testid="reg-submit" @click="doRegister">注册</n-button>
      </n-tab-pane>

      <n-tab-pane name="reset" tab="重置密码">
        <n-text depth="3" style="display: block; font-size: 12px; margin-bottom: 8px">重置码由管理员提供，24 小时内有效。重置后所有设备都需要重新登录。</n-text>
        <n-form label-placement="top">
          <n-form-item label="昵称">
            <n-input v-model:value="reset.nickname" data-testid="reset-nickname" />
          </n-form-item>
          <n-form-item label="重置码">
            <n-input v-model:value="reset.code" placeholder="RST-XXXXX-XXXXX-XXXXX" data-testid="reset-code" />
          </n-form-item>
          <n-form-item label="新密码" :feedback="reset.password ? resetErrors.password : ''" :validation-status="reset.password && resetErrors.password ? 'error' : undefined">
            <n-input v-model:value="reset.password" type="password" show-password-on="click" placeholder="至少 8 位" data-testid="reset-password" />
          </n-form-item>
          <n-form-item label="确认新密码" :feedback="reset.password2 ? resetErrors.password2 : ''" :validation-status="reset.password2 && resetErrors.password2 ? 'error' : undefined">
            <n-input v-model:value="reset.password2" type="password" show-password-on="click" data-testid="reset-password2" @keyup.enter="doReset" />
          </n-form-item>
        </n-form>
        <n-button type="primary" block :loading="busy" data-testid="reset-submit" @click="doReset">重置密码</n-button>
      </n-tab-pane>
    </n-tabs>
    <n-text v-if="error" type="error" style="display: block; margin-top: 12px" data-testid="auth-error">{{ error }}</n-text>
  </n-modal>
</template>

<style scoped>
.invite-state {
  display: none;
}
</style>
