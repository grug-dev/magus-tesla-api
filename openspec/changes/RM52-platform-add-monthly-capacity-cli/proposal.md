# Proposal — RM52-platform-add-monthly-capacity-cli

Source: MAG-32 — https://linear.app/magus-monitor/issue/MAG-32/vehicle-monthly-metrics-new-table

Roadmap: `openspec/roadmaps/RM52-vehicle-monthly-metrics.md` — **tier 3 of 3**, module
`platform` (cross-cutting). Implements roadmap decisions **RD8, RD9, RD10** and the two
decisions the owner settled with the leader on 2026-09-10 (T1: local-only tool, T2: a new
`config.LoadDatabase()`). RD1–RD7 and RD11–RD14 belong to tier 1 or tier 2 (both archived)
and are not re-opened here.

Design gate: **NOT tripped.** This change adds no table, column, index, or migration. It
adds one runnable and one small config loader, and calls a port tier 1 already built.
`openspec/config.yaml` §design's database gate does not apply.

Unit tests: **included.** The pure logic this change adds — period parsing, the
previous-month default, and the new config loader — is small, has no I/O beyond `.env`
reading, and is worth locking down before the tool ships. Expected values are fixed in
`design.md` §Test Contract before implementation, per `ai/go-conventions.md` §Testing.

The interview of record is the roadmap's own `grill-me` session, 2026-09-10, plus one short
follow-up with the owner the same day that settled T1 and T2 (see `design.md`). This tier
does not re-open RD1–RD7 or RD11–RD14.

---

## Why

Tier 1 built the estimator and the table. Tier 2 wired it into the nightly cycle. Nothing
yet lets a person run the job by hand — to backfill a past month, or to re-run one vehicle
after fixing bad data. RD8 asks for exactly this: a `cmd/` tool, not a gateway page.

## What Changes

- **ADDED** — `cmd/monthly-capacity/main.go`: a new runnable. Flags `-period YYYY-MM`
  (default: the previous month) and `-tesla-id <id>` (default: every vehicle). Builds a
  pool from `config.LoadDatabase()`, calls
  `charging.NewMonthlyCapacityCalculator(pool).Calculate(ctx, period, teslaID)`, and prints
  one summary line. Exit code `0` on success, `1` on any error (bad flag, DB error,
  `Calculate` error).
- **ADDED** — `cmd/monthly-capacity/period.go`: the pure functions behind the flags —
  `parsePeriod`, `previousMonth`, `resolvePeriod`, `teslaIDPointer` (design.md D1–D3).
- **ADDED** — `cmd/monthly-capacity/period_test.go` — offline unit tests for the four pure
  functions above (design.md §Test Contract Groups A–B). `package main`: this binary,
  unlike `cmd/poller`, carries real logic worth testing directly (design.md D4).
- **ADDED** — `cmd/monthly-capacity/README.md` — usage, flags, examples, and the "local
  only" note (T1).
- **ADDED** — a `make cmd-monthly-capacity` target, matching the Makefile's `cmd-*`
  convention (`##` help line, `.PHONY` entry), accepting optional `PERIOD=` / `TESLA_ID=`
  make variables (design.md D5).
- **ADDED** — `internal/config.LoadDatabase() (string, error)`: a new, small loader
  mirroring `LoadMigration()` — loads `.env`, reads `DATABASE_URL`, errors when empty,
  returns it. Nothing else (design.md D7/T2).
- **ADDED** — `internal/config/config_test.go` gains `TestLoadDatabase_*` cases (design.md
  §Test Contract Group C).
- **CHANGED** — `internal/config/AGENTS.md`: §Public interface gains `LoadDatabase`,
  §Testing gains its new cases.
- **VERIFIED, NOT CHANGED** — `cmd/poller/main.go` already passes
  `charging.NewMonthlyCapacityCalculator(pool)` into `app.NewProcessor` (tier 2, F13).
  `cmd/web/main.go` builds the gateway only and RD8 keeps the gateway untouched, so it
  needs no change. This tier only confirms both facts; it does not touch either file.
- **CHANGED** — root `README.md`: "Project Structure" gains the new `cmd/monthly-capacity/`
  entry, the "Architecture" dependency graph gains one composition-root line, and the
  `internal/charging` row already documents the table (tier 1/2 — checked, not re-written).
- **CHANGED** — `cmd/README.md`: gains a row for the new binary in the binaries table.
- **CHANGED** — `internal/charging/AGENTS.md`: notes that `MonthlyCapacityCalculator` now
  has a second caller, the manual CLI, alongside `internal/app`'s nightly step.
- **CHANGED** — `kkpa/context/architecture/nightly-cycle.md`: the port-map row for
  `charging.MonthlyCapacityCalculator` gains the CLI as its second caller.
- **ADDED** — a new KB guide, `kkpa/context/workflows/vehicle-monthly-metrics.md`
  (RD10), documenting the whole monthly-metrics story end to end: the table, the
  estimator, the nightly step, the CLI, and where a future second metric would go (RD12).
  Run via the `kkpa-context-curate` skill — a **leader** task (see `tasks.md`).

**Out of scope, deliberately:**

- `deploy/docker/` — the tool is **local only** (T1, decided with the owner today). It does
  not ship in the production image, and no backlog entry is added for that — the owner
  declined one.
- `internal/gateway` — never touched by this roadmap (RD8).
- Any change to `internal/charging`, `internal/app`, or any migration — tiers 1 and 2 own
  those, and both are archived.
- Automatic backfill of any kind — RD9 is explicit: the tool is always run by hand.

## Breaking?

**NO.** This tier adds one new binary and one new, additive config function. No existing
exported type, function, or signature changes. `cmd/poller/main.go` and `cmd/web/main.go`
are read to confirm they need no edit, not written to.

## Modules affected

- **`platform`** (cross-cutting) — owner. New runnable, new `make` target, docs, KB.
- **`internal/config`** — one new exported function (`LoadDatabase`), additive, no
  existing function changed.
- **`internal/charging`** — **not affected.** This tier only calls the port tier 1 built.
- **`internal/app`**, **`internal/gateway`** — **not affected.**

## Read paths affected

Per `openspec/config.yaml` §proposal. **This change touches no read path used by any
dashboard or gateway request.** The only database activity is the same tier-1 batch job
(two `SELECT`s and one `UPSERT`, already justified in tier 1's design.md), now reachable
by hand as well as by the nightly cycle. Running the tool does not change that job's shape
or cost.

## Impact

- **Affected spec:** a new capability, `monthly-capacity-cli` — this tool did not exist
  before, so there is no existing capability to modify.
- **Affected code:** `cmd/monthly-capacity/` (new), `internal/config/config.go` (new
  function), `internal/config/config_test.go` (new cases), `internal/config/AGENTS.md`,
  `internal/charging/AGENTS.md`, `Makefile`, `README.md`, `cmd/README.md`,
  `kkpa/context/architecture/nightly-cycle.md`, and a new KB guide.
- **Design gate: not tripped.** No database object of any kind is added or changed.
- **`MIGRATIONS_DIRS` order check:** not applicable — no migration.
- **Docker / deploy:** deliberately **not** touched (T1). Recorded here so a future reader
  does not read the silence as an oversight.

## Modules affected — summary table

| Module | Change |
|---|---|
| `platform` | Owner. New runnable, `make` target, docs, KB guide. |
| `internal/config` | One new function, additive. |
| `internal/charging` | None — this tier only calls tier 1's existing port. |
| `internal/app` | None — this tier only verifies tier 2's wiring, already done. |
| `internal/gateway` | None, ever, per roadmap RD8. |
| `deploy/docker` | None, on purpose (T1) — the tool stays local only. |
