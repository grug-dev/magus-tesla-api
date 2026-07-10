## Why

Developers need to see the **complete, real JSON** the Tesla Fleet API returns — including the
many fields the typed `...Tesla` DTOs deliberately drop — to learn the API and decide what to
model next. These calls cost money and wake the car, so they must never run under `go test`; they
belong in an on-demand tool invoked explicitly.

## What Changes

- Add raw, undecoded payload access to the `internal/tesla` adapter: `ListVehiclesRaw`,
  `VehicleDataRaw`, and `WakeUpRaw`, each returning `json.RawMessage`. These reuse the adapter's
  existing authenticated request path and stay **off** the `VehicleService` interface, so domain
  callers are unaffected.
- Add a new `cmd/explore-tesla-api` runnable (`package main`, no `_test.go`) that reads
  `TESLA_ACCESS_TOKEN` from `.env` via `config.Load()`, lists vehicles, wakes and polls the chosen
  vehicle until it reports online, then pretty-prints the full raw Fleet API JSON to stdout.
- Optional `make explore-tesla-api` convenience target.
- Not breaking. No change to any existing method, interface, or DTO.

## Capabilities

### New Capabilities
- `tesla-exploration`: raw, undecoded access to each Tesla Fleet API endpoint for on-demand
  inspection tooling. Implemented inside `internal/tesla` (an intentional exception to the
  usual one-capability-per-module mapping: it is a distinct, tooling-facing behavior that must
  not touch the typed `tesla` contract).

### Modified Capabilities
<!-- None. The typed VehicleService contract and existing tesla requirements are unchanged. -->

## Impact

- **Code:** new `internal/tesla/raw.go` (raw sibling methods on `*Client`); new
  `cmd/explore-tesla-api/main.go`; optional `Makefile` target. Existing `internal/tesla` typed
  methods, `VehicleService`, and all current callers (`internal/gateway`) are untouched.
- **Modules affected:** `tesla` (additive). No DB, no migrations, no new dependencies.
- **Test/cost isolation:** `cmd/explore-tesla-api` is `package main` with no tests, so
  `go test ./...` never compiles or runs it; a real (paid) Fleet API call happens only when the
  command is run explicitly.
- **Runtime prerequisite:** a fresh `TESLA_ACCESS_TOKEN` in `.env` (8h lifetime); re-run
  `cmd/setup` when stale. An expired token surfaces the existing `tesla.ErrUnauthorized`.
