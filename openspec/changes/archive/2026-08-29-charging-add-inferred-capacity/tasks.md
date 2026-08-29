# Tasks — charging-add-inferred-capacity

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only.
**[leader]** — outside every module sandbox (the root `README.md`), so a module worker may
not do it. See design.md **D1–D10** for the rationale behind each group.

> **Gate.** design.md is a `database` design gate (`CLAUDE.md` §Pipeline config). **Do not
> start task 1.1 until the owner has confirmed design.md**, and in particular has answered
> the one open item: the **result type** (design.md **D4** — unconstrained `NUMERIC` as
> specified, vs `NUMERIC(9,3)` as the documented fallback).
>
> The **column name** is settled: **`inferred_capacity_kwh_calc`** (design.md **D1** — the
> ticket's own `_calc` plus the `_kwh` the project mandates, matching
> `vehicle_metrics`' `<what>_<unit>_calc` shape). It is already written that way throughout
> these artifacts; **do not "restore" the ticket's literal `inferred_capacity_calc`.** If the
> owner overrides it at the gate anyway, it is a pure rename across the migration, both
> column comments, `charging.go`, `service.go`, `session_reader.go`, both test files and
> `AGENTS.md` — mechanical, but do it in every one of them.

**Ordering constraints:**

- **Wave 1 before everything.** `sqlc` generates against the migration directory, so the
  column must exist in a migration before `make sqlc` can see it, and the Go mapping code
  cannot reference `chargingdb`'s new field before it is generated.
- **1.1 before 1.2** — `make sqlc` runs exactly once, after the migration file exists.
- **Wave 2 before Wave 3.** The `DATABASE_URL`-gated integration tests cannot compile until
  `Entry.InferredCapacityKWhCalc` / `Session.InferredCapacityKWhCalc` exist
  (`ai/go-conventions.md` §Testing authoring order). Their expected values are already fixed
  in design.md §Test Contract — **author the tests against that contract, not against
  whatever the implementation produces.**
- **2.2 before 2.3** — `pgNumericToFloat64Ptr` lands in `service.go` (beside the other
  `pg*To*Ptr` helpers) and `session_reader.go` calls it. Same package, but the helper must
  exist first.
- **3.1 and 3.2 touch disjoint new files and may run in parallel** with each other and with
  3.3 / 3.4.

---

## Wave 1 — schema + codegen (module: charging worker)

- [x] **1.1** **[module: charging worker]** Create
  `internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql` with the DDL in
  design.md §"Database Changes" → "The migration", **verbatim, including its full header
  comment and both `COMMENT ON COLUMN` statements**. That comment block is the deliverable's
  documentation, not decoration — it carries D2 (why a generated column and not Go), D3 (why
  the strict `>` guard, and that `end = start` would otherwise be a division-by-zero that
  aborts a nightly mirror), D4 (why unconstrained `NUMERIC` and not `NUMERIC(8,3)`, with the
  overflow figure), D6 (why no index) and D9 (why the `ALTER` *is* the backfill, and why the
  `Down` is non-destructive). Points that must not be trimmed:
  - **Two expressions, not one.** They differ in exactly two ways — the
    `energy_kwh IS NOT NULL` guard and the `::NUMERIC` cast, both present only on
    `charge_sessions` — and in no other way. Do not "simplify" them into one shared shape,
    and do not add a dead `energy_added_kwh IS NOT NULL` check to `manual_charge_entries`
    (that column is `NOT NULL`; the check would falsely imply otherwise).
  - **No separate backfill `UPDATE`, no `DO $$ … $$` block, no `to_regclass` guard, no
    sentinel comments.** Unlike `20260823000001`, this migration reads nothing outside
    `internal/charging`, so none of that machinery applies (design.md **D9**).
  - **No index, no `CHECK` constraint** on either new column (design.md **D6**, and the
    migration comment's closing paragraph).
  - `-- +goose Up` / `-- +goose Down`; the `Down` drops both columns with
    `DROP COLUMN IF EXISTS`, `charge_sessions` first.
  Do **not** edit `internal/charging/db/query.sql` — design.md **D8** establishes that no
  query change is needed or permitted.
  `depends_on`: — (owner's design-gate confirmation) · `parallel_ok`: no (blocks everything)

- [x] **1.2** **[module: charging worker]** Run `make sqlc` (allowed by `CLAUDE.md`
  §"Builds & local checks") and **review the diff against design.md D8's probed
  expectations**, which is this task's acceptance criterion, not "it ran":
  - `internal/charging/db/models.go` — `ManualChargeEntry` and `ChargeSession` each gain
    **exactly one** field, `InferredCapacityKwhCalc pgtype.Numeric`, carrying the
    `COMMENT ON COLUMN` text as its doc comment.
  - `internal/charging/db/query.sql.go` — every `SELECT *` / `RETURNING *` column list and
    every corresponding `row.Scan(…)` / `rows.Scan(…)` list gains **exactly one** entry.
    That is all seven read queries plus `CreateEntry`, `UpdateEntry` and
    `VerifyChargeSession`.
  - **No `*Params` struct changes at all** — not `CreateEntryParams`, not
    `UpdateEntryParams`, not `MirrorChargeSessionParams`, not `VerifyChargeSessionParams`.
    Every write in this module names its columns explicitly, so there is structurally no
    way to bind the generated column. **If any `*Params` struct gained the field, stop and
    report it** — it means a write query was hand-edited, and the database would reject
    that write at runtime.
  - `sqlc.yaml` needs **no** change. Do not touch it.
  Report the diff shape in your final report. Then run `go build ./...` and `go vet ./...`
  (they will fail until Wave 2 wires the mapping — that is expected at this point; note it
  and move on).
  `depends_on`: 1.1 · `parallel_ok`: no

---

## Wave 2 — domain surface + mapping (module: charging worker)

- [x] **2.1** **[module: charging worker]** `internal/charging/charging.go` — add
  `InferredCapacityKWhCalc *float64` to **both** `Entry` and `Session`, each with a doc comment
  stating in full (design.md **D7**):
  - what it is (the pack capacity in kWh implied by this record alone) and the formula;
  - that it is **computed by the database and read-only** — set on the struct passed to
    `Writer.Create` / `Writer.Update` it is **silently ignored**, exactly as `ID`,
    `CreatedAt` and `UpdatedAt` already are, and the database rejects any direct write with
    `column "inferred_capacity_kwh_calc" can only be updated to DEFAULT` (SQLSTATE `428C9`);
  - that `nil` means the record's inputs did not support the formula — a missing input, an
    equal delta, or a decreasing one (design.md **D3**) — and is not an error;
  - for `Session` only: that `nil` additionally covers a session with no kWh fee
    (`EnergyKWh == nil`), and that the value recomputes both when the nightly mirror
    refreshes `energy_kwh` **and** when `SessionVerifier.VerifySession` corrects the
    percentages, without either path naming the column (design.md **D2**).
  Capitalization is `KWh`, matching this file's existing `EnergyAddedKWh` / `EnergyKWh` —
  deliberately different from sqlc's generated `InferredCapacityKwhCalc`, exactly as
  `EnergyAddedKWh` / `EnergyAddedKwh` already differ. Follow this file's conventions: no
  `pgtype`, doc comment on every exported symbol. **No interface gains a method and no
  signature changes.**
  `depends_on`: 1.2 · `parallel_ok`: no (2.2 and 2.3 both build on it)

- [x] **2.2** **[module: charging worker]** `internal/charging/service.go` —
  - add `pgNumericToFloat64Ptr(v pgtype.Numeric) *float64` beside the existing
    `pgTimestamptzToPtr` / `pgInt2ToIntPtr` / `pgTextToPtr` helpers: invalid (SQL `NULL`)
    → `nil`; otherwise the value via `Float64Value()`. Write it **non-erroring**
    (design.md **D8**): on a `Float64Value()` error return `nil`, and say in the doc comment
    that the branch is unreachable for this column — the value is a quotient of finite
    numerics, so it is finite by construction — and that the alternative, widening
    `rowToSession` to return an `error`, would ripple through `session_reader.go`,
    `session_verifier.go` and their callers for an unreachable branch.
  - wire it into `rowToEntry`: `InferredCapacityKWhCalc: pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc)`,
    and add the corresponding line to that function's "Mapping rules" doc-comment list.
  Do **not** convert the existing `EnergyAddedKwh` / `Price` mappings to the new helper —
  those columns are `NOT NULL` and their current erroring path is correct for them.
  `depends_on`: 2.1 · `parallel_ok`: no (2.3 needs the helper)

- [x] **2.3** **[module: charging worker]** `internal/charging/session_reader.go` — wire the
  helper into `rowToSession`:
  `InferredCapacityKWhCalc: pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc)`. Do **not** duplicate
  the helper here; do **not** change `rowToSession`'s signature. `session_verifier.go` and
  `session_writer.go` need no edit — `VerifySession` already maps its `RETURNING *` row
  through `rowToSession`, so it picks the field up for free. Then run `go build ./...`,
  `go vet ./...` and `gofmt -l` and report the results; all three must be clean at this
  point.
  `depends_on`: 2.2 · `parallel_ok`: no

---

## Wave 3 — tests + docs (module: charging worker)

- [x] **3.1** **[module: charging worker]**
  `internal/charging/db_inferred_capacity_entries_integration_test.go` (new file, `package
  charging_test`) — implement design.md §Test Contract **Group A (T1–T12) and T24**, with
  those exact expected values. Points that decide whether this test is right:
  - **T2 asserts `66.629` and T3 asserts `73.620`.** The ticket's prose says "66.63" and
    "73.63"; those are its own 2-decimal restatements. **Do not "correct" the expectations
    to match the ticket's prose** — design.md §"What must NOT change" says so explicitly.
    T3 is `73.620` (not `73.624`) because `energy_added_kwh` is `NUMERIC(6,2)` and cannot
    store the ticket's `52.273`.
  - **T7, T8 and T10 must assert a successful `Create`**, not just the resulting value.
    T7/T8 prove the guard prevents a `division_by_zero` / a stored negative; T10 proves the
    column's type does not reject a legal max-energy/min-delta row. They are the
    regression tests for design.md **D3** and **D4** — if the type is ever narrowed or the
    guard weakened, these are what catch it.
  - Seed via `charging.NewWriter(pool).Create`; read back via `Reader.ListEntriesByVehicle`.
    Fresh `uuid.New()` account ids per test.
  - **T12** needs direct SQL (`UPDATE manual_charge_entries SET inferred_capacity_kwh_calc = 1
    WHERE id = $1`) since no port can express it; assert the error is non-nil and match on
    SQLSTATE `428C9` rather than the message text.
  - Compare `*float64` with `math.Abs(*got - want) < 1e-9`, never `==`. Assert `nil`
    explicitly for every `NULL` case — never a zero.
  - **No `pgtype` in any assertion or helper** (`internal/charging/AGENTS.md` §Testing
    Notes).
  `depends_on`: 2.3 · `parallel_ok`: with 3.2, 3.3, 3.4

- [x] **3.2** **[module: charging worker]**
  `internal/charging/db_inferred_capacity_sessions_integration_test.go` (new file, `package
  charging_test`) — implement design.md §Test Contract **Group B (T13–T23)**, with those
  exact expected values. Points that decide whether this test is right:
  - **T20 is the load-bearing case of the whole change.** Mirror a session with
    `EnergyKWh = 41.31` and no percentages (assert `InferredCapacityKWhCalc == nil`), then call
    `SessionVerifier.VerifySession(ctx, accountID, id, ptr(18), ptr(80))` and assert
    **`66.629`** — **on the `charging.Session` that `VerifySession` itself returns**, so the
    `RETURNING *` freshness is proven too. Nothing in Go computes that number and
    `VerifyChargeSession`'s `SET` clause never names the column; the value appears only
    because the engine recomputed it. This is the direct proof of design.md **D2**.
  - **T21** re-mirrors the same `(account_id, session_id)` with `EnergyKWh = 44.64` and
    asserts **`72.000`** — the nightly `ON CONFLICT DO UPDATE SET` refresh path.
  - **T13 asserts `73.624`**, not the ticket's "73.63" (see 3.1's note). It also proves the
    `float8 → numeric` cast keeps the shortest round-trip decimal.
  - **T19 must assert `MirrorSessions` SUCCEEDS** with `EnergyKWh = 1e9`, `0 → 1%`. Under any
    fixed `NUMERIC` precision this row would abort the whole mirror transaction — RM29's
    contract is that one bad entry rejects the entire call. This is design.md **D4**'s
    regression test for this table.
  - **Percentages cannot be seeded through `MirrorSessions`** — `SessionMirror` has no
    fields for them, by RM29 D6 design, and that is correct and must not be "fixed". Mirror
    first, then set percentages via `SessionVerifier.VerifySession` (one `nil` argument for
    T15/T16); skip the verify step entirely for T14's no-percentage case.
    **[LEADER CORRECTION, 2026-08-29 — this bullet was wrong; design.md governs.]**
    design.md's Test Contract lists **T14 as `energy_kwh = NULL` with percentages 29 → 100
    set** — there is no no-percentage row among T13–T19, so T14 *does* need the verify step.
    That is deliberate: it isolates the "no kWh fee" guard from the missing-percentage
    guards. Implemented per design.md. No expected value changed.
  - `session_id`s in the **960001–960099** range, disjoint from RM29's 920001–920099,
    RM30's 940001–940099, RM31's 950001–950099 and the real backfilled `734860294`.
  - **T22** needs direct SQL, same shape as T12. **T23** reads through
    `SessionReader.ListSessionsByVehicleBetween`.
  - Same float-tolerance, explicit-`nil` and **no-`pgtype`** rules as 3.1.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 3.3, 3.4

- [x] **3.3** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's new surface (docs-track-structural-change, `CLAUDE.md` §Non-negotiables):
  - **§Units convention** — add `inferred_capacity_kwh_calc` to the compliant-column list,
    **and write down the naming rule it follows**, which is the durable half of this task.
    State that a *stored, derived* column in this project is named `<what>_<unit>_calc` —
    unit suffix first, `_calc` last — cite `internal/analytics/vehicle_metrics`' five
    columns as the precedent
    (`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql:67-71`), and
    note explicitly that **`ai/go-conventions.md` documents the unit-suffix half but never
    mentions `_calc`**, which is why this column's name had to be derived from code rather
    than read from a doc. Record that this is why the ticket's literal
    `inferred_capacity_calc` was not used: it is the same convention, missing the mandatory
    unit segment (design.md **D1**). The point is that the next agent naming a derived
    column reads the rule here instead of re-deriving it from `vehicle_metrics`.
  - **§Public Interface** — add `InferredCapacityKWhCalc *float64` to the documented `Session`
    struct, and note the field on `Entry` under the `Reader`/`Writer` block. State plainly
    that it is **database-computed and read-only**: ignored on `Create`/`Update`, and
    physically unwritable. **Do not** add it to `SessionMirror` — that type stays
    percentage-free and now capacity-free, and there is nothing for a mirror to supply.
  - **§Data Ownership** — for **both** tables, record the new column and its guard (present
    only when all inputs exist and `end > start`; `NULL` otherwise). For `charge_sessions`,
    add it to the column-by-column list as a **fourth category** alongside
    mirrored-write-once / mirrored-refreshed / charging-owned: *engine-generated — written
    by nobody, recomputed automatically whenever `energy_kwh`, `start_battery_pct` or
    `end_battery_pct` changes, through either the mirror or the verifier.* Note that the
    RM29 "protection by compile error" pattern is here strengthened to protection by the
    database itself.
  - **§Testing Notes** — list the two new integration test files alongside the existing
    ones, and note that the backfill-of-existing-rows property is verified by the owner on
    the real database rather than by a test, with design.md **D10**'s reason (the suite
    provisions a fresh database, so there is nothing in it to backfill).
  `depends_on`: 2.1 · `parallel_ok`: with 3.1, 3.2, 3.4

- [x] **3.4** **[leader]** Root `README.md` §"Database tables by module" — its rows describe
  each table's columns, and both `internal/charging` rows go stale with this change.
  README's own §"Making a change" table makes this mandatory for a new column: *"add the
  table to the README **Database tables by module** list in the same change."* Add one clause
  to the `manual_charge_entries` row and one to the `charge_sessions` row, each saying the
  new column is the database-computed inferred pack capacity in kWh, `NULL` when the
  record's inputs do not support the formula. **This file is outside every module sandbox**
  — a `charging` worker may not edit it, which is why it is a leader task. Nothing else in
  `README.md` changes: no module is added, removed, renamed or re-scoped, so the "Project
  Structure" tree, the "Architecture" table and the dependency graph all stay accurate. **No
  `Makefile` change either** — `internal/charging` is already in `MIGRATIONS_DIRS` (that
  clause applies only to a module's *first* table).
  `depends_on`: 1.1 · `parallel_ok`: with 3.1, 3.2, 3.3

---

## Owner verification (not automatable — design.md D10)

Two things only the owner can do. Neither is a worker task and neither may be reported as
`done` by an assistant.

- [x] **V1 — apply the migration.** `make migrate-up` (or `make db-setup`) against the real
  database.
- [x] **V2 — confirm existing rows were backfilled**, which is the ticket's acceptance
  criterion's second half. Paste:
  ```sql
  SELECT count(*) FILTER (WHERE inferred_capacity_kwh_calc IS NOT NULL) AS computed,
         count(*) FILTER (WHERE inferred_capacity_kwh_calc IS NULL)     AS null_by_guard,
         count(*)                                                  AS total
  FROM manual_charge_entries;

  SELECT count(*) FILTER (WHERE inferred_capacity_kwh_calc IS NOT NULL) AS computed,
         count(*) FILTER (WHERE inferred_capacity_kwh_calc IS NULL)     AS null_by_guard,
         count(*)                                                  AS total
  FROM charge_sessions;

  -- spot-check the actual figures against your own knowledge of the car:
  SELECT charged_on, energy_added_kwh, start_battery_pct, end_battery_pct,
         inferred_capacity_kwh_calc
  FROM manual_charge_entries
  WHERE inferred_capacity_kwh_calc IS NOT NULL
  ORDER BY charged_on DESC LIMIT 10;
  ```
  Expect `null_by_guard` to equal exactly the rows missing a percentage, missing energy
  (sessions only), or with a non-increasing delta — **not** an arbitrary number. A `computed`
  count of zero on a table that has such rows means the migration did not do what design.md
  **D9** reproduced, and is a stop-and-report.
- [x] **V3 — run the suite.** The exact commands are in §"Handing back" below.

---

## Handing back — the suite the assistant does not run

Per `CLAUDE.md` §"Builds & local checks" and the `Test-Execution-Policy`: the assistant runs
`go build ./...`, `go vet ./...`, `gofmt -l` and the standalone guards; **the owner runs the
suite.** Until the owner reports it, every task above whose tests exist but were not executed
is **`awaiting-user-verification`**, never `done`.

```bash
make check          # build vet ui-guard i18n-guard money-guard test
# or, for just this change's coverage:
go test ./internal/charging/... -run 'InferredCapacity' -v
```

The integration tests are `DATABASE_URL`-gated and otherwise start a disposable
`postgres:16-alpine` via testcontainers, so they need either `DATABASE_URL` set or a running
Docker daemon (`internal/charging/testdb_test.go`).

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast.

- **Any edit to `internal/charging/db/query.sql`.** design.md **D8** proved every read is
  `SELECT *` / `RETURNING *` (sqlc expands them automatically) and every write names its
  columns explicitly (so nothing can bind the column). Adding the column to a write query
  makes that write **fail at runtime** — the database rejects it.
- **Any new query or any new port method.** No `SessionReader`, `Reader`, `Writer`,
  `SessionWriter`, `SessionVerifier` or `SuperchargerSessionAnalyticsReader` signature
  changes. No "list entries by inferred capacity", no aggregate query.
- **Any index on either new column**, or any change to the three existing indexes.
  design.md **D6** states the reason; adding one anyway re-trips the `database` design gate
  without the owner's sign-off.
- **A `CHECK` constraint on either new column.** Unreachable on `manual_charge_entries` and
  actively wrong on `charge_sessions` (RM29 D1: a mirror is never stricter than its source).
- **A minimum-delta floor in the expression.** design.md §Risks item 1 flags it for the
  owner; it is not decided, so it is not built.
- **A separate backfill `UPDATE`, a `DO $$ … $$` block, a `to_regclass` guard, or sentinel
  comments in the migration.** design.md **D9** — the `ALTER` is the backfill, and this
  migration reads nothing outside `internal/charging`.
- **A trigger, a view, or a Go-side computation** of this value, in addition to or instead
  of the generated column. All three were considered and rejected in design.md **D2**.
- **Adding the field to `charging.SessionMirror`.** The mirror path has no value to supply
  and must have no way to supply one — RM29 D6's invariant, now reinforced by the database.
- **Any per-vehicle aggregate, rollup, summary table, or `internal/analytics` metric**, and
  any `internal/gateway` handler, route, template, i18n key or UI. design.md **D5** — the
  acceptance criterion names exactly one deliverable. Follow-on work is enumerated in
  proposal.md §"Follow-on work" for the backlog.
- **Any change to `internal/telemetry.supercharger_sessions`** — another module's table,
  another module's sandbox, and not named by the ticket.
- **Any change to `sqlc.yaml`.** The existing `charging` entry and its `uuid` override
  already cover this; `NUMERIC` maps to `pgtype.Numeric` exactly as `energy_added_kwh` and
  `price` already do.
- **Running `go test ./...`, `make test`, `make test-with-db` or `make check`.**
  `Test-Execution-Policy` — the owner runs the suite.
