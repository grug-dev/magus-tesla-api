## Why

Tier 1 of the `nightly-vehicle-telemetry` roadmap (`openspec/roadmaps/nightly-vehicle-telemetry.md`
— its **Decisions** section is binding; the grill-me interview was executed by the pipeline leader
and its outcomes are recorded there). The future `internal/telemetry` module will store one anchor
snapshot per vehicle per night as **raw JSONB plus extracted typed columns**. Today the adapter's
typed `VehicleData` discards the raw Fleet API body after decoding, so a caller wanting both the
typed fields and the lossless payload would have to pay for **two** Fleet API calls — and even then
the typed DTO lacks two of the columns the Decisions require (charge limit, sentry mode).

The typed wake method the tier calls for (`WakeUp`) already exists on the adapter and already
returns the resulting vehicle state; this change locks its behavior into the delta review scope
without altering it.

## What Changes

- **`VehicleService.VehicleData` returns the raw payload alongside the parsed DTO** — new
  signature `VehicleData(ctx, creds, vehicleID) (*VehicleDataTesla, json.RawMessage, error)`.
  Both values come from a **single** Fleet API request (one paid call), and the raw bytes are the
  unmodified `vehicle_data` response object, suitable for lossless JSONB storage.
- **Enrich `VehicleDataTesla`** with the snapshot fields the roadmap Decisions require that are
  missing today: `ChargeLimitSoc` (`charge_state.charge_limit_soc`) and `SentryMode`
  (`vehicle_state.sentry_mode`). Neither is a miles/mph value, so no new `Km()`/`Kmh()`
  companions are needed; every existing miles/mph field already has one (audited as a task).
- **`WakeUp` stays as-is**: issues the `wake_up` command and returns the vehicle's reported state.
  No wait/retry loop is added — polling-until-online remains the caller's orchestration, per the
  existing "Wake-Before-Data Ordering" requirement.
- **Explorer sync rule honored by audit** (`CLAUDE.md` §Tesla API Exploration): no *new* Fleet API
  endpoint is added, and the `Raw*` siblings (`VehicleDataRaw`, `WakeUpRaw`) plus their
  `cmd/explore-tesla-api` coverage already exist. A dedicated task verifies that coverage still
  matches the adapter one-to-one and reconciles `cmd/explore-tesla-api/README.md` if drift is
  found. The exploration capability stays strictly test-free.
- **First typed-method tests for the module**: `internal/tesla` currently has no `_test.go` at
  all. The typed methods gain tests against a local fake Fleet API (httptest) — never the live
  API. Only `raw.go` and the explorer remain test-free.

## Capabilities

### New Capabilities
<!-- None. -->

### Modified Capabilities
- `tesla`: the Full Vehicle Snapshot requirement now returns the raw payload alongside the typed
  snapshot from a single fetch, and the snapshot additionally exposes charge limit and
  sentry-mode status.

## Impact

- **Breaking: YES.** The `VehicleService.VehicleData` method signature changes (adds a
  `json.RawMessage` return value). Every implementer of the interface must adapt.
- **Modules affected:**
  - `tesla` (owner) — `internal/tesla/client.go`, `vehicles.go`, `types.go`, new test files.
  - `gateway` (compile-only) — the test fake `fakeTesla.VehicleData` in
    `internal/gateway/handlers/handlers_test.go` implements `VehicleService` and needs a
    mechanical one-line signature update. No production gateway code calls `VehicleData` or
    `WakeUp` today, so runtime behavior is unchanged. This cross-module edit is integrated by
    the pipeline leader (outside the tesla worker's sandbox).
- **No DB, no migrations, no new dependencies, no config changes.** The adapter remains stateless
  about identity (credentials passed per call).
- **Cost/test isolation preserved:** new tests use a local httptest server only;
  `internal/tesla/raw.go` and `cmd/explore-tesla-api/` keep zero `_test.go` files, so
  `go test ./...` still never touches the paid Fleet API.
