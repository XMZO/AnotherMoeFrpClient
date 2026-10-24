<script setup lang="ts">
import { computed, ref } from 'vue'
import NodeDrawer from './NodeDrawer.vue'
import { saveFeedback } from './saveFeedback'
import Icon from '../ui/Icon.vue'
import { dialog, message } from '../ui/feedback'
import type { Node } from '../api'
import { useProfilesStore } from '../stores/profiles'
import { useTunnelStore } from '../stores/tunnel'
import { codeText, describeError } from '../errors'
import { nodeLabel, quotaText } from '../util'

const profiles = useProfilesStore()
const tunnel = useTunnelStore()
const drawer = ref<{ show: boolean; index: number }>({ show: false, index: -1 })
const rows = computed(() => (profiles.current?.nodes ?? []).map((n, index) => ({ ...n, index })))
const ro = computed(() => profiles.readonly('nodes'))
const official = computed(() => (profiles.isSubscription ? [] : tunnel.official))

defineExpose({ openAdd: () => (drawer.value = { show: true, index: -1 }) })

function askRemove(n: Node & { index: number }) {
  dialog.warning({
    title: '删除节点',
    content: `删除节点 ${nodeLabel(n)}？`,
    icon: 'trash',
    danger: true,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        saveFeedback(message, await profiles.removeItem('nodes', n.index))
      } catch (e) {
        message.error(describeError(e))
      }
    },
  })
}

function ping(id: string): { cls: string; text: string; title: string } {
  const r = tunnel.pings[id]
  if (!r) return { cls: '', text: '未测速', title: '点上方的“测速”测试延迟' }
  if (r.ok) return { cls: r.latencyMs < 80 ? 'ok' : r.latencyMs < 200 ? 'warn' : 'bad', text: `${r.latencyMs} ms`, title: '' }
  const text = codeText(r.code, { target: r.target }, r.message)
  return { cls: 'bad', text, title: r.message ?? text }
}

async function use(id: string) {
  if (!profiles.current) return
  try {
    await tunnel.chooseNode(profiles.current.id, id)
  } catch (e) {
    message.error(describeError(e))
  }
}
</script>

<template>
  <div>
    <div v-if="rows.length" class="cards">
      <TransitionGroup name="row">
        <article
          v-for="(n, i) in rows"
          :key="n.id"
          class="ncard"
          :class="{ cur: n.id === tunnel.selectedNodeId }"
          :style="{ animationDelay: `${i * 0.04}s` }"
          :data-testid="`node-row-${n.remark || n.serverAddr}`"
        >
          <div class="ncard-top">
            <span class="tile-ic lg"><Icon name="server" /></span>
            <div class="ncard-t">
              <b :title="n.remark || '（无备注）'">{{ n.remark || '（无备注）' }}</b>
              <small class="mono">{{ n.serverAddr }}:{{ n.serverPort }}</small>
            </div>
            <span v-if="n.id === tunnel.selectedNodeId" class="bdg pri">当前选用</span>
          </div>
          <div class="ncard-tags">
            <span class="type">{{ n.protocol || 'tcp' }}</span>
            <span class="type">{{ n.tls === false ? 'tls 关' : 'tls' }}</span>
            <span v-if="n.user" class="type">user {{ n.user }}</span>
            <span v-if="n.proxyURL" class="type">经代理</span>
          </div>
          <div class="ncard-foot">
            <span class="ping" :class="ping(n.id).cls" :title="ping(n.id).title">{{ ping(n.id).text }}</span>
            <button v-if="n.id !== tunnel.selectedNodeId" type="button" class="btn soft xs" :disabled="tunnel.busy" @click="use(n.id)">用这个节点</button>
            <template v-if="!ro">
              <button type="button" class="icon-btn" title="编辑" :aria-label="`编辑节点 ${n.remark || n.serverAddr}`" :data-testid="`node-edit-${n.remark || n.serverAddr}`" @click="drawer = { show: true, index: n.index }">
                <Icon name="edit" size="s" />
              </button>
              <button type="button" class="icon-btn del" title="删除" :aria-label="`删除节点 ${n.remark || n.serverAddr}`" @click="askRemove(n)"><Icon name="trash" size="s" /></button>
            </template>
          </div>
        </article>
      </TransitionGroup>
    </div>
    <div v-else class="empty compact">
      <b>{{ ro ? '分享里没有节点' : '还没有节点' }}</b>
      <p v-if="!ro">点右上角的“添加节点”，填上 frps 的地址、端口和 token{{ official.length ? '；也可以直接用下面的官方节点' : '' }}</p>
    </div>

    <template v-if="official.length">
      <div class="sec-title">
        <h3>官方节点</h3>
        <small>由面板提供，登录后自动获得；在上方选择即可使用，代理名会自动加上你的昵称前缀</small>
      </div>
      <div class="cards">
        <article
          v-for="(n, i) in official"
          :key="n.id"
          class="ncard"
          :class="{ cur: n.id === tunnel.selectedNodeId }"
          :style="{ animationDelay: `${(rows.length + i) * 0.04}s` }"
          :data-testid="`official-node-${n.name}`"
        >
          <div class="ncard-top">
            <span class="tile-ic lg gold"><Icon name="star" /></span>
            <div class="ncard-t">
              <b :title="n.name">{{ n.name }}</b>
              <small>MoeFrp 托管节点</small>
            </div>
            <span class="bdg" :class="n.online ? 'ok' : ''">{{ n.online ? '在线' : '离线' }}</span>
          </div>
          <p v-if="n.description" class="ncard-desc">{{ n.description }}</p>
          <div class="ncard-quota"><Icon name="shield" size="xs" /><span>你的限额：{{ quotaText(n.quota) }}</span></div>
          <div class="ncard-foot">
            <span class="ping" :class="ping(n.id).cls" :title="ping(n.id).title">{{ ping(n.id).text }}</span>
            <span v-if="n.id === tunnel.selectedNodeId" class="bdg pri">当前选用</span>
            <button v-else-if="n.online" type="button" class="btn soft xs" :disabled="tunnel.busy" @click="use(n.id)">用这个节点</button>
          </div>
        </article>
      </div>
    </template>
    <NodeDrawer v-model:show="drawer.show" :index="drawer.index" />
  </div>
</template>

<style>
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(260px, 100%), 1fr));
  gap: 12px;
}
.ncard {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border-radius: 19px;
  background: var(--surface);
  border: 1.5px solid var(--line-2);
  box-shadow: var(--shadow-sm);
  animation: rowIn 0.45s var(--ease-out) backwards;
  transition: border-color 0.2s, box-shadow 0.25s, background-color 0.35s;
}
.ncard:hover {
  box-shadow: var(--shadow);
}
.ncard.cur {
  border-color: color-mix(in oklab, var(--pri) 55%, var(--line));
  box-shadow: 0 0 0 4px var(--pri-soft);
}
.ncard-top {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.ncard-t {
  flex: 1;
  min-width: 0;
  line-height: 1.3;
}
.ncard-t b {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14.5px;
  font-weight: 800;
}
.ncard-t small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-3);
}
.ncard-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.ncard-desc {
  font-size: 12.5px;
  color: var(--text-2);
  line-height: 1.55;
}
.ncard-quota {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  padding: 8px 10px;
  border-radius: 11px;
  background: var(--fill);
  font-size: 12px;
  font-weight: 700;
  color: var(--text-2);
  line-height: 1.45;
}
.ncard-quota .ic {
  margin-top: 2px;
}
.ncard-foot {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: auto;
}
.ncard-foot .ping {
  margin-right: auto;
  max-width: 60%;
}
.sec-title {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin: 22px 2px 12px;
}
.sec-title h3 {
  font-size: 15px;
  font-weight: 900;
}
.sec-title small {
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
}
.empty.compact {
  padding: 22px 16px;
  border-radius: 19px;
  border: 1.5px dashed var(--line);
}
</style>
