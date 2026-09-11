# Design — analytics-rekey-charge-gaps-on-tesla-id

Required because this change touches the database (`openspec/config.yaml` design gate).

## Overview

One table, `analytics.charge_gaps`, moves its identity key from
`(account_id, tesla_id, gap_date)` to `(tesla_id, gap_date)`. `tesla_id` already
uniquely names one vehicle; carrying `account_id` too added no real isolation,
only an extra parameter every query and the `GapWriter` port had to carry.

Four pieces, in this order: migration, queries + `sqlc generate`, port +
implementation, callers and their tests, then docs.

## D-INDEX — no replacement index for the dropped `idx_charge_gaps_account`

**Decision:** the migration drops `idx_charge_gaps_account (account_id, gap_date
DESC)` and does **not** replace it with `idx_charge_gaps_date (tesla_id, gap_date
DESC)` or any other new index. The new `UNIQUE (tesla_id, gap_date)` constraint's
own index is the only index this table has after this change.

**Rationale — the new UNIQUE index already serves every real query:**

| Query | Access pattern | Served by `(tesla_id, gap_date)` how |
|---|---|---|
| `UpsertChargeGap` | `ON CONFLICT (tesla_id, gap_date)` | the conflict target IS this index |
| `DeleteChargeGap` | `WHERE tesla_id = $1 AND gap_date = $2` | point lookup, leading columns equal |
| `ChargeGapDatesByVehicleBetween` | `WHERE tesla_id = $1 AND gap_date BETWEEN $2 AND $3` | `tesla_id` pinned by equality, `gap_date` range — one contiguous forward index scan |

No fourth query exists. There is still no read port on this table (unchanged by
this ticket).

**Rejected: `idx_charge_gaps_date (tesla_id, gap_date DESC)`.** The ticket's own
text asks for this, but the user reviewed and rejected it (confirmed
2026-09-11). The dropped `idx_charge_gaps_account` existed for a **future**
account-wide notification feature that was never built — its own column
comment said so. After this change `account_id` is gone from the table, so
that future index cannot survive in any form; the feature, if built later,
would need its own index shaped for whatever key it actually reads by. A
`(tesla_id, gap_date DESC)` index today would be redundant with the UNIQUE
index for every query above: with `tesla_id` pinned by equality, Postgres
reads the UNIQUE `(tesla_id, gap_date)` index **backward** to produce
`gap_date DESC` order at no extra cost — a second index would just be
maintained by every nightly upsert and delete while no query ever reads it.
This project's read-heavy Performance-Profile calls for indexing aggressively
**for reads that exist** — an index nothing reads is write cost with no read
benefit, the opposite of that profile's intent.

**If a real read pattern needing `tesla_id`-ordered-by-date-only (no exact
date bound) ever appears, add the index then, against that actual query** —
not speculatively now.

## D-MIGRATION — duplicate collapse before the new constraint

**Decision:** before dropping the old constraint and adding the new one,
delete any row that would violate the new `UNIQUE (tesla_id, gap_date)`,
keeping the row with the earliest `created_at` for each `(tesla_id, gap_date)`
pair.

**Why a collapse step is needed at all:** the old constraint was
`(account_id, tesla_id, gap_date)`. If a vehicle were ever re-keyed to a
different account while it still had an outstanding charge-gap row (or if two
accounts both briefly claimed the same `tesla_id`, however unlikely), two rows
could exist for the same `(tesla_id, gap_date)` under different `account_id`
values. The new constraint would then reject the migration outright with a
uniqueness violation.

**Why keep earliest `created_at`:** `created_at` records when a vehicle-day
was FIRST flagged (see the table's own column comment) — it answers "how
long has this been outstanding," which is the more informative value to
preserve for a future consumer of this ledger. The later duplicate's
`updated_at`/`missing_charging_type` carry no information the surviving row's
next nightly re-upsert won't immediately refresh anyway (`ReconcileWindow`
re-derives and re-upserts every still-flagged day on every run).

**Why this is defensive, not expected:** the platform is one-car-per-account
today (verified: `internal/account`'s `Vehicle`/`OwnedVehicle` model gives
each `tesla_id` exactly one owning account at a time). A real database is
expected to have **zero** rows deleted by this step. The collapse exists so
the migration is safe to run even if that assumption is ever violated, not
because a violation is anticipated.

### Migration SQL

New file: `internal/analytics/db/migrations/20260911000001_rekey_charge_gaps_on_tesla_id.sql`.

```sql
-- +goose Up
-- Collapse any (tesla_id, gap_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row whose created_at is earliest --
-- created_at records when a vehicle-day was first flagged, the more useful
-- value to preserve. On a database where every tesla_id has always belonged
-- to one account (true today), this deletes zero rows; it exists only so a
-- future or unusual state cannot make this migration fail outright.
DELETE FROM analytics.charge_gaps t
WHERE EXISTS (
    SELECT 1 FROM analytics.charge_gaps o
    WHERE o.tesla_id = t.tesla_id
      AND o.gap_date = t.gap_date
      AND (o.created_at < t.created_at
           OR (o.created_at = t.created_at AND o.id < t.id))
);

ALTER TABLE analytics.charge_gaps
    DROP CONSTRAINT charge_gaps_account_tesla_date_unique;

-- Schema-qualified: an index name is resolved through search_path, and
-- goose does not guarantee analytics is on it. IF EXISTS because
-- DROP COLUMN account_id below would remove this index anyway.
DROP INDEX IF EXISTS analytics.idx_charge_gaps_account;

ALTER TABLE analytics.charge_gaps
    DROP COLUMN account_id;

ALTER TABLE analytics.charge_gaps
    ADD CONSTRAINT charge_gaps_tesla_date_unique UNIQUE (tesla_id, gap_date);

-- +goose Down
ALTER TABLE analytics.charge_gaps
    DROP CONSTRAINT charge_gaps_tesla_date_unique;

-- account_id comes back NULLABLE, not NOT NULL: the DROP COLUMN in Up threw
-- the values away, so there is nothing to backfill a NOT NULL constraint
-- with on a populated table. Down restores the column's shape for a
-- same-session rollback (before any new row is written), not the original
-- constraint strength on a table that already has data -- the same
-- limitation every DROP COLUMN in this codebase's migrations has on Down.
ALTER TABLE analytics.charge_gaps
    ADD COLUMN account_id UUID;

CREATE INDEX idx_charge_gaps_account
    ON analytics.charge_gaps (account_id, gap_date DESC);

ALTER TABLE analytics.charge_gaps
    ADD CONSTRAINT charge_gaps_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, gap_date);
```

The table's `COMMENT ON TABLE` / `COMMENT ON COLUMN` text lives entirely in
the **original** migration file (`20260815000002_add_charge_gaps.sql`) and is
not touched here — that file is never edited (`ai/go-conventions.md`'s
"historic migrations are never edited" precedent), and this project's own
rule is explicit that a stale table comment is never a finding worth a
migration on its own.

## Queries (`internal/analytics/db/query.sql`)

`UpsertChargeGap`, `DeleteChargeGap`, `ChargeGapDatesByVehicleBetween` each
drop `account_id` from their parameter list and predicate.
`UpsertChargeGap`'s `ON CONFLICT` target changes from
`(account_id, tesla_id, gap_date)` to `(tesla_id, gap_date)`.

The three queries' own doc comments currently cite `design D-Table2`, `roadmap
D7b`, and `design.md Index Plan` (from the original RM28/RM29 changes, now
archived and frozen). When editing these three comments, replace each
citation with the reason itself — do not add a new citation to this change's
own ID in its place. See `tasks.md` T2 for the exact wording each comment
needs.

Then run `sqlc generate` (`make sqlc`) so `internal/analytics/db` regenerates
`UpsertChargeGapParams`, `DeleteChargeGapParams`, and
`ChargeGapDatesByVehicleBetweenParams` without an `AccountID` field.

## Port and implementation (`internal/analytics/analytics.go`, `gap_writer.go`)

### D-CHARGEGAP-FIELD — `ChargeGap` drops its `AccountID` field

**Decision:** `ChargeGap` (the domain type) loses its `AccountID uuid.UUID`
field, not just the `ReconcileWindow` parameter. Only `TeslaID`, `VIN`,
`Date`, and `MissingChargingType` remain.

**Rationale:** `ChargeGap.AccountID` existed for two reasons per its own doc
comment: (1) it was compared against `ReconcileWindow`'s `accountID`
parameter for tenant-isolation validation, and (2) it was kept so the same
shape could serve a future account-scoped notification read port. Reason (1)
disappears with the parameter. Reason (2) no longer holds either: a future
read port on this table can only read what the table stores, and the table
no longer stores `account_id` — so `ChargeGap` carrying an `AccountID` a
caller sets but nothing validates, stores, or could ever read back would be
a field that is silently ignored. That is worse than removing it: a
future caller could reasonably assume setting it does something. Ticket
text does not mention this field explicitly, but it is required by the
ticket's own "Done when" gate — no `account_id` in the port — since a field
literally named `AccountID` on the port's own value type would fail that
plain reading.

**Consequence for the mis-scope validation:** `ReconcileWindow`'s per-element
validation loop currently checks
`g.AccountID != accountID || g.TeslaID != teslaID`. With both the parameter
and the field gone, it becomes a single check: `g.TeslaID != teslaID`. The
date-bounds check (`g.Date.Before(start) || g.Date.After(end)`) is unchanged.

### `GapWriter.ReconcileWindow` new signature

```go
ReconcileWindow(ctx context.Context, teslaID int64, start, end time.Time, flagged []ChargeGap) error
```

`gap_writer.go`'s implementation: drop `accountID` from the function
signature, from the validation loop's comparison, and from every
`analyticsdb.*Params` literal it builds (`ChargeGapDatesByVehicleBetweenParams`,
`DeleteChargeGapParams`, `UpsertChargeGapParams`).

## Caller (`internal/app/processor.go`)

`recalculateAnalytics`'s `ReconcileWindow` call drops `v.AccountID`:

```go
if err := p.gapWriter.ReconcileWindow(ctx, v.TeslaID, start, end, flagged); err != nil {
```

The loop building `flagged` stops setting `AccountID` on each
`analytics.ChargeGap{...}` literal (the field no longer exists).

## D-SCOPE — cross-module touches are mechanical, not a roadmap

The owning module is `internal/analytics`. `internal/app/processor.go` (one
call-site line) and `internal/app/processor_test.go` (one fake's method
signature) change only because the port signature changed — the same
mechanical consequence any Go interface change has on its callers. This does
not make the change cross-module in the sense that would require a roadmap:
`internal/app` owns no data here and makes no decision of its own.

## D-TESTFIX — keeping the build and vet green, not writing new tests

**D-TESTS (binding, from the ticket): unit tests are excluded for this
change.** No new test file, and no new test case testing new behavior, is
written. But `go vet ./...` compiles every `_test.go` file, and the "Done
when" gate requires it stay clean — so every existing test that references
the changed signature or the changed field must be updated to compile and to
keep asserting what it already asserted, using the new shape. Updating an
existing test to match a changed signature is not "writing a test"; it is
keeping the build green, exactly like `go vet`'s own job.

Three files need this treatment — one more than the two named in the
dispatch (the third was found during this design's own verification and is
reported to the leader as a correction, not assumed silently):

### `internal/app/processor_test.go`

`fakeGapWriter.ReconcileWindow`'s signature drops `_ uuid.UUID` (the
`accountID` parameter):

```go
func (fakeGapWriter) ReconcileWindow(_ context.Context, _ int64, _, _ time.Time, _ []analytics.ChargeGap) error {
	return nil
}
```

No other change in this file: the fake's body was already a no-op, and no
other test in this file constructs an `analytics.ChargeGap` literal.

### `internal/analytics/db_gap_writer_integration_test.go` (found by this
design, not in the ticket)

This file is `GapWriter`'s own DB-integration test, inside the analytics
module. It is far more coupled to `account_id` than the dispatch's "verified
facts" anticipated — it is not a one-line signature fixup:

- Every `ReconcileWindow(ctx, accountID, teslaID, ...)` call drops the
  `accountID` argument.
- Every `ChargeGap{AccountID: ..., TeslaID: ..., ...}` literal drops the
  `AccountID:` field.
- `cleanupChargeGaps`, `countChargeGaps`, `fetchChargeGap` (test helpers)
  drop their `accountID uuid.UUID` parameter and their SQL's
  `account_id = $N` predicate, keyed on `tesla_id` (+ `gap_date` where
  applicable) alone.
- `TestGapWriter_ReconcileWindow_TenantIsolation_NeverTouchesOtherAccountVehicle`
  exercised isolation between two different `(account, vehicle)` pairs. Two
  different vehicles are still two different `tesla_id`s regardless of
  account, so the SAME assertion (vehicle A's reconciliation never touches
  vehicle B's rows) still holds and is still worth testing — rename it to
  `TestGapWriter_ReconcileWindow_DifferentVehiclesNeverInterfere` (merging it
  conceptually with the existing, separate
  `TestGapWriter_ReconcileWindow_DifferentVehiclesIndependent` test is
  **not** required — keep both if their scenarios remain distinct, or
  consolidate if the implementer finds them now identical; either is
  acceptable, this is not a new-behavior test).
- `TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing`'s
  `"wrong AccountID on second entry"` sub-case tests validation that no
  longer exists (D-CHARGEGAP-FIELD removes the account check). Delete this
  sub-case. Keep the other two sub-cases (`"wrong TeslaID"`,
  `"date outside window"`) — that validation is unchanged.

This is still test-fixup, not new-behavior authoring: every remaining
assertion already existed and already passed before this change; only the
key used to express "which vehicle" changes from an
`(account, vehicle)` pair to a bare `tesla_id`.

### `internal/charging/db_session_integration_test.go` — re-verified, no
change needed

The dispatch listed this file as needing an update. Direct inspection during
this design found it does **not** call `ReconcileWindow`, does not construct
an `analytics.ChargeGap`, does not import `internal/analytics`, and does not
reference the `account_id` column of `charge_gaps` — the file has exactly one
line mentioning charge gaps at all, a doc comment: `"mirror tier 5's
fetchChargeGap/countChargeGaps precedent —
internal/analytics/db_gap_writer_integration_test.go"`. That is a naming
precedent note about a **different** file, not a compile dependency. `tasks.md`
still includes a cheap verification task for this file (build/vet after the
port change) so this claim is checked, not just asserted, but no edit is
expected.

## Test contract (authored before implementation, per this project's testing
convention)

Concrete expected values for the migration's duplicate-collapse step, so the
migration is verified against something written before the code exists:

**Given** two `charge_gaps` rows both with `tesla_id = 445566`,
`gap_date = 2026-08-01`:
- Row X: `account_id = <acct-old>`, `created_at = 2026-08-02T03:30:00Z`
- Row Y: `account_id = <acct-new>`, `created_at = 2026-08-05T03:30:00Z`

**When** the migration's `DELETE` step runs.

**Then** Row Y is deleted (later `created_at`); Row X survives with its
original `created_at` unchanged. After the migration, `SELECT COUNT(*) FROM
analytics.charge_gaps WHERE tesla_id = 445566 AND gap_date = '2026-08-01'`
returns exactly 1, and its `created_at` equals Row X's original value
(`2026-08-02T03:30:00Z`).

**Given** a database with no duplicate `(tesla_id, gap_date)` pairs (the
expected real-world state, since the platform is one-car-per-account today).

**When** the migration runs.

**Then** the `DELETE` affects 0 rows, and every existing row's `id`,
`tesla_id`, `vin`, `gap_date`, `missing_charging_type`, `created_at`, and
`updated_at` are byte-identical to before the migration (only `account_id` is
gone and the constraint/index names changed).

`GapWriter.ReconcileWindow`'s own behavioral contract (upsert-and-delete
semantics, window scoping, mis-scope rejection) is unchanged by this ticket —
only the identity key it operates on changes from
`(account_id, tesla_id)` to `tesla_id` alone. The existing scenarios in
`internal/analytics/db_gap_writer_integration_test.go` already cover that
contract; D-TESTFIX above lists exactly which of them need updating to the
new key shape, not new scenarios.

## Docs

- `internal/analytics/AGENTS.md` — "Data ownership" section's `charge_gaps`
  description currently implies the `(account_id, tesla_id, gap_date)` key
  via its cross-reference to the KB guide; no direct schema detail lives in
  AGENTS.md itself today, so check for any stray mention of `account_id` on
  this table (none expected — verify, don't assume) and fix only what is
  actually there.
- `kkpa/context/entities/vehicle-metrics/guide.md` — the `charge_gaps` column
  list explicitly states `account_id UUID NOT NULL`,
  `UNIQUE (account_id, tesla_id, gap_date)`
  (`charge_gaps_account_tesla_date_unique`), and
  `idx_charge_gaps_account (account_id, gap_date DESC)`. All three go stale
  the moment this migration lands. Update this section to the new column
  list, the new constraint name and columns, and the fact that no second
  index exists (with D-INDEX's one-line reason, not the full rationale — the
  guide is a map, not a copy of this file).
- This change's own `specs/analytics/spec.md` — the "Charge Gap Ledger"
  requirement is rewritten as a `MODIFIED Requirements` delta (see that file):
  every scenario phrased in terms of "account" scoping is rephrased in terms
  of "vehicle" scoping, and the "Ledger rows for different accounts are
  independent" scenario is removed (there is no longer an account dimension
  to be independent across — `TestGapWriter_ReconcileWindow_DifferentVehiclesIndependent`
  already covers the vehicle-independence claim that survives).
- The "Module-Scoped Database Schema" requirement elsewhere in
  `openspec/specs/analytics/spec.md` names
  `UNIQUE (account_id, tesla_id, gap_date)` and `idx_charge_gaps_account` in a
  scenario about the **already-completed** RM39 schema-move migration. That
  scenario is a historical snapshot of what that migration preserved, not a
  standing claim about `charge_gaps`'s current shape — the same precedent
  `RM39-analytics-fix-watermark-vocabulary` already established for its own
  vocabulary snapshot ("this requirement's own claim... remains true and is
  deliberately not rewritten"). Do not edit that requirement.

## Risks

- **Migration order matters only within this file** — the collapse `DELETE`
  must run before the `DROP CONSTRAINT`/`ADD CONSTRAINT` pair, which this
  migration's own statement order already guarantees (goose runs a migration
  file's statements in the order written).
- **No cross-module migration ordering concern** — this migration touches
  only `analytics.charge_gaps`, a table no other module's migrations read or
  write (verified: `ai/go-conventions.md`'s only documented cross-module
  migration read is `telemetry` → `charging`, unrelated to this table).
- **`make migration-guard` / `make boundary-guard`** — unaffected by this
  change's shape (no new cross-module import, no schema file outside
  `internal/analytics/db/migrations/`); run both as part of this change's
  own verification (`tasks.md`).
