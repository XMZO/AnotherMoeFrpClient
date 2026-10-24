<script setup lang="ts">
import { computed, reactive } from 'vue'
import Icon from '../ui/Icon.vue'
import Menu from '../ui/Menu.vue'
import Modal from '../ui/Modal.vue'
import { dialog, message } from '../ui/feedback'
import { useAccountStore } from '../stores/account'
import { codeText, describeError, toAppError } from '../errors'
import { checkPassword } from '../invite'
import { syncStateLabels } from '../labels'
import { formatTime } from './shareFormat'

const account = useAccountStore()

const st = computed(() => account.sync)
const stateLabel = computed(() => syncStateLabels[st.value.state] ?? st.value.state)
const stateTone = computed(() => {
  if (st.value.conflicts > 0) return 'bad'
  return { synced: 'ok', syncing: 'info', offline: 'warn', error: 'warn', outdated: 'bad' }[st.value.state] ?? ''
})
const lastSync = computed(() => formatTime(st.value.lastSyncAt))
const stateTitle = computed(() => {
  const s = st.value
  const parts: string[] = []
  if (s.lastSyncAt) parts.push(`上次同步：${lastSync.value}`)
  if (s.error) parts.push(codeText(s.error.code, s.error.details, s.error.message))
  if (s.state === 'offline') parts.push('本机的配置仍可编辑和启动，网络恢复后自动同步')
  return parts.join('\n')
})
const initial = computed(() => account.nickname.slice(0, 1).toUpperCase() || '?')

async function onMenu(key: string, close: () => void) {
  close()
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
  if (info.runningCloud) lines.push(`正在运行的云端配置“${info.runningCloud}”会被停止。`)
  dialog.warning({
    title: '退出登录',
    content: lines.join('\n'),
    icon: 'logout',
    positiveText: '退出登录',
    negativeText: '取消',
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
  if (change.error) return
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
}
</script>

<template>
  <div class="acct-area" data-testid="account-area" :data-logged-in="account.loggedIn ? '1' : '0'">
    <template v-if="account.loggedIn">
      <Menu placement="right-end" :width="272" label="账号">
        <template #trigger="{ toggle, open }">
          <button type="button" class="acct" data-testid="account-menu" :aria-expanded="open" :title="stateTitle" @click="toggle">
            <span class="ava">{{ initial }}<span class="ava-dot" :class="stateTone" /></span>
            <span class="acct-txt">
              <b data-testid="account-nickname">{{ account.nickname }}</b>
              <small class="sync" :class="st.state" data-testid="sync-status" :data-state="st.state">{{ stateLabel }}</small>
            </span>
          </button>
        </template>
        <template #default="{ close }">
          <div class="acct-head">
            <span class="ava big">{{ initial }}</span>
            <div>
              <b>{{ account.nickname }}</b>
              <small><span class="bdg" :class="stateTone">{{ stateLabel }}</span><template v-if="lastSync"> · {{ lastSync }}</template></small>
            </div>
          </div>
          <div v-if="st.pending > 0 || st.conflicts > 0 || st.error || st.state === 'offline'" class="acct-stat">
            <div v-if="st.pending > 0"><Icon name="upload" size="xs" />{{ st.pending }} 项待同步</div>
            <div v-if="st.conflicts > 0" class="warn"><Icon name="alert" size="xs" />{{ st.conflicts }} 个冲突待处理</div>
            <div v-if="st.error"><Icon name="info" size="xs" />{{ codeText(st.error.code, st.error.details, st.error.message) }}</div>
            <div v-if="st.state === 'offline'"><Icon name="info" size="xs" />本机的配置仍可编辑和启动，网络恢复后自动同步</div>
          </div>
          <button type="button" role="menuitem" class="menu-item" @click="onMenu('sync', close)"><Icon name="refresh" size="s" />立即同步</button>
          <button type="button" role="menuitem" class="menu-item" @click="onMenu('password', close)"><Icon name="lock" size="s" />修改密码</button>
          <div class="menu-sep" />
          <button type="button" role="menuitem" class="menu-item danger" @click="onMenu('logout', close)"><Icon name="logout" size="s" />退出登录</button>
        </template>
      </Menu>
      <div v-if="st.pending > 0" class="acct-sub" data-testid="sync-pending">{{ st.pending }} 项待同步</div>
      <div v-if="st.conflicts > 0" class="acct-sub warn" data-testid="sync-conflicts">{{ st.conflicts }} 个冲突待处理</div>
    </template>
    <button v-else type="button" class="login-btn" data-testid="account-login" @click="account.openAuth('login')">
      <Icon name="user" /><span>登录</span>
    </button>

    <Modal v-model:show="change.show" title="修改密码" subtitle="修改后其他设备上的登录会失效" icon="lock" :width="420" :mask-closable="!change.busy">
      <form @submit.prevent="submitChange">
        <div class="fld"><label for="cp-old">当前密码</label><input id="cp-old" v-model="change.old" class="inp" type="password" autocomplete="current-password" data-testid="cp-old" /></div>
        <div class="fld"><label for="cp-new">新密码</label><input id="cp-new" v-model="change.pw" class="inp" type="password" placeholder="至少 8 位" autocomplete="new-password" data-testid="cp-new" /></div>
        <div class="fld"><label for="cp-new2">确认新密码</label><input id="cp-new2" v-model="change.pw2" class="inp" type="password" autocomplete="new-password" data-testid="cp-new2" /></div>
        <div v-if="change.error" class="alert bad" data-testid="cp-error">{{ change.error }}</div>
        <button type="submit" hidden />
      </form>
      <template #footer>
        <button type="button" class="btn quiet sm" :disabled="change.busy" @click="change.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="change.busy" @click="submitChange"><Icon v-if="change.busy" name="refresh" size="s" class="spin" />修改</button>
      </template>
    </Modal>
  </div>
</template>

<style>
.acct-area {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 2px;
}
.acct-area > .menu-anchor {
  display: flex;
}
.acct {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  width: 100%;
  padding: 6px;
  border-radius: 16px;
  transition: background-color 0.2s;
}
.acct:hover,
.acct[aria-expanded='true'] {
  background: color-mix(in oklab, var(--pri) 8%, transparent);
}
.ava {
  position: relative;
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  border-radius: 13px;
  background: linear-gradient(140deg, #ffb3d2, #b9a2ff);
  color: #2a1b4f;
  font-weight: 900;
  font-size: 15px;
}
.ava.big {
  width: 44px;
  height: 44px;
  border-radius: 15px;
  font-size: 18px;
}
.ava-dot {
  position: absolute;
  right: -3px;
  bottom: -3px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2.5px solid var(--rail);
  background: var(--text-3);
  transition: background-color 0.3s;
}
.ava-dot.ok {
  background: var(--ok);
}
.ava-dot.info {
  background: var(--info);
  animation: blink 1s ease-in-out infinite;
}
.ava-dot.warn {
  background: var(--warn);
}
.ava-dot.bad {
  background: var(--bad);
}
.acct-txt {
  display: none;
  flex-direction: column;
  line-height: 1.25;
  min-width: 0;
  text-align: left;
}
.acct-txt b {
  font-size: 13.5px;
  font-weight: 800;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.acct-txt small {
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 700;
}
.acct-sub {
  display: none;
  padding: 0 10px;
  font-size: 11.5px;
  font-weight: 700;
  color: var(--text-3);
}
.acct-sub.warn {
  color: var(--bad-text);
}
.login-btn {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  width: 100%;
  padding: 9px 2px 7px;
  border-radius: 16px;
  background: var(--pri);
  color: #fff;
  font-weight: 800;
  font-size: 11.5px;
  box-shadow: 0 8px 18px -8px color-mix(in oklab, var(--pri) 80%, transparent);
  transition: transform 0.2s, background-color 0.2s;
}
.login-btn:hover {
  transform: translateY(-1px);
  background: var(--pri-hover);
}
.acct-head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 10px 12px;
}
.acct-head b {
  display: block;
  font-size: 15px;
  font-weight: 900;
}
.acct-head small {
  display: block;
  margin-top: 2px;
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 600;
}
.acct-stat {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0 4px 6px;
  padding: 10px 12px;
  border-radius: 12px;
  background: var(--fill);
  font-size: 12px;
  font-weight: 700;
  color: var(--text-2);
  white-space: normal;
}
.acct-stat div {
  display: flex;
  align-items: flex-start;
  gap: 7px;
}
.acct-stat .ic {
  margin-top: 3px;
}
.acct-stat .warn {
  color: var(--bad-text);
}
@media (min-width: 1360px) {
  .acct {
    justify-content: flex-start;
    padding: 8px;
  }
  .acct-txt,
  .acct-sub {
    display: flex;
  }
  .login-btn {
    flex-direction: row;
    justify-content: center;
    gap: 8px;
    font-size: 13.5px;
    padding: 11px;
  }
}
</style>
