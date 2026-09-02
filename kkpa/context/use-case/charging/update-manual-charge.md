# Update a manual charge record — PUT /ui/charges/row/:id

> One external entry point, one output. **Backend only** — the adapter side lives in the
> input-port file that links here. Paths + symbols only; ask CodeGraph for signatures, never
> record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `Handler.ChargeRowUpdate` — `internal/gateway/handlers/charges.go`
- **Trigger:** `PUT /ui/charges/row/:id`
- **Module:** `charging` (the domain this serves; the handler itself lives in `internal/gateway`)

## Triggered by

- `input-port/charging/charges.md` — the Manual Records page (`/charges`), inline row edit form

## Input / output

- **Input:** form body from `fragments.ChargeRowEdit` — `csrf_token`, `charged_on`, `status`,
  `start_battery_pct`, `end_battery_pct`, `started_at`, `ended_at`, `energy_added_kwh`, `price`,
  `location_kind`, `location_label`, `charging_type`, `notes`, `odometer_km`, plus hidden
  `start` / `end` carrying the active filter window. The vehicle is **not** a form field — it
  comes from the session-selected vehicle. Currency is hardcoded `COP`.
- **Output:** `200` with `fragments.ChargeRowUpdateSuccessOOB` — the static row swap plus an
  out-of-band `#charges-list` refresh. `422` re-renders the edit row with per-field errors,
  `500` with a top-of-form error, `403` on CSRF failure, `400` on an unparseable id.

## Flow

1. `Handler.ChargeRowUpdate` — `internal/gateway/handlers/charges.go` — auth guard
   (`currentUID`), `checkCSRF` against `csrf_manualcharge`, parse the id.
2. `account.Service.RegisteredVehicles` — tenant vehicle list for the ownership check.
3. `windowFromForm` → `bestEffortWindow` — resolves the filter window to echo back. **Never a
   validation gate** in either direction.
4. `Handler.parseChargeForm` — validates; delegates the required-field rule to
   `charging.RequiredFieldsFor(status)` rather than hardcoding it; runs `vehicleOwned`.
5. `Handler.fetchEntryTeslaIDAndChargedOn` → `charging.Reader.ListEntriesByAccount` — reads the
   **pre-update** `charged_on`. Once the UPDATE commits the old date is unrecoverable.
   ⚠ capped at 100 rows — see `architecture/charge-record-mutation.md`.
6. `charging.Writer.Update` — `internal/charging/service.go` — `normalizeStatus` →
   `missingFields` → `resolveEnergy` → `UpdateEntry`. A rejected update writes nothing.
7. `Handler.recalculateAfterChargeWrite` → `analytics.Recalculator.Recalculate(uid, teslaID, D, D)`
   for the new date; called a **second time** for the old date when the edit moved it. Errors
   logged and swallowed.
8. `Handler.buildChargesPage` — re-reads for the OOB list refresh and the aggregation tiles.

## Database

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | `vehicles` | `account.Service.RegisteredVehicles` |
| 2 | READ | `manual_charge_entries` | `charging.Reader.ListEntriesByAccount` (pre-update date) |
| 3 | WRITE | `manual_charge_entries` | `UpdateEntry` — sets `updated_at = now()`; Postgres recomputes `inferred_capacity_kwh_calc` in the same statement |
| 4 | READ | `vehicle_snapshots` | `telemetry.Reader.SnapshotsByVehicleBetween` + `SnapshotPrecedingDay` |
| 5 | READ | `supercharger_sessions` | `charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween` |
| 6 | READ | `manual_charge_entries` | `charging.Reader.ListEntriesByVehicleBetween` |
| 7 | WRITE | `vehicle_metrics` | `UpsertVehicleMetric` × n **+** `DeleteVehicleMetricsInRangeExcept`, one transaction |
| 8 | READ | `manual_charge_entries`, `vehicles` | `buildChargesPage` |

Steps 4–7 repeat when the edit changed the date. **Not touched:** `charge_gaps`,
`vehicle_metric_watermarks`, `supercharger_sessions` (write side).

## Entities involved

- `entities/vehicle-metrics/guide.md`

## Related use cases

- `use-case/charging/delete-manual-charge.md` — same pre-write lookup, same recalc hook
- `use-case/charging/verify-session-battery.md` — the Supercharger counterpart; converges on the
  same `Recalculate` with a different window

## Conventions & gotchas

- **Read the old `charged_on` before calling `Update`.** There is no other way to recover it, and
  without it a date-moving edit leaves the vacated day's metrics wrong.
  _Source: `fetchEntryTeslaIDAndChargedOn` doc comment._
- **The recalculation window is a single day, `[chargedOn, chargedOn]`** — deliberately narrower
  than the Supercharger path's `[D−1, D+1]`, because `charged_on` is a bare date with no zone
  ambiguity. Do not "align" the two without reading
  `architecture/charge-record-mutation.md`.
  _Source: `recalculateAfterChargeWrite`._
- **`energy_source` is recomputed on every write and the caller's value is ignored.** Blanking
  the energy on an edit flips a `USER` row to `ESTIMATED`, and vice versa. This is correct and
  centralized in `resolveEnergy`.
  _Source: `charging/service.go` `resolveEnergy`._
- **Error branches re-render from the raw POST values, not from the parsed entry** — a value that
  failed validation has no representation in `charging.Entry`'s typed fields, so only the raw
  string survives to be echoed back.
  _Source: `chargeEntryVMFromRawValues`._
- **The filter window is cosmetic on this route.** A malformed `start`/`end` must never turn a
  successful write into an error — that is why this uses `bestEffortWindow`, not
  `parseChargesRange`.
  _Source: `bestEffortWindow` doc comment._
