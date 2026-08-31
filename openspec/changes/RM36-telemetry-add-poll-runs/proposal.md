Source: MAG-35 — https://linear.app/magus-monitor/issue/MAG-35/poll-attemps-tracking
Roadmap: openspec/roadmaps/RM36-poll-run-tracking.md
Tier: 1 of 2 (`RM36-app-record-poll-run`, module `app`, depends on this tier — it
calls the `RunWriter` port this tier creates)

## Why

MAG-35 asks whether the poller's nightly log counters are per-account or in total,
and today the honest answer is "neither, consistently" — `attempted`/`succeeded` are
vehicle-grain, `charging_failures` is account-grain, and account-level attempt/success
counts do not exist as a number anywhere, only as something you could reconstruct by
reading the vehicle rows. Worse, the log line is the *only* place any of this lives:
nothing persists it, so a Tesla-API-cost question ("how many Fleet API requests did
last night's run spend?") cannot be answered from the database at all — the ticket's
underlying ask is a cost figure, and the ingredient for it (request count) is
recorded nowhere.

`poll_attempts` already answers "what happened to each vehicle" — grain one row per
(vehicle, run), unchanged by this tier. What's missing is the run itself: one place
that says how long the whole invocation took, how many accounts and vehicles it
touched and with what outcomes, and how many Tesla requests it spent. Migration
`20260823000002` explicitly decided against a run-level table ("a caller wanting
run-level facts computes them with `GROUP BY run_id`") — that holds for facts that
live in `poll_attempts` already, but duration, the API-call count and the account
breakdown never did and never can: a run whose vehicle enumeration fails writes
**zero** `poll_attempts` rows, so `GROUP BY run_id` over an empty set answers nothing
and a failed run leaves no trace at all. This tier reopens that decision (roadmap D1)
for exactly the facts it doesn't cover.

## What Changes

- **New table `poll_runs`**, one row per `run_id`, owned by `internal/telemetry`
  (roadmap D1). `poll_attempts`' grain is untouched.
- **New `telemetry.RunWriter` port** (`RecordRun`) — the write seam `internal/app`
  will call in tier 2, shaped like the existing `charging.SessionWriter` /
  `analytics.GapWriter` precedent: a single method, no pool crossing the module
  boundary.
- **New `telemetry.PollRun` domain type** — the row `RecordRun` persists.
- **New `CycleReport` fields**: `TeslaAPICalls`, `AccountsAttempted`,
  `AccountsSucceeded`, `AccountsFailed` (all computed by `CollectAll` itself, since
  every one of these facts is a step-1/telemetry-only concern), plus `Duration`
  (populated by the *caller* after `CollectAll` returns — see design.md D6 for why
  telemetry cannot measure it itself and why this is the seam that lets tier 2 avoid
  editing `internal/telemetry` at all).
- **A counting decorator around `tesla.VehicleService`**, internal to `internal/telemetry`
  (roadmap D2): wraps the service's own `tesla.VehicleService` field for the
  duration of one `CollectAll` call only, counting every call regardless of
  success/failure (a rejected request still spends a request against Tesla).
- **Account-level counters** incremented at the two whole-account short-circuits
  `collectAccount` already implements (roadmap D4) — no new failure semantics.
- **`telemetry.LogCycle`'s printed line is disambiguated** (roadmap D7): the vehicle
  counters print as `vehicles_attempted`/`vehicles_succeeded`, and the line gains the
  new account counts, the API-call count, and the duration. The underlying
  `CycleReport.Attempted`/`.Succeeded` **Go field names are not renamed** — see
  design.md D8 for why (a rename would break a test file in `internal/app`, outside
  this tier's — and tier 2's — module sandbox).
- **`internal/telemetry/AGENTS.md`** — Data Ownership section gains `poll_runs`.
- **Root `README.md`** — the "Database tables by module" table gains a `poll_runs` row.

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key. `poll_attempts`'
schema and read ports are untouched.

**No — internally, to existing callers.** Every change to `CycleReport` is additive
(new fields only; Go struct literals with named fields are unaffected by additive
fields). `telemetry.Reader`, `telemetry.Collector`, and `telemetry.SuperchargerReader`
are unchanged. `LogCycle`'s **signature** does not change — only the text it prints —
so its two out-of-module call sites (`internal/app/scheduler.go`,
`cmd/poller/main.go`) need no edit in this tier.

**Yes — a new table and a new port**, both purely additive to the module's public
surface. Nothing existing is removed.

## Modules Affected

- **`internal/telemetry/`** — the owning module for this entire tier. Gains the
  `poll_runs` table + migration, the `RunWriter` port, the `PollRun` type, the
  counting decorator, the new `CycleReport` fields, and the disambiguated log line.
- **`internal/app/`** — **not touched by this tier.** Tier 2
  (`RM36-app-record-poll-run`) is the only consumer of `RunWriter`; it measures the
  full three-step run and calls `RecordRun`. This tier's `RunWriter` port and
  `PollRun` type exist so that tier can be written without either tier reaching into
  the other's files.
- **`internal/tesla/`** — **not touched.** The counting decorator lives entirely
  inside `internal/telemetry`, wrapping the `tesla.VehicleService` port from the
  outside (roadmap D2 — the two rejected alternatives, a `ctx`-scoped counter inside
  `tesla.do()` and a counting `http.RoundTripper`, both required editing `tesla`
  itself).
- **`internal/gateway/`, `cmd/`** — not touched. Nothing reads `poll_runs` in this
  roadmap (see "Out of scope" below); no composition-root wiring changes here
  (tier 2 wires `RunWriter` into `cmd/poller`).

## Database Changes

**One migration**, under `internal/telemetry/db/migrations/`, creating `poll_runs`.
Full DDL, the rationale for the `run_id`-as-primary-key design (no surrogate `id`),
why no foreign key correlates it to `poll_attempts`, why `finished_at`/
`duration_seconds` are `NOT NULL`, and the index plan (no index beyond the primary
key, justified against this table's write volume and the only stated future read):
design.md "Database Changes". **This change trips the `database` design gate and
must be confirmed by the owner before Apply.**

## Read Paths Affected

**None.** Nothing in this roadmap reads `poll_runs` — see "Out of scope" below. The
write path this tier adds (`RunWriter.RecordRun`, one `INSERT` per poller invocation,
called from the nightly batch or `cmd/poller --once`) is off the hot read path by
construction, matching the read-heavy Performance-Profile's stated latitude for
midnight-batch writes.

## Capabilities

### Added Capabilities

- **Run-level poll summary storage** — a durable, queryable record of every poller
  invocation, including invocations that fail before attempting a single vehicle.
  See `specs/telemetry/spec.md`.
- **Tesla API call counting** — every Fleet API request `internal/telemetry` makes
  during one cycle is counted and surfaced on `CycleReport`. See
  `specs/telemetry/spec.md`.
- **Account-level attempt/outcome counts** — `CycleReport` and `poll_runs` both gain
  `accounts_attempted`/`accounts_succeeded`/`accounts_failed`, computed from the two
  whole-account short-circuits `collectAccount` already implements. See
  `specs/telemetry/spec.md`.

### Modified Capabilities

- **Nightly cycle log summary** — the printed line disambiguates vehicle-grain from
  account-grain counts and adds the API-call count and duration. See
  `specs/telemetry/spec.md` `## MODIFIED Requirements`.

### Out of scope (explicitly deferred)

- **A gateway read surface for `poll_runs`.** Recorded in
  `openspec/roadmaps/backlog.md` per the roadmap's own "Future work" section —
  nothing reads this table in this roadmap, so no `Reader`-style port, no index
  beyond the primary key, and no HTTP route are added here.
- **Per-vehicle poll duration** (deferred from roadmap D3). `poll_attempts` gains no
  duration column in this tier.
- **`internal/app` wiring** — tier 2's job entirely: measuring the full run,
  constructing `PollRun`, calling `RecordRun`, and updating `NewProcessor`'s
  signature + `cmd/poller`'s composition root.
- **Renaming `CycleReport.Attempted`/`.Succeeded`.** Only the printed log text is
  disambiguated in this tier (design.md D8) — the Go field names are unchanged to
  avoid a breaking edit outside this tier's module sandbox.

## Testing

Per the Test-Execution-Policy: this tier writes tests but does not run the suite.
Offline unit tests (the counting decorator, the account-counter accounting, the
whole-cycle-failure "still leaves a trace" case) are authored against design.md's
Test Contract fixtures, written *before* the implementation that must satisfy them.
The `RunWriter.RecordRun` write path is `DATABASE_URL`-gated and is authored **last**
(it cannot compile before the migration and the regenerated `telemetrydb` types
exist) — its expected row values are likewise fixed in design.md's Test Contract up
front.

Exact commands for the owner to run, once implementation lands:
`go build ./...`, `go vet ./...`, `gofmt -l .`, `make sqlc`, and — owner-only —
`go test ./internal/telemetry/...` (or `make test` / `make test-with-db` for the
DB-backed fixtures).
