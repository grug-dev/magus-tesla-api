# Proposal — RM61-charging-add-manual-entry-start-derivation

Source: MAG-40 — https://linear.app/magus-monitor/issue/MAG-40/recalculated-battery-start
Roadmap: `openspec/roadmaps/RM61-manual-charge-start-derivation.md` — **tier 1 of 3**, module
`charging`, implementing roadmap decision **RD2** (the new column) and the three facts the
roadmap settled by reading the code (no precedence rule needed, no lock needed, status
unaffected).
Design gate: **TRIPPED.** This change adds one column to `manual_charge_entries` via a new
goose migration. Per `openspec/config.yaml` §design and `CLAUDE.md` §Pipeline config →
`Design-Gates: database`, design.md carries the full DDL, the rationale with rejected
alternatives, and the index plan, and **the owner must confirm that design before any
implementation is dispatched.**
Unit tests: **included** — the owner asked for them on 2026-09-15 (roadmap header). Both
kinds: offline unit tests for `resolveStartBatteryPct`, plus `TEST_DATABASE_URL`-gated
integration tests for the schema and the capacity-query exclusion. Expected values are fixed
in design.md §Test Contract **before** implementation, per `ai/go-conventions.md` §Testing
authoring order.

---

## Why

`SessionVerifier.VerifySession` already fills a missing start battery percentage on a
Supercharger session, from the energy Tesla metered and the ending percentage. A manual charge
entry has no such help — a user must always type the start percentage by hand, even when they
already typed the energy added and the ending percentage, from which the start percentage is
simple arithmetic.

The maths already exists and is shared: `derivedStartBatteryPct` (`internal/charging/capacity.go:110`)
computes `end - energy/capacity*100`, rounds half away from zero, and returns nothing outside
`[0, 100]` instead of clamping. Today only `VerifySession` calls it. This change gives
`Writer.Create` and `Writer.Update` the same call.

**Note on scope: REQ 1 of the source ticket contributes no code.** MAG-40 asked to change
`packCapacityKWh` to read the previous month's measured capacity. That work already shipped in
`RM52-charging-add-monthly-effective-capacity` (MAG-32): `packCapacityKWh` already reads the
newest non-NULL row from `monthly_effective_capacity`, which is deliberately better than
"previous month only" — a thin month falls back to the last real measurement instead of the
hardcoded default. The owner confirmed on 2026-09-15 to keep it exactly as it is. This proposal
is scoped to the start-percentage derivation only.

## What Changes

- **ADDED** — one goose migration
  `internal/charging/db/migrations/20260915000001_add_start_battery_source.sql`, adding one
  column to `manual_charge_entries`:
  - `start_battery_source TEXT CHECK (start_battery_source IN ('USER','ESTIMATED'))` —
    **nullable**, no `DEFAULT`, exactly where `start_battery_pct` is nullable.
  - a same-migration backfill
    `UPDATE manual_charge_entries SET start_battery_source = 'USER' WHERE start_battery_pct IS NOT NULL`.

  Unlike `price_source` (`NOT NULL DEFAULT 'UNCONFIRMED'`), this column stays nullable: a `NULL`
  `start_battery_pct` has no provenance to record. Full DDL, rationale and index plan in
  design.md §"Database Changes".
- **ADDED** — `StartBatterySource` type (`StartBatterySourceUser` / `StartBatterySourceEstimated`)
  and `Entry.StartBatterySource *StartBatterySource` (module-computed output, `nil` exactly when
  `Entry.StartBatteryPct` is `nil`) — the same "always computed, ignored when supplied" contract
  `EnergySource` and `PriceSource` already carry, adapted for nullability.
- **ADDED** — `resolveStartBatteryPct`, a sibling to the existing `resolveEnergy`
  (`internal/charging/service.go`): when the caller supplied no start percentage, supplied an
  end percentage, and the entry's (possibly just-resolved) energy is known, it derives a start
  percentage via the existing `derivedStartBatteryPct` and reports `ESTIMATED`. In every other
  case it stores exactly what the caller supplied (`nil` included) and reports `USER` only when
  a value is actually present. Wired into both `Writer.Create` and `Writer.Update`, **after**
  `resolveEnergy` — the energy it divides by may itself have just been derived in the same
  write.
- **CHANGED** — `internal/charging/db/query.sql`: `CreateEntry` and `UpdateEntry` bind the new
  `start_battery_source` column. `ListValidManualEntryCapacitiesForPeriod` gains
  `AND start_battery_source = 'USER'`, closing the circular-feedback gap the roadmap describes:
  a row whose energy is `USER` but whose start percentage was derived from a capacity must not
  feed that same capacity's own measurement. **No other read query changes** — the other three
  are `SELECT *`, which sqlc expands automatically.
- **CHANGED** — `internal/charging/db/models.go`, `db/query.sql.go` (both **generated**;
  `make sqlc` re-run). `sqlc.yaml` needs no change.
- **CHANGED** — `internal/charging/charging.go` (domain surface: `StartBatterySource` type +
  `Entry` field), `internal/charging/service.go` (the derivation function, wiring into
  `Create`/`Update`, the read-side mapping). **No change** to `internal/charging/validation.go`,
  `capacity.go`'s `derivedStartBatteryPct`/`derivedEnergyKWh`/`packCapacityKWh`, or
  `session_verifier.go` — they are called, not edited.
- **CHANGED** — `internal/charging/AGENTS.md` (§Public Interface, §Data Ownership, §Testing
  Notes) — docs-track-structural-change, `CLAUDE.md` §Non-negotiables.
- **UNCHANGED** — every existing index; `status`'s rules and `RequiredFieldsFor`; `energy_source`
  and its derivation seam; `price_source`; `inferred_capacity_kwh_calc`; the whole
  `supercharger_sessions` table and its ports; every file outside `internal/charging/` except
  `internal/charging/AGENTS.md`.

**Out of scope, deliberately:** relaxing the `start_battery_pct` "required" rule on the
`/external-charges` gateway page, and rendering a derived value as a placeholder — that is tier
2, `RM61-gateway-relax-start-battery-pct-required` (module `gateway`, depends on this tier).
This change does not touch `internal/gateway`. Also out of scope: the `internal/analytics`
62-vs-75 kWh disagreement (roadmap RD6, backlog item 31).

## Breaking?

**NO.** Every change here is additive: one new nullable `Entry` field
(`StartBatterySource`), one new exported type, one new unexported helper wired into two
existing methods. No interface gains, loses, or re-signs a method — `Writer`, `Reader`,
`SessionWriter`, `SessionReader`, `SuperchargerSessionAnalyticsReader`, and `SessionVerifier`
are all untouched, and so is every existing `Entry` field's type and meaning.

Every `charging.Entry{...}` literal in `internal/gateway` uses named fields
(`grep -rn "charging.Entry{" internal/gateway/` confirms this, same fact `RM51`'s proposal
recorded), so one new zero-valued field does not break compilation. A gateway caller that does
not yet know about `StartBatterySource` behaves exactly as it does today: a caller-supplied
start percentage is still stored as typed, unchanged in value. `go build ./...` and
`go vet ./...` should stay green repeatedly across module boundaries after this tier lands.

## Modules affected

- **`charging`** — owner. Schema, domain type, both write paths' behaviour, tests, `AGENTS.md`.
- **`gateway`** — **not affected by this tier.** It does not yet read `StartBatterySource` or
  relax the required-field rule; that is tier 2. `internal/gateway/handlers/external_charges.go`
  still sends `start_battery_pct` as it does today, and this tier does not require it to change
  that for `go build ./...` to stay green.
- **`analytics`** — consumes `charging.Reader.ListEntriesByVehicleUpdatedSince`. It reads
  `Entry.EnergyAddedKWh` and other fields, not `StartBatteryPct`/`StartBatterySource`
  (`grep -rn "StartBatteryPct\|StartBatterySource" internal/analytics/` should return nothing);
  the leader confirms with `go build ./...` after the change rather than trusting this sentence.
- No other module. `manual_charge_entries` is owned exclusively by `internal/charging`
  (`ai/architecture.md` §2); no cross-module FK, no cross-module read.

## Read paths affected

Per `openspec/config.yaml` §proposal.

| Port method / job | Query | Effect |
|---|---|---|
| `Reader.ListEntriesByVehicle` | `ListEntriesByVehicle` | +1 column per row (`SELECT *`) |
| `Reader.ListEntriesByVehicles` | `ListEntriesByVehicles` | +1 column per row (`SELECT *`) |
| `Reader.ListEntriesByVehicleBetween` | `ListEntriesByVehicleBetween` | +1 column per row (`SELECT *`) |
| `Reader.ListEntriesByVehicleUpdatedSince` | `ListEntriesByVehicleUpdatedSince` | +1 column per row (`SELECT *`) |
| `Writer.Create` / `Writer.Update` | `RETURNING *` | +1 column returned |
| `MonthlyCapacityCalculator.Calculate` (nightly + `cmd/monthly-capacity`) | `ListValidManualEntryCapacitiesForPeriod` | **one new equality predicate** on the new column, no new column returned |

**No query plan changes on the four `Reader` methods.** No `WHERE`, `ORDER BY`, or `LIMIT`
clause is touched and no predicate names the new column on any of them. Both existing indexes
(`idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time`) are
untouched and still serve exactly the scans they served before.

**`ListValidManualEntryCapacitiesForPeriod` is a monthly batch read, not a dashboard read.** It
already has no vehicle predicate and no index that serves its existing `energy_source = 'USER'`
filter (see design.md §Index Plan for the check). The new `start_battery_source = 'USER'`
predicate is evaluated in the same scan, at the same cost class, as the filter already there.

**No new index is added** (see design.md §Index Plan for the full justification). Nothing
filters, sorts, joins, or groups by `start_battery_source` outside this one monthly job; if that
changes, the query that needs it is what justifies an index, added then.

**Write path:** one `ADD COLUMN` with no `DEFAULT` — metadata-only, no table rewrite — plus one
`UPDATE` scoped to `WHERE start_battery_pct IS NOT NULL`, which rewrites only the rows it
touches.

## Impact

- **Affected specs:**
  - `manual-charge-log` — one **ADDED** requirement (the start-percentage derivation, mirroring
    the shape of the existing "Energy added is optional, may be derived on write, and records
    its provenance" requirement).
  - `monthly-effective-capacity` — one **MODIFIED** requirement ("A Vehicle's Effective Pack
    Capacity Is Measured Once Per Month"): its manual-entry validity rule currently reads "a
    user-logged charge record SHALL count only when its energy was supplied by the person, not
    estimated" — after this change it must also require the start percentage to be
    person-supplied, not derived, mirroring the rule the same requirement already states for a
    Supercharger session's calculated starting percentage. This is a **finding**, not part of the
    roadmap's own tier description — see design.md Context fact 9.
- **Affected code:** `internal/charging/` only —
  `db/migrations/20260915000001_add_start_battery_source.sql` (new), `db/query.sql`,
  `db/models.go` + `db/query.sql.go` (regenerated), `charging.go`, `service.go`, new
  `_test.go` files, `AGENTS.md`.
- **Design gate: tripped.** The owner confirms design.md before implementation. One item wants
  an explicit yes beyond the DDL itself: the type shape of `StartBatterySource` as a pointer to
  a new named type rather than a plain `*string` (design.md D4) — a decision the roadmap does
  not dictate.
- **`MIGRATIONS_DIRS` order check (recorded, not just claimed).** This migration's `ALTER TABLE`
  and `UPDATE` touch only `manual_charge_entries`, a table `internal/charging` owns exclusively,
  and reads no other module's table. The shared order (account → telemetry → charging →
  analytics) is not at risk — verified by reading the migration's own SQL, which names no table
  outside `manual_charge_entries`.
- **Deferred, explicitly NOT in scope:** the tier-2 gateway relaxation and placeholder
  rendering; any index on `start_battery_source`; the `internal/analytics` capacity
  disagreement (roadmap RD6, backlog item 31).

## Modules affected — summary table

| Module | Change |
|---|---|
| `charging` | Owner. Schema, domain, write-path rules, tests, docs. |
| `gateway` | None in this tier (additive change; no compile impact). |
| `analytics` | Read-only consumer; unaffected fields. |
