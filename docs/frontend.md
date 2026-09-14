# Frontend Architecture

Date: 2026-09-14 · Status: Phase 0 baseline
Goal: an explorer that feels like a modern observability product, not a CRUD
app. Dark-first, dense, fast, keyboard-driven.

## 1. Stack

- **React 19 + TypeScript + Vite** (build), **react-router** (routing).
- **Tailwind CSS** + design tokens; headless primitives via **Radix UI**.
- **TanStack Query** (server state: caching, invalidation, prefetch),
  **TanStack Table** (log grid: sorting/filter wiring, headless = full control
  of virtualization), **TanStack Virtual** (windowed rows, ~15k rows smooth).
- **Recharts** for charts (area/bar/donut) — sufficient at our data rates;
  charts consume pre-aggregated API series, not raw rows.
- `openapi-typescript` to generate the API client types from
  `docs/openapi.yaml` (checked into repo, CI-verified drift).

## 2. App Structure

```text
src/
├── app/            # router, providers (QueryClient, auth, theme), AppShell, sidebar
├── api/            # generated types + typed fetch client (auth, errors, cursors)
├── features/
│   ├── auth/       # login, session store, route guards
│   ├── dashboard/  # cards, volume chart, severity donut, top-N lists
│   ├── logs/       # explorer, query builder (visual+advanced), table, detail drawer,
│   │               # field sidebar w/ facets, time picker, live tail, export
│   ├── sources/    # list, editor, status badges, test
│   ├── searches/   # saved search list/detail
│   └── settings/   # storage, retention, users, system
├── components/     # Button, Modal, Menu, Tabs, Toast, DateTimeRangePicker…
└── query/          # AST types, URL codec, visual↔advanced conversion
```

## 3. URL State (shareable searches)

`/logs?from&to&query&fields&sort&live` — single source of truth via a
`useSearchState` hook that bi-directionally syncs a typed state object with the
URL (replace-history for keystrokes, push for explicit Run). Any explorer view
is copy-paste shareable (requirement §44). Time picker writes `from/to`
(ISO8601; presets resolve to concrete instants on selection).

## 4. Query Builder (the signature feature)

Two modes, one shared AST (`query/`):
- **Visual**: rows of `[Field ▼][Operator ▼][Value][±]`, groups joined by
  AND/OR; chips for NOT. Compiles AST → LogsQL previewed live.
  Field dropdown sourced from `/api/v1/fields`; value dropdown offers
  `/fields/{f}/values` completions (debounced prefix search).
- **Advanced**: raw LogsQL textarea with syntax highlighting + a validation
  call (debounced) that surfaces server errors inline.
Toggle preserves semantics: parse LogsQL → AST where possible; irreversible
constructions stay in Advanced with a notice. **The UI never hand-rolls LogsQL
outside the compiler module** — same rule as the backend.

## 5. Log Explorer Layout

```text
┌────────────────────────────────────────────────────────────┐
│ time picker │ search bar (builder chips or raw) │ Run  ⚙  ▼ │
├───────────┬──────────────────────────────────────────────────┤
│ Fields    │  volume mini-chart (range-aware, clickable)      │
│ (facets)  ├──────────────────────────────────────────────────┤
│ severity  │  ▼ table: timestamp host sev app message        │
│ host      │    row → expandable inline / drawer on click     │
│ facility  │  footer: cursor pager [Newer] [Older] + counts   │
└───────────┴──────────────────────────────────────────────────┘
```
- Facet value click → add filter; alt-click → exclude; search box per facet.
- Selected extra fields become table columns (persisted in local prefs).
- Table: virtualized rows, monospace message column, severity color chips,
  keyboard nav (j/k move, Enter opens detail), live "N new logs" pill that
  prepends without stealing scroll.
- Detail drawer: all standard fields, dynamic fields grid, per-value actions
  (filter/exclude/copy), copy JSON / copy raw, received-vs-event time delta.

## 6. Live Tail (`/logs/live`)

Full-width stream view: pause/resume, clear, auto-scroll toggle, highlight
rules (severity-based defaults), max-buffered indicator, filter bar (same
builder, narrowed scope), rate readout from WS `stats` frames. Disconnects
show reconnecting state w/ backoff; reconnect resumes with last-seen cursor.

## 7. Dashboard

Cards row (totals + logs/sec + errors + sources + storage) fed by
`/dashboard/overview` (poll 15s, pause when tab hidden). Volume area chart
(step-aware buckets from `/logs/stats`), severity donut, top hosts/apps/source
IPs/facilities/formats lists — all re-query when the global time range changes.
Charts are click-through: segment click → `/logs` with that filter applied.

## 8. Cross-cutting UX

- **Command bar** (⌘K): go to page, run saved search, set time range.
- Keyboard shortcuts: `r` run, `t` tail, `e` export, `?` shortcut help.
- Toasts for actions; confirm dialogs for destructive ops.
- Empty/zero states with next-action hints; skeletons for all async views.
- Responsive down to 1280px (NOC wall monitors); no mobile-specific claims.
- Reduced-motion respected; charts render ≤ 60 points by downsampling server-side.

## 9. Quality Bars

TypeScript strict; ESLint + Prettier; route-level code splitting; bundle
budget 350 KB gzip initial; e2e smoke (Playwright) for login → ingest-visible
→ search → tail; a11y basics (focus rings, aria labels on controls).
