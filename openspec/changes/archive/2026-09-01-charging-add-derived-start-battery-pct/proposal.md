Source: MAG-36 — https://linear.app/magus-monitor/issue/MAG-36/battery-start-calculated
Design gate: **not tripped.** This change creates or alters no database object — no
migration, no table, no column, no index, no constraint. It adds one new sqlc query
(`LockSessionForVerification`, a `SELECT ... FOR UPDATE` against the existing
`charge_sessions` primary key) that reads two columns already there. `openspec/config.yaml`'s
blanket "design.md is REQUIRED for any DB-touching change" still applies — this change adds a
query and changes `SessionVerifier.VerifySession`'s write behavior — so design.md documents
the full query, its transaction shape, and the read paths, without requiring the owner's
`database` gate confirmation a schema change would need.
Unit tests: **offline unit tests only, added early**, per the owner's explicit choice at the
grounding interview (design.md D6). No new `DATABASE_URL`-gated integration test is added for
the derivation itself — its two pure Go functions are 100% offline-testable and carry the
whole test contract. **Two pre-existing integration tests are corrected as part of this
change** (design.md D12) because this change's own behavior makes their previously-true
expected values false; that correction is maintenance forced by this change, not new coverage
for it, and — being `DATABASE_URL`-gated — is reported `awaiting-user-verification` like every
other integration-test file this module ships.

---

## ⚠️ Two pre-existing integration tests silently break under this change

Read this before approving the design. This is not a hypothetical risk — both are real,
currently-passing tests in the repository today, and this change's own intended behavior
makes their expected values wrong.

`SessionVerifier.VerifySession` is called with `startBatteryPct == nil` and
`endBatteryPct != nil` — the exact trigger condition this change adds derivation to — in two
existing fixtures whose sessions happen to carry a non-nil `energy_kwh` that derives an
in-range result:

| Test | File | Call | `energy_kwh` | Old expectation | New (correct) expectation |
|---|---|---|---|---|---|
| `TestVerifySession_PartialEndOnlyStillSetsSource` (RM31 T3) | `db_session_verifier_integration_test.go` | `VerifySession(ctx, acctA, v3ID, nil, ptr(90))` | `30.5` | `StartBatteryPct == nil` | `*StartBatteryPct == 41` |
| `TestMirrorAndVerify_InferredCapacity_TableCases/T15` (MAG-25 T15) | `db_inferred_capacity_sessions_integration_test.go` | `VerifySession(ctx, accountID, id, nil, ptr(100))`, fixture `energyKWh: ptr(52.273)` | `52.273` | `InferredCapacityKWhCalc == nil` (via `start_battery_pct == nil`) | still `nil`, but ONLY if the fixture's `energyKWh` is changed to a value the derivation rejects as out-of-range (design.md D12 specifies `124.0`) |

Every other `VerifySession` call site in the module (a full inventory is in design.md D12) was
checked and is unaffected — each either supplies a non-nil `startBatteryPct` (never
recomputed, by design) or a nil `endBatteryPct` (nothing to derive from). design.md D12 gives
the exact fix for each of the two affected tests, with the arithmetic worked out. Both fixes
are inside `internal/charging/`, inside this worker's sandbox, and are part of this change's
tasks — not deferred, not optional.

---

## Why

MAG-36: when a human edits a Supercharger session on `/supercharger-stats` and leaves the
start battery percentage blank while supplying (or already having) an end percentage, the
module should calculate the start percentage instead of leaving it absent — using the same
"energy ÷ pack capacity" algebra `internal/charging` already uses in the opposite direction
for manual entries (`capacity.go`'s `derivedEnergyKWh`, MAG-18/RM33). The gateway edit UI this
serves already exists (`RM31-gateway-add-session-battery-edit`); this change only touches the
domain module's write port.

## What Changes

**Additive to behavior, but no schema change and no new port method.**

- **CHANGED** — `SessionVerifier.VerifySession`'s behavior (not its signature): when the
  caller submits `startBatteryPct == nil` and `endBatteryPct != nil`, and the session's
  `energy_kwh` is non-`NULL`, and the algebraic result lands in `[0, 100]`, the method now
  stores the derived value instead of `NULL`. A caller-supplied `startBatteryPct` is **never**
  recomputed or overridden (design.md D2). Every other call shape — both supplied, only start
  supplied, both nil (clear), an out-of-range result, no energy on the row — is **unchanged**.
- **ADDED** — `derivedStartBatteryPct(capacityKWh float64, energyKWh *float64, endPct *int) *int`
  in `internal/charging/capacity.go`, beside `derivedEnergyKWh` — its algebraic inverse,
  mirroring its exact nil-tolerant, gate-inclusive shape (design.md D8).
- **ADDED** — `needsDerivedStartBatteryPct(startPct, endPct *int) bool` in
  `internal/charging/session_verifier.go` — the pure gate deciding whether `VerifySession`
  should even read the row, extracted as its own named, offline-testable function specifically
  so this change's whole test contract stays DB-free (design.md D8/D6).
- **ADDED** — `chargingdb.LockSessionForVerification`, one new sqlc `:one` query in
  `internal/charging/db/query.sql`: `SELECT vin, energy_kwh FROM charge_sessions WHERE id =
  @id AND account_id = @account_id FOR UPDATE`. Called only on the derivation path, inside the
  same transaction as the existing `VerifyChargeSession` update (design.md D7/D9).
- **CHANGED** — `internal/charging/session_verifier.go`: `VerifySession` now opens a
  transaction (`pool.Begin` / `WithTx` / `Commit`, mirroring `internal/account`'s
  `AccessTokenFor` and this module's own `SessionWriter.MirrorSessions`) **only** when
  `needsDerivedStartBatteryPct` is true. Every other call keeps today's single-statement,
  non-transactional path unchanged.
- **CHANGED** — `internal/charging/charging.go`: `SessionVerifier.VerifySession`'s doc comment
  gains the full derivation contract (trigger condition, out-of-range/no-energy behavior,
  rounding rule, that a supplied start is never overridden).
- **CORRECTED** — two pre-existing `DATABASE_URL`-gated integration tests whose expected
  values this change's own intended behavior makes false (see the box above and design.md D12).
- **ADDED** — `internal/charging/session_verifier_derivation_test.go` (new, `package
  charging`) — offline unit tests for `derivedStartBatteryPct` and
  `needsDerivedStartBatteryPct`, implementing design.md's Test Contract Groups A and B.
- **CHANGED** — `internal/charging/AGENTS.md` — §Public Interface, §Data Ownership →
  `charge_sessions`, §Testing Notes (docs-track-structural-change, `CLAUDE.md`
  §Non-negotiables).
- **UNCHANGED** — the migration file, `SessionMirror`, `SessionWriter`, `MirrorChargeSession`,
  `SessionReader`, `manual_charge_entries`, every existing column, index, and constraint, and
  every file outside `internal/charging/`. No port gains or loses a method; `VerifySession`'s
  signature is byte-for-byte the same.

**Breaking:** no. The port's signature is unchanged; every call that does not hit the new
trigger condition behaves exactly as before. The two corrected tests are the *only* observable
change to previously-tested behavior, and both are corrected because the ticket explicitly
asks for the old behavior (leaving start `NULL`) to change.

**Modules affected:** `charging` only. No gateway change — the ticket's `/supercharger-stats`
edit form already calls `SessionVerifier.VerifySession` unchanged
(`RM31-gateway-add-session-battery-edit`); this change only changes what that same call now
computes when the caller leaves start blank.

## Read paths affected

Per `openspec/config.yaml` §proposal. **No read path is added or changed.** The new query
(`LockSessionForVerification`) is a point lookup on `charge_sessions`' primary key, `FOR
UPDATE`, run only inside a human-triggered `VerifySession` call that already needed a
derivation — negligible frequency, identical scoping to the existing `VerifyChargeSession`
point lookup. No `SELECT` read port (`SessionReader`, `SuperchargerSessionAnalyticsReader`,
`Reader`) changes in shape, predicate, or index use. `inferred_capacity_kwh_calc` (MAG-25) may
now be non-`NULL` on more rows than before, since a derived `start_battery_pct` can satisfy its
guard — this is an intended, disclosed consequence of the ticket, not a query change.

## Impact

- **Affected specs:** `charge-session-log` (**MODIFIED** requirement — "A Charge Session's
  Battery Percentages Are Correctable By A Human" gains the derivation behavior; every other
  requirement in that spec is untouched).
- **Affected code:** `internal/charging/` only — `capacity.go`, `session_verifier.go`,
  `charging.go`, `db/query.sql` (+ regenerated `db/query.sql.go`/`db/models.go` via
  `make sqlc`), the two corrected integration test files, one new offline test file,
  `AGENTS.md`.
- **Design gate:** not tripped — see header.
- **Deferred, explicitly NOT in scope:** any gateway change (the edit form already exists and
  needs no change), any change to `manual_charge_entries` or `Writer`/`Reader`, any change to
  `MirrorChargeSession`/`SessionMirror`/`SessionReader`, any new `battery_pct_source` value
  (D1 — the owner explicitly chose to reuse `user_verified` over adding `'estimated'`), and any
  change to `packCapacityKWh`'s hardcoded `62.0` (backlog #18 / MAG-18, unrelated to this
  ticket — see design.md D1's accepted trade-off).
