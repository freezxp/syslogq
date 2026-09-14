import { useState } from 'react'
import { RANGE_PRESETS, iso } from '../lib/time'
import type { RangeKey, TimeRange } from '../lib/time'

export default function TimeRangePicker({
  range,
  onChange,
}: {
  range: TimeRange
  onChange: (r: TimeRange) => void
}) {
  const [customOpen, setCustomOpen] = useState(false)

  function setPreset(key: RangeKey) {
    if (key === 'custom') {
      setCustomOpen(true)
      return
    }
    const preset = RANGE_PRESETS.find((p) => p.key === key)
    if (!preset) return
    const end = new Date()
    onChange({ key, start: new Date(end.getTime() - preset.minutes * 60_000), end })
    setCustomOpen(false)
  }

  function setCustom(start: string, end: string) {
    const s = new Date(start)
    const e = new Date(end)
    if (Number.isNaN(s.getTime()) || Number.isNaN(e.getTime()) || s >= e) return
    onChange({ key: 'custom', start: s, end: e })
    setCustomOpen(false)
  }

  return (
    <div className="flex items-center gap-1 text-xs">
      {RANGE_PRESETS.map((p) => (
        <button
          key={p.key}
          onClick={() => setPreset(p.key)}
          className={`rounded-md px-2 py-1 ${
            range.key === p.key
              ? 'bg-accent/15 text-accent'
              : 'text-muted hover:bg-panel-2 hover:text-text'
          }`}
          title={p.label}
        >
          {p.key}
        </button>
      ))}
      <button
        onClick={() => setCustomOpen((v) => !v)}
        className={`rounded-md px-2 py-1 ${
          range.key === 'custom'
            ? 'bg-accent/15 text-accent'
            : 'text-muted hover:bg-panel-2 hover:text-text'
        }`}
        title="Custom range"
      >
        custom
      </button>
      {customOpen && (
        <form
          className="flex items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault()
            const fd = new FormData(e.currentTarget)
            setCustom(String(fd.get('start')), String(fd.get('end')))
          }}
        >
          <input
            type="datetime-local"
            name="start"
            required
            defaultValue={toLocalInput(range.start)}
            className="rounded-md border border-line bg-panel-2 px-1.5 py-1 text-xs"
          />
          <input
            type="datetime-local"
            name="end"
            required
            defaultValue={toLocalInput(range.end)}
            className="rounded-md border border-line bg-panel-2 px-1.5 py-1 text-xs"
          />
          <button
            type="submit"
            className="rounded-md bg-accent/20 px-2 py-1 text-accent hover:bg-accent/30"
          >
            Apply
          </button>
        </form>
      )}
      {range.key === 'custom' && !customOpen && (
        <span className="ml-1 font-mono text-[11px] text-muted">
          {range.start.toISOString().slice(0, 16)} → {range.end.toISOString().slice(0, 16)}
        </span>
      )}
    </div>
  )
}

function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function rangeParams(range: TimeRange): { start: string; end: string } {
  return { start: iso(range.start), end: iso(range.end) }
}
