# Session Usage — 2026-09-14

Phases 2–4 of the syslogq roadmap (auth + ingest + query API, React web UI,
CI bring-up), verified end-to-end with CI green on main.

## Lines created (git, exact — 7 commits pushed to main)

| Commit | What | Insertions |
|---|---|---|
| `d33e367` | Phase 2+3: auth, ingest, LogsQL query API | 3,973 |
| `9c35ac4` | Phase 4: React UI (explorer/dashboard/system) + embed | 4,661 |
| `28dbc2b` | Go 1.27 toolchain bump | 4 |
| `03647f1` | CI fixes (setup-node lock path, Docker outDir) | 4 |
| `214b81a` | Container state-dir ownership for nonroot | 17 |
| `d8091e9` | Black-screen fix (null vs empty array) + embed placeholder | 39 |
| `77c202d` | Scaffolding cleanup | −1 |

**Total: 8,694 insertions, 113 deletions, 70 files**

Plus ~334 lines of throwaway verification harness in `/tmp`
(Playwright walkthroughs, Firefox/curl debug scripts) — never committed.

## Token / credit usage

Exact billed credits are not visible from the agent side (platform
telemetry only). Session transcript measured **6.4 MB JSONL**; a naive
chars÷4 estimate is ~1.6M tokens, overstating true model-context usage
because the transcript includes base64 screenshots, CI logs, and tool
schemas that never enter the prompt intact.
