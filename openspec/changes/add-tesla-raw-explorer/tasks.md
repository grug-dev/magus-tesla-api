> **No tests for this capability.** Per `CLAUDE.md`, `internal/tesla/raw.go` and everything under
> `cmd/explore-tesla-api/` must have no `_test.go` files and no test may call the `Raw*` methods —
> its calls are paid and wake the car. That is deliberate, not an omission.

## 1. Adapter: raw payload access

- [x] 1.1 Add `internal/tesla/raw.go` with a file doc comment stating these methods are for exploration tooling (`cmd/explore-tesla-api`), that domain code must use the typed methods in `vehicles.go`, and the sync rule.
- [x] 1.2 Implement `func (c *Client) ListVehiclesRaw(ctx context.Context, creds Credentials) (json.RawMessage, error)` — reuse `c.get` with `/api/1/vehicles` decoding into a `json.RawMessage`.
- [x] 1.3 Implement `func (c *Client) VehicleDataRaw(ctx context.Context, creds Credentials, vehicleID int64) (json.RawMessage, error)` — reuse `c.get` with the `/api/1/vehicles/{id}/vehicle_data` path.
- [x] 1.4 Implement `func (c *Client) WakeUpRaw(ctx context.Context, creds Credentials, vehicleID int64) (json.RawMessage, error)` — reuse `c.post` with the `/api/1/vehicles/{id}/wake_up` path.
- [x] 1.5 Confirm the raw methods are NOT added to the `VehicleService` interface (contract isolation) and that a 401 still returns `ErrUnauthorized` (it routes through `do`).

## 2. Runnable: cmd/explore-tesla-api

- [x] 2.1 Create `cmd/explore-tesla-api/main.go` as `package main` with no `_test.go` file.
- [x] 2.2 Load config via `config.Load()`; if `cfg.AccessToken == ""`, print a "run `go run ./cmd/setup` first" hint and exit non-zero.
- [x] 2.3 Build `tesla.NewClient()` and `tesla.Credentials{AccessToken: cfg.AccessToken}`; add an optional `-i` index flag (default 0) to pick a vehicle.
- [x] 2.4 Call `ListVehiclesRaw`, pretty-print to stdout via `json.Indent`; locally unmarshal a small `{Response:[{id,state,display_name}]}` struct to select the target vehicle.
- [x] 2.5 If the chosen vehicle is not `online`, call `WakeUpRaw` (print), then poll `ListVehiclesRaw` (~3s interval, ~60s cap) until it reports `online` or time out.
- [x] 2.6 Call `VehicleDataRaw` for the vehicle and pretty-print the full snapshot to stdout; send all progress logs to stderr.
- [x] 2.7 On `errors.Is(err, tesla.ErrUnauthorized)` anywhere, print the "access token expired — re-run `go run ./cmd/setup`" hint and exit non-zero.

## 3. Docs, convenience & verification

- [x] 3.1 Add `cmd/explore-tesla-api/README.md` (how to run, cost/wake warning, flags, sync rule).
- [x] 3.2 Add CLAUDE.md rules: no tests for `tesla-exploration`, and keep the explorer in sync with new tesla adapters.
- [x] 3.3 Add a `make explore-tesla-api` target running `go run ./cmd/explore-tesla-api` with a help comment noting it costs a real API call and wakes the car.
- [x] 3.4 `go build ./...` and `go vet ./...` pass.
- [x] 3.5 `go test ./...` stays green and fast; confirmed `cmd/explore-tesla-api` and `internal/tesla` report `[no test files]`, no Tesla calls fire from the test run.
- [ ] 3.6 With a fresh `TESLA_ACCESS_TOKEN` in `.env`, run `go run ./cmd/explore-tesla-api` and confirm raw `ListVehicles` JSON, wake/poll progress on stderr, and the full raw `vehicle_data` JSON on stdout. _(Requires a live token — user to verify.)_
