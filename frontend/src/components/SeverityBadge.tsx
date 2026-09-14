import { sevClass, sevShort } from '../lib/time'

export default function SeverityBadge({ name }: { name: string | undefined }) {
  if (!name) return <span className="text-muted">—</span>
  return (
    <span
      className={`inline-block rounded px-1.5 py-px font-mono text-[11px] uppercase ${sevClass(name)}`}
    >
      {sevShort(name)}
    </span>
  )
}
