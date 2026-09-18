Source: MAG-73 — https://linear.app/magus-monitor/issue/MAG-73/analytics-monthly-metrics-table-distance-efficiency-and-copied
Roadmap: openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md
Tier: 1 of 4. Tier 2 (`analytics`) depends on this tier — it needs this read before it
can copy a vehicle's monthly capacity into its own new table. Tiers 3 and 4 depend on
tier 2 and do not touch `charging`.
Design gate: **not tripped.** This change creates or alters no database object — no
migration, no table, no column, no index, no constraint. It adds one read query against
`monthly_effective_capacity`, a table that already exists (RM52). `openspec/config.yaml`'s
blanket "design.md is REQUIRED for any DB-touching change" still applies — a new query is
a DB-touching change — so design.md documents the query, the index analysis, and the
rejected alternatives, without requiring the owner's gate confirmation a schema change
would need.
Unit tests: **integration-only**, matching RM30's precedent for this same kind of
change (a DB-boundary reader with no pure function of its own). The whole test surface is
the `DATABASE_URL`-gated suite, whose expected values are authored up front in design.md
§Test Contract (`ai/go-conventions.md` §Testing).

## Why

`analytics.vehicle_monthly_metrics` (tier 2) copies, for one vehicle and one exact month,
the pack capacity `charging` already measured that month. But `charging` exposes only one
read over `monthly_effective_capacity`: `LatestMeasuredCapacity`, which returns the
newest **non-NULL** row for a vehicle — the read `packCapacityKWh` needs for today's energy
math. That is the wrong question for tier 2. Tier 2 needs the answer for one specific,
possibly past, possibly thin month — not "the best answer available right now".

The table's own SQL comment says: *"Owned exclusively by internal/charging; no other
module reads this table directly."* This tier is what makes a lawful cross-module read
possible — a new port, not a workaround.

## What Changes

**Additive only — no schema change.**

- **ADDED** — `charging.MonthlyCapacityReader`, a one-method read port:
  `CapacityForMonth(ctx, teslaID, month) (capacityKWh *float64, found bool, err error)`.
  `found` is `false` when no row exists for that vehicle and exact calendar month —
  `capacityKWh` is then always `nil`. `found` is `true` and `capacityKWh` is `nil` when a
  row exists but that month's evidence was too thin to measure a capacity (a NULL
  `effective_capacity_kwh`). `found` is `true` and `capacityKWh` is non-nil for a
  measured month. This is the three states the tier-2 caller needs to tell apart, because
  its own column is `NOT NULL DEFAULT 0` (roadmap RD4) — it must know whether to write a
  real `0`, or fall back because nothing was measured yet.
- **ADDED** — `charging.NewMonthlyCapacityReader(pool *pgxpool.Pool) MonthlyCapacityReader`,
  declared in `charging.go`, implemented in the new `internal/charging/monthly_capacity_reader.go`
  (mirroring how `SessionReader`/`session_reader.go` and `MirrorWatermarkStore`/
  `mirror_watermark.go` are each their own port, their own file).
- **ADDED** — `chargingdb.EffectiveCapacityForPeriod`, one new sqlc query in
  `internal/charging/db/query.sql`, regenerated via `make sqlc`. No migration accompanies
  it — the existing `UNIQUE (tesla_id, effective_period)` constraint already builds the
  btree this query needs (design.md §"Index Plan").
- **ADDED** — a logging decorator, `loggingMonthlyCapacityReader`, in
  `internal/charging/query_log.go`, alongside this module's other nightly-path decorators
  (`MirrorWatermarkStore`, `MonthlyCapacityCalculator`). This port's only caller is the
  nightly cycle (roadmap RD3), the same class of caller query_log.go already instruments.
- **CHANGED** — `internal/charging/AGENTS.md` — documents the new port under §Public
  Interface, and adds the new file to both Allowed-Imports lists (`pgtype`, `chargingdb`)
  per the file's own stated rule (docs-track-structural-change, `CLAUDE.md`
  §Non-negotiables).
- **UNCHANGED** — the migration file, `LatestMeasuredCapacity`, `packCapacityKWh`,
  `MonthlyCapacityCalculator`, `estimateEffectiveCapacity`, `median`, and every file
  outside `internal/charging/`. No column, index, constraint, port or type is removed
  anywhere. The table's own SQL comment ("no other module reads this table directly") is
  now stale in letter — see design.md for the decision on updating it.

**Breaking:** no. Purely additive — a new type and a new interface method with no existing
caller yet (tier 2 is the first caller, in a later change).

**Modules affected:** `charging` only. No other module's code changes in this tier.
**`cmd/web` wiring** (constructing `charging.NewMonthlyCapacityReader(pool)` and injecting
it) is explicitly **not** part of this tier — the port has no caller until `internal/app`'s
nightly cycle exists (tier 4), and even then the caller is `internal/analytics` (tier 2),
not the gateway. Wiring is deferred to whichever later tier first needs a live instance.

## Read paths affected

Per `openspec/config.yaml` §proposal ("performance-sensitive proposals must name the read
paths they affect"):

- **One read path is added, with no caller yet.** `CapacityForMonth` is new, but nothing
  in this tier calls it — tier 2 is the consumer. It runs once per vehicle per sync
  (roadmap RD3: twice a night per vehicle, current month and previous month) — not a
  per-request read, and not on any user-facing hot path. It reuses
  `monthly_effective_capacity_tesla_id_effective_period_key` — the `UNIQUE (tesla_id,
  effective_period)` constraint's own btree, already indexing exactly the two columns
  this query filters on. No new index, no schema change.
- **No existing read path changes.** `LatestMeasuredCapacity` (`packCapacityKWh`'s own
  seam), `Reader`, `SessionReader`, `SuperchargerSessionAnalyticsReader`, and
  `MonthlyCapacityCalculator`'s own two batch queries are all untouched — their query
  plans, indexes and results are identical before and after this change.

## Impact

- **Affected specs:** `monthly-effective-capacity` (existing capability; **ADDED**
  requirement for the new per-month retrieval only — nothing about the existing
  measurement or newest-non-NULL requirements changes).
- **Affected code:** `internal/charging/` (`charging.go`, `monthly_capacity_reader.go`
  (new), `query_log.go`, `db/query.sql`, a new
  `db_monthly_capacity_reader_integration_test.go`, `AGENTS.md`).
- **Design gate:** not tripped — see header. design.md still carries the query, the index
  justification against the existing constraint, and the Test Contract, per
  `openspec/config.yaml`'s blanket design.md requirement for any DB-touching change.
- **Deferred, explicitly NOT in scope:** `cmd/web`/`internal/app` wiring (no caller until
  a later tier); any change to `LatestMeasuredCapacity`, `packCapacityKWh`,
  `MonthlyCapacityCalculator`, or the migration file; exposing `candidate_count` /
  `sample_count` to the caller (design.md decides against it — see D3).
