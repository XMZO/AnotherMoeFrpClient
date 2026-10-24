// 全局反馈：短消息（toast）、带操作的通知（notice）、确认框（dialog）。
// 状态放在模块里，由 UiHost.vue 统一渲染；任何组件直接 import 调用，不需要 provider。
import { reactive } from 'vue'
import type { IconName } from './icons'

// ---------- 短消息 ----------
export type ToastKind = 'success' | 'info' | 'warning' | 'error'
export interface Toast {
  id: number
  kind: ToastKind
  text: string
}
export interface ToastOptions {
  /** 显示多久（毫秒），0 表示不自动消失。默认 3 秒。 */
  duration?: number
}
export interface Handle {
  destroy(): void
}

export const toasts = reactive<Toast[]>([])
let seq = 0
const MAX_TOASTS = 3

function removeToast(id: number) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}

function pushToast(kind: ToastKind, text: string, opts?: ToastOptions): Handle {
  const id = ++seq
  toasts.push({ id, kind, text })
  if (toasts.length > MAX_TOASTS) toasts.splice(0, toasts.length - MAX_TOASTS)
  const duration = opts?.duration ?? 3000
  if (duration > 0) setTimeout(() => removeToast(id), duration)
  return { destroy: () => removeToast(id) }
}

export const message = {
  success: (text: string, opts?: ToastOptions) => pushToast('success', text, opts),
  info: (text: string, opts?: ToastOptions) => pushToast('info', text, opts),
  warning: (text: string, opts?: ToastOptions) => pushToast('warning', text, opts),
  error: (text: string, opts?: ToastOptions) => pushToast('error', text, opts),
}
export type MessageApi = typeof message

// ---------- 通知（右上角，带标题和操作按钮） ----------
export interface NoticeAction {
  label: string
  primary?: boolean
  testid?: string
  onClick: () => void
}
export interface NoticeOptions {
  type?: 'info' | 'warning' | 'success' | 'error'
  title: string
  content?: string
  /** 0 或不填表示不自动消失。 */
  duration?: number
  actions?: NoticeAction[]
  /** 点右上角的关闭按钮时调用。 */
  onClose?: () => void
}
export interface Notice extends NoticeOptions {
  id: number
}

export const notices = reactive<Notice[]>([])

function removeNotice(id: number) {
  const i = notices.findIndex((n) => n.id === id)
  if (i >= 0) notices.splice(i, 1)
}

export function notify(opts: NoticeOptions): Handle {
  const id = ++seq
  notices.push({ ...opts, id })
  if (opts.duration && opts.duration > 0) setTimeout(() => removeNotice(id), opts.duration)
  return { destroy: () => removeNotice(id) }
}

export function closeNotice(id: number) {
  const n = notices.find((x) => x.id === id)
  removeNotice(id)
  n?.onClose?.()
}

// ---------- 确认框 ----------
export type DialogTone = 'warning' | 'error' | 'success' | 'info'
export interface DialogOptions {
  title: string
  content: string
  positiveText?: string
  negativeText?: string
  /** 返回 false 时不关闭；返回 Promise 时按钮显示加载中，结束后关闭。 */
  onPositiveClick?: () => unknown
  onNegativeClick?: () => void
  /** 确定按钮用红色（删除、撤销之类）。 */
  danger?: boolean
  icon?: IconName
}
export interface DialogItem extends DialogOptions {
  id: number
  tone: DialogTone
  loading: boolean
}

export const dialogs = reactive<DialogItem[]>([])

function openDialog(tone: DialogTone, opts: DialogOptions): Handle {
  const id = ++seq
  dialogs.push({ ...opts, id, tone, loading: false })
  return { destroy: () => closeDialog(id) }
}

export function closeDialog(id: number) {
  const i = dialogs.findIndex((d) => d.id === id)
  if (i >= 0) dialogs.splice(i, 1)
}

export async function confirmDialog(id: number) {
  const d = dialogs.find((x) => x.id === id)
  if (!d || d.loading) return
  let res: unknown
  try {
    const r = d.onPositiveClick?.()
    if (r instanceof Promise) {
      d.loading = true
      res = await r
    } else res = r
  } finally {
    d.loading = false
  }
  if (res !== false) closeDialog(id)
}

export function cancelDialog(id: number) {
  const d = dialogs.find((x) => x.id === id)
  closeDialog(id)
  d?.onNegativeClick?.()
}

export const dialog = {
  warning: (o: DialogOptions) => openDialog('warning', o),
  error: (o: DialogOptions) => openDialog('error', o),
  success: (o: DialogOptions) => openDialog('success', o),
  info: (o: DialogOptions) => openDialog('info', o),
}
