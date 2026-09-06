Source: MAG-48 — https://linear.app/magus-monitor/issue/MAG-48/nightly-reconcile-recalculates-the-whole-vehicle-metrics-history-on
Roadmap: openspec/roadmaps/RM44-incremental-supercharger-sync.md
Tier: 2 of 4 (`RM44-telemetry-add-query-logging` is tier 1 and is already archived — it
gave this tier its log lines to prove the fix. `RM44-charging-add-change-detecting-mirror`
and `RM44-charging-add-mirror-watermark` come after this tier and depend on it.)

## Why

Every night, the poller copies the full Supercharger history again. Today,
`telemetry.UpsertSuperchargerHistory` always sets `updated_at = now()`, even when a
row's data did not change. That is wrong. `updated_at` should mean "this row's data
changed", not "the last pass touched this row".

This false signal reaches `internal/analytics`. `analytics.Recalculator.Reconcile`
reads `updated_at` to decide what to recalculate. Measured on 2026-09-05: the run
touched a 70-day window and rewrote 51 of 51 rows, when only 4 days of real data had
changed. `internal/analytics` is not changed by this tier — its logic is already
correct. It just needs a true signal to read.

A second module, `internal/charging`, has the same bug in its own mirror
(`MirrorSuperchargerSession`). That is a separate tier (tier 3), done after this one,
because a worker stays inside one module.

## What Changes

- `internal/telemetry/db/query.sql`, query `UpsertSuperchargerHistory`: the
  `ON CONFLICT (session_id) DO UPDATE SET` clause keeps refreshing the same six
  columns it refreshes today (`raw_data`, `energy_kwh`, `total_cost`, `currency`,
  `is_paid`, `tesla_id`). It changes only how `updated_at` is set: `now()` only when
  the row's data actually changed, else the row keeps its old `updated_at`.
- The check compares the whole row as JSON, minus a fixed deny-list of columns that
  must never count as "data" for this check. See design.md D1–D3 for the full
  reasoning and the exact deny-list.
- `make sqlc` regenerates `internal/telemetry/db/query.sql.go`. The generated
  `UpsertSuperchargerHistoryParams` struct does not change — sqlc only sees a new SQL
  string, not a new parameter. No Go call site changes.
- New tests prove the six required behaviors from design.md's test contract,
  including a schema-driven test that fails on its own the day a new column is added
  to the table and forgotten in the deny-list (roadmap D16).
- `internal/telemetry/AGENTS.md` gains a short note under "Testing notes" pointing at
  the new test file(s) and the deny-list rule, so a future change that adds a column
  to `supercharger_history` finds this rule before it ships.

## Breaking

**No.** No Go interface changes. No struct field changes. No new export, no removed
export. The only behavior change is that `updated_at` now stays put on an unchanged
re-sync — every caller that reads `SuperchargerHistory.UpdatedAt` already treats it as
"when this last changed", so this tier makes that field finally tell the truth. No
caller relies on `updated_at` advancing every night.

## Modules Affected

- **`internal/telemetry/`** — the only module touched. One query, its generated Go
  file, and new tests.
- **`internal/charging/`, `internal/analytics/`, `internal/app/`** — not touched.
  Tier 3 fixes `charging`'s own copy of this bug. Tier 4 bounds `charging`'s read.
  `analytics` needs no change; it already reads `updated_at` correctly.
- **`internal/gateway/`** — not touched, and not reachable. The gateway cannot import
  `internal/telemetry` at all (`make boundary-guard`).

## Database Changes

**Yes — one query's write behavior, no schema change.** No new table, column, index,
constraint, or migration. `UpsertSuperchargerHistory`'s `SET` clause keeps the same
six columns it writes today; only its `updated_at` expression changes from an
unconditional `now()` to a conditional `CASE`. The `database` design gate applies
because this is a database write path change — see design.md for the full schema,
rationale, rejected alternatives, and index plan, per `openspec/config.yaml`
§rules.design.

**Makefile/guard impact — checked, none found.** No new migration directory entry, no
change to `MIGRATIONS_DIRS`, no new guard needed. `make sqlc` must run once after the
query text changes (already on the allowed-commands list).

## Read Paths Affected

**None directly, but this tier fixes what a later tier speeds up.** This tier's own
query is a nightly write, not a dashboard read. Its effect shows up one hop away: once
`updated_at` tells the truth, tier 4 (a later change) can bound `charging`'s mirror
read to "what changed since last time" instead of the full history, and that in turn
lets `internal/analytics` recalculate only real changes. This tier does not touch any
gateway route or dashboard query itself.

## Capabilities

### Added Capabilities

- **Change-detecting Supercharger-history upsert** — the nightly upsert now leaves
  `updated_at` untouched on an unchanged re-sync, and advances it only when the row's
  mirrored data actually changed. See `specs/telemetry/spec.md`.

### Modified Capabilities

None — no existing requirement changes for a caller. The set of columns the upsert
refreshes is exactly the same as before; only when `updated_at` moves is different.

### Out of scope (explicitly deferred)

- **`internal/charging`'s own copy of this bug.** Tier 3, its own change, its own
  module.
- **Bounding the mirror's read window.** Tier 4, depends on tiers 2 and 3 both
  landing first.
- **Any change to `internal/analytics`.** Roadmap D1: layer 3 is not touched by this
  roadmap at all.

## Testing

Per the Test-Execution-Policy: this tier writes tests but does not run the suite.
design.md fixes the exact expected values for every test **before** any code exists,
per `ai/go-conventions.md` "contract-first authoring". The tests are
`DATABASE_URL`-gated integration tests, because the behavior under test is a real
Postgres `ON CONFLICT` clause — no fake can stand in for it.

Exact commands for the owner to run once implementation lands:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only, once ready: `go test ./internal/telemetry/...` (or `make test` /
`make test-with-db` — Docker or `DATABASE_URL` required for the DB-backed tests to
actually run rather than skip).
