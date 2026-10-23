<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useMessage } from 'naive-ui'
import ProfileList from '../components/ProfileList.vue'
import ProfileEditor from '../components/ProfileEditor.vue'
import RunBar from '../components/RunBar.vue'
import LogPanel from '../components/LogPanel.vue'
import { useProfilesStore } from '../stores/profiles'
import { useSettingsStore } from '../stores/settings'
import { useTunnelStore } from '../stores/tunnel'
import { describeError } from '../errors'

const profiles = useProfilesStore()
const settings = useSettingsStore()
const tunnel = useTunnelStore()
const message = useMessage()
const showLogs = ref(true)

onMounted(async () => {
  try {
    await profiles.loadList()
    if (!settings.settings) await settings.load()
    await tunnel.init()
    // 默认选中：正在运行的 → 上次启动的 → 列表第一个
    const ids = profiles.list.map((p) => p.id)
    const pick = [tunnel.status.profileId, settings.settings?.lastProfileId, ids[0]].find((id) => id && ids.includes(id))
    if (pick) await profiles.select(pick)
  } catch (e) {
    message.error(describeError(e))
  }
})
</script>

<template>
  <div class="tunnel-page">
    <div class="top">
      <ProfileList class="list" />
      <ProfileEditor class="editor" />
    </div>
    <RunBar v-model:show-logs="showLogs" />
    <LogPanel v-if="showLogs" class="logs" />
  </div>
</template>

<style scoped>
.tunnel-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.top {
  flex: 1;
  display: flex;
  min-height: 0;
}
.list {
  width: 220px;
  flex-shrink: 0;
}
.editor {
  flex: 1;
  min-width: 0;
}
.logs {
  height: 210px;
  flex-shrink: 0;
}
</style>
