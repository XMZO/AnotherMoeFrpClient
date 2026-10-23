import { describe, expect, it } from 'vitest'
import { checkInviteCode, checkNickname, checkPassword, inviteCheckChar } from './invite'

// 向量与 shared/validate/validate_test.go 相同（由旧 Python 代码算出）
const valid = ['FRPT-AAAA-AAAA', 'FRPT-2345-678N', 'FRPT-ZZZZ-ZZZ2', 'FRPT-MQEF-RPXN', 'FRPT-K7P2-W9QK', 'FRPT-HJKM-NPQB', 'FRPT-9999-9999', 'FRPT-ABCD-EFGZ']

describe('invite code', () => {
  it('accepts the shared test vectors', () => {
    for (const c of valid) expect(checkInviteCode(c), c).toBe('')
  })
  it('normalizes case and surrounding spaces', () => {
    expect(checkInviteCode('  frpt-2345-678n ')).toBe('')
  })
  it('rejects bad checksums and formats', () => {
    expect(checkInviteCode('FRPT-AAAA-AAAB')).toBe('checksum')
    expect(checkInviteCode('FRPT-AAAA-AAA')).toBe('invalid')
    expect(checkInviteCode('FRPT-AAAAAAAA-')).toBe('invalid')
    expect(checkInviteCode('FRPT-IAAA-AAAA')).toBe('invalid')
    expect(checkInviteCode('FRPT-0AAA-AAAA')).toBe('invalid')
    expect(checkInviteCode('')).toBe('required')
  })
  it('computes check chars', () => {
    expect(inviteCheckChar('2345678')).toBe('N')
    expect(inviteCheckChar('AAAAAAA')).toBe('A')
    expect(inviteCheckChar('AAAAAA')).toBeNull()
    expect(inviteCheckChar('AAAAAAI')).toBeNull()
  })
})

describe('account fields', () => {
  it('checks nickname and password like the server', () => {
    expect(checkNickname('ab')).toBe('invalid')
    expect(checkNickname('alice_01')).toBe('')
    expect(checkNickname('中文名')).toBe('invalid')
    expect(checkPassword('1234567')).toBe('too_short')
    expect(checkPassword('12345678')).toBe('')
    expect(checkPassword('x'.repeat(129))).toBe('too_long')
  })
})
