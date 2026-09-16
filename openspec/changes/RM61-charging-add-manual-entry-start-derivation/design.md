# Design — RM61-charging-add-manual-entry-start-derivation

> **Design gate: `database`.** This change adds one column to `manual_charge_entries`. Per
> `CLAUDE.md` §Pipeline config → `Design-Gates: database` and `openspec/config.yaml` §design,
> **the owner must confirm this document before any implementation task is dispatched.** It is
> written to be reviewable on its own: the complete DDL, the rationale with rejected
> alternatives, the index plan justified against the declared read patterns, and the Test
> Contract are all here.

Source ticket: MAG-40 · Roadmap: `openspec/roadmaps/RM61-manual-charge-start-derivation.md`, tier
1 of 2. The roadmap's "Decisions settled with the owner" (RD1–RD6) and its three code-derived
facts are binding and were confirmed with the user on 2026-09-15. This document does not re-open
them; where it says something the roadmap does not, it says so explicitly (**D2, D3, D4, D5**
below).

---

## Context

Facts that constrain every decision here. Each was read out of the repository, not recalled.

1. **`manual_charge_entries` is owned exclusively by `internal/charging`** — no cross-module FK,
   no cross-module read (`ai/architecture.md` §2). Its schema source of truth is the migration
   directory; there is no `schema.sql`.
2. **`derivedStartBatteryPct` (`internal/charging/capacity.go:110`) already exists and is
   unchanged by this proposal.** It takes a capacity, an energy figure, and an end percentage,
   and returns the start percentage that energy implies — or `nil` when the energy is `nil` or
   the rounded result falls outside `[0, 100]`. Its only caller today is
   `session_verifier.go:178`.
3. **`resolveEnergy` (`service.go:290`) is the sibling this new function mirrors.** It already
   runs inside `Writer.Create`/`Writer.Update`, after status validation, and calls
   `packCapacityKWh` only when the caller supplied no energy. `packCapacityKWh` itself reads
   `monthly_effective_capacity` through the `packCapacityLookup` seam and falls back to the
   hardcoded default when no measured row exists — unchanged by this proposal.
4. **`energy_source` and `price_source` are the existing precedent for a provenance column a
   caller may never supply**, both computed in Go and never read back from the caller's own
   struct field. Both are `TEXT NOT NULL` with a `DEFAULT`. The new column differs from both:
   it must be **nullable**, because a `NULL` `start_battery_pct` has no provenance to record —
   there is no third "absent" value to add to the `CHECK` set.
5. **`manual_charge_entries` has exactly three writers, all synchronous request-driven CRUD**
   (`db/query.sql:27,89,118`) — no background job writes this table, unlike
   `supercharger_sessions`, which the nightly mirror rewrites underneath `VerifySession`. This is
   why `VerifySession` takes a row lock and this change does not need to.
6. **All four `Reader` methods are `SELECT *`; both write queries name their columns
   explicitly** (the same fact `RM51`'s design.md recorded for `price_source`). A new column
   reaches every read for free, and cannot be bound by a write unless the write query is edited.
7. **Every `charging.Entry{...}` literal in `internal/gateway` uses named fields**
   (`grep -rn "charging.Entry{" internal/gateway/`). One new zero-valued struct field does not
   break compilation anywhere in the repo.
8. **`ListValidManualEntryCapacitiesForPeriod` (`db/query.sql:426`) has no `tesla_id` predicate
   and no index that serves its existing `energy_source = 'USER'` filter.** It scans
   `manual_charge_entries` bounded only by `charged_on >= @period_start AND charged_on <
   @period_end`, once a month, from the nightly job and the standalone `cmd/monthly-capacity`
   tool. This is a monthly batch read, not a dashboard read.
9. **`monthly-effective-capacity`'s own spec already states an asymmetric rule that this
   proposal must correct.** Its "A Vehicle's Effective Pack Capacity Is Measured Once Per Month"
   requirement says a Supercharger session counts as evidence only when its starting percentage
   "was both supplied directly, not calculated from an assumed capacity," but says a manual entry
   counts as evidence whenever "its energy was supplied by the person, not estimated" — with no
   mention of the starting percentage at all, because until this change a manual entry's starting
   percentage was never calculated. This is a **forced spec change**, not part of the roadmap's
   own tier description: see §Specs Affected.

## Goals / Non-Goals

**Goals**

- A manual charge entry missing only its starting percentage gets it filled in from the energy
  added and the ending percentage, the same help `VerifySession` already gives a Supercharger
  session.
- A caller's own typed starting percentage is never recomputed or overwritten.
- A row whose starting percentage was derived can never be counted as capacity evidence, even
  though its energy is genuinely user-supplied.
- The rule needs no new index.

**Non-Goals**

- The tier-2 gateway relaxation of the "required" rule and the placeholder rendering — that is
  `RM61-gateway-relax-start-battery-pct-required`. This change does not touch
  `internal/gateway`.
- Any change to `packCapacityKWh`, `derivedEnergyKWh`, `derivedStartBatteryPct`, or
  `session_verifier.go`. They are called, not edited.
- The `internal/analytics` pack-capacity disagreement (roadmap RD6, backlog item 31).
- A database constraint tying `start_battery_source`'s nullness to `start_battery_pct`'s — see
  **D2**.

---

## Database Changes

### The migration

`internal/charging/db/migrations/20260915000001_add_start_battery_source.sql`

```sql
-- +goose Up
-- A manual charge entry gains a provenance marker for its starting battery
-- percentage, matching the one energy_added_kwh and price already carry.
--
-- NULLABLE, NO DEFAULT -- unlike energy_source and price_source, which are
-- NOT NULL with a fallback value. A missing starting percentage has no
-- provenance to record: there is no third "absent" value that belongs in the
-- CHECK set, and a DEFAULT would falsely claim provenance for a row that
-- carries none.
--
-- BACKFILL: every existing row with a non-NULL starting percentage is set to
-- 'USER'. This is exact, not an approximation -- no derivation existed before
-- this column, so every one of those percentages was typed by a person.
--
-- TEXT + CHECK, NOT A POSTGRES ENUM: this table already models location_kind,
-- charging_type, status, energy_source and price_source the same way, and an
-- enum's value set is painful to widen later.
--
-- NO CROSS-COLUMN CHECK tying this column's nullness to start_battery_pct's.
-- The pairing is real (a NULL percentage should always have a NULL source) but
-- it is enforced in Go, at the single place that writes both columns, not in
-- the database: a same-transaction, same-statement invariant across two plain
-- columns does not need a CHECK to hold, and the CHECK expression itself would
-- have to reference two columns for a rule this module already guarantees by
-- construction.
--
-- COST: the ADD COLUMN is METADATA-ONLY on PostgreSQL 11+ -- no DEFAULT means
-- no value at all is written into existing rows by the ALTER itself. The
-- UPDATE rewrites only the rows it touches (every row with a non-NULL starting
-- percentage) and takes no stronger a lock than any ordinary UPDATE on this
-- table already takes.

ALTER TABLE charging.manual_charge_entries
    ADD COLUMN start_battery_source TEXT
        CHECK (start_battery_source IN ('USER','ESTIMATED'));

UPDATE charging.manual_charge_entries
   SET start_battery_source = 'USER'
 WHERE start_battery_pct IS NOT NULL;

COMMENT ON COLUMN charging.manual_charge_entries.start_battery_source IS
    'Provenance of start_battery_pct: USER when the person typed it, ESTIMATED '
    'when this module derived it from the energy added and the ending '
    'percentage. NULL exactly when start_battery_pct is NULL -- a missing '
    'percentage has no provenance. Always computed by internal/charging, never '
    'accepted from a caller -- the same shape energy_source and price_source '
    'already use. Historical rows were backfilled at migration time: every row '
    'with a non-NULL start_battery_pct is USER, because no derivation existed '
    'before this column. Not indexed: the one query that filters on it already '
    'scans a bounded period range with no supporting index of its own.';

-- +goose Down
-- LOSSY, not guarded -- there is no NOT NULL and no generated column here for
-- a restored constraint to violate, so DROP COLUMN always succeeds.
--
-- The loss is real but bounded and fully recomputable for every row that
-- existed at the time of this Down: a non-NULL start_battery_pct's provenance
-- is trivially 'USER' again by re-running the Up migration's own backfill
-- rule. A row whose provenance became ESTIMATED after this Up ran is
-- indistinguishable from a typed one once the column is gone -- the same
-- category of accepted loss this table's other backfilled columns already
-- carry on their own Down migrations.
ALTER TABLE charging.manual_charge_entries DROP COLUMN IF EXISTS start_battery_source;
```

### Resulting column (the one column this change touches)

| Column | Type | Null | Default | Constraint | Indexed |
|---|---|---|---|---|---|
| `start_battery_source` | `TEXT` | **nullable** | none | `CHECK (start_battery_source IN ('USER','ESTIMATED'))` | **no** |

### Query changes (`db/query.sql`)

- **`CreateEntry`** — add `start_battery_source` to the column list and `@start_battery_source`
  to the `VALUES`, with a comment stating it is computed in Go, mirroring `energy_source`'s
  existing comment.
- **`UpdateEntry`** — add `start_battery_source = @start_battery_source` to the `SET` clause,
  same comment.
- **`ListValidManualEntryCapacitiesForPeriod`** — add `AND start_battery_source = 'USER'`. This
  is the query change that closes the circular-feedback gap: a row whose energy is genuinely
  user-supplied but whose starting percentage was derived from a capacity must not feed that
  same capacity's own measurement, because its `inferred_capacity_kwh_calc` comes back equal to
  the capacity the derivation divided by. **No other read query changes** — the other three are
  `SELECT *`, expanded automatically by sqlc (Context fact 6).

### Expected sqlc diff (verify after `make sqlc`; report anything else)

- `db/models.go` — `ManualChargeEntry` gains **exactly one** field:
  `StartBatterySource pgtype.Text` (nullable — unlike `EnergySource string` and
  `PriceSource string`, which are non-nullable). `SuperchargerSession` is untouched.
- `db/query.sql.go` — `CreateEntryParams` and `UpdateEntryParams` each gain
  `StartBatterySource pgtype.Text`; every `SELECT *` / `RETURNING *` column list and every
  `Scan(…)` list gains one entry. **`ListValidManualEntryCapacitiesForPeriodRow` is unchanged**
  — the new predicate adds no returned column. **No other `*Params` or `*Row` struct changes.**
  If one did, stop and report it: it means a query outside this change's scope was edited.
- `sqlc.yaml` needs **no** change.

---

## Index Plan

Mandatory under `openspec/config.yaml` §design ("an index plan justified against the project's
read patterns") and `CLAUDE.md` §Design-Gates.

### The declared read patterns touching `start_battery_source`

Only one query predicates on the new column:

| # | Caller | Predicates | Frequency | Index used |
|---|---|---|---|---|
| 1 | `MonthlyCapacityCalculator.Calculate` → `ListValidManualEntryCapacitiesForPeriod` | `energy_source = 'USER'`, `inferred_capacity_kwh_calc IS NOT NULL`, `charged_on` range, **new:** `start_battery_source = 'USER'` | once per calendar month, per the nightly job (plus the standalone `cmd/monthly-capacity` tool) | **none today** |

The four `Reader` methods (`ListEntriesByVehicle`, `ListEntriesByVehicles`,
`ListEntriesByVehicleBetween`, `ListEntriesByVehicleUpdatedSince`) never predicate on
`start_battery_source` — it rides along in the row exactly like `status`, `energy_source`, and
`price_source` already do. Both existing indexes
(`idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time`) are
untouched and keep serving exactly the scans they served before.

### Decision: **no new index**

**`ListValidManualEntryCapacitiesForPeriod` already has no index serving its `energy_source`
filter** (Context fact 8) — it has no `tesla_id` predicate to lead an index on, and it runs once
a month, not on any request path a user waits for. Adding `start_battery_source = 'USER'` puts a
second equality filter into the same unindexed scan the existing `energy_source` filter already
runs inside; it does not change the query's cost class. The read-heavy `Performance-Profile`
licenses aggressive indexing **for reads that exist and matter to a waiting user** — a monthly
batch job that already tolerates a full scan of one table gains nothing from an index on one more
of its filters.

**Rejected: a composite index on `(charged_on, energy_source, start_battery_source)` to fully
cover the query.** Offered as the "do it properly" alternative and rejected here for the same
reason RM51 rejected a `price_source` index: nothing else in this tier or any planned one reads
this table by this combination outside a once-a-month batch job that already accepts a full
table scan. Building an index ahead of a query that would benefit from it is exactly the
aggressive-write, negligible-read-benefit cost the read-heavy profile does not ask for.

**Revisit trigger.** If a future change adds a *user-facing* worklist (e.g., "manual entries with
an estimated starting percentage, for review") that runs on a request path, the right shape is a
partial index `ON (tesla_id, charged_on) WHERE start_battery_source = 'ESTIMATED'` — mirroring
the revisit trigger both `RM33`'s `status` and `RM51`'s `price_source` design docs already
recorded for their own columns. Do not create a standalone `(start_battery_source)` index.

---

## Decisions

### D1 — `resolveStartBatteryPct`: rule, placement, and the two-way nil contract

*Implements roadmap **RD2** and the "no precedence rule needed" fact verbatim.*

```go
// resolveStartBatteryPct applies the derivation rule for Create/Update: when the
// caller supplied no starting percentage, supplied an ending percentage, and the
// entry's own energy (possibly just resolved by resolveEnergy) is known, it derives
// a starting percentage from the pack capacity, the energy, and the ending
// percentage. A non-nil derivation is reported as StartBatterySourceEstimated. In
// every other case it returns exactly what the caller supplied -- nil included --
// and reports StartBatterySourceUser only when a percentage is actually present.
// The caller's own Entry.StartBatterySource is never read.
//
// Runs AFTER resolveEnergy: the energy this function divides by may itself have
// just been derived by resolveEnergy in the same write.
func resolveStartBatteryPct(ctx context.Context, lookup packCapacityLookup, e Entry, energy *float64) (*int, *StartBatterySource, error) {
    if e.StartBatteryPct == nil && e.EndBatteryPct != nil && energy != nil {
        capacity, err := packCapacityKWh(ctx, lookup, e.TeslaID)
        if err != nil {
            return nil, nil, fmt.Errorf("charging: resolving pack capacity: %w", err)
        }
        if derived := derivedStartBatteryPct(capacity, energy, e.EndBatteryPct); derived != nil {
            source := StartBatterySourceEstimated
            return derived, &source, nil
        }
        return nil, nil, nil
    }
    if e.StartBatteryPct == nil {
        return nil, nil, nil
    }
    source := StartBatterySourceUser
    return e.StartBatteryPct, &source, nil
}
```

Call site, identical in `Create` and `Update`, immediately after `resolveEnergy`:

```go
energy, energySource, err := resolveEnergy(ctx, w.store, e)
if err != nil {
    return Entry{}, err
}
startPct, startSource, err := resolveStartBatteryPct(ctx, w.store, e, energy)
if err != nil {
    return Entry{}, err
}
```

**Why the outer gate checks `energy != nil` before calling `packCapacityKWh`.** A capacity
lookup that will not be used is a wasted database round-trip. `derivedStartBatteryPct` already
returns `nil` for a `nil` energy on its own, so the gate is an optimization, not a second source
of truth for the rule — the two nil-checks (the gate's and the function's own) agree by
construction.

**Why a failed derivation (`derived == nil`, out-of-range) returns `(nil, nil)`, not
`(nil, &StartBatterySourceUser)`.** A `NULL` percentage has no provenance, whether nothing was
ever supplied or a derivation was attempted and produced nothing usable. Recording `USER` for a
percentage nobody typed would be false.

**Why a caller-supplied percentage is never re-examined for a possible derivation.** The first
`if` requires `e.StartBatteryPct == nil`; a non-nil caller value always falls through to the last
two lines regardless of what `e.EndBatteryPct` or `energy` are. This is the direct implementation
of "a start percentage the user actually typed is never recomputed or overwritten."

| Alternative | Why rejected |
|---|---|
| Split into a `needsDerivedStartBatteryPct` gate plus a separate call, mirroring `session_verifier.go`'s shape | `VerifySession`'s split exists because it also needs the gate result to decide whether to open a transaction (Context fact 5 — no such need here: this table has no concurrent background writer). A single function mirrors `resolveEnergy`'s own shape more directly, and the roadmap calls this function `resolveEnergy`'s sibling. |
| Recompute a caller-supplied out-of-range or inconsistent percentage | Rejected by roadmap fact "a start % the user actually typed is never recomputed or overwritten" — no condition overrides a non-nil caller value, ever. |

### D2 — `start_battery_source TEXT`, nullable, no `DEFAULT`, no cross-column `CHECK`

*Implements roadmap **RD2** verbatim; the "no cross-column CHECK" half is this document's own
call, not the roadmap's.*

Nullable and `DEFAULT`-less because a `NULL` `start_battery_pct` genuinely has no provenance —
unlike `price_source`, where every row has a price and therefore always has a provenance
(`UNCONFIRMED` at worst). `TEXT` + `CHECK` over a Postgres `ENUM`, for the identical reason
`energy_source` and `price_source` already chose it (Context fact 4).

**Why no `CHECK ((start_battery_pct IS NULL) = (start_battery_source IS NULL))`.** This pairing
is real and always true after this change ships — the module is the only writer of both columns,
and it writes them from the exact same `resolveStartBatteryPct` return value in the same
statement. A cross-column `CHECK` would encode a rule the application already guarantees by
construction, for a table with no other writer to police (Context fact 5: no background job
writes this table, so there is no second writer that could violate the pairing behind the
module's back). It also could not be `NOT VALID`-migrated painlessly around historical rows in
the same way a single-column `CHECK` can, for no read or write this change needs. Revisit if this
table ever gains a second writer.

| Alternative | Why rejected |
|---|---|
| `NOT NULL DEFAULT 'ESTIMATED'` or `'USER'`, matching `energy_source`/`price_source`'s shape | Both defaults are false for a row with no starting percentage at all — there is no truthful non-NULL default. |
| A third `CHECK` value, e.g. `'ABSENT'`, to keep the column `NOT NULL` | Redundant with `start_battery_pct IS NULL` itself, which already says "absent" — a second column would need to always agree with the first, the exact cross-column coupling **this decision's second half** already declines to enforce in the database. |
| The cross-column `CHECK` above | See this decision's own reasoning: enforced by the sole writer, not by the schema. |

### D3 — Exclusion filter on `ListValidManualEntryCapacitiesForPeriod`

*Implements the roadmap's "circular-feedback problem" section verbatim.* `energy_source = 'USER'`
alone does not catch a row whose energy was genuinely typed by a person but whose starting
percentage was derived from a capacity: `inferred_capacity_kwh_calc` for such a row equals the
very capacity the derivation divided by, so counting it as evidence would feed that capacity's
own next measurement. `AND start_battery_source = 'USER'` closes this — the identical shape
`ListValidSessionCapacitiesForPeriod`'s existing `status = 'DONE'` filter already uses to exclude
a Supercharger session whose starting percentage was calculated (`DONE_CALCULATED`).

| Alternative | Why rejected |
|---|---|
| Filter in Go, after the query returns rows | The query already filters on `energy_source`; adding the second provenance check to the same `WHERE` keeps both rules in one place, visible in one query, matching the Supercharger side's own single-`WHERE`-clause shape. |
| A single combined provenance column instead of two (`energy_source` and `start_battery_source`) | Rejected in the roadmap interview (RD2): these are two independently-computed facts about two different fields, and folding them into one column would need a four-way value set (`USER/USER`, `USER/ESTIMATED`, …) that is harder to reason about than two binary columns. |

### D4 — `StartBatterySource`: an exported named type, held as a pointer on `Entry`

**Not a roadmap decision — the roadmap fixes the DB values (`'USER'`/`'ESTIMATED'`) but not the
Go type shape.** Two things pull in different directions here: `energy_source` and
`price_source` are modeled as exported named string types (`EnergySource`, `PriceSource`) for a
closed, typo-proof vocabulary — but both are non-nullable, so neither needs a pointer. This
column is nullable. The decision: keep the named-type vocabulary (`StartBatterySource`,
`StartBatterySourceUser`, `StartBatterySourceEstimated`), and hold it as `*StartBatterySource` on
`Entry` — `nil` reads as "no provenance," matching the column's own nullability, exactly as
`Session.BatteryPctSource` already holds a nullable provenance as a pointer (there, `*string`,
because it has only one possible non-nil value; here, two, so the named type still earns its
keep).

```go
// StartBatterySource is the provenance of Entry.StartBatteryPct: StartBatterySourceUser
// when the person typed it, StartBatterySourceEstimated when this module derived it
// from the energy added and the ending percentage on write. ALWAYS COMPUTED BY THIS
// MODULE on Create/Update -- a value set here is ignored and overwritten, the same
// shape EnergySource and PriceSource already use.
type StartBatterySource string

const (
    StartBatterySourceUser      StartBatterySource = "USER"
    StartBatterySourceEstimated StartBatterySource = "ESTIMATED"
)
```

`Entry` gains one field:

```go
// StartBatterySource records where StartBatteryPct came from. nil exactly when
// StartBatteryPct is nil -- a missing percentage has no provenance to record.
// ALWAYS COMPUTED BY THIS MODULE on Create/Update -- a value set here is ignored
// and overwritten, the same rule EnergySource and PriceSource already follow.
StartBatterySource *StartBatterySource
```

**Why `resolveStartBatteryPct` takes `packCapacityLookup`, not the wider `store` interface
`resolveEnergy` itself takes.** `resolveStartBatteryPct` needs exactly one method —
`latestMeasuredCapacity` — and nothing else `store` offers. Declaring the narrower interface
lets an offline unit test pass the existing `fakePackCapacityLookup` test double
(`monthly_capacity_estimator_test.go`) with no new fake type, satisfying the roadmap's ask for
unit tests "across every (energy, start, end) combination" without a database. `w.store` still
satisfies `packCapacityLookup` structurally, so the call site is unchanged in shape from
`resolveEnergy`'s own `w.store` argument.

| Alternative | Why rejected |
|---|---|
| Plain `*string` on `Entry`, unexported string constants | Loses the closed, typo-proof vocabulary `EnergySource`/`PriceSource` already give an agent reading this file — the exact "vocabulary an agent already finds here" reasoning the roadmap itself invokes for the DB values. |
| Declare `resolveStartBatteryPct`'s second parameter as `store`, matching `resolveEnergy` exactly | Would work, but forces any offline unit test to satisfy the entire `store` interface (five more methods this function never calls) instead of reusing the one-method fake that already exists. |

### D5 — No index (see §Index Plan)

Recorded as a numbered decision because `openspec/config.yaml` §design requires the index plan
to be an explicit, justified part of the design rather than an omission.

### Roadmap-decision mapping

| design.md | roadmap | Subject |
|---|---|---|
| D1 | **RD2** + "no precedence rule needed" | `resolveStartBatteryPct` — rule, placement, nil contract |
| D2 | **RD2** | `start_battery_source` column — nullable, no default, no cross-column CHECK |
| D3 | roadmap's "circular-feedback problem" | the capacity-query exclusion filter |
| D4 | — | Go type shape (forced, not covered by the roadmap) |
| D5 | roadmap's index hint | index plan — "no index," justified |

---

## Specs Affected

- **`manual-charge-log`** — one **ADDED** requirement, mirroring the shape of the existing
  "Energy added is optional, may be derived on write, and records its provenance" requirement.
- **`monthly-effective-capacity`** — one **MODIFIED** requirement ("A Vehicle's Effective Pack
  Capacity Is Measured Once Per Month"), correcting Context fact 9's asymmetry: a manual entry's
  validity now also requires its starting percentage to be person-supplied, not derived,
  mirroring the rule the same requirement already states for a Supercharger session's calculated
  starting percentage.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing ("author
their expected values up front, in the change's `design.md`, before the implementation exists").
**Tests written later must assert THIS contract**, not whatever the implementation happens to
produce.

**Conventions**, following this module's existing ones (`internal/charging/AGENTS.md` §Testing
Notes): fresh `uuid.New()` account ids per test; assert only against `charging.Entry` domain
fields — **never `pgtype`**, in any file; assert `nil`/non-`nil` explicitly for every optional
field; no measured-capacity row seeded anywhere in this contract, so every derivation below runs
against the hardcoded default capacity (62.0 kWh) via `packCapacityKWh`'s own fallback.

### Group A — offline unit tests (no DB): `resolveStartBatteryPct`

New file `start_battery_source_test.go`, package `charging` (the function under test is
unexported), using the existing `fakePackCapacityLookup` test double
(`monthly_capacity_estimator_test.go`) fixed at 62.0 kWh. Every case below states
(`StartBatteryPct`, `EndBatteryPct`, `energy` — the third argument, standing in for
`resolveEnergy`'s already-resolved output) and the expected `(*int, *StartBatterySource)`.

| ID | `StartBatteryPct` | `EndBatteryPct` | `energy` | Expected result | What it proves |
|---|---|---|---|---|---|
| **A1** | nil | nil | nil | `(nil, nil)` | Nothing supplied, nothing derivable — the base case. |
| **A2** | nil | nil | `10.0` | `(nil, nil)` | A missing end percentage blocks derivation even with energy present. |
| **A3** | nil | `74` | nil | `(nil, nil)` | A missing energy blocks derivation even with an end percentage present. |
| **A4** | nil | `74` | `6.20` | `(64, StartBatterySourceEstimated)` | The headline derivation, reusing `derivedStartBatteryPct`'s own precedent fixture (62.0 kWh capacity, 6.20 kWh, end 74 → start 64). |
| **A5** | nil | `10` | `62.00` | `(nil, nil)` | Derivation is attempted and produces an out-of-range result (raw deeply negative) — still `(nil, nil)`, never a clamp, never `USER`. |
| **A6** | `50` | nil | nil | `(50, StartBatterySourceUser)` | A caller-supplied value is returned as-is, `USER`, with nothing else present. |
| **A7** | `50` | nil | `10.0` | `(50, StartBatterySourceUser)` | Energy present does not matter once a start percentage is supplied. |
| **A8** | `50` | `90` | nil | `(50, StartBatterySourceUser)` | An end percentage present does not trigger recomputation of a supplied start. |
| **A9** | `50` | `90` | `20.0` | `(50, StartBatterySourceUser)` | **The never-recompute guarantee, proven even when a derivation would otherwise succeed**: all three inputs are present and a derivation is possible, but the caller's own `50` wins unconditionally. |

### Group B — schema, defaults, and constraints (integration, direct SQL)

`db_start_battery_source_integration_test.go` (new). Direct `INSERT`s where stated, because
these assert the *database's* behaviour, not the port's.

| ID | Statement | Expected | What it proves |
|---|---|---|---|
| **B1** | `INSERT` naming only pre-migration columns (no `start_battery_source`), `start_battery_pct = 50` | row created; `start_battery_source IS NULL` | **No `DEFAULT` exists.** Unlike `price_source`'s `B1`, a raw insert that never names this column gets `NULL`, not a fallback value — proving D2's "no truthful default" reasoning empirically. |
| **B2** | `INSERT … start_battery_source = 'DERIVED'` | error, SQLSTATE **`23514`** | The `start_battery_source` `CHECK` (D2). |
| **B3** | `INSERT … start_battery_pct = NULL, start_battery_source = 'USER'` | row created (no error) | **The database does not enforce the nullness pairing** — D2's own stated rejection of a cross-column `CHECK`. Only `resolveStartBatteryPct` (Group A/C) guarantees the pairing in practice; the schema alone would allow this inconsistent row. |

### Group C — write-path behaviour through `Writer`/`Reader` (integration)

Same file. Seed via `Writer.Create` unless stated; read back via `Reader.ListEntriesByVehicle`.
No measured-capacity row exists for the test vehicle in any case below, so every derivation uses
the hardcoded default (62.0 kWh).

| ID | Create/Update input (beyond the always-present `TeslaID`/`VIN`/`ChargedOn`/`LocationKind`/`Currency`) | Expected stored state | What it proves |
|---|---|---|---|
| **C1** | `StartBatteryPct: nil`, `EndBatteryPct: nil`, no energy | `StartBatteryPct == nil`, `StartBatterySource == nil` | Nothing supplied, nothing derived, through the real write path. |
| **C2** | `StartBatteryPct: nil`, `EndBatteryPct: ptr(74)`, `EnergyAddedKWh: ptr(6.20)` | `StartBatteryPct == 64`, `StartBatterySource == StartBatterySourceEstimated` | The headline derivation through `Writer.Create` — matches A4. |
| **C3** | `StartBatteryPct: nil`, `EndBatteryPct: ptr(74)`, no energy, no `StartBatteryPct` either | `EnergyAddedKWh == nil` (unresolvable — needs both percentages), `StartBatteryPct == nil`, `StartBatterySource == nil` | **No precedence conflict, proven end-to-end**: when `resolveEnergy` cannot derive an energy either (only one percentage present), nothing downstream can derive from it. |
| **C4** | `StartBatteryPct: ptr(50)`, `EndBatteryPct: ptr(90)`, no energy | `EnergyAddedKWh == 24.80`, `EnergySource == EnergySourceEstimated`; `StartBatteryPct == 50`, `StartBatterySource == StartBatterySourceUser` | **The two derivations never collide**: `resolveEnergy` derives the energy from the same percentage pair `resolveStartBatteryPct` would otherwise divide by, but the caller's own start percentage is never touched. |
| **C5** | `StartBatteryPct: nil`, `EndBatteryPct: ptr(90)`, `EnergyAddedKWh: ptr(20.0)` | `EnergySource == EnergySourceUser` (energy was supplied, not derived); `StartBatteryPct == 58`, `StartBatterySource == StartBatterySourceEstimated` | **The source of the energy does not matter to the start-percentage derivation** — only that it is non-nil. `90 - 20.0/62.0*100` rounds to `58`. |
| **C6** | Seed `Writer.Create` with `StartBatteryPct: ptr(50)`, `EndBatteryPct: ptr(90)`, `EnergyAddedKWh: ptr(20.0)` (stored `USER`/`50`); then `Writer.Update` with `StartBatteryPct: nil`, `EndBatteryPct: ptr(90)`, `EnergyAddedKWh: ptr(20.0)` | After `Update`: `StartBatteryPct == 58`, `StartBatterySource == StartBatterySourceEstimated` | **Clearing a typed value is how a caller asks for it to be calculated**, and the derivation re-runs on `Update` exactly as on `Create`. |
| **C7** | `StartBatteryPct: nil`, `EndBatteryPct: ptr(74)`, `EnergyAddedKWh: ptr(6.20)`, **and** `StartBatterySource: ptr(StartBatterySourceUser)` set on the same `Entry` | `StartBatteryPct == 64`, `StartBatterySource == StartBatterySourceEstimated` (the caller's assertion is ignored) | **A caller cannot set the provenance directly** — matches `EnergySource`'s and `PriceSource`'s identical precedent. |

### Group D — the capacity-query exclusion (integration, the change's central invariant)

Same file. Proves the one thing this change must not allow: a row whose starting percentage was
derived reaching `monthly_effective_capacity`'s evidence pool.

| ID | Setup | Query and expectation | What it proves |
|---|---|---|---|
| **D1** | Seed two entries in the same period, both with `EnergySource == USER` (energy supplied directly) and a valid (non-NULL) `InferredCapacityKWhCalc`: **Entry X** — `StartBatteryPct: ptr(50)` (typed), `EndBatteryPct: ptr(90)`, `EnergyAddedKWh: ptr(24.80)` → stored `StartBatterySource == USER`. **Entry Y** — `StartBatteryPct: nil`, `EndBatteryPct: ptr(90)`, `EnergyAddedKWh: ptr(24.80)` → derivation yields `90 - 24.80/62.0*100 = 50`, stored `StartBatterySource == ESTIMATED`. Both entries land on `InferredCapacityKWhCalc == 62.0` — the exact capacity constant both divisions used. | Call the store's `ListValidManualEntryCapacitiesForPeriod`-backed query for the covering period. | **Entry X is returned; Entry Y is not** — despite identical `EnergySource`, identical `InferredCapacityKWhCalc`, and passing every filter that existed before this change. This is the proof that `start_battery_source = 'USER'` is the only thing standing between a derived row and feeding its own capacity constant back into `monthly_effective_capacity`. |

### Owner verification (not automatable here)

The package's test database is provisioned fresh with every migration applied *before* any row
exists, so the real backfill of pre-existing production rows cannot be exercised by a test — the
same limitation `RM33` and `RM51` recorded for their own backfills. **B1** proves the mechanism
(no `DEFAULT`); the owner confirms the real outcome after `make migrate-up`:

```sql
SELECT start_battery_source, count(*)
  FROM charging.manual_charge_entries
 GROUP BY start_battery_source;
```

Expected on a database migrated from before this change: every row with a non-NULL
`start_battery_pct` shows `start_battery_source = 'USER'`; every row with a NULL
`start_battery_pct` shows `start_battery_source IS NULL`. There is no `ESTIMATED` row yet — this
migration's backfill never writes that value.

---

## Risks

1. **A row can be constructed, at the raw SQL level, with `start_battery_pct IS NULL` and
   `start_battery_source = 'USER'`** (B3) — the database does not forbid it. Mitigated by: this
   table has exactly one writer path (`Writer.Create`/`Writer.Update`, via `resolveStartBatteryPct`),
   which never produces that combination; the risk is confined to a hand-run `UPDATE` outside the
   module's own port, the same category of risk every column on this table already carries
   without a cross-column guard.
2. **The default-capacity dependency.** Every derivation in this contract runs against the
   hardcoded 62.0 kWh fallback because no vehicle in these tests has a measured
   `monthly_effective_capacity` row. Once a vehicle has a real measurement, the same derivation
   uses that number instead — unchanged behaviour, already covered by `packCapacityKWh`'s own
   existing tests.
3. **The `Down` migration is lossy**, the same accepted category of risk `RM51`'s `price_source`
   `Down` and `20260720000001`'s `location_kind` `Down` already carry.
