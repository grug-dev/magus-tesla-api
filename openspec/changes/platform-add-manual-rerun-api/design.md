# Design — platform-add-manual-rerun-api

> Numbering note: **D1–D4 each transcribe one settled owner decision** from the
> pickup interview (cited in their heading). They are not re-opened here. **D5–D8**
> are buildability decisions this artifacts pass had to make to turn D1–D4 into a
> buildable change — each says so in its heading.

## Context

RM29 tier 7 built `internal/app.Processor.ProcessVehicleData` — one method, three
steps, callable by any trigger that passes a `telemetry.TriggeredBy` value. The
roadmap's own diagram always showed two peer callers:

```
Scheduler ──┐
            ├──> ProcessVehicleData ──┬── Sync Fleet data       (telemetry)
API ────────┘                         ├── Process Charging data (the T6 mirror)
                                      └── Recalculate Analytics (analytics)
```

`Scheduler` was built. The `API` side was parked (roadmap D9): "the HTTP API cannot
be designed until `ProcessVehicleData` exists." It exists. This change designs and
builds the `API` side.

Two facts already exist, unused, and this change is what starts using them:

- `telemetry.TriggeredBy` already has `TriggeredByAPI = "api"`
  (`internal/telemetry/telemetry.go`), documented today as "a future manual re-run
  via HTTP (RM29 tier 8, parked)." That comment goes stale the moment this change
  lands — see the doc task list.
- `poll_attempts.triggered_by` (and, since RM36, `poll_runs.triggered_by`) already
  stores whatever `TriggeredBy` value a caller passes. No column and no migration
  changes here — only what writes into the existing column changes.

**Verified: the nightly schedule is not broken.** `deploy/docker/compose.yaml`'s
`poller` service runs with `restart: unless-stopped` and needs no operator action for
its 03:30 cycle. The ticket's "the VPS does not allow me to run the poller" means the
owner cannot force an *extra*, on-demand cycle without SSH — not that the nightly
cycle fails. This change adds the on-demand trigger; it changes nothing about the
schedule.

**Verified: the `poller` service is not reachable from outside today.**
`docker-compose.yaml`'s `poller` service publishes no `ports:` and has no entry in
`deploy/docker/Caddyfile`. Reaching it needs both an internal port the container
listens on and a Caddy route — see D8. The existing security posture (`db`'s
`127.0.0.1:5432:5432` comment) is explicit that a port must never be published to
`0.0.0.0` without a firewall in front of it; this change keeps that posture for
`poller` by never adding a `ports:` entry for it — only Caddy, already the sole
service reachable from outside, gets a route to it.

**Verified: the container's hardening needs no change.** `poller` runs with
`read_only: true`, `cap_drop: ALL`, and a `/tmp` tmpfs. An HTTP listener on a port
above 1024 needs no Linux capability (only ports below 1024, like Caddy's 80/443,
need `CAP_NET_BIND_SERVICE`) and writes nothing to disk. No hardening block changes.

## Goals / Non-Goals

**Goals:**
- One HTTP route, reachable from outside the VPS, that starts exactly one
  `ProcessVehicleData(ctx, telemetry.TriggeredByAPI)` cycle.
- Fail closed: no token configured means no route, ever.
- Never let two cycles (any mix of scheduler and API) run at once.
- Answer fast; do not make the owner's browser/`curl` wait for a sleeping car to wake.

**Non-Goals:**
- Authentication, rate limiting, or a hard-to-guess-but-structured token scheme
  beyond "a string the owner picks and puts in `.env`." The owner accepted this
  cost knowingly (D2).
- Returning the run's identifier synchronously (D4).
- Any change to what a cycle does or the order of its three steps.
- A queue for overlapping requests (D3).

## Decisions

### D1 — The listener lives inside `cmd/poller`, not `cmd/web` or a new binary (owner decision)

`cmd/poller` already builds one `Processor` from ten collaborator ports and starts
`internal/app`'s `Scheduler` with it (`main.go`, current code). This change adds a
small `net/http` listener to the same process, built from the **same** `Processor`
value the scheduler already holds. Because both triggers share one `Processor`
instance, they cannot drift apart in how a cycle is composed.

**Rejected — an endpoint on `cmd/web`'s gateway.** `cmd/web` does not build a
`Processor` today; adding one there duplicates roughly the 40 lines of port wiring
`cmd/poller/main.go` already has, and now two files must be kept in sync every time a
`NewProcessor` argument changes.

**Rejected — a new `cmd/` binary (e.g. `cmd/api`).** Same duplication as above, plus
a new Dockerfile build target and a sixth `compose.yaml` service for one route.

**Consequence for the docs.** `internal/app/AGENTS.md` currently states: "the tier-8
API will live in its own `cmd/` binary." That sentence is now wrong — this decision
supersedes it. Fixing it is task DOC1.

### D2 — No authentication; a secret path segment from `.env`, fail-closed (owner decision)

New env var `POLLER_RERUN_TOKEN`, read by `internal/config`. Route:

```
POST /internal/rerun/<POLLER_RERUN_TOKEN>
```

**Fail-closed is load-bearing, not a nice-to-have.** If `POLLER_RERUN_TOKEN` is empty
(unset, or set to an empty string), `cmd/poller` does not start the HTTP listener at
all — no port is opened, no route exists. There is no code path that serves an open
or default-secret route. See the spec's "The Endpoint Is Never Open By Accident"
requirement.

**Rotation is an `.env` edit plus a container restart** (`docker compose up -d` picks
up the new value; no rebuild, no code change).

**Accepted cost, recorded by the owner knowingly.** The secret appears in plaintext
in the URL, and therefore in Caddy's access logs and in shell history on any machine
that runs the `curl` command. This is the same class of cost as any bearer token in a
URL rather than a header, and the owner chose it anyway for simplicity — a single-user
tool with no other consumer. No mitigation is proposed here; none was asked for.

**Rejected — a header-based token (`Authorization: Bearer <token>`).** Marginally
better logging hygiene, but the owner explicitly asked for "a resource name hard to
guess" — i.e. the path itself as the secret — so this was not the ask.

### D3 — Overlap protection: one shared lock, held in `cmd/poller`, covering both triggers (owner decision + buildability)

**The problem the owner's own framing surfaces.** A queue or a naive per-caller lock
both fail: `app.Scheduler.Run` calls `s.processor.ProcessVehicleData(...)` directly,
inside `internal/app`, once a day. If the HTTP handler only locked around its own
call, a 03:30 nightly tick and a manual API call could still run at the same time —
two full paid Fleet API cycles, writing the same tables, at once. The lock must sit
somewhere **both** call paths pass through.

**Chosen: a small guard type in `cmd/poller`, built once, wrapping the single shared
`Processor`.** `cmd/poller` already builds one `Processor` and hands it to
`app.NewScheduler`. This change wraps that value once, in a new
`cmd/poller/rerun.go`:

```go
// guardedProcessor serializes every call to the wrapped Processor behind one
// mutex, so the scheduler's nightly tick and an API-triggered rerun can never
// overlap. Built once in main() and shared by both call sites.
type guardedProcessor struct {
	inner app.Processor
	mu    sync.Mutex
}

// ProcessVehicleData implements app.Processor — this is what Scheduler.Run calls.
// Synchronous: acquire, run the whole cycle, release. If a cycle (from either
// trigger) is already running, it returns immediately with errCycleBusy instead of
// running. Scheduler.Run already logs and swallows any cycle error without
// stopping the schedule, so this needs no change to internal/app.
func (g *guardedProcessor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	if !g.mu.TryLock() {
		return telemetry.CycleReport{}, errCycleBusy
	}
	defer g.mu.Unlock()
	return g.inner.ProcessVehicleData(ctx, triggeredBy)
}

// TryStartAPIRun attempts the SAME lock for a background, API-triggered run. On
// success it returns a start func the caller must run exactly once, in a new
// goroutine — it releases the lock when the cycle finishes. On failure (a cycle
// from either trigger already holds the lock) it returns ok=false and touches
// nothing: the HTTP handler must not spawn a goroutine in that case.
func (g *guardedProcessor) TryStartAPIRun(ctx context.Context) (start func(), ok bool) {
	if !g.mu.TryLock() {
		return nil, false
	}
	return func() {
		defer g.mu.Unlock()
		report, err := g.inner.ProcessVehicleData(ctx, telemetry.TriggeredByAPI)
		telemetry.LogCycle(report, err)
	}, true
}
```

`main()` builds `processor := app.NewProcessor(...)` exactly as today, then wraps it
ONCE, unconditionally, before branching on `--once`:
`guarded := &guardedProcessor{inner: processor}`. Every call site downstream uses
`guarded`, never the raw `processor` — the `!*once` branch passes it to
`app.NewScheduler(guarded, ...)`, and the `--once` branch calls
`guarded.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)` in place of today's
direct `processor.ProcessVehicleData(...)` call (inert for `--once`'s one-shot run,
but it means zero code paths call `ProcessVehicleData` un-guarded). The HTTP handler
(D4) calls `guarded.TryStartAPIRun(ctx)` directly. One `sync.Mutex`, one instance,
every path.

**Why not put the lock inside `internal/app`.** `internal/app/AGENTS.md`'s
`ProcessVehicleData` doc guarantees it "returns `telemetry.CycleReport` unchanged"
(RM29 D7) — internal/app introduces no new report/result type. Teaching the port
itself about "busy" would either overload that one error return with a second,
unrelated meaning (whole-cycle sync failure vs. lock contention — two different
callers need to react to these differently) or widen the `Processor` interface for a
concern (serializing HTTP-triggered concurrency) that only the API caller's own
composition root needs to solve. A lock is not HTTP — the constraint that
`internal/app` must not learn about HTTP still holds either way — but a lock IS a new
piece of behavior on a port design.md already fixed (RM29 D7/D10), and the minimum-
diff answer is to add nothing there. `internal/app`'s `Processor` interface and
`processor` struct are untouched by this change.

**Why not two independent locks (one in the handler, one wrapping the scheduler
call).** Two separate `sync.Mutex` values can never observe each other — this is
exactly the naive mistake the owner's own framing warns against. It would compile,
look correct, and still let a nightly tick and an API call run concurrently.

**Accepted cost — a nightly run can be skipped, and the owner chose this.** The guard
uses `TryLock` on **both** paths, so the rule cuts both ways. If a manual cycle is
still running when the scheduler's tick fires, `ProcessVehicleData` returns
`errCycleBusy` and **that night's scheduled collection does not run at all**. It is
logged, the schedule is not stopped, and the next day runs normally.

The window is small: the tick fires at one minute of the day, and a cycle takes
1-3 minutes, so a manual run must start within roughly three minutes of the scheduled
time to cause it. The owner was shown this and chose it over the alternative
(a blocking `Lock` on the scheduler path only, which would make the nightly run wait
its turn and never be skipped). The reason to reject that alternative is one lock
behaviour instead of two, at the price of one lost night in a rare race.

This is a real, if small, change to the scheduler's behaviour, which the ticket's
"not in scope" line otherwise protects. It is named here because it is a consequence
of the lock, not a change to the scheduler's own code: `internal/app/scheduler.go` is
untouched. The spec covers it — see the scenario "The scheduler skips its turn when a
manual cycle is running".

**Rejected — queue the second call.** The caller waits for the first cycle to finish
(1-3 minutes, longer if a car is asleep) before its own even starts — worse than the
`curl` command timing out for no good reason — and two full paid Fleet API cycles
still eventually run back-to-back, which the owner does not want.

**Rejected — allow concurrent runs.** Both cycles write the same tables
(`vehicle_metrics` reconciliation, `poll_attempts`, `poll_runs`) and both pay the
Fleet API; nothing about the schema or the vendor API is safe under two writers at
once.

### D4 — Response: `202 Accepted`, cycle runs in the background, on the poller's root context (owner decision)

The handler answers immediately; the cycle runs in a goroutine (`start()` from D3),
via `go start()`. The owner does not wait 1-3 minutes for a car to wake.

**(a) The response body cannot carry a run ID.** `ProcessVehicleData` generates its
`RunID` internally (`uuid.New()`), inside `internal/app`, after the caller has
already received its `202`. Returning the ID to the HTTP caller would require
widening `Processor.ProcessVehicleData`'s signature — explicitly out of scope
(RM29 D7/D10 fixed this signature; the ticket does not ask to reopen it). The
response body is exactly:

```json
{"status":"started"}
```

and nothing more. The owner finds the run afterward in `telemetry.poll_runs` by
`triggered_by = 'api'` plus its `started_at` — see the Manual verification contract
below and the deployment doc task (DOC-D).

**(b) The background goroutine MUST run on `cmd/poller`'s root context — the
`signal.NotifyContext` ctx built once in `main()` — never the HTTP request's
`r.Context()`.** `net/http` cancels a request's context the moment the handler
returns and the response has been written. Since the handler returns immediately
(that is the whole point of `202`), a goroutine started from `r.Context()` would have
its context cancelled within microseconds of the response being flushed — killing
the cycle before it does anything. **This is the single most likely bug in this
change; task IMPL-C names it explicitly, and design.md's Manual verification
contract includes a check for it (V4).** The fix: the handler function is built with
the root `ctx` bound at wiring time (a closure), and it is that `ctx` — never
anything derived from the `*http.Request` — that goes into `TryStartAPIRun(ctx)` and
then into `ProcessVehicleData(ctx, ...)`.

`telemetry.CycleReport` is reused unchanged (RM29 D7 still holds — this change adds
no new report/result type). It is never serialized to the HTTP client; it goes only
to `telemetry.LogCycle(report, err)` inside `start()`, exactly as the `--once` path
already does.

**Rejected — block the HTTP response until the cycle finishes (synchronous
`200`).** The owner explicitly rejected waiting 1-3 minutes for a sleeping car.

### D5 — HTTP stack: `net/http` stdlib, not Gin (buildability — mine)

`cmd/poller` imports no HTTP package today; `internal/gateway` uses Gin. This change
adds exactly one route with a literal, fully-known-at-startup path (the secret is
baked into the registered pattern itself — see below) and no middleware, templating,
or session needs. Go 1.22+'s stdlib `http.ServeMux` supports method-qualified
patterns natively (`go.mod` pins Go 1.25.7, well past that): `mux.HandleFunc("POST "+path, handler)`.

**The registered path bakes the secret in — no manual comparison needed.** Rather
than registering a wildcard (`POST /internal/rerun/{token}`) and comparing the
captured value inside the handler, this change registers the literal path built from
the configured secret at startup:

```go
path := "/internal/rerun/" + cfg.PollerRerunToken
mux.HandleFunc("POST "+path, rerunHandler(ctx, guarded))
```

`http.ServeMux` does exact segment matching, so any other path — including a
near-miss on the token — gets the mux's own default 404, with no handler code
involved in the comparison at all. This is simpler than a wildcard route plus a
manual string-equality check, and there is nothing to get subtly wrong (e.g. a
non-constant-time compare) because there is no comparison in application code to get
wrong.

**Why not Gin.** One route, no templating, no session, no middleware chain — Gin's
router, binding, and rendering machinery buys nothing here. Per `CLAUDE.md`'s
AI-efficiency principle, adding a second HTTP framework to a binary that currently
imports none is exactly the kind of indirection that costs tokens (a new dependency
to learn, a new import to explain in `cmd/poller`'s otherwise Gin-free doc comment)
without buying change-locality on a volatile surface — this route is not expected to
grow. If `cmd/poller` ever grows a second or third route with real routing needs,
that is the point to revisit, not now.

### D6 — Internal listen port is a hardcoded constant, not a new config var (buildability — mine)

The listener binds `:8081` (a package-level constant in `cmd/poller`, e.g.
`const rerunAddr = ":8081"`). This is the second and last new "surface" this change
adds to the container network, after the token itself.

**Why hardcode instead of adding `POLLER_RERUN_PORT` to `.env`.** The port is never
published to the host (D8) and never chosen by the owner — only Caddy, inside the
same compose network, ever dials it, by the fixed service name `poller`. A config
knob with no legitimate reason to vary is pure token cost for future readers, against
`CLAUDE.md`'s "do not over-abstract" guidance. `cfg.Port` (the existing web-gateway
port variable) is not reused here — its doc comment scopes it to `cmd/web`
specifically, and giving it a second, different meaning for `cmd/poller` would be a
worse false economy than one more constant.

### D7 — No database object, no index, in this change (buildability — mine)

This change writes `triggered_by = 'api'` into columns that already exist
(`poll_attempts.triggered_by`, `poll_runs.triggered_by` — both added by RM29/RM36).
**No migration, no new column, no new index, no new table.** Nothing in this change
reads or filters by `triggered_by` — the owner finds a run by eye (`ORDER BY
started_at DESC LIMIT 1` in the Manual verification contract below), not through a
new query path. RM29 D7 already deferred an index on this column until a reader
exists; this ticket does not add that reader, so the deferral still holds.

Because there is no database object at all in this change, the `database` design
gate (`openspec/config.yaml` → `rules.design`) does not apply — recorded here
explicitly so a reviewer can see the gate was considered and found not to apply,
rather than silently skipped.

### D8 — Compose and Caddy: an internal-only port, routed by path (buildability — mine)

**`compose.yaml`.** Add `expose: ["8081"]` to the `poller` service. This is
documentation, not a security boundary — containers on the same Compose network can
already reach each other by service name and any port without an `expose:` entry;
`expose:` only makes the port's existence visible to a reader of the file (mirroring
why `db`'s port comment exists) without publishing anything to the host. **No
`ports:` entry is added** — the existing rule the `db` service's own comment states
(a bound host port bypasses `ufw` via Docker's own iptables rules) applies just as
much to `poller`, and there is no reason for anything outside the Compose network to
dial `poller:8081` directly; only Caddy needs to.

**`Caddyfile`.** The current file is one bare `reverse_proxy web:8080` for the whole
domain. Confirmed against Caddy's own docs (`handle` directive): `handle` blocks at
the same nesting level are evaluated in order and are mutually exclusive — the first
matching block wins, and a `handle` with no matcher is the fallback. This change
restructures the file to:

```caddyfile
{$BASE_DOMAIN} {
	handle /internal/rerun/* {
		reverse_proxy poller:8081
	}
	handle {
		reverse_proxy web:8080
	}
}
```

Any request whose path starts with `/internal/rerun/` goes to `poller:8081`;
everything else keeps going to `web:8080`, unchanged. Caddy does not need to know the
secret — it forwards the whole subtree, and `poller`'s own `http.ServeMux` (D5) is
what actually accepts or 404s a specific path.

**Reverse-direction check — existing `make` targets and guards** (verified against
the actual `Makefile` and `openspec/config.yaml`, not assumed):

- **`migration-guard`.** Unaffected — no migration file in this change.
- **`boundary-guard`.** Unaffected — it scans `internal/gateway/**/*.go` for an
  `internal/telemetry` import; this change touches no gateway file.
- **`archive-guard`.** Unaffected — no file under `openspec/changes/archive/` is
  touched.
- **`ui-guard` / `i18n-guard` / `money-guard` / `tz-guard` / `theme-guard`.**
  Unaffected — no `internal/gateway/templates` or `internal/gateway/handlers` file
  changes, and the one new timestamp-adjacent value (`started_at`/`finished_at`) is
  already produced inside `internal/app`/`internal/telemetry` via `internal/clock`,
  unchanged by this ticket.
- **`sqlc`.** Unaffected — no `query.sql` change, no migration, so no generated type
  changes shape.
- **`env-setup` (Makefile target).** Checked: it has its own fixed list of vars it
  prompts for (`SESSION_SECRET`, the four OAuth credential vars, `DATABASE_URL`,
  `PORT`, `BASE_URL`) and explicitly documents which vars it skips as optional. This
  change does **not** add `POLLER_RERUN_TOKEN` to that prompt list: the var is
  optional-by-design (D2's fail-closed behavior means "unset" is a valid, working
  state — the endpoint is simply off), which is exactly the class of var `env-setup`
  already declines to prompt for (mirroring `TESLA_ACCESS_TOKEN` /
  `MAGUS_DB_PASSWORD`, both left to manual `.env` edits). Forcing a prompt would
  contradict the opt-in design D2 states.

Finding: all of the above are unaffected, verified by reading the Makefile and the
guard implementations, not assumed unaffected because "it's just one route."

## Database objects

**This change adds no table, column, index, constraint, view, or migration.** See D7
for the full reasoning. The `database` design gate does not apply.

## Manual verification contract (replaces a test contract — Unit tests: excluded)

The owner runs these by hand on the VPS after deploy. Exact, copy/paste-ready.

**V1 — the route is off when the token is unset.** With `POLLER_RERUN_TOKEN` empty
or absent from `.env`, after `docker compose ... up -d`:
```bash
docker compose --project-directory . -f deploy/docker/compose.yaml logs poller --tail=20
```
Expect no "rerun endpoint listening" log line (or whatever startup line task IMPL-B
adds), and no successful connection to `poller:8081` from inside the `caddy`
container — the listener never started.

**V2 — a normal trigger returns 202 and starts a cycle.** With
`POLLER_RERUN_TOKEN=changeme123` set and the stack restarted:
```bash
curl -i -X POST https://<domain>/internal/rerun/changeme123
```
Expect immediately (well under a second):
```
HTTP/1.1 202 Accepted
Content-Type: application/json

{"status":"started"}
```

**V3 — a second request while the first is still running returns 409.** Immediately
after V2, before the cycle finishes, repeat the same `curl` command. Expect:
```
HTTP/1.1 409 Conflict
```

**V4 — the cycle actually completes (the root-context bug, D4(b), did not
regress).** Wait 1-3 minutes after V2, then:
```bash
docker compose --project-directory . -f deploy/docker/compose.yaml logs poller --tail=50
```
Expect the same `LogCycle` line format the scheduler and `--once` already produce,
naming a completed cycle — NOT a line showing the cycle was cancelled/context-
deadline-exceeded within milliseconds of the `202` being written. Then:
```sql
SELECT run_id, triggered_by, started_at, finished_at, duration_seconds
FROM telemetry.poll_runs
WHERE triggered_by = 'api'
ORDER BY started_at DESC
LIMIT 1;
```
Expect one row, `triggered_by = 'api'`, with `finished_at - started_at` in the tens
of seconds to a few minutes (a real cycle), not near-zero (a killed goroutine).

**V5 — an unknown path 404s.** `curl -i -X POST https://<domain>/internal/rerun/wrongtoken`
→ expect `404`, not `409` or `202` — confirms `http.ServeMux` is doing the matching,
not a comparison that could be tricked.
