# Charge record mutation — the shared contract behind every charge write

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `edit charging records`, `charge record lifecycle`, `charge recalculation`,
  `what happens when I edit a charge`, `charge write side effects`
- **Internal name:** the contract shared by `charging.Writer` (manual) and
  `charging.SessionVerifier` (Supercharger), followed by
  `analytics.Recalculator.Recalculate` — tables `manual_charge_entries`, `supercharger_sessions`,
  `vehicle_metrics`, `charge_gaps`, `vehicle_metric_watermarks`

This topic exists because **two sources are supposed to behave identically once their source row
is written**, and the KB needs one place that states what "identically" means and where the
implementations actually diverge. The per-endpoint detail lives in the three use cases; this file
is the contract they share.

## The intended model (five steps)

Every charge mutation, whichever source it came from, is meant to run this sequence:

1. **Update or delete the source record** through the owning module's port.
2. **Determine the affected period** — which metric day(s) this record contributes to.
3. **Recalculate the derived fields** for that period through centralized logic.
4. **Persist** the recalculated values.
5. **Recalculate/remove dependent records** — charge gaps and anything derived from them.

## Where the implementation actually lands

| # | Step | Manual | Supercharger | State |
|---|---|---|---|---|
| 1 | Update / delete source row | `charging.Writer.Update` / `.Delete` | `charging.SessionVerifier.VerifySession` (2 fields only; no create/delete route exists) | Implemented, both |
| 2 | Determine affected period | `recalculateAfterExternalChargeWrite` — window `[chargedOn, chargedOn]` | `recalculateAfterSessionVerify` — window `[day−1, day+1]` around the **UTC** day of `charge_stop_date_time` | **Two separate gateway functions, two policies** |
| 3 | Recalculate | `analytics.Recalculator.Recalculate` → `deriveVehicleMetrics` | *same call, same derivation* | Implemented and genuinely centralized |
| 4 | Persist | one tx: `UpsertVehicleMetric` × n + `DeleteVehicleMetricsInRangeExcept` | *same* | Implemented and centralized |
| 5 | Dependent records (`charge_gaps`) | not touched | not touched | **Missing on both** — nightly job only |

**Read this table as: the arithmetic is centralized; the trigger is not.** Steps 3 and 4 are one
shared implementation with zero duplication. Steps 2 and 5 were left in the gateway, and every
known divergence lives there.

## Component map

### Gateway — the trigger side (where the divergence is)

| File | Role |
|---|---|
| `internal/gateway/handlers/external_charges.go` | `ExternalChargeRowUpdate`, `ExternalChargeRowDelete`, and the recalc hook `recalculateAfterExternalChargeWrite`. Also `fetchEntryTeslaIDAndChargedOn` — the pre-write lookup that resolves the affected day. |
| `internal/gateway/handlers/supercharger.go` | `SuperchargerRowUpdate` and its **separate** recalc hook `recalculateAfterSessionVerify`. |
| `internal/gateway/handlers/handlers.go` | `Handler` deps: `chargingWriter`, `chargingReader`, `superchargerVerifier`, `analyticsRecalculator`. **No `GapWriter`** — which is why step 5 cannot happen here. |
| `internal/gateway/gateway.go` | Route registration for both pages. |

### charging — the source rows

| File | Role |
|---|---|
| `internal/charging/service.go` | `Writer.Update` / `.Delete`; `resolveEnergy` (derives `energy_added_kwh` + `energy_source`); `defaultLimit = 100`. |
| `internal/charging/session_verifier.go` | `VerifySession`; computes `battery_pct_source`; the only writer of the verified percentages. |
| `internal/charging/capacity.go` | `packCapacityKWh(ctx, lookup, teslaID)` — reads the measured capacity from `charging.monthly_effective_capacity`, falls back to `62.0` only while unmeasured (RM52 tier 1, MAG-32). Also `derivedEnergyKWh`. |
| `internal/charging/db/query.sql` | `UpdateEntry`, `DeleteEntry`, `VerifySuperchargerSession`, `MirrorSuperchargerSession`. |
| `internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql` | The `inferred_capacity_kwh_calc` generated column, on **both** tables. |

### analytics — the derived model

| File | Role |
|---|---|
| `internal/analytics/recalculate.go` | `Recalculate` (the shared step 3+4) and `Reconcile` (the nightly watermark scan). |
| `internal/analytics/consumed.go` | `deriveVehicleMetrics` — the single derivation both sources reach. |
| `internal/analytics/consumption.go` | `deriveConsumption` — the five `_calc` figures. |
| `internal/analytics/gap_writer.go` | `GapWriter.ReconcileWindow` — the only writer of `charge_gaps`. |
| `internal/analytics/capacity.go` | A **second** `packCapacityKWh`, a car-type map. |

### app — the nightly backstop

| File | Role |
|---|---|
| `internal/app/processor.go` | `recalculateAnalytics`: step 1 `Reconcile`, step 2 gap reconciliation over `analytics.GapReconciliationWindow` (30 days). The **only** caller of `ReconcileWindow` in the repo. |

## Calculated fields — who owns what

| Field | Formula / rule | Computed where | Persisted in |
|---|---|---|---|
| `inferred_capacity_kwh_calc` | energy ÷ ((end_pct − start_pct) ÷ 100), 3dp; NULL unless end > start | Postgres `GENERATED ALWAYS … STORED` | `manual_charge_entries`, `supercharger_sessions` |
| `energy_added_kwh`, `energy_source` | derived from capacity × Δpct when the user supplied no energy → `ESTIMATED`, else `USER` | `charging/service.go` `resolveEnergy` | `manual_charge_entries` |
| `battery_pct_source` | `user_verified` when either pct non-nil, else NULL | `charging/session_verifier.go` | `supercharger_sessions` |
| `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`, `estimated_range_km_calc`, `days_spanned_calc` | from the (predecessor, current) snapshot pair | `analytics/consumption.go` `deriveConsumption` | `vehicle_metrics` |
| `consumed_pct` | `battery_used_pct` + Σ Supercharger Δpct + Σ manual Δpct over the span | `analytics/consumed.go` | `vehicle_metrics` |
| `flagged`, `missing_charging_type` | `consumed < 0`, or `consumed == 0` with distance > `minFlagDistanceKm` (10 km) | `analytics/consumed.go` | `vehicle_metrics` |
| `charge_gaps` rows | one row per `flagged` day in a trailing 30-day window | `app/processor.go` → `GapWriter.ReconcileWindow` | `charge_gaps` — **nightly only** |
| `source_updated_at` | max `updated_at` seen per source this run | `analytics/recalculate.go` `Reconcile` | `vehicle_metric_watermarks` — **nightly only** |

Render-time only, never persisted: `Entry.CostPerKWh` / `.BatteryDelta` / `.SessionDuration`
(`internal/charging/charging.go`), the page tiles (`external_charges_tiles.go`,
`buildSuperchargerTiles`), and `analytics.RecentEfficiency`.

## Known divergences

Documented from the code as at 2026-08-29. Each is a real finding, not a design intent.

- **`charge_gaps` is never written by a request** — `ReconcileWindow` has exactly one caller
  (`app/processor.go`) and the gateway holds no `GapWriter`. Correcting the record that caused a
  gap does not clear the gap row; the nightly pass heals it within its 30-day trailing window,
  and never outside it. Nothing currently **reads** `charge_gaps` back (no reader port), which is
  the only reason the blast radius is small today.
  _Source: `analytics/gap_writer.go`, `app/processor.go`, `gateway/handlers/handlers.go`._
- **The manual pre-write lookup is capped at 100 rows** — `fetchEntryTeslaIDAndChargedOn` and
  `fetchEntryVM` call `ListEntriesByAccount(ctx, uid, 0)`, and `limit <= 0` becomes
  `defaultLimit = 100`, ordered `charged_on DESC` across all the account's vehicles. There is no
  `GetEntry` on the `charging.Reader` port. A miss is indistinguishable from "not found" and is
  not logged. On update this skips the old-date recalculation; **on delete it skips recalculation
  entirely**.
  _Source: `gateway/handlers/external_charges.go`, `charging/service.go` `defaultLimit`._
- **A deleted manual entry has no nightly safety net** — `Reconcile` discovers work via
  `ListEntriesByVehicleUpdatedSince` over live rows, so a deleted row is invisible to it forever.
  The post-delete `Recalculate` is the only path, and its error is logged and swallowed.
  _Source: `analytics/recalculate.go` `Reconcile`, `gateway/handlers/external_charges.go`
  `recalculateAfterExternalChargeWrite`._
- **Two recalculation windows, two functions** — `[D, D]` vs `[D−1, D+1]`. The asymmetry itself
  is defensible (`charged_on` is a bare date; `charge_stop_date_time` is a timestamp whose UTC
  day can differ from the bucketed day), but the reasoning is duplicated in two private
  functions in two files. Both windows are also too narrow across a snapshot capture gap, where
  `deriveVehicleMetrics` attributes a charge to a metric row several days later.
  _Source: `gateway/handlers/external_charges.go`, `gateway/handlers/supercharger.go`,
  `analytics/consumed.go` `deriveVehicleMetrics`._
- **Two pack capacities** — `charging/capacity.go` returns the vehicle's **measured** capacity from `charging.monthly_effective_capacity`, and `62.0` only while that vehicle has no measured month (RM52 tier 1, MAG-32);
  `analytics/capacity.go` is a car-type map (`model3:75`, `modely:75`, `models:100`,
  `modelx:100`). For an `ESTIMATED` manual entry, `inferred_capacity_kwh_calc` inverts the same
  formula `resolveEnergy` used, so it returns exactly the capacity that was used — carrying no
  information. That is why the monthly job in `charging/monthly_capacity.go` filters
  `WHERE energy_source = 'USER'`: it must never average a derived value back into itself.
  _Source: `charging/capacity.go`, `analytics/capacity.go`._
- **Different ownership vocabulary** — the manual handlers call `acct.RegisteredVehicles` +
  `vehicleOwned`; `SuperchargerRowUpdate` relies solely on the SQL `AND tesla_id` scope (a
  documented deliberate divergence). Both are secure; the inconsistency is in the vocabulary and
  the extra read.
  _Source: `gateway/handlers/external_charges.go` `vehicleOwned`, `gateway/handlers/supercharger.go` D8._
- **Different post-write refresh** — the manual update returns an OOB `#external-charges-list` refresh so
  tiles follow the edit; the Supercharger update swaps only the row. Harmless today (a battery-%
  correction changes no tile), a defect the moment that page surfaces anything derived from the
  percentages.
  _Source: `fragments.ChargeRowUpdateSuccessOOB` vs `fragments.SuperchargerRow`._
- **`start_battery_pct_est` / `end_battery_pct_est` no longer exist.** They were
  never written (excluded from `VerifySuperchargerSession`, `MirrorSuperchargerSession`,
  and telemetry's own upsert) for their entire lifetime, and were dropped from
  `charging.supercharger_sessions` by `RM41-charging-drop-estimate-columns`
  (2026-09-03); the gateway had already stopped rendering them one tier earlier
  (`RM41-gateway-revise-battery-pct-ui`).
  _Source: `charging/charging.go`, `charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`._
- **`supercharger_sessions.updated_at` is not a "data changed" signal** — `MirrorSuperchargerSession` carries
  no `WHERE` predicate, so every nightly mirror pass bumps it on every row. That is why the
  Supercharger path self-heals nightly and the manual path does not. The robustness is
  incidental: adding the obvious `IS DISTINCT FROM` optimisation would silently remove it.
  _Source: `charging/db/query.sql` `MirrorSuperchargerSession`._

## Conventions & gotchas

- **Never add a fourth write path without a shared trigger.** `external_charges.go`'s own comment names
  the intended composition root — `app.RecalculateVehicleData` — which does not exist yet. Until
  it does, any new charge write must re-derive the window policy by hand.
  _Source: `gateway/handlers/external_charges.go` `recalculateAfterExternalChargeWrite` doc comment._
- **Never compute a derived figure in the gateway.** The gateway calls
  `analytics.Recalculator.Recalculate` and formats results; every formula belongs to the module
  that owns the column. The tiles are the boundary case — they aggregate what is already
  rendered, they do not derive.
  _Source: `ai/architecture.md` boundary rules._
- **A recalculation failure never fails the user's write.** Both hooks log and swallow. This is
  deliberate — the source data *is* saved — but it means "the write succeeded" says nothing about
  whether the derived model is current.
  _Source: `recalculateAfterExternalChargeWrite` / `recalculateAfterSessionVerify` doc comments._
- **`inferred_capacity_kwh_calc` cannot be written by any caller.** It is
  `GENERATED ALWAYS … STORED` on both tables; an attempt to write it fails with SQLSTATE 428C9.
  Do not add it to an INSERT column list or a SET clause.
  _Source: `charging/db/migrations/20260829000001_add_inferred_capacity.sql`._
- **`VerifySuperchargerSession` and `MirrorSuperchargerSession` are deliberate mirror images.** The verifier
  can touch only the human-owned percentages + source + `status` + `updated_at`; the mirror can
  touch everything except those. (`status` joined the verifier's SET clause in RM41 tier 4,
  MAG-45; the mirror still excludes it, so a freshly mirrored session takes `IN_PROGRESS` from
  the column DEFAULT.) Do not "complete the pattern" on either.
  _Source: `charging/db/query.sql`._

- **The gap ledger is keyed on vehicle and day, never on account.** There is at most one row per
  vehicle-day. A day's shortfall is one aggregate observation, so it is never split across two
  rows for the same day. A car can change hands; the flagged day belongs to the car.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **A reconciliation is all-or-nothing.** Every insert, update, and removal it makes either fully
  applies or has no effect. Do not add a write to this path that can land on its own.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **A mis-scoped flagged entry rejects the WHOLE call, writing nothing.** If any supplied day
  does not belong to the call's own vehicle, or falls outside the call's own window, nothing is
  stored — including the entries that were correctly scoped. This is a caller bug, not data to
  accept quietly.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **Removal leaves no trace.** A day that stops flagging is deleted. There is no resolved marker
  and no soft delete: a row is either present (still flagged) or absent. Do not add a
  `resolved_at` column to make history queryable — the ledger is a live worklist by design.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **Days outside the reconciled window are never touched**, whatever their own flagged state.
  This is why a correction older than the nightly window is never healed: the pass simply does
  not look there.
  _Source: spec analytics — Requirement: Charge Gap Ledger._

## Related KB

- Use cases: `use-case/charging/update-manual-charge.md`,
  `use-case/charging/delete-manual-charge.md`,
  `use-case/charging/verify-session-battery.md`
- Input ports: `input-port/charging/external-charges.md`,
  `input-port/charging/supercharger-stats.md`
- Entities: `entities/vehicle-metrics/guide.md`
- Workflows: `workflows/manual-charge-crud.md`, `workflows/supercharger-stats-read.md`
- Architecture: `architecture/nightly-cycle.md`, `architecture/telemetry-ingest-only.md`
