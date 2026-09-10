# Tasks — RM52-charging-add-monthly-effective-capacity

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only. **[owner]** —
the human. This tier has no task outside the `charging` sandbox. See design.md **D1–D8** for the
rationale behind each group.

> ## Gate — do not start 1.1
>
> design.md is a `database` design gate (`CLAUDE.md` §Pipeline config). **No task below starts
> until the owner has confirmed design.md**, in particular:
>
> **The gate has been passed. The owner confirmed design.md on 2026-09-10.** The four items below
> are recorded so an implementer knows what was settled and does not re-open any of them.
>
> 1. **The DDL itself** — confirmed at the roadmap's own gate (roadmap RD11), re-confirmed when the
>    table moved from `analytics` to `charging`, and amended once more to add `candidate_count`.
> 2. **D1 — two counts, and both are deliberate.** `candidate_count` is counted BEFORE the delta
>    gate, `sample_count` AFTER it (roadmap RD11 / RD15). This began as a design.md interpretation
>    of RD3+RD4; the owner confirmed the post-gate meaning and added the pre-gate column so a
>    `NULL` month can explain itself. **Do not collapse the two counts into one.**
> 3. **No new index for the two batch-read queries** — design.md §Index Plan, including the
>    revisit trigger.
> 4. **D2 — a `tesla_id` with zero valid rows in a period gets no row at all.** Combined with D1
>    this means a stored row always has `candidate_count >= 1`; `candidate_count = 0` is never
>    written.

## Ordering constraints

- **Wave 1 is a serialization point and blocks everything.** sqlc generates against the migration
  directory *and* `query.sql`, so both must be final before `make sqlc` runs, and no Go file can
  reference the new `chargingdb` types before they are generated. Nothing in Wave 2+ can start
  early.
- **1.1, 1.2, and 1.3 touch disjoint files** (the migration, `query.sql`, `sqlc.yaml`) and may be
  done in any order; **1.4 needs all three.**
- **Wave 2 builds the domain surface and the two updated seams.** 2.1 (`capacity.go`) and 2.2
  (`charging.go`) touch disjoint files and may run in parallel. 2.3 (`service.go`) and 2.4
  (`session_verifier.go`) each depend on 2.1's `packCapacityLookup`/`packCapacityKWh` signature and
  touch disjoint files from each other, so they may run in parallel once 2.1 lands. 2.5 (the job,
  `monthly_capacity.go`) depends on 2.2's interface declaration and touches a file nothing else in
  Wave 2 touches, so it may run in parallel with 2.3/2.4.
- **Wave 3's tests cannot compile before Wave 2** (`ai/go-conventions.md` §Testing authoring
  order). Their expected values are already fixed in design.md §Test Contract — **author them
  against that contract, not against whatever the implementation produces.**
- **3.1 and 3.2 touch disjoint files** and may run in parallel.
- **This tier has no cross-module compile-fix task.** Every change is additive or confined to
  unexported functions inside `internal/charging` (proposal.md §Breaking): `go build ./...`/`go vet
  ./...` are expected to stay green outside this module throughout.

---

## Wave 1 — schema + codegen (serialization point)

- [x] **1.1** **[module: charging worker]** Create
  `internal/charging/db/migrations/20260909000002_add_monthly_effective_capacity.sql` with the DDL
  in design.md §"Database Changes" → "The migration", **verbatim, including its full header
  comment, both `COMMENT ON` statements, and the Index Plan comment.** Points that must not be
  trimmed:
  - **Schema-qualified `charging.monthly_effective_capacity`** — this module's tables live in the
    `charging` schema (`RM39-charging-move-to-own-schema`), not `public`.
  - **No `account_id` column, no FK on `tesla_id`, no `raw_data` JSONB** — all three omissions are
    deliberate (design.md's migration header, RD5).
  - **The `CHECK` constraint name is exactly `monthly_effective_capacity_period_is_month_start`**
    — Test Contract B1 asserts against this constraint's behaviour (SQLSTATE, not name), but a
    consistent name matters for `internal/charging/AGENTS.md` §Data Ownership's own
    "verify with `pg_constraint`" convention this module already follows.
  - **No separate `CREATE INDEX`** — the `UNIQUE (tesla_id, effective_period)` constraint's own
    btree is the whole index plan (design.md, roadmap RD11).
  - Confirm before writing: `20260909000002` collides with no file anywhere under
    `internal/*/db/migrations/` (design.md "Verified: no migration-version collision" already did
    this once; re-run the check as this task's own acceptance step, since Wave 1 may execute after
    other branches merge).
  `depends_on`: the owner's design-gate confirmation · `parallel_ok`: with 1.2, 1.3

- [x] **1.2** **[module: charging worker]** Edit `internal/charging/db/query.sql`: append the four
  new queries from design.md §"Query changes" **verbatim** —
  `ListValidManualEntryCapacitiesForPeriod`, `ListValidSessionCapacitiesForPeriod`,
  `UpsertMonthlyEffectiveCapacity`, `LatestMeasuredCapacity` — each with its full comment. Then
  edit the existing `LockSessionForVerification` query to add `tesla_id` to its `SELECT` list,
  between `vin` and `energy_kwh`, per design.md's shown diff. **Change no other existing query** —
  not `MirrorSuperchargerSession`, not `VerifySuperchargerSession`, not any `List…` query on either
  existing table.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.3

- [x] **1.3** **[module: charging worker]** Edit `sqlc.yaml`: add
  `charging_monthly_effective_capacity: "MonthlyEffectiveCapacity"` to the existing `charging`
  module's `rename:` block (design.md §"sqlc.yaml"), in the same block as the other three
  `charging_*` entries. **Change no other line, no other module's block.**
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2

- [x] **1.4** **[module: charging worker]** Run `make sqlc` (allowed by `CLAUDE.md` §"Builds &
  local checks") and **review the diff against design.md §"Expected sqlc diff"**, which is this
  task's acceptance criterion — not "it ran":
  - `db/models.go` gains exactly one new struct, `MonthlyEffectiveCapacity`. `ManualChargeEntry`,
    `SuperchargerSession`, `MirrorWatermark` are untouched.
  - `db/query.sql.go` gains the four new query methods and their `Params`/`Row` types listed in
    design.md, plus exactly one new field (`TeslaID pgtype.Int8`) on
    `LockSessionForVerificationRow`.
  - **No other `*Params` or `*Row` type changes anywhere in the file.** If one changed, stop and
    report it — it means a query outside this change's scope was edited.
  Report the diff shape in your final report. `go build ./...` will fail until Wave 2 — expected
  at this point; note it and move on.
  `depends_on`: 1.1, 1.2, 1.3 · `parallel_ok`: no (blocks Wave 2)

---

## Wave 2 — domain surface, the two updated seams, the job

- [x] **2.1** **[module: charging worker]** `internal/charging/capacity.go`:
  - Add `const defaultPackCapacityKWh = 62.0` (design.md D4).
  - Add the `packCapacityLookup` interface (design.md D3), verbatim including its doc comment.
  - Replace `packCapacityKWh`'s body and signature with design.md D4's version:
    `packCapacityKWh(ctx context.Context, lookup packCapacityLookup, teslaID int64) (float64,
    error)`. Remove the `TODO(MAG-18)` comment block entirely — this task closes backlog #18
    (roadmap RD1).
  - `derivedEnergyKWh` and `derivedStartBatteryPct` are **untouched** — do not alter their
    signature, body, or doc comment.
  - **This file gains no new import.** `packCapacityLookup` and `packCapacityKWh`'s new body use
    only `context`, `fmt` (new — for the wrapped error), and the package-local
    `defaultPackCapacityKWh`/`packCapacityLookup`. No `chargingdb`, no `pgtype`.
  `depends_on`: 1.4 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: charging worker]** `internal/charging/charging.go`:
  - Add `MonthlyCapacityReport` and the `MonthlyCapacityCalculator` interface, verbatim from
    design.md D7, with their doc comments.
  - Add `NewMonthlyCapacityCalculator(pool *pgxpool.Pool) MonthlyCapacityCalculator` as a forward
    declaration (body in `monthly_capacity.go`, mirroring every other constructor's split between
    this file and its implementation file).
  - **No existing interface, type, or constructor in this file changes.** `Writer`, `Reader`,
    `SessionWriter`, `SessionReader`, `SuperchargerSessionAnalyticsReader`, `SessionVerifier`,
    `MirrorWatermarkStore`, `Status`, `EnergySource`, `PriceSource`, `Field`, `Entry`, `Session`,
    `SessionMirror` are all untouched.
  `depends_on`: 1.4 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: charging worker]** `internal/charging/service.go`:
  - Add `latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error)` to the
    `store` interface, with its doc comment (design.md D5).
  - Implement it on `dbStore`, verbatim from design.md D5, including the `pgx.ErrNoRows`
    translation.
  - Change `resolveEnergy`'s signature to `resolveEnergy(ctx context.Context, s store, e Entry)
    (*float64, EnergySource, error)`; inside, change `packCapacityKWh(ctx, e.VIN)` to
    `packCapacityKWh(ctx, s, e.TeslaID)` (RD11: was `e.VIN`, now `e.TeslaID`).
  - Update both call sites, `writerService.Create` and `writerService.Update`, from
    `resolveEnergy(ctx, e)` to `resolveEnergy(ctx, w.store, e)`. **No other line in either method
    changes.**
  - Add two new imports: `"errors"` and `"github.com/jackc/pgx/v5"`.
  - `resolvePriceSource`, `promoteIfComplete`'s call site, and every other function in this file
    are **untouched**.
  `depends_on`: 2.1 · `parallel_ok`: with 2.4, 2.5

- [x] **2.4** **[module: charging worker]** `internal/charging/session_verifier.go`:
  - Inside `VerifySession`, after the `LockSessionForVerification` call succeeds, replace the
    unconditional `packCapacityKWh(ctx, row.Vin)` call with the nil-check shown in design.md D5:
    derive `teslaID := pgInt8ToInt64Ptr(row.TeslaID)`; when non-nil, call `packCapacityKWh(ctx, v,
    *teslaID)`; when nil, set `capacityKWh = defaultPackCapacityKWh` directly, **without** calling
    `packCapacityKWh` (RD11's caller-table row, design.md D5's "Alternative rejected" note).
  - Add a `latestMeasuredCapacity` method on `*sessionVerifier`, verbatim from design.md D5,
    mirroring `dbStore`'s method but using `v.q` directly.
  - Add one new import: `"errors"`. (`"github.com/jackc/pgx/v5"` is already imported.)
  - Every other line of `VerifySession`, and every other function in this file, is **untouched**.
  `depends_on`: 2.1 · `parallel_ok`: with 2.3, 2.5

- [x] **2.5** **[module: charging worker]** Create `internal/charging/monthly_capacity.go` with, in
  this order: the `capacitySample` type, the `minSamples`/`minDeltaPct` constants,
  `estimateEffectiveCapacity`, `median`, the `monthlyCapacityCalculator` struct,
  `newMonthlyCapacityCalculator`, the `var _ MonthlyCapacityCalculator = (*monthlyCapacityCalculator)(nil)`
  assertion, and `Calculate` — all verbatim from design.md D7. This file imports `chargingdb` and
  `pgtype` directly (it owns three new queries) — see task **2.6** for the required `AGENTS.md`
  update this creates.
  **design.md D9 is binding here**: `estimateEffectiveCapacity` holds only the gate, `median` takes
  `[]capacitySample` and sorts its own copy, and the two stay separate functions. Do not "simplify"
  them into one, and do not narrow `median` back to `[]float64`.
  `depends_on`: 2.2 · `parallel_ok`: with 2.3, 2.4

- [x] **2.6** **[module: charging worker]** `internal/charging/AGENTS.md` §Allowed Imports: add
  `monthly_capacity.go` to **both** the `chargingdb`-import file list and the `pgtype`-import file
  list (six files and five files respectively, after this change). This is the same rule the file
  itself already states: "When you add a file that owns a query, add it to both lists in the SAME
  change." Do this task in the same commit as 2.5, not later.
  `depends_on`: 2.5 · `parallel_ok`: with 2.3, 2.4 (but not before 2.5)

---

## Wave 3 — tests (unit tests are included, per `internal/charging/AGENTS.md` §Testing Notes)

Every expected value is fixed in design.md §Test Contract. **Assert that contract.** Fixture
conventions: fresh `uuid.New()` account ids per test; never `pgtype` in any assertion or helper
(`internal/charging/AGENTS.md` §Testing Notes).

- [x] **3.1** **[module: charging worker]** Create `internal/charging/monthly_capacity_estimator_test.go`
  — offline, no DB, package `charging`. Cover design.md Test Contract **A1–A9**:
  `estimateEffectiveCapacity`'s five cases (the `minSamples` boundary, the odd/even median, the
  delta-gate dropping both a row and its outlier value, the gate's `>=` boundary),
  `packCapacityKWh`'s three cases against an in-package fake `packCapacityLookup` (no measured row,
  a measured value, a lookup error), and **A9** — `median` called directly with an unsorted
  `[]capacitySample`, which pins D9's method seam (the signature a future second method plugs into,
  and `median`'s own responsibility to sort).
  `depends_on`: 2.5, 2.1 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: charging worker]** Create `internal/charging/db_monthly_capacity_integration_test.go`
  — `DATABASE_URL`-gated, using the package's existing `testdb_test.go` pool. Cover design.md Test
  Contract **B1–B2** (the `CHECK` and the `UNIQUE` constraint, direct SQL, SQLSTATE not message
  text) and **C1–C12** (the RD2 exclusions with their one-line reasons, RD4's thin-month case, the
  odd/even median through the real job, RD5's cross-account pooling and null-`tesla_id` skip, both
  updated seams' unchanged-until-measured behaviour, RD4's newest-measured-skips-thin-current-month
  read, RD11's caller-side nil short-circuit, and RD9's idempotent re-run).
  `depends_on`: 2.3, 2.4, 2.5, 2.6 · `parallel_ok`: with 3.1

- [x] **3.3** **[module: charging worker]** Delete `TestPackCapacityKWh_VinIndependent` from
  `internal/charging/entry_status_test.go` (the test function and its `// --- ... ---` section
  header, if that header covers only this test). It calls the old
  `packCapacityKWh(ctx, vin string)` seam and asserts "always returns 62.0, whatever the VIN" —
  the exact rule design.md D4 removes. The test cannot be ported: the VIN argument it varies no
  longer exists.
  **Lost coverage, and where it went:** its three cases (real VIN, empty VIN, unknown VIN) all
  asserted the 62.0 fallback. Task **3.1**'s Test Contract **A6** asserts that same fallback
  against the new seam, with a fake `packCapacityLookup` returning no measured row. A7 and A8
  then cover the two cases the old test could not reach. Net coverage goes up, not down.
  **Touch nothing else in this file** — `entry_status_test.go` holds RM33's and RM51's own test
  groups, and both stay exactly as they are.
  This task exists because no other task owned this file, and it is the only thing keeping
  `go vet ./...` red after Wave 2. Appended by the leader at the Wave 2 boundary, on the owner's
  decision (recorded in progress.json as `D-lead-6`).
  `depends_on`: 3.1 · `parallel_ok`: no (do it after 3.1 lands A6-A8, so the replacement exists
  before the original is removed)

---

## Wave 4 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [ ] **4.1** **[module: charging worker]** `internal/charging/AGENTS.md`:
  - §Responsibility — one sentence noting the module now also measures and stores each vehicle's
    effective pack capacity monthly (RM52 tier 1), alongside the two existing record types.
  - §Public Interface — add `MonthlyCapacityCalculator`, `MonthlyCapacityReport`, and
    `NewMonthlyCapacityCalculator`, following the existing per-port doc block shape.
  - §Data Ownership — add a `monthly_effective_capacity` subsection mirroring the existing
    `manual_charge_entries`/`supercharger_sessions` ones: no `account_id` and why, no FK and why,
    the `CHECK`, and that `effective_capacity_kwh IS NULL` never means "guessed."
  - §Coding Rules / the `packCapacityKWh` `TODO(MAG-18)` reference — remove or update every mention
    of the old hardcoded-`62.0`/backlog-#18 language elsewhere in this file (search for
    `MAG-18`/`backlog #18` across the whole file, not just §Data Ownership) so nothing here still
    calls the seam a stub.
  - §Coding Rules — add the D9 rule, in two or three lines: the monthly capacity estimator is
    split into a **gate** (`estimateEffectiveCapacity`) and a **method** (`median`); a new
    estimation method is a new function with `median`'s exact signature
    (`func(gated []capacitySample) float64`), never an edit to `median` and never inlined into the
    gate; the gate is never duplicated, so every method sees the same evidence and the same
    `sample_count`. Point at design.md D9 for the full reasoning and the priced second-column path.
  - §Testing Notes — the two new test files and what each covers.
  - §Allowed Imports — confirm task 2.6's edit is present (do not duplicate it if 2.6 already
    landed it).
  `depends_on`: 2.6, 3.2 · `parallel_ok`: no

---

## Wave 5 — signals

- [ ] **5.1** **[module: charging worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/charging`, `go build ./...`, `go vet
  ./...`. `go build ./...` and `go vet ./...` should be clean repo-wide (proposal.md §Breaking —
  every change is additive or confined to unexported functions). If either fails outside
  `internal/charging`, stop and report it rather than assuming it is expected; it is not.
  `depends_on`: 3.1, 3.2, 4.1 · `parallel_ok`: no

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

- [ ] **O2** **[owner]** Confirm the migration applies cleanly and the new table exists, since this
  is a brand-new table with no pre-existing rows to backfill (unlike RM33/RM51's column additions,
  there is no "Owner verification" data-shape query needed here — the table starts empty and stays
  empty until the job runs, which is tier 2/3's job):
  ```sql
  SELECT conname FROM pg_constraint WHERE conrelid = 'charging.monthly_effective_capacity'::regclass;
  ```
  Expected: `monthly_effective_capacity_pkey`, `monthly_effective_capacity_period_is_month_start`,
  and the table's `UNIQUE (tesla_id, effective_period)` constraint — three rows, no
  `charge_sessions`-era leftovers possible since this table is new.

## Cross-module tasks the leader owns

- [ ] **L1** **[leader]** Confirm `go build ./...`/`go vet ./...` are green **outside**
  `internal/charging` once Wave 5 lands. Proposal.md §Breaking states no cross-module compile fix
  should be needed — verify rather than assume.
- [ ] **L2** **[leader]** Confirm the root `README.md` needs no edit. This change adds no module
  and no runnable (tier 3 adds `cmd/monthly-capacity/`, not this tier) — the "Project Structure"
  tree and the "Architecture" table should already be correct.
- [ ] **L3** **[leader]** When tier 1 archives, close roadmap backlog item **#18** in
  `openspec/roadmaps/backlog.md` (superseded by this change, per roadmap RD1) and update
  `openspec/roadmaps/RM52-vehicle-monthly-metrics.md`'s tier 1 status to `[x]`.
