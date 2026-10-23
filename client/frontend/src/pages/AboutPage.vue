<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NSpace, NTable, NText } from 'naive-ui'
import { AppService, type License, type StartupInfo, type UpdateInfo } from '../api'
import { describeError, toAppError } from '../errors'
import { formatTime } from '../components/shareFormat'

const info = ref<StartupInfo | null>(null)
const update = ref<UpdateInfo | null>(null)
const licenses = ref<License[]>([])
const checking = ref(false)
const checkError = ref('')

onMounted(async () => {
  info.value = await AppService.Startup()
  update.value = await AppService.UpdateInfo()
  licenses.value = (await AppService.Licenses()) ?? []
})

const open = (url: string) => AppService.OpenURL(url).catch(() => {})

async function check() {
  checking.value = true
  checkError.value = ''
  try {
    update.value = await AppService.CheckUpdate()
  } catch (e) {
    checkError.value = describeError(toAppError(e))
  } finally {
    checking.value = false
  }
}
</script>

<template>
  <div class="page">
    <n-card title="关于" :bordered="false">
      <n-descriptions v-if="info" :column="1" label-placement="left" bordered size="small">
        <n-descriptions-item label="客户端版本"><span data-testid="about-version">{{ info.version.app }}</span></n-descriptions-item>
        <n-descriptions-item label="frp 版本">{{ info.version.frp }}</n-descriptions-item>
        <n-descriptions-item label="Wails">{{ info.version.wails }}</n-descriptions-item>
        <n-descriptions-item label="Go">{{ info.version.go }}</n-descriptions-item>
        <n-descriptions-item label="设置和配置缓存"><span class="mono">{{ info.dataDir }}</span></n-descriptions-item>
        <n-descriptions-item label="WebView2 数据和日志"><span class="mono">{{ info.localDir }}</span></n-descriptions-item>
      </n-descriptions>

      <div class="section">
        <n-space align="center">
          <n-button size="small" :loading="checking" data-testid="about-check-update" @click="check">检查更新</n-button>
          <n-text v-if="update?.checkedAt" depth="3" style="font-size: 12px">上次检查：{{ formatTime(update.checkedAt) }}</n-text>
        </n-space>
        <n-alert v-if="update?.available" type="info" :show-icon="false" style="margin-top: 8px" data-testid="about-update-available">
          有新版本 {{ update.latest }}（当前 {{ update.current }}）。
          <n-button v-if="update.downloadUrl" text type="primary" @click="open(update.downloadUrl)">下载</n-button>
          <div v-if="update.releaseNotes" style="white-space: pre-wrap; margin-top: 4px">{{ update.releaseNotes }}</div>
          <div v-if="update.sha256" class="mono" style="margin-top: 4px; font-size: 12px">SHA256：{{ update.sha256 }}</div>
        </n-alert>
        <n-text v-else-if="update?.checkedAt && !update.error" depth="3" style="display: block; margin-top: 8px" data-testid="about-up-to-date">已是最新版本</n-text>
        <n-text v-if="checkError" type="error" style="display: block; margin-top: 8px">{{ checkError }}</n-text>
        <n-alert v-if="update?.announcement" type="default" title="公告" style="margin-top: 8px" data-testid="about-announcement">
          <div style="white-space: pre-wrap">{{ update.announcement }}</div>
        </n-alert>
      </div>

      <div class="section">
        <n-text strong>开源许可</n-text>
        <n-table size="small" :bordered="false" style="margin-top: 6px" data-testid="about-licenses">
          <tbody>
            <tr v-for="l in licenses" :key="l.name">
              <td>
                <n-button text type="primary" @click="open(l.url)">{{ l.name }}</n-button>
              </td>
              <td>{{ l.license }}</td>
              <td><n-text depth="3">{{ l.note }}</n-text></td>
            </tr>
          </tbody>
        </n-table>
      </div>
    </n-card>
  </div>
</template>

<style scoped>
.page {
  padding: 12px;
  overflow: auto;
}
.section {
  margin-top: 16px;
}
.mono {
  font-family: Consolas, monospace;
  word-break: break-all;
}
</style>
