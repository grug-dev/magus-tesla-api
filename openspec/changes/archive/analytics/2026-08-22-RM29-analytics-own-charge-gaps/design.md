# Design — RM29-analytics-own-charge-gaps

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from the roadmap's own **D1–D10** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md` (cited as **roadmap D1**, …),
> from tier 4's archived decisions (cited as **tier 4 D9**, …), and from the binding
> interview outcomes fixed in the dispatch prompt (cited as **T5-I1**, **T5-I2**,
> **T5-I3**). D1–D3 each carry exactly one interview outcome, named in their heading,
> verbatim in substance and not re-litigated. D4 onward are decisions this artifacts
> pass had to make to turn T5-I1–I3 into a buildable change.

## Context

Tier 4 moved the five per-day consumption figures' *derivation* out of `telemetry`
into `analytics`. `charge_gaps` is the mirror-image leftover: the derivation that
*decides* a vehicle-day is flagged (`analytics.ConsumedByDay`'s D5/D5a rule, already
live in `internal/analytics/consumed.go`) already lives in `analytics`; only the
table that *stores the conclusion*, and the Go vocabulary describing it
(`ChargeGap`, `MissingChargingType`, the `GapWriter` port and its implementation),
still lives in `internal/telemetry`. The table's own migration comment already names
this stale state plainly ("Owned by `internal/telemetry`, written through the
`GapWriter` port by `internal/battery`" — pre-tier-1 language for what is
`internal/analytics` today).

Unlike tier 4, there is **no re-derivation here** — `charge_gaps`' schema, its three
sqlc queries, and `gap_writer.go`'s transaction shape are already correct and already
module-scoped in shape. The whole vertical slice moves together, unchanged in
behavior. The closest precedent is tier 2 (`manualcharge`→`charging`, commit
`fe69cc8`), not tier 4.

Three properties shape the design, one per binding interview outcome:

1. **The table cannot be recreated without risking data loss** (T5-I1) — this
   project's migration runner shares one global version table across every module's
   migrations directory, ordered by directory list, not by version number.
2. **The write port's contract, and `cmd/poller`'s two-step orchestration around it,
   must not change** (T5-I2) — only which package owns the port changes.
3. **`MissingChargingType` is analytics' own domain vocabulary and moves with the
   rest** (T5-I3) — its only two consumers before this change (`ChargeGap` and
   `gap_writer.go`) both leave `telemetry` in the same change, so nothing is left
   behind for it to describe.

## Goals / Non-Goals

**Goals**
- `charge_gaps` (table, migration, three queries, `ChargeGap`, `MissingChargingType`,
  `GapWriter`, `NewGapWriter`, `gap_writer.go`) is owned end-to-end by
  `internal/analytics`, with **zero behavior change** — same schema, same index, same
  transaction shape, same validation, same error strings.
- `cmd/poller`'s nightly reconciliation keeps its exact current two-step shape and
  order (`analytics.Reconcile` then charge-gap reconciliation), its exact per-vehicle
  isolation, and its exact "step-2 error only `continue`s" behavior.
- `internal/telemetry` ends this change with zero references to `ChargeGap`,
  `MissingChargingType`, or `GapWriter`.
- The `DATABASE_URL`-gated integration suite (7 tests) moves with every assertion and
  every expected value unchanged.

**Non-Goals**
- Redesigning `charge_gaps`' schema, its index, or its transaction shape. Nothing
  here is added, dropped, or altered at the DDL level.
- A read port for `charge_gaps` (the future notification feature) — still out of
  scope, unchanged by this tier.
- Collapsing `cmd/poller`'s two-step reconciliation (`Reconcile` then gap
  reconciliation) into one transaction — see D2.
- `charge_sessions` (tier 6), `internal/app` (tier 7).

## Decisions

### D1 — The migration file moves via `git mv`; no new migration, no DDL (carries T5-I1)

`internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` moves,
byte-for-byte, to `internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql`.
Its `-- +goose Up`/`-- +goose Down` content is untouched — not one character of DDL
changes. This is safe, and the "obvious" alternative is not, for a fact specific to
how this project runs goose:

**The fact:** the `Makefile`'s own comment above `MIGRATIONS_DIRS` (line ~26) states
it plainly: goose shares a **single `goose_db_version` table across ALL directories**,
so "the table's current version is global while each dir's migrations are versioned
independently... Ordering this list CANNOT fix that: it orders directories, not the
migrations inside them." Migration `20260815000002` is already recorded as applied in
that single global table. After the file's directory changes, **goose does not
re-run it** — it reads the version number out of the filename, sees that version
already recorded as applied, and skips it in its new location exactly as it did in
its old one. Both `up` loops additionally pass `-allow-missing`
(`make migrate-up`/`migrate-down`), which the same Makefile comment explains exists
precisely because "modules never share tables or FKs... so cross-module version order
carries no meaning" — reinforcing that a directory move carries no re-run risk.

**Why the naive alternative loses data.** The shape that looks obvious from a
distance — telemetry's migration `DROP TABLE charge_gaps`, a *new* analytics
migration `CREATE TABLE charge_gaps` plus an `INSERT INTO ... SELECT` copy — is
**actively unsafe** under this Makefile's own `MIGRATIONS_DIRS` value:

```
MIGRATIONS_DIRS ?= internal/account/db/migrations internal/telemetry/db/migrations internal/charging/db/migrations internal/analytics/db/migrations
```

`migrate-up`'s loop (`Makefile` ~line 88) walks this list **in order**, running
`goose up` to completion in each directory before moving to the next. `telemetry`
is fourth in that list; `analytics` is last. In a single `make migrate-up` run, the
telemetry directory's `DROP TABLE` migration executes and commits **before** the
analytics directory's `CREATE TABLE ... SELECT` migration is even considered — the
copy has no source table left to read from by the time it runs. This is not a
hypothetical: it is the literal, deterministic order this Makefile already runs in
today, for every module in the list.

**Precedent.** Tier 2 (`RM29-charging-rename-from-manualcharge`) moved
`manual_charge_entries`' migration files across a package rename the identical way —
plain `mv` inside the atomic wave, zero DDL change, `sqlc.yaml` and `Makefile`
re-pointed to the new path (commit `fe69cc8`). This change repeats that shape for a
single file rather than a whole `db/` subtree.

**Self-containment check.** `20260815000002_add_charge_gaps.sql` creates exactly one
object (`charge_gaps`) plus its one index (`idx_charge_gaps_account`); no other
migration in `internal/telemetry/db/migrations/` references `charge_gaps` in an
`ALTER TABLE`, a foreign key, or a view — grep-verified against every file in that
directory. The table also carries **no FK** to any other telemetry table by design
(the migration's own header comment: a cross-module FK "would couple telemetry
migrations to the account schema — exactly the coupling `ai/architecture.md` §2
forbids"; referential integrity is upheld by flow, not by constraint). The file is
therefore fully self-contained and safe to relocate as a unit.

**Rejected — DROP-and-recreate with a data copy.** Loses data under this project's
own migration order, as shown above. Also doubles the DDL surface for zero schema
benefit — the "before" and "after" schemas are identical.

**Rejected — leave the table in `telemetry`, expose a cross-module write callback.**
This is exactly the "processed flag" / cross-module-write shape roadmap D7 already
rejected for a different table (`vehicle_snapshots`), for the same reason: it
requires a write into another module's tables from outside that module, the precise
coupling the boundary rule exists to prevent. It also does not solve the actual
complaint (§Context) — `telemetry` would still define `ChargeGap`/
`MissingChargingType`/`GapWriter`, describing analytics' domain vocabulary.

### D2 — The `GapWriter` port moves as-is; the poller's two-step shape is unchanged (carries T5-I2)

`GapWriter`'s interface — one method, `ReconcileWindow(ctx, accountID, teslaID,
start, end, flagged []ChargeGap) error` — moves to `internal/analytics/analytics.go`
verbatim, with `analytics.ChargeGap` replacing `telemetry.ChargeGap` in its
signature. `NewGapWriter(pool *pgxpool.Pool) GapWriter` and its concrete
implementation (`gap_writer.go`'s `gapWriter` struct, `newGapWriter`, and
`ReconcileWindow`'s validate-then-transact body) move to
`internal/analytics/gap_writer.go`, re-targeted from `telemetrydb.Queries` to
`analyticsdb.Queries` — same three query calls
(`ChargeGapDatesByVehicleBetween`/`DeleteChargeGap`/`UpsertChargeGap`), same
validate-before-`tx.Begin` order, same single-transaction commit-or-rollback shape,
same error-string wording. This is an **analytics→analytics port** after the move —
`cmd/poller` is still the only caller, but the port owner and the port's own storage
now agree, which was never true while it sat in `telemetry`.

`cmd/poller/main.go`'s `newNightlyReconciler` keeps its exact current two-step body:
step 1 (`recalculator.Reconcile`) advances `vehicle_metrics`; step 2 reads
`analyticsReader.ConsumedByDay` over the trailing `GapReconciliationWindow` and hands
the flagged days to the gap writer. Only the package qualifiers change —
`telemetry.NewGapWriter`/`telemetry.GapWriter`/`[]telemetry.ChargeGap` become
`analytics.NewGapWriter`/`analytics.GapWriter`/`[]analytics.ChargeGap`.

**Explicitly documented as pre-existing and deliberately UNCHANGED, not a
regression this tier introduces:** `newNightlyReconciler`'s step-2 error handling is
`log.Printf(...); continue` — a vehicle whose gap reconciliation fails is logged and
skipped, while step 1 (`Reconcile`) for that same vehicle has already committed. This
means `vehicle_metrics` can advance for a vehicle in a cycle where `charge_gaps`
does not, and `charge_gaps` stays stale until the next nightly run reconciles it from
scratch (every call recomputes `flagged` fresh — no incremental state to lose). This
behavior existed before this change and is out of scope here.

**Rejected — collapse the two steps into one transaction spanning both modules'
tables.** Considered, because the port and its consumer are now both `analytics`
code and a single transaction is newly *possible* in a way it never was while
`GapWriter` lived in a different module from `Reconcile`. Rejected anyway: this is a
**behavior change beyond an ownership move**, and the owner's standing minimal-scope
preference (`feedback-prefer-minimal-scope`) is to keep this tier to exactly what
T5-I1–I3 ask for. A cross-step transaction is a legitimate future improvement, not
part of this change — noted for the backlog, not implemented here.

### D3 — `MissingChargingType` moves; telemetry ends this change with zero references (carries T5-I3)

`MissingChargingType` (the type, its two constants `MissingChargingTypeManual` /
`MissingChargingTypeSupercharger`, and its doc comment) moves from
`internal/telemetry/telemetry.go` to `internal/analytics/analytics.go`, alongside
`ChargeGap`. Verified today's only two consumers inside `internal/telemetry` are
`ChargeGap.MissingChargingType`'s field type and `gap_writer.go`'s
`UpsertChargeGapParams.MissingChargingType: string(g.MissingChargingType)` binding —
**both leave `telemetry` in this same change** (D1/D2), so nothing remains in
`telemetry` to reference the type. `grep -rn "MissingChargingType"
internal/telemetry/` after this change must return nothing.

`internal/analytics` already references `telemetry.MissingChargingType` as an actual
Go qualifier (not just a doc-comment mention) in three of its own non-test files
today — `analytics.go` (`DayConsumption.MissingChargingType`'s field type),
`consumed.go` (`inferMissingChargingType`'s parameter/return type and its two call
sites), and `mapping.go` (the two `pgtype.Text` mapping helpers' signatures and
bodies) — plus a prose-only mention in `recalculate.go`'s doc comment (no code
reference there; it only calls `pgTextFromMissingType`, already unqualified). Every
code reference becomes **self-referential** after the move: the `telemetry.`
qualifier drops, the bare `MissingChargingType` resolves to the type now defined in
the same package. `internal/analytics/mapping.go`'s `import
"github.com/cristianpena/magus-tesla-api/internal/telemetry"` becomes unused once
this is the only reason it was imported there — `go vet`/`go build` catches this
directly (an unused import is a compile error, not a lint warning), so no manual
audit is needed to find it.

**Two of this module's own pre-existing test files also carry the qualifier**,
found during this artifacts pass and not named in the dispatch's binding outcomes:
`consumed_test.go` (5 occurrences, fixture/assertion values unrelated to
`charge_gaps` itself — they exercise `deriveVehicleMetrics`'s D5/D5a flag-inference
logic from tier 3/4) and `db_integration_test.go` (2 occurrences). Both are
`package analytics` (internal test package) and both keep their `internal/telemetry`
import for unrelated `telemetry.Snapshot`/`telemetry.SuperchargerSession` fixtures —
only the `MissingChargingType` qualifier changes, to the same self-referential form.

**The two out-of-module consumers:**

- `internal/gateway/handlers/history.go`'s `chargeTypeLabel(ctx context.Context, t
  telemetry.MissingChargingType) string` (line ~622) re-points its parameter to
  `analytics.MissingChargingType`. This file already imports `internal/analytics`
  (for `DayConsumption`), so no new import is added — only the qualifier on this
  one parameter and the doc comment above it ("`telemetry.MissingChargingType` is a
  closed 2-value enum" → "`analytics.…`") change.
- `internal/gateway/handlers/history_test.go` — found during this artifacts pass,
  not named in the dispatch's binding outcomes: five `analytics.DayConsumption{...}`
  test fixtures set `MissingChargingType: telemetry.MissingChargingTypeManual` /
  `telemetry.MissingChargingTypeSupercharger`. These become
  `analytics.MissingChargingTypeManual`/`analytics.MissingChargingTypeSupercharger`.
  This is **not** contingent on `telemetry.MissingChargingType` being deleted (D1's
  telemetry-side removal, task 1.7) — it breaks the moment `DayConsumption`'s field
  type itself changes (D3 above, task 1.6), because Go treats
  `telemetry.MissingChargingType` and `analytics.MissingChargingType` as distinct
  named types with no implicit conversion between them, even while both
  definitions exist side by side mid-wave. tasks.md's task 1.10 depends on 1.6, not
  1.7, for exactly this reason.

**Both are leader-owned cross-module integration** (outside both `telemetry`'s and
`analytics`' worker sandboxes), not worker tasks.

## Database Changes (design gate — full schema, rationale, index plan)

> **This change trips the `database` design gate.** There is **no DDL** in this
> change — the migration's `-- +goose Up`/`-- +goose Down` content is byte-for-byte
> unchanged, only its containing directory moves (D1). The owner must confirm this
> before Apply: no schema object is created, altered, or dropped by this change.

### What physically moves

`internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` →
`internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql`, filename and
content identical. For the record, the schema it already carries (unchanged):

```sql
CREATE TABLE charge_gaps (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             UUID NOT NULL,
    tesla_id               BIGINT NOT NULL,
    vin                    TEXT NOT NULL,
    gap_date               DATE NOT NULL,
    missing_charging_type  TEXT NOT NULL CHECK (missing_charging_type IN ('MANUAL', 'SUPERCHARGER')),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charge_gaps_account_tesla_date_unique UNIQUE (account_id, tesla_id, gap_date)
);

CREATE INDEX idx_charge_gaps_account
    ON charge_gaps (account_id, gap_date DESC);
```

No FK (by design — see D1's self-containment check). No `raw_data JSONB` (by design
— stores a Go-computed conclusion, not an external API response, mirroring
`manual_charge_entries`' identical precedent).

### Index Plan

**No index is added, altered, or dropped.** Both index-backed objects move with the
table, unchanged:

| # | Object | Read pattern served | Change |
|---|---|---|---|
| 1 | `charge_gaps_account_tesla_date_unique` (the UNIQUE constraint's own index, `(account_id, tesla_id, gap_date)`) | `UpsertChargeGap`'s `ON CONFLICT` target; `ChargeGapDatesByVehicleBetween`'s and `DeleteChargeGap`'s `WHERE account_id = … AND tesla_id = … AND gap_date …` predicates — all three of `GapWriter.ReconcileWindow`'s own queries | Unchanged — same columns, same constraint, moves with the table |
| 2 | `idx_charge_gaps_account (account_id, gap_date DESC)` | The future notification read port (not built; out of scope) — `account_id`-leading per this project's multi-tenant indexing convention, `gap_date DESC` anticipating newest-first ordering | Unchanged — created by the same migration, moves with it |

`analyticsdb`'s generated code gains three new query methods
(`UpsertChargeGap`/`DeleteChargeGap`/`ChargeGapDatesByVehicleBetween`) and their
`*Params`/row types, identical in shape to `telemetrydb`'s versions being removed —
`make sqlc` regenerates both packages from the moved schema and query text; no
hand-written Go query code changes behavior.

**Deliberately not added:** any new index. Both existing read patterns
(`GapWriter`'s own upsert/delete/list-dates trio, and the still-unbuilt notification
port) are already served by the two objects above, unchanged by this move.

## Full inventory of what moves

| Artifact | From | To |
|---|---|---|
| Migration | `internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` | `internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql` |
| `UpsertChargeGap`, `DeleteChargeGap`, `ChargeGapDatesByVehicleBetween` (+ doc comments) | `internal/telemetry/db/query.sql` | `internal/analytics/db/query.sql` |
| `ChargeGap` struct | `internal/telemetry/telemetry.go` | `internal/analytics/analytics.go` |
| `MissingChargingType` + its two constants | `internal/telemetry/telemetry.go` | `internal/analytics/analytics.go` |
| `GapWriter` interface + `NewGapWriter` constructor | `internal/telemetry/telemetry.go` | `internal/analytics/analytics.go` |
| `gapWriter` struct + `newGapWriter` + `ReconcileWindow` implementation | `internal/telemetry/gap_writer.go` | `internal/analytics/gap_writer.go` |
| DB-integration suite (7 tests) | `internal/telemetry/db_gap_writer_integration_test.go` | `internal/analytics/db_gap_writer_integration_test.go` |
| sqlc regeneration | `telemetrydb` loses `ChargeGap*` symbols | `analyticsdb` gains them (`make sqlc`, both modules) |

Every reference is re-pointed in the same change (D2, D3): `cmd/poller/main.go`,
`internal/gateway/handlers/history.go`, and `internal/gateway/handlers/history_test.go`
(the last found during this artifacts pass, not named in the dispatch's binding
outcomes — see D3's "out-of-module consumers").

## Test Contract (restated before implementation, per `ai/go-conventions.md`)

Not new tests — the existing 7-test `DATABASE_URL`-gated suite moves verbatim from
`internal/telemetry/db_gap_writer_integration_test.go` to
`internal/analytics/db_gap_writer_integration_test.go`, repackaged `package
telemetry` → `package analytics`, re-targeted `newGapWriter(pool)` against
`analyticsdb.Queries` instead of `telemetrydb.Queries`. Every assertion below is
**restated from the existing file**, per `ai/go-conventions.md`'s "author expected
values up front" convention, so the move can be verified against a fixed target
rather than "whatever the moved code happens to produce."

All fixtures use fresh `uuid.New()` account IDs and small positive `teslaID`s in the
`910001`–`910009` range (collision-free against any real data). `MissingChargingTypeManual`/
`MissingChargingTypeSupercharger` are `analytics.MissingChargingType`'s two values
after the move (D3).

1. **Upsert is idempotent; `created_at` survives, `updated_at` advances.** Flag
   `(accountID, teslaID=910001, VIN="V1", date=2026-08-10, MANUAL)` inside window
   `[2026-08-01, 2026-08-15]`. First call: exactly 1 row, `MissingChargingType="MANUAL"`,
   `CreatedAt` non-zero. Second call with the *same* flagged day (after a ≥10ms sleep):
   still exactly 1 row (no duplicate), `MissingChargingType` unchanged, `CreatedAt`
   byte-for-byte equal to the first call's value, `UpdatedAt` strictly later.

2. **A resolved day is deleted; a still-flagged day in the same call is untouched.**
   Seed two flagged days (`2026-08-05` keep, `2026-08-10` resolve) in window
   `[2026-08-01, 2026-08-15]` → 2 rows. Reconcile the same window with only the keep
   day flagged → the resolve day's row is gone, the keep day's row is still present,
   exactly 1 row remains.

3. **An empty flagged set clears every previously-flagged day in the window.** Seed
   two flagged days (one MANUAL, one SUPERCHARGER) → 2 rows. Reconcile the same
   window with `flagged = []ChargeGap{}` → 0 rows.

4. **Tenant isolation: two different accounts, the same `gap_date`.** Account A
   flags `(teslaA=910004, 2026-08-10, MANUAL)`; account B independently flags
   `(teslaB=910005, 2026-08-10, SUPERCHARGER)` — same calendar day, disjoint
   accounts. After both calls: A's row is present and still `MANUAL`; B's row is
   present and still `SUPERCHARGER`; B's call did not write anything under A's
   account before A ever called, and A resolving its own day (empty flagged) leaves
   B's row for the same date completely untouched (0 rows for A, 1 row still for B).

5. **A mis-scoped flagged entry rejects the WHOLE call, writing nothing — not even
   the correctly-scoped entries in the same slice.** Three sub-cases, each a 2-entry
   `flagged` slice whose first entry is correctly scoped
   `(accountID, teslaID=910006, 2026-08-05, MANUAL)` and whose second entry is
   mis-scoped one way: (a) wrong `AccountID`, (b) wrong `TeslaID` (`teslaID+1`), (c)
   `Date` (`2026-09-01`) outside the call's own window `[2026-08-01, 2026-08-15]`.
   Every sub-case: `ReconcileWindow` returns a non-nil error, and **zero** rows exist
   afterward under the call's own scope, under the mis-scoped `tesla_id`, and under
   the mis-scoped `account_id` — including the entry that was individually
   well-formed.

6. **Reconciling one window never touches a row outside it.** Seed
   `(teslaID=910007, 2026-07-01, MANUAL)` via a call whose own window
   `[2026-06-25, 2026-07-05]` contains it. Then reconcile a disjoint window
   `[2026-08-01, 2026-08-15]` with a flagged set that only mentions
   `2026-08-10` (never `2026-07-01`). The `2026-07-01` row is still present
   afterward, with `MissingChargingType`, `CreatedAt` AND `UpdatedAt` all
   byte-for-byte unchanged from before the second call.

7. **Two vehicles in the same account resolve independently, even on the same
   overlapping `gap_date`.** Within one account, vehicle A
   (`teslaA=910008`) flags `2026-08-10` (shared) and `2026-08-05` (A-only);
   vehicle B (`teslaB=910009`) flags `2026-08-10` (shared) and `2026-08-12`
   (B-only) — each via its own `ReconcileWindow` call over the same window
   `[2026-08-01, 2026-08-15]`. A resolving its own shared day must not affect B's
   row for that same date, and vice versa — distinct from scenario 4 above, which
   separates two *accounts*; this scenario separates two *vehicles inside the same
   account*, guarding `tesla_id` specifically (not just `account_id`) in every one
   of the three queries' predicates and the UPSERT's conflict target.

### What must NOT change during the move

- No test's fixture data, account/vehicle ID ranges, dates, or expected row counts.
- No test's name.
- The `gapWriter`/`newGapWriter` unexported names (only their package changes).
- `chargeGapRow`, `fetchChargeGap`, `countChargeGaps`, `cleanupChargeGaps`,
  `cleanupChargeGapsByAccount` — all move verbatim as this file's own test helpers.

## Risks / Trade-offs

- **This is an atomic, not a phased, move.** Unlike tier 4 (which could add the new
  telemetry port *before* removing the old columns, keeping the tree green at every
  intermediate commit), this change cannot be split that way: `analytics/db/query.sql`
  cannot reference `charge_gaps` until the migration that creates it sits inside
  `analytics`' own schema directory (sqlc validates queries against the schema
  directory for that module's entry in `sqlc.yaml`), and the moment the migration
  leaves `telemetry`'s schema directory, `telemetry/db/query.sql`'s three
  `charge_gaps` queries stop validating there too. The migration move and both
  modules' query-file edits are therefore one inseparable unit — mirroring tier 2's
  identical "no safe intermediate state" call for its own rename. See tasks.md's
  wave notes for exactly which sub-tasks land together.
- **`cmd/poller`, `internal/gateway/handlers/history.go`, and
  `internal/gateway/handlers/history_test.go` reference the moved package
  directly (no interface insulates them).** All three compile only once the
  corresponding half of the move has landed — they cannot be split ahead of or
  behind the module-level move without an intermediate non-compiling state.
- **The DB-integration suite is the entire test surface for this table.** There is
  no offline/pure-function test to catch a transcription error in the move; a typo
  in a re-typed assertion would only surface when the owner runs
  `go test ./internal/analytics/...` against a real database. Mitigated by moving
  the file with `mv` (preserving every line) rather than re-typing it, and by this
  design's Test Contract restating every expected value for the owner to cross-check
  against the moved file.
- **`internal/telemetry/AGENTS.md` and `internal/analytics/AGENTS.md` both need
  edits** (the former loses a "Data ownership" entry and its `GapWriter` port
  description; the latter gains both) — docs-track-structural-change, in scope, in
  tasks.md's docs wave.

## Verification signals

Per the Test-Execution-Policy: the assistant runs and reports `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, the standalone
guards (`make ui-guard` / `make i18n-guard` / `make money-guard` — all three
expected no-ops: no gateway markup beyond one parameter-type re-point, no new
user-facing string, no monetary column), and `make sqlc` after the migration and
query moves land. `make migrate-status` is run and reported — the check that catches
a bad directory move while the build stays green (tier 2's lesson). `openspec
validate --changes --strict` is run and reported.

The owner alone runs the suite:

```
go test ./internal/telemetry/... ./internal/analytics/... ./internal/gateway/... ./cmd/...
```

or the full suite:

```
go test ./...
```

Until the owner runs one of these and reports the result, this tier's implementation
status is **awaiting-user-verification**, never "done".
