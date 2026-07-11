> **Additive, non-breaking change** (tier 3 of `openspec/roadmaps/nightly-vehicle-telemetry.md`).
> New module `internal/telemetry/` owning `vehicle_snapshots` + `poll_attempts` (append-only,
> module-scoped `internal/telemetry/db`); a collection service consuming the `account` + `tesla`
> **ports only**; an in-app 03:30-local scheduler; a thin `cmd/poller`. Units stay API-native
> (miles); km only via `Km()`/`Kmh()` domain companions (`ai/go-conventions.md` non-negotiable).
> No cross-module DB access; no FK from telemetry tables into account tables (D2). `pgtype` →
> domain at the boundary. No Tesla API call fires from `go test`.
>
> **Dependencies / parallelism:**
> - 1 (migration + schema) has no dependencies.
> - 2 (sqlc/Makefile wiring) — repo-root, **leader-integrated**; depends on 1 (needs the migration
>   dir + DDL). Blocks 3.
> - 3 (sqlc queries + regen) depends on 2.
> - 4 (domain types + Km/Kmh companions) has no dependencies — disjoint file (`telemetry.go`); MAY
>   run in parallel with 1–3.
> - 5 (wake-with-timeout orchestration) depends on 4 (uses the domain/config types).
> - 6 (collection service) depends on 3, 4, 5.
> - 7 (scheduler) depends on 6 (drives the `Collector`).
> - 8 (config env) — `internal/config`, **leader-integrated**; independent of 1–7 (disjoint file);
>   blocks 9.
> - 9 (`cmd/poller` wiring) depends on 6, 7, 8.
> - 10 (offline collector/wake/schedule tests) depend on 5, 6, 7.
> - 11 (`DATABASE_URL`-gated store tests) depend on 3.
> - 12 (module AGENTS.md) has no dependencies — MAY run any time (created with this change).
> - 13 (verification) depends on 1–12.
>
> **Leader-integrated / cross-module tasks (outside the `internal/telemetry` sandbox):** 2
> (`sqlc.yaml` + `Makefile`) and 8 (`internal/config`). The telemetry worker must NOT edit these;
> the leader wires them and reconciles.

## 1. Migration + schema (`internal/telemetry/db/migrations/`) — no dependencies

- [x] 1.1 Add goose migration `internal/telemetry/db/migrations/<timestamp>_init_telemetry.sql`
      creating `vehicle_snapshots` per design D1: `id UUID PK DEFAULT gen_random_uuid()`,
      `account_id UUID NOT NULL`, `tesla_id BIGINT NOT NULL`, `captured_at TIMESTAMPTZ NOT NULL`,
      `raw_data JSONB NOT NULL`, and the typed columns — `battery_level INTEGER NOT NULL`,
      `battery_range DOUBLE PRECISION NOT NULL`, `charging_state TEXT NOT NULL`,
      `charge_limit_soc INTEGER NOT NULL`, `odometer DOUBLE PRECISION NOT NULL`,
      `inside_temp DOUBLE PRECISION NOT NULL`, `outside_temp DOUBLE PRECISION NOT NULL`,
      `locked BOOLEAN NOT NULL`, `sentry_mode BOOLEAN` (**nullable** — preserve absent≠false, D1),
      `car_version TEXT NOT NULL`, `latitude DOUBLE PRECISION NOT NULL`,
      `longitude DOUBLE PRECISION NOT NULL`. **No FK** on `account_id`/`tesla_id` (D2 — boundary,
      not constraint). Add index `(account_id, tesla_id, captured_at)`. Include a header comment
      that the table is append-only and telemetry-owned.
- [x] 1.2 In the SAME migration, create `poll_attempts` per D5: `id UUID PK`,
      `account_id UUID NOT NULL`, `tesla_id BIGINT NOT NULL`, `attempted_at TIMESTAMPTZ NOT NULL`,
      `outcome TEXT NOT NULL` (`success`|`failure`), `reason TEXT NOT NULL`
      (`ok`|`asleep-timeout`|`unauthorized`|`api-error`). No FK (D2). Add a `-- +goose Down` that
      drops both tables. Document that one row is written per (vehicle, run) as
      availability/sleep-behavior data.

## 2. sqlc entry + Makefile migration wiring (`sqlc.yaml`, `Makefile`) — LEADER-INTEGRATED, depends on 1

- [x] 2.1 Add a second `sql:` entry to `sqlc.yaml` generating package `telemetrydb` into
      `internal/telemetry/db`, `schema: internal/telemetry/db/migrations`,
      `queries: internal/telemetry/db/query.sql`, keeping the project-wide `uuid → google/uuid.UUID`
      override and the same gen options as the account entry (one `sql:` per module,
      `ai/go-conventions.md` §persistence). Do NOT merge into the account entry.
- [x] 2.2 Extend the `Makefile` migration wiring so `make db-setup` / `make migrate-up` also apply
      `internal/telemetry/db/migrations` (goose runs one `-dir` per module — add a second run;
      today `MIGRATIONS_DIR` is account-only). Keep `DATABASE_URL` as the single DSN source. This
      is a repo-root edit outside the telemetry sandbox — leader-integrated.

## 3. sqlc queries + regeneration (`internal/telemetry/db/`) — depends on 2

- [x] 3.1 Add `internal/telemetry/db/query.sql` with `InsertVehicleSnapshot :exec` (all snapshot
      columns; `sentry_mode` bound as a nullable boolean) and `InsertPollAttempt :exec`
      (account_id, tesla_id, attempted_at, outcome, reason). Add read helpers used only by the DB
      tests (e.g. `ListSnapshotsByVehicle :many`, `ListPollAttemptsByVehicle :many`) so task 11 can
      read back without touching internals. Add header comment: sqlc generates `telemetrydb`; no
      other module imports it.
- [x] 3.2 Regenerate `telemetrydb` with `make sqlc` (or `sqlc generate`). Confirm only
      `internal/telemetry/db` changed and no cross-module package imports `telemetrydb`.

## 4. Domain types + Km/Kmh companions (`internal/telemetry/telemetry.go`) — no dependencies, parallel-ok

- [x] 4.1 Add the module's domain types (no vendor suffix, `ai/architecture.md` §6): a `Snapshot`
      carrying account id, tesla id, captured-at, the extracted typed fields, and the raw payload;
      with `SentryMode *bool` (nil = not reported, D1). Add companion value-receiver methods
      `BatteryRangeKm()` and `OdometerKm()` (miles→km via a package-level `milesToKm = 1.609344`
      constant — never inline the factor; never a km field), matching the tier-1 pattern. Add an
      `Attempt` / outcome+reason type (or named string consts) for `poll_attempts`. Define the
      `Config` struct (wake timeout, optional clock) and the `CycleReport` return type.
- [x] 4.2 Declare the public port `Collector` interface with
      `CollectAll(ctx context.Context) (CycleReport, error)` and a doc comment stating per-vehicle
      isolation and that an error is returned only for a whole-cycle failure (D9). Add a
      compile-time `var _ Collector = (*service)(nil)` once the service exists (task 6).

## 5. Wake-with-timeout orchestration (`internal/telemetry/wake.go`) — depends on 4

- [x] 5.1 Implement a bounded wake-then-poll helper (D4): given `tesla.VehicleService`,
      `tesla.Credentials`, a `teslaID`, and a timeout, call `WakeUp`, then poll `ListVehicles` for
      that vehicle's `State == "online"` on a fixed interval under a `context.WithTimeout` deadline.
      Return online-reached vs timed-out distinctly (so the caller maps the reason). Honor ctx
      cancellation. Keep it pure of storage — it only orchestrates the tesla port.

## 6. Collection service (`internal/telemetry/service.go`) — depends on 3, 4, 5

- [x] 6.1 Implement `NewService(pool, acct account.Service, tsla tesla.VehicleService, cfg Config)
      Collector` and `CollectAll`: call `acct.AllRegisteredVehicles(ctx)`, group by `AccountID`
      (per-account batching, D3); per account resolve `acct.AccessTokenFor(accountID)` once (on
      `account.ErrNoTeslaConnection`, record every vehicle `failure`/`unauthorized` and skip — no
      Tesla calls), wrap in `tesla.Credentials`, call `ListVehicles` once, then per vehicle
      wake-if-needed (task 5), `VehicleData`, map the raw + typed DTO to a `Snapshot`, and
      `InsertVehicleSnapshot`. `pgtype` never leaks (map at the boundary; `sentry_mode` nil↔NULL).
- [x] 6.2 Implement per-vehicle isolation + reason mapping + one bounded retry (D5): each vehicle's
      collection is error-contained (a failure never aborts the account or cycle); map
      `tesla.ErrUnauthorized`/`account.ErrNoTeslaConnection` → `unauthorized` (no retry),
      wake-deadline → `asleep-timeout`, other tesla/decode/store errors → `api-error` (retried once
      after a short backoff). Write exactly one `InsertPollAttempt` per vehicle per cycle
      (`ok` on success). Accumulate a `CycleReport`. Return an error only if the whole-cycle
      enumeration fails.

## 7. Scheduler (`internal/telemetry/scheduler.go`) — depends on 6

- [x] 7.1 Implement an in-app daily-at-`hour:minute`-in-`timezone` daemon (default 03:30 `Local`,
      D7) driving a `Collector`. Factor the next-run-time computation into a pure function
      (`nextRun(now, hour, minute, loc) time.Time`) so it is unit-testable without waiting. `Run(ctx)`
      sleeps until the next run via a timer, runs one `CollectAll`, reschedules for the next day, and
      returns when `ctx` is cancelled (graceful shutdown; no new cycle started after shutdown begins).

## 8. Config env (`internal/config/config.go`) — LEADER-INTEGRATED, independent of 1–7

- [x] 8.1 Add `POLLER_SCHEDULE_HOUR` (default 3), `POLLER_SCHEDULE_MINUTE` (default 30),
      `POLLER_TIMEZONE` (default `Local`), and `POLLER_WAKE_TIMEOUT` (default ~90s) to
      `internal/config` (typed fields + defaults), so `os.Getenv` stays confined to config
      (`ai/go-conventions.md`). Outside the telemetry sandbox — leader-integrated.

## 9. cmd/poller wiring (`cmd/poller/main.go`) — depends on 6, 7, 8

- [x] 9.1 Add a thin `cmd/poller/main.go` mirroring `cmd/web`: `config.Load()`, require
      `DATABASE_URL`, `signal.NotifyContext(SIGINT, SIGTERM)`, `pgxpool.New(ctx, dsn)` (defer
      Close), `account.NewService(pool, clientID, clientSecret)`, `tesla.NewClient()`,
      `telemetry.NewService(pool, acct, tesla, cfg)`, build the scheduler, `scheduler.Run(ctx)`,
      graceful shutdown. Zero business logic (`ai/go-conventions.md` — `cmd/` stays thin).

## 10. Offline collector / wake / schedule tests (`internal/telemetry/*_test.go`) — depend on 5, 6, 7

- [x] 10.1 Unit-test `CollectAll` with **fake** `account.Service` and `tesla.VehicleService`
      implementations (no DB, no network): assert per-vehicle isolation (one vehicle failing → the
      rest still captured), exactly one attempt per vehicle, reason mapping
      (`ok`/`unauthorized`/`asleep-timeout`/`api-error`), the one-retry-on-transient behavior, the
      wake-then-fetch happy path, and multi-account/multi-vehicle coverage. Use a fake store or a
      port seam so no `DATABASE_URL` is needed. NO Tesla API call fires.
- [x] 10.2 Unit-test the pure `nextRun` schedule-time math (before/after the target time, timezone,
      day rollover) and the wake helper's online-vs-timeout outcomes with a fake tesla port and a
      short timeout / fake clock.

## 11. DATABASE_URL-gated store tests (`internal/telemetry/db_integration_test.go`) — depend on 3

- [x] 11.1 Add `DATABASE_URL`-gated integration coverage that self-skips when unset (the account
      module's pattern): insert a `vehicle_snapshot` (raw JSONB + all typed columns; a `nil`
      `sentry_mode` round-trips as SQL NULL and a `*true`/`*false` round-trips faithfully) and a
      `poll_attempt`, then read them back via the task-3 read helpers and assert the values.
      Append-only: a second insert for the same vehicle adds a row, leaves the first intact.

## 12. Module AGENTS.md (`internal/telemetry/AGENTS.md`) — no dependencies

- [x] 12.1 Create `internal/telemetry/AGENTS.md`: first line after the title `Agent-Name: telemetry`;
      a `## Doc-Pack (module)` section (additive to base — point to `docs/deployment.md` for the
      `DATABASE_URL` / migration runbook); the module's responsibility; its public interface (the
      `Collector` port + domain types); allowed imports (`account` + `tesla` **ports**, pgx/pgxpool
      — NOT their `db` packages, NOT the gateway); data ownership (`vehicle_snapshots` +
      `poll_attempts`); DTO/units conventions (miles stored, `Km()`/`Kmh()` companions; no vendor
      suffix); testing notes (offline fakes for the ports, `DATABASE_URL`-gated store tests).

## 13. Verification — depends on 1–12

- [x] 13.1 `go build ./...` and `go vet ./...` pass; `telemetrydb` generated; `cmd/poller` builds.
- [x] 13.2 `go test ./...` green and fast; telemetry store tests self-skip without `DATABASE_URL`
      (and pass with it set); NO Tesla API call fires from the test run.
- [x] 13.3 `openspec validate telemetry-add-nightly-snapshots --strict` passes and every tasks.md
      checkbox reflects real completion.
- [x] 13.4 Boundary check: `internal/telemetry` imports only the `account` + `tesla` public
      packages (not `accountdb`/`internal/account/db`, not `internal/tesla` internals, not the
      gateway); no HTML anywhere; no km field on any struct or column.
