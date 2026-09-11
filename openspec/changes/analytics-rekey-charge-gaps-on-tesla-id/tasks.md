# Tasks — analytics-rekey-charge-gaps-on-tesla-id

All work is inside `internal/analytics`, plus two mechanical touches in
`internal/app`. Sequential dependency chain for the code path (migration →
queries/sqlc → port/implementation → callers/tests), because Go cannot
compile against `sqlc`-generated types until they are regenerated, and the
port cannot be re-implemented until the generated types exist. The docs
sub-task is independent once the final shape (T1–T4) is settled and can be
written in parallel with T5/T6/T7 by a separate agent.

## T1 — Migration

Depends on: nothing.

- [x] 1.1 Write `internal/analytics/db/migrations/20260911000001_rekey_charge_gaps_on_tesla_id.sql`
      exactly as specified in `design.md`'s "Migration SQL": the duplicate-collapse
      `DELETE`, `DROP CONSTRAINT charge_gaps_account_tesla_date_unique`,
      `DROP INDEX IF EXISTS analytics.idx_charge_gaps_account`, `DROP COLUMN account_id`, then
      `ADD CONSTRAINT charge_gaps_tesla_date_unique UNIQUE (tesla_id, gap_date)`,
      plus the `-- +goose Down` half.
- [x] 1.2 Do not touch `internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql`
      — historic migrations are never edited, per `ai/go-conventions.md`.
- [x] 1.3 Run `make migration-guard`. Expect clean (no cross-module schema
      reference introduced).

## T2 — Queries + sqlc

Depends on: T1 (schema must exist before `sqlc generate` can validate the
queries against it).

- [x] 2.1 In `internal/analytics/db/query.sql`, remove `account_id` from
      `UpsertChargeGap`'s column list, VALUES list, and change its
      `ON CONFLICT` target from `(account_id, tesla_id, gap_date)` to
      `(tesla_id, gap_date)`.
- [x] 2.2 Remove `account_id` from `DeleteChargeGap`'s `WHERE` clause.
- [x] 2.3 Remove `account_id` from `ChargeGapDatesByVehicleBetween`'s `WHERE`
      clause.
- [x] 2.4 Rewrite each of the three queries' doc comments so they no longer
      cite `design D-Table2`, `roadmap D7b`, or `design.md Index Plan` (those
      point at the now-archived, frozen `RM28`/`RM29` design docs) — replace
      each citation with the reason itself, following this change's own
      `design.md` D-INDEX and D-MIGRATION sections for the substance. Do not
      cite this change's own name or any decision ID in the new comment text
      (`ai/go-conventions.md`'s code-comment rule).
- [x] 2.5 Run `make sqlc` (or `sqlc generate`). Confirm
      `UpsertChargeGapParams`, `DeleteChargeGapParams`, and
      `ChargeGapDatesByVehicleBetweenParams` in the regenerated
      `internal/analytics/db/query.sql.go` no longer have an `AccountID`
      field.

## T3 — Port and implementation

Depends on: T2 (needs the regenerated `analyticsdb` types).

- [x] 3.1 In `internal/analytics/analytics.go`: remove the `AccountID
      uuid.UUID` field from the `ChargeGap` struct (design.md
      D-CHARGEGAP-FIELD) and update its doc comment (the two-reasons
      paragraph that currently justifies carrying `AccountID` no longer
      applies — replace it, do not just delete it, so a reader knows why the
      field is gone rather than assuming an oversight).
- [x] 3.2 In the same file, change `GapWriter.ReconcileWindow`'s signature to
      drop `accountID uuid.UUID`:
      `ReconcileWindow(ctx context.Context, teslaID int64, start, end time.Time, flagged []ChargeGap) error`.
      Update the interface's doc comment: drop the paragraph describing the
      `accountID`/`AccountID` mis-scope check, keep the `teslaID`/`TeslaID`
      and date-window mis-scope checks as described.
- [x] 3.3 In `internal/analytics/gap_writer.go`, update
      `(w *gapWriter) ReconcileWindow` to the new signature. Change the
      validation loop to `if g.TeslaID != teslaID { ... }` (drop the
      `g.AccountID != accountID ||` clause and its half of the error
      message). Drop `AccountID` from the `analyticsdb.*Params` literals for
      `ChargeGapDatesByVehicleBetweenParams`, `DeleteChargeGapParams`, and
      `UpsertChargeGapParams`.
- [x] 3.4 `go build ./internal/analytics/...` and `go vet
      ./internal/analytics/...` — expect these to fail until T4 also lands
      (the package's own test files still reference the old shapes); that is
      expected at this point in the sequence, not a regression to fix here.

## T4 — Analytics' own test fixups (not new tests — see design.md D-TESTFIX)

Depends on: T3.

- [x] 4.1 In `internal/analytics/db_gap_writer_integration_test.go`: update
      `cleanupChargeGaps`, `countChargeGaps`, `fetchChargeGap` to drop their
      `accountID uuid.UUID` parameter and the `account_id = $N` predicate
      from their SQL, keyed on `tesla_id` (and `gap_date` where the original
      already filtered on it) alone.
- [x] 4.2 Update every `ReconcileWindow(ctx, accountID, teslaID, ...)` call in
      this file to drop the `accountID` argument, and every
      `ChargeGap{AccountID: ..., ...}` literal to drop the `AccountID:` field.
- [x] 4.3 Rename
      `TestGapWriter_ReconcileWindow_TenantIsolation_NeverTouchesOtherAccountVehicle`
      to `TestGapWriter_ReconcileWindow_DifferentVehiclesNeverInterfere` and
      update its body per T4.1/T4.2 — the underlying claim (vehicle A's
      reconciliation never touches vehicle B's rows) is unchanged and still
      worth asserting via two different `tesla_id`s.
- [x] 4.4 In
      `TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing`,
      delete the `"wrong AccountID on second entry"` sub-case (design.md
      D-CHARGEGAP-FIELD: that validation no longer exists). Keep `"wrong
      TeslaID"` and `"date outside window"` unchanged apart from the
      mechanical argument-list update.
- [x] 4.5 `go build ./internal/analytics/...` and `go vet
      ./internal/analytics/...` — expect clean now.

## T5 — Cross-module caller

Depends on: T3.

- [x] 5.1 In `internal/app/processor.go`'s `recalculateAnalytics`, drop
      `v.AccountID` from the `ReconcileWindow` call, and stop setting
      `AccountID:` on the `analytics.ChargeGap{...}` literals built in the
      `flagged` loop.
- [x] 5.2 `go build ./internal/app/...` — expect this to still fail until T6
      lands (the package's test file needs the same fixup); expected at this
      point, not a regression.

## T6 — Cross-module test fixup

Depends on: T3 (same signature both T5 and T6 depend on; T5 and T6 touch
different files and may be done by two agents in parallel).

- [x] 6.1 In `internal/app/processor_test.go`, update `fakeGapWriter`'s
      `ReconcileWindow` method to the new signature (drop the `_ uuid.UUID`
      parameter for `accountID`). No `ChargeGap` literal in this file needs a
      change — it only implements the interface, it does not construct the
      type.
- [x] 6.2 `go build ./internal/app/...` and `go vet ./internal/app/...` —
      expect clean now.

## T7 — Verify `internal/charging/db_session_integration_test.go` needs no change

Depends on: T3 (so there is a final signature to check against).

- [x] 7.1 Confirm this file does not import `internal/analytics`, does not
      call `ReconcileWindow`, and does not construct an `analytics.ChargeGap`
      — design.md's D-TESTFIX section already found this to be the case (the
      file's only charge-gap-related content is one comment naming a
      different file's helper-naming precedent). `go vet
      ./internal/charging/...` passing with no edit to this file confirms it.
      If this check instead finds a real dependency this design missed, stop
      and treat it as a design gap to escalate, not a task to silently
      expand.

## T8 — Docs sweep

Depends on: T1–T4 (needs the final schema/signature to describe accurately).
Independent of T5–T7; may run in parallel with them.

- [x] 8.1 `internal/analytics/AGENTS.md`: grep the file for `account_id` in
      the context of `charge_gaps` and fix any stale mention found. (None is
      currently expected — the module's own file defers column detail to the
      KB guide — but verify rather than skip.)
- [x] 8.2 `internal/analytics/README.md`: does not exist in this repo today —
      no-op. (The ticket named it; this repo's analytics module has no
      README.md, only `AGENTS.md`. Confirmed by directory listing.)
- [ ] 8.3 This change's `specs/analytics/spec.md` delta (already written as
      part of this change's artifacts) is synced into
      `openspec/specs/analytics/spec.md` at archive time via the normal
      OpenSpec sync step — no separate task here, but confirm at archive
      time that the sync picked up the "Charge Gap Ledger" rewrite and did
      not also touch the unrelated "Module-Scoped Database Schema"
      requirement's historical scenario (design.md explains why that one
      stays as written).
- [x] 8.4 `kkpa/context/entities/vehicle-metrics/guide.md`: update the
      `charge_gaps` column list — remove `account_id UUID NOT NULL`, change
      `UNIQUE (account_id, tesla_id, gap_date)` /
      `charge_gaps_account_tesla_date_unique` to
      `UNIQUE (tesla_id, gap_date)` / `charge_gaps_tesla_date_unique`, and
      remove the `idx_charge_gaps_account` sentence — replace it with a
      one-line note that no second index exists because the UNIQUE index
      alone serves every query (point to design.md's D-INDEX table shape,
      but do not quote design.md verbatim — the guide is a map, written in
      its own words, not a copy).

- [x] 8.5 `docs/battery-consumed-graph.md`: found by the 9.5 grep, not named in
      the original task list. Its "Table shape" section asserted
      `UNIQUE (account_id, tesla_id, gap_date)` and
      `Index idx_charge_gaps_account (account_id, gap_date DESC)`, and its
      "Nothing consumes charge_gaps yet" section told a future reader that
      index "already exists for the account-wide read". All three are false
      after this change. Updated both sections: the new key, no `account_id`
      column, one index only, and an account-wide read now has to resolve the
      account's vehicles through `internal/account` first. Added by the leader
      during T9; the task list is append-only, so it is recorded here rather
      than folded into 8.1-8.4.

## T9 — Final verification

Depends on: T1–T8.

- [x] 9.1 `go build ./...`
- [x] 9.2 `go vet ./...`
- [x] 9.3 `make migration-guard`
- [x] 9.4 `make boundary-guard`
- [x] 9.5 Grep the whole repo for `account_id` scoped to `charge_gaps` /
      `ChargeGap` / `ReconcileWindow` / `GapWriter` and confirm zero
      remaining hits outside the immutable
      `openspec/changes/archive/` tree and the untouched original migration
      file (`20260815000002_add_charge_gaps.sql`).
- [x] 9.6 Suite commands for the owner to run (Claude does not run these):
      `go test ./internal/analytics/... ./internal/app/... ./internal/charging/...`
      (or `make test` / `make test-with-db` for the full suite).
