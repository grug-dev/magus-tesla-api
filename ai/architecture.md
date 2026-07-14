# Architecture — magus-tesla-api

**The "Agentic Modular Monolith."** This file is the structural source of truth: what the
modules are, the boundaries between them, and where every kind of file belongs. Any AI
assistant or human **must read and follow this before adding or moving code.** Referenced
from `CLAUDE.md`. The AI-development *workflow* that maintains this structure lives in
[`agentic-workflow.md`](./agentic-workflow.md); Go code style in
[`go-conventions.md`](./go-conventions.md); the web layer in
[`htmx-conventions.md`](./htmx-conventions.md) and
[`htmx-go-integration.md`](./htmx-go-integration.md).

> **Heads-up on the current repo:** the existing `internal/config`, `internal/auth`, and
> `internal/server` packages are a **smoke test** to learn the Tesla API — **not** the target
> design; their responsibilities move into the `account` module. 

---

## 1. Core Architectural Pillars

1. **Modular Monolith.** The codebase is split into isolated domain modules under
   `internal/`. Each module owns its own data and business logic and exposes a **strict
   public Go interface** (its "port"). Modules never reach into each other's internals.
2. **UI Gateway.** A single presentation layer (`internal/gateway/`, served by `cmd/web`) is
   the *only* place HTML exists. It receives htmx requests, calls domain module
   **interfaces** (in-process) for data, pipes that data into Templ templates, and streams
   back **HTML fragments**. Its htmx fragment routes are prefixed `/ui` (plus the page routes
   and an ops `/healthz`). The gateway is built on **Gin** — the project's chosen web router.
   *(Note: the separate `internal/server` Gin helper, used only by `cmd/setup` for the
   one-time OAuth redirect, remains a smoke-test helper and is unrelated to the gateway.)*
3. **Agentic Modular Monolith (dev workflow).** The same module boundaries that isolate
   *code* also isolate the *dev-time AI coding agents* that maintain each module. Fully
   described in [`agentic-workflow.md`](./agentic-workflow.md). **No runtime agent code
   exists** (there is no `agent.go`).
4. **Tesla as an Adapter.** `internal/tesla/` is an **anti-corruption layer** that wraps
   the external Tesla Fleet API. It is **stateless about identity** — callers hand it the
   credentials to use — and it returns vendor-shaped DTOs (see the `...Tesla` suffix rule
   below).

---

## 2. Strict Boundary Rules

Enforce these invariants on every change:

- **No HTML inside domain modules.** Never import `html/template`, `templ`, or return raw
  HTML strings anywhere outside `internal/gateway/`. Domain modules return pure Go
  structs. *Why: keeps rendering swappable and domains testable without a browser.*
- **No cross-module database leaks.** A module may not read another module's tables,
  repositories, or private structs. Cross-module data flows **only** through public
  interfaces. *Why: this is what makes a module boundary real rather than cosmetic.*
- **The gateway is an orchestrator, not a database client.** `internal/gateway/` has zero
  DB access. It obtains data solely by calling module interfaces. *Why: the gateway
  translates between HTTP/htmx and the domain — it must not own domain state.*
- **Agents are sandboxed to their module** (dev-time; see
  [`agentic-workflow.md`](./agentic-workflow.md)).

---

## 3. Two HTTP Surfaces

| Surface | Prefix | Returns | Who consumes it | Status |
|---|---|---|---|---|
| **UI Gateway** | `/ui` | HTML fragments | the browser, via htmx | built as the web layer lands |
| **JSON API** | `/api/{version}` (e.g. `/api/v1`) | JSON | *external* clients (mobile, 3rd-party, other services) | **convention only — built per-module on demand** |

The **Go interface is the mandatory contract** every module exposes; the gateway and
sibling modules use it via in-process calls (no HTTP, no serialization). The HTTP JSON API
is a *secondary adapter* layered on the same service — documented now, implemented for a
module only when a real external consumer exists. *Why: don't build and version an HTTP+JSON
surface for consumers that don't exist yet.*

---

## 4. Target Directory Blueprint

Greenfield target layout (create a module's files when that module is actually built —
do not scaffold empty folders):

```text
internal/
├── gateway/                 # THE UI GATEWAY — the only place HTML lives
│   ├── handlers/            # receive htmx (/ui/...), call module interfaces, render Templ
│   └── templates/           # .templ files: layouts, pages, and htmx fragments
│
│   # --- DOMAIN MODULES (strict boundaries, interface-first) ---
├── account/                 # MULTI-TENANT identity
│   ├── service.go           # users + per-user Tesla tokens (DB), OAuth connect flow
│   └── account.go           # public interface, e.g. TokensFor(ctx, userID) (Credentials, error)
│
├── tesla/                   # ADAPTER / anti-corruption layer (stateless about identity)
│   ├── client.go            # calls the Tesla Fleet API using credentials passed IN
│   └── types.go             # vendor-shaped DTOs — every struct suffixed ...Tesla
│
└── battery/                 # EXAMPLE domain module
    ├── service.go           # core battery logic/math on clean domain structs
    └── api.go               # public Go interface (+ optional /api/v1 JSON, on demand)
```

There is **no `agent.go`** in any module — agents are dev-time config, not runtime code.

---

## 5. Multi-Tenancy

The system serves **many users**, each with their own Tesla account. Rules:

- **`internal/account/` owns identity and credentials** — the app's users, their persisted
  per-user Tesla tokens (a DB table), and the OAuth *connect* flow that obtains those
  tokens. It exposes something like `TokensFor(ctx, userID) (Credentials, error)`.
- **`internal/tesla/` is stateless about identity.** Every method takes the credentials to
  use as an argument (e.g. `GetVehicleData(ctx, creds)`); it never reads global config or
  another module's token table. *Why: an adapter that fetched its own credentials would
  either leak across a boundary or hardcode a single user.*
- **Standard request flow:**
  `gateway → account.TokensFor(userID) → tesla.GetVehicleData(ctx, creds) → domain module → gateway renders fragment`.
  No module ever touches another module's database; everything crosses via interfaces.

---

## 6. DTO Naming — the Anti-Corruption Rule

**Any struct that mirrors an external service's JSON shape ends in that service's name;
our own domain models never carry a vendor suffix.**

- Tesla's raw payloads → `VehicleDataTesla`, `ChargeStateTesla`, `DriveStateTesla`, … in
  `internal/tesla/`. These are the *only* structs that unmarshal Tesla JSON.
- Our clean domain models (owned by domain modules, e.g. `battery.State`) carry **no**
  suffix. The absence of a suffix means "this is our model, safe to build logic on."
- The adapter's job includes **mapping** `...Tesla` → clean domain structs before data
  flows into domain logic.
- The rule generalizes: a future weather adapter would use `...Weather`, a charging-network
  adapter `...Ocpp`, etc. — one consistent, visible marker per external source.

*Why: you should be able to tell at a glance whether a value is vendor-shaped (fragile,
external) or a domain model (yours, stable). The suffix makes an accidental leak obvious.*

---

## 7. Read-Heavy Workload Profile

This system has an **asymmetric workload**: ~99% of database operations are reads
(user opens dashboard, HTMX fragment refresh, API consumer fetches metrics) and ~1%
are writes (the nightly telemetry batch at 03:30 that appends one snapshot per
vehicle across all accounts). Every architectural and schema decision must bias
toward **read performance**. Write optimization is secondary and happens off-hours.

### The Reader/Collector port split

Data-owning modules (e.g. `internal/telemetry/`) expose **two distinct ports**:

- **`Collector`** — called by the nightly batch (`cmd/scheduler` or equivalent) to
  write data. The gateway never calls this.
- **`Reader`** — called by the gateway and other read consumers to read data. This
  is the only port the gateway depends on at request time.

The split is a **read-optimization design**, not just a separation of concerns: it
makes it impossible for a user-facing request to trigger a write path, and it lets
the `Reader` interface be shaped purely for dashboard query patterns (batch, latest,
aggregated) without being polluted by collection concerns.

Reference implementation: `internal/telemetry/` — `Collector.CollectAll` (write,
nightly) vs `Reader.LatestSnapshotsByAccount` (read, per dashboard load).

### Pre-computed summaries

Dashboards read **pre-aggregated data**, not raw append-only tables on every request.
The nightly batch is the right time to compute daily/weekly/monthly summaries and
write them to summary tables (or materialized views). This keeps dashboard reads
cheap (one indexed `SELECT` from a summary table) instead of expensive (scanning
months of raw snapshots and aggregating on the fly).

- Summary tables live in the owning module's `db/` package, written by the
  `Collector` port, read by the `Reader` port.
- They are an **optimization**, not a replacement for raw history — the immutable
  append-only tables remain the source of truth.

### Concrete patterns already in use (now conventions)

1. **Raw `JSONB` is mandatory insurance for external API ingestion — then typed
   columns for hot reads.** Any table persisting an external API response (Tesla
   Fleet API or any third-party) MUST store the lossless `raw_data JSONB NOT NULL`
   payload. This is a schema-drift hedge: if the API renames or reshapes a field,
   only the extraction code (the `...Tesla` DTO JSON tags) changes — the table
   schema and all historical rows stay valid. If you later want a field you weren't
   extracting, you backfill from `raw_data` with a one-time SQL `UPDATE` — no
   re-calling the API (paid, rate-limited, wakes the car), no lost history.
   `vehicle_snapshots` stores the full `vehicle_data` payload in `raw_data JSONB`
   *plus* extracted typed columns (battery_level, odometer, etc.). Dashboards read
   the typed columns (indexed, cheap); the raw JSONB is write-once-read-never-
   unless-backfilling. Convention: never force a dashboard to extract from JSONB on
   the hot path. See [`go-conventions.md`](./go-conventions.md) §Persistence.

2. **`DISTINCT ON` for "latest per X"** — `LatestSnapshotsByAccount` uses
   `DISTINCT ON (tesla_id) ... ORDER BY tesla_id, captured_at DESC` to get the latest
   snapshot per vehicle in one index scan. Convention: batch reads like this replace
   N+1 per-vehicle queries at the module interface boundary.

3. **`account_id` as leading index column** — every multi-tenant table indexes
   `account_id` first, because every dashboard read scopes by account. The
   `(account_id, tesla_id, captured_at)` index in telemetry covers both the WHERE
   filter and the ORDER BY in a single range scan. Convention: no multi-tenant table
   is created without an `account_id`-leading index.

4. **Append-only inserts for history** — `vehicle_snapshots` and `poll_attempts`
   are `INSERT`-only (no UPDATE/DELETE). Writes are cheap; reads are indexed.
   Convention: historical event tables are append-only.

### What this means for any new feature

Before adding a new query, table, or module interface, ask:

- **Is this on the read path?** Then it must be fast — indexed, batched, and shaped
  for the dashboard's access pattern.
- **Is this on the write path?** Then it runs during the nightly batch and has
  latitude to be slower (the user isn't waiting).
- **Could a summary table replace a runtime aggregation?** If yes, compute it in the
  nightly batch and read cheap during the day.

The database schema, module interfaces, and API surface should all reflect this
asymmetry: optimized reads at the edge, deferred writes at the center.
