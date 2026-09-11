# analytics-rekey-charge-gaps-on-tesla-id

> Source: MAG-64 — https://linear.app/magus-monitor/issue/MAG-64/1-pilot-re-key-analyticscharge-gaps-on-tesla-id
> Step 1 of the MAG-63 re-key decision record. This is the **pilot**: six later
> tickets copy the same pattern on other tables. Chosen first because it is the
> smallest safe case — `tesla_id` is `NOT NULL` already, no gateway code reads
> this table, and the nightly job deletes and rebuilds it, so a mistake here
> cannot lose data that cannot be recomputed.

## Why

`analytics.charge_gaps` is keyed on `(account_id, tesla_id, gap_date)`. But a vehicle
(`tesla_id`) already belongs to exactly one account at a time, and `GapWriter.ReconcileWindow`
is always called for one vehicle. Carrying `account_id` in the key and in every
query buys no real isolation — it just makes the code carry an extra parameter
that a single-tenant-per-vehicle model does not need. `tesla_id` alone is the
correct identity key, matching the parent decision record's re-key plan for
every table in the roadmap.

## What changes

- **Migration** (`internal/analytics/db/migrations/`): collapse any duplicate
  `(tesla_id, gap_date)` rows (keep the earliest `created_at`), drop the
  `(account_id, tesla_id, gap_date)` unique constraint and the `account_id`-leading
  index, add a new `UNIQUE (tesla_id, gap_date)` constraint, then `DROP COLUMN
  account_id`.
- **Queries** (`internal/analytics/db/query.sql`): `UpsertChargeGap`,
  `DeleteChargeGap`, `ChargeGapDatesByVehicleBetween` drop the `account_id`
  parameter and predicate; `UpsertChargeGap`'s conflict target becomes
  `(tesla_id, gap_date)`.
- **Port** (`internal/analytics/analytics.go`, `gap_writer.go`):
  `GapWriter.ReconcileWindow` drops its `accountID uuid.UUID` parameter;
  `ChargeGap` drops its `AccountID` field (see `design.md` D-CHARGEGAP-FIELD).
- **Caller** (`internal/app/processor.go`): drops the account argument at the
  `ReconcileWindow` call site and stops setting `AccountID` on the `ChargeGap`
  values it builds.
- **Test fixups** (not new tests — keeping the build/vet green after the
  signature change): `internal/app/processor_test.go`'s `fakeGapWriter`, and
  `internal/analytics/db_gap_writer_integration_test.go`'s own account-scoped
  helpers and scenarios.
- **Docs**: `internal/analytics/AGENTS.md`, the delta spec in this change's
  `specs/analytics/spec.md` (the "Charge Gap Ledger" requirement), and the
  `kkpa/context/entities/vehicle-metrics/guide.md` KB guide's `charge_gaps`
  column list.

## Breaking?

Internal only — no external API. `GapWriter.ReconcileWindow`'s signature
changes (drops one parameter) and `ChargeGap` loses a field, so every Go
caller and every test building a `ChargeGap` value must update. There is
exactly one production caller (`internal/app/processor.go`) and two test
files that build `ChargeGap` values or implement `GapWriter`
(`internal/app/processor_test.go`, `internal/analytics/db_gap_writer_integration_test.go`).
No HTTP surface, no gateway code, and no other module touches `charge_gaps`
or the `GapWriter` port.

## Modules affected

- `internal/analytics` — owns the table, the port, and the implementation.
- `internal/app` — the one caller of `ReconcileWindow`; one call-site line and
  one test fixture change (mechanical consequence of the port signature
  change, not a design decision of its own — see `design.md` D-SCOPE).

## Read paths affected

None. `charge_gaps` has no read port today (`internal/analytics/AGENTS.md`
confirms: "Nothing currently reads `charge_gaps` back"). The only queries
this change touches are the nightly write path's own upsert, delete, and
diff-read (`ChargeGapDatesByVehicleBetween`), all inside
`GapWriter.ReconcileWindow`. See `design.md`'s Index Plan for why the new
`UNIQUE (tesla_id, gap_date)` index alone serves all three without adding a
second index.

## Non-goals

- No change to `vehicle_metrics` or `vehicle_metric_watermarks` — those keep
  `account_id` for now; they are separate tickets in the MAG-63 roadmap.
- No new read port on `charge_gaps` — still write-only, as today.
- No fix to the `charge_gaps` table `COMMENT`, which still names
  `internal/battery` and `telemetry` as this table's owner. That drift
  predates this change and is not a finding this change corrects (project
  rule: a stale table comment is never a finding). The comment lives in the
  original migration file, which this change does not edit — see
  `ai/go-conventions.md`'s "historic migrations are never edited" precedent.
