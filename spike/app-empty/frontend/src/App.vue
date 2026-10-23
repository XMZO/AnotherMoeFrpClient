<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { AppService } from '../bindings/spike/app-empty'

const startup = ref('...')

onMounted(() => {
  AppService.Ready().then((v) => (startup.value = v)).catch((e) => (startup.value = String(e)))
})
</script>

<template>
  <main style="font-family: sans-serif; color: #ddd; padding: 24px">
    <h1>MoeFrp Phase 0 · app-empty</h1>
    <p>托盘 / 单实例 / 关闭到托盘 验证用空应用。</p>
    <p>进程创建 → 前端挂载：<b>{{ startup }}</b></p>
  </main>
</template>
