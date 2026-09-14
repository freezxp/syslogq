import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { api } from '../lib/api'
import { autoBucket, fmtNumber, fmtTime, resolveRange } from '../lib/time'
import type { RangeKey, TimeRange } from '../lib/time'
import TimeRangePicker, { rangeParams } from '../components/TimeRangePicker'

const ERRORS_Q =
  'severity_name:error OR severity_name:critical OR severity_name:alert OR severity_name:emergency'

export default function Dashboard() {
  const [rangeKey, setRangeKey] = useState<RangeKey>('24h')
  const range = useMemo(() => resolveRange(rangeKey, null, null), [rangeKey])
  const rp = useMemo(() => rangeParams(range), [range])
  const bucket = autoBucket(range)

  const count = useQuery({
    queryKey: ['dash-count', rp.start, rp.end],
    queryFn: ({ signal }) => api.count(rp, signal),
  })
  const errors = useQuery({
    queryKey: ['dash-errors', rp.start, rp.end],
    queryFn: ({ signal }) => api.count({ q: ERRORS_Q, ...rp }, signal),
  })
  const volume = useQuery({
    queryKey: ['dash-volume', rp.start, rp.end, bucket],
    queryFn: ({ signal }) => api.volume({ ...rp, bucket }, signal),
  })
  const severity = useQuery({
    queryKey: ['dash-severity', rp.start, rp.end],
    queryFn: ({ signal }) => api.fieldValues('severity_name', { ...rp, limit: 8 }, signal),
  })
  const hosts = useQuery({
    queryKey: ['dash-hosts', rp.start, rp.end],
    queryFn: ({ signal }) => api.fieldValues('hostname', { ...rp, limit: 8 }, signal),
  })
  const apps = useQuery({
    queryKey: ['dash-apps', rp.start, rp.end],
    queryFn: ({ signal }) => api.fieldValues('app_name', { ...rp, limit: 8 }, signal),
  })

  const rangeSeconds = (range.end.getTime() - range.start.getTime()) / 1000
  const rate = count.data ? count.data.count / rangeSeconds : 0
  const errorPct =
    count.data && count.data.count > 0 && errors.data ? (errors.data.count / count.data.count) * 100 : 0

  const chartData = (volume.data?.buckets ?? []).map((b) => ({
    t: fmtTime(new Date(b.time)),
    n: b.count,
  }))

  const sevTotal = (severity.data?.values ?? []).reduce((s, v) => s + v.count, 0)

  return (
    <div className="h-full overflow-auto p-4">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold">Dashboard</h1>
        <TimeRangePicker
          range={range}
          onChange={(r: TimeRange) => setRangeKey(r.key === 'custom' ? '24h' : r.key)}
        />
      </div>

      <div className="mb-4 grid grid-cols-4 gap-3">
        <Card label="Logs in range" value={count.data ? fmtNumber(count.data.count) : '…'} />
        <Card label="Avg logs/sec" value={rate.toFixed(1)} />
        <Card
          label="Errors & worse"
          value={errors.data ? fmtNumber(errors.data.count) : '…'}
          sub={errors.data ? `${errorPct.toFixed(1)}% of volume` : undefined}
          tone={errorPct > 5 ? 'bad' : 'neutral'}
        />
        <Card label="Bucket" value={bucket} sub={`${chartData.length} points`} />
      </div>

      <Panel title="Log volume" className="mb-4 h-64">
        {chartData.length === 0 ? (
          <Empty />
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={chartData} margin={{ top: 10, right: 12, bottom: 0, left: 0 }}>
              <defs>
                <linearGradient id="vol" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#35b8f0" stopOpacity={0.35} />
                  <stop offset="100%" stopColor="#35b8f0" stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke="#262c35" strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="t" stroke="#8b94a3" fontSize={11} tickLine={false} minTickGap={40} />
              <YAxis stroke="#8b94a3" fontSize={11} tickLine={false} width={40} />
              <Tooltip
                contentStyle={{
                  background: '#13161b',
                  border: '1px solid #262c35',
                  borderRadius: 6,
                  fontSize: 12,
                }}
                labelStyle={{ color: '#8b94a3' }}
              />
              <Area type="monotone" dataKey="n" name="logs" stroke="#35b8f0" fill="url(#vol)" strokeWidth={1.5} />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </Panel>

      <div className="grid grid-cols-3 gap-3">
        <Panel title="Severity mix">
          {!severity.data?.values.length ? (
            <Empty />
          ) : (
            <Bars
              items={severity.data.values.map((v) => ({
                key: v.value || '(none)',
                count: v.count,
                total: sevTotal,
                sev: v.value,
              }))}
            />
          )}
        </Panel>
        <Panel title="Top hosts">
          {!hosts.data?.values.length ? (
            <Empty />
          ) : (
            <Bars items={hosts.data.values.map((v) => ({ key: v.value || '(none)', count: v.count, total: hosts.data!.values[0].count }))} />
          )}
        </Panel>
        <Panel title="Top apps">
          {!apps.data?.values.length ? (
            <Empty />
          ) : (
            <Bars items={apps.data.values.map((v) => ({ key: v.value || '(none)', count: v.count, total: apps.data!.values[0].count }))} />
          )}
        </Panel>
      </div>
    </div>
  )
}

function Card({
  label,
  value,
  sub,
  tone = 'neutral',
}: {
  label: string
  value: string
  sub?: string
  tone?: 'neutral' | 'bad'
}) {
  return (
    <div className="rounded-lg border border-line bg-panel px-4 py-3">
      <div className="text-xs text-muted">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${tone === 'bad' ? 'text-red-400' : ''}`}>
        {value}
      </div>
      {sub && <div className="mt-0.5 text-xs text-muted">{sub}</div>}
    </div>
  )
}

function Panel({
  title,
  className = '',
  children,
}: {
  title: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={`flex flex-col rounded-lg border border-line bg-panel ${className}`}>
      <div className="border-b border-line px-4 py-2 text-sm font-medium">{title}</div>
      <div className="min-h-0 flex-1 p-3">{children}</div>
    </div>
  )
}

function Empty() {
  return <div className="flex h-full items-center justify-center text-xs text-muted/60">no data in range</div>
}

function Bars({
  items,
}: {
  items: { key: string; count: number; total: number; sev?: string }[]
}) {
  const max = Math.max(1, ...items.map((i) => i.count))
  return (
    <div className="flex flex-col gap-1.5">
      {items.map((i) => (
        <div key={i.key} className="text-xs">
          <div className="mb-0.5 flex justify-between">
            <span className="truncate font-mono" title={i.key}>
              {i.key}
            </span>
            <span className="text-muted">{fmtNumber(i.count)}</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded bg-panel-2">
            <div
              className={`h-full rounded ${i.sev ? 'bg-accent/60' : 'bg-accent'}`}
              style={{ width: `${(i.count / max) * 100}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  )
}
