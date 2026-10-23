<script setup lang="ts">
import { computed, h, watch } from 'vue'
import { NButton, NModal, NSpace, NText, useMessage, useNotification } from 'naive-ui'
import { useAccountStore } from '../stores/account'
import { AppService } from '../api'
import { codeText, describeError, sessionEndedMessages } from '../errors'
import { saveFeedback } from './saveFeedback'

// 账号和同步的各种提示：被挤下线 / 会话失效、版本过低、运行中的配置被云端修改、推送失败。
const account = useAccountStore()
const message = useMessage()
const notification = useNotification()

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
      const n = notification.info({
        title: '云端配置已更新',
        content: `正在运行的配置"${r.name}"在其他设备上被修改了，是否应用到运行中的隧道？` + (r.mode === 'restart' ? '（节点连接参数有变化，需要重启隧道）' : '（热更新，连接不断）'),
        duration: 0,
        onClose: () => {
          shown.delete(k)
          account.dismissRemote(r.profileId).catch(() => {})
        },
        action: () =>
          h(NSpace, null, () => [
            h(
              NButton,
              {
                size: 'small',
                type: 'primary',
                'data-testid': 'remote-update-apply',
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
              () => '应用',
            ),
            h(
              NButton,
              {
                size: 'small',
                'data-testid': 'remote-update-dismiss',
                onClick: () => {
                  shown.delete(k)
                  n.destroy()
                  account.dismissRemote(r.profileId).catch(() => {})
                },
              },
              () => '暂不',
            ),
          ]),
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
      message.warning(e.reverted ? `"${e.name}"上传失败，已改回本地配置：${why}` : `"${e.name}"同步失败：${why}`, { duration: 8000 })
    }
  },
)
</script>

<template>
  <n-modal :show="!!ended" preset="dialog" type="warning" title="已退出登录" :closable="false" :mask-closable="false">
    <div data-testid="session-ended-dialog" :data-code="ended?.code">
      <p data-testid="session-ended-text" style="margin: 4px 0 8px">{{ endedText }}</p>
      <n-text depth="3" style="font-size: 12px">本机缓存的配置仍然可以启动，重新登录后会继续同步。</n-text>
    </div>
    <template #action>
      <n-space>
        <n-button data-testid="session-ended-ok" @click="account.ended = null">知道了</n-button>
        <n-button type="primary" data-testid="session-ended-relogin" @click="relogin">重新登录</n-button>
      </n-space>
    </template>
  </n-modal>

  <n-modal :show="!!outdated" preset="dialog" type="error" title="客户端版本过低" :closable="false" :mask-closable="false">
    <div data-testid="outdated-dialog">
      <p style="margin: 4px 0 8px">
        当前版本已不再支持登录和云同步，请下载新版{{ latest ? `（${latest}）` : '' }}。
      </p>
      <n-text depth="3" style="font-size: 12px">本机缓存的配置和本地配置仍然可以照常启动。</n-text>
    </div>
    <template #action>
      <n-space>
        <n-button data-testid="outdated-later" @click="account.outdatedDismissed = true">稍后</n-button>
        <n-button v-if="downloadUrl" type="primary" data-testid="outdated-download" @click="download">下载新版</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
