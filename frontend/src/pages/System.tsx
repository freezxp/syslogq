import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { fmtNumber } from '../lib/time'

interface ReadyBody {
  status?: string
  checks?: Record<string, string | { status?: string; error?: string }>
}

export default function System() {
  const health = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => api.health(signal),
    refetchInterval: 10_000,
  })
  const ready = useQuery({
    queryKey: ['ready'],
    queryFn: ({ signal }) => api.ready(signal),
    refetchInterval: 10_000,
    retry: false,
  })
  const info = useQuery({
    queryKey: ['sysinfo'],
    queryFn: ({ signal }) => api.systemInfo(signal),
    staleTime: 5 * 60_000,
  })
  const metrics = useQuery({
    queryKey: ['metrics'],
    queryFn: ({ signal }) => api.metrics(signal),
    refetchInterval: 10_000,
  })

  const sums = useMemo(() => parseMetrics(metrics.data ?? ''), [metrics.data])

  const readyChecks = useMemo(() => {
    const r = ready.data as ReadyBody | undefined
    return Object.entries(r?.checks ?? {}).map(([name, v]) => {
      if (typeof v === 'string') return { name, status: v, error: '' }
      return { name, status: v.status ?? 'unknown', error: v.error ?? '' }
    })
  }, [ready.data])

  return (
    <div className="h-full overflow-auto p-4">
      <h1 className="mb-4 text-lg font-semibold">System</h1>

      <div className="mb-4 grid grid-cols-3 gap-3">
        <div className="rounded-lg border border-line bg-panel px-4 py-3">
          <div className="text-xs text-muted">Version</div>
          <div className="mt-1 font-mono text-xl">{info.data?.version ?? '…'}</div>
        </div>
        <div className="rounded-lg border border-line bg-panel px-4 py-3">
          <div className="text-xs text-muted">Liveness (/health)</div>
          <div className={`mt-1 text-xl font-semibold ${health.data ? 'text-emerald-400' : 'text-red-400'}`}>
            {health.isFetching && !health.data ? '…' : health.data ? 'ok' : 'down'}
          </div>
        </div>
        <div className="rounded-lg border border-line bg-panel px-4 py-3">
          <div className="text-xs text-muted">Readiness (/ready)</div>
          <div
            className={`mt-1 text-xl font-semibold ${
              ready.data && !ready.isError ? 'text-emerald-400' : 'text-amber-400'
            }`}
          >
            {ready.isFetching && !ready.data ? '…' : ready.isError ? 'degraded' : 'ready'}
          </div>
        </div>
      </div>

      <div className="mb-4 rounded-lg border border-line bg-panel">
        <div className="border-b border-line px-4 py-2 text-sm font-medium">Components</div>
        <table className="w-full text-xs">
          <tbody>
            {readyChecks.length === 0 && (
              <tr>
                <td className="px-4 py-2 text-muted">loading…</td>
              </tr>
            )}
            {readyChecks.map((check) => (
              <tr key={check.name} className="border-b border-line/40 last:border-0">
                <td className="px-4 py-2 font-mono">{check.name}</td>
                <td
                  className={`px-4 py-2 ${
                    check.status === 'ok' ? 'text-emerald-400' : 'text-red-400'
                  }`}
                >
                  {check.status}
                </td>
                <td className="px-4 py-2 text-muted">{check.error}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="rounded-lg border border-line bg-panel">
        <div className="border-b border-line px-4 py-2 text-sm font-medium">
          Ingestion metrics
          <span className="ml-2 text-xs font-normal text-muted">
            (live from /metrics · persisted counters reset on restart)
          </span>
        </div>
        <table className="w-full text-xs">
          <tbody>
            {sums.map((m) => (
              <tr key={m.name} className="border-b border-line/40 last:border-0">
                <td className="w-72 px-4 py-2 font-mono text-muted">{m.name}</td>
                <td className="px-4 py-2">
                  {m.byLabel ? (
                    <div className="flex flex-wrap gap-x-4 gap-y-1">
                      {m.byLabel.map((l) => (
                        <span key={l.label}>
                          <span className="text-muted">{l.label}</span>{' '}
                          <span className="font-mono">{fmtNumber(l.value)}</span>
                        </span>
                      ))}
                    </div>
                  ) : (
                    <span className="font-mono">{fmtNumber(m.value)}</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

interface MetricSum {
  name: string
  value: number
  byLabel?: { label: string; value: number }[]
}

/** Parse the few syslogq_ series we surface; everything else is ignored. */
function parseMetrics(text: string): MetricSum[] {
  const wanted = new Set([
    'syslogq_messages_received_total',
    'syslogq_messages_stored_total',
    'syslogq_messages_dropped_total',
    'syslogq_messages_parse_errors_total',
    'syslogq_queue_depth',
  ])
  const byName = new Map<string, MetricSum>()
  for (const line of text.split('\n')) {
    if (!line || line.startsWith('#')) continue
    const m = line.match(/^(\S+)\{([^}]*)\}\s+([0-9.e+-]+)$/) ?? line.match(/^(\S+)\s+([0-9.e+-]+)$/)
    if (!m) continue
    const [, name] = m
    if (!wanted.has(name)) continue
    let labels = ''
    let value = 0
    if (m.length === 4) {
      labels = m[2]
      value = Number(m[3])
    } else {
      value = Number(m[2])
    }
    let entry = byName.get(name)
    if (!entry) {
      entry = { name, value: 0 }
      byName.set(name, entry)
    }
    entry.value += value
    if (labels) {
      entry.byLabel ??= []
      entry.byLabel.push({ label: labels, value })
    }
  }
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name))
}
