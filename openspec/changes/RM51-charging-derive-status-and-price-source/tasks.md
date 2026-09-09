# Tasks — RM51-charging-derive-status-and-price-source

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only.
**[leader]** — outside the module sandbox, so a module worker may not do it. **[owner]** — the
human. See design.md **D1–D7** for the rationale behind each group.

> ## Gate — do not start 1.1
>
> design.md is a `database` design gate (`CLAUDE.md` §Pipeline config). **No task below starts
> until the owner has confirmed design.md**, in particular:
>
> 1. **The promotion placement** — design.md **D1**: after `normalizeStatus`, before
>    `missingFields`, so an entry invalid outright is rejected before promotion is considered.
> 2. **The re-promotion-on-reopen interaction** — design.md **D1**/Risk 1: "reopening" a `DONE`
>    entry without clearing an end-of-session field gets immediately re-promoted back to `DONE`.
> 3. **No index on `price_source`** — design.md **D4**/§Index Plan, including the partial index
>    offered and declined at the interview.

## Ordering constraints

- **Wave 1 is a serialization point and blocks everything.** sqlc generates against the migration
  directory *and* `query.sql`, so both must be final before `make sqlc` runs, and no Go file can
  reference `chargingdb`'s new field before it is generated. Nothing in Wave 2+ can start early.
- **1.1 and 1.2 touch disjoint files** and may be done in either order; **1.3 needs both.**
- **2.1, 2.2, and 2.3 touch different files** (`charging.go`, `validation.go`, `service.go`
  respectively) but 2.3 depends on the types 2.1 declares and the helper 2.2 declares, so 2.3
  runs last within Wave 2.
- **Wave 3's tests cannot compile before Wave 2** (`ai/go-conventions.md` §Testing authoring
  order). Their expected values are already fixed in design.md §Test Contract — **author them
  against that contract, not against whatever the implementation produces.**
- **3.1, 3.2, and 3.3 touch disjoint files** and may run in parallel with each other.
- **This tier has no cross-module compile-fix task** (contrast RM33's `L1`/`L1b`): every change
  is additive (proposal.md §Breaking), so `go build ./...`/`go vet ./...` stay green outside
  `internal/charging` throughout.

---

## Wave 1 — schema + codegen (serialization point)

- [x] **1.1** **[module: charging worker]** Create
  `internal/charging/db/migrations/20260909000001_add_price_source.sql` with the DDL in
  design.md §"Database Changes" → "The migration", **verbatim, including its full header comment
  and the `COMMENT ON COLUMN` statement**. That comment block carries D2 (why two statements, why
  TEXT+CHECK, why `UNCONFIRMED` as the default, why the backfill splits on `price > 0`) and D7
  (why the `Down` is lossy but unguarded). Points that must not be trimmed:
  - **One `ADD COLUMN ... DEFAULT 'UNCONFIRMED' ... CHECK (...)` statement, plus one separate
    `UPDATE ... WHERE price > 0` statement.** Two statements, not one — do not "simplify" this
    into a single `ADD COLUMN` the way `20260829000002` could, because the backfill value here is
    not uniform across rows (design.md D2).
  - **No index, on `price_source`** (design.md §Index Plan). Do not add the partial index that
    was offered and declined at the interview.
  - **Do not touch `status`, `energy_source`, `odometer_km`, or any `supercharger_sessions`
    column.** This migration names `manual_charge_entries` and only the one new column.
  - The `Down` is a single `ALTER TABLE ... DROP COLUMN IF EXISTS price_source` — **no guard
    block**, unlike `20260829000002`'s `Down` (design.md D7). Do not add one; there is no
    impossible state here to guard against.
  `depends_on`: the owner's design-gate confirmation · `parallel_ok`: with 1.2

- [x] **1.2** **[module: charging worker]** Edit `internal/charging/db/query.sql`:
  - `CreateEntry` — add `price_source` to the column list and `@price_source` to the `VALUES`.
  - `UpdateEntry` — add `price_source = @price_source` to the `SET` clause.
  - Add a short comment on each explaining that `price_source` is **computed by this module and
    never accepted from a caller** (design.md D2/D3), pointing at the `energy_source` precedent
    already commented in this same file.
  - **Change no read query.** All four are `SELECT *` and sqlc expands them automatically. Do not
    add a `WHERE price_source = …` anywhere; nothing predicates on the new column (§Index Plan).
  - **Change no `supercharger_sessions` query** — `MirrorSuperchargerSession`,
    `ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`,
    and `VerifySuperchargerSession` are outside this change entirely.
  `depends_on`: — · `parallel_ok`: with 1.1

- [x] **1.3** **[module: charging worker]** Run `make sqlc` (allowed by `CLAUDE.md` §"Builds &
  local checks") and **review the diff against design.md §"Expected sqlc diff"**, which is this
  task's acceptance criterion — not "it ran":
  - `db/models.go` — `ManualChargeEntry` gains **exactly one** field: `PriceSource string`.
    `SuperchargerSession` must be untouched.
  - `db/query.sql.go` — `CreateEntryParams` and `UpdateEntryParams` each gain the same one field;
    every `SELECT *` / `RETURNING *` column list and `Scan(…)` list gains one entry.
  - **No other `*Params` struct changes** — not `MirrorSuperchargerSessionParams`, not
    `VerifySuperchargerSessionParams`, not any `List…Params`. **If one did, stop and report it.**
  - `sqlc.yaml` needs **no** change. Do not touch it.
  Report the diff shape in your final report. `go build ./...` will fail until Wave 2 — expected
  at this point; note it and move on.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: no (blocks Wave 2)

---

## Wave 2 — domain surface, validation, service wiring

- [x] **2.1** **[module: charging worker]** `internal/charging/charging.go` — the domain surface:
  - Add the exported `PriceSource` type and its two constants (`PriceSourceUser`,
    `PriceSourceUnconfirmed`), doc comment from design.md **D3** — "ALWAYS COMPUTED BY
    internal/charging on Create/Update -- a value set on the Entry passed to Writer is ignored
    and overwritten," mirroring `EnergySource`'s existing comment shape.
  - Add `PriceSource PriceSource` and `PriceConfirmed bool` to `Entry`, each with the doc comment
    from design.md **D3**. `PriceConfirmed`'s comment must state it is read only when `Price ==
    0`, ignored when `Price > 0` (design.md **D5**), and **not persisted directly** — a round-trip
    through `Reader` always returns `PriceConfirmed: false`.
  - **No interface gains, loses, or re-signs a method.** `Writer`, `Reader`, `SessionWriter`,
    `SessionReader`, `SuperchargerSessionAnalyticsReader`, and `SessionVerifier` are untouched, as
    are `Status`, `EnergySource`, `Field`, `RequiredFieldsFor`'s signature, `Session`, and
    `SessionMirror`.
  - Update `Entry`'s own doc comment and the package doc comment: the module now also records a
    price provenance and may auto-promote a complete entry to `DONE`.
  `depends_on`: 1.3 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: charging worker]** `internal/charging/validation.go` — add
  `promoteIfComplete(e Entry) Entry`, the full function from design.md **D1**, verbatim including
  its doc comment (the reuse of `missingFields`/`RequiredFieldsFor(StatusDone)`, the never-demote
  guarantee, and the call-site ordering it depends on). Place it beside `missingFields` in this
  file — it is a validation-adjacent helper, not a new file, since it directly composes
  `missingFields`.
  `depends_on`: 1.3 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: charging worker]** `internal/charging/service.go` — wire both rules
  into `Create` and `Update`. This is the join point:
  - **In both methods, immediately after `normalizeStatus` succeeds and before `missingFields`
    is called**, insert `e = promoteIfComplete(e)` (design.md **D1**, exact placement).
  - Add `resolvePriceSource(e Entry) PriceSource`, the full function from design.md **D3**,
    verbatim including its doc comment. Call it in both `Create` and `Update` when building the
    params — alongside where `price` is already encoded into `numericFromFloat64`. **The caller's
    own `e.PriceSource` is never read.**
  - Bind `PriceSource: string(resolvePriceSource(e))` on both `CreateEntryParams` and
    `UpdateEntryParams`.
  - In `rowToEntry`, map `PriceSource: PriceSource(r.PriceSource)`. **Do not set
    `PriceConfirmed`** on the mapped `Entry` — it stays the Go zero value `false` on every read,
    per design.md **D3**'s "not persisted directly."
  - Update `rowToEntry`'s mapping-rules doc comment to cover the new column.
  - `pgtype` stays confined to this file, as today. No new `pgtype` usage is needed for
    `price_source` — it is a plain, non-nullable `TEXT` column, mapped as a plain string exactly
    like `Status`/`EnergySource` already are.
  `depends_on`: 1.3, 2.1, 2.2 · `parallel_ok`: no

---

## Wave 3 — tests (unit tests are included, per `internal/charging/AGENTS.md` §Testing Notes)

Every expected value is fixed in design.md §Test Contract. **Assert that contract.** Fixture
conventions: fresh `uuid.New()` account ids per test; never `pgtype` in any assertion or helper
(`internal/charging/AGENTS.md` §Testing Notes).

- [ ] **3.1** **[module: charging worker]** Extend `internal/charging/entry_status_test.go` —
  offline, no DB. Cover design.md Test Contract **A1–A6**: `promoteIfComplete`'s promotion case,
  its two single-missing-field blocking cases (`ended_at`, `end_battery_pct`), its two
  full-DONE-set blocking cases (`charged_on`, `location_kind` — proving the helper reuses the
  **full** `RequiredFieldsFor(StatusDone)` set, not a hardcoded two-field list), and the
  never-demote guard on a `DONE`-status input.
  `depends_on`: 2.2 · `parallel_ok`: with 3.2, 3.3

- [ ] **3.2** **[module: charging worker]** Create `internal/charging/price_source_test.go` —
  offline, no DB, package `charging` (mirrors `entry_status_test.go`'s package choice, since
  `resolvePriceSource` is unexported). Cover design.md Test Contract **A7–A10**: the three RD3
  rule-table branches plus the precedence case (a positive price wins regardless of
  `PriceConfirmed`).
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 3.3

- [ ] **3.3** **[module: charging worker]** Create
  `internal/charging/db_promotion_price_source_integration_test.go` — `DATABASE_URL`-gated,
  mirroring `db_entry_status_integration_test.go`'s style and using the package's existing
  `testdb_test.go` pool. Cover design.md Test Contract **B1–B3** (direct SQL: the `DEFAULT`-path
  proof for a row that never names the column; the `CHECK` rejecting an invalid `price_source`;
  and the proof that the `DEFAULT` is price-blind — a raw insert with a positive price still
  defaults to `UNCONFIRMED`) and **C1–C11** (through `Writer`/`Reader`: all three `price_source`
  branches, the precedence case, that a caller cannot set `PriceSource` directly, that it is
  recomputed — not sticky — on `Update`, promotion firing through both `Create` and `Update`, the
  empty-status-then-promote path, that an incomplete explicit `DONE` submission is still rejected,
  and the re-promotion-on-reopen interaction). Assert SQLSTATE `23514` on the `CHECK` violation
  rather than message text.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 3.2

---

## Wave 4 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [ ] **4.1** **[module: charging worker]** `internal/charging/AGENTS.md`:
  - §Public Interface — the `PriceSource` type and its two constants, the two new `Entry` fields
    (`PriceSource`, `PriceConfirmed`), and that `PriceSource` is module-computed and ignored when
    supplied. Note the promotion rule (`Writer.Create`/`Update` auto-promote a complete
    `IN_PROGRESS` entry to `DONE`) beside the existing `RequiredFieldsFor` documentation, since it
    changes when an entry is stored as `DONE` without changing the required-field rule itself.
  - §Data Ownership → `manual_charge_entries` — the new `price_source` column: its `CHECK`, its
    default, that it is not indexed and why, and that the DEFAULT alone does not implement the
    price-based rule (design.md D2).
  - §Testing Notes — the three new/extended test files and what each covers.
  `depends_on`: 2.3 · `parallel_ok`: with 4.2

- [ ] **4.2** **[leader — outside the charging sandbox; grant the path or do it]**
  - `kkpa/context/workflows/manual-charge-crud.md` — add a note for the promotion rule (a complete
    `IN_PROGRESS` entry is auto-promoted to `DONE` on both write paths, per design.md D1) beside
    the existing "Required fields depend on the entry's status" note, and a note for the
    `price_source` rule (design.md D2/D3) beside the existing "Energy added is OPTIONAL" note.
  - `kkpa/context/INDEX.md` — add one new glossary row for `price source` →
    `charging.PriceSource` (`USER` / `UNCONFIRMED`) / `manual_charge_entries.price_source` —
    module-computed, never caller-supplied, pointing at `workflows/manual-charge-crud.md`,
    mirroring the existing `energy source` row's shape exactly. Verify the existing `entry status`
    and `energy source` rows still read correctly after this change (they should — this tier adds
    a rule, it does not change what those two rows already describe); update them only if reading
    them against the new behaviour finds them stale.
  - **Never touch `openspec/changes/archive/`** — both files above are outside it. This file is
    outside `internal/charging/`, so a charging worker cannot write it without an explicit grant.
  `depends_on`: 2.3 · `parallel_ok`: with 4.1

---

## Wave 5 — signals

- [ ] **5.1** **[module: charging worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/charging`, `go build ./...`,
  `go vet ./...`. **Unlike RM33, `go build ./...` and `go vet ./...` should be clean repo-wide**
  after this tier (proposal.md §Breaking — every change is additive). If either fails outside
  `internal/charging`, stop and report it rather than assuming it is expected; it is not.
  `depends_on`: 3.1, 3.2, 3.3, 4.1 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the assistant's
  say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make migrate-up
  make check
  ```
  (`make check` = `build vet ui-guard i18n-guard money-guard tz-guard migration-guard
  boundary-guard archive-guard test`. `make test-with-db` if you want the `DATABASE_URL`-gated
  integration tests specifically.)

- [ ] **O2** **[owner]** Confirm the real backfill of pre-existing rows after `make migrate-up` —
  the package's test database is provisioned fresh with every migration applied before any row
  exists, so this cannot be a test (design.md §"Owner verification", the same limitation RM33
  recorded for its own backfill):
  ```sql
  SELECT price_source, count(*), count(*) FILTER (WHERE price = 0) AS zero_price_rows
    FROM manual_charge_entries
   GROUP BY price_source;
  ```
  Expected on a database migrated from before this change: the `USER` group's `zero_price_rows`
  is `0`, and the `UNCONFIRMED` group's `zero_price_rows` equals that group's total.

## Cross-module tasks the leader owns

- [ ] **L1** **[leader]** Confirm `go build ./...`/`go vet ./...` are green **outside**
  `internal/charging` once Wave 5 lands. Proposal.md §Breaking states no cross-module compile fix
  should be needed — verify rather than assume, the same way RM33's leader was asked to verify the
  `analytics` module rather than trust a grep.
- [ ] **L2** **[leader]** Confirm the root `README.md` needs no edit. This change alters a
  module's public surface but adds, removes, or renames no module and no runnable, so the
  "Project Structure" tree and the "Architecture" table should already be correct.
  `depends_on`: 4.1 · `parallel_ok`: yes
