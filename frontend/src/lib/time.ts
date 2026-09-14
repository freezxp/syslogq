import { format, formatDistanceToNowStrict, parseISO } from 'date-fns'

export type RangeKey = '15m' | '1h' | '6h' | '24h' | '7d' | '30d' | 'custom'

export const RANGE_PRESETS: { key: RangeKey; label: string; minutes: number }[] = [
  { key: '15m', label: 'Last 15m', minutes: 15 },
  { key: '1h', label: 'Last 1h', minutes: 60 },
  { key: '6h', label: 'Last 6h', minutes: 360 },
  { key: '24h', label: 'Last 24h', minutes: 1440 },
  { key: '7d', label: 'Last 7d', minutes: 10080 },
  { key: '30d', label: 'Last 30d', minutes: 43200 },
]

export interface TimeRange {
  key: RangeKey
  start: Date
  end: Date
}

export function resolveRange(
  key: RangeKey,
  startISO: string | null,
  endISO: string | null,
): TimeRange {
  if (key === 'custom' && startISO && endISO) {
    const start = parseISO(startISO)
    const end = parseISO(endISO)
    if (!Number.isNaN(start.getTime()) && !Number.isNaN(end.getTime()) && start < end) {
      return { key, start, end }
    }
  }
  const preset = RANGE_PRESETS.find((p) => p.key === key) ?? RANGE_PRESETS[1]
  const end = new Date()
  return { key: preset.key, start: new Date(end.getTime() - preset.minutes * 60_000), end }
}

/** Auto bucket: aim for ~120 buckets over the range. */
export function autoBucket(range: TimeRange): string {
  const ms = range.end.getTime() - range.start.getTime()
  const target = ms / 120
  if (target <= 30_000) return '30s'
  if (target <= 60_000) return '1m'
  if (target <= 5 * 60_000) return '5m'
  if (target <= 30 * 60_000) return '15m'
  if (target <= 2 * 60 * 60_000) return '30m'
  if (target <= 12 * 60 * 60_000) return '1h'
  if (target <= 3 * 24 * 60 * 60_000) return '6h'
  return '1d'
}

export function iso(d: Date): string {
  return d.toISOString()
}

export function fmtTime(d: Date): string {
  return format(d, 'HH:mm:ss')
}

export function fmtDateTime(d: Date): string {
  return format(d, 'yyyy-MM-dd HH:mm:ss')
}

export function fmtRelative(d: Date): string {
  return formatDistanceToNowStrict(d, { addSuffix: true })
}

export function fmtNumber(n: number): string {
  return n.toLocaleString()
}

/** Column timestamp from a log row's _time (ISO string). */
export function rowTime(row: Record<string, string>): Date {
  const t = row['_time']
  if (!t) return new Date(0)
  const d = parseISO(t)
  return Number.isNaN(d.getTime()) ? new Date(0) : d
}

const SEVERITIES: Record<string, string> = {
  emergency: 'emerg',
  alert: 'alert',
  critical: 'crit',
  error: 'err',
  warning: 'warning',
  notice: 'notice',
  informational: 'info',
  debug: 'debug',
}

export function sevShort(name: string | undefined): string {
  if (!name) return '—'
  return SEVERITIES[name.toLowerCase()] ?? name
}

export function sevClass(name: string | undefined): string {
  if (!name) return ''
  return `sev-${SEVERITIES[name.toLowerCase()] ?? 'info'}`
}
