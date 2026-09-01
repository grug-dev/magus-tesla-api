# Design — charging-add-derived-start-battery-pct

> **Numbering note.** This document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from RM29 tier 6's, RM30's, RM31's, and MAG-25's archived decisions (cited
> as **RM29 D1**, …/**RM30 D1**, …/**RM31 D1**, …/**MAG-25 D1**, …). **D1–D6 transcribe
> binding instructions settled with the owner at the grounding interview and handed down in
> the dispatch**; each says so in its heading. **D7–D12 are decisions this artifacts pass had
> to make** to turn those into a buildable change — D7–D9 are the design problem the dispatch
> posed explicitly (how `VerifySession` reaches `energy_kwh`/`vin`), D10–D11 are
> implementation-shape consequences of D7–D9, and **D12 is a discovery made while writing this
> document, not a question the dispatch anticipated**: two pre-existing integration tests
> break under this change's own intended behavior. Read D12 before approving.

---

## Context

Six facts about the existing code shape everything below.

1. **The algebraic forward direction already exists and is a direct precedent.**
   `capacity.go`'s `derivedEnergyKWh(capacityKWh float64, startPct, endPct *int) *float64`
   computes energy from a capacity and a percentage delta, for `manual_charge_entries`
   (MAG-18/RM33). It is nil-tolerant on both pointer inputs, returns `nil` rather than a
   negative or zero-division result, and is unexported — "an implementation detail of
   `derivedEnergyKWh`'s caller… nothing outside this module may divide by a pack capacity
   behind the module's back" (`capacity.go`'s own doc comment). This change's function is its
   algebraic inverse and inherits the same shape and the same privacy reasoning.

2. **The composition pattern that calls `derivedEnergyKWh` already lives beside its caller,
   not beside the pure function.** `service.go`'s `resolveEnergy` calls
   `packCapacityKWh(ctx, e.VIN)` then `derivedEnergyKWh(capacity, …)`, and lives in
   `service.go` — the file that owns `Writer.Create`/`Writer.Update` — not in `capacity.go`.
   This change's composition (deciding *whether* to derive, fetching what the derivation
   needs, calling the pure inverse) follows the identical placement: beside `VerifySession` in
   `session_verifier.go`, not in `capacity.go`.

3. **`VerifySession` has neither `energy_kwh` nor `vin` today.** Its signature is
   `VerifySession(ctx, accountID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session,
   error)`. Both facts this derivation needs live on the `charge_sessions` row being updated,
   not in any parameter. `packCapacityKWh(ctx, vin)` needs the `vin`; the inverse formula needs
   `energy_kwh`.

4. **A read-then-write-in-one-transaction pattern already exists in this exact codebase, for
   the exact same shape of problem.** `internal/account`'s `AccessTokenFor`: *"The read and the
   rotating write share one transaction. The row is locked `FOR UPDATE` so concurrent refreshes
   of the same connection serialize…"* (`internal/account/service.go`). This module's own
   `SessionWriter.MirrorSessions` already uses the identical `pool.Begin` /
   `q.WithTx(tx)` / `tx.Commit(ctx)` shape (`session_writer.go`), citing `AccessTokenFor` as its
   own precedent. Nothing in this change invents a new pattern; it reuses one this codebase
   already trusts for read-then-conditionally-write correctness.

5. **`energy_kwh` is refreshed nightly, by a different write path, on the exact row this
   change also writes to.** `SessionWriter.MirrorSessions`'s `ON CONFLICT DO UPDATE SET`
   refreshes `energy_kwh` "as Tesla's fees settle" (RM29 D1). A derivation that reads
   `energy_kwh` and later writes `start_battery_pct` based on it, without holding the row, could
   observe a value the nightly mirror has since replaced — a genuine (if low-frequency, given a
   single-household, single-vehicle deployment) correctness hazard, not a hypothetical one.
   Context 4's pattern exists specifically to close this class of race.

6. **Every existing `VerifySession` call site in the module was inventoried, not assumed
   safe.** `grep -rn "VerifySession(ctx" internal/charging/*.go` turned up eleven call sites.
   Nine supply a non-nil `startBatteryPct` or a nil `endBatteryPct` — this change's trigger
   condition (D2) never fires for them. **Two do not**, and both are pre-existing, currently
   passing, `DATABASE_URL`-gated integration tests whose fixtures happen to carry a non-nil
   `energy_kwh` that derives an in-range result under the new logic. D12 names both and gives
   the exact fix.

## Goals / Non-Goals

**Goals**

- `SessionVerifier.VerifySession` derives `start_battery_pct` under exactly the conditions D2
  states, using the same pack-capacity algebra `derivedEnergyKWh` already established, never
  overriding a caller-supplied value (D2).
- The whole derivation is provably correct against a concurrent nightly `energy_kwh` refresh,
  reusing this codebase's own established locking idiom rather than inventing one (D7/D9).
- The new logic's entire test contract is satisfiable **offline**, with no database, per the
  owner's explicit choice (D6/D8).
- Zero schema change; the new query is scoped, reviewed, and its read cost stated (D7).
- Every pre-existing test this change's own behavior would silently break is found and fixed
  in this same change, not discovered later by a failing suite (D12).

**Non-Goals**

- No change to `packCapacityKWh`'s hardcoded `62.0` (backlog #18/MAG-18) — out of scope, and
  D1 records the accepted trade-off this creates.
- No new `battery_pct_source` value, no schema change, no migration (D1).
- No gateway change — the `/supercharger-stats` edit form already calls `VerifySession`
  unchanged.
- No change to `manual_charge_entries`, `Writer`, `Reader`, `SessionMirror`,
  `MirrorChargeSession`, or `SessionReader`.
- No new `DATABASE_URL`-gated integration test proving the derivation itself end-to-end (D6) —
  the two pure functions carry that proof offline. D12's corrections are maintenance forced by
  this change's own behavior, not new coverage authored for it.

---

## Decisions

### D1 — Storage & provenance: reuse `user_verified`, no schema change (binding — interview/owner)

**Decision.** The derived value is written into the existing `start_battery_pct` column.
`battery_pct_source` keeps its existing single value, `user_verified` — no new value, no new
column, no migration, no `sqlc generate` beyond the one new query D7 adds.

The owner was shown the alternative — adding an `'estimated'` value, mirroring
`EnergySource`'s `USER`/`ESTIMATED` split on `manual_charge_entries` — together with its cost
(a migration, a `CHECK` constraint change, a `SessionVerifier` signature or behavior change to
surface provenance, and a gateway change to render it) and chose to reuse `user_verified`.

**Accepted trade-off, stated plainly, not as an open question.** A derived start percentage
becomes indistinguishable from one a human actually typed. Backlog item MAG-18 ("replace the
hardcoded 62 kWh with a real per-vehicle value") is described in this module's own `AGENTS.md`
as needing to filter `WHERE energy_source = 'USER'` on `manual_charge_entries` when averaging
inferred capacities, so it does not average the `62.0` constant back into itself. This
change's `charge_sessions` rows carry no equivalent filter column at all — `battery_pct_source`
does not distinguish "a human typed this" from "the module computed this from the same `62.0`
constant" — so a future capacity-averaging feature over `charge_sessions` cannot exclude these
rows by provenance. It would have to be re-derived by recomputing the reverse formula and
checking for an exact match, or accept a small self-reinforcing bias toward `62.0`. This is a
known, accepted consequence of D1, not a defect to fix here.

### D2 — Trigger condition: submitted start nil, submitted end non-nil, row energy non-nil, result in range (binding — owner)

**Decision.** `VerifySession` derives `start_battery_pct` **iff all four** hold:

1. The caller's `startBatteryPct` parameter is `nil`.
2. The caller's `endBatteryPct` parameter is non-`nil`.
3. The session row's `energy_kwh` is non-`NULL`.
4. The algebraic result (D4's rounding applied) lands in `[0, 100]` (D3).

A `startBatteryPct` the caller actually supplied is **never** recomputed or overridden, under
any condition — this is unconditional, not merely the common case. Clearing the start field
in the edit form is therefore the caller's way of saying "calculate it for me": a start
percentage can never remain absent while a valid end and a usable energy figure exist and the
maths lands in range.

**Why the whole gate is a named condition, not scattered `if`s.** Condition 1+2 is factored
into `needsDerivedStartBatteryPct` (D8) specifically so it is a single, offline-testable
statement of the trigger — not something inferred by reading `VerifySession`'s control flow.

### D3 — Out-of-range result: derive nothing, store `NULL`, no error (binding — owner)

**Decision.** If the computed start (after D4's rounding) is `< 0` or `> 100`, the stored
`start_battery_pct` is `NULL`. The call **succeeds**. No error, no clamp to `0` or `100`, no
user-facing message.

**Why this is not theoretical.** `internal/charging` hardcodes `packCapacityKWh` at `62.0`
kWh for every vehicle, while `internal/analytics`'s own capacity map gives `model3`/`modely`
`75` kWh (a fact already on record in this module's `AGENTS.md`). A large `energy_kwh` figure
on a vehicle whose real capacity exceeds `62.0` computes a start percentage below `0` under
this module's constant — routinely, not as an edge case. Rejecting the save or clamping the
value would either block a legitimate edit or silently fabricate `0`/`100`; `NULL` is the same
"nothing recorded" contract every other optional column on this table already uses, and
mirrors `derivedEnergyKWh`'s own precedent of returning `nil` rather than fabricating a value.

**No user-facing error message** is added anywhere — that would require new `i18n.T` catalogue
keys in `internal/gateway` (`CLAUDE.md` §i18n), which is outside this module's boundary and
outside this change's scope.

### D4 — Rounding: `math.Round`, half away from zero (binding — owner, matching established precedent)

**Decision.** The raw algebraic result is rounded with `math.Round` before the range check
(D3) and before storage. Go's `math.Round` rounds half away from zero — `48.5 → 49`, not
banker's rounding — which is also PostgreSQL's `numeric` rounding mode, exactly the reasoning
`derivedEnergyKWh`'s own doc comment already states for the forward direction. `start_battery_pct`
is a `SMALLINT`; the result is always stored as a whole percentage, never a fraction.

### D5 — Nil `energy_kwh`: skip silently (binding — owner)

**Decision.** When the session row's `energy_kwh` is `SQL NULL`, no derivation is attempted —
`start_battery_pct` is left `NULL` (unless the caller separately supplied one, which D2's gate
never overrides regardless). No error.

The ticket's own text asserts energy added is "always not null" — **this is not true of the
column**: `charge_sessions.energy_kwh` is `DOUBLE PRECISION` with no `NOT NULL`, documented
in the migration as *"NULL when the session had no kWh fee."* D5 governs that real, legal
state; it is not a defensive branch against an input the schema forbids.

### D6 — Test scope: offline unit tests only, added early (binding — owner's explicit choice)

**Decision.** The new logic's tests live in `internal/charging/session_verifier_derivation_test.go`
(package `charging` — see D11 for why not `charging_test.go`), table-testing the two pure
functions D8 introduces. No `DATABASE_URL`-gated integration test is authored for the
derivation itself. The owner was offered "no tests" and chose offline coverage instead. Per
`ai/go-conventions.md` §Testing authoring order, these are pure/offline tests and are written
**early** — Wave 2, immediately after the functions exist, not deferred to a final wave, since
there is no migration or generated type this module's DB-backed tests would otherwise have to
wait for.

D12 is a distinct, unavoidable exception forced by this change's own behavior, not new
optional coverage — see D12.

---

### D7 — How `VerifySession` obtains `vin` + `energy_kwh`: a new locked `SELECT`, in a transaction with the existing `UPDATE` (the design problem, resolved)

The dispatch posed three options. All three are evaluated here, against the module's boundary
rules (`pgtype` confined to the DB boundary — `ai/architecture.md` §2), the read-heavy
`Performance-Profile`, and D6/D8's requirement that the derivation stay a pure, offline-testable
Go function.

**Option 2 — derive in SQL, from the row's own `energy_kwh`, inside the `UPDATE` — rejected.**
This is the option the dispatch specifically asked to be checked against "keeping the maths in
Go where `packCapacityKWh` lives" and against D6. It fails both:

- `packCapacityKWh(ctx, vin)` is a **Go function** — today a hardcoded constant, but explicitly
  a seam MAG-18/backlog-18 will replace with "a real per-vehicle lookup," per its own doc
  comment: *"The `ctx` and `error` results are deliberate future-proofing… the whole reason the
  ticket demands a function here is that a DB- or module-backed lookup replaces it later."* An
  in-SQL derivation would have to hardcode `62.0` a **second** time, in `query.sql`, creating
  two independent sources of truth for the pack capacity that MAG-18 would have to keep in sync
  by hand, in two different languages, forever. This is precisely the kind of duplicated
  vocabulary `CLAUDE.md` §Non-negotiables' AI-efficiency principle (closed, small vocabularies)
  argues against.
- It cannot satisfy D6. A `CASE` expression inside an `UPDATE ... SET` is not a Go function;
  there is nothing to unit-test offline. The owner explicitly chose offline coverage over "no
  tests" — Option 2 would make that choice impossible to honor.

Rejected on both grounds independently; either alone would be sufficient.

**Option 1 — read the row first (a plain `SELECT`), compute in Go, then `UPDATE` — accepted,
refined by D9.** This is the only option that keeps `packCapacityKWh` and the algebraic inverse
as ordinary Go functions, calling them exactly as `resolveEnergy` already calls
`packCapacityKWh` + `derivedEnergyKWh` for the forward direction (Context 2). The open question
this option raises — and the dispatch asks explicitly — is whether the `SELECT` and the
`UPDATE` need one transaction to be correct. Context 5 answers yes: `energy_kwh` is refreshed
by a different write path (the nightly mirror) on the same row, so an un-transacted,
un-locked read can observe a value the nightly refresh has since replaced. D9 details the
transaction shape.

**Option 3 — a new sqlc query returning `vin` + `energy_kwh` — accepted, and is how Option 1 is
implemented.** Options 1 and 3 are not competing alternatives here: Option 1 names the *shape*
(read, then write), Option 3 names the *mechanism* (a dedicated query rather than reusing an
existing one). No existing query returns exactly `vin` + `energy_kwh` scoped by `id` +
`account_id` — `VerifyChargeSession` is an `UPDATE ... RETURNING *`, which would require running
the write before knowing what to write. A new, minimal, purpose-built `:one` query is the
correct mechanism for Option 1's shape.

**Decision: Option 1, implemented as Option 3, refined by D9's locking.** A new query,
`LockSessionForVerification`, returns `vin` + `energy_kwh` for the target row; `VerifySession`
calls it, computes in Go via D8's pure functions, then calls the existing `VerifyChargeSession`
— both inside one transaction (D9). **No new database object is created** — no table, column,
index, constraint, view, or migration. `LockSessionForVerification` is a query change against
the existing schema, exactly the kind `openspec/config.yaml`'s design.md requirement covers
without tripping the `CLAUDE.md` §Pipeline config `Design-Gates: database` gate, which triggers
on new/changed database *objects*, not new *queries* (RM31's own `VerifyChargeSession` query
is the direct precedent for this same distinction).

### D8 — Function decomposition: two pure functions, satisfying D6's offline-only test contract

**Decision.** Two new unexported functions, split by what each needs to know:

```go
// capacity.go — the pure algebraic inverse of derivedEnergyKWh. Needs no ctx, no DB: given
// a capacity, an energy figure, and an end percentage, it returns the start percentage that
// energy implies, or nil per D3/D5.
func derivedStartBatteryPct(capacityKWh float64, energyKWh *float64, endPct *int) *int {
	if energyKWh == nil || endPct == nil {
		return nil
	}
	raw := float64(*endPct) - (*energyKWh)/capacityKWh*100
	rounded := int(math.Round(raw))
	if rounded < 0 || rounded > 100 {
		return nil
	}
	return &rounded
}

// session_verifier.go — the pure trigger gate (D2, conditions 1+2). Needs no ctx, no DB, no
// energy figure: purely a property of what the CALLER supplied.
func needsDerivedStartBatteryPct(startPct, endPct *int) bool {
	return startPct == nil && endPct != nil
}
```

**Why two functions and not one.** `derivedStartBatteryPct` is nil-tolerant on `energyKWh` and
`endPct` for the same reason `derivedEnergyKWh` is nil-tolerant on its two pointer inputs — it
is a complete, standalone algebraic function, safe to call without a precondition check, mirroring
the sibling function's exact contract. `needsDerivedStartBatteryPct` is a **distinct** concern:
whether to even attempt a database read at all, decided purely from what the caller passed in,
before any row is fetched. Folding it into `derivedStartBatteryPct` would force that function to
also accept the caller's `startPct` (a value it does not otherwise need, since it computes a
start percentage rather than receiving one), and — more importantly — would make "a caller-supplied
start percentage is kept, not overwritten" untestable without exercising a database read, since
`derivedStartBatteryPct` alone has no way to represent "don't read the row at all." Splitting the
gate out is what makes every one of D6's required test-contract cases — including the
"user-supplied start is kept" case — assertable with a single, cheap, pure function call and zero
`context.Context` plumbing.

**Composition lives in `VerifySession` (`session_verifier.go`), not in `capacity.go`, mirroring
Context 2's `resolveEnergy` placement exactly:**

```go
startToStore := startBatteryPct
if needsDerivedStartBatteryPct(startBatteryPct, endBatteryPct) {
    // D9: locked read + compute, inside one transaction with the write.
    row, err := qtx.LockSessionForVerification(ctx, chargingdb.LockSessionForVerificationParams{
        ID: id, AccountID: accountID,
    })
    if err != nil {
        return Session{}, fmt.Errorf("charging: verify session: %w", err) // D10
    }
    capacityKWh, err := packCapacityKWh(ctx, row.Vin)
    if err != nil {
        return Session{}, fmt.Errorf("charging: resolving pack capacity: %w", err)
    }
    startToStore = derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh), endBatteryPct)
}
```

`pgFloat8ToFloat64Ptr` already exists (`session_writer.go`) — no new pgtype-conversion helper
is needed.

### D9 — Transaction shape: `pool.Begin` / `WithTx` / `SELECT ... FOR UPDATE` / `Commit`, paid only when deriving

**Decision.** `VerifySession` opens a transaction and locks the row **only** when
`needsDerivedStartBatteryPct` is true. Every other call — the large majority: both percentages
supplied, only start supplied, both nil (clear), or end supplied with no start but the caller's
own start already nil for a reason that doesn't matter (covered by the same gate) — keeps
today's single-statement, non-transactional call to `VerifyChargeSession` against `v.q`
directly, completely unchanged in cost and shape.

**Why `FOR UPDATE` and not a plain `SELECT`.** Context 4/5: the row is also written by
`SessionWriter.MirrorSessions`'s nightly `ON CONFLICT DO UPDATE SET`, which refreshes
`energy_kwh`. `FOR UPDATE` locks the row for the remainder of the transaction, so a concurrent
`MirrorSessions` upsert touching the same row blocks until this transaction commits (or aborts
until it rolls back) — the same guarantee `AccessTokenFor`'s own comment states for its
identical shape: *"the row is locked `FOR UPDATE` so concurrent refreshes… serialize."* Without
it, `energy_kwh` could be refreshed between the read and the write, and the derived
`start_battery_pct` would be computed from a value the row no longer holds by the time it is
stored — silently wrong, not merely stale, since nothing about the stored `start_battery_pct`
would signal it was derived from an old figure.

**Why conditional rather than always transactional.** `AccessTokenFor` and
`MirrorSessions` both transact unconditionally because every call they handle needs the same
read-then-write guarantee. Most `VerifySession` calls do not — they have no derivation to
protect, so wrapping every call in a transaction would add `Begin`/`Commit` round-trip cost to
the common case for a guarantee only the uncommon case needs. This is the read-heavy
`Performance-Profile`'s own logic applied to a write path: pay for the guarantee exactly where
correctness needs it, not uniformly.

**The query itself:**

```sql
-- name: LockSessionForVerification :one
-- Read vin and energy_kwh for one account-scoped charge session, LOCKING the row (FOR
-- UPDATE) for the remainder of the caller's transaction. Called ONLY by VerifySession, and
-- ONLY when it must derive start_battery_pct from energy and the end percentage (design.md
-- D2/D7/D9, MAG-36) -- every other VerifySession call skips this query entirely and runs its
-- single UPDATE outside a transaction, exactly as before this change.
--
-- FOR UPDATE mirrors internal/account's AccessTokenFor and this module's own
-- SessionWriter.MirrorSessions: the read and the later write (VerifyChargeSession, called
-- against the SAME transaction) must observe one consistent row, so a concurrent
-- SessionWriter.MirrorSessions refresh of energy_kwh cannot land between this read and that
-- write and leave the derived percentage computed from a value the row no longer holds
-- (design.md D9).
--
-- WHERE id = @id AND account_id = @account_id mirrors VerifyChargeSession's own scoping
-- exactly; zero rows matched surfaces as pgx.ErrNoRows, wrapped by the caller identically to
-- VerifyChargeSession's own not-found case (design.md D10).
SELECT vin, energy_kwh FROM charge_sessions
WHERE id = @id
  AND account_id = @account_id
FOR UPDATE;
```

No index change: this is a point lookup on the table's `PRIMARY KEY` (`id`), the same access
pattern `VerifyChargeSession`'s own `WHERE id = @id AND account_id = @account_id` already uses
with no dedicated index of its own — the primary key already serves it.

### D10 — Not-found error contract is unchanged, regardless of which of the two queries surfaces it

**Decision.** If `LockSessionForVerification` matches zero rows (unknown `id`, or an `id`
belonging to a different account), it is wrapped identically to `VerifyChargeSession`'s own
not-found case: `fmt.Errorf("charging: verify session: %w", err)`, satisfying
`errors.Is(err, pgx.ErrNoRows)` after unwrapping — the exact shape RM31 D5 already established
and RM31's `TestVerifySession_WrongAccountIsNoOp`/`TestVerifySession_UnknownIDSameErrorShapeAsWrongAccount`
already assert. A caller of `VerifySession` cannot observe, and must not need to care, which of
the two internal queries produced a given not-found error — the public contract is one error
shape for "no such row under this account," exactly as before this change.

Context 6's inventory confirms neither of RM31's existing not-found tests (T6, T7) actually
exercises the new locked-read path — both supply `ptr(10), ptr(20)` (both non-nil), so
`needsDerivedStartBatteryPct` is false and the original single-statement path runs unchanged.
This decision is stated for correctness and future-proofing, not because an existing test
depends on it today.

### D11 — Test file location: `session_verifier_derivation_test.go` (`package charging`), correcting the dispatch's literal instruction

**The dispatch says:** "Add table tests for the new pure function to
`internal/charging/charging_test.go`, next to `TestBatteryDelta_*`." **This cannot be done as
written**, and the reason is structural, not stylistic: `charging_test.go` declares
`package charging_test` — the project's external test package, which by Go's own visibility
rules can only call **exported** symbols. Both `derivedStartBatteryPct` and
`needsDerivedStartBatteryPct` are deliberately **unexported** (D8, mirroring `derivedEnergyKWh`
and `packCapacityKWh`'s own established privacy — `capacity.go`'s doc comment: *"nothing outside
this module may divide by a pack capacity behind the module's back"*). A `package charging_test`
file cannot reference either function; the file would not compile.

**This is exactly the situation `entry_status_test.go` already solved, and the same fix
applies.** That file's own header states it directly: *"Package charging (NOT charging_test):
A7 and A9 exercise `derivedEnergyKWh` and `packCapacityKWh`, which are deliberately
unexported… A1-A5 exercise the exported `RequiredFieldsFor` and could live in `charging_test`,
but are kept in this file so every Group A case lives together."* This change's tests need the
identical accommodation, for the identical reason.

**Decision:** a new file, `internal/charging/session_verifier_derivation_test.go`,
`package charging` (not `charging_test`), following `entry_status_test.go`'s exact precedent —
own header comment stating the scope and why the package is `charging`, table-driven tests for
both new functions. This is a correction of the dispatch's literal file-location instruction,
not of its intent — the tests still live "next to" the module's other pure-function tests, in
spirit and in the same directory, just in the file whose package can actually reach them.
**Exporting either function to make `charging_test.go` reachable was considered and rejected**:
it would break the established privacy invariant the sibling functions (`derivedEnergyKWh`,
`packCapacityKWh`) deliberately hold, purely to satisfy a file-location preference — a worse
trade than adding one small, precedented file.

### D12 — Two pre-existing integration tests break under this change's own intended behavior, and are corrected here

**This decision exists because Context 6's full inventory found it, not because the dispatch
anticipated it.** `grep -rn "VerifySession(ctx" internal/charging/*.go` lists every call site.
Filtering to `startBatteryPct == nil && endBatteryPct != nil` — D2's own trigger condition —
finds exactly two, both in pre-existing, currently-passing `DATABASE_URL`-gated tests:

**1. `TestVerifySession_PartialEndOnlyStillSetsSource`
(`db_session_verifier_integration_test.go`, RM31 Test Contract T3).** Fixture: `seedVerifierSession`
seeds `EnergyKWh: ptrFloat64(30.5)` (shared by several tests in this file — verified none of
the others are affected, below). Call: `v.VerifySession(ctx, acctA, v3ID, nil, ptrIntV(90))`.
Current assertion: `s3.StartBatteryPct != nil` is treated as a failure — i.e. the test expects
`nil`. **Under this change, that expectation is false**: `derivedStartBatteryPct(62.0, ptr(30.5),
ptr(90))` computes `raw = 90 - 30.5/62.0*100 = 90 - 49.193548... = 40.806451...`, rounds
(D4, half away from zero) to **41**, which is in `[0, 100]` — so `start_battery_pct` is now
stored as `41`, not left `NULL`.

**The fix keeps this test's original purpose intact rather than discarding it.** T3's own doc
comment states its purpose: proving `battery_pct_source` is computed correctly "regardless of
which single percentage is supplied" — a fact this change does not touch. Rather than
neutralizing the fixture (e.g. forcing `EnergyKWh` to `nil`, which would dodge the new behavior
instead of proving it), this test becomes the natural, already-in-place regression proof that
the new derivation actually fires end-to-end: update its expected value to the derived `41`,
and its doc comment to state both facts — the source is still set correctly, **and** the start
percentage is now derived rather than left absent, because a non-nil `energy_kwh` is present.
This is a **correction to an existing test's expected value**, not new test authorship — the
narrow exception D6 anticipates.

Every other call in this file was checked and is unaffected: T1/T6/T7/T8 supply both
percentages non-nil; T2 supplies only start (`endBatteryPct` nil — D2's gate never fires); T4
supplies `(nil, nil)` (`endBatteryPct` nil — gate never fires, "clear both" is untouched); T5's
two out-of-range calls both supply a non-nil `startBatteryPct`.

**2. `TestMirrorAndVerify_InferredCapacity_TableCases`, case `"T15"`
(`db_inferred_capacity_sessions_integration_test.go`, MAG-25 Test Contract T15).** Fixture row:
`{id: "T15", energyKWh: ptrFloat64(52.273), startPct: nil, endPct: ptrIntV(100), want: nil}`.
T15's actual purpose (per the file's own doc comment) is isolating `inferred_capacity_kwh_calc`'s
"missing start percentage" guard (MAG-25 D3) from T14's "no kWh fee" guard — it deliberately
keeps `energyKWh` non-nil so only the missing-start condition is under test.

**Under this change, `energyKWh: 52.273` no longer produces a missing start.**
`derivedStartBatteryPct(62.0, ptr(52.273), ptr(100))` computes `raw = 100 -
52.273/62.0*100 = 100 - 84.309677... = 15.690322...`, rounds to **16**, in range. So
`start_battery_pct` becomes `16`, not `NULL` — and because `end_battery_pct = 100 > 16`, the
`GENERATED ALWAYS` column `inferred_capacity_kwh_calc` (MAG-25) now computes a value instead of
staying `NULL`, breaking `want: nil` **through a second, cascading mechanism** the fixture never
anticipated.

**The fix preserves T15's precondition — a genuinely `NULL` `start_battery_pct` — by making
the new derivation itself land out of range, rather than by avoiding the derivation.** T14
already isolates the "energy is nil" case; T15 must stay "energy present, start still ends up
missing" to keep testing something T14 doesn't. Changing `energyKWh` to a value whose derived
start falls outside `[0, 100]` achieves exactly that, through the same D3 guard this change
already documents and offline-tests, with no special-casing of the test itself:

`energyKWh: ptrFloat64(124.0)` (exactly `2 × 62.0`, chosen so the arithmetic is legible):
`derivedStartBatteryPct(62.0, ptr(124.0), ptr(100))` → `raw = 100 - 124.0/62.0*100 = 100 -
200.0 = -100.0` → rounds to `-100` → outside `[0, 100]` → `nil`. `start_battery_pct` stays
`NULL`, exactly as T15's fixture intends, and `want: nil` for `InferredCapacityKWhCalc` is
**unchanged** — only the `energyKWh` input value changes, not the assertion.

Every other case in that table was checked: T13/T17/T18/T19 supply a non-nil `startPct`; T14
supplies a non-nil `startPct` too (deliberately, per its own doc comment, to isolate the
energy-null guard); T16 supplies a nil `endPct` (gate never fires). Only T15 is affected.

**Both corrections are inside `internal/charging/`, inside this change's own sandbox, and are
tasks in this change** — not filed as follow-on work, not deferred. They are
`DATABASE_URL`-gated, so per `Test-Execution-Policy` the worker who makes this edit cannot run
them; the change's `awaiting-user-verification` status covers them exactly as it covers every
other integration-test file this module ships.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing
("author their expected values up front… before the implementation exists"). Tests written
later must assert **this** contract.

### Group A — `derivedStartBatteryPct` (`capacity.go`, package `charging`)

| ID | `capacityKWh` | `energyKWh` | `endPct` | Expected | What it proves |
|---|---|---|---|---|---|
| **A1** | `62.0` | `6.20` | `74` | **`64`** | Normal case — the exact algebraic inverse of `derivedEnergyKWh`'s own "small delta: 64→74 ⇒ 6.20" precedent case. |
| **A2** | `62.0` | `62.00` | `10` | **`nil`** | Negative/out-of-range (D3) — the row's own energy is larger than what an end of 10 can absorb at this capacity; result is deeply negative. |
| **A3** | `62.0` | `nil` | `74` | **`nil`** | Nil energy (D5) — the row has no kWh fee. |
| **A4** | `62.0` | `6.20` | `nil` | **`nil`** | Nil end percentage — defensive; `VerifySession`'s own gate (`needsDerivedStartBatteryPct`) never calls this function with a nil `endPct` in practice, but the pure function is safe standalone, mirroring `derivedEnergyKWh`'s identical nil-tolerance on both its inputs. |
| **A5** | `62.0` | `62.00` | `100` | **`0`** | Exact lower boundary — the largest energy this capacity can absorb over a full 0→100 range, landing exactly on `0`. |
| **A6** | `62.0` | `0.00` | `100` | **`100`** | Exact upper boundary — zero energy added means the pack didn't move; start equals end exactly. |
| **A7** | `62.35` | `0.93525` | `50` | **`49`** | Rounding pin (mirrors `derivedEnergyKWh`'s own A8): raw is exactly `48.5`; `math.Round` must round **away from zero** to `49`, not to the even `48` a banker's-rounding implementation would produce. Deliberately uses a non-round capacity so this is not a no-op case. |

Compare `*int` directly (no float tolerance needed — the function's output type is `*int`).
Assert `nil` explicitly for every nil case, never a zero value.

### Group B — `needsDerivedStartBatteryPct` (`session_verifier.go`, package `charging`)

| ID | `startPct` | `endPct` | Expected | What it proves |
|---|---|---|---|---|
| **B1** | `nil` | `&74` | **`true`** | The trigger fires — start absent, end supplied. |
| **B2** | `&50` | `&74` | **`false`** | A caller-supplied start is never recomputed (D2) — true regardless of what `endPct` is, including a value that would otherwise derive cleanly. |
| **B3** | `nil` | `nil` | **`false`** | Nothing to derive from — matches the existing "clear both" call shape (RM31 T4), untouched by this change. |
| **B4** | `&50` | `nil` | **`false`** | Start-only supply (RM31 T2's shape) — no end to derive from, and a start was supplied anyway. |
| **B5** | `&0` | `&74` | **`false`** | Edge case: an explicit, legitimate `0` is a **non-nil pointer**, not the same as "absent." Proves the check is pointer-nil, not value-zero. |

Compare `bool` directly.

### Group C — corrections to pre-existing integration tests (D12; `DATABASE_URL`-gated, owner-verified)

| ID | File / Test | Change | New expected value |
|---|---|---|---|
| **C1** | `db_session_verifier_integration_test.go` / `TestVerifySession_PartialEndOnlyStillSetsSource` | Expected value only (fixture `EnergyKWh: 30.5` unchanged) | `s3.StartBatteryPct != nil && *s3.StartBatteryPct == 41` (was: `== nil`) |
| **C2** | `db_inferred_capacity_sessions_integration_test.go` / `TestMirrorAndVerify_InferredCapacity_TableCases`, case `"T15"` | Fixture `energyKWh` only, `52.273` → `124.0` | `want: nil` unchanged (now reached via D3's out-of-range guard instead of "never touched") |

### What must NOT change

- **A7 = 49, not 48.** If this ever asserts `48`, the rounding was silently switched to
  round-half-to-even; `math.Round`'s documented behavior is half-away-from-zero and D4 requires
  it.
- **C1's fixture `EnergyKWh` stays `30.5`.** The fix is the expected value, not neutralizing the
  fixture — see D12's reasoning for why that would defeat the test's new purpose as the
  end-to-end regression proof.
- **C2's `want` stays `nil`.** Only the `energyKWh` input changes, specifically so the
  precondition (`start_battery_pct` genuinely `NULL`) stays true under the new code path.

---

## Risks / Trade-offs

1. **D1's accepted provenance blur** — see D1's own paragraph. A future capacity-averaging
   feature over `charge_sessions` cannot filter derived rows out by `battery_pct_source` alone.
   Accepted by the owner; not a defect of this change.
2. **A single, low-frequency race window remains theoretically possible even with `FOR
   UPDATE`**: if `MirrorSessions`' nightly upsert and a human's edit race for the *same* row at
   the *same* moment, one blocks until the other commits — Postgres resolves the ordering, but
   whichever transaction's write lands second still sees whatever the first committed, which is
   correct-by-definition, not a residual bug. Recorded here only because "locking" can sound
   like it prevents concurrency entirely; it prevents an *inconsistent* read, not concurrent
   access itself.
3. **D12's two corrections touch tests from two different, already-archived changes (RM31,
   MAG-25).** This is an unusual but necessary shape for a "new feature" change — the
   alternative (shipping this change without the corrections) would leave `go test ./...`
   failing for the owner the moment they run the suite, which is strictly worse.

## Verification signals

Computed by hand and cross-checked twice for every Test Contract value above (A1–A7, C1, C2) —
this is an artifacts-only pass; no Go was written and no query was run against a live database.
The arithmetic:

| Case | Computation | Result |
|---|---|---|
| A1 | `74 - 6.20/62.0*100 = 74 - 10.0` | `64.0 → 64` |
| A2 | `10 - 62.00/62.0*100 = 10 - 100.0` | `-90.0 → -90` (out of range) |
| A5 | `100 - 62.00/62.0*100 = 100 - 100.0` | `0.0 → 0` |
| A6 | `100 - 0.00/62.0*100 = 100 - 0.0` | `100.0 → 100` |
| A7 | `50 - 0.93525/62.35*100 = 50 - 1.5` | `48.5 → 49` (half away from zero) |
| C1 | `90 - 30.5/62.0*100 = 90 - 49.193548...` | `40.806451... → 41` |
| C2 | `100 - 124.0/62.0*100 = 100 - 200.0` | `-100.0 → -100` (out of range) |

**Not run** (per `Test-Execution-Policy`): `go test ./...`, `make test`, `make test-with-db`,
`make check`, `make sqlc`. This is an artifacts-only pass.
