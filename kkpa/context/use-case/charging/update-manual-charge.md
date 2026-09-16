# Update a manual charge record — PUT /ui/external-charges/row/:id

> One external entry point, one output. **Backend only** — the adapter side lives in the
> input-port file that links here. Paths + symbols only; ask CodeGraph for signatures, never
> record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `Handler.ExternalChargeRowUpdate` — `internal/gateway/handlers/external_charges.go`
- **Trigger:** `PUT /ui/external-charges/row/:id`
- **Module:** `charging` (the domain this serves; the handler itself lives in `internal/gateway`)

## Triggered by

- `input-port/charging/external-charges.md` — the External charges page (`/external-charges`), inline row edit form

## Input / output

- **Input:** form body from `fragments.ExternalChargeRowEdit` — `csrf_token`, `charged_on`, `status`,
  `start_battery_pct`, `end_battery_pct`, `started_at`, `ended_at`, `energy_added_kwh`, `price`,
  `location_kind`, `location_label`, `charging_type`, `notes`, `odometer_km`, plus hidden
  `start` / `end` carrying the active filter window. The vehicle is **not** a form field — it
  comes from the session-selected vehicle. Currency is hardcoded `COP`.
- **Output:** `200` with `fragments.ChargeRowUpdateSuccessOOB` — the static row swap plus an
  out-of-band `#external-charges-list` refresh. `422` re-renders the edit row with per-field errors,
  `500` with a top-of-form error, `403` on CSRF failure, `400` on an unparseable id, `404` when the
  entry is not found among the account's registered vehicles.
- **`start_battery_pct` is optional.** An empty value is accepted and passed to
  `charging.Writer.Update` as absent, so the module derives it from the energy added and the
  ending percentage. A supplied value is validated as an integer in 0–100. The same rule holds on
  the create form.
  _Source: spec gateway — Requirement: Inline Row Editing._

## Flow

1. `Handler.ExternalChargeRowUpdate` — `internal/gateway/handlers/external_charges.go` — auth guard
   (`currentUID`), `checkCSRF` against `csrf_externalcharge`, parse the id.
2. `account.Service.RegisteredVehicles` — tenant vehicle list for the ownership check.
3. `windowFromForm` → `bestEffortWindow` — resolves the filter window to echo back. **Never a
   validation gate** in either direction.
4. `Handler.parseExternalChargeForm` — validates; delegates the required-field rule to
   `charging.RequiredFieldsFor(status)` rather than hardcoding it; runs `vehicleOwned`.
5. `Handler.fetchEntryTeslaIDAndChargedOn` → `charging.Reader.ListEntriesByVehicles` (over the
   account's `RegisteredVehicles`, since `RM58-charging-demote-manual-charge-account-id`, now
   through the shared `h.ownedVehicles` seam) — reads
   the **pre-update** `charged_on`. Once the UPDATE commits the old date is unrecoverable. A miss
   answers `404` and returns — never `403`, so a probed id cannot be confirmed as real.
   ⚠ capped at 100 rows — see `architecture/charge-record-mutation.md`.
6. `Handler.authorizeVehicle` — proves the vehicle stored **on the entry** (not one named by the
   request) against the account's registered vehicles, and returns a `vehicleref.Ref`. An error
   here answers the same `404` as step 5.
7. `charging.Writer.Update` — `internal/charging/service.go` — takes the `Ref` from step 6.
   `normalizeStatus` → `missingFields` → `resolveEnergy` → `UpdateEntry`. The `Ref` overwrites
   `Entry.TeslaID` before anything else runs, so energy derivation always reads the proven car.
   A rejected update writes nothing.
8. `Handler.recalculateAfterExternalChargeWrite` → `analytics.Recalculator.Recalculate(uid, teslaID, D, D)`
   for the new date; called a **second time** for the old date when the edit moved it. Errors
   logged and swallowed.
9. `Handler.buildExternalChargesPage` — re-reads for the OOB list refresh and the aggregation tiles.

## Database

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | `vehicles` | `account.Service.RegisteredVehicles` |
| 2 | READ | `manual_charge_entries` | `charging.Reader.ListEntriesByVehicles` (pre-update date) |
| 3 | WRITE | `manual_charge_entries` | `UpdateEntry` — sets `updated_at = now()`; Postgres recomputes `inferred_capacity_kwh_calc` in the same statement |
| 4 | READ | `vehicle_snapshots` | `telemetry.Reader.SnapshotsByVehicleBetween` + `SnapshotPrecedingDay` |
| 5 | READ | `supercharger_sessions` | `charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween` |
| 6 | READ | `manual_charge_entries` | `charging.Reader.ListEntriesByVehicleBetween` |
| 7 | WRITE | `vehicle_metrics` | `UpsertVehicleMetric` × n **+** `DeleteVehicleMetricsInRangeExcept`, one transaction |
| 8 | READ | `manual_charge_entries`, `vehicles` | `buildExternalChargesPage` |

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
  _Source: `recalculateAfterExternalChargeWrite`._
- **`energy_source` is recomputed on every write and the caller's value is ignored.** Blanking
  the energy on an edit flips a `USER` row to `ESTIMATED`, and vice versa. This is correct and
  centralized in `resolveEnergy`.
  _Source: `charging/service.go` `resolveEnergy`._
- **Error branches re-render from the raw POST values, not from the parsed entry** — a value that
  failed validation has no representation in `charging.Entry`'s typed fields, so only the raw
  string survives to be echoed back.
  _Source: `externalChargeEntryVMFromRawValues`._
- **The filter window is cosmetic on this route.** A malformed `start`/`end` must never turn a
  successful write into an error — that is why this uses `bestEffortWindow`, not
  `parseExternalChargesRange`.
  _Source: `bestEffortWindow` doc comment._
- **A derived starting percentage renders as a `placeholder`, never as a `value`.** When the
  entry's stored `start_battery_pct` provenance is the `charging` module's estimated kind, the
  edit form puts the number in the input's `placeholder`. A save that does not touch the field
  then arrives empty, and `Update` derives it again. A person-typed percentage (or none) renders
  as a normal `value`, as it always did. The gateway reads the provenance through
  `ExternalChargeEntryVM.StartBatterySource`, a plain string — the view model never imports
  `charging`.
  _Source: spec gateway — Requirement: Inline Row Editing._
- **Why the placeholder matters: `Update` requires every mutable field on every call.** A
  pre-filled derived value would come back looking user-supplied and get stamped as the person's
  own reading. The capacity job reads only person-supplied percentages, so that loop would feed
  the computed value back into the constant it was computed from.
  _Source: spec gateway — Requirement: Inline Row Editing._
- **An out-of-range `start_battery_pct` is still rejected; an empty one is not.** When editing a
  test that needs a 422 from this field, use a value like `150`. Omitting the field no longer
  fails, so a test that omits it will pass for the wrong reason or fall through to a different
  field's error.
  _Source: spec gateway — Requirement: Create Charge Entry._
- **`start_battery_pct` sits in the always-visible main grid, marked optional.** It is not moved
  into the "More details" expander, which still holds only `charging_type`, `location_label` and
  `notes`. The field carries its own help line explaining that an empty value is computed.
  _Source: spec gateway — Requirement: location_kind Visible Without Expanding "More Details"._
- **The telemetry battery suggestion is unrelated to the derivation.** The create form's
  `start_battery_pct` placeholder suggestion comes from the vehicle's latest snapshot through
  `analytics.Reader.LatestMetricsForVehicles`. It is a hint on a fresh form. It neither feeds nor
  is fed by the value `charging` derives on save.
  _Source: spec gateway — Requirement: Create Charge Entry._
