import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { LogRow } from '../lib/api'
import { useAuth } from '../lib/auth'
import { fmtNumber, resolveRange } from '../lib/time'
import type { RangeKey, TimeRange } from '../lib/time'
import TimeRangePicker, { rangeParams } from '../components/TimeRangePicker'
import FacetSidebar from '../components/FacetSidebar'
import LogTable, { DetailDrawer } from '../components/LogTable'

const PAGE_SIZE = 100
const SAVED_KEY = 'syslogq_saved_searches'

interface SavedSearch {
  name: string
  q: string
}

function loadSaved(): SavedSearch[] {
  try {
    return JSON.parse(localStorage.getItem(SAVED_KEY) ?? '[]') as SavedSearch[]
  } catch {
    return []
  }
}

/** Quote a facet value for insertion into the advanced query string. */
function filterTerm(field: string, value: string): string {
  const safe = /^[A-Za-z0-9._@/~+-]+$/.test(value)
  return safe ? `${field}:${value}` : `${field}:"${value.replace(/(["\\])/g, '\\$1')}"`
}

export default function Explorer() {
  const { user } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const q = searchParams.get('q') ?? ''
  const range = useMemo(
    () =>
      resolveRange(
        (searchParams.get('range') as RangeKey | null) ?? '1h',
        searchParams.get('start'),
        searchParams.get('end'),
      ),
    [searchParams],
  )
  const rp = useMemo(() => rangeParams(range), [range])
  const params = useMemo(() => ({ q: q || undefined, ...rp }), [q, rp])

  const [draft, setDraft] = useState(q)
  const [rows, setRows] = useState<LogRow[]>([])
  const [nextOffset, setNextOffset] = useState<number | null>(null)
  const [selected, setSelected] = useState<LogRow | null>(null)
  const [live, setLive] = useState(false)
  const [liveCount, setLiveCount] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState<SavedSearch[]>(loadSaved)
  const searchInput = useRef<HTMLInputElement>(null)

  useEffect(() => setDraft(q), [q])

  // "/" focuses the search box.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === '/' && document.activeElement !== searchInput.current) {
        e.preventDefault()
        searchInput.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  function updateParams(patch: Record<string, string | null>) {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        for (const [k, v] of Object.entries(patch)) {
          if (v === null || v === '') next.delete(k)
          else next.set(k, v)
        }
        return next
      },
      { replace: false },
    )
  }

  function submitQuery(text: string) {
    updateParams({ q: text })
    setSelected(null)
  }

  function addFilter(field: string, value: string) {
    const term = filterTerm(field, value)
    submitQuery(q ? `${q} AND ${term}` : term)
  }

  const search = useQuery({
    queryKey: ['search', q, rp.start, rp.end],
    queryFn: ({ signal }) => api.search({ ...params, limit: PAGE_SIZE }, signal),
    refetchInterval: live ? 3000 : false,
  })

  // First page replaces the table; live mode bumps a counter so new rows
  // are visually countable.
  useEffect(() => {
    if (!search.data) return
    setRows(search.data.logs)
    setNextOffset(search.data.next_offset ?? null)
    if (live) setLiveCount((n) => n + 1)
    setError(null)
  }, [search.data, live])

  useEffect(() => {
    if (search.error) setError(search.error.message)
  }, [search.error])

  async function loadMore() {
    if (nextOffset === null) return
    try {
      const res = await api.search({ ...params, limit: PAGE_SIZE, offset: nextOffset })
      setRows((prev) => [...prev, ...res.logs])
      setNextOffset(res.next_offset ?? null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'load more failed')
    }
  }

  const count = useQuery({
    queryKey: ['count', q, rp.start, rp.end],
    queryFn: ({ signal }) => api.count(params, signal),
    staleTime: 15_000,
  })

  async function exportLogs(format: 'ndjson' | 'csv') {
    try {
      const blob = await api.exportBlob({ ...params, format })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `syslogq-export.${format}`
      a.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'export failed')
    }
  }

  function saveSearch() {
    const name = q.trim()
    if (!name) return
    const next = [{ name, q }, ...saved.filter((s) => s.q !== q)].slice(0, 20)
    setSaved(next)
    localStorage.setItem(SAVED_KEY, JSON.stringify(next))
  }

  const liveFresh = live && liveCount > 0

  return (
    <div className="flex h-full flex-col">
      <header className="flex flex-col gap-2 border-b border-line bg-panel px-4 py-2.5">
        <div className="flex items-center gap-2">
          <form
            className="relative min-w-0 flex-1"
            onSubmit={(e) => {
              e.preventDefault()
              submitQuery(draft)
            }}
          >
            <input
              ref={searchInput}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder='Search logs…  e.g.  severity_name:err AND hostname:fw01   (press "/" to focus)'
              spellCheck={false}
              className="w-full rounded-md border border-line bg-panel-2 py-1.5 pl-3 pr-24 font-mono text-[13px] text-text outline-none focus:border-accent"
            />
            <div className="absolute right-1.5 top-1/2 flex -translate-y-1/2 gap-1">
              <button
                type="button"
                onClick={saveSearch}
                disabled={!draft.trim()}
                title="Save this search"
                className="rounded px-2 py-0.5 text-xs text-muted hover:bg-panel hover:text-text disabled:opacity-40"
              >
                save
              </button>
              <button
                type="submit"
                className="rounded bg-accent/20 px-2.5 py-0.5 text-xs text-accent hover:bg-accent/30"
              >
                Search
              </button>
            </div>
          </form>
          <TimeRangePicker
            range={range}
            onChange={(r: TimeRange) => {
              const patch: Record<string, string> = { range: r.key }
              if (r.key === 'custom') {
                patch.start = r.start.toISOString()
                patch.end = r.end.toISOString()
              } else {
                patch.start = ''
                patch.end = ''
              }
              updateParams(patch)
            }}
          />
          <button
            onClick={() => {
              setLive((v) => !v)
              setLiveCount(0)
            }}
            className={`flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs ${
              live ? 'bg-emerald-500/15 text-emerald-400' : 'text-muted hover:bg-panel-2 hover:text-text'
            }`}
            title="Poll for new logs every 3s (WebSocket tail lands in Phase 5)"
          >
            <span
              className={`inline-block h-1.5 w-1.5 rounded-full ${
                liveFresh ? 'animate-pulse bg-emerald-400' : live ? 'bg-emerald-400/60' : 'bg-muted'
              }`}
            />
            {live ? 'Live' : 'Live off'}
          </button>
          {user?.role !== 'viewer' && (
            <div className="flex gap-1">
              <button
                onClick={() => void exportLogs('ndjson')}
                className="rounded-md border border-line px-2 py-1 text-xs text-muted hover:text-text"
              >
                NDJSON
              </button>
              <button
                onClick={() => void exportLogs('csv')}
                className="rounded-md border border-line px-2 py-1 text-xs text-muted hover:text-text"
              >
                CSV
              </button>
            </div>
          )}
        </div>
        <div className="flex items-center gap-3 text-xs text-muted">
          <span>
            {fmtNumber(rows.length)} loaded
            {count.data && ` of ${fmtNumber(count.data.count)} matched`}
          </span>
          {search.isFetching && <span className="text-accent">…</span>}
          {error && <span className="text-red-400">{error}</span>}
          {nextOffset !== null && (
            <button onClick={() => void loadMore()} className="text-accent hover:underline">
              Load more ↓
            </button>
          )}
          {saved.length > 0 && (
            <span className="flex items-center gap-1.5">
              ·
              {saved.slice(0, 6).map((s) => (
                <span key={s.q} className="group flex items-center gap-0.5">
                  <button
                    onClick={() => submitQuery(s.q)}
                    className="max-w-40 truncate rounded bg-panel-2 px-1.5 py-px font-mono text-[11px] text-muted hover:text-accent"
                    title={s.q}
                  >
                    {s.name.slice(0, 24)}
                  </button>
                  <button
                    onClick={() => {
                      const next = saved.filter((x) => x.q !== s.q)
                      setSaved(next)
                      localStorage.setItem(SAVED_KEY, JSON.stringify(next))
                    }}
                    className="opacity-0 transition-opacity group-hover:opacity-100 hover:text-red-400"
                    title="Remove"
                  >
                    ✕
                  </button>
                </span>
              ))}
            </span>
          )}
        </div>
      </header>
      <div className="flex min-h-0 flex-1">
        <FacetSidebar params={params} onFilter={addFilter} />
        <LogTable rows={rows} selected={selected} onSelect={setSelected} />
        <DetailDrawer
          row={selected}
          onClose={() => setSelected(null)}
          onFilter={(f, v) => addFilter(f, v)}
        />
      </div>
    </div>
  )
}
