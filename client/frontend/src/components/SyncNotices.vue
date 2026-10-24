<script setup lang="ts">
import { computed, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import { message, notify } from '../ui/feedback'
import { useAccountStore } from '../stores/account'
import { AppService } from '../api'
import { codeText, describeError, sessionEndedMessages } from '../errors'
import { saveFeedback } from './saveFeedback'

// 账号和同步的各种提示：被挤下线 / 会话失效、版本过低、运行中的配置被云端修改、推送失败。
const account = useAccountStore()

// ---- 被动下线（PLAN 6.6：返回 401 就下线；A 被 B 挤下线时要有明确提示） ----
const ended = computed(() => account.ended)
const endedText = computed(() => {
  const e = ended.value
  if (!e) return ''
  return sessionEndedMessages[e.code] ?? codeText(e.code, e.details, e.message)
})
function relogin() {
  account.ended = null
  account.openAuth('login')
}

// ---- 版本过低（client_outdated）：账号和同步停用，本地配置照常启动 ----
const outdated = computed(() => (account.outdatedDismissed ? null : account.outdated))
const downloadUrl = computed(() => String(outdated.value?.details?.downloadUrl ?? ''))
const latest = computed(() => String(outdated.value?.details?.latestVersion ?? ''))
function download() {
  if (downloadUrl.value) AppService.OpenURL(downloadUrl.value).catch((e) => message.error(describeError(e)))
}

// ---- 正在运行的配置被云端修改：询问是否应用（PLAN 6.6） ----
const shown = new Set<string>()
watch(
  () => account.remoteUpdates.map((r) => r.profileId + ':' + r.mode).join(','),
  () => {
    for (const r of account.remoteUpdates) {
      const k = r.profileId + ':' + r.mode
      if (shown.has(k)) continue
      shown.add(k)
      const n = notify({
        title: '云端配置已更新',
        content:
          `正在运行的配置“${r.name}”在其他设备上被修改了，是否应用到运行中的隧道？` +
          (r.mode === 'restart' ? '（节点连接参数有变化，需要重启隧道）' : '（热更新，连接不断）'),
        onClose: () => {
          shown.delete(k)
          account.dismissRemote(r.profileId).catch(() => {})
        },
        actions: [
          {
            label: '暂不',
            testid: 'remote-update-dismiss',
            onClick: () => {
              shown.delete(k)
              n.destroy()
              account.dismissRemote(r.profileId).catch(() => {})
            },
          },
          {
            label: '应用',
            primary: true,
            testid: 'remote-update-apply',
            onClick: async () => {
              shown.delete(k)
              n.destroy()
              try {
                saveFeedback(message, await account.applyRemote(r.profileId))
              } catch (e) {
                message.error(describeError(e))
              }
            },
          },
        ],
      })
    }
  },
)

// ---- 推送失败（超出数量上限、校验失败等，网络问题不在这里） ----
watch(
  () => account.pushErrors.length,
  () => {
    for (const e of account.pushErrors.splice(0)) {
      const why = describeError(e.error)
      message.warning(e.reverted ? `“${e.name}”上传失败，已改回本地配置：${why}` : `“${e.name}”同步失败：${why}`, { duration: 8000 })
    }
  },
)
</script>

<template>
  <Modal :show="!!ended" center :closable="false" :mask-closable="false" :width="420" title="已退出登录">
    <div data-testid="session-ended-dialog" :data-code="ended?.code">
      <div class="big-ic warning"><Icon name="logout" /></div>
      <h2 class="dlg-title">已退出登录</h2>
      <p class="dlg-text" data-testid="session-ended-text">{{ endedText }}</p>
      <p class="fld-help" style="margin-top: 6px">本机缓存的配置仍然可以启动，重新登录后会继续同步。</p>
    </div>
    <template #footer>
      <button type="button" class="btn quiet sm" data-testid="session-ended-ok" @click="account.ended = null">知道了</button>
      <button type="button" class="btn pri sm" data-testid="session-ended-relogin" @click="relogin">重新登录</button>
    </template>
  </Modal>

  <Modal :show="!!outdated" center :closable="false" :mask-closable="false" :width="420" title="客户端版本过低">
    <div data-testid="outdated-dialog">
      <div class="big-ic error"><Icon name="download" /></div>
      <h2 class="dlg-title">客户端版本过低</h2>
      <p class="dlg-text">当前版本已不再支持登录和云同步，请下载新版{{ latest ? `（${latest}）` : '' }}。</p>
      <p class="fld-help" style="margin-top: 6px">本机缓存的配置和本地配置仍然可以照常启动。</p>
    </div>
    <template #footer>
      <button type="button" class="btn quiet sm" data-testid="outdated-later" @click="account.outdatedDismissed = true">稍后</button>
      <button v-if="downloadUrl" type="button" class="btn pri sm" data-testid="outdated-download" @click="download"><Icon name="download" size="s" />下载新版</button>
    </template>
  </Modal>
</template>
