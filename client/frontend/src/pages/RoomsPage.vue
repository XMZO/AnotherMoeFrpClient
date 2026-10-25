<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Clipboard } from '@wailsio/runtime'
import Icon from '../ui/Icon.vue'
import Modal from '../ui/Modal.vue'
import Select from '../ui/Select.vue'
import Switch from '../ui/Switch.vue'
import Check from '../ui/Check.vue'
import { dialog, message } from '../ui/feedback'
import { Events, VnetService } from '../api'
import { useAccountStore } from '../stores/account'
import { describeError, toAppError } from '../errors'
import mascot from '../assets/mascot.png'

// 联机房间：和朋友组成虚拟局域网（客户端 internal/vnet，面板 shared/api/vnet.go，docs/vnet.md）。
// 进页面先把关：没登录 → 登录 / 游客加入 / 返回；没有管理员权限 → 以管理员身份重启（装驱动）/ 返回；
// 有权限但驱动没装 → 自动装。
const emit = defineEmits<{ back: [] }>()

interface Member { userId: string; nickname: string; slot: number; ip: string; online: boolean }
interface Settings { allowGuests: boolean; relay: string; relayLocked: boolean }
interface Room extends Settings { id: string; name: string; code: string; ownerId: string; ownerName: string; members: Member[] }
interface PeerState { slot: number; nickname: string; ip: string; mode: string }
interface RelayView { id: string; name: string; region?: string; latency: number }
interface VnetState {
  admin: boolean
  driver: boolean
  room: string
  self: number
  ip: string
  peers: PeerState[]
  relays: RelayView[]
  home: string
  pref: string
  settings: Settings
  guest?: string
  error?: string
}

const account = useAccountStore()
const rooms = ref<Room[]>([])
const loading = ref(false)
const busy = ref('')
const state = ref<VnetState>({ admin: true, driver: true, room: '', self: 0, ip: '', peers: [], relays: [], home: '', pref: 'auto', settings: { allowGuests: false, relay: '', relayLocked: false } })
const installing = ref(false)
const installErr = ref('')
const modal = ref<{ show: boolean; mode: 'create' | 'join' | 'guest'; value: string; nick: string; error: string }>({ show: false, mode: 'create', value: '', nick: '', error: '' })
const setDlg = ref<{ show: boolean; room: Room | null; allowGuests: boolean; relay: string; relayLocked: boolean; relays: { id: string; name: string }[] }>({
  show: false,
  room: null,
  allowGuests: false,
  relay: '',
  relayLocked: false,
  relays: [],
})

const myId = computed(() => account.account.user?.id ?? '')
const guest = computed(() => !!state.value.guest)
const usable = computed(() => account.loggedIn || guest.value)
const active = computed(() => rooms.value.find((r) => r.id === state.value.room) ?? null)
const isOwner = (r: Room | null) => !!r && !guest.value && r.ownerId === myId.value
const modeView: Record<string, [string, string]> = {
  direct: ['直连', 'ok'],
  relay: ['中继', 'gold'],
  connecting: ['打洞中', 'info'],
  offline: ['不在线', ''],
}
const fmtCode = (c: string) => (c.length === 8 ? `${c.slice(0, 4)}-${c.slice(4)}` : c)
const latencyText = (l: number) => (l > 0 ? `${l} ms` : l < 0 ? '连不上' : '测速中')
const titles = { create: '创建房间', join: '加入房间', guest: '游客加入' }
const hints = { create: '创建后把邀请码发给朋友', join: '朋友创建房间后能看到邀请码', guest: '房主要先在房间设置里打开"允许游客"' }

// 收件中继的选项：自动（显示实际选中的）、面板、各节点（带延迟）
const relayOptions = computed(() => {
  const st = state.value
  const homeName = st.relays.find((r) => r.id === st.home)?.name ?? '面板'
  return [
    { value: 'auto', label: `自动（${homeName}）` },
    { value: 'panel', label: '面板（不用节点）' },
    ...st.relays.map((r) => ({ value: r.id, label: `${r.name} · ${latencyText(r.latency)}` })),
  ]
})
const relayLocked = computed(() => state.value.settings.relayLocked)
const settingsRelayOptions = computed(() => [{ value: '', label: '自动（每人选最近的节点）' }, ...setDlg.value.relays.map((x) => ({ value: x.id, label: x.name }))])

function confirm(o: { title: string; content: string; positiveText: string; danger?: boolean }) {
  return new Promise<boolean>((resolve) =>
    dialog.warning({ ...o, icon: 'alert', negativeText: '取消', onPositiveClick: () => resolve(true), onNegativeClick: () => resolve(false) }),
  )
}

async function load() {
  if (!usable.value) return
  loading.value = true
  try {
    rooms.value = ((await VnetService.Rooms()) ?? []) as Room[]
  } catch (e) {
    message.error(describeError(e))
  } finally {
    loading.value = false
  }
}

// 有管理员权限但驱动没装：进页面就自动装
async function ensureDriver() {
  const st = state.value
  if (!st.admin || st.driver || installing.value) return
  installing.value = true
  installErr.value = ''
  try {
    await VnetService.InstallDriver()
    state.value = (await VnetService.State()) as VnetState
  } catch (e) {
    installErr.value = describeError(e)
  } finally {
    installing.value = false
  }
}

let off: (() => void) | undefined
let reloadTimer: ReturnType<typeof setTimeout> | undefined
onMounted(async () => {
  off = Events.On('vnet:state', (ev: any) => {
    state.value = ev.data as VnetState
    clearTimeout(reloadTimer)
    reloadTimer = setTimeout(load, 800)
  })
  state.value = (await VnetService.State()) as VnetState
  ensureDriver()
  load()
})
onBeforeUnmount(() => {
  off?.()
  clearTimeout(reloadTimer)
})
watch(usable, (v) => (v ? load() : (rooms.value = [])))

async function act(key: string, fn: () => Promise<unknown>, ok?: string) {
  busy.value = key
  try {
    await fn()
    if (ok) message.success(ok)
    await load()
  } catch (e) {
    const err = toAppError(e)
    if (err.code === 'vnet_need_admin') restartAdmin()
    else message.error(describeError(err))
  } finally {
    busy.value = ''
  }
}

async function restartAdmin() {
  try {
    await VnetService.RestartAsAdmin()
  } catch (e) {
    message.error('没有重启：' + describeError(e))
  }
}

function openModal(mode: 'create' | 'join' | 'guest') {
  modal.value = { show: true, mode, value: '', nick: '', error: '' }
}

async function submitModal() {
  const m = modal.value
  const v = m.value.trim()
  if (!v) {
    m.error = m.mode === 'create' ? '给房间起个名字' : '输入朋友发给你的邀请码'
    return
  }
  if (m.mode === 'guest' && !m.nick.trim()) {
    m.error = '起个昵称，房间里的人看到的是它'
    return
  }
  busy.value = 'modal'
  try {
    if (m.mode === 'create') {
      const r = (await VnetService.CreateRoom(v)) as Room
      message.success(`房间已创建，邀请码 ${fmtCode(r.code)}`)
    } else if (m.mode === 'join') {
      await VnetService.JoinRoom(v)
      message.success('已加入房间')
    } else {
      await VnetService.GuestJoin(v, m.nick.trim())
      message.success('已以游客身份加入')
      state.value = (await VnetService.State()) as VnetState
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
  const owner = isOwner(r)
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

async function openSettings(r: Room) {
  setDlg.value = { show: true, room: r, allowGuests: r.allowGuests, relay: r.relay, relayLocked: r.relayLocked, relays: [] }
  try {
    setDlg.value.relays = ((await VnetService.RoomRelays()) ?? []).map((x: any) => ({ id: x.id, name: x.name }))
  } catch {
    /* 拿不到就只有"自动" */
  }
}

async function saveSettings() {
  const d = setDlg.value
  if (!d.room) return
  busy.value = 'settings'
  try {
    await VnetService.UpdateRoom(d.room.id, { allowGuests: d.allowGuests, relay: d.relay, relayLocked: d.relayLocked })
    message.success('房间设置已保存')
    d.show = false
    await load()
  } catch (e) {
    message.error(describeError(e))
  } finally {
    busy.value = ''
  }
}

async function copy(text: string, what: string) {
  await Clipboard.SetText(text)
  message.success(`${what}已复制`)
}

// 房间卡片最多列出这么多成员（在线的在前），其余显示 +N，卡片高度不跟着人数长
const CARD_MEMBERS = 8
const shownMembers = (r: Room) => [...r.members].sort((a, b) => Number(b.online) - Number(a.online)).slice(0, CARD_MEMBERS)
const memberOf = (slot: number) => active.value?.members.find((m) => m.slot === slot)

const disconnect = () => act('disc', () => VnetService.Disconnect())
const enter = (r: Room) => act('go-' + r.id, () => VnetService.Connect(r.id))
const setRelay = (v: string) => VnetService.SetRelay(v)
const back = () => emit('back')
const login = () => account.openAuth('login')
</script>

<template>
  <div class="page dotted-bg">
    <div class="inner rooms-page">
      <div class="pg-head head">
        <div>
          <h1>联机房间 <span v-if="guest" class="bdg gold">游客</span></h1>
          <p>和朋友组成一个虚拟局域网：游戏里找"局域网"房间，或者直接连对方的虚拟 IP。优先打洞直连，连不上自动经节点或面板中继。</p>
        </div>
        <div v-if="usable && state.admin && state.driver" class="pg-actions">
          <button type="button" class="btn ghost sm" :disabled="loading" @click="load"><Icon name="refresh" size="s" :class="{ spin: loading }" />刷新</button>
          <template v-if="!guest">
            <button type="button" class="btn soft sm" data-testid="room-join" @click="openModal('join')"><Icon name="link" size="s" />加入房间</button>
            <button type="button" class="btn pri sm" data-testid="room-create" @click="openModal('create')"><Icon name="plus" size="s" />创建房间</button>
          </template>
        </div>
      </div>

      <div v-if="!usable" class="card gate">
        <span class="tile-ic lg info"><Icon name="users" /></span>
        <b>联机需要登录，或者用邀请码以游客身份加入</b>
        <p>登录后可以创建房间、加入多个房间；游客只能进房主允许游客的那个房间。</p>
        <div class="gate-btns">
          <button type="button" class="btn quiet sm" @click="back">返回</button>
          <button type="button" class="btn soft sm" data-testid="room-guest" @click="openModal('guest')"><Icon name="link" size="s" />游客加入</button>
          <button type="button" class="btn pri sm" @click="login"><Icon name="user" size="s" />登录 / 注册</button>
        </div>
      </div>
      <div v-else-if="!state.admin" class="card gate">
        <span class="tile-ic lg gold"><Icon name="shield" /></span>
        <b>联机要安装虚拟网卡驱动（Wintun），需要管理员权限</b>
        <p>点"安装"会以管理员身份重新启动程序（系统会弹出权限确认），重启后自动装好驱动并回到这里。驱动只装一次。</p>
        <div class="gate-btns">
          <button type="button" class="btn quiet sm" @click="back">返回</button>
          <button type="button" class="btn pri sm" data-testid="room-elevate" @click="restartAdmin"><Icon name="shield" size="s" />安装</button>
        </div>
      </div>
      <div v-else-if="!state.driver" class="card gate">
        <span class="tile-ic lg info"><Icon :name="installing ? 'refresh' : 'alert'" :class="{ spin: installing }" /></span>
        <b>{{ installing ? '正在安装虚拟网卡驱动…' : '虚拟网卡驱动没装好' }}</b>
        <p v-if="installErr">{{ installErr }}</p>
        <div v-if="!installing" class="gate-btns">
          <button type="button" class="btn quiet sm" @click="back">返回</button>
          <button type="button" class="btn pri sm" @click="ensureDriver"><Icon name="refresh" size="s" />重试</button>
        </div>
      </div>

      <template v-else>
        <div v-if="state.error" class="alert bad room-alert"><Icon name="alert" size="s" /><span>{{ state.error }}</span></div>

        <section v-if="state.room" class="card room-live">
          <div class="rl-head">
            <span class="tile-ic lg ok"><Icon name="users" /></span>
            <div class="rl-t">
              <small>正在房间里</small>
              <b>{{ active?.name ?? '…' }} <span v-if="active" class="bdg" :class="active.allowGuests ? 'info' : ''">{{ active.allowGuests ? '允许游客' : '禁止游客' }}</span></b>
            </div>
            <div v-if="state.ip" class="rl-ip">
              <small>我的虚拟 IP</small>
              <button type="button" class="mono copyable bigip" title="复制" @click="copy(state.ip, '虚拟 IP')">{{ state.ip }}<Icon name="copy" size="s" /></button>
            </div>
            <div v-if="active" class="rl-ip">
              <small>邀请码（发给朋友）</small>
              <button type="button" class="mono copyable code big" title="复制邀请码" @click="copy(fmtCode(active.code), '邀请码')">{{ fmtCode(active.code) }}<Icon name="copy" size="s" /></button>
            </div>
            <button v-if="isOwner(active)" type="button" class="btn soft sm" @click="openSettings(active!)"><Icon name="settings" size="s" />房间设置</button>
            <button type="button" class="btn ghost sm" :disabled="busy === 'disc'" @click="disconnect"><Icon name="stop" size="s" />断开</button>
          </div>
          <div class="rl-relay">
            <span class="lbl">收件中继</span>
            <Select class="rl-relay-sel" :model-value="relayLocked ? state.home || 'panel' : state.pref" :options="relayOptions" :disabled="relayLocked" @change="setRelay" />
            <span class="fld-help">{{ relayLocked ? '房主锁定了中继' : '打洞打不通时，别人发给你的数据经它转发；"自动"选最近的' }}</span>
          </div>
          <div class="rl-peers">
            <div v-for="p in state.peers" :key="p.slot" class="rl-peer">
              <b :title="p.nickname">{{ p.nickname }}</b>
              <button type="button" class="mono copyable peerip" title="复制" @click="copy(p.ip, p.nickname + ' 的虚拟 IP')">{{ p.ip }}<Icon name="copy" size="xs" /></button>
              <span class="bdg" :class="modeView[p.mode]?.[1]">{{ modeView[p.mode]?.[0] ?? p.mode }}</span>
              <button v-if="isOwner(active) && memberOf(p.slot)" type="button" class="rc-kick" title="移出房间" @click="kick(active!, memberOf(p.slot)!)"><Icon name="x" size="xs" /></button>
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
              <b class="rc-name">{{ r.name }}</b>
            </div>
            <small class="rc-meta">房主 {{ r.ownerName }} · {{ r.members.filter((m) => m.online).length }}/{{ r.members.length }} 人在线</small>
            <div class="rc-members">
              <span v-for="m in shownMembers(r)" :key="m.userId" class="rc-m" :class="{ on: m.online }" :title="m.ip">
                <i />{{ m.nickname }}<small class="mono">.{{ m.slot }}</small>
                <button v-if="isOwner(r) && m.userId !== myId" type="button" class="rc-kick" title="移出房间" :disabled="busy === 'kick-' + m.userId" @click="kick(r, m)">
                  <Icon name="x" size="xs" />
                </button>
              </span>
              <span v-if="r.members.length > CARD_MEMBERS" class="rc-m more" title="进入房间后在上面看完整名单">+{{ r.members.length - CARD_MEMBERS }}</span>
            </div>
            <div class="rc-foot">
              <button v-if="r.id === state.room" type="button" class="btn ghost xs" :disabled="busy === 'disc'" @click="disconnect">断开</button>
              <button v-else type="button" class="btn pri xs" :disabled="busy === 'go-' + r.id" :data-testid="`room-go-${r.name}`" @click="enter(r)">
                <Icon :name="busy === 'go-' + r.id ? 'refresh' : 'play'" size="xs" :class="{ spin: busy === 'go-' + r.id }" />进入
              </button>
              <span class="grow" />
              <button type="button" class="btn quiet xs danger" :disabled="busy === 'leave-' + r.id" @click="leave(r)">{{ isOwner(r) ? '解散' : '离开' }}</button>
            </div>
          </article>
        </div>
      </template>
    </div>

    <Modal v-model:show="modal.show" :title="titles[modal.mode]" :icon="modal.mode === 'create' ? 'plus' : 'link'" :width="400">
      <form @submit.prevent="submitModal">
        <div class="fld">
          <label for="room-input">{{ modal.mode === 'create' ? '房间名' : '邀请码' }}</label>
          <input
            id="room-input"
            v-model="modal.value"
            class="inp"
            :class="{ mono: modal.mode !== 'create', err: modal.error }"
            :maxlength="modal.mode === 'create' ? 30 : 12"
            :placeholder="modal.mode === 'create' ? '例如：周末 MC' : '例如 ABCD-2345'"
            data-testid="room-input"
          />
        </div>
        <div v-if="modal.mode === 'guest'" class="fld">
          <label for="room-nick">昵称</label>
          <input id="room-nick" v-model="modal.nick" class="inp" maxlength="20" placeholder="房间里的人看到的名字" data-testid="room-nick" />
        </div>
        <span class="fld-help" :class="{ err: modal.error }">{{ modal.error || hints[modal.mode] }}</span>
        <button type="submit" hidden />
      </form>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="modal.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="busy === 'modal'" data-testid="room-submit" @click="submitModal">{{ modal.mode === 'create' ? '创建' : '加入' }}</button>
      </template>
    </Modal>

    <Modal v-model:show="setDlg.show" title="房间设置" icon="settings" :width="440">
      <div class="sw-line">
        <div class="set-txt"><b>允许游客</b><small>没登录的人也能用邀请码以游客身份进来</small></div>
        <Switch v-model="setDlg.allowGuests" label="允许游客" />
      </div>
      <div class="fld" style="margin-top: 12px">
        <span class="lbl">默认中继</span>
        <Select v-model="setDlg.relay" :options="settingsRelayOptions" />
        <Check v-model="setDlg.relayLocked" style="margin-top: 8px">锁定：所有人只能用这个中继（不锁定时成员可以自己换）</Check>
      </div>
      <template #footer>
        <button type="button" class="btn quiet sm" @click="setDlg.show = false">取消</button>
        <button type="button" class="btn pri sm" :disabled="busy === 'settings'" @click="saveSettings">保存</button>
      </template>
    </Modal>
  </div>
</template>

<style>
.gate {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  max-width: 520px;
  margin: 40px auto 0;
  padding: 28px 24px;
  text-align: center;
}
.gate b {
  font-size: 16px;
  font-weight: 900;
}
.gate p {
  font-size: 13px;
  color: var(--text-3);
  line-height: 1.6;
}
.gate-btns {
  display: flex;
  gap: 8px;
  margin-top: 6px;
}
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
.rl-relay {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
}
.rl-relay .lbl {
  font-size: 12.5px;
  font-weight: 800;
  white-space: nowrap;
}
.rl-relay-sel {
  width: 240px;
  height: 34px;
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
/* 成员多的时候是紧凑的多列网格，超过 4 行在框里滚动，页面不会越拉越长 */
.rl-peers {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
  align-content: start;
  gap: 6px;
  margin-top: 12px;
  max-height: 168px; /* 最多 4 行，再多在框里滚动 */
  overflow-y: auto;
  padding-right: 2px;
}
.rl-peers > .fld-help {
  grid-column: 1 / -1;
}
.rl-peer {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 5px 10px;
  border-radius: 12px;
  background: var(--fill);
  font-size: 13px;
}
.rl-peer b {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rc-m.more {
  color: var(--text-3);
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
  align-items: center;
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
.rc-name {
  flex: 1;
  min-width: 0;
  font-weight: 900;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rc-meta {
  margin-top: -6px;
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
}
.rc-code {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.copyable.code {
  padding: 3px 8px;
  border-radius: 8px;
  background: var(--pri-soft);
  color: var(--pri-text);
  font-size: 12.5px;
}
.copyable.bigip {
  font-size: 20px;
  font-weight: 800;
}
.copyable.peerip {
  font-size: 15px;
}
.copyable.code.big {
  padding: 6px 14px;
  border-radius: 12px;
  font-size: 20px;
  font-weight: 800;
  letter-spacing: 1px;
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
