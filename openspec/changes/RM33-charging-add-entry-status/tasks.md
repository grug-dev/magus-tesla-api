# Tasks — RM33-charging-add-entry-status

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only.
**[leader]** — outside every module sandbox, so a module worker may not do it.
**[owner]** — the human. See design.md **D1–D11** for the rationale behind each group.

> ## Gate — do not start 1.1
>
> design.md is a `database` design gate (`CLAUDE.md` §Pipeline config). **No task below starts
> until the owner has confirmed design.md**, and in particular has answered the three items
> proposal.md §Impact lists:
>
> 1. **The DONE required-field set** — design.md **D5** makes `ended_at` required for a `DONE`
>    entry. It is optional everywhere today. This is the only reading consistent with roadmap D5's
>    "skip set", but it is a real tightening.
> 2. **What an empty status means** — design.md **D8**. Not covered by any roadmap decision, and
>    forced: until tier 2 ships, the gateway sends no status at all.
> 3. **Who fixes the `internal/gateway` compile break** — design.md **D11** / task **L1**.

## Ordering constraints

- **Wave 1 is a serialization point and blocks everything.** sqlc generates against the migration
  directory *and* `query.sql`, so both must be final before `make sqlc` runs, and no Go file can
  reference `chargingdb`'s new fields before they are generated. Nothing in Wave 2+ can start
  early.
- **1.1 and 1.2 touch disjoint files** and may be done in either order; **1.3 needs both.**
- **2.2 is independent of the rest of Wave 2** — a new file containing two pure functions. It may
  run in parallel with 2.1, and even during Wave 1.
- **2.4 is the join point**: it needs 1.3, 2.1, 2.2 and 2.3.
- **Wave 3's integration tests cannot compile before Wave 2** (`ai/go-conventions.md` §Testing
  authoring order). Their expected values are already fixed in design.md §Test Contract — **author
  them against that contract, not against whatever the implementation produces.**
- **3.1, 3.2, 3.3 and 3.4 touch disjoint files** and may run in parallel with each other.
- **L1 (the gateway compile fix) is not this module's**, but until it lands `go build ./...` fails
  repo-wide, so task **5.1** cannot be green without it.

---

## Wave 1 — schema + codegen (serialization point)

- [ ] **1.1** **[module: charging worker]** Create
  `internal/charging/db/migrations/20260829000002_add_entry_status.sql` with the DDL in design.md
  §"Database Changes" → "The migration", **verbatim, including its full header comment and all
  three `COMMENT ON COLUMN` statements**. That comment block is documentation, not decoration — it
  carries D1 (why one statement and not `20260720000001`'s two-step shape; why `IN_PROGRESS` and
  not `DONE`; why TEXT+CHECK and not an enum), D2 (why the `CHECK (> 0)` is kept and still works
  on `NULL`), D4 (what `inferred_capacity_kwh_calc` now yields), D6 (why `INTEGER`) and D10 (why
  the `Down` is destructive and why it refuses). Points that must not be trimmed:
  - **Three separate `ALTER TABLE … ADD COLUMN` statements plus one `ALTER COLUMN … DROP NOT
    NULL`.** No backfill `UPDATE` — each backfill value *is* its column's `DEFAULT` (D1).
  - **Do NOT touch `inferred_capacity_kwh_calc`**, its expression, or `charge_sessions` (roadmap
    **D4**). This migration names `manual_charge_entries` only.
  - **Do NOT drop and re-add `CHECK (energy_added_kwh > 0)`** — it is kept exactly as it is.
  - **No index, on any of the three new columns** (design.md §Index Plan).
  - The `Down` opens with the `-- +goose StatementBegin` / `DO $$ … $$` / `-- +goose StatementEnd`
    guard that raises an exception when `NULL`-energy rows exist, then restores `NOT NULL` and
    drops the three columns in reverse order. **The `StatementBegin`/`StatementEnd` markers are
    required** — goose splits on semicolons otherwise and the `DO` block breaks (the precedent is
    `20260823000001:229`).
  `depends_on`: the owner's design-gate confirmation · `parallel_ok`: with 1.2 and 2.2

- [ ] **1.2** **[module: charging worker]** Edit `internal/charging/db/query.sql`:
  - `CreateEntry` — add `status`, `energy_source`, `odometer_km` to the column list and
    `@status`, `@energy_source`, `@odometer_km` to the `VALUES`.
  - `UpdateEntry` — add `status = @status`, `energy_source = @energy_source`,
    `odometer_km = @odometer_km` to the `SET` clause.
  - Add a short comment on each explaining that `energy_source` is **computed by this module and
    never accepted from a caller** (design.md **D4**), pointing at the `battery_pct_source`
    precedent in `VerifyChargeSession` in this same file.
  - **Change no read query.** All four are `SELECT *` and sqlc expands them automatically. Do not
    add a `WHERE status = …` anywhere; nothing predicates on the new columns (§Index Plan).
  - **Change no `charge_sessions` query** — `MirrorChargeSession`, `ListSessionsByVehicleBetween`,
    `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle` and `VerifyChargeSession` are
    outside this change entirely.
  `depends_on`: — · `parallel_ok`: with 1.1 and 2.2

- [ ] **1.3** **[module: charging worker]** Run `make sqlc` (allowed by `CLAUDE.md` §"Builds &
  local checks") and **review the diff against design.md §"Expected sqlc diff"**, which is this
  task's acceptance criterion — not "it ran":
  - `db/models.go` — `ManualChargeEntry` gains **exactly three** fields: `Status string`,
    `EnergySource string`, `OdometerKm pgtype.Int4`. **`EnergyAddedKwh` must NOT change type** —
    it is already `pgtype.Numeric`, which carries its own `Valid` flag, so `DROP NOT NULL`
    produces no Go-type change. `ChargeSession` must be untouched.
  - `db/query.sql.go` — `CreateEntryParams` and `UpdateEntryParams` each gain the same three
    fields; every `SELECT *` / `RETURNING *` column list and `Scan(…)` list gains three entries.
  - **No other `*Params` struct changes** — not `MirrorChargeSessionParams`, not
    `VerifyChargeSessionParams`, not any `List…Params`. **If one did, stop and report it**: it
    means a query outside this change's scope was edited.
  - `sqlc.yaml` needs **no** change. Do not touch it.
  Report the diff shape in your final report. `go build ./...` will fail until Wave 2 — expected
  at this point; note it and move on.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: no (blocks Wave 2)

---

## Wave 2 — domain surface, validation, derivation

- [ ] **2.1** **[module: charging worker]** `internal/charging/charging.go` — the domain surface:
  - Add the exported types and constants from design.md **D5**: `Status` (`StatusInProgress`,
    `StatusDone`), `EnergySource` (`EnergySourceUser`, `EnergySourceEstimated`), `Field`
    (`FieldChargedOn`, `FieldLocationKind`, `FieldEndedAt`, `FieldEndBatteryPct`). Each `Field`
    constant's string value is the **database column name, which is also the gateway's form input
    name and error-map key** — document that, it is why the values are what they are.
  - Add `Status Status`, `EnergySource EnergySource`, `OdometerKm *int` to `Entry`, each with a doc
    comment. `EnergySource`'s comment must state that a value set by a caller is **ignored and
    overwritten** on `Create`/`Update` (D4), the way `InferredCapacityKWhCalc`'s already states it
    is ignored.
  - **Change `EnergyAddedKWh float64` to `*float64`** and move it out of the "Required fields"
    block into the optional block, with a comment: `nil` = not supplied and not derivable; the DB
    `CHECK (> 0)` still rejects `0`; the value may have been derived on write, in which case
    `EnergySource` is `ESTIMATED` (D2/D3).
  - Make `CostPerKWh()` nil-safe over the pointer: `nil` when `EnergyAddedKWh == nil` **and** when
    it points at `0` (the existing defensive guard survives).
  - Declare `RequiredFieldsFor(s Status) []Field` with the full doc comment from design.md **D5** —
    single source of truth, fresh slice per call, fail-closed on an unknown status. The body may
    live here or in the file 2.3 creates; the doc comment lives with the declaration.
  - Update `Entry`'s own doc comment and the package doc comment: the module now records a
    lifecycle status and may derive energy on write.
  - **No interface gains, loses or re-signs a method.** `Writer`, `Reader`, `SessionWriter`,
    `SessionReader`, `SuperchargerSessionAnalyticsReader` and `SessionVerifier` are untouched, as
    are `Session` and `SessionMirror`.
  `depends_on`: 1.3 · `parallel_ok`: with 2.2

- [ ] **2.2** **[module: charging worker]** Create `internal/charging/capacity.go` — a new file, so
  backlog #18 is a one-file change:
  - `packCapacityKWh(ctx context.Context, vin string) (float64, error)` returning `62.0, nil`, with
    the full doc comment from design.md **D7**, including the `TODO(MAG-18)` naming backlog #18
    **and** the instruction that the future lookup must filter `WHERE energy_source = 'USER'`, and
    the explicit "do not simplify this to `func packCapacityKWh() float64`" note.
  - `derivedEnergyKWh(capacityKWh float64, startPct, endPct *int) *float64` — the pure helper: `nil`
    unless both pointers are non-nil and `*endPct > *startPct`; otherwise
    `math.Round(capacityKWh*float64(*endPct-*startPct)/100*100) / 100`. Document that the scale is
    **2 because the column is `NUMERIC(6,2)`**, that the rounding mode is half-away-from-zero to
    match Postgres `numeric`, and that this is a no-op in exact arithmetic only for today's `62.0`
    constant (design.md **D3**).
  - `math` is already an allowed import for this module (`AGENTS.md` §Allowed Imports). Import no
    `pgtype` and no `chargingdb` here — this file is pure.
  `depends_on`: — · `parallel_ok`: yes (new file, disjoint from everything)

- [ ] **2.3** **[module: charging worker]** Create `internal/charging/validation.go`:
  - `RequiredFieldsFor(s Status) []Field`'s body: the two sets from design.md **D5**'s table,
    returned as a **fresh slice on every call**; an unrecognized status returns the `DONE`
    (strictest) set.
  - `normalizeStatus(s Status) (Status, error)`: `""` → `StatusInProgress`; `StatusInProgress` /
    `StatusDone` unchanged; anything else an error (design.md **D8**).
  - `missingFields(e Entry) []Field`: evaluates `RequiredFieldsFor(e.Status)` against the entry —
    `charged_on` present iff `!e.ChargedOn.IsZero()`, `location_kind` present iff non-nil and
    non-empty, `ended_at` present iff non-nil, `end_battery_pct` present iff non-nil. Returns the
    missing fields **in the lookup's own order**, so the error message is deterministic.
  - The error `Create`/`Update` return when the slice is non-empty: a plain error listing **every**
    missing field, e.g. `charging: status DONE requires: ended_at, end_battery_pct`. **No typed
    error struct** — explicitly rejected in design.md **D5**.
  `depends_on`: 2.1 · `parallel_ok`: with 2.2

- [ ] **2.4** **[module: charging worker]** `internal/charging/service.go` — wire it together. This
  is the join point:
  - **Create and Update, in this exact order** (design.md **D3**): normalize + validate the status
    (2.3) → enforce `missingFields` and reject listing all of them → derive energy → build params.
    Rejecting before the capacity seam matters: that seam will one day hit a database.
  - **Delete the two ad-hoc `if e.LocationKind == nil || *e.LocationKind == ""` checks**
    (`service.go:117`, `:166`) — `location_kind` is now part of `RequiredFieldsFor`, and leaving
    them would give the "single source of truth" a second source six lines above it (design.md
    **D5**).
  - **Derivation**: when `e.EnergyAddedKWh == nil`, call `packCapacityKWh(ctx, e.VIN)` then
    `derivedEnergyKWh(...)`. Non-nil result ⇒ store it with `energy_source = 'ESTIMATED'`. In every
    other case store what the caller gave (possibly `NULL`) with `energy_source = 'USER'`. **The
    caller's `e.EnergySource` is never read** (design.md **D4**).
  - **Nullable energy on the write path**: add `numericPtrFromFloat64(f *float64) (pgtype.Numeric,
    error)` beside the existing `numericFromFloat64` — `nil` → `pgtype.Numeric{Valid: false}`.
    Bind `Status: string(status)`, `EnergySource: string(source)`, `OdometerKm:
    intPtrToPgInt4(e.OdometerKm)` (a new helper beside `intPtrToPgInt2`).
  - **Nullable energy on the read path**: in `rowToEntry`, `EnergyAddedKWh` becomes
    `pgNumericToFloat64Ptr(r.EnergyAddedKwh)` — **reuse the existing helper**, do not add a second
    one. Map `Status: Status(r.Status)`, `EnergySource: EnergySource(r.EnergySource)`,
    `OdometerKm: pgInt4ToIntPtr(r.OdometerKm)`. Remove the now-dead `energyF8` block
    (`service.go:383-386`); `rowToEntry` still returns `(Entry, error)` for `Price`.
  - Update `rowToEntry`'s mapping-rules doc comment to cover the four changed/added columns.
  - `pgtype` stays confined to this file, as today.
  `depends_on`: 1.3, 2.1, 2.2, 2.3 · `parallel_ok`: no

---

## Wave 3 — tests (unit tests are INCLUDED for this change)

Every expected value is fixed in design.md §Test Contract. **Assert that contract.** Fixture
conventions: fresh `uuid.New()` account ids per test; float compare with
`math.Abs(*got-want) < 1e-9`; never `pgtype` in any assertion or helper
(`internal/charging/AGENTS.md` §Testing Notes).

- [ ] **3.1** **[module: charging worker]** Create `internal/charging/entry_status_test.go` —
  offline, no DB. Cover design.md Test Contract **A1–A5** (`RequiredFieldsFor`: both sets exactly
  and in order; **A3 as a set difference**, so the test states roadmap D5's skip-set rule rather
  than restating two literal slices; the defensive-copy property; fail-closed on an unknown
  status), **A7** (the eight-row derivation table), **A8** (rounding at capacity `62.35`, which is
  deliberately **not** `62.0` — with `62.0` the rounding is a no-op and the test would pass with
  the rounding deleted), and **A9** (`packCapacityKWh` returns exactly `62.0` for a real VIN, `""`
  and an unknown VIN).
  `depends_on`: 2.2, 2.3 · `parallel_ok`: with 3.2, 3.3, 3.4

- [ ] **3.2** **[module: charging worker]** `internal/charging/charging_test.go` — adapt to
  `*float64` at lines 21 and 40, and add design.md Test Contract **A6**: `CostPerKWh()` is `nil`
  for `EnergyAddedKWh == nil` (the new case), `nil` for `ptr(0)` (the existing guard, which must
  survive), and `≈64.516129` for `Price` 1000 / `ptr(15.5)`.
  `depends_on`: 2.1 · `parallel_ok`: with 3.1, 3.3, 3.4

- [ ] **3.3** **[module: charging worker]** Create
  `internal/charging/db_entry_status_integration_test.go` — `DATABASE_URL`-gated, mirroring
  `db_inferred_capacity_entries_integration_test.go`'s style and using the package's existing
  `testdb_test.go` pool. Cover design.md Test Contract **B1–B8** (direct SQL: the `DEFAULT`-path
  proof for pre-existing rows; the retained `CHECK (> 0)` rejecting `0` and negatives and now
  **accepting `NULL`**; the three new `CHECK`s; and `inferred_capacity_kwh_calc IS NULL` on a
  `NULL`-energy row) and **C1–C16** (through `Writer`/`Reader`: the in-progress entry, derivation
  and its `62.000` consequence, user-supplied energy, caller-supplied provenance ignored, no
  derivation on a non-positive delta or a missing percentage, both DONE rejections **with the row
  count unchanged**, a complete DONE entry, empty-status normalization, unknown-status rejection,
  odometer round-trip, and the four `Update` cases including **DONE → IN_PROGRESS being allowed**).
  Assert SQLSTATE `23514` on `CHECK` violations rather than message text.
  `depends_on`: 2.4 · `parallel_ok`: with 3.1, 3.2, 3.4

- [ ] **3.4** **[module: charging worker]** Adapt the existing integration fixtures to `*float64` —
  **mechanical, no behavioural change, and no expected value may be edited**:
  `db_integration_test.go` lines 69, 123, 238, 256 and
  `db_inferred_capacity_entries_integration_test.go` lines 128, 170, 203. Lines 238/256 set energy
  to `0` / `-5.0` to prove the `CHECK` still rejects them — they become `ptr(0)` / `ptr(-5.0)` and
  **must keep asserting rejection**. Every MAG-25 expected capacity value stays exactly as it is.
  `depends_on`: 2.1 · `parallel_ok`: with 3.1, 3.2, 3.3

---

## Wave 4 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [ ] **4.1** **[module: charging worker]** `internal/charging/AGENTS.md`:
  - §Public Interface — the `Status` / `EnergySource` / `Field` types and constants,
    `RequiredFieldsFor` and its two sets, `Entry.EnergyAddedKWh` now `*float64`, the three new
    `Entry` fields, and that `EnergySource` is module-computed and ignored when supplied.
  - §Data Ownership → `manual_charge_entries` — the three new columns, the relaxed `NOT NULL`, that
    `CHECK (energy_added_kwh > 0)` is retained and passes on `NULL`, that no index was added and
    why, and the `packCapacityKWh` seam with its `TODO(MAG-18)` / backlog #18 pointer.
  - §Testing Notes — the two new test files and what each covers, plus the owner-verification note
    that the pre-existing-row backfill is proven by the `DEFAULT` path (B1), not by a backfill test,
    for the same reason MAG-25 recorded.
  `depends_on`: 2.4 · `parallel_ok`: with 4.2

- [ ] **4.2** **[leader — outside the charging sandbox; grant the path or do it]**
  `kkpa/context/workflows/manual-charge-crud.md` — add the roadmap **D8** KB note: the 62 kWh pack
  capacity lives in `internal/charging/capacity.go` (`packCapacityKWh`), it is a placeholder pending
  a real per-vehicle value (backlog #18), and any future averaging of inferred capacities must
  filter `WHERE energy_source = 'USER'`. Also note that energy added is now optional and may be
  derived on write. **This file is outside `internal/charging/`**, so a charging worker cannot
  write it without an explicit grant.
  `depends_on`: 2.2 · `parallel_ok`: with 4.1

---

## Wave 5 — signals

- [ ] **5.1** **[module: charging worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/charging`, `go build ./...`,
  `go vet ./...`. **`go build`/`go vet` will fail in `internal/gateway` until L1 lands** (design.md
  **D11**) — that is expected and is not this module's defect. Confirm that **every remaining
  failure is in `internal/gateway`** and that `go vet ./internal/charging/...` is clean, and report
  exactly that.
  `depends_on`: 3.1, 3.2, 3.3, 3.4, 4.1 · `parallel_ok`: no

---

## Cross-module task the leader owns

- [ ] **L1** **[leader → a `gateway` worker]** The minimal mechanical compile fix for
  `Entry.EnergyAddedKWh` becoming `*float64` (design.md **D11**, proposal.md §Breaking). Sites:
  `internal/gateway/handlers/charges.go:612, 623, 798`;
  `internal/gateway/handlers/charges_test.go` (~10 fixture/assertion sites);
  `internal/gateway/handlers/charges_error_visibility_test.go:169`. Nil-guard the two formatters
  to render `—` (the project-wide empty placeholder, roadmap **D14**); take the address of the
  parsed value at `:798`; update the fixtures. **Nothing behavioural** — the real gateway work is
  roadmap tiers 2 and 3. Without this, `go build ./...` fails repo-wide and task 5.1 cannot be
  green.
  `depends_on`: 2.1 · `parallel_ok`: with Wave 3 and Wave 4

- [ ] **L2** **[leader]** Confirm the root `README.md` needs no edit. This change alters a module's
  *public surface* but adds, removes or renames no module and no runnable, so the "Project
  Structure" tree and the "Architecture" table should both already be correct. Verify rather than
  assume; `internal/charging` has no `README.md` of its own, so `AGENTS.md` (task 4.1) is the
  module-level doc.
  `depends_on`: 4.1 · `parallel_ok`: yes

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the assistant's
  say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make migrate-up
  make check
  ```
  (`make check` = `build vet ui-guard i18n-guard money-guard test`. `make test-with-db` if you want
  the `DATABASE_URL`-gated integration tests specifically.)

- [ ] **O2** **[owner]** Confirm the real backfill of pre-existing rows after `make migrate-up`.
  The package's test database is provisioned fresh with every migration applied before any row
  exists, so this cannot be a test (design.md §"Owner verification", the same limitation MAG-25
  recorded):
  ```sql
  SELECT status, energy_source, count(*), count(odometer_km) AS with_odometer
    FROM manual_charge_entries
   GROUP BY status, energy_source;
  ```
  Expected on a database migrated from before this change: exactly one row —
  `IN_PROGRESS | USER | <total> | 0`.
