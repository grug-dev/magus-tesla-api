# Design — RM52-charging-add-monthly-effective-capacity

> **Design gate: `database`.** This change adds a new table, `charging.monthly_effective_capacity`,
> and two new read queries against existing tables. Per `CLAUDE.md` §Pipeline config →
> `Design-Gates: database` and `openspec/config.yaml` §design, **the owner must confirm this
> document before any implementation task is dispatched.** The DDL itself was confirmed once
> already, at the roadmap's own database design gate (roadmap RD11 header) — this confirmation is
> the second one, now that the table lives in `charging` instead of `analytics`. One character of
> the DDL must not change without re-confirming again.

Source ticket: MAG-32 · Roadmap: `openspec/roadmaps/RM52-vehicle-monthly-metrics.md`, tier 1 of 3.
Roadmap decisions **RD1–RD11** are binding and were confirmed with the owner at the 2026-09-10
`grill-me` interview. This document does not re-open them; where it says something the roadmap does
not, it says so explicitly (its own **D**-numbered decisions below).

---

## Context

Facts that constrain every decision here. Each was read out of the repository or the roadmap, not
recalled.

1. **`packCapacityKWh(ctx, vin string) (float64, error)` is unexported, returns a hardcoded `62.0`,
   and is called from exactly two places** (roadmap F4): `resolveEnergy` in `service.go`, and
   `VerifySession` in `session_verifier.go`. Neither caller is public; both are reachable only
   through `Writer.Create`/`Update` and `SessionVerifier.VerifySession`.
2. **`Entry.InferredCapacityKWhCalc` and `Session.InferredCapacityKWhCalc` already exist** as
   `*float64`, computed by PostgreSQL as `GENERATED ALWAYS AS (...) STORED` columns (roadmap F1).
   Each is non-`NULL` only when both battery percentages are present and the delta is strictly
   positive — this change never has to re-derive that guard; it only has to read the column.
3. **`Entry.EnergySource` is `USER` / `ESTIMATED`.** `Session` has no such field at all — Supercharger
   energy is metered by Tesla, so there is nothing to mark as estimated on that table (roadmap F2).
4. **`SessionStatus` is `IN_PROGRESS` / `DONE_CALCULATED` / `DONE`.** `DONE_CALCULATED` means
   `derivedStartBatteryPct` computed the start percentage from `energy_kwh` and the existing
   `62.0` constant — so that row's own `inferred_capacity_kwh_calc` is *exactly* `62.0`, by
   construction, not by coincidence (roadmap F3, RD2).
5. **`Entry.TeslaID` is `int64`, never nil; `manual_charge_entries.tesla_id` is `BIGINT NOT NULL`**
   (roadmap F5). **`Session.TeslaID` is `*int64`; `supercharger_sessions.tesla_id` is nullable**,
   refreshed on every nightly mirror pass, `NULL` when the VIN is not a currently-registered
   vehicle (roadmap F6).
6. **`internal/app.ProcessVehicleData` runs step 1 → 2 → 3, with a short-circuit if step 1 fails**
   (roadmap F7). This tier does not touch that function — tier 2 adds step 4.
7. **`charging` owns its own PostgreSQL schema (`charging`) and its own goose migrations + sqlc.**
   The highest migration version in the whole repository is `20260909000001`, in this module
   (roadmap F8, re-verified below).
8. **`charging` imports no other domain module, and this tier does not change that** (roadmap F9).
9. **`service.go`'s `store` interface is the seam that makes `Writer`'s higher-level logic
   offline-testable with a fake** — its own doc comment states this explicitly. `SessionVerifier`
   (`session_verifier.go`) is deliberately **not** part of it: it "is tested only via real
   `DATABASE_URL`-gated integration tests (no fake)" and talks to `chargingdb.Queries` directly.
   This shapes **D3** below: `packCapacityKWh`'s new seam must be usable from both shapes without
   forcing `SessionVerifier` to join `store`, and without forcing `store`'s fake-testability to
   depend on a real database.
10. **`internal/charging`'s existing helpers already cover every pgtype conversion this change
    needs**: `pgNumericToFloat64Ptr` (`service.go`), `pgFloat8ToFloat64Ptr`, `pgInt8ToInt64Ptr`
    (`session_writer.go`), `pgInt2ToIntPtr`, `dateFromTime` (`service.go`). No new conversion
    helper is needed anywhere in this change.
11. **Both existing indexes on `manual_charge_entries` and `supercharger_sessions` lead with
    `account_id`** (`idx_manual_charge_entries_account_time`,
    `idx_charge_sessions_account_time`/its schema-moved successor). This change's two new batch
    read queries have **no `account_id` predicate at all** (RD5 pools across accounts), so neither
    existing index can serve them — this is addressed in §Index Plan below, not assumed away.

## Verified: no migration-version collision (re-checked for this change specifically)

Roadmap F8 recorded the repo's highest version as `20260909000001` (this module's own
`RM51-charging-derive-status-and-price-source` migration, already merged to this branch). A
repo-wide check across every module's `db/migrations/` directory, run while writing this document,
confirms no file anywhere uses `20260909000002` — the version chosen below. `make migration-guard`
is the deterministic backstop; this is the check that precedes it.

## Goals / Non-Goals

**Goals**

- A per-vehicle, per-month measured pack capacity, computed only from evidence that cannot feed
  itself back into the constant it is meant to replace (RD2).
- `packCapacityKWh` reads that measurement instead of returning a constant, with **zero** behaviour
  change until the first month is actually computed (RD4).
- The estimator is a pure function, unit-testable with no database, no container, no `ctx` (Tier 1
  scope statement).
- No cross-module port, no cross-module import, no import cycle (RD1, RD14).
- No historical row is rewritten. `ESTIMATED` entries and `DONE_CALCULATED` sessions keep their
  `62.0`-derived values forever (roadmap "Future work").

**Non-Goals**

- The nightly-processor trigger — tier 2, `RM52-app-add-monthly-capacity-step`.
- The `cmd/monthly-capacity` runnable and its `make` target — tier 3,
  `RM52-platform-add-monthly-capacity-cli`.
- Any gateway surface. `internal/gateway` is never touched by this roadmap (RD8).
- The wide, multi-metric `analytics.vehicle_monthly_metrics` table the original ticket sketched —
  deferred until a second monthly metric exists (RD12).
- Making `supercharger_sessions.tesla_id NOT NULL` — its own ticket (RD13).
- Recomputing historical `ESTIMATED`/`DONE_CALCULATED` values with a measured capacity — explicitly
  rejected (roadmap "Future work").

---

## Database Changes

### The migration

`internal/charging/db/migrations/20260909000002_add_monthly_effective_capacity.sql`

```sql
-- +goose Up
-- monthly_effective_capacity: one measured pack-capacity estimate per vehicle per
-- month (RM52-charging-add-monthly-effective-capacity, MAG-32, roadmap RD1/RD3/
-- RD4/RD5/RD10/RD11). Owned by internal/charging -- no other module may import the
-- generated chargingdb package (ai/architecture.md §2).
--
-- WHY IN charging, NOT analytics (roadmap RD1/RD14, ai/architecture.md §2 "First
-- ask where the fact belongs, not how to break the cycle"). Every input row --
-- manual_charge_entries.inferred_capacity_kwh_calc and
-- supercharger_sessions.inferred_capacity_kwh_calc -- already belongs to charging.
-- charging owns the derivation and the table for the same reason it owns the rows
-- the derivation reads: a module that could become a separate service owns its
-- data AND the facts derived from it. No cross-module port exists for this table.
--
-- NO account_id (roadmap RD5). This is the only table in charging's schema
-- without account scoping, deliberately: it describes a battery pack, not user
-- data. One tesla_id is one car, whoever registered it -- GROUP BY tesla_id
-- (done in Go, not SQL -- see monthly_capacity.go) pools every account's rows
-- automatically, with no extra code.
--
-- NO FK on tesla_id, and NO raw_data JSONB -- the same two reasons every sibling
-- table in this schema already documents: a cross-module FK into the account
-- module's tables would couple this migration to a schema this module does not
-- own (ai/architecture.md §2), and this table stores a computed conclusion, not a
-- vendor payload, so there is nothing lossless to preserve.
--
-- effective_capacity_kwh IS NULLABLE ON PURPOSE (roadmap RD4). NULL means "under
-- minSamples valid rows this period" -- never a guessed number. packCapacityKWh's
-- read (LatestMeasuredCapacity, below) skips NULL rows and reads the newest
-- non-NULL one instead, falling back to a hardcoded 62.0 only when no measured
-- row exists at all for this vehicle. A stored number therefore always means "we
-- measured this."
--
-- candidate_count and sample_count are NOT NULL, DEFAULT 0, both counted at
-- different stages of the same pipeline -- added together at the owner's
-- design gate on 2026-09-10 (roadmap RD11, design.md D1). candidate_count is
-- every RD2-valid record this month, counted BEFORE the RD3 delta gate;
-- sample_count is the subset that survived the gate and actually produced the
-- median. Without candidate_count, "20 small top-ups, none big enough to
-- measure" and "no charging activity at all" would both read as
-- sample_count 0 -- indistinguishable without querying the source tables by
-- hand. A row exists only when candidate_count >= 1 (design.md D2): the job
-- iterates the tesla_ids it actually observed, so a vehicle with zero valid
-- records that month has no row, never a row with candidate_count = 0.
--
-- effective_period is always the FIRST DAY of the month it summarizes (roadmap
-- RD7), enforced by the CHECK below rather than left to caller discipline.
CREATE TABLE charging.monthly_effective_capacity (
    id                     UUID   PRIMARY KEY DEFAULT gen_random_uuid(),
    tesla_id               BIGINT NOT NULL,
    effective_period       DATE   NOT NULL,

    effective_capacity_kwh DOUBLE PRECISION,
    candidate_count        INTEGER NOT NULL DEFAULT 0,
    sample_count           INTEGER NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT monthly_effective_capacity_period_is_month_start
        CHECK (EXTRACT(DAY FROM effective_period) = 1),
    UNIQUE (tesla_id, effective_period)
);

COMMENT ON TABLE charging.monthly_effective_capacity IS
    'One measured pack-capacity estimate per vehicle per month '
    '(RM52-charging-add-monthly-effective-capacity, MAG-32). Computed monthly '
    'from this module''s own valid charge records (manual_charge_entries where '
    'energy_source = USER, supercharger_sessions where status = DONE) -- never '
    'from ESTIMATED or DONE_CALCULATED rows, whose numbers were themselves '
    'derived by dividing by this module''s hardcoded 62.0 constant (roadmap '
    'RD2). No account_id: this describes a battery pack, not user data, and one '
    'tesla_id pools every account''s rows. Owned exclusively by '
    'internal/charging; no other module reads this table directly.';

COMMENT ON COLUMN charging.monthly_effective_capacity.effective_capacity_kwh IS
    'The measured pack capacity in kWh for this vehicle and month, or NULL when '
    'fewer than minSamples (a Go constant, currently 3) valid records survived '
    'the minimum-delta gate this period (roadmap RD3/RD4). NULL never means '
    '"guessed" -- packCapacityKWh''s read skips NULL rows and reads the newest '
    'non-NULL one instead, falling back to a hardcoded 62.0 only when no '
    'measured row exists at all for this vehicle.';

COMMENT ON COLUMN charging.monthly_effective_capacity.candidate_count IS
    'How many records passed the RD2 filter this period, counted BEFORE the '
    'RD3 minimum-delta gate (roadmap RD11, added at the design gate on '
    '2026-09-10 -- design.md D1). Always >= 1 when a row exists -- a vehicle '
    'with zero valid records gets no row at all (design.md D2). Compare '
    'against sample_count: when the two differ, every record that did not '
    'make sample_count was dropped by the delta gate, not missing entirely.';

COMMENT ON COLUMN charging.monthly_effective_capacity.sample_count IS
    'How many candidate_count records also survived the minDeltaPct gate this '
    'period -- the exact slice the median was computed over -- whether or not '
    'that count reached minSamples. Written on every run, including a thin '
    'one, so a thin month is visible instead of silent (roadmap RD4).';

-- Index Plan (roadmap RD11): no separate CREATE INDEX. The UNIQUE (tesla_id,
-- effective_period) constraint's own btree serves both operations completely --
-- equality on the leading column (tesla_id) then a backwards range scan on the
-- second (effective_period DESC) for the read below, and the exact conflict
-- target for the upsert:
--
--   SELECT effective_capacity_kwh
--     FROM charging.monthly_effective_capacity
--    WHERE tesla_id = $1 AND effective_capacity_kwh IS NOT NULL
--    ORDER BY effective_period DESC
--    LIMIT 1;
--
--   INSERT INTO charging.monthly_effective_capacity (...) VALUES (...)
--   ON CONFLICT (tesla_id, effective_period) DO UPDATE SET ...;
--
-- Mirrors how vehicle_metrics and charge_gaps each reason about their own
-- UNIQUE constraint (roadmap RD11).

-- +goose Down
DROP TABLE IF EXISTS charging.monthly_effective_capacity;
```

### Resulting columns

| Column | Type | Null | Default | Constraint | Indexed |
|---|---|---|---|---|---|
| `id` | `UUID` | NOT NULL | `gen_random_uuid()` | PRIMARY KEY | yes (PK) |
| `tesla_id` | `BIGINT` | NOT NULL | — | part of `UNIQUE` | yes (leading, via `UNIQUE`) |
| `effective_period` | `DATE` | NOT NULL | — | `CHECK` (1st of month), part of `UNIQUE` | yes (second, via `UNIQUE`) |
| `effective_capacity_kwh` | `DOUBLE PRECISION` | NULL | — | — | no |
| `candidate_count` | `INTEGER` | NOT NULL | `0` | — | no |
| `sample_count` | `INTEGER` | NOT NULL | `0` | — | no |
| `created_at` | `TIMESTAMPTZ` | NOT NULL | `now()` | — | no |
| `updated_at` | `TIMESTAMPTZ` | NOT NULL | `now()` | — | no |

### Query changes (`internal/charging/db/query.sql`)

Four new queries, plus one column added to an existing query. All are appended at the end of the
file, after the `MirrorWatermark`/`AdvanceMirrorWatermark` block — this table has no relation to
the mirror-watermark table beyond living in the same schema.

```sql
-- name: ListValidManualEntryCapacitiesForPeriod :many
-- RD2's manual_charge_entries branch: only energy_source = 'USER' rows are
-- honest capacity evidence -- an 'ESTIMATED' row's energy was itself derived by
-- dividing by packCapacityKWh, so averaging it feeds the constant back into
-- itself (roadmap RD2, internal/charging/AGENTS.md §Data Ownership).
-- inferred_capacity_kwh_calc IS NOT NULL is the existing GENERATED-column guard
-- (20260829000001): it is non-NULL only when both battery percentages are
-- present AND end_battery_pct > start_battery_pct, so this query never returns
-- a NULL capacity or a NULL percentage -- the caller (monthly_capacity.go) does
-- not need to re-check that. tesla_id is BIGINT NOT NULL on this table (roadmap
-- F5) -- no NULL-skip needed here, unlike the session query below.
SELECT tesla_id,
       inferred_capacity_kwh_calc,
       start_battery_pct,
       end_battery_pct
  FROM charging.manual_charge_entries
 WHERE energy_source = 'USER'
   AND inferred_capacity_kwh_calc IS NOT NULL
   AND charged_on >= @period_start
   AND charged_on <  @period_end;

-- name: ListValidSessionCapacitiesForPeriod :many
-- RD2's supercharger_sessions branch: only status = 'DONE' rows are honest
-- capacity evidence -- 'DONE_CALCULATED' means derivedStartBatteryPct computed
-- start_battery_pct as endPct - energyKWh/62.0*100, which cancels back to
-- exactly 62.0 in this formula (roadmap RD2); 'IN_PROGRESS' has no complete
-- percentage pair at all, so its inferred_capacity_kwh_calc is already NULL.
-- tesla_id IS NOT NULL enforces RD5: a session whose VIN is not a
-- currently-registered vehicle cannot be attributed to a car. Supercharger
-- energy is metered by Tesla, so (unlike the manual_charge_entries branch)
-- there is no energy_source-equivalent column to check here.
SELECT tesla_id,
       inferred_capacity_kwh_calc,
       start_battery_pct,
       end_battery_pct
  FROM charging.supercharger_sessions
 WHERE status = 'DONE'
   AND tesla_id IS NOT NULL
   AND inferred_capacity_kwh_calc IS NOT NULL
   AND charge_stop_date_time >= @period_start
   AND charge_stop_date_time <  @period_end;

-- name: UpsertMonthlyEffectiveCapacity :exec
-- Store one vehicle's monthly estimate (roadmap RD11's write seam, RD9's
-- backfill/re-run path). ON CONFLICT so a re-run of the same period -- a manual
-- backfill, or a corrected earlier run -- updates the existing row instead of
-- erroring or duplicating it. effective_capacity_kwh is bound as a nullable
-- Float8: NULL when the caller's estimate was NULL (RD4's thin-month case),
-- never coerced to a fabricated number. candidate_count and sample_count are
-- both plain, non-nullable integers -- the two-stage count the owner approved
-- at the design gate (design.md D1): candidate_count before the delta gate,
-- sample_count after it.
INSERT INTO charging.monthly_effective_capacity (
    tesla_id, effective_period, effective_capacity_kwh, candidate_count, sample_count
) VALUES (
    @tesla_id, @effective_period, @effective_capacity_kwh, @candidate_count, @sample_count
)
ON CONFLICT (tesla_id, effective_period) DO UPDATE SET
    effective_capacity_kwh = EXCLUDED.effective_capacity_kwh,
    candidate_count        = EXCLUDED.candidate_count,
    sample_count           = EXCLUDED.sample_count,
    updated_at             = now();

-- name: LatestMeasuredCapacity :one
-- packCapacityKWh's own read (roadmap RD11, RD4). Returns the newest row for
-- this vehicle whose effective_capacity_kwh IS NOT NULL -- so a current thin
-- month (NULL) never hides an earlier real measurement; the caller in Go
-- translates pgx.ErrNoRows (no measured row exists at all yet) to the
-- defaultPackCapacityKWh fallback, never an error.
SELECT effective_capacity_kwh
  FROM charging.monthly_effective_capacity
 WHERE tesla_id = @tesla_id
   AND effective_capacity_kwh IS NOT NULL
 ORDER BY effective_period DESC
 LIMIT 1;
```

`LockSessionForVerification` gains one column, `tesla_id`, between `vin` and `energy_kwh` — no
other change to that query:

```sql
-- name: LockSessionForVerification :one
-- (existing comment unchanged, plus:) tesla_id is now also selected: RD11's
-- second caller row needs it to decide, before calling packCapacityKWh at all,
-- whether this session is even attributable to a registered vehicle.
SELECT vin, tesla_id, energy_kwh FROM charging.supercharger_sessions
WHERE id = @id
  AND account_id = @account_id
FOR UPDATE;
```

### `sqlc.yaml`

One new line in the existing `charging` module's `rename:` block (`sqlc.yaml`, the `charging`
entry documented in `ai/go-conventions.md` §Persistence):

```yaml
rename:
  charging_manual_charge_entry:        "ManualChargeEntry"
  charging_supercharger_session:       "SuperchargerSession"
  charging_mirror_watermark:           "MirrorWatermark"
  charging_monthly_effective_capacity: "MonthlyEffectiveCapacity"
```

No other line in `sqlc.yaml` changes. The table name is already singular (`monthly_effective_
capacity`, roadmap RD10 — not pluralized like its three siblings), so this rename entry is not
stripping a plural the way the other three do; it exists purely for a readable, explicit Go type
name rather than relying on sqlc's own schema-prefixed default.

### Expected sqlc diff (verify after `make sqlc`; report anything else)

- `db/models.go` gains **one new struct**, `MonthlyEffectiveCapacity`, with fields `ID
  uuid.UUID`, `TeslaID int64`, `EffectivePeriod pgtype.Date`, `EffectiveCapacityKwh
  pgtype.Float8`, `CandidateCount int32`, `SampleCount int32`, `CreatedAt pgtype.Timestamptz`,
  `UpdatedAt pgtype.Timestamptz`. `ManualChargeEntry`, `SuperchargerSession`, and
  `MirrorWatermark` are **untouched**.
- `db/query.sql.go` gains:
  - `ListValidManualEntryCapacitiesForPeriodRow` (`TeslaID int64`, `InferredCapacityKwhCalc
    pgtype.Numeric`, `StartBatteryPct pgtype.Int2`, `EndBatteryPct pgtype.Int2`) and its
    `Params` (`PeriodStart pgtype.Date`, `PeriodEnd pgtype.Date`).
  - `ListValidSessionCapacitiesForPeriodRow` (`TeslaID pgtype.Int8`, `InferredCapacityKwhCalc
    pgtype.Numeric`, `StartBatteryPct pgtype.Int2`, `EndBatteryPct pgtype.Int2`) and its
    `Params` (`PeriodStart pgtype.Timestamptz`, `PeriodEnd pgtype.Timestamptz`).
  - `UpsertMonthlyEffectiveCapacityParams` (`TeslaID int64`, `EffectivePeriod pgtype.Date`,
    `EffectiveCapacityKwh pgtype.Float8`, `CandidateCount int32`, `SampleCount int32`) and the
    `UpsertMonthlyEffectiveCapacity` method (`:exec`, no return row).
  - `LatestMeasuredCapacity(ctx, teslaID int64) (pgtype.Float8, error)` — a single-column
    query returns the scalar type directly, not a row struct.
  - `LockSessionForVerificationRow` gains **exactly one** field, `TeslaID pgtype.Int8`, inserted
    between `Vin` and `EnergyKwh`. Its `Scan(...)` call gains one argument in the same position.
  - **No other `*Params` or `*Row` type changes.** In particular, `CreateEntryParams`,
    `UpdateEntryParams`, `MirrorSuperchargerSessionParams`, `VerifySuperchargerSessionParams`,
    and every `List*Params` for the two existing tables are untouched. **If one did, stop and
    report it** — it means a query outside this change's scope was edited.

---

## Index Plan

Mandatory under `openspec/config.yaml` §design ("an index plan justified against the project's
read patterns") and `CLAUDE.md` §Design-Gates. Two tables are affected: the new one (already
covered above) and the two existing ones, which gain new read patterns for the first time since
they were created.

### The new table's read pattern

Already covered in §"Index Plan" inside the migration itself: `LatestMeasuredCapacity`'s `WHERE
tesla_id = $1 AND effective_capacity_kwh IS NOT NULL ORDER BY effective_period DESC LIMIT 1` is
served completely by the `UNIQUE (tesla_id, effective_period)` btree — equality on the leading
column, backwards range scan on the second, `IS NOT NULL` applied as a cheap filter over a
per-vehicle result set that is at most a few dozen rows (one row per month the vehicle has
existed). No new index.

### The two new batch-read patterns (Context fact 11)

| # | Query | Predicates | Existing index that could serve it |
|---|---|---|---|
| 1 | `ListValidManualEntryCapacitiesForPeriod` | `energy_source = 'USER'`, `inferred_capacity_kwh_calc IS NOT NULL`, `charged_on BETWEEN` | none — both existing indexes lead with `account_id`, which this query does not filter by (RD5: no account scoping) |
| 2 | `ListValidSessionCapacitiesForPeriod` | `status = 'DONE'`, `tesla_id IS NOT NULL`, `inferred_capacity_kwh_calc IS NOT NULL`, `charge_stop_date_time BETWEEN` | none, for the same reason |

**Decision: no new index for either query.** Three reasons, together:

1. **Frequency.** `MonthlyCapacityCalculator.Calculate` runs once a month per the tier-2 trigger
   (RD6/RD7), plus occasional manual re-runs (RD9). This is the textbook case the
   `Performance-Profile` already names: "writes are mostly done by pollers at midnight... denorm
   alizing, indexing aggressively, and precomputing for reads is acceptable [for the *read* side]
   — never at the cost of module boundaries." The inverse also holds: a batch job that runs at
   most a few times a month has no read-latency budget to protect, unlike a dashboard request.
2. **Bound.** Each call scans at most one calendar month of rows in each table — not the whole
   history. Both tables are append-mostly and not yet large enough (nightly writes, one row per
   session/entry) for a sequential scan over ~30 days of rows to be a real cost, and this is
   exactly the profile `ai/architecture.md` §7 calls "write path... has latitude to be slower (the
   user isn't waiting)" — nobody is waiting on this job synchronously.
3. **Precedent for declining a premature index.** `RM51`'s design.md declined an index on
   `price_source` for an identical reason shape: "an index would additionally serve the planner
   poorly on its own terms" for a low-selectivity or rarely-filtered column, and the read-heavy
   profile "licenses aggressive indexing for reads that exist — it does not license indexing a
   column no query mentions [outside its own narrow batch use]." The same reasoning applies here:
   building an `account_id`-free index ahead of a once-a-month reader is the same aggressive-write,
   marginal-read-benefit trade this project has already rejected once this week.

**Revisit trigger, stated so the next agent does not re-derive it.** If either table's per-month
row count grows large enough that this batch job's runtime becomes a real operational concern
(measured, not guessed), the right shape is a **partial** index scoped to exactly this job's
predicate: `ON manual_charge_entries (charged_on) WHERE energy_source = 'USER' AND
inferred_capacity_kwh_calc IS NOT NULL`, and the session-table equivalent on
`charge_stop_date_time`. Do not add a full, unconditional index on either date column — nothing
else filters by it without the same `WHERE`.

---

## Decisions

### D1 — Two counts: `candidate_count` before the delta gate, `sample_count` after it

**Confirmed by the owner at the database design gate on 2026-09-10 (roadmap RD11 / RD15).** This
started as a design.md interpretation of RD3+RD4 and is now a settled decision. Do not re-open it.

RD3's pipeline is: filter to valid rows (RD2, done in SQL) → filter by
`batteryDelta >= minDeltaPct` → check the *surviving* count against `minSamples`. The row stores a
count from **both** sides of that gate.

| Column | Counted | Equals |
|---|---|---|
| `candidate_count` | after RD2, **before** the delta gate | `len(rows)` the job passed to the estimator |
| `sample_count` | **after** the delta gate | `len(gated)`, the slice the median was computed over |

`sample_count` keeps the post-gate meaning: a row the gate drops was never usable evidence, so it
could not have contributed to the median even if `minSamples` were met. The number therefore always
describes the value stored beside it.

`candidate_count` exists because `sample_count` alone cannot explain a `NULL` month. Two valid,
RD2-passing rows both with a delta of `5` points produce `sample_count = 0` — correct, but
indistinguishable from a month whose rows were all unusable for some other reason.

**The three readable states:**

| What is stored | What it means |
|---|---|
| no row at all | no RD2-valid charge records that month (D2 — the job never invents a row) |
| `candidate_count 20`, `sample_count 0`, capacity `NULL` | charged 20 times, every one below the delta gate |
| `candidate_count 14`, `sample_count 11`, capacity set | 11 of 14 passed the gate; the median used those 11 |

A stored row always has `candidate_count >= 1`, which follows from D2. `candidate_count = 0` can
never be written.

**No change to the estimator's signature.** RD2 filtering happens in SQL, so the job already knows
`len(rows)` before it calls `estimateEffectiveCapacity`. It writes that as `candidate_count` and
the estimator's returned count as `sample_count`. The pure function stays a pure function over one
slice.

| Alternative | Why rejected |
|---|---|
| `sample_count` alone, post-gate (the original design) | Cannot tell "no measurable charges" from "20 charges, all too small". Diagnosing a `NULL` month then means querying `charge_sessions` and `manual_charge_entries` by hand. |
| `sample_count` = raw RD2-valid count, before the delta gate | The number would stop describing the value next to it: a month showing `14` might have had its median built from only `11` rows. |
| One column plus a flag | A flag says less than a count and costs the same. |

### D2 — A `tesla_id` with zero valid rows in a period gets no row at all — not even a thin one

**Not a roadmap decision — a boundary consequence.** `charging` has no registry of which vehicles
exist independent of its own two tables (it does not import `internal/account`,
`ai/architecture.md` §2). A vehicle with zero valid input rows for a period is therefore
indistinguishable, from this module's point of view, from a vehicle that does not exist. The job
only ever considers `tesla_id` values it actually observed in `ListValidManualEntryCapacitiesFor
Period` or `ListValidSessionCapacitiesForPeriod`'s results — grouping happens over the union of
those two result sets, never over an external vehicle list.

| Alternative | Why rejected |
|---|---|
| Write a `sample_count = 0` row for every registered vehicle | Requires `internal/account`'s vehicle list — an import this module must not have (`ai/architecture.md` §2). |

### D3 — `packCapacityLookup`: one seam, two callers, neither joins the other's testing shape

*Implements roadmap RD11's caller table, respecting Context fact 9.*

```go
// packCapacityLookup is the narrow read seam packCapacityKWh needs: the newest
// measured capacity for one vehicle, or nil when none exists yet. Both
// service.go's store interface (via dbStore.latestMeasuredCapacity) and
// session_verifier.go's *sessionVerifier (via its own latestMeasuredCapacity
// method) satisfy this structurally -- Go interfaces need no "implements"
// declaration -- so packCapacityKWh stays callable from both existing call
// sites without joining SessionVerifier to store's fake-testability seam, and
// without giving store a database-shaped dependency it does not otherwise have
// (design.md Context fact 9).
type packCapacityLookup interface {
    latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error)
}
```

`capacity.go` declares this interface and `packCapacityKWh` itself, but imports **no** `chargingdb`
and **no** `pgtype` — both conversions happen inside whichever concrete type implements
`latestMeasuredCapacity` (`dbStore` in `service.go`, or `*sessionVerifier` in
`session_verifier.go`), both already on the module's `pgtype`/`chargingdb` approved-file lists.
This is a deliberate, narrow interface — one method, shaped to exactly what `packCapacityKWh`
needs — not a widening of `store` or a new shared `db` seam, matching the "closed, small
vocabularies" and "don't over-abstract" principles `CLAUDE.md` §Non-negotiables states.

| Alternative | Why rejected |
|---|---|
| Add `latestMeasuredCapacity` to `store` and make `SessionVerifier` use `store` too | Rejected: `SessionVerifier`'s own existing doc comment states it is deliberately not part of `store` because it needs no fake — folding it in would change an established, documented boundary for no benefit (Context fact 9). |
| Pass `*chargingdb.Queries` directly into `packCapacityKWh` | Rejected: `store`'s whole reason to exist is offline-fakeable testing of `resolveEnergy`'s caller (`Create`/`Update`); a concrete `*chargingdb.Queries` parameter would remove that property the moment `packCapacityKWh` needed it. |
| A package-level function var (`packCapacityKWhImpl func(...) ...`) for test injection | Rejected: this module already has an established interface-seam pattern (`store`); a function-variable seam would be a second, inconsistent way to fake the same kind of dependency. |

### D4 — `packCapacityKWh`'s new body, and the `defaultPackCapacityKWh` constant

*Implements roadmap RD11 verbatim, replacing the `TODO(MAG-18)` body (closes backlog #18, roadmap
RD1).*

```go
// defaultPackCapacityKWh is the fallback used whenever no vehicle-specific
// measurement exists yet -- packCapacityKWh's own "no row" branch, and
// VerifySession's "tesla_id is nil" branch (RD11), both read this same named
// constant rather than repeating the literal 62.0 in two places.
const defaultPackCapacityKWh = 62.0

// packCapacityKWh returns the usable pack capacity in kWh for the vehicle
// identified by teslaID (RD11 -- this seam took a vin string before this
// change; it takes the vehicle's stable tesla_id now, matching the key
// monthly_effective_capacity is keyed on, RD5). It is the seam resolveEnergy's
// energy derivation (service.go) divides by, and the seam
// derivedStartBatteryPct's caller (session_verifier.go's VerifySession) also
// divides by, when deriving a start percentage from an end percentage and
// energy. Two call directions, one seam: never duplicated.
//
// Reads charging's own monthly_effective_capacity table through lookup,
// returning the newest measured value for teslaID. Returns
// defaultPackCapacityKWh when no measured row exists yet for this vehicle --
// which is every vehicle, until RM52's job (monthly_capacity.go) has actually
// run at least once and found enough evidence (roadmap RD4). Behaviour is
// therefore UNCHANGED until the first month is computed.
//
// Unexported: it is an implementation detail of its two callers' own capacity
// derivation, and nothing outside this module may divide by a pack capacity
// behind the module's back.
func packCapacityKWh(ctx context.Context, lookup packCapacityLookup, teslaID int64) (float64, error) {
    measured, err := lookup.latestMeasuredCapacity(ctx, teslaID)
    if err != nil {
        return 0, fmt.Errorf("charging: resolving pack capacity for tesla_id %d: %w", teslaID, err)
    }
    if measured == nil {
        return defaultPackCapacityKWh, nil
    }
    return *measured, nil
}
```

`derivedEnergyKWh` and `derivedStartBatteryPct` in `capacity.go` are **untouched** — both already
take `capacityKWh float64` as a plain parameter, so nothing about their own signature or body
changes. Only `packCapacityKWh`'s signature and body change, and only in this file.

### D5 — The two callers, exactly as roadmap RD11's table specifies

*Implements roadmap RD11's caller table verbatim.*

**`service.go`, `resolveEnergy`** — gains a `store` parameter (Context fact 9: `store` is already
the seam `Create`/`Update` hold), passes `e.TeslaID` (never nil, Context fact 5):

```go
// resolveEnergy applies the design.md D3 derivation rule for Create/Update...
// (existing comment, amended:) s is the same store the caller (writerService)
// already holds -- packCapacityKWh reads through it via s.latestMeasuredCapacity,
// structurally satisfying packCapacityLookup (design.md D3).
func resolveEnergy(ctx context.Context, s store, e Entry) (*float64, EnergySource, error) {
    if e.EnergyAddedKWh == nil {
        capacity, err := packCapacityKWh(ctx, s, e.TeslaID) // RD11: was e.VIN
        if err != nil {
            return nil, "", fmt.Errorf("charging: resolving pack capacity: %w", err)
        }
        if derived := derivedEnergyKWh(capacity, e.StartBatteryPct, e.EndBatteryPct); derived != nil {
            return derived, EnergySourceEstimated, nil
        }
        return nil, EnergySourceUser, nil
    }
    return e.EnergyAddedKWh, EnergySourceUser, nil
}
```

Both call sites (`writerService.Create`, `writerService.Update`) change from `resolveEnergy(ctx,
e)` to `resolveEnergy(ctx, w.store, e)` — no other line in either method changes.

**`service.go`'s `store` interface and `dbStore`** gain one method each:

```go
type store interface {
    // ...existing methods, unchanged...

    // latestMeasuredCapacity reads the newest non-NULL effective_capacity_kwh
    // for teslaID from monthly_effective_capacity, or nil when none exists yet.
    // Satisfies packCapacityLookup structurally (design.md D3).
    latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error)
}
```

```go
func (d *dbStore) latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error) {
    v, err := d.q.LatestMeasuredCapacity(ctx, teslaID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, nil
        }
        return nil, fmt.Errorf("charging: reading latest measured capacity for tesla_id %d: %w", teslaID, err)
    }
    return pgFloat8ToFloat64Ptr(v), nil
}
```

This is the first method on `store`/`dbStore` to need `errors.Is(err, pgx.ErrNoRows)` — `service.go`
gains two new imports, `"errors"` and `"github.com/jackc/pgx/v5"`, alongside its existing ones.

**`session_verifier.go`, `VerifySession`** — reads the locked row's now-selected `tesla_id`;
`nil` short-circuits to `defaultPackCapacityKWh` **without calling `packCapacityKWh` at all**
(RD11: "nullable — nil goes straight to the `62.0` fallback" — read literally: the fallback is
reached directly, not through the seam):

```go
        row, err := qtx.LockSessionForVerification(ctx, chargingdb.LockSessionForVerificationParams{
            ID:        id,
            AccountID: accountID,
        })
        if err != nil {
            return Session{}, fmt.Errorf("charging: verify session: %w", err)
        }

        var capacityKWh float64
        if teslaID := pgInt8ToInt64Ptr(row.TeslaID); teslaID != nil {
            capacityKWh, err = packCapacityKWh(ctx, v, *teslaID)
            if err != nil {
                return Session{}, fmt.Errorf("charging: resolving pack capacity: %w", err)
            }
        } else {
            capacityKWh = defaultPackCapacityKWh
        }

        startToStore = derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh), endBatteryPct)
        calculated = startToStore != nil
```

`*sessionVerifier` gains one method, mirroring `dbStore`'s shape but talking to `v.q` directly (it
is deliberately outside `store`, Context fact 9):

```go
// latestMeasuredCapacity satisfies packCapacityLookup (design.md D3) using this
// port's own *chargingdb.Queries -- mirrors dbStore's identical method in
// service.go; not shared, because sessionVerifier is deliberately outside the
// store interface (this file's own existing doc comment).
func (v *sessionVerifier) latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error) {
    val, err := v.q.LatestMeasuredCapacity(ctx, teslaID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, nil
        }
        return nil, fmt.Errorf("charging: reading latest measured capacity for tesla_id %d: %w", teslaID, err)
    }
    return pgFloat8ToFloat64Ptr(val), nil
}
```

`session_verifier.go` already imports `"github.com/jackc/pgx/v5"`; it gains one new import,
`"errors"`.

| Alternative | Why rejected |
|---|---|
| Have `VerifySession` call `packCapacityKWh` unconditionally, letting the seam's own `nil` handling apply to a nil `teslaID` too | Would require `packCapacityKWh` to accept `*int64` instead of `int64`, contradicting RD11's explicit signature `packCapacityKWh(ctx, teslaID int64)`. RD11's own caller table already resolves this by putting the nil-check at the call site, not inside the seam. |

### D6 — Small, per-file duplication of the `pgx.ErrNoRows` translation, not a shared helper

**Not a roadmap decision — a direct consequence of D3.** `dbStore.latestMeasuredCapacity` and
`*sessionVerifier.latestMeasuredCapacity` both translate `pgx.ErrNoRows` to `(nil, nil)` with
near-identical bodies. This mirrors an already-established pattern in this module:
`MirrorWatermarkStore`, `SessionWriter`, `SessionReader`, and `SessionVerifier` are each "a small
concrete type talking directly to `chargingdb.Queries`" (their own doc comments), each owning its
full DB-access implementation locally rather than sharing a central store. Two four-line method
bodies are cheaper, in review cost and in coupling, than a new shared abstraction two callers use
once each.

### D7 — The job's file, its report type, and why `teslaID` filtering happens in Go, not SQL

*Implements Tier 1's `MonthlyCapacityCalculator` interface verbatim, plus RD5's grouping.*

New file `internal/charging/monthly_capacity.go` holds the pure estimator, the two Go constants,
and the job. It is added to both of this module's Allowed-Imports lists (`chargingdb`, `pgtype`) —
see §"AGENTS.md updates" below.

```go
// MonthlyCapacityReport summarizes one Calculate call: how many distinct
// vehicles it considered, how many got a measured (non-NULL) capacity, and how
// many were left thin (a row was still written, per RD4, but
// effective_capacity_kwh is NULL). cmd/monthly-capacity (tier 3) prints this;
// nothing in this tier consumes it yet.
type MonthlyCapacityReport struct {
    Period        time.Time
    VehiclesFound int
    Measured      int
    Thin          int
}

// MonthlyCapacityCalculator computes and stores the effective pack capacity for
// one or every vehicle, for one calendar month (roadmap Tier 1, RD1/RD3/RD5).
// period must be the first instant of the month to compute; this module does
// not compute "now" or "the previous month" itself and imports no clock -- the
// tier-2 caller (internal/app, via internal/clock, RD7) decides both and passes
// the result in. teslaID nil means every vehicle with at least one valid row
// this period (RD8); non-nil scopes the run to one vehicle.
type MonthlyCapacityCalculator interface {
    Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyCapacityReport, error)
}

// NewMonthlyCapacityCalculator -- the only publicly exported factory function
// for this port.
func NewMonthlyCapacityCalculator(pool *pgxpool.Pool) MonthlyCapacityCalculator
```

(`MonthlyCapacityReport` and the `MonthlyCapacityCalculator` interface + constructor declaration
live in `charging.go`, matching every other port's existing placement; the concrete type and
`Calculate`'s body live in `monthly_capacity.go`.)

```go
// capacitySample is one valid input row for the monthly effective-capacity
// estimate: the capacity charging's own inferred_capacity_kwh_calc already
// computed for one record (RD1/RD2), and the battery-percentage delta that
// computation divided by. Package-private -- it never leaves the job that
// builds it.
type capacitySample struct {
    CapacityKWh     float64
    BatteryDeltaPct int
}

// minSamples and minDeltaPct are Go constants, never database values (roadmap
// RD3): tuning them is a code change plus a re-run, never a migration.
const (
    minSamples  = 3
    minDeltaPct = 15
)

// estimateEffectiveCapacity applies RD3's gate-then-median rule to one
// vehicle's valid samples for one month: drop samples whose battery delta is
// smaller than minDeltaPct (a small delta amplifies percentage error in the
// implied capacity -- roadmap RD3's own worked example), then require at least
// minSamples surviving samples before reporting a capacity at all (RD4).
// sampleCount is always the count AFTER the delta gate (design.md D1) -- a
// sample the gate drops was never valid evidence, so it does not count as
// "found" either.
//
// No ctx, no I/O -- pure over its input slice, unit-testable with no container
// (roadmap Tier 1 scope statement).
func estimateEffectiveCapacity(samples []capacitySample) (capacityKWh *float64, sampleCount int) {
    gated := make([]float64, 0, len(samples))
    for _, s := range samples {
        if s.BatteryDeltaPct >= minDeltaPct {
            gated = append(gated, s.CapacityKWh)
        }
    }
    sampleCount = len(gated)
    if sampleCount < minSamples {
        return nil, sampleCount
    }
    sort.Float64s(gated)
    capacity := median(gated)
    return &capacity, sampleCount
}

// median returns the median of a non-empty, ALREADY SORTED slice: the middle
// value on an odd count, the average of the two middle values on an even count
// (roadmap RD3).
func median(sorted []float64) float64 {
    n := len(sorted)
    mid := n / 2
    if n%2 == 1 {
        return sorted[mid]
    }
    return (sorted[mid-1] + sorted[mid]) / 2
}
```

```go
// monthlyCapacityCalculator is the concrete implementation of
// MonthlyCapacityCalculator. Like sessionVerifier and mirrorWatermarkStore, it
// is a small unexported struct talking directly to chargingdb.Queries, not
// part of service.go's store interface -- this job runs once a month, offline
// from any request path, and is exercised by DATABASE_URL-gated integration
// tests, not a fake (design.md Context fact 9's reasoning applies here too).
type monthlyCapacityCalculator struct {
    pool *pgxpool.Pool
    q    *chargingdb.Queries
}

func newMonthlyCapacityCalculator(pool *pgxpool.Pool) *monthlyCapacityCalculator {
    return &monthlyCapacityCalculator{pool: pool, q: chargingdb.New(pool)}
}

var _ MonthlyCapacityCalculator = (*monthlyCapacityCalculator)(nil)

// Calculate implements MonthlyCapacityCalculator. See the interface doc comment
// (charging.go) for the full contract.
//
// teslaID non-nil scopes the run to one vehicle by filtering byVehicle in Go
// AFTER both queries run for the whole period, rather than adding a second,
// nearly-identical pair of SQL queries with a tesla_id predicate (design.md
// D7). This job is not on any hot read path (ai/architecture.md §7) and this
// repo has no existing precedent for a nullable-filter SQL idiom
// (sqlc.narg/sqlc.arg do not appear anywhere in this project) -- introducing
// one for a once-a-month debug/backfill path would be exactly the
// over-abstraction CLAUDE.md §Non-negotiables warns against.
func (c *monthlyCapacityCalculator) Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyCapacityReport, error) {
    periodEnd := period.AddDate(0, 1, 0)
    periodStartDate := dateFromTime(period)
    periodEndDate := dateFromTime(periodEnd)
    periodStartTS := pgtype.Timestamptz{Time: period, Valid: true}
    periodEndTS := pgtype.Timestamptz{Time: periodEnd, Valid: true}

    entryRows, err := c.q.ListValidManualEntryCapacitiesForPeriod(ctx, chargingdb.ListValidManualEntryCapacitiesForPeriodParams{
        PeriodStart: periodStartDate,
        PeriodEnd:   periodEndDate,
    })
    if err != nil {
        return MonthlyCapacityReport{}, fmt.Errorf("charging: listing valid manual entry capacities: %w", err)
    }
    sessionRows, err := c.q.ListValidSessionCapacitiesForPeriod(ctx, chargingdb.ListValidSessionCapacitiesForPeriodParams{
        PeriodStart: periodStartTS,
        PeriodEnd:   periodEndTS,
    })
    if err != nil {
        return MonthlyCapacityReport{}, fmt.Errorf("charging: listing valid session capacities: %w", err)
    }

    byVehicle := map[int64][]capacitySample{}
    for _, r := range entryRows {
        capacity := pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc)
        start := pgInt2ToIntPtr(r.StartBatteryPct)
        end := pgInt2ToIntPtr(r.EndBatteryPct)
        if capacity == nil || start == nil || end == nil {
            continue // defensive; the query's own WHERE already guarantees this
        }
        byVehicle[r.TeslaID] = append(byVehicle[r.TeslaID], capacitySample{CapacityKWh: *capacity, BatteryDeltaPct: *end - *start})
    }
    for _, r := range sessionRows {
        tid := pgInt8ToInt64Ptr(r.TeslaID)
        capacity := pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc)
        start := pgInt2ToIntPtr(r.StartBatteryPct)
        end := pgInt2ToIntPtr(r.EndBatteryPct)
        if tid == nil || capacity == nil || start == nil || end == nil {
            continue // defensive; the query's own WHERE already guarantees this (RD5)
        }
        byVehicle[*tid] = append(byVehicle[*tid], capacitySample{CapacityKWh: *capacity, BatteryDeltaPct: *end - *start})
    }

    report := MonthlyCapacityReport{Period: period}
    for tid, samples := range byVehicle {
        if teslaID != nil && tid != *teslaID {
            continue
        }
        report.VehiclesFound++
        capacityKWh, sampleCount := estimateEffectiveCapacity(samples)

        var capacityParam pgtype.Float8
        if capacityKWh != nil {
            capacityParam = pgtype.Float8{Float64: *capacityKWh, Valid: true}
            report.Measured++
        } else {
            report.Thin++
        }

        if err := c.q.UpsertMonthlyEffectiveCapacity(ctx, chargingdb.UpsertMonthlyEffectiveCapacityParams{
            TeslaID:              tid,
            EffectivePeriod:      periodStartDate,
            EffectiveCapacityKwh: capacityParam,
            SampleCount:          int32(sampleCount),
        }); err != nil {
            return report, fmt.Errorf("charging: upserting monthly effective capacity for tesla_id %d: %w", tid, err)
        }
    }
    return report, nil
}
```

| Alternative | Why rejected |
|---|---|
| A second pair of SQL queries, filtered by `tesla_id = @tesla_id` when scoping to one vehicle | Rejected: doubles the SQL surface for a debug/backfill path this job is not on the hot read path for (§Index Plan), and this repo has no precedent for the nullable-filter idiom that would avoid the duplication. |
| Aggregate (median) in SQL via `percentile_cont` | Rejected: the dispatch and roadmap both require the estimator to be a **pure Go function over a slice**, unit-testable with no container — moving the math into SQL would remove that property entirely. |

### D8 — `packCapacityKWh` never recomputes a historical row; this change writes no `UPDATE` to
either existing table

**Not a roadmap decision — restated here because the Database Changes section only shows what this
migration and these queries touch, and it is worth being explicit about what they do not.**
`ListValidManualEntryCapacitiesForPeriod` and `ListValidSessionCapacitiesForPeriod` are pure
`SELECT`s. `UpsertMonthlyEffectiveCapacity` writes only to the new table. No statement in this
change names `manual_charge_entries` or `supercharger_sessions` on the write side. Every
`ESTIMATED` entry and every `DONE_CALCULATED` session keeps its stored, `62.0`-derived value
forever — recomputing them was explicitly rejected at the roadmap level ("Future work": "would
rewrite stored history and needs its own ticket").

### Roadmap-decision mapping

| design.md | roadmap | Subject |
|---|---|---|
| Database Changes | **RD10, RD11** | table name, column shapes, both seam queries, verbatim |
| Index Plan | **RD11** | the new table's index plan (verbatim) plus the two batch queries' own (new) justification |
| D1 | RD11, RD15 | Two counts: `candidate_count` pre-gate, `sample_count` post-gate. Owner-confirmed at the design gate 2026-09-10 |
| D2 | — | a `tesla_id` with zero valid rows gets no row (forced by the module-boundary rule, `ai/architecture.md` §2) |
| D3 | **RD1, RD11** | `packCapacityLookup` seam shape, respecting `store`'s existing fake-testability boundary |
| D4 | **RD11** | `packCapacityKWh`'s new signature and body; closes backlog #18 (RD1) |
| D5 | **RD11** | both updated callers, verbatim per the roadmap's own caller table |
| D6 | — | accepted small duplication over a new shared abstraction (forced by D3) |
| D7 | Tier 1 scope statement, **RD5, RD8** | the job's file, its report type, in-Go vehicle filtering |
| D8 | roadmap "Future work" | no historical row is ever recomputed by this change |

Roadmap **RD6, RD7 (the trigger), RD8, RD9** are tier 2/tier 3 and are not implemented here, except
RD7's constraint that this module never computes "now" or "the previous month" itself (reflected in
D7's `Calculate` doc comment) and RD10's naming table (reflected throughout).

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing ("author
their expected values up front, in the change's `design.md`, before the implementation exists").
**Tests written later must assert THIS contract**, not whatever the implementation happens to
produce.

**Conventions**, following this module's existing ones (`internal/charging/AGENTS.md` §Testing
Notes): fresh `uuid.New()` account ids per test; assert only against `charging` domain types and
direct SQL column values — **never `pgtype`**, in any file; assert `nil`/non-`nil` explicitly for
every optional field. Integration tests are `DATABASE_URL`-gated through the package's existing
`testdb_test.go`.

### Group A — offline unit tests (no DB)

New file: `monthly_capacity_estimator_test.go` (package `charging`, since `estimateEffectiveCapacity`,
`median`, `packCapacityKWh`, and `packCapacityLookup` are all unexported).

**A1–A5 — `estimateEffectiveCapacity`.**

| ID | Input samples (`CapacityKWh`, `BatteryDeltaPct`) | Expected `(capacityKWh, sampleCount)` | What it proves |
|---|---|---|---|
| **A1** | Two samples, deltas `20`, `25` | `(nil, 2)` | Exactly 2 valid (gated) rows: `NULL` capacity, `sample_count = 2` (RD4) — below `minSamples = 3`. |
| **A2** | Three samples, deltas all `≥15`, capacities `60.0`, `62.0`, `64.0` | `(ptr(62.0), 3)` | Odd count at exactly `minSamples`: the middle value, boundary inclusive (RD3/RD4). |
| **A3** | Four samples, deltas all `≥15`, capacities `58.0`, `60.0`, `64.0`, `70.0` | `(ptr(62.0), 4)` | Even count: average of the two middle values (`(60.0+64.0)/2`), matching RD3's stated rule. |
| **A4** | Four samples: three with delta `≥15` (capacities `60.0`, `62.0`, `64.0`), one with delta `10` (capacity `120.0`, an outlier that would skew the result if counted) | `(ptr(62.0), 3)` | **A small-delta row is dropped by the gate** (RD3) — it is excluded from both the median and `sample_count` (design.md D1), and its outlier value never reaches the median. |
| **A5** | Two samples with delta `≥15`, plus one with delta `14` (just under the gate) | `(nil, 2)` | The gate's boundary is `>= minDeltaPct`, not `>`; `14` is dropped, leaving 2 — below `minSamples`. |

**A6–A8 — `packCapacityKWh`**, using an in-package fake `packCapacityLookup`.

| ID | Fake `latestMeasuredCapacity` returns | Expected | What it proves |
|---|---|---|---|
| **A6** | `(nil, nil)` — no measured row | `(defaultPackCapacityKWh, nil)` | No measured row ⇒ returns `62.0` (RD4). |
| **A7** | `(ptr(70.5), nil)` | `(70.5, nil)` | A measured value is returned as-is, no rounding, no recomputation. |
| **A8** | `(nil, errors.New("boom"))` | `(0, error)` wrapping the fake's error | A lookup failure is a real error, never silently coerced to `defaultPackCapacityKWh`. |

### Group B — schema and constraints (integration, direct SQL)

`db_monthly_capacity_integration_test.go` (new). Direct `INSERT`s, asserting the database's own
behaviour, not the port's.

| ID | Statement | Expected | What it proves |
|---|---|---|---|
| **B1** | `INSERT INTO charging.monthly_effective_capacity (tesla_id, effective_period, sample_count) VALUES ($1, '2026-08-15', 0)` | error, SQLSTATE **`23514`** | `monthly_effective_capacity_period_is_month_start` rejects a non-1st-of-month date. |
| **B2** | Two `INSERT`s (no `ON CONFLICT`) with the same `(tesla_id, effective_period)` | second errors, SQLSTATE **`23505`** | The `UNIQUE (tesla_id, effective_period)` constraint exists and is the upsert's conflict target. |

### Group C — the job and both updated seams (integration, through the real ports)

Same file. `q := chargingdb.New(pool)` for direct seeding where a public writer does not cover the
fixture shape (mirrors `internal/charging/AGENTS.md` §Testing Notes' existing "seed via direct
`INSERT`" convention for another module's tables — here it is this module's own tables, seeded
below the public port to set up percentages/status/energy_source combinations the port's own
validation would otherwise reject or normalize).

| ID | Setup | Action | Expected | What it proves |
|---|---|---|---|---|
| **C1** | One `manual_charge_entries` row, `energy_source = 'ESTIMATED'`, valid percentages (delta `≥15`) | `Calculate(ctx, period, nil)` | No `monthly_effective_capacity` row exists for that `tesla_id` afterward | **`ESTIMATED` is excluded** (RD2) — its energy was itself derived from `62.0`, so counting it would feed the constant back into itself. |
| **C2** | One `supercharger_sessions` row, `status = 'DONE_CALCULATED'`, valid percentages | `Calculate(ctx, period, nil)` | No row exists for that `tesla_id` afterward | **`DONE_CALCULATED` is excluded** (RD2) — its `start_battery_pct` was itself derived via `derivedStartBatteryPct` dividing by `62.0`, so its `inferred_capacity_kwh_calc` is exactly `62.0` by construction, not a real measurement. |
| **C3** | One `supercharger_sessions` row, `status = 'IN_PROGRESS'` | `Calculate(ctx, period, nil)` | No row exists for that `tesla_id` afterward | **`IN_PROGRESS` is excluded** — it has no complete percentage pair, so `inferred_capacity_kwh_calc` is already `NULL` and the query's own `WHERE` drops it. |
| **C4** | Two `USER` manual entries, same `tesla_id`, deltas `≥15`, distinct capacities | `Calculate(ctx, period, nil)` | Row exists: `effective_capacity_kwh IS NULL`, `candidate_count = 2`, `sample_count = 2` | **Exactly 2 valid rows ⇒ `NULL` capacity, `sample_count = 2`** (RD4), through the real job, not just the pure function (A1). |
| **C5** | Four `USER` manual entries, same `tesla_id`, deltas `≥15`, capacities `58.0, 60.0, 64.0, 70.0` | `Calculate(ctx, period, nil)` | Row exists: `effective_capacity_kwh = 62.0`, `candidate_count = 4`, `sample_count = 4` | **Even valid-sample count ⇒ average of the two middle values** (RD3), end-to-end. |
| **C6** | Two `USER` manual entries under account A, one `DONE` session under account B, all three same `tesla_id`, deltas `≥15` | `Calculate(ctx, period, nil)` | Exactly one row for that `tesla_id`; `candidate_count = 3`, `sample_count = 3` | **Two accounts with the same `tesla_id` are pooled into one row** (RD5) — no `account_id` on the table, `GROUP BY tesla_id` (in Go) pools automatically. |
| **C7** | One `supercharger_sessions` row, `status = 'DONE'`, `tesla_id = NULL`, valid percentages | `Calculate(ctx, period, nil)` | No row is written attributing capacity to any vehicle from this session | **A session row with `tesla_id IS NULL` is skipped** (RD5) — a capacity cannot be attributed to a car nobody has registered. |
| **C8** | No `monthly_effective_capacity` row exists for a `tesla_id` | Call `packCapacityKWh` through the real `dbStore`/`sessionVerifier` seam (e.g. via `resolveEnergy` with `EnergyAddedKWh: nil` and a valid percentage delta) | Derived energy matches what `defaultPackCapacityKWh` (`62.0`) would produce | **`packCapacityKWh` with no measured row ⇒ returns `62.0`**, through the real seam, not the fake (A6). |
| **C9** | Current-month row exists with `effective_capacity_kwh IS NULL` (a thin month); an earlier month's row exists with `effective_capacity_kwh = 70.0` | Call `packCapacityKWh` (real seam) for that `tesla_id` | Returns `70.0`, not `62.0` and not an error | **`packCapacityKWh` with a `NULL` current month and a measured earlier month ⇒ returns the earlier one** (RD4) — `LatestMeasuredCapacity`'s own `WHERE ... IS NOT NULL` skips the thin row unconditionally. |
| **C10** | No `monthly_effective_capacity` row exists anywhere | `Writer.Create` with `EnergyAddedKWh: nil` and a valid percentage delta, **and** `SessionVerifier.VerifySession` deriving a start percentage on a session with a non-nil `tesla_id` | Both derive using `62.0`, matching pre-change behaviour bit-for-bit | **Behaviour is unchanged until the first month is computed** (proposal.md §Breaking) — both callers, in one test. |
| **C11** | A `supercharger_sessions` row with `tesla_id = NULL`, needing a derived start percentage | `SessionVerifier.VerifySession` | Derivation succeeds using `defaultPackCapacityKWh` directly; **no call reaches `LatestMeasuredCapacity`** for this row (asserted by seeding no measured row and confirming no error path was taken, or by a call-count fake substituted only for this one case) | **RD11's caller-side nil short-circuit**: a session whose `tesla_id` is `NULL` never reaches `packCapacityKWh`'s DB read at all. |
| **C12** | `Calculate(ctx, period, nil)` run once (state as in C4), then a fifth valid `USER` entry added for the same vehicle and period, then `Calculate` run again | The row updates in place (`candidate_count` and `sample_count` both become `5`); no second row is created | **The upsert is idempotent** (RD9's re-run path) — `ON CONFLICT (tesla_id, effective_period) DO UPDATE` is exercised, not just declared. |
| **C13** | Five `USER` manual entries, same `tesla_id`, every delta `5` points (all below `minDeltaPct = 15`) | `Calculate(ctx, period, nil)` | Row exists: `effective_capacity_kwh IS NULL`, `candidate_count = 5`, `sample_count = 0` | **The case `candidate_count` exists for** (D1, roadmap RD15). Five valid records were found and none survived the gate. Without `candidate_count` this row is indistinguishable from a month with nothing measurable. |

---

## Risks

1. **RESOLVED at the design gate, 2026-09-10 — `sample_count`'s post-gate definition.** This was
   raised here as an open interpretation of RD3+RD4. The owner confirmed the post-gate meaning and
   added a second column, `candidate_count`, for the pre-gate number (roadmap RD15, design.md D1).
   The gate caught it before Wave 1 started, which is what the flag was for. No longer a risk.
2. **No index for the two new batch queries (§Index Plan) trades batch-job latency for zero extra
   write cost.** Acceptable today per the stated reasoning; the revisit trigger is recorded so a
   future agent facing a slow monthly run does not have to re-derive when an index becomes
   justified.
3. **A `tesla_id` that later becomes a real, registered vehicle but had zero valid rows in an
   already-computed month never gets a backfilled row for that month** (D2's consequence) unless a
   human re-runs `Calculate` for that period (RD9, tier 3). This mirrors `mirror_watermarks`'
   already-accepted "no row yet means epoch" shape and is not a new category of risk for this
   module.
