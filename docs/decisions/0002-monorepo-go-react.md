# ADR-0002: Monorepo with Go backend and React frontend

Status: Accepted · Date: 2026-09-14 · Supersedes: none

## Context
The product is one deployable unit: a Go API/ingestion service plus a React
SPA. API changes and UI changes frequently land together (query AST, field
discovery, tail frames). Team is small; cross-repo version skew is pure
overhead at this stage.

## Decision
Single repository with `backend/` (Go module) and `frontend/` (pnpm
workspace), per the layout in `architecture.md` §8. One CI pipeline, one
version tag, one release artifact (the frontend is embedded in the Go binary
— see ADR-0006). `docs/openapi.yaml` is the contract between the two trees
and lives in `docs/`.

## Consequences
**Positive:** atomic API+UI changes; one issue tracker, one CI, one release
process; refactors cross the boundary in a single PR.

**Negative:** CI must be conditioned (backend jobs skip on `docs/**`-only
changes and vice versa) to keep it fast; repo gets big — mitigated by
`docs/` staying design-only and generated artifacts never being committed.

**Neutral:** if the team splits later, the split line is exactly the
`backend/`/`frontend/` boundary plus the OpenAPI contract, so extracting
repositories later is mechanical.

## Alternatives
- **Two repos + published API client package** — rejected for now: release
  choreography and contract drift tooling cost more than the monorepo costs
  at this size.
- **Polyglot single repo without boundaries** — rejected: explicit
  directories keep Go tooling (`go test ./...`) and pnpm tooling clean.
