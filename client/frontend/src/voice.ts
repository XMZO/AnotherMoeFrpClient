import { reactive } from 'vue'
import { Events, VnetService } from './api'

// 联机房间的语音（像 Discord 的语音频道，轻量版）：每两个人之间一条 WebRTC 连接（网状，房间小够用）。
//   - 只用虚拟网的地址（10.241.0.x）连：直连、节点中继、面板中继都能通，不暴露真实 IP，不需要 STUN/TURN。
//   - 降噪 / 回声消除 / 自动增益用 WebRTC 自带的（WebView2 = Chromium），编码 Opus。
//   - 协商（offer/answer）经面板的 signal 转发（VnetService.VoiceSignal，事件 vnet:voice）。
//   - 房主可以关掉语音（settings.noVoice）或禁言某个座位（settings.muted）：被禁言的人发不出声，别人也不播他的声音。
// 进语音：hello 广播给在线成员；收到 hello 的回 here；两边都知道对方在语音里后，座位号小的发 offer。

interface Msg {
  t: 'voice'
  k: 'hello' | 'here' | 'bye' | 'offer' | 'answer' | 'state'
  sdp?: string
  mic?: boolean
}

interface Peer {
  pc: RTCPeerConnection
  audio?: HTMLAudioElement
  level?: () => number
}

interface VnetPeerLite {
  slot: number
  mode: string
}

interface VnetLite {
  room: string
  self: number
  peers: VnetPeerLite[]
  settings: { noVoice?: boolean; muted?: number[] | null }
}

export const voice = reactive({
  joined: false,
  joining: false,
  mic: true, // 我想开麦（被禁言时实际不发声）
  deaf: false, // 关掉扬声器（听不到别人，也自动闭麦）
  hasMic: true, // 有没有拿到麦克风（没有就只能听）
  members: {} as Record<number, { mic: boolean }>, // 语音里的其他人：座位号 → 是否开麦
  speaking: {} as Record<number, boolean>, // 正在说话（含自己）
  noVoice: false,
  muted: [] as number[],
  self: 0,
  error: '',
})

let room = ''
let online = new Set<number>()
let local: MediaStream | null = null
let localLevel: (() => number) | null = null
let ctx: AudioContext | null = null
const peers = new Map<number, Peer>()
let timer: number | undefined

const audioCtx = () => (ctx ??= new AudioContext())

// 音量检测（说话指示）：返回 0–1 的 RMS
function meter(stream: MediaStream): () => number {
  const c = audioCtx()
  const src = c.createMediaStreamSource(stream)
  const an = c.createAnalyser()
  an.fftSize = 512
  src.connect(an)
  const buf = new Float32Array(an.fftSize)
  return () => {
    an.getFloatTimeDomainData(buf)
    let sum = 0
    for (const v of buf) sum += v * v
    return Math.sqrt(sum / buf.length)
  }
}

function send(slot: number, m: Omit<Msg, 't'>) {
  VnetService.VoiceSignal(slot, JSON.stringify({ t: 'voice', ...m })).catch(() => {})
}
function broadcast(m: Omit<Msg, 't'>) {
  for (const s of online) send(s, m)
}

const selfMuted = () => voice.muted.includes(voice.self)
const micLive = () => voice.joined && voice.mic && !voice.deaf && !selfMuted() && !voice.noVoice

function applyLocal() {
  local?.getAudioTracks().forEach((t) => (t.enabled = micLive()))
}
function applyRemote(slot: number) {
  const p = peers.get(slot)
  if (p?.audio) p.audio.muted = voice.deaf || voice.muted.includes(slot)
}

// 只留虚拟网（10.241.0.x）和 mDNS（.local）的候选地址
function filterSdp(sdp: string): string {
  return sdp
    .split('\r\n')
    .filter((l) => {
      if (!l.startsWith('a=candidate:')) return true
      const addr = l.split(' ')[4] ?? ''
      return addr.startsWith('10.241.0.') || addr.endsWith('.local')
    })
    .join('\r\n')
}

// 等候选地址收集完（不用 trickle，一条消息带全部），最多 2 秒
function gathered(pc: RTCPeerConnection): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise((resolve) => {
    const t = setTimeout(resolve, 2000)
    pc.addEventListener('icegatheringstatechange', () => {
      if (pc.iceGatheringState === 'complete') {
        clearTimeout(t)
        resolve()
      }
    })
  })
}

function newPeer(slot: number): Peer {
  closePeer(slot)
  const pc = new RTCPeerConnection({ iceServers: [] })
  const p: Peer = { pc }
  peers.set(slot, p)
  if (local) for (const t of local.getAudioTracks()) pc.addTrack(t, local)
  else pc.addTransceiver('audio', { direction: 'recvonly' })
  pc.ontrack = (ev) => {
    const stream = ev.streams[0] ?? new MediaStream([ev.track])
    const a = new Audio()
    a.autoplay = true
    a.srcObject = stream
    p.audio = a
    applyRemote(slot)
    a.play().catch(() => {})
    try {
      p.level = meter(stream)
    } catch {
      /* 拿不到音量也能听 */
    }
  }
  pc.onconnectionstatechange = () => {
    if (pc.connectionState === 'failed' && peers.get(slot)?.pc === pc) {
      closePeer(slot)
      // 座位号小的过一会重连
      if (voice.joined && voice.self < slot && voice.members[slot]) setTimeout(() => voice.joined && !peers.has(slot) && call(slot), 3000)
    }
  }
  return p
}

function closePeer(slot: number) {
  const p = peers.get(slot)
  if (!p) return
  peers.delete(slot)
  p.pc.close()
  if (p.audio) {
    p.audio.pause()
    p.audio.srcObject = null
  }
  delete voice.speaking[slot]
}

async function call(slot: number) {
  const { pc } = newPeer(slot)
  await pc.setLocalDescription(await pc.createOffer())
  await gathered(pc)
  if (peers.get(slot)?.pc !== pc) return
  send(slot, { k: 'offer', sdp: filterSdp(pc.localDescription!.sdp) })
}

async function onMsg(from: number, m: Msg) {
  if (!voice.joined) return
  switch (m.k) {
    case 'hello':
    case 'here':
      if (m.k === 'hello') closePeer(from) // 他刚进语音（或重进）：旧连接作废
      voice.members[from] = { mic: !!m.mic }
      if (m.k === 'hello') send(from, { k: 'here', mic: micLive() })
      if (voice.self < from && !peers.has(from)) await call(from)
      break
    case 'state':
      if (voice.members[from]) voice.members[from].mic = !!m.mic
      break
    case 'bye':
      delete voice.members[from]
      closePeer(from)
      break
    case 'offer': {
      voice.members[from] ??= { mic: true }
      const { pc } = newPeer(from)
      await pc.setRemoteDescription({ type: 'offer', sdp: m.sdp! })
      await pc.setLocalDescription(await pc.createAnswer())
      await gathered(pc)
      if (peers.get(from)?.pc !== pc) return
      send(from, { k: 'answer', sdp: filterSdp(pc.localDescription!.sdp) })
      break
    }
    case 'answer': {
      const p = peers.get(from)
      if (p && p.pc.signalingState === 'have-local-offer') await p.pc.setRemoteDescription({ type: 'answer', sdp: m.sdp! })
      break
    }
  }
}

function tick() {
  const th = 0.02
  voice.speaking[voice.self] = !!localLevel && micLive() && localLevel() > th
  for (const [slot, p] of peers) voice.speaking[slot] = !!p.level && !voice.muted.includes(slot) && p.level() > th
}

// 进语音
export async function joinVoice() {
  if (voice.joined || voice.joining || !room || voice.noVoice) return
  voice.joining = true
  voice.error = ''
  try {
    try {
      local = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
      })
      voice.hasMic = true
      localLevel = meter(local)
    } catch {
      local = null // 没有麦克风或被系统禁用：只听
      voice.hasMic = false
      voice.error = '没拿到麦克风，只能听'
    }
    await audioCtx().resume()
    voice.joined = true
    applyLocal()
    broadcast({ k: 'hello', mic: micLive() })
    timer = window.setInterval(tick, 120)
  } finally {
    voice.joining = false
  }
}

// 退出语音
export function leaveVoice() {
  if (!voice.joined) return
  broadcast({ k: 'bye' })
  for (const s of [...peers.keys()]) closePeer(s)
  local?.getTracks().forEach((t) => t.stop())
  local = null
  localLevel = null
  clearInterval(timer)
  voice.joined = false
  voice.members = {}
  voice.speaking = {}
}

export function setMic(on: boolean) {
  voice.mic = on
  if (on && voice.deaf) voice.deaf = false
  for (const s of peers.keys()) applyRemote(s)
  applyLocal()
  if (voice.joined) broadcast({ k: 'state', mic: micLive() })
}

export function setDeaf(on: boolean) {
  voice.deaf = on
  for (const s of peers.keys()) applyRemote(s)
  applyLocal()
  if (voice.joined) broadcast({ k: 'state', mic: micLive() })
}

function onState(st: VnetLite) {
  if (st.room !== room) {
    leaveVoice()
    room = st.room
  }
  voice.self = st.self
  const was = { noVoice: voice.noVoice, muted: voice.muted.join() }
  voice.noVoice = !!st.settings?.noVoice
  voice.muted = st.settings?.muted ?? []
  if (voice.noVoice && voice.joined) leaveVoice()
  const now = new Set(st.peers.filter((p) => p.mode !== 'offline').map((p) => p.slot))
  for (const s of online) {
    if (!now.has(s)) {
      delete voice.members[s]
      closePeer(s)
    }
  }
  // 新上线的人：告诉他我在语音里
  if (voice.joined) for (const s of now) if (!online.has(s)) send(s, { k: 'hello', mic: micLive() })
  online = now
  if (was.muted !== voice.muted.join() || was.noVoice !== voice.noVoice) {
    for (const s of peers.keys()) applyRemote(s)
    applyLocal()
    if (voice.joined) broadcast({ k: 'state', mic: micLive() })
  }
}

let started = false
// initVoice 在 Shell 里调用一次：订阅联机状态和语音协商消息
export async function initVoice() {
  if (started) return
  started = true
  Events.On('vnet:state', (ev: any) => onState(ev.data as VnetLite))
  Events.On('vnet:voice', (ev: any) => {
    const d = ev.data as { from: number; data: string }
    try {
      const m = JSON.parse(d.data) as Msg
      if (m.t === 'voice') onMsg(d.from, m).catch(() => {})
    } catch {
      /* 坏消息忽略 */
    }
  })
  try {
    onState((await VnetService.State()) as unknown as VnetLite)
  } catch {
    /* 没关系，等事件 */
  }
}
