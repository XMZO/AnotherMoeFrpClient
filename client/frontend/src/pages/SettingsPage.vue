<script setup lang="ts">
import { ref, watch } from 'vue'
import { NButton, NCard, NForm, NFormItem, NInput, NRadio, NRadioGroup, NSelect, NSpace, NSwitch, NText, useMessage } from 'naive-ui'
import { useSettingsStore } from '../stores/settings'
import { describeError } from '../errors'
import type { Settings } from '../api'

const settings = useSettingsStore()
const message = useMessage()
const levels = ['trace', 'debug', 'info', 'warn', 'error'].map((l) => ({ label: l, value: l }))

// 自定义代理：先填地址，点"应用"才切过去（地址不对时服务端请求会全部失败，所以单独确认）
const proxyUrl = ref('')
watch(
  () => settings.settings?.proxyUrl,
  (v) => (proxyUrl.value = v ?? ''),
  { immediate: true },
)
const customSelected = ref(false)
watch(
  () => settings.settings?.networkProxy,
  (v) => (customSelected.value = v === 'custom'),
  { immediate: true },
)

async function set(patch: Partial<Settings>) {
  try {
    await settings.update(patch)
    return true
  } catch (e) {
    message.error(describeError(e))
    return false
  }
}

async function onProxyMode(v: string) {
  if (v === 'custom') {
    customSelected.value = true // 等填好地址再应用
    return
  }
  customSelected.value = false
  await set({ networkProxy: v })
}

async function applyCustom() {
  if (await set({ networkProxy: 'custom', proxyUrl: proxyUrl.value.trim() })) message.success('已切换到自定义代理')
}
</script>

<template>
  <div class="page">
    <n-card title="设置" :bordered="false">
      <n-form v-if="settings.settings" label-placement="left" label-width="140">
        <n-form-item label="主题">
          <n-radio-group :value="settings.settings.theme" data-testid="settings-theme" @update:value="(v: string) => set({ theme: v })">
            <n-space>
              <n-radio value="system">跟随系统</n-radio>
              <n-radio value="light">浅色</n-radio>
              <n-radio value="dark">深色</n-radio>
            </n-space>
          </n-radio-group>
        </n-form-item>
        <n-form-item label="点关闭按钮时">
          <n-radio-group :value="settings.settings.closeAction" @update:value="(v: string) => set({ closeAction: v })">
            <n-space>
              <n-radio value="tray">最小化到托盘</n-radio>
              <n-radio value="quit">退出程序</n-radio>
            </n-space>
          </n-radio-group>
        </n-form-item>
        <n-form-item v-if="settings.settings.closeAction === 'tray'" label="最小化到托盘时">
          <n-space vertical :size="2">
            <n-switch :value="settings.settings.releaseOnHide" data-testid="settings-release-on-hide" @update:value="(v: boolean) => set({ releaseOnHide: v })">
              <template #checked>释放界面内存</template>
              <template #unchecked>只隐藏窗口</template>
            </n-switch>
            <n-text depth="3" style="font-size: 12px">释放后后台只占几十 MB 内存（隧道照常运行），再次打开窗口约需半秒</n-text>
          </n-space>
        </n-form-item>
        <n-form-item label="开机自启">
          <n-switch :value="settings.settings.autoStart" data-testid="settings-autostart" @update:value="(v: boolean) => set({ autoStart: v })" />
          <n-text depth="3" style="margin-left: 10px; font-size: 12px">开机后只在托盘显示图标，不弹出窗口</n-text>
        </n-form-item>
        <n-form-item label="启动后自动连接">
          <n-switch :value="settings.settings.autoConnect" data-testid="settings-autoconnect" @update:value="(v: boolean) => set({ autoConnect: v })" />
          <n-text depth="3" style="margin-left: 10px; font-size: 12px">启动时自动运行上次使用的配置和节点</n-text>
        </n-form-item>
        <n-form-item label="图片面板">
          <n-switch :value="settings.settings.imagePanel" data-testid="settings-image-panel" @update:value="(v: boolean) => set({ imagePanel: v })" />
          <n-text depth="3" style="margin-left: 10px; font-size: 12px">关闭后不会发出任何图片请求</n-text>
        </n-form-item>
        <n-form-item label="frp 日志级别">
          <n-select :value="settings.settings.logLevel" :options="levels" style="width: 160px" @update:value="(v: string) => set({ logLevel: v })" />
        </n-form-item>
        <n-form-item label="网络代理">
          <n-space vertical :size="6">
            <n-radio-group :value="customSelected ? 'custom' : settings.settings.networkProxy" data-testid="settings-network-proxy" @update:value="onProxyMode">
              <n-space>
                <n-radio value="system">跟随系统</n-radio>
                <n-radio value="none">不使用代理</n-radio>
                <n-radio value="custom" data-testid="settings-proxy-custom">自定义</n-radio>
              </n-space>
            </n-radio-group>
            <n-space v-if="customSelected" :wrap="false">
              <n-input v-model:value="proxyUrl" placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080" style="width: 320px" data-testid="settings-proxy-url" />
              <n-button type="primary" size="medium" data-testid="settings-proxy-apply" @click="applyCustom">应用</n-button>
            </n-space>
          </n-space>
        </n-form-item>
        <n-form-item label=" ">
          <n-text depth="3" style="font-size: 12px">
            网络代理用于登录、云同步、检查更新和图片面板。"跟随系统"读取 Windows"Internet 选项"里的手动代理（不支持自动配置脚本）。
            frp 连接节点不受影响，节点的代理在节点设置里单独填写。
          </n-text>
        </n-form-item>
      </n-form>
    </n-card>
  </div>
</template>

<style scoped>
.page {
  padding: 12px;
  overflow: auto;
}
</style>
