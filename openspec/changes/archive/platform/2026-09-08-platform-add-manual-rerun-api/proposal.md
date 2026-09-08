# platform-add-manual-rerun-api

Source: MAG-27 — https://linear.app/magus-monitor/issue/MAG-27/manual-rerun-http-api-for-processvehicledata

## Why

`internal/app.Processor.ProcessVehicleData` runs one full sync + charging + analytics
cycle. Today only two things can trigger it: the nightly scheduler at 03:30, or
`go run ./cmd/poller --once` from a shell on the VPS.

The owner cannot run `--once` on the VPS. The poller container itself runs fine there
(`deploy/docker/compose.yaml`'s `poller` service has `restart: unless-stopped`, and the
nightly schedule works). What is missing is a way to force ONE extra cycle on demand,
without SSH access to run a one-off command inside the container. This ticket adds an
HTTP endpoint that does that.

This was RM29 tier 8, parked by roadmap decision D9: "the HTTP API cannot be designed
until `ProcessVehicleData` exists." It exists now (RM29 tier 7). The groundwork is
already in place and unused: `telemetry.TriggeredBy` already has a `TriggeredByAPI`
constant, and `poll_attempts.triggered_by` already stores it — nothing writes it yet.
Only the adapter is missing.

## What Changes

- Add an HTTP listener inside `cmd/poller` (same process as the scheduler, not a new
  binary and not part of `cmd/web`) that exposes one route:
  `POST /internal/rerun/<POLLER_RERUN_TOKEN>`.
- The route has no authentication. Its only protection is a secret path segment read
  from a new env var, `POLLER_RERUN_TOKEN`. If the var is empty or unset, the HTTP
  listener does not start at all — there is no open fallback.
- A cycle already running (from either trigger) makes the route answer `409 Conflict`
  and start nothing. One shared lock covers both the scheduler's nightly tick and this
  route, so the two can never overlap.
- A successful request answers `202 Accepted` immediately with `{"status":"started"}`,
  and the cycle runs in the background. The response cannot carry a run ID —
  `ProcessVehicleData` generates it internally, and this change does not touch that
  signature. The owner finds the run in `telemetry.poll_runs` by
  `triggered_by = 'api'` and its start time.
- Wire `deploy/docker/compose.yaml` (an internal-only port on the `poller` service,
  never published to `0.0.0.0`) and `deploy/docker/Caddyfile` (a path-scoped route to
  `poller`) so the endpoint is reachable from outside the VPS.
- Update `internal/config` to read `POLLER_RERUN_TOKEN`.
- Correct three stale doc claims that call this API "parked" or say it will live in
  its own `cmd/` binary: `internal/app/AGENTS.md`, `internal/app/app.go`'s package
  doc, `cmd/poller/main.go`'s package doc, `internal/telemetry/telemetry.go`'s
  `TriggeredByAPI` comment, root `README.md`, and `cmd/README.md`.
- Add `POLLER_RERUN_TOKEN` to `.env.example` and document the endpoint, with an exact
  `curl` command, in `docs/0-set-up/deployment.md`.

## Not In Scope

- Changing `ProcessVehicleData`'s three steps or their order.
- Anything about the nightly scheduler's own behavior or schedule.
- Returning a run ID from the endpoint (would require widening the `Processor` port —
  out of scope; see design.md D4).
- Unit tests. The owner chose no tests for this change (see design.md and tasks.md
  header). Verification is `go build ./...`, `go vet ./...`, `gofmt -l`, plus the
  owner calling the endpoint on the VPS.

## Impact

- **Breaking:** No. No existing Go signature changes. `ProcessVehicleData`,
  `Processor`, and `telemetry.CycleReport` are all unchanged.
- **Modules affected:** `internal/config` (new env var read), `cmd/poller`
  (composition root — new HTTP listener, new lock, new handler; this is where all the
  new logic lives), plus cross-cutting deploy files (`deploy/docker/compose.yaml`,
  `deploy/docker/Caddyfile`) and docs (`internal/app/AGENTS.md`, `cmd/README.md`,
  root `README.md`, `.env.example`, `docs/0-set-up/deployment.md`). No domain module
  (`telemetry`, `charging`, `analytics`, `account`) changes.
- **Read paths affected:** None. This change writes `triggered_by = 'api'`; nothing
  reads or filters by it yet (see design.md D7 — no index, no new query).
- **Database objects:** None. No migration, no new table, no new column, no new
  index. See design.md → "Database objects" — the `database` design gate does not
  apply to this change.
