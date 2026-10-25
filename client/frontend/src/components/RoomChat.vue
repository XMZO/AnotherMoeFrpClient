<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import { message } from '../ui/feedback'
import { Events, VnetService } from '../api'
import { describeError } from '../errors'

// 联机房间的聊天：右下角一个圆按钮，点开是聊天窗口（像网站的在线客服）。只在进了房间时出现。
// 别人发来消息、窗口没展开或程序不在前台时，响一声轻的"叮咚"（WebAudio 现场合成，不带音频文件）。
interface Msg { from: number; nickname: string; text: string; at: number }

const room = ref('')
const self = ref(0)
const msgs = ref<Msg[]>([])
const open = ref(false)
const unread = ref(0)
const draft = ref('')
const sending = ref(false)
const list = ref<HTMLElement | null>(null)
const input = ref<HTMLTextAreaElement | null>(null)

const shown = computed(() => !!room.value)

function scrollDown() {
  nextTick(() => {
    if (list.value) list.value.scrollTop = list.value.scrollHeight
  })
}

// 轻量提示音：两个短的正弦音（E6 → A6），音量很小，渐入渐出
let ctx: AudioContext | null = null
function ding() {
  try {
    ctx ??= new AudioContext()
    const t = ctx.currentTime
    for (const [i, f] of [1318.5, 1760].entries()) {
      const o = ctx.createOscillator()
      const g = ctx.createGain()
      o.type = 'sine'
      o.frequency.value = f
      const s = t + i * 0.11
      g.gain.setValueAtTime(0, s)
      g.gain.linearRampToValueAtTime(0.07, s + 0.015)
      g.gain.exponentialRampToValueAtTime(0.0001, s + 0.32)
      o.connect(g).connect(ctx.destination)
      o.start(s)
      o.stop(s + 0.34)
    }
  } catch {
    /* 没声卡之类的，忽略 */
  }
}

let offState: (() => void) | undefined
let offChat: (() => void) | undefined
onMounted(async () => {
  offState = Events.On('vnet:state', (ev: any) => {
    const st = ev.data as { room: string; self: number }
    if (st.room !== room.value) {
      msgs.value = []
      unread.value = 0
      if (!st.room) open.value = false
    }
    room.value = st.room
    self.value = st.self
  })
  offChat = Events.On('vnet:chat', (ev: any) => {
    const d = ev.data as { msgs: Msg[]; new?: Msg | null }
    msgs.value = d.msgs ?? []
    const m = d.new
    if (m && m.from !== self.value) {
      if (!open.value) unread.value++
      if (!open.value || document.hidden || !document.hasFocus()) ding()
    }
    scrollDown()
  })
  const st = (await VnetService.State()) as { room: string; self: number }
  room.value = st.room
  self.value = st.self
  if (st.room) msgs.value = ((await VnetService.ChatHistory()) ?? []) as Msg[]
})
onBeforeUnmount(() => {
  offState?.()
  offChat?.()
})

watch(open, (v) => {
  if (!v) return
  unread.value = 0
  scrollDown()
  nextTick(() => input.value?.focus())
})

async function send() {
  const text = draft.value.trim()
  if (!text || sending.value) return
  sending.value = true
  try {
    await VnetService.SendChat(text)
    draft.value = ''
  } catch (e) {
    message.error(describeError(e))
  } finally {
    sending.value = false
    input.value?.focus()
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    send()
  }
}

const time = (at: number) => new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
</script>

<template>
  <div v-if="shown" class="rchat">
    <Transition name="rchat-pop">
      <section v-if="open" class="rchat-box" aria-label="房间聊天">
        <header class="rchat-h">
          <Icon name="chat" size="s" />
          <b>房间聊天</b>
          <span class="grow" />
          <button type="button" class="icon-btn" aria-label="收起" title="收起" @click="open = false"><Icon name="chev" size="s" /></button>
        </header>
        <div ref="list" class="rchat-list">
          <p v-if="!msgs.length" class="rchat-empty">还没有消息，打个招呼吧</p>
          <div v-for="(m, i) in msgs" :key="m.at + '-' + i" class="rchat-msg" :class="{ me: m.from === self }">
            <small v-if="m.from !== self && (i === 0 || msgs[i - 1].from !== m.from)">{{ m.nickname }}</small>
            <div class="rchat-bubble" :title="time(m.at)">{{ m.text }}</div>
          </div>
        </div>
        <form class="rchat-in" @submit.prevent="send">
          <textarea ref="input" v-model="draft" rows="1" maxlength="500" placeholder="说点什么…（Enter 发送）" @keydown="onKey" />
          <button type="submit" class="btn pri sm" :disabled="!draft.trim() || sending" aria-label="发送"><Icon name="send" size="s" /></button>
        </form>
      </section>
    </Transition>
    <button type="button" class="rchat-fab" :class="{ on: open }" :aria-label="open ? '收起聊天' : '打开房间聊天'" data-testid="room-chat" @click="open = !open">
      <Icon :name="open ? 'x' : 'chat'" />
      <span v-if="unread && !open" class="rchat-badge">{{ unread > 99 ? '99+' : unread }}</span>
    </button>
  </div>
</template>

<style>
.rchat {
  position: fixed;
  right: 20px;
  bottom: 20px;
  z-index: 900;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 12px;
}
.rchat-fab {
  position: relative;
  width: 52px;
  height: 52px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--pri);
  color: #fff;
  box-shadow: var(--shadow);
  transition: transform 0.2s var(--ease-spring);
}
.rchat-fab:hover {
  transform: translateY(-2px) scale(1.04);
}
.rchat-badge {
  position: absolute;
  top: -4px;
  right: -4px;
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  border-radius: 99px;
  background: var(--bad);
  color: #fff;
  font-size: 11px;
  font-weight: 800;
  line-height: 20px;
  text-align: center;
}
.rchat-box {
  width: 330px;
  height: 430px;
  max-height: calc(100vh - 110px);
  display: flex;
  flex-direction: column;
  border-radius: 18px;
  background: var(--raised);
  border: 1.5px solid var(--line-2);
  box-shadow: var(--shadow);
  overflow: hidden;
  transform-origin: bottom right;
}
.rchat-h {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 10px 10px 14px;
  border-bottom: 1.5px solid var(--line-2);
  color: var(--text-2);
}
.rchat-h b {
  color: var(--text);
  font-weight: 900;
}
.rchat-h .grow {
  flex: 1;
}
.rchat-list {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.rchat-empty {
  margin: auto;
  font-size: 12.5px;
  color: var(--text-3);
}
.rchat-msg {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}
.rchat-msg.me {
  align-items: flex-end;
}
.rchat-msg small {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-3);
  margin: 4px 4px 0;
}
.rchat-bubble {
  max-width: 85%;
  padding: 7px 11px;
  border-radius: 14px 14px 14px 4px;
  background: var(--fill);
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
.rchat-msg.me .rchat-bubble {
  border-radius: 14px 14px 4px 14px;
  background: var(--pri);
  color: #fff;
}
.rchat-in {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  padding: 10px;
  border-top: 1.5px solid var(--line-2);
}
.rchat-in textarea {
  flex: 1;
  min-height: 36px;
  max-height: 96px;
  resize: none;
  padding: 8px 10px;
  border-radius: 12px;
  border: 1.5px solid var(--line);
  background: var(--surface-2);
  font: inherit;
  font-size: 13px;
  outline: none;
}
.rchat-in textarea:focus {
  border-color: var(--pri);
}
.rchat-in .btn {
  height: 36px;
}
.rchat-pop-enter-active {
  transition: opacity 0.18s, transform 0.25s var(--ease-pop);
}
.rchat-pop-leave-active {
  transition: opacity 0.12s, transform 0.12s;
}
.rchat-pop-enter-from,
.rchat-pop-leave-to {
  opacity: 0;
  transform: scale(0.9) translateY(8px);
}
</style>
