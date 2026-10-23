<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { NButton, NDropdown, NForm, NFormItem, NInput, NModal, NText, NTooltip, useDialog, useMessage } from 'naive-ui'
import { useAccountStore } from '../stores/account'
import { codeText, describeError, toAppError } from '../errors'
import { checkPassword } from '../invite'
import { syncStateLabels } from '../labels'

const account = useAccountStore()
const message = useMessage()
const dialog = useDialog()

const st = computed(() => account.sync)
const stateLabel = computed(() => syncStateLabels[st.value.state] ?? st.value.state)
const stateTitle = computed(() => {
  const s = st.value
  const parts: string[] = []
  if (s.lastSyncAt) parts.push(`上次同步：${new Date(s.lastSyncAt).toLocaleString()}`)
  if (s.error) parts.push(codeText(s.error.code, s.error.details, s.error.message))
  if (s.state === 'offline') parts.push('本机的配置仍可编辑和启动，网络恢复后自动同步')
  return parts.join('\n')
})

const menu = [
  { label: '立即同步', key: 'sync' },
  { label: '修改密码', key: 'password' },
  { type: 'divider', key: 'd' },
  { label: '退出登录', key: 'logout' },
]

async function onMenu(key: string) {
  if (key === 'sync') {
    account.syncNow().catch((e) => message.error(describeError(e)))
  } else if (key === 'password') {
    openChange()
  } else if (key === 'logout') {
    await confirmLogout()
  }
}

async function confirmLogout() {
  let info = { pending: 0, runningCloud: '' }
  try {
    info = await account.logoutInfo()
  } catch {}
  const lines = ['退出后会清除本机缓存的云端配置（本地配置保留），下次启动不会自动登录。']
  if (info.pending > 0) lines.push(`还有 ${info.pending} 份配置的修改没有同步到云端，退出前会尝试上传，失败的修改会丢失。`)
  if (info.runningCloud) lines.push(`正在运行的云端配置"${info.runningCloud}"会被停止。`)
  dialog.warning({
    title: '退出登录',
    content: lines.join('\n'),
    positiveText: '退出登录',
    negativeText: '取消',
    style: 'white-space: pre-line',
    onPositiveClick: async () => {
      try {
        await account.logout()
        message.success('已退出登录')
      } catch (e) {
        message.error(describeError(e))
      }
    },
  })
}

// ---- 修改密码 ----
const change = reactive({ show: false, old: '', pw: '', pw2: '', error: '', busy: false })
function openChange() {
  Object.assign(change, { show: true, old: '', pw: '', pw2: '', error: '', busy: false })
}
async function submitChange() {
  const p = checkPassword(change.pw)
  if (!change.old) change.error = '请输入当前密码'
  else if (p) change.error = { required: '请输入新密码', too_short: '新密码至少 8 位', too_long: '新密码最多 128 位' }[p]
  else if (change.pw !== change.pw2) change.error = '两次输入的新密码不一致'
  else change.error = ''
  if (change.error) return false
  change.busy = true
  try {
    await account.changePassword(change.old, change.pw)
    change.show = false
    message.success('密码已修改，其他设备上的登录已失效')
  } catch (e) {
    const ae = toAppError(e)
    change.error = ae.code === 'invalid_credentials' ? '当前密码不正确' : describeError(ae)
  } finally {
    change.busy = false
  }
  return false
}
</script>

<template>
  <div class="account" data-testid="account-area" :data-logged-in="account.loggedIn ? '1' : '0'">
    <template v-if="account.loggedIn">
      <n-dropdown trigger="click" :options="menu" @select="onMenu">
        <div class="who" data-testid="account-menu">
          <span class="avatar">{{ account.nickname.slice(0, 1).toUpperCase() || '?' }}</span>
          <span class="name" data-testid="account-nickname">{{ account.nickname }}</span>
        </div>
      </n-dropdown>
      <n-tooltip :disabled="!stateTitle" placement="right">
        <template #trigger>
          <div class="sync" :class="st.state" data-testid="sync-status" :data-state="st.state">
            <span class="dot" />
            <span>{{ stateLabel }}</span>
          </div>
        </template>
        <span style="white-space: pre-line">{{ stateTitle }}</span>
      </n-tooltip>
      <div v-if="st.pending > 0" class="sub" data-testid="sync-pending">{{ st.pending }} 项待同步</div>
      <div v-if="st.conflicts > 0" class="sub warn" data-testid="sync-conflicts">{{ st.conflicts }} 个冲突待处理</div>
    </template>
    <template v-else>
      <n-text depth="3" style="font-size: 12px">未登录</n-text>
      <n-button size="small" type="primary" secondary block style="margin-top: 6px" data-testid="account-login" @click="account.openAuth('login')">
        登录 / 注册
      </n-button>
    </template>

    <n-modal
      v-model:show="change.show"
      preset="dialog"
      title="修改密码"
      positive-text="修改"
      negative-text="取消"
      :loading="change.busy"
      :on-positive-click="submitChange"
    >
      <n-form label-placement="top" style="margin-top: 8px">
        <n-form-item label="当前密码">
          <n-input v-model:value="change.old" type="password" show-password-on="click" data-testid="cp-old" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="change.pw" type="password" show-password-on="click" placeholder="至少 8 位" data-testid="cp-new" />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input v-model:value="change.pw2" type="password" show-password-on="click" data-testid="cp-new2" />
        </n-form-item>
      </n-form>
      <n-text v-if="change.error" type="error" data-testid="cp-error">{{ change.error }}</n-text>
    </n-modal>
  </div>
</template>

<style scoped>
.account {
  padding: 10px 12px;
  border-top: 1px solid rgba(128, 128, 128, 0.2);
}
.who {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  border-radius: 6px;
  padding: 2px;
}
.who:hover {
  background: rgba(128, 128, 128, 0.1);
}
.avatar {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #18a058;
  color: #fff;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  flex-shrink: 0;
}
.name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
}
.sync {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  margin-top: 6px;
  opacity: 0.85;
}
.sync .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #999;
}
.sync.synced .dot {
  background: #18a058;
}
.sync.syncing .dot {
  background: #2080f0;
}
.sync.offline .dot,
.sync.error .dot {
  background: #f0a020;
}
.sync.outdated .dot {
  background: #d03050;
}
.sub {
  font-size: 12px;
  opacity: 0.7;
  margin-top: 2px;
}
.sub.warn {
  color: #d03050;
  opacity: 1;
}
</style>
