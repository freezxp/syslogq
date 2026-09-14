import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { fmtNumber } from '../lib/time'

const FACET_FIELDS = [
  'hostname',
  'severity_name',
  'facility_name',
  'app_name',
  'source_type',
  'protocol',
]

export default function FacetSidebar({
  params,
  onFilter,
}: {
  params: { q?: string; start: string; end: string }
  onFilter: (field: string, value: string) => void
}) {
  return (
    <div className="flex w-56 shrink-0 flex-col overflow-y-auto border-r border-line bg-panel">
      {FACET_FIELDS.map((field) => (
        <FacetBlock key={field} field={field} params={params} onFilter={onFilter} />
      ))}
    </div>
  )
}

function FacetBlock({
  field,
  params,
  onFilter,
}: {
  field: string
  params: { q?: string; start: string; end: string }
  onFilter: (field: string, value: string) => void
}) {
  const { data } = useQuery({
    queryKey: ['facet', field, params.q ?? '', params.start, params.end],
    queryFn: ({ signal }) => api.fieldValues(field, { ...params, limit: 8 }, signal),
    staleTime: 30_000,
  })
  const values = data?.values ?? []
  const max = Math.max(1, ...values.map((v) => v.count))
  return (
    <div className="border-b border-line px-3 py-2.5">
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-muted">
        {field}
      </div>
      {!values.length && <div className="text-xs text-muted/60">no data</div>}
      <div className="flex flex-col gap-1">
        {values.map((v) => (
          <button
            key={v.value === '' ? '(none)' : v.value}
            disabled={v.value === ''}
            onClick={() => onFilter(field, v.value)}
            className="group relative overflow-hidden rounded px-1.5 py-0.5 text-left text-xs hover:bg-panel-2 disabled:cursor-default"
            title={v.value === '' ? 'rows missing this field' : `${v.value} (${v.count})`}
          >
            <span
              className="absolute inset-y-0 left-0 bg-accent/10"
              style={{ width: `${(v.count / max) * 100}%` }}
            />
            <span className="relative flex justify-between gap-2">
              <span className="truncate">{v.value === '' ? '(none)' : v.value}</span>
              <span className="shrink-0 text-muted">{fmtNumber(v.count)}</span>
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}
