# Tasks — RM61-charging-add-manual-entry-start-derivation

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only.
**[leader]** — outside the module sandbox, so a module worker may not do it. **[owner]** — the
human. See design.md **D1–D5** for the rationale behind each group.

> ## Gate — do not start 1.1
>
> design.md is a `database` design gate (`CLAUDE.md` §Pipeline config). **No task below starts
> until the owner has confirmed design.md**, in particular:
>
> 1. **The column is nullable, no `DEFAULT`, no cross-column `CHECK`** — design.md **D2**,
>    unlike `energy_source`/`price_source`'s `NOT NULL DEFAULT` shape.
> 2. **No index on `start_battery_source`** — design.md **D5**/§Index Plan, including the
>    rejected composite index.
> 3. **The Go type shape** — design.md **D4**: `StartBatterySource` as an exported named type
>    held as a pointer, and `resolveStartBatteryPct` typed on the narrower `packCapacityLookup`
>    rather than `resolveEnergy`'s own `store`.

## Ordering constraints

- **Wave 1 is a serialization point and blocks everything.** sqlc generates against the
  migration directory *and* `query.sql`, so both must be final before `make sqlc` runs, and no
  Go file can reference `chargingdb`'s new field before it is generated. Nothing in Wave 2+ can
  start early.
- **1.1 and 1.2 touch disjoint files** and may be done in either order; **1.3 needs both.**
- **2.1 and 2.2 touch different files** (`charging.go`, `service.go`) but 2.2 depends on the
  type 2.1 declares.
- **Wave 3's tests cannot compile before Wave 2** (`ai/go-conventions.md` §Testing authoring
  order). Their expected values are already fixed in design.md §Test Contract — **author them
  against that contract, not against whatever the implementation produces.**
- **3.1 and 3.2 touch disjoint files** and may run in parallel with each other.
- **This tier has no cross-module compile-fix task**: every change is additive
  (proposal.md §Breaking), so `go build ./...`/`go vet ./...` should stay green outside
  `internal/charging` throughout.
- **No comment in any file this tier touches may cite `design.md`, a decision id (`D1`…`D5`),
  or `RM61`/`RD2`/`MAG-40`.** Write the reason itself — the exact wording design.md already
  uses for every function and column comment above. This binds Wave 1, 2, and 4 equally.

---

## Wave 1 — schema + codegen (serialization point)

- [ ] **1.1** **[module: charging worker]** Create
  `internal/charging/db/migrations/20260915000001_add_start_battery_source.sql` with the DDL in
  design.md §"Database Changes" → "The migration", **verbatim, including its full header
  comment and the `COMMENT ON COLUMN` statement**. Points that must not be trimmed:
  - **`ADD COLUMN start_battery_source TEXT CHECK (...)` — no `NOT NULL`, no `DEFAULT`.** Do not
    add either, even to "match" `energy_source`/`price_source` — design.md D2 explains why this
    column cannot have a truthful default.
  - **One separate `UPDATE ... WHERE start_battery_pct IS NOT NULL` statement**, backfilling to
    `'USER'` only.
  - **No cross-column `CHECK`** tying this column's nullness to `start_battery_pct`'s (design.md
    D2's own rejection). Do not add one even though it looks like an obvious safety net.
  - **No index.** Do not add the composite index design.md's §Index Plan rejected.
  - **Do not touch `status`, `energy_source`, `price_source`, `odometer_km`, or any
    `supercharger_sessions` column.** This migration names `manual_charge_entries` and only the
    one new column.
  - The `Down` is a single `ALTER TABLE ... DROP COLUMN IF EXISTS start_battery_source` — no
    guard block; there is no impossible state here to guard against.
  `depends_on`: the owner's design-gate confirmation · `parallel_ok`: with 1.2

- [ ] **1.2** **[module: charging worker]** Edit `internal/charging/db/query.sql`:
  - `CreateEntry` — add `start_battery_source` to the column list and
    `@start_battery_source` to the `VALUES`.
  - `UpdateEntry` — add `start_battery_source = @start_battery_source` to the `SET` clause.
  - Add a short comment on each stating that `start_battery_source` is **computed by this
    module and never accepted from a caller**, pointing at the `energy_source`/`price_source`
    precedent already commented in this same file. **No `design.md`/`RM61` citation** — state
    the reason plainly, matching this file's existing comment style for those two columns.
  - `ListValidManualEntryCapacitiesForPeriod` — add `AND start_battery_source = 'USER'` to the
    `WHERE` clause. Add a short comment explaining that a row whose starting percentage was
    derived divides by the same capacity this query feeds, so it must not count as evidence
    (design.md D3 — state the reason itself, not the decision id).
  - **Change no other read query.** All four `Reader` queries are `SELECT *` and sqlc expands
    them automatically. **Change no `supercharger_sessions` query** — none of
    `MirrorSuperchargerSession`, `ListSessionsByVehicleBetween`,
    `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`, `VerifySuperchargerSession`, or
    `ListValidSessionCapacitiesForPeriod` are in scope.
  `depends_on`: — · `parallel_ok`: with 1.1

- [ ] **1.3** **[module: charging worker]** Run `make sqlc` (allowed by `CLAUDE.md` §"Builds &
  local checks") and **review the diff against design.md §"Expected sqlc diff"**, which is this
  task's acceptance criterion — not "it ran":
  - `db/models.go` — `ManualChargeEntry` gains **exactly one** field:
    `StartBatterySource pgtype.Text` (nullable). `SuperchargerSession` must be untouched.
  - `db/query.sql.go` — `CreateEntryParams` and `UpdateEntryParams` each gain the same one
    field; every `SELECT *` / `RETURNING *` column list and `Scan(…)` list gains one entry.
    `ListValidManualEntryCapacitiesForPeriodRow` must be **unchanged** — the new predicate adds
    no returned column.
  - **No other `*Params` or `*Row` struct changes.** If one did, stop and report it.
  - `sqlc.yaml` needs **no** change. Do not touch it.
  Report the diff shape in your final report. `go build ./...` will fail until Wave 2 —
  expected at this point; note it and move on.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: no (blocks Wave 2)

---

## Wave 2 — domain surface and service wiring

- [ ] **2.1** **[module: charging worker]** `internal/charging/charging.go` — the domain
  surface:
  - Add the exported `StartBatterySource` type and its two constants
    (`StartBatterySourceUser`, `StartBatterySourceEstimated`), doc comment from design.md **D4**
    — state plainly that it is always computed by this module and a caller-supplied value is
    ignored, mirroring `EnergySource`'s comment shape. **No citation of `design.md`, `D4`, or
    `RM61` in the comment itself.**
  - Add `StartBatterySource *StartBatterySource` to `Entry`, doc comment from design.md **D4**:
    `nil` exactly when `StartBatteryPct` is `nil` — a missing percentage has no provenance.
  - **No interface gains, loses, or re-signs a method.** `Writer`, `Reader`, `SessionWriter`,
    `SessionReader`, `SuperchargerSessionAnalyticsReader`, and `SessionVerifier` are untouched,
    as are `Status`, `EnergySource`, `PriceSource`, `Field`, `RequiredFieldsFor`'s signature,
    `Session`, and `SessionMirror`.
  - Update `Entry`'s own doc comment and the package doc comment: the module can now fill in a
    missing starting battery percentage from the energy added and the ending percentage.
  `depends_on`: 1.3 · `parallel_ok`: no (2.2 depends on this)

- [ ] **2.2** **[module: charging worker]** `internal/charging/service.go`:
  - Add `resolveStartBatteryPct`, the full function from design.md **D1**, **verbatim including
    its doc comment** (with any decision-id citation stripped — state the reason plainly). Its
    second parameter is `packCapacityLookup`, **not** the wider `store` interface
    `resolveEnergy` takes (design.md D4) — do not widen it to `store` even though `w.store`
    already satisfies it.
  - In both `Create` and `Update`, **immediately after `resolveEnergy` returns**, call
    `resolveStartBatteryPct(ctx, w.store, e, energy)` and handle its error exactly like
    `resolveEnergy`'s own error.
  - Bind the result: `StartBatteryPct: intPtrToPgInt2(startPct)` (already present; unchanged
    binding, now fed by the resolved value instead of `e.StartBatteryPct` directly) and a new
    `StartBatterySource: <mapped *string>` param on both `CreateEntryParams` and
    `UpdateEntryParams`. Add the small `*StartBatterySource → *string` conversion helper
    `stringPtrToPgText` needs (nil stays nil; non-nil converts via `string(*source)`), named and
    placed beside this module's other domain→DB helpers (`numericPtrFromFloat64`,
    `intPtrToPgInt2`, …).
  - In `rowToEntry`, map the read-side `pgtype.Text → *StartBatterySource` (nil stays nil;
    non-nil converts via `StartBatterySource(*s)`), placed beside this module's other DB→domain
    helpers, and set `Entry.StartBatterySource` from it. Update `rowToEntry`'s mapping-rules doc
    comment to cover the new column.
  - `pgtype` stays confined to this file, as today.
  `depends_on`: 1.3, 2.1 · `parallel_ok`: no

---

## Wave 3 — tests (unit tests are included, per the roadmap header)

Every expected value is fixed in design.md §Test Contract. **Assert that contract.** Fixture
conventions: fresh `uuid.New()` account ids per test; never `pgtype` in any assertion or helper
(`internal/charging/AGENTS.md` §Testing Notes); no measured-capacity row seeded anywhere in this
wave, so every derivation runs against the hardcoded default (62.0 kWh).

- [ ] **3.1** **[module: charging worker]** Create `internal/charging/start_battery_source_test.go`
  — offline, no DB, package `charging` (the function under test is unexported). Reuse the
  existing `fakePackCapacityLookup` test double from `monthly_capacity_estimator_test.go`, fixed
  at 62.0 kWh. Cover design.md Test Contract **A1–A9** verbatim: every presence/absence
  combination of (`StartBatteryPct`, `EndBatteryPct`, `energy`), the out-of-range attempted
  derivation, and the never-recompute guarantee (A9) where all three inputs would allow a
  derivation but the caller's own value still wins.
  `depends_on`: 2.2 · `parallel_ok`: with 3.2

- [ ] **3.2** **[module: charging worker]** Create
  `internal/charging/db_start_battery_source_integration_test.go` — `TEST_DATABASE_URL`-gated,
  mirroring `db_promotion_price_source_integration_test.go`'s style and using the package's
  existing `testdb_test.go` pool. Cover design.md Test Contract:
  - **Group B (B1–B3)**: direct SQL — no `DEFAULT` on a raw insert omitting the column; the
    `CHECK` rejecting an invalid value (assert SQLSTATE `23514`, not message text); and the
    database allowing the inconsistent `start_battery_pct IS NULL, start_battery_source = 'USER'`
    row, proving the pairing is enforced only in Go.
  - **Group C (C1–C7)**: through `Writer`/`Reader` — the base case, the headline derivation, the
    "no precedence conflict" case, the "two derivations never collide" case, the
    "energy's own source does not matter" case, the `Update`-re-derives-on-clear case, and the
    "a caller cannot set the provenance directly" case.
  - **Group D (D1)**: the capacity-query exclusion — seed the two entries exactly as design.md
    specifies (identical `EnergySource`, identical `InferredCapacityKWhCalc == 62.0`, differing
    only in `StartBatterySource`), call the store method backing
    `ListValidManualEntryCapacitiesForPeriod` for the covering period, and assert the `USER` row
    is returned while the `ESTIMATED` row is not. **This is the change's central proof — do not
    skip it or reduce it to an assertion on `resolveStartBatteryPct`'s return value alone.**
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

---

## Wave 4 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [ ] **4.1** **[module: charging worker]** `internal/charging/AGENTS.md`:
  - §Public Interface — the `StartBatterySource` type and its two constants, the new `Entry`
    field, and that it is module-computed and ignored when supplied, nullable exactly where
    `StartBatteryPct` is nullable. Place it beside the existing `EnergySource`/`PriceSource`
    documentation.
  - §Data Ownership → `manual_charge_entries` — the new `start_battery_source` column: its
    `CHECK`, that it is nullable with no default (unlike its two siblings), that it is not
    indexed and why, and that the database does not enforce the nullness pairing with
    `start_battery_pct` (design.md D2).
  - §Testing Notes — the two new test files and what each covers.
  `depends_on`: 2.2 · `parallel_ok`: with 4.2

- [ ] **4.2** **[leader — outside the charging sandbox; grant the path or do it]**
  - `kkpa/context/architecture/charging-tables.md` — add the `start_battery_source` column
    beside the existing `energy_source`/`price_source` entries: its `CHECK`, its nullability
    (no default), and the "not indexed, revisit trigger" note from design.md §Index Plan.
  - `kkpa/context/workflows/manual-charge-crud.md` — add a note for the derivation rule beside
    the existing "Energy added is OPTIONAL and may be DERIVED on write" note, and update the
    "the monthly job filters `WHERE energy_source = 'USER'`" note (around the existing
    `packCapacityKWh` bullet) to also state the new `start_battery_source = 'USER'` filter and
    why (a derived starting percentage divides by the same capacity being measured).
  - `kkpa/context/INDEX.md` — add one new glossary row for `start battery source` →
    `charging.StartBatterySource` (`USER` / `ESTIMATED`) / `manual_charge_entries.start_battery_source`
    — module-computed, nullable, never caller-supplied, pointing at
    `workflows/manual-charge-crud.md`, mirroring the existing `price source` row's shape.
    Include a `start battery provenance` synonym row, mirroring `price provenance`.
  - **Never touch `openspec/changes/archive/`** — none of the three files above are inside it.
    Each is outside `internal/charging/`, so a charging worker cannot write it without an
    explicit grant.
  `depends_on`: 2.2 · `parallel_ok`: with 4.1

- [ ] **4.3** **[leader]** Sync the two delta specs in this change's own `specs/` folder into
  `openspec/specs/` — `manual-charge-log` (ADDED requirement) and `monthly-effective-capacity`
  (MODIFIED requirement) — via the project's normal spec-sync step, at the point the pipeline
  calls for it (not necessarily this wave). Confirm neither sync touches
  `openspec/changes/archive/`.
  `depends_on`: — (tracked here so it is not forgotten; timing follows the pipeline's own
  sync/archive step)

---

## Wave 5 — signals

- [ ] **5.1** **[module: charging worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/charging`, `go build ./...`,
  `go vet ./...`. **`go build ./...` and `go vet ./...` should be clean repo-wide** after this
  tier (proposal.md §Breaking — every change is additive). If either fails outside
  `internal/charging`, stop and report it rather than assuming it is expected; it is not.
  `depends_on`: 3.1, 3.2, 4.1 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the
  assistant's say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make migrate-up
  make check
  ```
  (`make check` = `build vet ui-guard i18n-guard money-guard tz-guard migration-guard
  boundary-guard theme-guard vehicleref-guard tenancy-guard archive-guard test`.
  `make test-with-db` if you want the `TEST_DATABASE_URL`-gated integration tests specifically.)

- [ ] **O2** **[owner]** Confirm the real backfill of pre-existing rows after `make migrate-up` —
  the package's test database is provisioned fresh with every migration applied before any row
  exists, so this cannot be a test (design.md §"Owner verification"):
  ```sql
  SELECT start_battery_source, count(*)
    FROM charging.manual_charge_entries
   GROUP BY start_battery_source;
  ```
  Expected: every row with a non-NULL `start_battery_pct` shows `start_battery_source = 'USER'`;
  every row with a NULL `start_battery_pct` shows `start_battery_source IS NULL`. No
  `'ESTIMATED'` row should exist yet — this migration's backfill never writes that value.

## Cross-module tasks the leader owns

- [ ] **L1** **[leader]** Confirm `go build ./...`/`go vet ./...` are green **outside**
  `internal/charging` once Wave 5 lands. Proposal.md §Breaking states no cross-module compile
  fix should be needed — verify rather than assume.
- [ ] **L2** **[leader]** Confirm the root `README.md` needs no edit. This change alters a
  module's public surface but adds, removes, or renames no module and no runnable.
  `depends_on`: 4.1 · `parallel_ok`: yes
