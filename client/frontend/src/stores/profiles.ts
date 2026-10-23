import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { ProfileService, ShareService, type Profile, type ProfileSummary, type SaveResult, type SubscriptionParams } from '../api'
import { toAppError } from '../errors'
import { clone } from '../util'

export type Collection = 'nodes' | 'proxies' | 'visitors'

export const useProfilesStore = defineStore('profiles', () => {
  const list = ref<ProfileSummary[]>([])
  const current = ref<Profile | null>(null)
  const currentId = ref('')
  const loading = ref(false)

  const nodes = computed(() => current.value?.nodes ?? [])
  const currentSummary = computed(() => list.value.find((p) => p.id === currentId.value))
  /** 当前选中的是订阅（PLAN 7.6）：内容由分享决定，只有模板订阅的代理和 P2P 的本地绑定可以改。 */
  const isSubscription = computed(() => currentSummary.value?.origin === 'subscription')

  /** 某一类条目在当前配置里是否只读。 */
  function readonly(col: Collection): boolean {
    if (!isSubscription.value) return false
    return !(col === 'proxies' && currentSummary.value?.shareKind === 'template')
  }

  async function call<T>(fn: () => Promise<T>): Promise<T> {
    try {
      return await fn()
    } catch (e) {
      throw toAppError(e)
    }
  }

  async function loadList() {
    list.value = (await call(() => ProfileService.List())) ?? []
  }

  async function select(id: string) {
    currentId.value = id
    if (!id) {
      current.value = null
      return
    }
    loading.value = true
    try {
      current.value = await call(() => ProfileService.Get(id))
    } finally {
      loading.value = false
    }
  }

  /** 新建配置。origin 为 cloud 时属于账号并同步到云端，local 时只在本机。 */
  async function create(name: string, origin: 'local' | 'cloud' = 'local') {
    const p = await call(() => ProfileService.Create(name, origin))
    await loadList()
    await select(p.id)
    return p
  }

  /** 把本地配置上传到云端（变成云端配置，ID 不变）。 */
  async function upload(id: string) {
    const s = await call(() => ProfileService.Upload(id))
    await loadList()
    return s
  }

  /**
   * 同步后刷新：重新读列表；当前配置被云端改动（ids 里有它）就重新读取，被删掉了就选第一个。
   */
  async function refreshAfterSync(ids: string[]) {
    await loadList()
    const cur = currentId.value
    if (cur && !list.value.some((p) => p.id === cur)) {
      await select(list.value[0]?.id ?? '')
    } else if (cur && ids.includes(cur)) {
      await select(cur)
    } else if (!cur && list.value.length) {
      await select(list.value[0].id)
    }
  }

  async function duplicate(id: string, name: string) {
    const p = await call(() => ProfileService.Duplicate(id, name))
    await loadList()
    await select(p.id)
    return p
  }

  async function remove(id: string) {
    const sum = list.value.find((p) => p.id === id)
    if (sum?.origin === 'subscription') await call(() => ShareService.Unsubscribe(id))
    else await call(() => ProfileService.Delete(id))
    await loadList()
    if (currentId.value === id) await select(list.value[0]?.id ?? '')
  }

  /** 修改订阅参数，然后重新读取订阅内容（服务端组装好的 runtime）。 */
  async function updateSubscription(id: string, params: SubscriptionParams): Promise<SaveResult> {
    const res = await call(() => ShareService.UpdateSubscription(id, params))
    await loadList()
    if (currentId.value === id) await select(id)
    return { profile: current.value!, applied: res.applied, applyError: res.applyError } as SaveResult
  }

  /** 保存整份配置。失败（例如重名）时抛出 AppError，当前配置不变。 */
  async function save(p: Profile): Promise<SaveResult> {
    const sum = list.value.find((x) => x.id === p.id)
    if (sum?.origin === 'subscription') {
      // 模板订阅：代理是订阅者自己的，作为订阅参数保存到云端
      if (sum.shareKind !== 'template') throw toAppError({ code: 'subscription_read_only', message: '' })
      const cur = await call(() => ShareService.Subscription(p.id))
      return updateSubscription(p.id, { nodeId: cur.params.nodeId, proxies: p.proxies ?? [], binds: [] })
    }
    const res = await call(() => ProfileService.Save(p))
    if (currentId.value === res.profile.id) current.value = res.profile
    await loadList()
    return res
  }

  function editable(): Profile {
    if (!current.value) throw toAppError({ code: 'profile_not_found', message: '' })
    return clone(current.value)
  }

  async function rename(name: string) {
    return save({ ...editable(), name })
  }

  /** 新增（index 为 -1）或替换列表里的一项，然后保存。 */
  async function upsert(col: Collection, item: any, index: number): Promise<SaveResult> {
    const next = editable() as any
    const arr: any[] = next[col] ?? []
    if (index < 0) arr.push(item)
    else arr[index] = item
    next[col] = arr
    return save(next)
  }

  async function removeItem(col: Collection, index: number): Promise<SaveResult> {
    const next = editable() as any
    const arr: any[] = (next[col] ?? []).slice()
    arr.splice(index, 1)
    next[col] = arr
    return save(next)
  }

  /** 切换某个代理或访客的启用开关。 */
  async function toggle(col: 'proxies' | 'visitors', index: number, enabled: boolean): Promise<SaveResult> {
    const next = editable() as any
    next[col][index].enabled = enabled
    return save(next)
  }

  /** 新增项保存后在列表里的下标（用于把字段错误对应到抽屉里的表单）。 */
  function indexForNew(col: Collection): number {
    return ((current.value as any)?.[col] as any[] | null)?.length ?? 0
  }

  return {
    list,
    current,
    currentId,
    loading,
    nodes,
    currentSummary,
    isSubscription,
    readonly,
    updateSubscription,
    loadList,
    select,
    create,
    upload,
    refreshAfterSync,
    duplicate,
    remove,
    save,
    rename,
    upsert,
    removeItem,
    toggle,
    indexForNew,
  }
})
