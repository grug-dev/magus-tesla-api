# Tasks — charging-add-derived-start-battery-pct

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only. This
change has **no leader-owned task**: it ships no migration, touches no `cmd/`, adds no
database object, and its docs live entirely inside `internal/charging/AGENTS.md`. See
design.md D1–D12 for the rationale behind each group.

**Ordering constraints:**

- **Wave 1 (query) before Wave 2 (Go).** `sqlc` generates `LockSessionForVerificationParams`/
  `LockSessionForVerificationRow` from `query.sql`, so the query must exist before
  `session_verifier.go` can reference the generated types. No migration is added or changed
  (design.md D7 — this is a query change, not a schema change).
- **1.1 before 1.2** — `make sqlc` runs once, after the query exists.
- **Wave 2 before Wave 3.** The offline tests (2.1's `derivedStartBatteryPct`, 2.2's
  `needsDerivedStartBatteryPct`) must exist before 3.1 can compile against them
  (`ai/go-conventions.md` §Testing authoring order). Their expected values are already fixed
  in design.md §Test Contract Groups A/B — **author the tests against that contract, not
  against whatever the implementation produces.**
- **Wave 2 before Wave 4.** The two pre-existing-test corrections (4.1, 4.2) depend on
  `VerifySession`'s new behavior actually existing (2.3) — correcting a test's expected value
  before the behavior it exercises exists would leave it failing for the wrong reason in the
  interim.
- **2.1 and 2.2 touch disjoint files (`capacity.go` vs `session_verifier.go`) and may run in
  parallel.** 2.3 depends on both. 3.1 depends on 2.1+2.2 (it tests both). 4.1 and 4.2 touch
  disjoint files and may run in parallel with each other, and with 3.1 and 5.1 — all four
  depend only on 2.3.

---

## Wave 1 — query (module: charging worker)

- [x] **1.1** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: LockSessionForVerification :one` exactly as specified in design.md §D9, including
  its full doc comment, immediately before `VerifyChargeSession`. `SELECT vin, energy_kwh FROM
  charge_sessions WHERE id = @id AND account_id = @account_id FOR UPDATE;` — no other column,
  no `RETURNING`, no `ON CONFLICT`. Do **not** add or edit any migration file — this table
  already has both columns and its primary key already serves this point lookup (design.md
  D9's closing paragraph). Run `make sqlc` and report the result — this generates
  `chargingdb.LockSessionForVerificationParams` (fields `ID`, `AccountID`) and
  `chargingdb.LockSessionForVerificationRow` (fields `Vin string`, `EnergyKwh pgtype.Float8`),
  and the `LockSessionForVerification` method on `*chargingdb.Queries`. Confirm no other
  generated file changes (in particular, `VerifyChargeSessionParams`/`ChargeSession` must be
  byte-for-byte unchanged — this task touches no existing query).
  `depends_on`: — · `parallel_ok`: no (blocks everything)

---

## Wave 2 — the Go derivation (module: charging worker)

- [x] **2.1** **[module: charging worker]** `internal/charging/capacity.go` — add
  `derivedStartBatteryPct(capacityKWh float64, energyKWh *float64, endPct *int) *int`
  immediately after `derivedEnergyKWh`, matching design.md D8's body exactly. Doc comment
  states in full: that it is the algebraic inverse of `derivedEnergyKWh`; that it returns
  `nil` when `energyKWh` is `nil` (design.md **D5**) or `endPct` is `nil` (defensive
  nil-tolerance, mirroring `derivedEnergyKWh`'s own shape — `VerifySession`'s own gate never
  calls it with a nil `endPct` in practice); that the result is rounded with `math.Round`,
  **half away from zero**, matching `derivedEnergyKWh`'s own stated rounding rule and
  PostgreSQL's `numeric` mode (design.md **D4**); and that it returns `nil` — never clamps,
  never errors — when the rounded result falls outside `[0, 100]` (design.md **D3**).
  Unexported, for the identical reason `derivedEnergyKWh`/`packCapacityKWh` are unexported —
  say so in the comment, do not silently omit the rationale. Update `packCapacityKWh`'s own
  doc comment to note it is now called from two directions (`resolveEnergy` and this
  function's caller).
  `depends_on`: 1.1 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: charging worker]** `internal/charging/session_verifier.go` — add
  `needsDerivedStartBatteryPct(startPct, endPct *int) bool { return startPct == nil && endPct
  != nil }` near the top of the file, above `VerifySession`. Doc comment states design.md
  **D2**'s trigger condition in full (conditions 1+2 of the four — the row-energy and
  in-range conditions are decided by `derivedStartBatteryPct` once the row is read) and states
  explicitly, per design.md **D8**, why this is a separately named function rather than folded
  into `derivedStartBatteryPct`: a caller-supplied start is never recomputed, and this decision
  must be assertable without reading any row.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: charging worker]** `internal/charging/session_verifier.go` — rewrite
  `VerifySession`'s body per design.md §D8/D9's composition and query text:
  - Keep the existing range-validation-then-source-computation shape for the two supplied
    parameters, unchanged, before any database call.
  - After validation, if `needsDerivedStartBatteryPct(startBatteryPct, endBatteryPct)`:
    open a transaction (`v.pool.Begin(ctx)`, `defer tx.Rollback(ctx)`, mirroring
    `session_writer.go`'s `MirrorSessions`/`internal/account`'s `AccessTokenFor` pattern
    exactly — design.md **D9**), build `qtx := v.q.WithTx(tx)`, call
    `qtx.LockSessionForVerification(ctx, chargingdb.LockSessionForVerificationParams{ID: id,
    AccountID: accountID})`; on error, wrap **identically** to `VerifyChargeSession`'s own
    not-found wrap — `fmt.Errorf("charging: verify session: %w", err)` (design.md **D10**) —
    do not introduce a second error message shape. On success, call `packCapacityKWh(ctx,
    row.Vin)`; on error, wrap as `fmt.Errorf("charging: resolving pack capacity: %w", err)`
    (mirrors `resolveEnergy`'s own wrap in `service.go`, verbatim). Compute
    `startToStore := derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh),
    endBatteryPct)` — reuse the existing `pgFloat8ToFloat64Ptr` helper (`session_writer.go`);
    do not add a second one.
  - If `needsDerivedStartBatteryPct` is false: `startToStore := startBatteryPct` (unchanged —
    the existing single-statement path).
  - Compute `battery_pct_source` from `startToStore`/`endBatteryPct` exactly as today (the
    existing `batteryPctSourceUserVerified` constant and its one branch are unchanged — design.md
    **D1**: no new source value).
  - Call `VerifyChargeSession` using `qtx` when a transaction is open, or `v.q` otherwise, with
    `StartBatteryPct: intPtrToPgInt2(startToStore)` (reusing the existing helper). If a
    transaction is open, `tx.Commit(ctx)` after a successful call, before returning.
  - Map the returned row via the existing `rowToSession` — no new mapping code.
  Update `VerifySession`'s doc comment (also mirrored onto the `SessionVerifier` interface
  declaration in `charging.go`, task 2.4) to state the full derivation contract.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: no

- [x] **2.4** **[module: charging worker]** `internal/charging/charging.go` — update the
  `SessionVerifier.VerifySession` interface doc comment to state, in full, alongside its
  existing content (the three-column `SET` clause, the `battery_pct_source` computation, the
  partial-call legality, the range validation, the not-found shape): the new derivation
  contract — trigger condition (design.md **D2**), that a supplied start is never overridden,
  that a missing energy figure or an out-of-range result both silently leave the start absent
  rather than erroring (design.md **D3**/**D5**), and that the source computation is otherwise
  unaffected (design.md **D1** — still no new value). Then run `go build ./...`, `go vet
  ./...` and `gofmt -l` and report the results — all three must be clean.
  `depends_on`: 2.3 · `parallel_ok`: no

---

## Wave 3 — offline tests (module: charging worker)

- [x] **3.1** **[module: charging worker]**
  `internal/charging/session_verifier_derivation_test.go` (new file, `package charging` — NOT
  `charging_test`; design.md **D11** explains why, mirroring `entry_status_test.go`'s
  established precedent for the identical reason) — implement Test Contract **Groups A
  (A1–A7) and B (B1–B5)** exactly as design.md states them, with those exact expected values.
  Header comment states the file's scope (MAG-36, this change) and, per D11, explicitly why
  the package is `charging` rather than `charging_test` — do not omit that explanation, it is
  the load-bearing fact a future reader needs. Compare `*int` results directly (no float
  tolerance — the function returns `*int`); compare `bool` results directly. Assert `nil`
  explicitly for every nil case in Group A, never a zero value. No database, no
  `testcontainers`, no `pool` — every case is a direct function call.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: with 4.1, 4.2

---

## Wave 4 — corrections to pre-existing integration tests (module: charging worker)

> **Read design.md D12 before starting this wave.** Both tasks here are corrections to tests
> from *already-archived* changes (RM31, MAG-25), made necessary by this change's own intended
> behavior — not new coverage authored for this change (design.md D6 scopes new coverage to
> Wave 3's offline tests only). Skipping this wave would leave `go test ./...` failing for the
> owner the moment they run it — it is not optional follow-up work.

- [x] **4.1** **[module: charging worker]**
  `internal/charging/db_session_verifier_integration_test.go` —
  `TestVerifySession_PartialEndOnlyStillSetsSource` (RM31 Test Contract T3): change the
  expected value only — `s3.StartBatteryPct == nil || *s3.StartBatteryPct != 41` is now the
  failure condition (was: `s3.StartBatteryPct != nil`). Do **not** change
  `seedVerifierSession`'s `EnergyKWh: ptrFloat64(30.5)` or any other fixture value (design.md
  **D12**, §"What must NOT change" — the fixture is what makes this the natural end-to-end
  regression proof of the new derivation, not something to dodge). Update the function's doc
  comment to state both facts now true: the source is still set correctly regardless of which
  single percentage is supplied (RM31's original point, unchanged), **and** the start
  percentage is now derived — `41`, from `EnergyKWh = 30.5` and `endPct = 90` at the
  `62.0` kWh constant capacity — rather than left absent, because MAG-36 (this change) added
  that derivation. Update the file's header table-of-contents comment (`T3
  TestVerifySession_PartialEndOnlyStillSetsSource`) with a one-line note that this test was
  updated by MAG-36, citing this change's name. This is a `DATABASE_URL`-gated test; per
  `Test-Execution-Policy` it cannot be run by the worker making this edit.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 4.2

- [x] **4.2** **[module: charging worker]**
  `internal/charging/db_inferred_capacity_sessions_integration_test.go` — in
  `TestMirrorAndVerify_InferredCapacity_TableCases`'s `cases` table, change case `"T15"`'s
  `energyKWh` field from `ptrFloat64(52.273)` to `ptrFloat64(124.0)`. Do **not** change `want:
  nil` — it stays correct, now reached through design.md D3's out-of-range guard
  (`derivedStartBatteryPct(62.0, ptr(124.0), ptr(100))` → `-100`, out of range → `nil`) rather
  than through "the start percentage was never touched." Update the comment immediately above
  case `"T15"` in the table to state why `124.0` was chosen (exactly `2 × 62.0`, so the
  resulting `raw = -100.0` is legible by inspection) and to note this is a MAG-36 (this
  change) correction, not part of MAG-25's original contract. Do not touch any other case in
  the table (T13/T14/T16–T19 are unaffected — design.md D12 confirms each individually). This
  is a `DATABASE_URL`-gated test; per `Test-Execution-Policy` it cannot be run by the worker
  making this edit.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 4.1

---

## Wave 5 — module docs (module: charging worker)

- [x] **5.1** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's changed write behavior (docs-track-structural-change, `CLAUDE.md`
  §Non-negotiables):
  - **§Public Interface** — under the existing `SessionVerifier` block, add the derivation
    contract (mirroring task 2.4's interface doc comment): trigger condition, that a supplied
    start is never overridden, the silent-absence behavior for missing energy or an
    out-of-range result, and that `battery_pct_source` computation is otherwise unchanged
    (design.md D1/D2/D3/D5).
  - **§Data Ownership → `charge_sessions`** — in the `start_battery_pct`/`end_battery_pct`/
    `battery_pct_source` bullet, add a sentence: since MAG-36
    (`charging-add-derived-start-battery-pct`), a `SessionVerifier.VerifySession` call that
    leaves `start_battery_pct` unsupplied MAY derive it from `energy_kwh` and the supplied end
    percentage, still writable only through this same port — no new writer, no schema change.
    Cross-reference design.md D1's accepted provenance trade-off (a derived value is
    indistinguishable from a human-typed one under `battery_pct_source = 'user_verified'`) so
    a future MAG-18 implementer reads it here instead of rediscovering it.
  - **§Allowed Imports** — no change (no new import categories).
  - **§Testing Notes** — add `session_verifier_derivation_test.go` to the offline-unit-test
    list (package `charging`, not `charging_test` — state why, per design.md D11), and note
    that `db_session_verifier_integration_test.go` and
    `db_inferred_capacity_sessions_integration_test.go` were each updated by this change
    (design.md D12) — one test's expected value, one fixture's `energyKWh`, both DB-gated and
    unrun by the assistant.
  `depends_on`: 2.4, 3.1, 4.1, 4.2 · `parallel_ok`: no (touches the file every other task also
  references, kept last to avoid merge noise)

---

## Handing back — the suite the assistant does not run

Per `CLAUDE.md` §"Builds & local checks" and the `Test-Execution-Policy`: the assistant runs
`go build ./...`, `go vet ./...`, `gofmt -l`, `make sqlc`, and the standalone guards; **the
owner runs the suite.** Until the owner reports it, every task above whose tests exist but were
not executed is **`awaiting-user-verification`**, never `done` — this includes Wave 3's new
offline tests (which `go vet` will compile-check but not run) and, in particular, Wave 4's two
corrections, which are `DATABASE_URL`-gated and cannot be exercised without a database.

```bash
make check          # build vet ui-guard i18n-guard money-guard tz-guard migration-guard test
# or, for just this change's coverage:
go test ./internal/charging/... -run 'DerivedStartBatteryPct|NeedsDerivedStartBatteryPct' -v
go test ./internal/charging/... -run 'VerifySession|InferredCapacity' -v
```

The integration tests are `DATABASE_URL`-gated and otherwise start a disposable
`postgres:16-alpine` via testcontainers, so they need either `DATABASE_URL` set or a running
Docker daemon (`internal/charging/testdb_test.go`).

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast.

- **Any migration file, new or edited.** design.md D7/D9 established the existing primary key
  already serves the new query; adding an index or column anyway would trip the `database`
  design gate without the owner's sign-off.
- **A new `battery_pct_source` value (e.g. `'estimated'`).** Design.md D1 — the owner chose to
  reuse `user_verified`, explicitly, with the trade-off recorded.
- **Any change to `packCapacityKWh`'s hardcoded `62.0`.** Backlog #18/MAG-18, unrelated to
  this ticket.
- **Deriving an end percentage from a start percentage, in either direction.** The ticket and
  design.md D2 are explicit this is one-directional: start is derived from end + energy, never
  the reverse.
- **Clamping an out-of-range derived result to `0` or `100`, or rejecting the save.** Design.md
  D3 — `NULL`, always, silently.
- **A user-facing error message or i18n key for the "could not derive" case.** Out of this
  module's boundary (`internal/gateway` owns i18n) and out of scope.
- **Any gateway change.** The `/supercharger-stats` edit form already calls `VerifySession`
  unchanged; nothing in `internal/gateway` needs to change for this ticket.
- **Any change to `SessionMirror`, `SessionWriter`, `MirrorChargeSession`, `SessionReader`, or
  `manual_charge_entries`/`Writer`/`Reader`.** Untouched by this change.
- **A new `DATABASE_URL`-gated integration test proving the derivation end-to-end.** Design.md
  D6 — the two pure functions carry the whole test contract offline. Wave 4's corrections are
  maintenance, not new coverage.
- **Exporting `derivedStartBatteryPct` or `needsDerivedStartBatteryPct`.** Design.md D11
  considered and rejected this as the fix for the test-file-location problem — it would break
  the sibling functions' established privacy invariant.
- **Touching any `VerifySession` call site other than the two named in design.md D12.** The
  full inventory is in D12/Context 6; every other call site was checked and is unaffected.
- **Root `README.md` edits.** No module, table, or column is added, removed, renamed, or
  re-scoped; the existing `charging` module description does not go stale.
