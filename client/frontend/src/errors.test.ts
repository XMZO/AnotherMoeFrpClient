import { describe, expect, it } from 'vitest'
import { codeMessages, codeText, describeError, fieldErrorsFor, toAppError } from './errors'
import { mergeLogs } from './logmerge'

// Go 端 internal/apperr 里定义的所有 code，必须都有中文提示（新增 code 时这里会提醒）。
const goClientCodes = [
  'profile_not_found', 'profile_in_use', 'node_not_found', 'node_required', 'frp_config_invalid',
  'tunnel_busy', 'tunnel_not_running', 'tunnel_start_failed',
  'node_token_invalid', 'node_refused', 'node_timeout', 'node_dns', 'node_unreachable', 'node_handshake_failed', 'node_udp_unprobed',
  'proxy_port_in_use', 'proxy_port_not_allowed', 'proxy_port_unavailable', 'proxy_name_taken', 'proxy_domain_conflict',
  'proxy_type_unsupported', 'proxy_start_error',
  'server_unreachable', 'bad_response', 'not_logged_in', 'conflict_gone',
  'subscription_read_only', 'share_needs_cloud', 'share_not_synced', 'subscription_inactive',
  'image_disabled', 'image_no_source', 'image_busy', 'image_fetch_failed', 'image_not_found', 'image_decode_failed',
  'clipboard_failed', 'autostart_failed', 'proxy_url_invalid',
  'storage_failed', 'export_failed', 'invalid_argument',
]
// shared/apierr 的全部 code
const apiCodes = [
  'unauthorized', 'session_expired', 'session_replaced', 'forbidden', 'not_found', 'validation_failed',
  'invalid_credentials', 'account_locked', 'account_disabled', 'invite_invalid', 'invite_used', 'nickname_taken',
  'reset_code_invalid', 'revision_conflict', 'share_revoked', 'share_expired', 'share_full', 'already_subscribed',
  'limit_exceeded', 'rate_limited', 'client_outdated', 'internal', 'method_not_allowed', 'payload_too_large',
]

describe('error code mapping', () => {
  it('covers every code', () => {
    for (const c of [...goClientCodes, ...apiCodes]) expect(codeMessages[c], c).toBeTruthy()
  })

  it('reads the cause of a Wails RuntimeError', () => {
    const err = Object.assign(new Error('node_refused: session shutdown'), {
      cause: { code: 'node_refused', message: 'session shutdown', details: { target: '127.0.0.1:7000' } },
    })
    const ae = toAppError(err)
    expect(ae.code).toBe('node_refused')
    expect(describeError(err)).toContain('连接被拒绝（127.0.0.1:7000）')
  })

  it('falls back to the raw message for unknown codes', () => {
    expect(codeText('something_new', null, 'raw text')).toBe('raw text')
    expect(describeError(new Error('boom'))).toBe('程序内部错误')
  })

  it('maps duplicate-name field errors to the drawer field', () => {
    const err = {
      code: 'validation_failed',
      message: '',
      details: {
        fields: [
          { field: 'proxies[2].name', reason: 'duplicate', message: '名称 "web" 与 proxies[0].name 重复' },
          { field: 'proxies[2].customDomains[1]', reason: 'invalid', message: 'x' },
          { field: 'proxies[0].localPort', reason: 'range', message: 'x' },
        ],
      },
    }
    expect(fieldErrorsFor(err, 'proxies', 2)).toEqual({
      name: '与已有的名称重复（代理和访客的名称不能相同）',
      customDomains: '格式不正确',
    })
    expect(describeError(err)).toContain('代理 #3 · 名称：与已有的名称重复')
    expect(describeError(err)).toContain('代理 #1 · 本地端口：超出范围')
  })
})

describe('mergeLogs', () => {
  const l = (seq: number) => ({ seq, level: 'info', text: `line ${seq}` })

  it('appends, dedupes overlapping batches and keeps order', () => {
    let lines = mergeLogs([], [l(1), l(2), l(3)])
    lines = mergeLogs(lines, [l(2), l(3), l(4)]) // Logs() 和推送重叠
    lines = mergeLogs(lines, [l(5)])
    expect(lines.map((x) => x.seq)).toEqual([1, 2, 3, 4, 5])
    expect(mergeLogs(lines, [l(3)])).toBe(lines) // 全部重复时不产生新数组
  })

  it('keeps only the last max lines', () => {
    const lines = mergeLogs([], Array.from({ length: 10 }, (_, i) => l(i + 1)), 4)
    expect(lines.map((x) => x.seq)).toEqual([7, 8, 9, 10])
  })
})
