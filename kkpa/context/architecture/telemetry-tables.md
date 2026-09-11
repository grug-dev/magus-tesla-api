# telemetry tables — columns, constraints and the units history

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle_snapshots`, `poll_attempts`, `supercharger_history`, `poll_runs`,
  `captured_date`, `raw_data`, `sentry_mode`, `max_range_charge_counter`,
  `battery_pct_source`, `start_battery_pct`, `end_battery_pct`, `telemetry schema`,
  `supercharger_sessions` (the old name), `charge_gaps` (moved away)
- **Internal name:** the `telemetry` Postgres schema. `internal/telemetry` is its sole owner.

## Component map

| Layer | File / symbol | Role |
|---|---|---|
| schema source | `internal/telemetry/db/migrations/` | goose migrations, the only schema source of truth |
| generated | `internal/telemetry/db/` (package `telemetrydb`) | sqlc output. No other module imports it. |
| ports | `internal/telemetry/telemetry.go` | `Collector` / `Reader` / `SuperchargerHistoryReader` / `RunWriter` and the domain types |
| mapping | `internal/telemetry/mapping.go` | row → domain, the only place `pgtype` is unwrapped |
| rules | `internal/telemetry/AGENTS.md` | Which tables the module owns and the binding conventions. Points here for column detail. |

Why this guide exists: the column detail below used to live in `internal/telemetry/AGENTS.md`,
which every worker dispatched to this module re-reads in full. The ownership rules and the
port contracts stayed there; the schema detail is here.

## The tables

Owns five tables, all living in the dedicated **`telemetry` Postgres schema**
(`RM39-telemetry-move-to-own-schema` tier 4, MAG-31, migration `20260903000001`; moved
out of `public` — every table reference in `db/query.sql` and in this module's
`_test.go` files is schema-qualified as `telemetry.<table>`, and a bare, unqualified
table name in new code here is a bug, not a style choice, because sqlc resolves names
statically at generate time). Module-scoped `internal/telemetry/db` (goose migrations
are the single schema source; sqlc generates `telemetrydb`, which **no other module
imports**):

- `vehicle_snapshots` — **no longer append-only** (superseded by
  `telemetry-dedupe-daily-snapshots`, migration `20260805000001` — see below): at most one row
  per `(account_id, tesla_id, captured_date)`, enforced by the
  `vehicle_snapshots_account_tesla_date_unique` constraint. A same-day re-capture **REPLACES**
  the existing row via `ON CONFLICT ... DO UPDATE` — the newest capture for a calendar day always
  wins (design D1). Columns: `account_id`, `tesla_id`, `captured_at` (the precise capture
  instant), `captured_date DATE` (the calendar day, **Go-computed** from `captured_at` in the
  poller's configured timezone — `Config.Location`/`clock.CalendarDay`, design D2; never a DB expression,
  since a UNIQUE index cannot depend on the runtime `POLLER_TIMEZONE` env var), `raw_data JSONB`
  (lossless `vehicle_data`), plus extracted typed columns (battery level, rated range, charging
  state, charge limit, odometer, inside/outside temp, locked, `sentry_mode` **nullable**, car
  version, 5 charge-enrichment fields, and `max_range_charge_counter` **nullable** — see migration
  20260801000001). Dropped columns (`latitude`, `longitude`, `fast_charger_type`) remain lossless
  in `raw_data`. `updated_at TIMESTAMPTZ` is the audit trail for a replace: `DEFAULT now()` on a
  fresh insert, explicitly set to `now()` on a same-day conflict-update (design D5). The five
  per-day consumption figures once stored here were **dropped** by migration `20260822000001`
  (`RM29-telemetry-drop-derived-columns`) — the derivation now lives in `internal/analytics`,
  computed from this table's surviving `odometer_km` / `battery_level_pct` / `captured_date`
  columns via `Reader.SnapshotPrecedingDay` (see below); this module never read the dropped
  columns back, and their only consumer was another module.
- `poll_attempts` — **unaffected, still append-only/immutable** (design D4): one row per
  (vehicle, run): `account_id`, `tesla_id`, `attempted_at`, `outcome` (`success`|`failure`),
  `reason` (`ok`|`asleep-timeout`|`unauthorized`|`api-error`). Doubles as future availability /
  sleep-behavior data — a daily collapse would destroy that signal, so this table is explicitly
  out of scope for the dedupe change. **Gains two columns, migration `20260823000002`**
  (`RM29-app-add-process-vehicle-data`): `run_id UUID` (nullable, permanently unbackfilled for
  legacy rows) and `triggered_by TEXT NOT NULL DEFAULT 'scheduler'`. **This SUPERSEDES roadmap
  D2**, which had planned to move this table to a new `internal/app`-owned `process_runs` —
  that plan was reversed during this change's design (design.md D1): `poll_attempts` never held
  anything Tesla reported to begin with (every existing column is already "our own fact about our
  own attempt"), so adding `triggered_by` extends the table rather than blurring a boundary it
  never had. `internal/app` ends up owning no schema at all. Grain is unchanged (one row per
  vehicle per run, design.md D2); no CHECK, no index (nothing reads either column in this tier).
- `supercharger_history` — **renamed from `supercharger_sessions`** by
  `RM39-telemetry-move-to-own-schema` tier 4 (MAG-31, migration `20260903000001`,
  roadmap D5a/D5c) in the same migration that moved it into the `telemetry` schema —
  every catalog object that named the old table (7 constraints + 2 standalone indexes,
  all `supercharger_sessions_*` / `idx_supercharger_sessions_*`) was renamed alongside
  it, and the hand-written Go domain type followed: `SuperchargerSession` →
  `SuperchargerHistory` (`telemetry.go`). One row per Tesla `session_id` (UPSERT, not
  append-only: billing state — `is_paid`, invoice status — mutates post-session,
  migration `20260716000001`). Extended by `RM27-telemetry-add-supercharger-battery-pct`
  (MAG-14, migration `20260815000001`) with three nullable columns (originally five;
  `RM41-telemetry-drop-estimate-columns`, 2026-09-03, dropped the frozen
  verification-snapshot pair), all excluded
  from `UpsertSuperchargerHistory`'s (the sqlc query, renamed from `UpsertSuperchargerSession`
  by the same tier-4 change) `INSERT`/`ON CONFLICT DO UPDATE SET` (see "Battery-%
  verification columns" below for the full convention):
  - `start_battery_pct SMALLINT CHECK (0..100)`, `end_battery_pct SMALLINT CHECK (0..100)` —
    human-owned verification/override trio.
  - `battery_pct_source TEXT CHECK (IN ('user_verified', 'polled'))` — why the trio is set;
    NULL when no override exists.
- `charge_gaps` (the flagged-vehicle-day ledger, formerly owned here as
  `RM28-telemetry-add-charge-gap-storage`, MAG-15) **moved to `internal/analytics`** by
  `RM29-analytics-own-charge-gaps` (MAG-26 tier 5), table, migration, and `GapWriter` port
  together — this module never read it back after writing it, and its only consumer was
  another module.
- `poll_runs` — one row per `run_id` (PRIMARY KEY, no surrogate `id` — `RunContext.RunID`
  is already the row's natural, immutable identity, design D1), written **exactly once**
  per `app.ProcessVehicleData` invocation by `RunWriter.RecordRun`, including on the
  step-1 whole-cycle-failure short-circuit (a failed run still leaves an all-zero-counts
  trace, design D3). A plain single `INSERT`, never an upsert: a duplicate `run_id` is a
  caller bug and fails loudly on the PRIMARY KEY (design D11). No FK to/from
  `poll_attempts` — the two tables correlate only through the application-known `run_id`
  value, since a `poll_runs` row is written after every `poll_attempts` row for that run
  already exists (design D2). Columns reproduce the poller's per-cycle log line without a
  join: `triggered_by`, `started_at`/`finished_at`/`duration_seconds` (all `NOT NULL` —
  design D3), the account-grain counts (`accounts_attempted`/`_succeeded`/`_failed`), the
  vehicle-grain counts and per-reason failure counts, `tesla_api_calls` (every Tesla Fleet
  API request this run made, counted regardless of success/failure by the module's
  internal `callCounter` decorator, design D9/D10), and the three
  charging/config-capture counts already on `CycleReport`. **No read port yet** — no
  `Reader`-style method, no gateway page (design D5/backlog); the only way to observe a
  row today is direct SQL or a future gateway read surface. No index beyond the PK's
  automatic B-tree (design D5: nothing reads this table in this tier, and the future
  "list recent runs" read stays a cheap sequential scan at this table's write volume for
  years). Schema/rationale: `openspec/changes/RM36-telemetry-add-poll-runs/design.md`
  (MAG-35) — moves under `openspec/changes/archive/` once this change is archived.

`account_id`/`tesla_id` are plain columns (no cross-module FK, D2 of the change design). `pgtype`
never leaves the module — convert to/from plain domain types at the DB→domain mapping boundary
(`ai/go-conventions.md` §persistence), mirroring the account module.

## Units — how the current rule came about

Platform-wide unit rule (normative): `openspec/specs/unit-of-measure/spec.md` and
`ai/go-conventions.md` §Coding Rules / §Persistence — display units, unit-suffixed names,
conversion once on write, never on read. This section records `telemetry`'s own application of
that rule: `vehicle_snapshots` is the table the platform rule was generalised from.

- **SUPERSEDED (telemetry-store-display-units, migration `20260806000001`): units are now stored
  in their DISPLAY unit, converted exactly once at capture time.** `vehicle_snapshots` stores km /
  °C / PSI, not miles/bar — the opposite of the rule this section used to state. Every unit-bearing
  column and `Snapshot` field carries its unit as a name suffix (`OdometerKm`, `TpmsPressureFLPSI`,
  `InsideTempC`, …), so the unit is self-describing at every call site. This module holds **zero**
  conversion constants and exposes **zero** read-time conversion methods — `milesToKm` and
  `barToPSI` were deleted from `telemetry.go`; the six companion methods that used to convert
  `BatteryRange`/`Odometer`/`TpmsPressure*` on read were deleted too (a field and a method of the
  same name cannot coexist in Go, so renaming the fields to carry the unit forced the deletion —
  design D4). Conversion happens exactly once, in `snapshotFrom` (`service.go`), by calling the
  `tesla` adapter's `Km()`/`PSI()` companions on the vendor DTO — **never** by multiplying inline
  here. `internal/tesla` remains the single owner of `milesToKm` / `barToPSI`; this module only
  calls its companions.
- This inverts the prior "API-native storage, derived on read" rule specifically because reads
  vastly outnumber writes (CLAUDE.md's read-heavy Performance-Profile): the nightly poller writes
  once per vehicle per night, while every dashboard render, chart, and API consumer used to pay
  the conversion cost on every read. Converting once at write time and never on read matches that
  asymmetry.
- The **NULL-vs-zero invariant is unchanged and still binding**: for nullable columns (the four
  TPMS pressure fields and the Source A charge-enrichment fields), NULL means "not reported at
  capture, or the row predates this extraction"; a truthfully reported `0` is stored non-NULL via
  `ptr()` in `snapshotFrom`, applied to the ALREADY-CONVERTED value (D12/DSA3 convention).
- Tesla temperatures are already Celsius — `InsideTempC`/`OutsideTempC` are assigned straight from
  the DTO with no conversion. Domain types carry **no** vendor suffix; only the `tesla` adapter's
  `...Tesla` DTOs unmarshal Tesla JSON, and this module maps them into clean `Snapshot`s.
- `raw_data JSONB` is UNCHANGED by this and stays lossless in the Fleet API's native units (miles/
  bar) — only the typed, extracted columns moved to display units. A backfill from `raw_data` must
  still apply the conversion itself; the raw payload was never converted.
- `SentryMode` is `*bool` end to end (nil = not reported, `*false` = off, `*true` = on) mapping to a
  nullable column — preserve the fidelity, never collapse absent into false.
- `MaxRangeChargeCounter` is `*int` end to end (nil = pre-migration row / not reported, `*0` = new
  vehicle / never charged to max-range, `*N` = charged to max-range N times). Mirrors the same
  pointer-wrap convention as the 5 Source A charge-enrichment fields (D12/DSA3). SQL NULL for
  pre-migration rows; the Up migration backfills from `raw_data->'charge_state'->'max_range_charge_counter'`
  where the JSONB path exists.
- **The five derived-consumption figures no longer live here.** `DistanceTraveledKmCalc`,
  `BatteryUsedPctCalc`, `KmPerPctCalc`, `EstimatedRangeKmCalc`, and `DaysSpannedCalc` were dropped
  from `Snapshot` (and their columns from `vehicle_snapshots`) by `RM29-telemetry-drop-derived-columns`
  (migration `20260822000001`). The derivation moved to `internal/analytics` (`consumption.go`),
  which computes the same figures from this table's surviving `OdometerKm` / `BatteryLevelPct` /
  `CapturedDate` fields, fetching the exact predecessor via the new `Reader.SnapshotPrecedingDay`
  (below) instead of reading a value this module used to precompute. This module never read the
  dropped columns back after writing them — their only consumer was `internal/analytics`, which
  is exactly the boundary blur `RM29-modular-monolith-boundaries` exists to fix.

## `SuperchargerHistory` gains three pointer fields (`StartBatteryPct *int`, `EndBatteryPct *int`,

`BatteryPctSource *string`), mapped by
`rowToSuperchargerHistory` (`mapping.go`) via the new `pgNullableInt16AsInt` helper (first
`SMALLINT`/`pgtype.Int2` column in this module; reused for both `SMALLINT` fields) and the
existing `pgNullableText` helper for `BatteryPctSource`.

> **SCOPE NOTE (2026-08-15) — there is no estimator, and there will not be one under RM27.**
> RM27 originally planned two further tiers: a taper-curve SOC estimator in `internal/analytics`
> and a gateway page rendering it. **Both were descoped by the owner**; RM27 shipped these three
> columns (plus a since-dropped reserved pair — see the next bullet) and nothing else. Wherever the text below says an estimate is computed "on read",
> read that as *not implemented* — the platform computes no SOC estimate anywhere. The columns
> remain a purely human-owned channel with no writer yet. Deferred work: backlog entry 11.

- **Trio NULL convention:** `StartBatteryPct`/`EndBatteryPct`/`BatteryPctSource` all `nil` means
  "no value has been recorded". A non-nil trio means a human verified/overrode the value;
  `BatteryPctSource` records why (`"user_verified"` or `"polled"`). Nothing fills a NULL trio
  today — no fallback, no estimate.
- **Never auto-written (R3/R7):** all three columns are excluded from
  `UpsertSuperchargerHistory`'s `INSERT` column list and its `ON CONFLICT DO UPDATE SET` clause
  — deliberately, not an oversight (design D3). The nightly poller re-upserts every session
  because Tesla billing state (`is_paid`, invoices) mutates post-session; if any of these three
  were bound as a query parameter, a human-verified value would be silently overwritten on the
  next nightly re-upsert. **No writer for any of the three columns exists anywhere in this
  repository as of this change** — the future verification UI's Writer port is out of scope
  here (backlog entry 11).
- **`BatteryPctSource` never stores `"estimated"`** (R4/R6): `BatteryPctSource` only ever
  describes why a **verified** trio exists. With the estimator descoped nothing produces an
  estimate at all; and were one ever added, the distinction would still be carried structurally
  (by column presence), never by a stored label, since a persisted estimate goes stale the
  moment the model behind it changes — exactly what R6 forbids.
- **The frozen verification-snapshot pair no longer exists.** RM27 (2026-08-15)
  kept it reserved, unwritten, so a future SOC estimator could land without a
  migration. That reservation is now obsolete: the estimator MAG-36 eventually
  shipped (`derivedStartBatteryPct`, `internal/charging/capacity.go`,
  2026-09-01) writes the real `start_battery_pct` column instead of a frozen
  snapshot column — so `RM41-telemetry-drop-estimate-columns` (2026-09-03)
  dropped both columns from `telemetry.supercharger_history` along with the
  two `SuperchargerHistory` fields and the RM27-D6 comment block that
  described them. Full history of the original design:
  `openspec/changes/archive/2026-08-15-RM27-telemetry-add-supercharger-battery-pct/design.md`
  D6 (superseded).

## Related KB

- `architecture/telemetry-ingest-only.md` — the ingest-only rule and the consumer map
- `architecture/schema-per-module.md` — why each module gets its own Postgres schema
- `architecture/charging-tables.md` — the mirror downstream of `supercharger_history`
- `architecture/nightly-cycle.md` — the cycle that writes these tables
- `entities/vehicle-metrics/guide.md` — the analytics read model derived from snapshots
