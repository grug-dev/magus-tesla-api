# Design — RM33-charging-add-entry-status

> **Design gate: `database`.** This change adds three columns to `manual_charge_entries` and
> relaxes a `NOT NULL` on a fourth. Per `CLAUDE.md` §Pipeline config → `Design-Gates: database`
> and `openspec/config.yaml` §design, **the owner must confirm this document before any
> implementation task is dispatched.** It is written to be reviewable on its own: the complete
> DDL, the rationale with rejected alternatives, the index plan justified against the declared
> read patterns, and the Test Contract are all here.

Source ticket: MAG-18 · Roadmap: `openspec/roadmaps/RM33-manual-record-status.md`, tier 1 of 3.
Roadmap decisions **D1–D8** are binding and were confirmed with the user at the 2026-08-29
interview. This document does not re-open them; where it says something the roadmap does not, it
says so explicitly (**D8**, **D10**, **D11** below).

---

## Context

Facts that constrain every decision here. Each was read out of the repository, not recalled.

1. **`manual_charge_entries` is owned exclusively by `internal/charging`** — no cross-module FK, no
   cross-module read (`ai/architecture.md` §2, the table's own migration header). Its schema
   source of truth is the migration directory; there is no `schema.sql`.
2. **`energy_added_kwh` is `NUMERIC(6,2) NOT NULL CHECK (energy_added_kwh > 0)`**
   (`20260718000001`, line 28). `price` is `NUMERIC(14,2) NOT NULL CHECK (price >= 0)`;
   `currency` is `TEXT NOT NULL DEFAULT 'COP'`.
3. **`inferred_capacity_kwh_calc` is a `GENERATED ALWAYS AS (…) STORED` column** on this table
   (`20260829000001`), whose expression divides `energy_added_kwh` by
   `((end_battery_pct - start_battery_pct)::NUMERIC / 100)` under a
   `start IS NOT NULL AND end IS NOT NULL AND end > start` guard. Its guard **does not test
   energy** — deliberately, because `energy_added_kwh` was `NOT NULL` when it was written, and a
   dead check there would falsely imply nullability (that migration's own header, "the two
   expressions differ in exactly two ways").
4. **Two existing indexes**, both `account_id`-leading:
   `idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)` and
   `idx_manual_charge_entries_account_time (account_id, charged_on DESC)`.
5. **All four read queries are `SELECT *`**; both write queries name their columns explicitly
   (`db/query.sql`). Consequence, already proven by MAG-25: a new column reaches every read for
   free, and cannot be bound by a write unless the write query is edited.
6. **`location_kind` is the precedent for a required `TEXT` + `CHECK` column**: `NOT NULL` in the
   DB, surfaced as `*string` on the domain type, with the required-ness enforced in Go by an
   ad-hoc `if e.LocationKind == nil || *e.LocationKind == ""` in **both** `Create` and `Update`
   (`service.go:117`, `:166`). That duplicated ad-hoc check is exactly what **D5** replaces.
7. **`internal/gateway`'s form field names already are the column names** — `parseChargeForm`
   keys its error map `errs["end_battery_pct"]`, `errs["ended_at"]`, `errs["start_battery_pct"]`
   (`handlers/charges.go:770-790`). This is why `Field`'s string values are the column names
   (**D5**).
8. **`battery_pct_source` is the precedent for a provenance column that a caller may never
   supply**: `SessionVerifier.VerifySession` computes it in Go and the port takes no source
   parameter (RM31 D2/D7). **D4** applies that same shape to `energy_source`.
9. **`internal/gateway` reads `Entry.EnergyAddedKWh` at `handlers/charges.go:612, 623, 798`.**
   The `*float64` change breaks its compilation, and a `charging` worker may not fix it
   (**D11**).

## Goals / Non-Goals

**Goals**

- A charge record can exist as `IN_PROGRESS` with only what is known at plug-in time, and be
  completed to `DONE` later, with the required-field set a function of the status.
- Energy may be omitted; when the battery percentages allow it, it is derived on write.
- The provenance of every stored energy value is recorded, so backlog #18's capacity average can
  exclude derived rows.
- The odometer reading at the charge event is recordable.
- The pack capacity sits behind a seam that backlog #18 can replace with a one-body change.

**Non-Goals**

- Every gateway change (tiers 2 and 3). This change does not touch `internal/gateway`.
- Changing `inferred_capacity_kwh_calc`'s expression — roadmap **D4** rejected it as a
  table-rewriting migration altering MAG-25's behaviour out of scope.
- Any per-vehicle capacity aggregate or averaging (backlog #18).
- Any status *transition* rule. Nothing forbids `DONE` → `IN_PROGRESS`; see **D5**.
- Any index on the new columns; see §Index Plan.
- Any `price`/`currency` schema change — roadmap **D7** is a gateway-only coercion.

---

## Database Changes

### The migration

`internal/charging/db/migrations/20260829000002_add_entry_status.sql`

```sql
-- +goose Up
-- MAG-18 / RM33 tier 1: a manual charge entry gains a lifecycle status, an energy
-- provenance marker, and the odometer reading taken at the charge event -- and
-- energy_added_kwh stops being mandatory.
--
-- WHY EACH ADD COLUMN IS A SINGLE STATEMENT, unlike 20260720000001's two-step
-- backfill-then-constrain (roadmap D1). That migration needed two steps because its
-- backfill value ('OTHER') deliberately was NOT the column default -- a DEFAULT there
-- would have silently masked a missing field. Here the opposite is true: the chosen
-- backfill value IS the intended default in both cases ('IN_PROGRESS' for status,
-- 'USER' for energy_source), so ADD COLUMN ... NOT NULL DEFAULT ... assigns it to
-- every pre-existing row as part of the same statement. There is nothing left for a
-- separate UPDATE to do.
--
-- WHY 'IN_PROGRESS' AND NOT 'DONE' AS THE BACKFILL (roadmap D1). Every historical row
-- renders as unfinished until reviewed. The user chose this over the recommended
-- 'DONE' deliberately, as a prompt to re-check historical entries. It is not an
-- oversight; do not "correct" it.
--
-- WHY TEXT + CHECK AND NOT A POSTGRES ENUM (roadmap D1). Consistency with the
-- location_kind and charging_type columns this table already carries, and because
-- altering an enum's value set later is painful (ALTER TYPE ... ADD VALUE cannot run
-- inside a transaction block before PG12, and a value can never be removed).
--
-- COST: all three adds are METADATA-ONLY on PostgreSQL 11+. status and energy_source
-- carry a non-volatile constant DEFAULT, which since PG11 is stored in the catalogue
-- rather than written into every row; odometer_km is nullable with no default. The
-- DROP NOT NULL is a catalogue update. Each briefly takes ACCESS EXCLUSIVE. This
-- migration does NOT rewrite the table -- unlike 20260829000001, which did.

ALTER TABLE manual_charge_entries
    ADD COLUMN status TEXT NOT NULL DEFAULT 'IN_PROGRESS'
        CHECK (status IN ('IN_PROGRESS','DONE'));

ALTER TABLE manual_charge_entries
    ADD COLUMN energy_source TEXT NOT NULL DEFAULT 'USER'
        CHECK (energy_source IN ('USER','ESTIMATED'));

ALTER TABLE manual_charge_entries
    ADD COLUMN odometer_km INTEGER
        CHECK (odometer_km >= 0);

-- energy_added_kwh becomes optional (roadmap D2). FORCED, not chosen: an IN_PROGRESS
-- record legitimately has no end_battery_pct, so the derivation of D3 has no inputs
-- and no honest value exists. NULL is the honest answer; a fabricated 0 is not, and
-- would in any case violate the CHECK below.
--
-- THE EXISTING CHECK (energy_added_kwh > 0) IS DELIBERATELY KEPT. A CHECK constraint
-- evaluates to NULL -- not false -- on a NULL input, and Postgres accepts a row whose
-- CHECK is NULL. So the constraint continues to reject 0 and every negative value
-- exactly as before, while permitting the new NULL. Nothing about it needs changing,
-- and it must NOT be dropped and re-added.
--
-- THE GENERATED COLUMN inferred_capacity_kwh_calc IS NOT TOUCHED (roadmap D4). Its
-- expression continues to reference energy_added_kwh. Dropping a source column's
-- NOT NULL neither invalidates the generation expression nor triggers a rewrite:
-- Postgres re-checks a generation expression only when the expression itself or a
-- referenced column's TYPE changes, and every STORED value already written stays as
-- it is. What the column now yields for the new row shapes:
--   * energy NULL, percentages valid and increasing -> the CASE's WHEN is TRUE (it
--     tests only the two percentages -- see 20260829000001's header), so the THEN
--     branch evaluates NULL / <positive divisor>, and SQL arithmetic on NULL yields
--     NULL. The column is NULL. It is NOT an error and NOT a division by zero.
--   * energy DERIVED (energy_source = 'ESTIMATED') -> the column returns EXACTLY the
--     pack capacity constant, by algebra: (C * d/100) / (d/100) = C. This is the known,
--     accepted consequence roadmap D4 documents, and it is precisely why
--     energy_source exists: backlog #18's capacity average MUST filter
--     WHERE energy_source = 'USER', or it will average its own seed value back into
--     itself and stop converging on the pack's real capacity.
ALTER TABLE manual_charge_entries
    ALTER COLUMN energy_added_kwh DROP NOT NULL;

COMMENT ON COLUMN manual_charge_entries.status IS
    'Lifecycle state of this user-asserted charge record: IN_PROGRESS (logged at plug-in '
    'time, may lack the end-of-session facts) or DONE (complete). The required-field set is '
    'a function of this value and lives in Go, in charging.RequiredFieldsFor -- deliberately '
    'NOT as a database CHECK (roadmap D5): a CHECK backstop would turn every future change '
    'to the skip set into a migration, working against the ticket''s maintainability '
    'requirement. Pre-existing rows backfilled to IN_PROGRESS by this column''s DEFAULT, by '
    'the user''s explicit choice, so historical entries surface as unreviewed. Not indexed: '
    'nothing predicates on it.';

COMMENT ON COLUMN manual_charge_entries.energy_source IS
    'Provenance of energy_added_kwh: USER when the value came from the person, ESTIMATED '
    'when this module derived it from the pack capacity and the battery delta on write '
    '(roadmap D3/D4). Always computed by internal/charging, never accepted from a caller -- '
    'the same shape charge_sessions.battery_pct_source already uses. It exists so a future '
    'per-vehicle capacity average (backlog #18) can filter WHERE energy_source = ''USER'': '
    'inferred_capacity_kwh_calc on an ESTIMATED row returns exactly the capacity constant by '
    'algebra, so including such rows would seed that average with its own output. This fact '
    'CANNOT be reconstructed later -- once 31.00 is stored, a typed value and a derived one '
    'are indistinguishable. Not indexed: nothing predicates on it yet.';

COMMENT ON COLUMN manual_charge_entries.odometer_km IS
    'Odometer reading in kilometres observed AT this charge event -- an observation belonging '
    'to the event, not current vehicle state, which is why it lives here and not on a vehicle '
    'table (roadmap D6). INTEGER, not NUMERIC(10,1): whole kilometres are what the user reads '
    'off the dash (the user chose this over the recommended one-decimal type). The _km suffix '
    'is mandatory under the project display-unit rule (ai/go-conventions.md). NULL means not '
    'recorded. Not indexed: nothing predicates on it.';

-- +goose Down
-- DESTRUCTIVE, and it REFUSES rather than destroys where it cannot be honest.
--
-- Dropping the three columns destroys their data irrecoverably: recorded odometer
-- readings, and every energy provenance marker. status is recoverable only in the
-- trivial sense that it was uniformly IN_PROGRESS at Up time -- not after users have
-- set it. Nothing here is recomputable from the surviving columns, unlike
-- 20260829000001's Down.
--
-- Restoring energy_added_kwh's NOT NULL is impossible on any database that has since
-- accepted a NULL-energy row, and there is no honest repair: backfilling 0 violates
-- the CHECK, and any other invented number is a lie about the user's charge. So the
-- guard below aborts the rollback with an actionable message instead of deleting the
-- operator's rows or fabricating a value. Resolve those rows first, then re-run.
-- +goose StatementBegin
DO $$
DECLARE
    null_rows BIGINT;
BEGIN
    SELECT count(*) INTO null_rows
      FROM manual_charge_entries
     WHERE energy_added_kwh IS NULL;

    IF null_rows > 0 THEN
        RAISE EXCEPTION
            'cannot roll back 20260829000002: % row(s) in manual_charge_entries have a NULL '
            'energy_added_kwh, which the restored NOT NULL constraint forbids. Supply or '
            'delete those rows deliberately, then re-run this rollback. This migration will '
            'not invent an energy value or delete a user row on your behalf.', null_rows;
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE manual_charge_entries ALTER COLUMN energy_added_kwh SET NOT NULL;
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS odometer_km;
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS energy_source;
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS status;
```

### Resulting column set (the four columns this change touches)

| Column | Type | Null | Default | Constraint | Indexed |
|---|---|---|---|---|---|
| `status` | `TEXT` | NOT NULL | `'IN_PROGRESS'` | `CHECK (status IN ('IN_PROGRESS','DONE'))` | **no** |
| `energy_source` | `TEXT` | NOT NULL | `'USER'` | `CHECK (energy_source IN ('USER','ESTIMATED'))` | **no** |
| `odometer_km` | `INTEGER` | NULL | — | `CHECK (odometer_km >= 0)` | **no** |
| `energy_added_kwh` | `NUMERIC(6,2)` | **NULL** *(was NOT NULL)* | — | `CHECK (energy_added_kwh > 0)` *(unchanged)* | no (unchanged) |

### Query changes (`db/query.sql`)

Only the two write queries change. `CreateEntry` adds `status`, `energy_source`, `odometer_km` to
its column list and `@status`, `@energy_source`, `@odometer_km` to its `VALUES`. `UpdateEntry` adds
`status = @status`, `energy_source = @energy_source`, `odometer_km = @odometer_km` to its `SET`.
**No read query is edited** — all four are `SELECT *`, which sqlc expands to include the new
columns automatically (Context fact 5). `sqlc.yaml` is not touched.

### Expected sqlc diff (verify after `make sqlc`; report anything else)

- `db/models.go` — `ManualChargeEntry` gains exactly three fields: `Status string`,
  `EnergySource string`, `OdometerKm pgtype.Int4`, each carrying its `COMMENT ON COLUMN` text as a
  doc comment. **`EnergyAddedKwh` does not change type**: it is already `pgtype.Numeric`, which
  carries its own `Valid` flag, so `DROP NOT NULL` produces no Go-type change at all. `ChargeSession`
  is untouched.
- `db/query.sql.go` — `CreateEntryParams` and `UpdateEntryParams` each gain the same three fields;
  every `SELECT *` / `RETURNING *` column list and every `Scan(…)` list gains three entries. No
  other `*Params` struct changes — in particular **not** `MirrorChargeSessionParams` or
  `VerifyChargeSessionParams`, which belong to the untouched `charge_sessions` table.

---

## Index Plan

Mandatory under `openspec/config.yaml` §design ("an index plan justified against the project's
read patterns") and `CLAUDE.md` §Design-Gates.

### The declared read patterns on `manual_charge_entries`

| # | Port method | Predicates | Order | Index used |
|---|---|---|---|---|
| 1 | `Reader.ListEntriesByVehicle` | `account_id`, `tesla_id` | `charged_on DESC` | `idx_..._vehicle_time` |
| 2 | `Reader.ListEntriesByAccount` | `account_id` | `charged_on DESC` | `idx_..._account_time` |
| 3 | `Reader.ListEntriesByVehicleBetween` | `account_id`, `tesla_id`, `charged_on BETWEEN` | `charged_on DESC` | `idx_..._vehicle_time` |
| 4 | `Reader.ListEntriesByVehicleUpdatedSince` | `account_id`, `tesla_id`, `updated_at >=` (residual) | `charged_on DESC` | `idx_..._vehicle_time` |

### Decision: **no new index**, and no change to either existing index

**None of the three new columns appears in any `WHERE`, `ORDER BY`, `JOIN` or `GROUP BY` clause —
before or after this change.** They are *projected*, never *predicated*. Concretely:

- `status` is rendered as a badge and a completeness dot in tier 3's table. Rendering a column is
  a projection; every one of those rows was already fetched by read pattern 3.
- Tier 3's date filter and its four aggregation tiles are computed **server-side from one query**
  over the **existing** `ListEntriesByVehicleBetween` (roadmap **D13** — "no new port method"), and
  the tiles are summed in Go over that same result set. So even the dashboard that displays status
  never asks the database to filter by it.
- `energy_source` has **no reader at all** in this tier. It exists so backlog #18 can filter on it
  later — and that future consumer is a per-vehicle aggregate over a handful of rows, not a hot
  dashboard read.
- `odometer_km` is displayed, never searched.

This is the identical reasoning `20260829000001` applied to `inferred_capacity_kwh_calc`, and that
`internal/analytics` applies to `vehicle_metrics`' five `_calc` columns: **an index on a column
nothing predicates on is pure write and storage cost on a read path that gains nothing.** The
project's read-heavy `Performance-Profile` licenses aggressive indexing *for reads that exist*; it
does not license indexing a column no query mentions.

A `status` index would additionally be a poor one on its own terms: two distinct values over a
single household's rows, so the planner would prefer a sequential scan anyway.

**Revisit trigger — stated so the next agent does not have to re-derive it.** If a future change
adds a read that *filters* by status (e.g. "show only my unfinished charges"), the right shape is a
**partial** index that keeps the existing tenant-leading prefix:

```sql
CREATE INDEX idx_manual_charge_entries_vehicle_open
    ON manual_charge_entries (account_id, tesla_id, charged_on DESC)
    WHERE status = 'IN_PROGRESS';
```

Partial, because `IN_PROGRESS` rows are the minority in steady state and a partial index over them
is a fraction of the size of a full one. `account_id`-leading, because every multi-tenant table in
this project indexes `account_id` first (`ai/go-conventions.md` §"Read optimization"). Do **not**
create a standalone `(status)` index.

---

## Decisions

### D1 — `status TEXT NOT NULL DEFAULT 'IN_PROGRESS' CHECK (…)`, one statement, no index

*Implements roadmap **D1** verbatim.* `TEXT` + `CHECK` over a Postgres `ENUM`, backfill to
`IN_PROGRESS`, single `ADD COLUMN` because the backfill value equals the default, no index.

| Alternative | Why rejected |
|---|---|
| Postgres `ENUM` type | Inconsistent with `location_kind` / `charging_type`, which this table already models as `TEXT` + `CHECK`; and altering an enum later is painful (a value can never be removed). Roadmap D1. |
| Backfill to `DONE` | The leader recommended it; **the user chose `IN_PROGRESS` deliberately**, so historical rows surface as unreviewed. Not an oversight. Roadmap D1. |
| Two-step backfill-then-constrain (`20260720000001`'s shape) | Needed there only because the backfill value was deliberately *not* the default. Here it is, so `ADD COLUMN … NOT NULL DEFAULT …` already assigns every existing row and a separate `UPDATE` would match zero rows. |
| A `BOOLEAN is_done` | Closes the door on a third state (e.g. `ABANDONED`) without a migration, and reads worse at every call site. |

### D2 — `energy_added_kwh` drops `NOT NULL`; `Entry.EnergyAddedKWh` becomes `*float64`

*Implements roadmap **D2** verbatim.* The existing `CHECK (energy_added_kwh > 0)` is **kept**: a
`CHECK` evaluates to `NULL`, not `false`, on a `NULL` input, and Postgres accepts a row whose
`CHECK` is `NULL`. So it still rejects `0` and every negative, while permitting the new `NULL`.
Verified by Test Contract **B2/B3/B4**, not assumed.

`*float64` follows this module's existing rule that every nullable column maps to `*T`
(`StartBatteryPct *int`, `StartedAt *time.Time`, `InferredCapacityKWhCalc *float64`). `CostPerKWh()`
becomes `nil`-safe over it: `nil` when the pointer is `nil`, and — as today — `nil` when the value
is `0`.

| Alternative | Why rejected |
|---|---|
| Keep `NOT NULL`, store `0` for an unfinished charge | Violates the existing `CHECK (> 0)`, and `0 kWh` is a *claim* ("no energy was delivered"), not an absence. It would also feed `inferred_capacity_kwh_calc` a real zero. |
| Keep `float64` and use `0` as the sentinel in Go only | Makes `CostPerKWh`'s existing zero-guard ambiguous, and the DB column is genuinely nullable — a domain type that cannot represent `NULL` would silently invent a value on read. |
| A separate `energy_known BOOLEAN` companion | Two columns to keep consistent where `NULL` already says it, and every reader would have to check both. |

### D3 — Energy is derived **on write**, with a fixed rounding scale of 2

*Implements roadmap **D3** verbatim.* On `Create` **and** on `Update`, in this order:

1. Normalize and validate `Status` (**D8**).
2. Enforce `RequiredFieldsFor(status)` (**D5**). Reject before touching the capacity seam, so a
   rejected entry never pays for a capacity lookup that will one day hit a database.
3. **Derive energy** iff *all three* hold: the caller supplied **no** energy (`EnergyAddedKWh ==
   nil`), **both** battery percentages are present, and `*EndBatteryPct > *StartBatteryPct`. Then
   `energy = round2(capacity × (end − start) / 100)` and `energy_source = 'ESTIMATED'`.
4. Otherwise store exactly what the caller gave, with `energy_source = 'USER'` — including `NULL`
   when they gave nothing and it could not be derived. **Never a fabricated `0`.**

**The rounding scale is 2, and the column wins over the form.** `energy_added_kwh` is
`NUMERIC(6,2)`; tier 2's form input carries `step="0.001"`. Those have never agreed — the database
has silently rounded three-decimal input to two since `20260718000001`, and this change does not
alter that. **The column wins**, for three reasons: widening it is a table-rewriting `ALTER` no
roadmap decision asked for; widening it would change `inferred_capacity_kwh_calc`'s inputs and so
alter MAG-25's stored values, which roadmap **D4** explicitly rules out of scope; and two decimals
is the honest precision of a value read off a charger. The module therefore rounds **half away from
zero to 2 decimals** in Go (`math.Round(x*100)/100`, which is Postgres `numeric`'s own rounding
mode) *before* writing, so the number the module computed and the number Postgres stores are
provably the same — rather than leaving it to Postgres to round `22.939999999999998` for us.
*Recommendation to tier 2 (not a change here): set the form's `step` to `0.01`.*

With today's `62.0` capacity the rounding is a no-op in exact arithmetic — `62.0 × Δ/100 = 0.62Δ`
has at most two decimals for any integer `Δ` — but it is **not** a no-op in float64
(`0.62 × 37 = 22.939999999999998`), and it stops being a no-op in exact arithmetic the moment
backlog #18 replaces `62.0` with a measured capacity such as `62.35`. Test Contract **A8** pins
that case now, so the rule is proven before the constant changes.

**Range safety:** the derived value spans `0.62` (Δ=1) to `62.00` (Δ=100) — comfortably inside
`NUMERIC(6,2)`'s `9999.99`, and always `> 0`, so it can never trip the retained `CHECK`.

| Alternative | Why rejected |
|---|---|
| Derive on **read** | Contradicts the project's standing "conversion happens once, on write, never on read" non-negotiable and its read-heavy `Performance-Profile`; and it would make the stored `NULL` and the displayed number disagree. Roadmap D3. |
| Derive in the **gateway** | Puts domain arithmetic and the pack-capacity constant in the presentation layer, so a second caller (the JSON API, a future importer) would silently not get it. |
| Derive in a **generated column** | Impossible: the value must be stored *only when the user did not supply one*, which a generated column cannot express — it would overwrite the user's typed value. It is also why `inferred_capacity_kwh_calc` is a generated column and this is not; the two are opposites. |
| Derive when energy is supplied but the percentages disagree with it | Silently discards what the person typed. |

### D4 — `energy_source`, module-computed; `inferred_capacity_kwh_calc` untouched

*Implements roadmap **D4** verbatim.* `energy_source` is **always computed by
`internal/charging`, never accepted from a caller** — a value set on the `Entry` handed to
`Writer.Create`/`Update` is ignored and overwritten, exactly as `charge_sessions.battery_pct_source`
is computed by `SessionVerifier.VerifySession` and never taken as a parameter (Context fact 8).
Test Contract **C4** pins this.

**What `inferred_capacity_kwh_calc` now yields**, stated explicitly because it is the whole reason
this column exists:

| Row shape | `inferred_capacity_kwh_calc` | Why |
|---|---|---|
| energy `NULL`, percentages valid & increasing | **`NULL`** | The generated `CASE`'s `WHEN` tests only the two percentages (Context fact 3), so the `THEN` branch evaluates. `NULL / <positive divisor>` is `NULL` in SQL — no error, no division by zero. |
| energy `NULL`, a percentage missing or Δ ≤ 0 | `NULL` | The `WHEN` is false; `ELSE NULL`. Unchanged from today. |
| energy `ESTIMATED` (derived) | **exactly the capacity constant** (`62.000` today) | Algebra: `(C × d/100) / (d/100) = C`. This is the known, accepted consequence roadmap D4 documents. |
| energy `USER` | the genuine inference | Unchanged from today. |

The third row is why `energy_source` cannot wait: backlog #18's "average of the inferred
capacities" **must** filter `WHERE energy_source = 'USER'`, or it averages its own seed value back
into itself (the roadmap's worked example: 3 real rows averaging 68 plus 7 derived rows gives 63.8,
dragged toward the seed). And the fact is unreconstructable after the event — once `31.00` is
stored, a typed value and a derived one are indistinguishable.

Dropping `energy_added_kwh`'s `NOT NULL` **does not invalidate or rebuild** the generated column:
Postgres re-checks a generation expression when the expression itself or a referenced column's
*type* changes, not on a nullability change, and every already-`STORED` value stays as written.
Test Contract **B8** proves the `NULL`-energy case empirically rather than trusting this paragraph.

| Alternative | Why rejected |
|---|---|
| Change `inferred_capacity_kwh_calc`'s expression to skip `ESTIMATED` rows | A table-rewriting migration that alters MAG-25's behaviour out of this ticket's scope. Explicitly rejected in roadmap D4. |
| Add nothing, and reconstruct provenance later | Impossible in principle — see above. |
| A `BOOLEAN energy_is_estimated` | Closes the door on a third provenance (e.g. `IMPORTED`, `POLLED`) without a migration, and is inconsistent with `battery_pct_source`, which this project already models as a `TEXT` source marker. |

### D5 — `RequiredFieldsFor(Status) []Field` is the single source of truth; no DB `CHECK`

*Implements roadmap **D5** verbatim.* One exported, declarative lookup in `internal/charging`.
`Writer.Create` and `Writer.Update` enforce it for **every** caller; `internal/gateway` imports the
same lookup in tier 2 to decide which inputs render as required. Adding a field to the skip set
later is a one-line, one-file change that both layers follow — the ticket's "easy to maintain"
requirement.

```go
// Status is the lifecycle state of a manual charge entry (TEXT + CHECK in the DB).
type Status string

const (
    StatusInProgress Status = "IN_PROGRESS"
    StatusDone       Status = "DONE"
)

// EnergySource is the provenance of Entry.EnergyAddedKWh. Always computed by this
// module on write; a value supplied by a caller is ignored (D4).
type EnergySource string

const (
    EnergySourceUser      EnergySource = "USER"
    EnergySourceEstimated EnergySource = "ESTIMATED"
)

// Field names one field of an Entry whose presence the module can determine. Its
// string value is the database column name, which is ALSO the gateway's form input
// name and its validation-error map key (handlers/charges.go) -- so the gateway can
// map a Field straight onto an input with no translation table (Context fact 7).
type Field string

const (
    FieldChargedOn     Field = "charged_on"
    FieldLocationKind  Field = "location_kind"
    FieldEndedAt       Field = "ended_at"
    FieldEndBatteryPct Field = "end_battery_pct"
)

// RequiredFieldsFor returns the fields an entry must carry to be stored with the
// given status. It is the SINGLE SOURCE OF TRUTH for that rule: Writer.Create and
// Writer.Update enforce exactly this, and internal/gateway drives which inputs render
// as required from exactly this. Change the sets here and both layers follow.
//
// Returns a fresh slice on every call, so a caller mutating the result cannot corrupt
// the rule for the next one.
//
// An unrecognized status returns the DONE (strictest) set -- fail-closed. In practice
// unreachable: Writer rejects an unknown status before calling this (D8).
func RequiredFieldsFor(s Status) []Field
```

| Status | Required set |
|---|---|
| `IN_PROGRESS` | `charged_on`, `location_kind` |
| `DONE` | `charged_on`, `location_kind`, **`ended_at`**, **`end_battery_pct`** |

The `IN_PROGRESS` **skip set is therefore exactly `{ended_at, end_battery_pct}`**, which is roadmap
D5's stated initial skip set. Test Contract **A3** asserts that *as a set difference*, so the rule
is what the test states rather than two literal slices that can drift apart.

**⚠️ This makes `ended_at` newly required for a `DONE` entry.** Today it is optional in the DB and
in the form. This is the only reading consistent with the phrase "skip set", and it is the point of
the feature — a finished charge has an end time — but it is a genuine tightening and tier 2's form
must render `ended_at` as required when the status is `DONE`. Flagged for the design gate.

**Why `energy_added_kwh`, `price` and `currency` are *not* in either set** — stated so nobody
"completes" the table: energy is optional by **D2** and derivable by **D3**; price falls back to
`0` in the gateway by roadmap **D7** and so is always supplied; `currency` is a hardcoded, disabled
input defaulting to `'COP'`. They are also all non-pointer domain fields, so "missing" is not
determinable for them at all — a `Field` the enforcement loop cannot evaluate would be a lie in the
lookup the gateway trusts.

**Folding in `location_kind`.** The ad-hoc `if e.LocationKind == nil || *e.LocationKind == ""`
duplicated in `Create` and `Update` today (Context fact 6) is deleted and replaced by this lookup —
otherwise the "single source of truth" would have a second source sitting six lines above it.

**No status transition rule.** `DONE` → `IN_PROGRESS` is permitted, deliberately: a user who
mis-marked a charge as finished must be able to reopen it, and the roadmap defines no transition
machine. Test Contract **C16** pins it so nobody adds one by accident.

| Alternative | Why rejected |
|---|---|
| A DB `CHECK (status <> 'DONE' OR ended_at IS NOT NULL AND end_battery_pct IS NOT NULL)` | **Offered and explicitly rejected at the interview (roadmap D5)**: every future change to the skip set would then require a migration, defeating the maintainability requirement that motivated the ticket. |
| A typed `*MissingFieldsError{Fields []Field}` | More public surface for no gain: the gateway drives its own per-field messages from `RequiredFieldsFor` *before* calling, so the module's error is a backstop a human reads, not a structure a caller parses. A plain error listing every missing field, in the lookup's own deterministic order, is enough. Revisit if a caller ever needs to branch on the fields. |
| Validation in the gateway only | Leaves `Writer` accepting an invalid record from any other caller, and makes the rule un-testable without a browser. |
| A `map[Status][]Field` package variable | Mutable global — any caller could edit the rule at runtime. A function returning a copy cannot be corrupted. |

### D6 — `odometer_km INTEGER NULL CHECK (odometer_km >= 0)`

*Implements roadmap **D6** verbatim.* `INTEGER` over the recommended `NUMERIC(10,1)` by the user's
choice: whole kilometres are what the user reads off the dash. The `_km` suffix is mandatory under
the project's display-unit non-negotiable. Domain field `OdometerKm *int`, matching this module's
`*int` mapping for every nullable integral column. No index (§Index Plan). It lives on
`manual_charge_entries` because it is an observation made **at that charge event**, not current
vehicle state.

`INTEGER`'s ceiling is 2,147,483,647 km — roughly 700× a Tesla's expected lifetime mileage, so the
type is not a practical constraint. Go validation is deliberately **not** added for the `>= 0`
bound; the DB `CHECK` is the enforcement, matching how `start_battery_pct`/`end_battery_pct`'s
`BETWEEN 0 AND 100` is already enforced on this table's write path (`Writer` does not pre-check
them either). Consistency beats a one-off.

### D7 — `packCapacityKWh(ctx, vin) (float64, error)`, hardcoded `62.0`

*Implements roadmap **D8** verbatim* (numbered D7 here; the mapping is in the summary table below).

```go
// packCapacityKWh returns the usable pack capacity in kWh for the vehicle identified
// by vin. It is the seam D3's energy derivation divides by.
//
// TODO(MAG-18): this returns a hardcoded 62.0 for every vehicle. Backlog #18
// ("charging: replace the hardcoded 62 kWh pack capacity with a real per-vehicle
// value") replaces this body with a real lookup. That lookup MUST filter
// WHERE energy_source = 'USER' when averaging inferred capacities, or it averages
// this very constant back into itself -- see D4.
//
// The ctx and error results are deliberate future-proofing and are NOT dead weight:
// the whole reason the ticket demands a function here is that a DB- or module-backed
// lookup replaces it later, and such a lookup needs both. Having them now makes that
// swap a one-body change with zero caller churn. Do not "simplify" this to
// `func packCapacityKWh() float64`.
func packCapacityKWh(ctx context.Context, vin string) (float64, error)
```

Unexported: it is an implementation detail of **D3**'s derivation, and nothing outside this module
may divide by a pack capacity behind the module's back.

**Known limitation, stated rather than hidden.** `Writer.Update` derives using `e.VIN` from the
supplied `Entry`, but `UpdateEntry`'s SQL treats `vin` as immutable and never binds it, so a caller
that leaves `Entry.VIN` empty on an update still gets a derivation. That is harmless today (the
constant ignores `vin`) and would not be once backlog #18 lands. Two things follow, both recorded
for #18 rather than fixed here: the real lookup should resolve the vehicle from the **stored row**,
not the supplied `Entry`; and tier 2's gateway must keep populating `Entry.VIN` on the update path,
which `parseChargeForm` already does (`handlers/charges.go:797`).

### D8 — An empty `Status` normalizes to `IN_PROGRESS`; any other non-enum value is rejected

**Not a roadmap decision — forced, and flagged for the design gate.** The roadmap says what the two
statuses mean but not what an *absent* one means, and absence is the normal case for the whole
window between this tier landing and tier 2 shipping: `parseChargeForm` builds a `charging.Entry`
today with no `Status` field at all, so it arrives as Go's zero value `""`.

Specified: `Writer.Create` and `Writer.Update` treat `Status == ""` as `StatusInProgress` — the
same value the column's `DEFAULT` assigns — and reject any other value that is neither
`IN_PROGRESS` nor `DONE` with an error, before any database call. The DB `CHECK` remains the
backstop, not the error message, exactly as `SessionVerifier` treats the battery-percentage range
(RM31 D3).

This is what makes tier 1 **shippable on its own**: with it, the un-updated gateway keeps working
and every entry it creates lands on `IN_PROGRESS`. Without it, tier 1 would break entry creation
until tier 2 shipped, and the roadmap explicitly allows tiers 2 and 3 to run in either order or
concurrently after tier 1.

| Alternative | Why rejected |
|---|---|
| Reject `""` as invalid | Breaks entry creation between tiers, for zero benefit — `""` is unambiguous. |
| Let `""` reach the DB and take the column `DEFAULT` | Impossible: `CreateEntry` binds `status` explicitly, so `''` would be sent and the `CHECK` would reject it. The normalization has to be in Go. |
| Normalize *any* unrecognized value to `IN_PROGRESS` | Silently swallows a typo like `"DONE "` or `"done"`, storing the wrong lifecycle state. Fail loudly on a real value, quietly on absence. |

### D9 — No index on any new column

See §Index Plan for the full justification and the revisit trigger. Recorded as a numbered decision
because `openspec/config.yaml` §design requires the index plan to be an explicit, justified part of
the design rather than an omission.

### D10 — The `Down` migration is destructive and refuses rather than destroys

**Not a roadmap decision.** Dropping the three columns destroys their data irrecoverably (odometer
readings, provenance markers, and any status a user actually set), and nothing here is recomputable
from surviving columns — unlike `20260829000001`'s `Down`, which was non-destructive by
construction. Restoring `energy_added_kwh`'s `NOT NULL` is moreover *impossible* on a database that
has accepted a `NULL`-energy row, and there is no honest repair: `0` violates the retained `CHECK`,
and any invented number is a lie about the user's charge.

So the `Down` opens with a guarded `DO $$ … $$` block that raises an actionable exception naming the
row count, rather than deleting the operator's rows or fabricating a value. Stated in the migration
header so an operator meets it before running the rollback, not during.

### D11 — The `*float64` break in `internal/gateway` is out of this tier's sandbox

**Not a roadmap decision — a consequence of D2 that the roadmap's tier split does not cover.** A
`charging` worker may not edit `internal/gateway` (`ai/architecture.md` §2 sandbox rule), so at the
end of this tier `go build ./...` and `go vet ./...` fail repo-wide at `handlers/charges.go:612,
623, 798` plus the gateway's test fixtures. proposal.md §Breaking lists every site.

Recommended: the leader dispatches a **minimal mechanical `gateway` compile-fix** in the same wave —
nil-guard the two formatters (render `—`, the project-wide empty placeholder, per roadmap **D14**),
take the address of the parsed value at `:798`, update the fixtures. Nothing behavioural; tiers 2
and 3 own the real gateway work. The alternative, a red build between tiers, makes this tier's
reviewer gate unenforceable, since `go build ./...` is one of the signals the reviewer runs.

### Roadmap-decision mapping

| design.md | roadmap | Subject |
|---|---|---|
| D1 | **D1** | `status` column, TEXT+CHECK, IN_PROGRESS backfill, single statement, no index |
| D2 | **D2** | `energy_added_kwh` DROP NOT NULL, `CHECK (> 0)` kept, `*float64` |
| D3 | **D3** | derived energy on write, rounding scale, guard conditions |
| D4 | **D4** | `energy_source` provenance; `inferred_capacity_kwh_calc` untouched |
| D5 | **D5** | `RequiredFieldsFor`, skip set, no DB CHECK backstop |
| D6 | **D6** | `odometer_km INTEGER NULL CHECK (>= 0)` |
| D7 | **D8** | `packCapacityKWh` seam, 62.0, `TODO(MAG-18)`, backlog #18, KB note |
| D8 | — | empty-status normalization (forced; not covered by the roadmap) |
| D9 | **D1/D6** | index plan — the "no index" both decisions state, justified |
| D10 | — | `Down` semantics (forced; not covered by the roadmap) |
| D11 | — | the cross-module compile break (forced; not covered by the roadmap) |

Roadmap **D7** (`price` stays `NUMERIC(14,2) NOT NULL`, gateway coerces empty → `0`) requires **no
migration and no charging change** and is implemented entirely in tier 2. Roadmap **D9–D16** are
tiers 2 and 3.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing ("author
their expected values up front, in the change's `design.md`, before the implementation exists").
**Tests written later must assert THIS contract**, not whatever the implementation happens to
produce.

Unit tests are **included** for this change (roadmap header — the user's confirmed override of
their standing default).

**Conventions**, following this module's existing ones (`internal/charging/AGENTS.md` §Testing
Notes): fresh `uuid.New()` account ids per test; assert only against `charging.Entry` domain fields
and direct SQL column values — **never `pgtype`**, in any file; float comparison via
`math.Abs(*got - want) < 1e-9`, never `==`; assert `nil` / non-`nil` explicitly for every `NULL`
case. Integration tests are `DATABASE_URL`-gated through the package's existing `testdb_test.go`
and mirror `db_inferred_capacity_entries_integration_test.go`'s style.

Throughout, **`C` denotes the pack capacity constant, `62.0`** (D7).

### Group A — offline unit tests (no DB)

`entry_status_test.go` (new) for A1–A5, A7–A9; `charging_test.go` (existing) extended for A6.

| ID | Input | Expected | What it proves |
|---|---|---|---|
| **A1** | `RequiredFieldsFor(StatusInProgress)` | `[]Field{FieldChargedOn, FieldLocationKind}`, in that order | The IN_PROGRESS set, exactly. |
| **A2** | `RequiredFieldsFor(StatusDone)` | `[]Field{FieldChargedOn, FieldLocationKind, FieldEndedAt, FieldEndBatteryPct}`, in that order | The DONE set, exactly. |
| **A3** | set(DONE) − set(IN_PROGRESS) | `{FieldEndedAt, FieldEndBatteryPct}` | **Roadmap D5's skip set, asserted as a rule** rather than as two literal slices that can drift apart. |
| **A4** | mutate the returned slice, call again | The second call is unaffected | The lookup returns a defensive copy; the rule cannot be corrupted at runtime (D5). |
| **A5** | `RequiredFieldsFor(Status("PAUSED"))` | the DONE set | Fail-closed on an unrecognized status (D5). |
| **A6** | `CostPerKWh()` on `EnergyAddedKWh` = `nil` / `ptr(0)` / `ptr(15.5)` with `Price` 1000 | `nil` / `nil` / `≈64.516129` | The new `nil` case, and that the existing zero-guard survives the `*float64` change (D2). |

**A7 — the derivation table.** `derivedEnergyKWh(capacity, startPct, endPct) *float64`, the pure
helper D3's write path calls:

| capacity | start | end | Expected | What it proves |
|---|---|---|---|---|
| `62.0` | 50 | 100 | **`31.00`** | The headline case; also C2's stored value. |
| `62.0` | 64 | 74 | **`6.20`** | A small delta. |
| `62.0` | 0 | 1 | **`0.62`** | Minimum positive delta — still `> 0`, so it can never trip the retained `CHECK` (D2). |
| `62.0` | 0 | 100 | **`62.00`** | Maximum delta = the whole pack; the derivation's ceiling, well inside `NUMERIC(6,2)`. |
| `62.0` | 74 | 74 | **`nil`** | Zero delta: no derivation (D3). |
| `62.0` | 74 | 64 | **`nil`** | Negative delta: no derivation, never a negative energy (D3). |
| `62.0` | `nil` | 80 | **`nil`** | Missing start (D3). |
| `62.0` | 50 | `nil` | **`nil`** | Missing end — the ordinary IN_PROGRESS shape (D3). |

**A8 — rounding is half-away-from-zero at scale 2.** `derivedEnergyKWh(62.35, 0, 37)` ⇒ **`23.07`**
(`23.0695` rounded up), and `derivedEnergyKWh(62.35, 0, 25)` ⇒ **`15.59`** (`15.5875` rounded up).
Uses a capacity of `62.35`, **not** `62.0`, deliberately: with `62.0` the rounding is a no-op in
exact arithmetic, so a test using it would pass even if the rounding were deleted. This case pins
the rule **before** backlog #18 changes the constant (D3).

**A9 — the capacity seam.** `packCapacityKWh(ctx, vin)` returns exactly `62.0` and a `nil` error
for a real VIN, for `""`, and for an unknown VIN. Documents that the constant is VIN-independent
today, so a reviewer of backlog #18 sees exactly what changed (D7).

### Group B — schema, defaults and constraints (integration, direct SQL)

`db_entry_status_integration_test.go` (new). Direct `INSERT`s, because these assert the *database's*
behaviour, not the port's.

| ID | Statement | Expected | What it proves |
|---|---|---|---|
| **B1** | `INSERT` naming **only the pre-migration columns** (`account_id, tesla_id, vin, charged_on, energy_added_kwh, price, currency, location_kind`) | row created; `status = 'IN_PROGRESS'`, `energy_source = 'USER'`, `odometer_km IS NULL` | **The existing-row case.** A row that names none of the new columns takes exactly the path a pre-migration row took: the column `DEFAULT`. Roadmap D1's backfill, proven by the mechanism that performs it. |
| **B2** | `INSERT … energy_added_kwh = 0` | error, SQLSTATE **`23514`** | `CHECK (energy_added_kwh > 0)` still rejects zero after `DROP NOT NULL` (D2). |
| **B3** | `INSERT … energy_added_kwh = -5.0` | error, SQLSTATE `23514` | …and still rejects a negative (D2). |
| **B4** | `INSERT … energy_added_kwh = NULL` | row created; reads back `NULL` | …**and now accepts `NULL`**, because a `CHECK` is `NULL`-not-`false` on a `NULL` input. The core D2 proof. |
| **B5** | `INSERT … status = 'PAUSED'` | error, SQLSTATE `23514` | The `status` `CHECK` (D1). |
| **B6** | `INSERT … energy_source = 'GUESSED'` | error, SQLSTATE `23514` | The `energy_source` `CHECK` (D4). |
| **B7** | `INSERT … odometer_km = -1` | error, SQLSTATE `23514`; and `0` and `999999` both insert successfully | The `odometer_km` `CHECK`, both directions (D6). |
| **B8** | `INSERT … energy_added_kwh = NULL, start_battery_pct = 50, end_battery_pct = 100` | row created; `inferred_capacity_kwh_calc IS NULL` | **The D4 consequence for a NULL-energy row**, proven rather than argued: the generated `CASE`'s `WHEN` is true (it tests only the percentages), the `THEN` evaluates `NULL / 0.5`, and SQL yields `NULL` — not an error, not a division by zero. Also proves `DROP NOT NULL` did not invalidate the generation expression. |

### Group C — write-path behaviour through `Writer` / `Reader` (integration)

Same file. Seed via `Writer.Create`; read back via `Reader.ListEntriesByVehicle` unless stated.

| ID | Create input (beyond the always-present `AccountID`/`TeslaID`/`VIN`/`ChargedOn`/`LocationKind`/`Price`/`Currency`) | Expected stored state | What it proves |
|---|---|---|---|
| **C1** | `Status: IN_PROGRESS`, `EnergyAddedKWh: nil`, `StartBatteryPct: ptr(40)`, `EndBatteryPct: nil`, `EndedAt: nil` | created; `EnergyAddedKWh == nil`, `EnergySource == USER`, `InferredCapacityKWhCalc == nil`, `Status == IN_PROGRESS` | **The headline feature**: a charge can be logged at plug-in time. Nothing is fabricated (D2/D3). |
| **C2** | `Status: IN_PROGRESS`, `EnergyAddedKWh: nil`, `StartBatteryPct: ptr(50)`, `EndBatteryPct: ptr(100)` | `*EnergyAddedKWh == 31.00`, `EnergySource == ESTIMATED`, `*InferredCapacityKWhCalc == 62.000` | **Derivation (D3) and its D4 consequence together.** `62.000` is asserted deliberately: it is exactly `C`, by algebra, and that is the whole reason `energy_source` exists. |
| **C3** | `EnergyAddedKWh: ptr(20.5)`, `StartBatteryPct: ptr(50)`, `EndBatteryPct: ptr(100)` | `*EnergyAddedKWh == 20.50`, `EnergySource == USER`, `*InferredCapacityKWhCalc == 41.000` | The derivation does **not** fire when the caller supplied a value, even though the percentages would have allowed it (D3). |
| **C4** | `EnergySource: ESTIMATED`, `EnergyAddedKWh: ptr(20.5)`, percentages as C3 | `EnergySource == USER` | **A caller cannot set provenance** — it is always computed (D4), matching `battery_pct_source`'s precedent. |
| **C5** | `EnergyAddedKWh: nil`, `StartBatteryPct: ptr(60)`, `EndBatteryPct: ptr(60)`; and again with `ptr(60)`/`ptr(40)` | both created; `EnergyAddedKWh == nil`, `EnergySource == USER`, `InferredCapacityKWhCalc == nil` | No derivation on a zero or negative delta; the row is still created, not rejected (D3). |
| **C6** | `EnergyAddedKWh: nil`, `StartBatteryPct: nil`, `EndBatteryPct: ptr(80)` | created; `EnergyAddedKWh == nil`, `EnergySource == USER` | No derivation with a percentage missing (D3). |
| **C7** | `Status: DONE`, `EndedAt: ptr(t)`, `EndBatteryPct: nil` | **error** naming `end_battery_pct`; **row count unchanged** | `RequiredFieldsFor(DONE)` is enforced by `Writer`, and rejection happens before any DB write (D5). |
| **C8** | `Status: DONE`, `EndBatteryPct: ptr(90)`, `EndedAt: nil` | **error** naming `ended_at`; row count unchanged | The other half of the DONE set — the newly-required field (D5). |
| **C9** | `Status: DONE`, `EndedAt: ptr(t)`, `EndBatteryPct: ptr(90)` | created; `Status == DONE` | A complete DONE entry is accepted (D5). |
| **C10** | `Status: ""` (zero value), no `EndedAt`, no `EndBatteryPct` | created; `Status == IN_PROGRESS` | **D8** — what makes tier 1 shippable before tier 2. If this test fails, entry creation is broken for the un-updated gateway. |
| **C11** | `Status: "PAUSED"` | **error**; row count unchanged | An unknown status is rejected **in Go, before the DB** (D8); B5 covers the DB backstop separately. |
| **C12** | `OdometerKm: ptr(123456)`; and again `nil` | reads back `*OdometerKm == 123456`; and `nil` | `odometer_km` round-trips through the port in both states (D6). |

**Update cases** (same file; each seeds with `Writer.Create`, then calls `Writer.Update`):

| ID | Setup → Update | Expected | What it proves |
|---|---|---|---|
| **C13** | C3's row (`20.50`, `USER`) → Update with `EnergyAddedKWh: nil`, start 50, end 100 | `*EnergyAddedKWh == 31.00`, `EnergySource == ESTIMATED`, `*InferredCapacityKWhCalc == 62.000` | Derivation runs on `Update` identically to `Create`, and provenance flips USER → ESTIMATED (D3/D4). |
| **C14** | C2's row (`31.00`, `ESTIMATED`) → Update with `EnergyAddedKWh: ptr(25.0)`, start 50, end 100 | `*EnergyAddedKWh == 25.00`, `EnergySource == USER`, `*InferredCapacityKWhCalc == 50.000` | Provenance is **recomputed, never sticky** — an estimated row corrected by hand becomes USER (D4). |
| **C15** | C1's IN_PROGRESS row → Update with `Status: DONE`, `EndedAt: nil` | **error**; re-read shows the row **unchanged and still `IN_PROGRESS`** | `RequiredFieldsFor` is enforced on `Update`, not only on `Create`, and a rejected update writes nothing (D5). |
| **C16** | C9's DONE row → Update with `Status: IN_PROGRESS`, `EndedAt: nil`, `EndBatteryPct: nil` | accepted; `Status == IN_PROGRESS`, `EndedAt == nil` | **DONE → IN_PROGRESS is allowed** — there is deliberately no transition rule (D5). Pinned so nobody adds one by accident. |

### Owner verification (not automatable here)

The package's test database is provisioned fresh with every migration applied *before* any row
exists, so the real backfill of pre-existing production rows cannot be exercised by a test — the
same limitation `20260829000001` recorded as its D10. **B1** proves the mechanism (the column
`DEFAULT`); the owner confirms the outcome after `make migrate-up`:

```sql
SELECT status, energy_source, count(*), count(odometer_km) AS with_odometer
  FROM manual_charge_entries
 GROUP BY status, energy_source;
```

Expected on a database migrated from before this change: one row —
`IN_PROGRESS | USER | <total> | 0`.

---

## Risks

1. **`ended_at` becomes required for `DONE`** (D5). Every entry a user marks DONE from tier 2
   onward must carry an end time it did not have to carry before. Mitigated by: existing rows all
   backfill to `IN_PROGRESS`, so nothing already stored is invalidated; and tier 2 renders the
   field as required, so the user meets the rule in the form rather than as a 422.
2. **A derived energy is a claim the user did not make.** `energy_source` records that it is
   derived, and tier 3 can surface it — but a user reading the table sees a number they never
   typed. Deliberate (roadmap D3): a derived figure is more useful than a blank, and the
   provenance column is what keeps it honest.
3. **`62.0` is wrong for any vehicle that is not the first test car.** Every `ESTIMATED` value on
   another pack is wrong in proportion. Tracked as backlog #18; the seam (D7) exists precisely so
   the fix is a one-body change.
4. **The `Down` cannot fully roll back** (D10). Accepted, and made loud rather than silent.
5. **The cross-module compile break** (D11). The single item most likely to stall this tier at its
   reviewer gate; the leader must decide who fixes it before dispatching implementation.
