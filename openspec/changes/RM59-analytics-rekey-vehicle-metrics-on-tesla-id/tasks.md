# Tasks — RM59-analytics-rekey-vehicle-metrics-on-tesla-id

All work is inside `internal/analytics`, except **T8**, which is leader-owned and
touches `internal/app` and `internal/gateway`.

## Dependency graph

```
T1 (migration) ──► T2 (queries + sqlc) ──► T3 (ports) ──► T4 (implementation)
                                            │                  │
                                            │                  ├──► T5 (DB integration test fixups)
                                            │                  ├──► T6 (offline test fixups)
                                            │                  └──► T8 (leader-owned: app + gateway)
                                            └──► T7 (docs) — parallel with T4–T8
                                                         T9 (final verification) — depends on all
```

- **T1 → T2 → T3 → T4 is a hard chain.** Go cannot compile against sqlc-generated
  types until they are regenerated, and the ports cannot be re-implemented until those
  types exist.
- **T5 and T6 may run in parallel** — disjoint files (`db_integration_test.go` vs
  `reader_test.go`).
- **T7 (docs) touches no Go file** and may run in parallel with T4–T8.
- **T8 must land in the same wave as T4**, or `go build ./...` stays red outside
  `internal/analytics` (design.md D12).

## T1 — Migration

Depends on: nothing.

- [x] 1.1 Write
      `internal/analytics/db/migrations/20260914000001_rekey_vehicle_metrics_on_tesla_id.sql`
      exactly as specified in `design.md`'s "Migration SQL": the two duplicate-collapse
      `DELETE`s (keep latest `updated_at`, tie broken by `id`), `DROP CONSTRAINT
      vehicle_metrics_account_tesla_date_unique`, `DROP INDEX
      analytics.idx_vehicle_metrics_latest`, `DROP COLUMN account_id` on
      `vehicle_metrics`, `ADD CONSTRAINT vehicle_metrics_tesla_date_unique UNIQUE
      (tesla_id, metric_date)`, `CREATE INDEX idx_vehicle_metrics_latest ON
      analytics.vehicle_metrics (tesla_id, metric_date DESC)`, then the mirror
      sequence for `vehicle_metric_watermarks` (`DROP CONSTRAINT
      vehicle_metric_watermarks_account_tesla_source_unique`, `DROP COLUMN
      account_id`, `ADD CONSTRAINT vehicle_metric_watermarks_tesla_source_unique
      UNIQUE (tesla_id, source)` — no index recreation needed there, design.md D7),
      plus the `-- +goose Down` half.
- [x] 1.2 Do NOT write a `SET NOT NULL` for `tesla_id` on either table (already
      `NOT NULL`, verified) and do NOT write `DROP INDEX
      idx_vehicle_metrics_vehicle_date` (does not exist) — design.md D4.
- [x] 1.3 Do not touch `20260821000001_add_vehicle_metrics.sql` or
      `20260821000002_add_vehicle_metric_watermarks.sql` — historic migrations are
      never edited.
- [x] 1.4 Run `make migration-guard`. Expect clean (no cross-module schema reference
      introduced).

## T2 — Queries + sqlc

Depends on: T1 (schema must exist before `sqlc generate` can validate the queries
against it).

- [x] 2.1 In `internal/analytics/db/query.sql`, remove `account_id` from
      `UpsertVehicleMetric`'s column list, `VALUES` list, and `ON CONFLICT` target
      (becomes `(tesla_id, metric_date)`).
- [x] 2.2 Remove `account_id` from `DeleteVehicleMetricsInRangeExcept`'s `WHERE`
      clause.
- [x] 2.3 Remove `account_id` from `VehicleMetricsConsumedByVehicleBetween`'s,
      `VehicleMetricsOdometerByVehicleBetween`'s, and
      `VehicleMetricsBatteryByVehicleBetween`'s `WHERE` clauses (the three MAG-71
      queries, folded in per roadmap RD2).
- [x] 2.4 Remove `account_id` from `GetVehicleMetricWatermark`'s `WHERE` clause and
      `UpsertVehicleMetricWatermark`'s column list / `VALUES` list / `ON CONFLICT`
      target.
- [x] 2.5 Rename `LatestVehicleMetricsByAccount` to `LatestVehicleMetricsByVehicles`:
      replace `WHERE account_id = @account_id` with
      `WHERE tesla_id = ANY(@tesla_ids::bigint[])`. Keep the `SELECT` column list,
      `DISTINCT ON (tesla_id)`, and `ORDER BY tesla_id, metric_date DESC` unchanged.
- [x] 2.6 Rewrite the doc comments on every query touched above so none cites a
      constraint name being replaced (`vehicle_metrics_account_tesla_date_unique`,
      `vehicle_metric_watermarks_account_tesla_source_unique`) or an archived design
      doc's "Index Plan" by name — replace each citation with the reason itself
      (design.md D5/D6/D7 give the substance). Do not cite this change's own name or
      any decision ID in the new comment text.
- [x] 2.7 Run `make sqlc` (or `sqlc generate`). Confirm the regenerated
      `internal/analytics/db/query.sql.go` has no `AccountID` field on
      `UpsertVehicleMetricParams`, `DeleteVehicleMetricsInRangeExceptParams`,
      `VehicleMetricsConsumedByVehicleBetweenParams`,
      `VehicleMetricsOdometerByVehicleBetweenParams`,
      `VehicleMetricsBatteryByVehicleBetweenParams`,
      `GetVehicleMetricWatermarkParams`, or `UpsertVehicleMetricWatermarkParams`, and
      that `LatestVehicleMetricsByVehiclesParams`/`Row` exist in place of the
      `...ByAccount` names.

## T3 — Ports

Depends on: T2 (needs the regenerated `analyticsdb` types).

- [x] 3.1 In `internal/analytics/analytics.go`: change `Reader.ConsumedByDay`,
      `OdometerDeltaByDay`, `BatteryLevelByDay` to drop `accountID uuid.UUID` (keep
      `teslaID int64`, design.md D10). Update each method's doc comment: drop every
      sentence describing account-scoping as the security boundary, replace with a
      one-line note that scoping is by vehicle identity (`tesla_id`) alone, and that a
      caller must already have proven the requesting account owns this vehicle before
      calling.
- [x] 3.2 In the same file, change `LatestMetricsByAccount(ctx, accountID
      uuid.UUID)` to `LatestMetricsForVehicles(ctx context.Context, refs
      []vehicleref.Ref) ([]VehicleStatus, error)` (design.md D9). Add the
      `internal/vehicleref` import. Rewrite the method's doc comment: replace every
      "for the given account"/"a different account's vehicle" phrase with "for each
      vehicle in the given set"/"a vehicle outside the given set"; keep the existing
      "latest = greatest metric_date" and "empty, non-nil slice" contract sentences,
      reworded for a vehicle set instead of an account.
- [x] 3.3 In the same file, change `Recalculator.Recalculate` and `Reconcile` to drop
      `accountID uuid.UUID` (keep `teslaID int64`, design.md D10). Update both doc
      comments the same way as 3.1.
- [x] 3.4 `go build ./internal/analytics/...` — expect this to fail until T4 also
      lands (the implementation files still reference the old signatures); expected
      at this point in the sequence, not a regression.

## T4 — Implementation

Depends on: T3.

- [x] 4.1 `internal/analytics/consumed.go`: delete `vehicleMetricRow.AccountID` (no
      caller sets it once Recalculate has no `accountID` to assign from — design.md
      D10).
- [x] 4.2 `internal/analytics/recalculate.go`: `Recalculate` drops the `accountID`
      parameter and the `for i := range rows { rows[i].AccountID = accountID }` loop.
      `upsertVehicleMetricParamsFrom` drops `AccountID: row.AccountID` from the
      `analyticsdb.UpsertVehicleMetricParams` literal. `Reconcile` drops the
      `accountID` parameter; every `r.watermark(ctx, accountID, teslaID, source)` /
      `r.advanceWatermark(ctx, accountID, teslaID, source, ...)` call, and the
      `watermark`/`advanceWatermark` helper signatures themselves, drop `accountID`.
      Every `analyticsdb.GetVehicleMetricWatermarkParams` /
      `UpsertVehicleMetricWatermarkParams` / `DeleteVehicleMetricsInRangeExceptParams`
      literal drops `AccountID:`.
- [x] 4.3 `internal/analytics/reader.go`: `ConsumedByDay`, `OdometerDeltaByDay`,
      `BatteryLevelByDay` drop the `accountID` parameter and the `AccountID:
      accountID` field from their respective `analyticsdb.*BetweenParams` literals.
      Rename the `vehicleMetricsStore` interface's `LatestVehicleMetricsByAccount`
      method to `LatestVehicleMetricsByVehicles(ctx context.Context, teslaIDs
      []int64) ([]analyticsdb.LatestVehicleMetricsByVehiclesRow, error)`. Implement
      `LatestMetricsForVehicles`: call `vehicleref.TeslaIDs(refs)` to unwrap the given
      refs, pass the result to `r.metrics.LatestVehicleMetricsByVehicles`, map rows to
      `[]VehicleStatus` exactly as `LatestMetricsByAccount` did (no change to the
      mapping body itself, only its inputs).
- [x] 4.4 `go build ./internal/analytics/...` and `go vet ./internal/analytics/...`
      — expect `go vet` to fail until T5/T6 also land (this package's own test files
      still reference the old shapes); expected at this point, not a regression.

## T5 — Database-backed integration test fixups (`db_integration_test.go`)

Depends on: T4. May run in parallel with T6 (disjoint file).

- [x] 5.1 Update every helper that seeds or reads `vehicle_metrics` /
      `vehicle_metric_watermarks` (`cleanupVehicleMetrics`, `fetchVehicleMetric`,
      `seedPreMigrationVehicleMetric`, `fetchWatermark` and its siblings, and any
      other helper design.md's test-fixup section names) to drop its `accountID
      uuid.UUID` parameter and the `account_id = $N` predicate from its SQL, keyed on
      `tesla_id` (+ `metric_date` / `+ source` where the original already filtered on
      it) alone. `fetchVehicleMetric`'s `SELECT`/`Scan` list drops `account_id` /
      `&m.AccountID`.
- [x] 5.2 Update every `Recalculate(ctx, accountID, teslaID, ...)` /
      `Reconcile(ctx, accountID, teslaID)` call in this file to drop the `accountID`
      argument.
- [x] 5.3 Rename `TestRecalculate_AccountIDScoping_PassedToEveryPort` to
      `TestRecalculate_TeslaIDScoping_PassedToEveryPort`; drop its `accountID`
      variable and argument; rewrite its doc comment's closing sentence, which
      currently claims "the account still scopes the rows Recalculate writes" — that
      is no longer true (design.md's test-fixup section).
- [x] 5.4 Rename the five `TestReader_LatestMetricsByAccount_*` tests to
      `TestReader_LatestMetricsForVehicles_*` (design.md's test-fixup section gives
      the exact list). Replace each `r.LatestMetricsByAccount(ctx, accountID)` call
      with `r.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{...}))` — a test
      file is exempt from `make vehicleref-guard`, so a direct `vehicleref.All(...)`
      call here is correct. `TestReader_LatestMetricsByAccount_EmptyAccountReturnsEmptyNonNilSlice`
      becomes `TestReader_LatestMetricsForVehicles_EmptyVehicleSetReturnsEmptyNonNilSlice`,
      asserted against an empty/nil ref slice rather than an account with no vehicles.
- [x] 5.5 Grep this file for `AccountID` and `account_id` once done. Any fixture
      builder that still carries `accountID` because it also seeds a
      `telemetry.Snapshot`/`charging.Session` row (untouched by this change) is
      correct to keep it there — only drop it where it exists solely to reach a
      `vehicle_metrics`/`vehicle_metric_watermarks` call.
- [x] 5.6 `go build ./internal/analytics/...` and `go vet ./internal/analytics/...`
      restricted to this file's package — expect clean once T6 also lands (both
      files are in the same package).

## T6 — Offline test fixups (`reader_test.go`)

Depends on: T4. May run in parallel with T5 (disjoint file).

- [x] 6.1 `TestRecentEfficiency_AccountIDScoping_PassedToEveryPort` — confirm
      unchanged (`RecentEfficiency` keeps `accountID`, design.md D10). No edit
      expected; verify rather than skip.
- [x] 6.2 Drop the `accountID` argument from every `r.ConsumedByDay(...)` /
      `r.OdometerDeltaByDay(...)` call in `TestReader_ConsumedByDay_ReadsPrecomputedRows`,
      `TestReader_OdometerDeltaByDay_ClampsOnRead`,
      `TestReader_ConsumedByDay_ExcludesPredecessorlessRow`,
      `TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow`,
      `TestReader_VehicleMetricsStoreError_Propagates`.
- [x] 6.3 Update the fake implementing `vehicleMetricsStore` in this file: drop
      `AccountID:` from its `*BetweenParams` literals and any `gotConsumedParams.AccountID`
      assertion; rename its `LatestVehicleMetricsByAccount` method to
      `LatestVehicleMetricsByVehicles(ctx context.Context, teslaIDs []int64)`, and
      rename its recorded `gotLatestAccountID uuid.UUID` field to `gotLatestTeslaIDs
      []int64`.
- [x] 6.4 `go build ./internal/analytics/...` and `go vet ./internal/analytics/...`
      — expect clean once T5 also lands.

## T7 — Docs

Depends on: T1–T4 (needs the final schema/signature shape to describe accurately).
Independent of T5/T6/T8; may run in parallel with them.

- [x] 7.1 `internal/analytics/AGENTS.md`: rename the "Public interface" table's
      `LatestMetricsByAccount` row to `LatestMetricsForVehicles` with an updated
      "Returns" description; add `internal/vehicleref` to "May import" with a
      one-line reason (design.md D9). Grep the file for any other `account_id` /
      `LatestMetricsByAccount` mention on these two tables and fix what is found
      (none is currently expected beyond the two spots above — verify, don't
      assume).
- [x] 7.2 `kkpa/context/entities/vehicle-metrics/guide.md`: update the
      `vehicle_metrics` and `vehicle_metric_watermarks` column lists — remove
      `account_id UUID NOT NULL` from both, rename
      `vehicle_metrics_account_tesla_date_unique` /
      `vehicle_metric_watermarks_account_tesla_source_unique` to
      `vehicle_metrics_tesla_date_unique` / `vehicle_metric_watermarks_tesla_source_unique`
      with their new column sets, and update `idx_vehicle_metrics_latest`'s
      documented columns to `(tesla_id, metric_date DESC)`.
- [x] 7.3 This change's `specs/analytics/spec.md` delta (already written as part of
      this change's artifacts) is synced into `openspec/specs/analytics/spec.md` at
      archive time via the normal OpenSpec sync step. Confirm at archive time that
      the sync picked up the three `MODIFIED Requirements` below and did NOT also
      touch "Module-Scoped Database Schema" — design.md explains why that one stays
      as written.
- [x] 7.4 Grep `kkpa/context/` for any other guide naming `LatestMetricsByAccount` or
      this table's old constraint names outside the one guide named in 7.2 (the base
      `CLAUDE.md` docs-track-change rule). None is currently expected.

## T8 — Leader-owned integration (`internal/app`, `internal/gateway`)

Depends on: T3 (needs the final port signatures). **Not this module's sandbox —
`internal/analytics` workers must not edit these files.** Must land in the same wave
as T4, or `go build ./...` stays red (design.md D12).

- [x] 8.1 `internal/app/processor.go`'s `recalculateAnalytics`: build the distinct set
      of `tesla_id`s from `p.acct.AllRegisteredVehicles(ctx)`'s result before the
      loop, mirroring `processChargingData`'s own existing dedupe idiom in the same
      file. Loop over that distinct set. `p.recalculator.Reconcile(ctx, teslaID)` and
      `p.analyticsReader.ConsumedByDay(ctx, teslaID, start, end)` drop `v.AccountID`
      (design.md D11).
- [x] 8.2 Same file: the `flagged` loop's `analytics.ChargeGap{...}` literals are
      unaffected (that type carries no `AccountID` field already); `VIN` still comes
      from one representative vehicle per `tesla_id`.
- [x] 8.3 Same file: rewrite `recalculateAnalytics`'s doc comment where it describes
      the loop as running once per `(account, vehicle)` pair — it now runs once per
      distinct `tesla_id`, mirroring `processChargingData`'s own "why per vehicle"
      paragraph.
- [x] 8.4 `internal/app/processor_test.go`: update the `analytics.Recalculator` /
      `analytics.Reader` fakes' method signatures to match (drop `accountID`), and
      re-shape the dedupe assertion if one already exists for the mirror step's own
      identical pattern (`processChargingData`'s test likely already asserts
      "registered twice → mirrored once"; mirror that shape for `Reconcile`/
      `ConsumedByDay` if `recalculateAnalytics` has an equivalent test).
- [x] 8.5 `internal/gateway/handlers/history.go`'s `buildHistoryView`: drop `uid` from
      the three `h.analyticsReader.BatteryLevelByDay(...)` /
      `OdometerDeltaByDay(...)` / `ConsumedByDay(...)` calls.
- [x] 8.6 `internal/gateway/handlers/handlers.go`: at `vehiclesFor`, `dashboardFor`,
      `navHeaderFor`, build `refs := vehicleref.All(...)` inline from the already-
      resolved `registered []account.Vehicle` slice's `TeslaID`s, and replace
      `h.analyticsReader.LatestMetricsByAccount(ctx, uid)` with
      `h.analyticsReader.LatestMetricsForVehicles(ctx, refs)` (design.md D12 — this is
      guard-compliant because the whole file is exempt from `make vehicleref-guard`).
- [x] 8.7 `internal/gateway/handlers/external_charges.go`'s `buildExternalChargesPage`:
      replace `h.analyticsReader.LatestMetricsByAccount(ctx, uid)` with a call using
      `h.ownedVehicles(ctx, uid)`'s returned `refs` — this file is NOT exempt from
      `make vehicleref-guard`, so do not call `vehicleref.All`/`vehicleref.Authorize`
      directly here (design.md D12). Keep the existing "loop returned statuses, match
      `s.TeslaID == teslaIDFilter`" filtering unchanged.
- [x] 8.8 `go build ./internal/app/... ./internal/gateway/... ./cmd/...` — expect
      clean. `go vet` on the same packages is **expected to still fail** for
      `internal/gateway` (its own test files are tier 2's job, design.md D12) — this
      is not a regression to chase down here; `go vet ./internal/app/...` IS expected
      clean once 8.4 lands.

## T9 — Final verification

Depends on: T1–T8.

- [x] 9.1 `go build ./...` — expect clean.
- [x] 9.2 `go vet ./...` — expect clean EXCEPT `internal/gateway` (design.md D12,
      tier 2's job). Record the exact failing package/test names for the tier 2
      dispatch rather than leaving them implicit.
- [x] 9.3 `gofmt -l` over every file this change touched — expect no output.
- [x] 9.4 `make migration-guard`
- [x] 9.5 `make boundary-guard`
- [x] 9.6 `make vehicleref-guard`
- [ ] 9.7 Verify the two index claims (design.md D5/D6) with
      `SET enable_seqscan = off;` before `EXPLAIN` on `LatestVehicleMetricsByVehicles`
      and on one of the three chart queries — confirms the index CAN serve the query
      on this small table, which a bare `EXPLAIN` cannot prove (design.md D8). This
      is a read-only session setting plus a read-only `EXPLAIN` — no DDL.
- [x] 9.8 Grep the whole repo for `account_id` scoped to `vehicle_metrics` /
      `vehicle_metric_watermarks` / `VehicleMetricRow` / `LatestMetricsByAccount` and
      confirm zero remaining hits outside the immutable `openspec/changes/archive/`
      tree and the two untouched original migration files
      (`20260821000001_add_vehicle_metrics.sql`,
      `20260821000002_add_vehicle_metric_watermarks.sql`).
- [ ] 9.9 Suite commands for the owner to run (this worker does not run them):
      `go test ./internal/analytics/... ./internal/app/...` (skip
      `./internal/gateway/...` until tier 2 lands), or `make test` /
      `make test-with-db` for the full suite once both tiers are done.
