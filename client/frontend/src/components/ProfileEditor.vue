<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ProxyTable from './ProxyTable.vue'
import NodeTable from './NodeTable.vue'
import VisitorTable from './VisitorTable.vue'
import RunBar from './RunBar.vue'
import LogPanel from './LogPanel.vue'
import StatusPanel from './StatusPanel.vue'
import Icon from '../ui/Icon.vue'
import Tabs, { type TabOption } from '../ui/Tabs.vue'
import type { IconName } from '../ui/icons'
import { useProfilesStore } from '../stores/profiles'
import { shareKindLabels } from '../labels'
import mascot from '../assets/mascot.png'

// 隧道页右侧：顶部是配置名和运行控制，中间是代理 / 节点 / 访客，底部是日志（本地配置）或运行状态（云端配置、订阅：配置对用户是黑盒，看不懂的原始日志换成状态和事件；它们的日志 Go 端根本不发给界面，见 engine.LogLine.Private）。
const emit = defineEmits<{ 'toggle-list': [] }>()
const profiles = useProfilesStore()
const summary = computed(() => profiles.currentSummary)
const isSub = computed(() => summary.value?.origin === 'subscription')
const kind = computed(() => summary.value?.shareKind ?? '')
type Tab = 'proxies' | 'nodes' | 'visitors'
const tab = ref<Tab>('proxies')

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

const tabs = computed(() => {
  const c = profiles.current
  const t: TabOption<Tab>[] = []
  if (showProxies.value) t.push({ value: 'proxies', label: '代理', count: c?.proxies?.length ?? 0 })
  t.push({ value: 'nodes', label: '节点', count: c?.nodes?.length ?? 0 })
  if (showVisitors.value) t.push({ value: 'visitors', label: '访客', count: c?.visitors?.length ?? 0 })
  return t
})

const origin = computed<{ text: string; tone: string; icon: IconName }>(() => {
  const o = summary.value?.origin
  if (o === 'cloud') return { text: '云端配置', tone: 'pri', icon: 'cloud' }
  if (o === 'subscription') return { text: '订阅', tone: 'gold', icon: 'link' }
  return { text: '本地配置', tone: '', icon: 'laptop' }
})

const subHint = computed(() => {
  switch (kind.value) {
    case 'template':
      return '节点由分享者提供；代理是你自己的，修改后会保存到云端'
    case 'full':
      return '节点和代理都由分享者设定，这里只能查看和启动'
    case 'p2p':
      return '访客由分享生成，可以修改本地端口和启用开关；启动后访问“本地监听”的地址就能连到分享者的服务'
  }
  return ''
})
const tabHint = computed(() => {
  if (isSub.value) return subHint.value
  if (tab.value === 'nodes') return '一份配置可以有多个节点，运行时所有代理和访客都通过上方选中的那一个连接'
  if (tab.value === 'visitors') return '访问别人提供的 stcp / xtcp / sudp 代理，在本机监听一个端口'
  return '运行时，所有启用的代理都通过上方选中的节点连接'
})
const blackBox = computed(() => summary.value?.origin === 'cloud' || isSub.value)
const inactive = computed(() => isSub.value && (summary.value?.revoked || summary.value?.expired))

const proxyTable = ref<InstanceType<typeof ProxyTable> | null>(null)
const nodeTable = ref<InstanceType<typeof NodeTable> | null>(null)
const visitorTable = ref<InstanceType<typeof VisitorTable> | null>(null)
const add = computed(() => {
  if (!profiles.current) return null
  if (tab.value === 'proxies' && !profiles.readonly('proxies')) return { text: '添加代理', testid: 'proxy-add', go: () => proxyTable.value?.openAdd() }
  if (tab.value === 'nodes' && !profiles.readonly('nodes')) return { text: '添加节点', testid: 'node-add', go: () => nodeTable.value?.openAdd() }
  if (tab.value === 'visitors' && !profiles.readonly('visitors')) return { text: '添加访客', testid: 'visitor-add', go: () => visitorTable.value?.openAdd() }
  return null
})
</script>

<template>
  <section class="editor dotted-bg">
    <RunBar @manage-nodes="tab = 'nodes'">
      <template #title>
        <div class="rh-title title">
          <button type="button" class="icon-btn list-toggle" aria-label="显示配置列表" @click="emit('toggle-list')"><Icon name="panel" /></button>
          <template v-if="profiles.current">
            <h1 :title="profiles.current.name">{{ profiles.current.name }}</h1>
            <span v-if="summary && !isSub" class="bdg lg" :class="origin.tone" :title="origin.text" data-testid="editor-origin"><Icon :name="origin.icon" size="xs" /><span class="bdg-text">{{ origin.text }}</span></span>
            <span v-if="isSub" class="bdg lg gold" data-testid="editor-share-kind"><Icon name="link" size="xs" />{{ shareKindLabels[kind] ?? kind }}订阅</span>
            <span v-if="isSub" class="rh-from">来自 {{ summary?.ownerNickname }}</span>
          </template>
          <h1 v-else class="muted">还没有选中配置</h1>
        </div>
      </template>
    </RunBar>

    <template v-if="profiles.current">
      <div v-if="inactive" class="alert bad editor-alert" data-testid="editor-sub-inactive">
        <Icon name="alert" size="s" /><span>{{ summary?.revoked ? '分享者已撤销这个分享' : '这个分享已过期' }}，无法再启动。可以在左侧列表里取消订阅。</span>
      </div>
      <div class="tabsrow">
        <Tabs v-model="tab" :options="tabs" />
        <p class="tabs-hint" :data-testid="isSub && !inactive ? 'editor-sub-hint' : undefined">{{ tabHint }}</p>
        <button v-if="add" :key="add.testid" type="button" class="btn pri sm add-item" :data-testid="add.testid" @click="add.go()"><Icon name="plus" size="s" />{{ add.text }}</button>
      </div>
      <div class="content">
        <ProxyTable v-show="tab === 'proxies'" ref="proxyTable" />
        <NodeTable v-show="tab === 'nodes'" ref="nodeTable" />
        <VisitorTable v-show="tab === 'visitors'" ref="visitorTable" />
      </div>
    </template>
    <div v-else class="content">
      <div v-if="profiles.loading" class="empty"><Icon name="refresh" class="spin" /></div>
      <div v-else class="empty">
        <img :src="mascot" alt="" />
        <b>新建一个配置开始使用</b>
        <p>点左上角的 + 新建配置，先添加节点，再添加代理，然后点“启动”。</p>
      </div>
    </div>
    <StatusPanel v-if="blackBox" />
    <LogPanel v-else />
  </section>
</template>

<style>
.editor {
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 0;
  transition: background-color 0.35s;
}
/* 标题和运行控制一行放下：窗口变窄时标题省略、节点框变窄，不换行 */
.runhead {
  display: flex;
  align-items: center;
  gap: 12px 14px;
  padding: 16px 20px 0;
}
.rh-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1 1 auto;
  min-width: 0;
}
.rh-title h1 {
  flex: 0 1 auto;
  min-width: 4em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 20px;
  font-weight: 900;
  letter-spacing: -0.01em;
}
.rh-title h1.muted {
  color: var(--text-3);
  font-size: 17px;
}
.rh-title .bdg {
  flex-shrink: 0;
}
.rh-from {
  flex: 0 3 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 12px;
  color: var(--text-3);
  font-weight: 700;
  white-space: nowrap;
}
.list-toggle {
  display: none;
}
.editor-alert {
  margin: 12px 20px 0;
}
.tabsrow {
  display: flex;
  align-items: center;
  gap: 10px 14px;
  padding: 14px 20px 12px;
}
.add-item {
  animation: fade 0.3s;
}
.tabs-hint {
  flex: 1 1 0;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
  line-height: 1.5;
}
.tabsrow .add-item {
  flex-shrink: 0;
  margin-left: auto;
}
.editor > .content {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0 20px 18px;
  container-type: inline-size;
}
@media (max-width: 1000px) {
  .rh-title .bdg-text,
  .tabs-hint {
    display: none;
  }
  .tabsrow .btn {
    margin-left: auto;
  }
}
@media (max-width: 860px) {
  .list-toggle {
    display: inline-grid;
  }
}
</style>
