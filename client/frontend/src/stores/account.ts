import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  AuthService,
  Events,
  ProfileService,
  type AccountState,
  type Conflict,
  type PushError,
  type RemoteUpdate,
  type SyncStatus,
} from '../api'
import { toAppError, type AppError } from '../errors'
import { useProfilesStore } from './profiles'

export type AuthTab = 'login' | 'register' | 'reset'

/** 账号和云同步的状态（PLAN 6.4、6.6）。数据都来自 Go 端，事件驱动更新。 */
export const useAccountStore = defineStore('account', () => {
  const account = ref<AccountState>({ loggedIn: false, keepLoggedIn: false })
  const sync = ref<SyncStatus>({ state: 'off', pending: 0, conflicts: 0 })
  const conflicts = ref<Conflict[]>([])
  const remoteUpdates = ref<RemoteUpdate[]>([])
  const pushErrors = ref<PushError[]>([])
  /** 被动下线的提示（session_replaced 等），用户确认后清空。 */
  const ended = ref<AppError | null>(null)
  /** 版本过低的提示，用户关掉后本次运行不再弹。 */
  const outdatedDismissed = ref(false)
  const authDialog = ref<{ show: boolean; tab: AuthTab; nickname: string }>({ show: false, tab: 'login', nickname: '' })
  let started = false

  const loggedIn = computed(() => account.value.loggedIn)
  const nickname = computed(() => account.value.user?.nickname ?? '')
  const outdated = computed(() => (account.value.outdated ? toAppError(account.value.outdated) : null))

  async function call<T>(fn: () => Promise<T>): Promise<T> {
    try {
      return await fn()
    } catch (e) {
      throw toAppError(e)
    }
  }

  async function refreshConflicts() {
    conflicts.value = (await ProfileService.Conflicts()) ?? []
  }

  async function init() {
    if (started) return
    started = true
    const profiles = useProfilesStore()
    Events.On('auth:state', (ev) => {
      if (ev.data) account.value = ev.data
    })
    Events.On('auth:expired', (ev) => {
      if (ev.data) ended.value = toAppError(ev.data)
      conflicts.value = []
    })
    Events.On('sync:status', (ev) => {
      if (!ev.data) return
      const prev = sync.value
      sync.value = ev.data
      // 推送完成、进入离线等都会改变列表上每份配置的同步标记，重新读一次列表（本地调用，很快）
      if (prev.state !== ev.data.state || prev.pending !== ev.data.pending || prev.conflicts !== ev.data.conflicts) {
        profiles.loadList().catch(() => {})
      }
    })
    Events.On('sync:changed', (ev) => {
      profiles.refreshAfterSync(ev.data?.profileIds ?? []).catch(() => {})
      refreshConflicts().catch(() => {})
    })
    Events.On('sync:conflict', () => {
      refreshConflicts().catch(() => {})
      profiles.loadList().catch(() => {})
    })
    Events.On('sync:remote-update', (ev) => {
      if (!ev.data) return
      remoteUpdates.value = [...remoteUpdates.value.filter((r) => r.profileId !== ev.data!.profileId), ev.data]
    })
    Events.On('sync:error', (ev) => {
      if (ev.data) pushErrors.value = [...pushErrors.value, ev.data]
      profiles.loadList().catch(() => {})
    })
    const [a, s] = await Promise.all([AuthService.Account(), AuthService.SyncStatus()])
    account.value = a
    sync.value = s
    if (a.ended) ended.value = toAppError(a.ended)
    await refreshConflicts()
    remoteUpdates.value = (await ProfileService.RemoteUpdates()) ?? []
  }

  function openAuth(tab: AuthTab = 'login', nick = '') {
    authDialog.value = { show: true, tab, nickname: nick || account.value.cacheOwner || '' }
  }

  async function login(nick: string, password: string, keep: boolean) {
    account.value = await call(() => AuthService.Login(nick, password, keep))
    ended.value = null
    outdatedDismissed.value = false
    await useProfilesStore().loadList()
  }

  const register = (nick: string, password: string, invite: string) => call(() => AuthService.Register(nick, password, invite))
  const resetPassword = (nick: string, code: string, password: string) => call(() => AuthService.ResetPassword(nick, code, password))
  const changePassword = (oldPw: string, newPw: string) => call(() => AuthService.ChangePassword(oldPw, newPw))
  const logoutInfo = () => call(() => AuthService.LogoutInfo())

  async function logout() {
    await call(() => AuthService.Logout())
    conflicts.value = []
    remoteUpdates.value = []
    account.value = await AuthService.Account()
    await useProfilesStore().refreshAfterSync([])
  }

  const syncNow = () => call(() => AuthService.SyncNow())

  async function resolve(id: string, keep: 'local' | 'cloud') {
    const res = await call(() => ProfileService.ResolveConflict(id, keep))
    conflicts.value = conflicts.value.filter((c) => c.profileId !== id)
    await useProfilesStore().refreshAfterSync([id])
    return res
  }

  async function applyRemote(id: string) {
    remoteUpdates.value = remoteUpdates.value.filter((r) => r.profileId !== id)
    return call(() => ProfileService.ApplyRemote(id))
  }

  async function dismissRemote(id: string) {
    remoteUpdates.value = remoteUpdates.value.filter((r) => r.profileId !== id)
    await call(() => ProfileService.DismissRemote(id))
  }

  return {
    account,
    sync,
    conflicts,
    remoteUpdates,
    pushErrors,
    ended,
    outdatedDismissed,
    authDialog,
    loggedIn,
    nickname,
    outdated,
    init,
    openAuth,
    login,
    register,
    resetPassword,
    changePassword,
    logoutInfo,
    logout,
    syncNow,
    resolve,
    applyRemote,
    dismissRemote,
    refreshConflicts,
  }
})
