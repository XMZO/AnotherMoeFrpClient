import type { LogLine } from '../bindings/moefrp/client/internal/engine/models'

/** 前端最多保留的行数，与 Go 端环形缓冲一致（PLAN 6.3.6）。 */
export const MAX_LOG_LINES = 5000

/**
 * 把新的一批日志并入已有列表：按 seq 去重、保持顺序、只留最后 max 行。
 * 首次打开时 Logs() 返回的缓冲和 tunnel:logs 推送的批次可能重叠，所以必须按 seq 去重。
 */
export function mergeLogs(existing: LogLine[], batch: LogLine[] | null | undefined, max = MAX_LOG_LINES): LogLine[] {
  if (!batch || batch.length === 0) return existing
  const last = existing.length ? existing[existing.length - 1].seq : 0
  let all: LogLine[]
  if (batch.every((l) => l.seq > last)) {
    all = existing.concat(batch) // 常见情况：新的一批都在后面
  } else {
    const seen = new Set(existing.map((l) => l.seq))
    const fresh = batch.filter((l) => !seen.has(l.seq))
    if (fresh.length === 0) return existing
    all = existing.concat(fresh).sort((a, b) => a.seq - b.seq)
  }
  return all.length > max ? all.slice(all.length - max) : all
}
