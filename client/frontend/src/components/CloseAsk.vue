<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import Check from '../ui/Check.vue'
import Modal from '../ui/Modal.vue'
import { message } from '../ui/feedback'
import { AppService, Events } from '../api'
import { useSettingsStore } from '../stores/settings'
import { describeError } from '../errors'

// 点关闭按钮、设置是"每次询问"时，Go 端推 app:close-ask，这里问一次：最小化到托盘还是退出。
// 勾了"记住我的选择"就写进设置，以后不再问（设置页"点关闭按钮时"可以改回来）。
const settings = useSettingsStore()
const show = ref(false)
const remember = ref(false)
const busy = ref(false)
let off: (() => void) | undefined

onMounted(() => {
  off = Events.On('app:close-ask', () => {
    remember.value = false
    show.value = true
  })
})
onBeforeUnmount(() => off?.())

async function choose(action: 'tray' | 'quit') {
  if (busy.value) return
  busy.value = true
  try {
    if (remember.value) await settings.update({ closeAction: action })
    show.value = false
    await AppService.CloseWindow(action)
  } catch (e) {
    message.error(describeError(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal v-model:show="show" title="关闭窗口" icon="power" :width="400" data-testid="close-ask">
    <p class="close-ask-text">最小化到托盘时隧道照常运行；退出程序会先停止隧道。</p>
    <Check v-model="remember" plain data-testid="close-ask-remember">记住我的选择，不再询问</Check>
    <template #footer>
      <button type="button" class="btn quiet sm" @click="show = false">取消</button>
      <button type="button" class="btn quiet sm" :disabled="busy" data-testid="close-ask-quit" @click="choose('quit')">退出程序</button>
      <button type="button" class="btn pri sm" :disabled="busy" data-testid="close-ask-tray" @click="choose('tray')">最小化到托盘</button>
    </template>
  </Modal>
</template>

<style>
.close-ask-text {
  margin: 0 0 12px;
  color: var(--text-2);
  font-size: 13px;
}
</style>
