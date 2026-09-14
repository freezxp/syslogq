import { useRef } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import SeverityBadge from './SeverityBadge'
import { fmtTime, rowTime } from '../lib/time'
import type { LogRow } from '../lib/api'

const HIDDEN_DETAIL = new Set(['_msg', '_time', '_stream', '_stream_id'])

export default function LogTable({
  rows,
  selected,
  onSelect,
}: {
  rows: LogRow[]
  selected: LogRow | null
  onSelect: (row: LogRow | null) => void
}) {
  const parentRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 30,
    overscan: 20,
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="grid grid-cols-[86px_64px_110px_110px_1fr] gap-2 border-b border-line px-3 py-1.5 text-[11px] font-medium uppercase tracking-wide text-muted">
        <span>Time</span>
        <span>Sev</span>
        <span>Host</span>
        <span>App</span>
        <span>Message</span>
      </div>
      <div ref={parentRef} className="min-h-0 flex-1 overflow-auto font-mono text-xs">
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((v) => {
            const row = rows[v.index]
            const isSel = selected === row
            return (
              <div
                key={v.key}
                onClick={() => onSelect(isSel ? null : row)}
                className={`absolute inset-x-0 grid cursor-pointer grid-cols-[86px_64px_110px_110px_1fr] items-center gap-2 border-b border-line/40 px-3 hover:bg-panel-2 ${
                  isSel ? 'bg-accent/10' : ''
                }`}
                style={{ height: v.size, transform: `translateY(${v.start}px)` }}
              >
                <span className="text-muted">{fmtTime(rowTime(row))}</span>
                <SeverityBadge name={row['severity_name']} />
                <span className="truncate" title={row['hostname']}>
                  {row['hostname'] || '—'}
                </span>
                <span className="truncate text-muted" title={row['app_name']}>
                  {row['app_name'] || '—'}
                </span>
                <span className="truncate" title={row['_msg']}>
                  {row['_msg']}
                </span>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

export function DetailDrawer({
  row,
  onClose,
  onFilter,
}: {
  row: LogRow | null
  onClose: () => void
  onFilter: (field: string, value: string) => void
}) {
  if (!row) return null
  const fields = Object.entries(row)
    .filter(([k]) => !HIDDEN_DETAIL.has(k))
    .sort(([a], [b]) => a.localeCompare(b))
  return (
    <div className="flex w-[380px] shrink-0 flex-col overflow-auto border-l border-line bg-panel">
      <div className="flex items-center justify-between border-b border-line px-4 py-2.5">
        <span className="text-sm font-medium">Log detail</span>
        <button onClick={onClose} className="rounded px-1.5 text-muted hover:text-text">
          ✕
        </button>
      </div>
      <div className="border-b border-line px-4 py-3">
        <div className="mb-1 text-[11px] uppercase tracking-wide text-muted">Message</div>
        <div className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">
          {row['_msg']}
        </div>
      </div>
      <div className="flex-1 px-4 py-3">
        <div className="mb-1.5 text-[11px] uppercase tracking-wide text-muted">
          Fields ({fields.length})
        </div>
        <table className="w-full text-xs">
          <tbody>
            {fields.map(([k, v]) => (
              <tr key={k} className="border-b border-line/40">
                <td className="w-32 py-1 pr-2 align-top font-mono text-muted">{k}</td>
                <td className="py-1 align-top">
                  <button
                    className="break-all text-left font-mono hover:text-accent"
                    title={`Filter ${k}:${v}`}
                    onClick={() => onFilter(k, v)}
                  >
                    {v || '""'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
