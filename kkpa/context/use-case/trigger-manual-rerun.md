# Trigger a manual rerun — `POST /internal/rerun/<token>`

> One external entry point, one output. **Backend only** — the adapter side lives in
> `input-port/manual-rerun-endpoint.md`. Paths + symbols only; ask CodeGraph for signatures,
> never record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `rerunHandler` — `cmd/poller/rerun.go`
- **Trigger:** `POST /internal/rerun/<POLLER_RERUN_TOKEN>`
- **Module:** `n/a` — the entry point lives in `cmd/poller`, which is not a child of
  `internal/` and so is not a module by this project's definition.

## Triggered by

- `input-port/manual-rerun-endpoint.md` — the endpoint itself. There is no UI page.

## Input / output

- **Input:** none. The whole request is the path. Its last segment is the shared secret,
  `POLLER_RERUN_TOKEN`. No body, no header, no session.
- **Output:**
  - `202 Accepted`, `{"status":"started"}` — a cycle started.
  - `409 Conflict` — a cycle is already running, from either trigger. Nothing was started.
  - `404 Not Found` — wrong secret, or no secret configured, so the route does not exist.
- The response carries **no run identifier**. Find the run afterward in `telemetry.poll_runs`
  where `triggered_by = 'api'`, newest first.

## Flow

1. `rerunHandler` — `cmd/poller/rerun.go` — the closure built at wiring time. It holds the
   poller's root context, never the request's.
2. `guardedProcessor.TryStartAPIRun` — `cmd/poller/rerun.go` — tries the shared mutex. On
   failure it returns `ok == false` and the handler answers `409`. On success it returns a
   `start` func that owns the lock.
3. `rerunHandler` writes `202` and the body, then runs `start` in a goroutine.
4. `app.Processor.ProcessVehicleData` — `internal/app/processor.go` — called with
   `telemetry.TriggeredByAPI`. **From here the path is identical to the nightly one.**
5. `telemetry.LogCycle` — logs the cycle, exactly as the scheduler and `--once` do.

**The three steps inside `ProcessVehicleData` are not repeated here.** They belong to the
cycle, not to this call path — see `architecture/nightly-cycle.md`.

## Database

**This use case touches no table of its own.** Every read and write belongs to the cycle it
starts. The full table-effects table, per step, lives in `architecture/nightly-cycle.md`.

The one row that identifies a run started this way:

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | WRITE | `telemetry.poll_runs` (`triggered_by = 'api'`) | `telemetry.RunWriter.RecordRun`, via the cycle |

## Entities involved

- `entities/vehicle-metrics/guide.md` — what step 3 of the cycle derives.

## Related use cases

- `architecture/nightly-cycle.md` — the same cycle, and its other trigger, the scheduler.
- `architecture/telemetry-ingest-only.md` — `poll_runs` and the `triggered_by` column.

## Conventions & gotchas

- **No secret configured means no endpoint at all.** There is no state where it answers
  without one. Not open, not a default secret — the route is never registered.
  _Source: spec `manual-rerun-api` — Requirement: The Endpoint Is Never Open By Accident._
- **The secret is the last path segment of the registered route, so there is no comparison to
  get wrong.** A wrong secret is a plain `404` from `http.ServeMux`. Never add a manual string
  check; that is what this design avoids.
  _Source: `cmd/poller/main.go`; design.md D5 of `platform-add-manual-rerun-api`._
- **The secret rides in the URL, not a header.** It reaches Caddy access logs and shell
  history. Accepted knowingly for a single-user tool.
  _Source: spec `manual-rerun-api`; design.md D2._
- **Only one cycle ever runs at a time, across every trigger.** One `sync.Mutex` inside
  `guardedProcessor` covers the scheduler tick, `--once`, and this endpoint. Any new caller of
  `ProcessVehicleData` must go through `guardedProcessor`, never the raw processor.
  _Source: `cmd/poller/rerun.go`; design.md D3._
- **The scheduler can lose a night.** If a manual cycle still holds the lock when 03:30
  arrives, that night's collection does not run. It is logged as skipped and the schedule
  continues. Deliberate, and in the spec.
  _Source: spec `manual-rerun-api`; design.md D3 "Accepted cost"._
- **The background goroutine must use the poller's ROOT context.** `net/http` cancels the
  request context as soon as the handler returns, and this handler returns immediately. Using
  `r.Context()` would kill the cycle in microseconds. This is the single easiest bug to
  reintroduce here.
  _Source: `cmd/poller/rerun.go` (`rerunHandler` doc comment); design.md D4(b)._
- **A token that cannot be a URL path segment turns the listener OFF, it never panics the
  poller.** `http.ServeMux` panics on a malformed pattern; `newRerunMux` recovers it and
  returns an error. The nightly cycle matters more than this endpoint.
  _Source: `cmd/poller/rerun.go` (`newRerunMux`); design.md D9._
- **The response can never carry a run ID** without widening
  `Processor.ProcessVehicleData`'s signature, which RM29 D7/D10 fixed.
  _Source: design.md D4(a)._
