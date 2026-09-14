# Frontend Architecture

Date: 2026-09-14 · Status: Phase 4 as-built (design baseline below)
Goal: an explorer that feels like a modern observability product, not a CRUD
app. Dark-first, dense, fast, keyboard-driven.

## 1. Stack — as built

- **React 19 + TypeScript + Vite** (build), **react-router 7** (routing).
- **Tailwind CSS v4** with `@theme` design tokens (dark-first, no toggle yet).
- **TanStack Query** (server state) + **TanStack Virtual** (windowed rows —
  first paint p95 440 ms on a 100K-row explorer view, well under the 2 s
  exit criterion).
- **Recharts** for the dashboard volume area chart; bars/donuts are plain
  DOM (cheaper than chart libs at facet sizes).
- No generated client: `src/lib/api.ts` is a small typed fetch wrapper
  (openapi-typescript drift generation is a future hardening step).

## 2. App Structure — as built

```text
src/
├── lib/            # api.ts (typed fetch + token store), auth.tsx (context),
│                   # time.ts (ranges, buckets, formatting)
├── components/     # Layout (sidebar shell), TimeRangePicker, FacetSidebar,
│                   # LogTable (virtualized) + DetailDrawer, SeverityBadge
├── pages/          # Login, Explorer, Dashboard, System
├── App.tsx         # routes + auth guard
└── main.tsx        # providers (QueryClient, Auth, BrowserRouter)
```

## 3. URL State — as built

`/explorer?q&range&start&end` via `useSearchParams`: `q` is the Advanced-mode
query (the only query mode shipped; the visual builder is a Phase 5 polish),
`range` is a preset key, `start`/`end` carry concrete instants for custom
ranges. Searches are shareable by URL.

## 4. Explorer

- Query box (Advanced subset = the API's `q` grammar) with `/` shortcut.
- Facet sidebar: hostname, severity_name, facility_name, app_name,
  source_type, protocol — click adds `field:value` to the query.
- Virtualized table (time/severity/host/app/message) with detail drawer;
  drawer fields are click-to-filter.
- Live toggle: 3 s polling refetch (WebSocket tail lands in Phase 5 with the
  `/api/v1/logs/tail` design in `api.md` §8).
- Saved searches (localStorage, per-browser) + NDJSON/CSV export
  (operator+ only, hidden for viewers).
- Load-more pagination via `next_offset`.

## 5. Dashboard

Cards (logs in range, avg rate, errors & worse, bucket), volume area chart
(auto-bucketed to ~120 points), severity/hosts/apps facet bars — all fed by
the Phase 3 endpoints (`/logs/count`, `/logs/volume`, `/fields/{f}/values`).

## 6. System

Version, liveness/readiness cards, component table (`/ready` checks), live
ingestion metrics parsed from `/metrics` (received/stored/dropped/parse
errors/queue depth).

## 7. Serving

`npm run build` → `backend/internal/web/dist` → `go:embed` (ADR-0006) →
served at `/` by the Go binary with SPA fallback and immutable caching for
hashed `/assets/*`. Dev: `npm run dev` proxies `/api` to `127.0.0.1:8080`.

---

## Design baseline (future phases, kept for planning)

### Stack notes not yet built
Radix headless primitives, TanStack Table, openapi-typescript generation,
theme toggle, saved searches server-side (`/api/v1/saved-searches`), sources
CRUD UI, settings UI, keyboard shortcut palette.

### App Structure (baseline)

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

### URL State (shareable searches) — baseline

`/logs?from&to&query&fields&sort&live` — single source of truth via a
`useSearchState` hook that bi-directionally syncs a typed state object with the
URL (replace-history for keystrokes, push for explicit Run). Any explorer view
is copy-paste shareable (requirement §44). Time picker writes `from/to`
(ISO8601; presets resolve to concrete instants on selection).

### Query Builder (baseline design)

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

### Log Explorer Layout (baseline)

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

### Live Tail (`/logs/live`) — baseline

Full-width stream view: pause/resume, clear, auto-scroll toggle, highlight
rules (severity-based defaults), max-buffered indicator, filter bar (same
builder, narrowed scope), rate readout from WS `stats` frames. Disconnects
show reconnecting state w/ backoff; reconnect resumes with last-seen cursor.

### Dashboard — baseline

Cards row (totals + logs/sec + errors + sources + storage) fed by
`/dashboard/overview` (poll 15s, pause when tab hidden). Volume area chart
(step-aware buckets from `/logs/stats`), severity donut, top hosts/apps/source
IPs/facilities/formats lists — all re-query when the global time range changes.
Charts are click-through: segment click → `/logs` with that filter applied.

### Cross-cutting UX (baseline)

- **Command bar** (⌘K): go to page, run saved search, set time range.
- Keyboard shortcuts: `r` run, `t` tail, `e` export, `?` shortcut help.
- Toasts for actions; confirm dialogs for destructive ops.
- Empty/zero states with next-action hints; skeletons for all async views.
- Responsive down to 1280px (NOC wall monitors); no mobile-specific claims.
- Reduced-motion respected; charts render ≤ 60 points by downsampling server-side.

### Quality Bars (baseline)

TypeScript strict; ESLint + Prettier; route-level code splitting; bundle
budget 350 KB gzip initial; e2e smoke (Playwright) for login → ingest-visible
→ search → tail; a11y basics (focus rings, aria labels on controls).
