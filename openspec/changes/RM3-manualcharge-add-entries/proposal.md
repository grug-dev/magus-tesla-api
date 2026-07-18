## Why

The Tesla Fleet API's `GET /api/1/dx/charging/history` endpoint returns only **Supercharger and DC
fast-charging sessions billed by Tesla**. It cannot attribute home/work/third-party AC or DC
charging sessions to a specific vehicle in a multi-tenant platform — the session is keyed by
connector device ID, not by VIN or `tesla_id`. RM2 already collects Supercharger sessions; RM3
fills the gap with **user-asserted** manual charge entries: the user says which vehicle, which day,
how much energy was added, and what it cost.

This approach was chosen over the Tesla Wall Connector energy API on 2026-07-18 (see
`openspec/roadmaps/RM3-manual-charge-log.md` for the full rationale). Manual entry flips the
attribution problem: the **user knows** which car they plugged in.

> grill-me was run by the leader (2026-07-18). All design decisions are locked in the roadmap
> `openspec/roadmaps/RM3-manual-charge-log.md` — this proposal captures the agreed scope without
> reopening them.

## What Changes

**New isolated module `internal/manualcharge`** — a backend-only (no HTML, no Fleet API) domain
module that owns the `manual_charge_entries` table and exposes two public Go interfaces:

- **Writer** — Create, Update, Delete for user-asserted charge entries.
- **Reader** — List entries per vehicle or per account, ordered newest-first.

The module is fully isolated: it imports no other module's internals, calls no Tesla Fleet API,
needs no OAuth scope, and wakes no car. Frontend (htmx/Templ form + list) lives in Tier 2
(`RM3-gateway-add-manual-charge-ui`) as required by the boundary rule "no HTML outside
`internal/gateway/`."

**Artifacts in this change:**

- Goose migration: new `manual_charge_entries` table + two indexes.
- sqlc queries: `CreateEntry`, `UpdateEntry`, `DeleteEntry`, `ListEntriesByVehicle`,
  `ListEntriesByAccount`.
- Domain type `manualcharge.Entry` with derived value-receiver methods (`CostPerKWh`,
  `BatteryDelta`, `SessionDuration`).
- Public ports: `Writer` interface (Create/Update/Delete) and `Reader` interface (list methods).
- Service wiring `pgx` pool to the ports.
- Unit tests (derived-method math) + DATABASE_URL-gated integration tests (full round-trip CRUD
  and ordering).
- `internal/manualcharge/AGENTS.md`.

**Breaking:** No. This is a new isolated module; no existing module is modified.

**Modules affected:**

- `internal/manualcharge` — new module (this worker — owned).
- `sqlc.yaml` — new `sql:` entry for the `manualcharge` module (leader-integrated, outside the
  manualcharge sandbox; the worker describes what to add in design.md).
- No changes to `internal/tesla`, `internal/telemetry`, `internal/account`, or
  `internal/gateway`.

## Read Paths Affected

- **Per-vehicle charge history** — `Reader.ListEntriesByVehicle(ctx, accountID, teslaID, limit)`
  (new). Index: `(account_id, tesla_id, charged_on DESC)` serves this path with no sort step.
- **Account-wide charge history** — `Reader.ListEntriesByAccount(ctx, accountID, limit)` (new).
  Index: `(account_id, charged_on DESC)` serves this path with no sort step.

Both paths are read-heavy (dashboard-frequency) and both indexes are shaped to avoid a sort step,
in compliance with the project's read-heavy performance profile.

## Capabilities

### Added Capabilities

- `manual-charge-log`: A new capability allowing users to record home/work/third-party charging
  sessions that Tesla's Fleet API cannot capture. Entries are user-asserted: the user specifies
  the vehicle, the date, the energy added (kWh), the cost, and optional battery before/after,
  timing, charging type, and location. Entries are mutable (correctable) and multi-tenant-scoped
  (a user only ever sees and writes their own account's entries). Derived values (cost-per-kWh,
  battery delta, session duration) are computed on read as value-receiver methods — never stored.

## Deferred / Out of Scope

- **Frontend (htmx/Templ form + list)** — deferred to Tier 2 (`RM3-gateway-add-manual-charge-ui`).
  All HTML lives in `internal/gateway/` by rule; this tier is backend only.
- **Semi-automatic home-charge inference** — inferring home charge sessions from vehicle telemetry
  energy-added deltas. Recorded in `openspec/roadmaps/backlog.md`. The manual-entry API is the
  right foundation; automation is additive.
- **Currency conversion or display formatting** — out of scope; `currency` is a free-text field
  (`'COP'` default) for the UI/user to interpret. No FX conversion.
- **Aggregation / summary tables** — deferred until the gateway tier has a concrete dashboard
  aggregation requirement.
- **Retention / archiving** — entries are user data; no retention policy in this tier.

## Impact

**New files inside `internal/manualcharge`:**

- `internal/manualcharge/AGENTS.md` — module identity and doc-pack for sub-agents.
- `internal/manualcharge/manualcharge.go` — `Entry` domain type, derived methods, `Writer` and
  `Reader` interfaces, `NewWriter` / `NewReader` constructors.
- `internal/manualcharge/service.go` — service structs implementing `Writer` and `Reader`,
  mapping helpers (pgtype ↔ domain), and the unexported `store` seam.
- `internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql` — goose
  migration: table + two indexes + `-- +goose Down`.
- `internal/manualcharge/db/query.sql` — sqlc annotated queries.
- `internal/manualcharge/db/db.go` — sqlc boilerplate (generated; worker stubs it pre-generate).
- `internal/manualcharge/db/models.go` — sqlc-generated model (generated post `make sqlc`).
- `internal/manualcharge/db/query.sql.go` — sqlc-generated query implementations.
- `internal/manualcharge/manualcharge_test.go` — offline unit tests for derived methods.
- `internal/manualcharge/db_integration_test.go` — DATABASE_URL-gated integration tests.

**Repo-root (leader-integrated, outside the manualcharge sandbox):**

- `sqlc.yaml` — add a new `sql:` entry for `internal/manualcharge` (schema:
  `internal/manualcharge/db/migrations`, queries: `internal/manualcharge/db/query.sql`, package:
  `manualchargedb`, out: `internal/manualcharge/db`). The leader runs `make sqlc` after the
  migration and query files are authored.

**Dependencies:** no new Go dependencies — existing `pgx/pgxpool/sqlc/uuid/stdlib` stack.

**Migrations:** one new goose migration in `internal/manualcharge/db/migrations/`. Applied with
`make migrate-up`; never part of a build.
