<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ProfileList from '../components/ProfileList.vue'
import ProfileEditor from '../components/ProfileEditor.vue'
import { message } from '../ui/feedback'
import { useProfilesStore } from '../stores/profiles'
import { useSettingsStore } from '../stores/settings'
import { useTunnelStore } from '../stores/tunnel'
import { describeError } from '../errors'

const profiles = useProfilesStore()
const settings = useSettingsStore()
const tunnel = useTunnelStore()
// 窄窗口（< 860px）时配置列表变成从左边滑出的面板
const listOpen = ref(false)

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
  <div class="tunnel" :class="{ 'list-open': listOpen }">
    <ProfileList class="plist" @picked="listOpen = false" />
    <ProfileEditor class="work" @toggle-list="listOpen = !listOpen" />
    <Transition name="fade"><div v-if="listOpen" class="list-scrim" @click="listOpen = false" /></Transition>
  </div>
</template>

<style>
.tunnel {
  position: relative;
  display: flex;
  min-height: 0;
  height: 100%;
}
.tunnel > .plist {
  width: 248px;
  flex-shrink: 0;
}
.tunnel > .work {
  flex: 1;
  min-width: 0;
}
.list-scrim {
  display: none;
}
@media (max-width: 1100px) {
  .tunnel > .plist {
    width: 228px;
  }
}
@media (max-width: 860px) {
  .tunnel > .plist {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 20;
    width: 264px;
    transform: translateX(-104%);
    transition: transform 0.35s var(--ease-out), box-shadow 0.3s;
  }
  .tunnel.list-open > .plist {
    transform: none;
    box-shadow: var(--shadow-lg);
  }
  .list-scrim {
    display: block;
    position: absolute;
    inset: 0;
    z-index: 15;
    background: var(--scrim);
  }
}
</style>
