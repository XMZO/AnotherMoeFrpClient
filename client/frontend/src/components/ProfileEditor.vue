<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NEmpty, NSpin, NTabPane, NTabs, NTag, NText } from 'naive-ui'
import ProxyTable from './ProxyTable.vue'
import NodeTable from './NodeTable.vue'
import VisitorTable from './VisitorTable.vue'
import { useProfilesStore } from '../stores/profiles'
import { shareKindLabels } from '../labels'

const profiles = useProfilesStore()
const summary = computed(() => profiles.currentSummary)
const isSub = computed(() => summary.value?.origin === 'subscription')
const kind = computed(() => summary.value?.shareKind ?? '')
const tab = ref<'proxies' | 'nodes' | 'visitors'>('proxies')

// 订阅：P2P 只有访客，其余只有代理
const showProxies = computed(() => !isSub.value || kind.value !== 'p2p')
const showVisitors = computed(() => !isSub.value || kind.value === 'p2p')
watch(
  () => [profiles.currentId, kind.value] as const,
  () => {
    if (isSub.value) tab.value = kind.value === 'p2p' ? 'visitors' : 'proxies'
    else if (tab.value === 'visitors' && !showVisitors.value) tab.value = 'proxies'
  },
)

const originTag = computed(() => {
  const o = summary.value?.origin
  if (o === 'cloud') return { text: '云端配置', type: 'info' as const }
  if (o === 'subscription') return { text: '订阅', type: 'success' as const }
  return { text: '本地配置', type: 'default' as const }
})

const subHint = computed(() => {
  switch (kind.value) {
    case 'template':
      return '节点由分享者提供；代理是你自己的，修改后会保存到云端'
    case 'full':
      return '节点和代理都由分享者设定，这里只能查看和启动'
    case 'p2p':
      return '访客由分享生成，可以修改本地端口和启用开关'
  }
  return ''
})
</script>

<template>
  <div class="editor">
    <n-spin v-if="profiles.loading && !profiles.current" style="margin: 80px auto; display: block" />
    <n-empty v-else-if="!profiles.current" description="新建一个配置开始使用：先添加节点，再添加代理" style="margin-top: 80px" />
    <template v-else>
      <div class="title">
        <n-text strong style="font-size: 16px">{{ profiles.current.name }}</n-text>
        <n-tag v-if="summary" size="small" :bordered="false" :type="originTag.type" style="margin-left: 8px" data-testid="editor-origin">
          {{ originTag.text }}
        </n-tag>
        <n-tag v-if="isSub" size="small" :bordered="false" style="margin-left: 4px" data-testid="editor-share-kind">
          {{ shareKindLabels[kind] ?? kind }}
        </n-tag>
        <n-text v-if="isSub" depth="3" style="margin-left: 8px; font-size: 12px">来自 {{ summary?.ownerNickname }}</n-text>
      </div>
      <n-alert v-if="isSub && (summary?.revoked || summary?.expired)" type="error" :show-icon="false" style="margin: 6px 0" data-testid="editor-sub-inactive">
        {{ summary?.revoked ? '分享者已撤销这个分享' : '这个分享已过期' }}，无法再启动。可以在左侧列表里取消订阅。
      </n-alert>
      <n-text v-else-if="isSub" depth="3" style="display: block; font-size: 12px; margin: 2px 0 4px" data-testid="editor-sub-hint">{{ subHint }}</n-text>
      <n-tabs v-model:value="tab" type="line" animated class="tabs" pane-style="padding-top: 8px">
        <n-tab-pane v-if="showProxies" name="proxies" :tab="`代理（${profiles.current.proxies?.length ?? 0}）`" display-directive="show">
          <ProxyTable />
        </n-tab-pane>
        <n-tab-pane name="nodes" :tab="`节点（${profiles.current.nodes?.length ?? 0}）`" display-directive="show">
          <NodeTable />
        </n-tab-pane>
        <n-tab-pane v-if="showVisitors" name="visitors" :tab="`访客（${profiles.current.visitors?.length ?? 0}）`" display-directive="show">
          <VisitorTable />
        </n-tab-pane>
      </n-tabs>
    </template>
  </div>
</template>

<style scoped>
.editor {
  padding: 12px 16px 0;
  overflow: auto;
  min-height: 0;
}
.title {
  margin-bottom: 4px;
  display: flex;
  align-items: center;
}
</style>
