// 邀请码的格式和校验位检查（PLAN 6.7：注册前在前端先检查），算法与 shared/validate 的 InviteCode 相同：
// FRPT-XXXX-XXXX，字符集是去掉 I、L、O 的大写字母加 2–9，前 7 个字符是正文，最后一个是校验位（旧版的 Luhn mod 31 变体）。

export const INVITE_CHARSET = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789'
const PREFIX = 'FRPT-'

/** 去掉首尾空白并转成大写（与服务端 NormalizeInviteCode 一致）。 */
export function normalizeInviteCode(s: string): string {
  return s.trim().toUpperCase()
}

/** 7 个字符正文的校验位。正文不合法时返回 null。 */
export function inviteCheckChar(body: string): string | null {
  if (body.length !== 7) return null
  const n = INVITE_CHARSET.length
  const parity = body.length % 2
  let sum = 0
  for (let i = 0; i < body.length; i++) {
    let d = INVITE_CHARSET.indexOf(body[i])
    if (d < 0) return null
    if (i % 2 === parity) {
      d *= 2
      if (d >= n) d = (d % n) + Math.floor(d / n)
    }
    sum += d
  }
  return INVITE_CHARSET[(sum * 9) % n]
}

/** 检查邀请码（先 normalize），返回 shared/validate 的 reason：'' 表示通过，否则 required / invalid / checksum。 */
export function checkInviteCode(raw: string): '' | 'required' | 'invalid' | 'checksum' {
  const code = normalizeInviteCode(raw)
  if (!code) return 'required'
  if (code.length !== 14 || !code.startsWith(PREFIX) || code[9] !== '-') return 'invalid'
  const body = code.slice(5, 9) + code.slice(10)
  for (const ch of body) if (!INVITE_CHARSET.includes(ch)) return 'invalid'
  return inviteCheckChar(body.slice(0, 7)) === body[7] ? '' : 'checksum'
}

/** 昵称：3–20 位字母、数字、下划线（与服务端相同，保留词由服务端检查）。 */
export function checkNickname(s: string): '' | 'required' | 'invalid' {
  if (!s) return 'required'
  return /^[a-zA-Z0-9_]{3,20}$/.test(s) ? '' : 'invalid'
}

/** 密码长度 8–128 个字符。 */
export function checkPassword(s: string): '' | 'required' | 'too_short' | 'too_long' {
  const n = [...s].length
  if (n === 0) return 'required'
  if (n < 8) return 'too_short'
  if (n > 128) return 'too_long'
  return ''
}
