## Why

Tier 1 (`RM6-account-add-vehicle-config-fields`, archived) added `exterior_color` and `car_type`
nullable columns to the `account` module's `vehicles` registry, plus a conditional-update port
method — `account.Service.SetVehicleConfigIfEmpty(ctx, accountID, teslaID, exteriorColor, carType)
error` — that writes them back only while at least one is still uncaptured. Nobody calls that port
method yet: every registered vehicle's `exterior_color`/`car_type` stay `NULL` forever until a
caller supplies the two observed values. The nightly telemetry collector already fetches the full
`VehicleData` payload for every vehicle it captures — including the `vehicle_config` sub-object —
so it is the natural, zero-extra-cost caller: no new Tesla Fleet API request, no extra wake, no
extra paid call.

This is **tier 2 of 2** of roadmap `RM6-static-vehicle-config-fields`
(`openspec/roadmaps/RM6-static-vehicle-config-fields.md`), which captures the binding decisions
(RD1–RD8) agreed with the user via the `grill-me` skill at the roadmap level. This proposal and its
sibling artifacts implement RD1, RD2, RD4, RD5, RD6, RD7, and RD8 for the `internal/telemetry`
module (plus the `internal/tesla` leaf grant, RD8). It depends on tier 1 having already landed the
account-side column + port method (it has — `openspec/changes/archive/account/2026-08-03-RM6-account-add-vehicle-config-fields/`).

## What Changes

- **`internal/tesla` (granted leaf path, RD8/T1)** — add `VehicleConfigTesla{ExteriorColor,
  CarType string}` and a `VehicleConfig VehicleConfigTesla` field on `VehicleDataTesla` in
  `internal/tesla/types.go`. This is an additive DTO field only: no new `VehicleService` interface
  method, no `Raw*` sibling, no `cmd/explore-tesla-api` surface change — the existing `VehicleData`
  call already returns the full payload; only the typed extraction is missing. Exactly mirrors the
  `telemetry-add-tire-pressure-columns` T1 precedent.
- **`collectVehicle` / `attemptVehicle` re-signatured (RD7)** — both now take the
  `account.OwnedVehicle` they were destructured from, in place of the separate `accountID int64` +
  `teslaID int64` pair (a net parameter *reduction*). `attemptVehicle` additionally returns a new
  unexported `vehicleConfig{exteriorColor, carType string}` carrier alongside its existing `Reason`
  (RD6): `(Reason, vehicleConfig)`. A failed or short-circuited attempt returns the zero
  `vehicleConfig{}` (both fields `""`), which the caller's empty-string guard (RD5) naturally skips.
- **The write-back guard, port call, and counter live in `collectAccount`, never in `attemptVehicle`
  (RD6)** — `attemptVehicle` stays a pure `[wake→]fetch→map→store` pass that never touches `s.acct`.
  A new unexported `captureVehicleConfig` helper, called from `collectAccount`'s per-vehicle loop
  right beside the existing `record(...)` call:
  - Skips entirely when the vehicle's already-known registry record has **both**
    `ExteriorColor != nil && CarType != nil` (RD2 Go-side guard; tier 1's SQL `WHERE (exterior_color
    IS NULL OR car_type IS NULL)` is the independent, defense-in-depth second guard).
  - Skips entirely when either observed value from this cycle's `vehicleConfig` is `""` (RD5) —
    covers both "Tesla reported an empty string" and "the attempt never reached `VehicleData`"
    (asleep-timeout / unauthorized / api-error all leave `vehicleConfig` zero-valued).
  - Otherwise calls `s.acct.SetVehicleConfigIfEmpty(ctx, v.AccountID, v.TeslaID, cfg.exteriorColor,
    cfg.carType)`; on error, increments a new `report.ConfigCaptureFailures` counter (RD4) — never
    alters the vehicle's `Reason`, never writes a `poll_attempts` row, and never aborts the cycle.
  Because `collectVehicle`'s bounded retry calls `attemptVehicle` at most twice but returns only the
  *final* pass's result to `collectAccount`, at most one write-back attempt happens per vehicle per
  cycle even when the retry fires (RD6).
- **`CycleReport.ConfigCaptureFailures int`** — a new counter on the existing report struct in
  `telemetry.go`, mirroring the `ChargingFetchFailures` precedent. Zero when nothing failed.
- **Scheduler cycle log line** — `LogCycle` in `scheduler.go` gains `config_capture_failures=%d` to
  the existing single operational log line, so a permanently failing back-fill is visible in
  unattended-poller logs rather than silent.
- **Test doubles** — `service_test.go`'s `fakeAccount.SetVehicleConfigIfEmpty` stub (added by tier 1
  purely to satisfy the widened `account.Service` interface) becomes a recording double that
  captures every call (account/vehicle/values) and can be programmed to fail, so the new behavior
  is testable offline.

**Not breaking.** `collectVehicle`/`attemptVehicle` are unexported — their signature change has no
external caller. `CollectAll` (the public `Collector` port method) is untouched in signature;
`CycleReport` gains one additive field (existing callers that don't name it are unaffected — Go
zero-value defaults it to 0). No migration, no new table, no new column, no new index — this tier
touches zero database objects (the account-side schema landed in tier 1).

## Capabilities

### Modified Capabilities

- `telemetry`: two new requirements — the nightly collector's static vehicle-config write-back
  trigger (the skip conditions and the at-most-once-per-vehicle-per-cycle guarantee), and the cycle
  report's new `ConfigCaptureFailures` counter (best-effort, never affects `poll_attempts` or
  `Reason`).

### Consumed Capabilities (no change to their specs)

- `account-vehicle-registry` — this tier is the first real caller of the
  `SetVehicleConfigIfEmpty` port method tier 1 added; no change to that capability's spec.

## Impact

- `internal/telemetry` — primary: `service.go` (re-signature, new `vehicleConfig` type, new
  `captureVehicleConfig` helper, wiring in `collectAccount`), `telemetry.go` (new `CycleReport`
  field), `scheduler.go` (log line), `service_test.go` (recording double + new tests). No change to
  `mapping.go`, `reader.go`, `wake.go`, or any DB migration/query file.
- `internal/tesla` — granted leaf path only: `types.go` gains `VehicleConfigTesla` +
  `VehicleDataTesla.VehicleConfig`. No other file in `internal/tesla` changes.
- `internal/account` — **not touched by this tier.** It already has everything this tier needs
  (tier 1, archived).
- No `internal/gateway` change — no dashboard display of colour/model is proposed by this roadmap.

**No database change, no read path affected.** This tier writes through an existing port method
(tier 1's `SetVehicleConfigIfEmpty`) and reads nothing new. The database design gate does not
trigger — see `design.md` "No Database Objects Touched".
