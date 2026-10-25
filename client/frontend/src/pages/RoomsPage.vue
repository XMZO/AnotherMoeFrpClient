<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Clipboard } from '@wailsio/runtime'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import { dialog, message } from '../ui/feedback'
import { Events, VnetService } from '../api'
import { useAccountStore } from '../stores/account'
import { describeError, toAppError } from '../errors'
import mascot from '../assets/mascot.png'

// 联机房间：和朋友组成虚拟局域网（客户端 internal/vnet，面板 shared/api/vnet.go）。
// 进房间要管理员权限（建虚拟网卡）；优先打洞直连，打不通经面板中继。
interface Member { userId: string; nickname: string; slot: number; ip: string; online: boolean }
interface Room { id: string; name: string; code: string; ownerId: string; ownerName: string; members: Member[] }
interface PeerState { slot: number; nickname: string; ip: string; mode: string }
interface VnetState { admin: boolean; room: string; self: number; ip: string; peers: PeerState[]; error?: string }

const account = useAccountStore()
const rooms = ref<Room[]>([])
const loading = ref(false)
const busy = ref('')
const state = ref<VnetState>({ admin: true, room: '', self: 0, ip: '', peers: [] })
const modal = ref<{ show: boolean; mode: 'create' | 'join'; value: string; error: string }>({ show: false, mode: 'create', value: '', error: '' })

const myId = computed(() => account.account.user?.id ?? '')
const active = computed(() => rooms.value.find((r) => r.id === state.value.room) ?? null)
const modeView: Record<string, [string, string]> = {
  direct: ['直连', 'ok'],
  relay: ['中继', 'gold'],
  connecting: ['打洞中', 'info'],
  offline: ['不在线', ''],
}
// confirm 弹一个确认框，点确定返回 true
function confirm(o: { title: string; content: string; positiveText: string; danger?: boolean }) {
  return new Promise<boolean>((resolve) =>
    dialog.warning({ ...o, icon: 'alert', negativeText: '取消', onPositiveClick: () => resolve(true), onNegativeClick: () => resolve(false) }),
  )
}
const fmtCode = (c: string) => (c.length === 8 ? `${c.slice(0, 4)}-${c.slice(4)}` : c)

async function load() {
  if (!account.loggedIn) return
  loading.value = true
  try {
    rooms.value = ((await VnetService.Rooms()) ?? []) as Room[]
  } catch (e) {
    message.error(describeError(e))
  } finally {
    loading.value = false
  }
}

let off: (() => void) | undefined
let reloadTimer: ReturnType<typeof setTimeout> | undefined
onMounted(async () => {
  off = Events.On('vnet:state', (ev: any) => {
    state.value = ev.data as VnetState
    clearTimeout(reloadTimer)
    reloadTimer = setTimeout(load, 800) // 成员上下线：名单里的"在线"也刷新一下
  })
  state.value = (await VnetService.State()) as VnetState
  load()
})
onBeforeUnmount(() => {
  off?.()
  clearTimeout(reloadTimer)
})
watch(() => account.loggedIn, (v) => (v ? load() : (rooms.value = [])))

async function act(key: string, fn: () => Promise<unknown>, ok?: string) {
  busy.value = key
  try {
    await fn()
    if (ok) message.success(ok)
    await load()
  } catch (e) {
    const err = toAppError(e)
    if (err.code === 'vnet_need_admin') askAdmin()
    else message.error(describeError(err))
  } finally {
    busy.value = ''
  }
}

async function askAdmin() {
  const yes = await confirm({
    title: '需要管理员权限',
    content: '进入联机房间要创建一块虚拟网卡，需要以管理员身份运行。现在重启程序吗？（会弹出系统的权限确认）',
    positiveText: '以管理员身份重启',
  })
  if (yes) restartAdmin()
}

async function restartAdmin() {
  try {
    await VnetService.RestartAsAdmin()
  } catch (e) {
    message.error('没有重启：' + describeError(e))
  }
}

function openModal(mode: 'create' | 'join') {
  modal.value = { show: true, mode, value: '', error: '' }
}

async function submitModal() {
  const m = modal.value
  const v = m.value.trim()
  if (!v) {
    m.error = m.mode === 'create' ? '给房间起个名字' : '输入朋友发给你的邀请码'
    return
  }
  busy.value = 'modal'
  try {
    if (m.mode === 'create') {
      const r = (await VnetService.CreateRoom(v)) as Room
      message.success(`房间已创建，邀请码 ${fmtCode(r.code)}`)
    } else {
      await VnetService.JoinRoom(v)
      message.success('已加入房间')
    }
    m.show = false
    await load()
  } catch (e) {
    m.error = describeError(e)
  } finally {
    busy.value = ''
  }
}

async function leave(r: Room) {
  const owner = r.ownerId === myId.value
  const yes = await confirm({
    title: owner ? `解散「${r.name}」` : `离开「${r.name}」`,
    content: owner ? '你是房主，离开等于解散房间，所有成员都会被移出。' : '离开后要重新输入邀请码才能回来。',
    positiveText: owner ? '解散' : '离开',
    danger: true,
  })
  if (yes) act('leave-' + r.id, () => VnetService.LeaveRoom(r.id), owner ? '房间已解散' : '已离开房间')
}

async function kick(r: Room, m: Member) {
  const yes = await confirm({ title: `移出 ${m.nickname}`, content: '移出后对方要重新输入邀请码才能回来。', positiveText: '移出', danger: true })
  if (yes) act('kick-' + m.userId, () => VnetService.KickMember(r.id, m.userId), '已移出')
}

async function copy(text: string, what: string) {
  await Clipboard.SetText(text)
  message.success(`${what}已复制`)
}

const disconnect = () => act('disc', () => VnetService.Disconnect())
const enter = (r: Room) => act('go-' + r.id, () => VnetService.Connect(r.id))

function peerMode(slot: number) {
  return state.value.peers.find((p) => p.slot === slot)?.mode ?? 'offline'
}
</script>

<template>
  <div class="page dotted-bg">
    <div class="inner rooms-page">
      <div class="pg-head head">
        <div>
          <h1>联机房间</h1>
          <p>和朋友组成一个虚拟局域网：游戏里找"局域网"房间，或者直接连对方的虚拟 IP。优先打洞直连，连不上自动经面板中继。</p>
        </div>
        <div class="pg-actions">
          <button type="button" class="btn ghost sm" :disabled="!account.loggedIn || loading" @click="load"><Icon name="refresh" size="s" :class="{ spin: loading }" />刷新</button>
          <button type="button" class="btn soft sm" :disabled="!account.loggedIn" data-testid="room-join" @click="openModal('join')"><Icon name="link" size="s" />加入房间</button>
          <button type="button" class="btn pri sm" :disabled="!account.loggedIn" data-testid="room-create" @click="openModal('create')"><Icon name="plus" size="s" />创建房间</button>
        </div>
      </div>

      <div v-if="!account.loggedIn" class="empty" style="padding-top: 60px">
        <img :src="mascot" alt="" />
        <b>登录后可以和朋友联机</b>
        <p>创建房间把邀请码发给朋友，大家进同一个房间就像在一个局域网里</p>
        <button type="button" class="btn pri sm" @click="account.openAuth('login')">登录 / 注册</button>
      </div>
      <template v-else>
        <div v-if="!state.admin" class="alert warn room-alert">
          <Icon name="shield" size="s" />
          <span>进入房间要创建虚拟网卡，需要以管理员身份运行。</span>
          <button type="button" class="btn soft xs" @click="restartAdmin">以管理员身份重启</button>
        </div>
        <div v-if="state.error" class="alert bad room-alert"><Icon name="alert" size="s" /><span>{{ state.error }}</span></div>

        <section v-if="state.room" class="card room-live">
          <div class="rl-head">
            <span class="tile-ic lg ok"><Icon name="users" /></span>
            <div class="rl-t">
              <small>正在房间里</small>
              <b>{{ active?.name ?? '…' }}</b>
            </div>
            <div class="rl-ip" v-if="state.ip">
              <small>我的虚拟 IP</small>
              <button type="button" class="mono copyable" title="复制" @click="copy(state.ip, '虚拟 IP')">{{ state.ip }}<Icon name="copy" size="xs" /></button>
            </div>
            <button type="button" class="btn ghost sm" :disabled="busy === 'disc'" @click="disconnect"><Icon name="stop" size="s" />断开</button>
          </div>
          <div class="rl-peers">
            <div v-for="p in state.peers" :key="p.slot" class="rl-peer">
              <b>{{ p.nickname }}</b>
              <button type="button" class="mono copyable" title="复制" @click="copy(p.ip, p.nickname + ' 的虚拟 IP')">{{ p.ip }}<Icon name="copy" size="xs" /></button>
              <span class="bdg" :class="modeView[p.mode]?.[1]">{{ modeView[p.mode]?.[0] ?? p.mode }}</span>
            </div>
            <p v-if="!state.peers.length" class="fld-help">房间里只有你，把邀请码发给朋友吧</p>
          </div>
        </section>

        <div v-if="!rooms.length && !loading" class="empty">
          <img :src="mascot" alt="" />
          <b>还没有房间</b>
          <p>点右上角"创建房间"，把邀请码发给朋友；或者输入朋友的邀请码加入</p>
        </div>
        <div class="room-grid">
          <article v-for="r in rooms" :key="r.id" class="card room-card" :class="{ cur: r.id === state.room }">
            <div class="rc-top">
              <div class="rc-t">
                <b>{{ r.name }}</b>
                <small>房主 {{ r.ownerName }} · {{ r.members.filter((m) => m.online).length }}/{{ r.members.length }} 人在线</small>
              </div>
              <button type="button" class="mono copyable code" title="复制邀请码" @click="copy(fmtCode(r.code), '邀请码')">{{ fmtCode(r.code) }}<Icon name="copy" size="xs" /></button>
            </div>
            <div class="rc-members">
              <span v-for="m in r.members" :key="m.userId" class="rc-m" :class="{ on: m.online }" :title="m.ip">
                <i />{{ m.nickname }}<small class="mono">.{{ m.slot }}</small>
                <button
                  v-if="r.ownerId === myId && m.userId !== myId"
                  type="button"
                  class="rc-kick"
                  title="移出房间"
                  :disabled="busy === 'kick-' + m.userId"
                  @click="kick(r, m)"
                ><Icon name="x" size="xs" /></button>
              </span>
            </div>
            <div class="rc-foot">
              <button v-if="r.id === state.room" type="button" class="btn ghost xs" :disabled="busy === 'disc'" @click="disconnect">断开</button>
              <button v-else type="button" class="btn pri xs" :disabled="busy === 'go-' + r.id" :data-testid="`room-go-${r.name}`" @click="enter(r)">
                <Icon :name="busy === 'go-' + r.id ? 'refresh' : 'play'" size="xs" :class="{ spin: busy === 'go-' + r.id }" />进入
              </button>
              <span class="grow" />
              <button type="button" class="btn quiet xs danger" :disabled="busy === 'leave-' + r.id" @click="leave(r)">{{ r.ownerId === myId ? '解散' : '离开' }}</button>
            </div>
          </article>
        </div>
      </template>
    </div>

    <Modal v-model:show="modal.show" :title="modal.mode === 'create' ? '创建房间' : '加入房间'" :icon="modal.mode === 'create' ? 'plus' : 'link'" :width="400">
      <form @submit.prevent="submitModal">
        <div class="fld">
          <label for="room-input">{{ modal.mode === 'create' ? '房间名' : '邀请码' }}</label>
          <input
            id="room-input"
            v-model="modal.value"
            class="inp"
            :class="{ mono: modal.mode === 'join', err: modal.error }"
            :maxlength="modal.mode === 'create' ? 30 : 12"
            :placeholder="modal.mode === 'create' ? '例如：周末 MC' : '例如 ABCD-2345'"
            data-testid="room-input"
          />
          <span class="fld-help" :class="{ err: modal.error }">{{ modal.error || (modal.mode === 'create' ? '创建后把邀请码发给朋友' : '朋友创建房间后能看到邀请码') }}</span>
        </div>
        <button type="submit" hidden />
      </form>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="modal.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="busy === 'modal'" data-testid="room-submit" @click="submitModal">{{ modal.mode === 'create' ? '创建' : '加入' }}</button>
      </template>
    </Modal>
  </div>
</template>

<style>
.room-alert {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.room-alert span {
  flex: 1;
}
.room-live {
  padding: 14px 16px;
  margin-bottom: 16px;
}
.rl-head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.rl-t,
.rl-ip {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.rl-t {
  flex: 1;
}
.rl-t small,
.rl-ip small {
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 700;
}
.rl-t b {
  font-size: 16px;
  font-weight: 900;
}
.copyable {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 700;
  color: var(--text);
}
.copyable .ic {
  color: var(--text-3);
}
.rl-peers {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}
.rl-peer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 12px;
  background: var(--fill);
  font-size: 13px;
}
.room-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 12px;
}
.room-card {
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.room-card.cur {
  border-color: var(--pri);
}
.rc-top {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.rc-t {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.rc-t b {
  font-weight: 900;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rc-t small {
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
}
.copyable.code {
  padding: 3px 8px;
  border-radius: 8px;
  background: var(--pri-soft);
  color: var(--pri-text);
  font-size: 12.5px;
}
.rc-members {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.rc-m {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  border-radius: 99px;
  background: var(--fill);
  font-size: 12px;
  font-weight: 700;
  color: var(--text-2);
}
.rc-m i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-3);
}
.rc-m.on i {
  background: var(--ok);
}
.rc-m small {
  color: var(--text-3);
}
.rc-kick {
  display: inline-flex;
  color: var(--text-3);
}
.rc-kick:hover {
  color: var(--bad-text);
}
.rc-foot {
  display: flex;
  align-items: center;
  gap: 8px;
}
.rc-foot .grow {
  flex: 1;
}
</style>
