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

### Dependency direction & import cycles

Dependencies flow **one way only**: `gateway → domain modules → adapters`. A domain module may
depend on a sibling *below* it (`telemetry → account`), never on the gateway, and never on a
module that already depends on it. The live graph is in the root `README.md` §Architecture.

**Go enforces acyclicity for you.** An import cycle between packages is a *compile error*
(`import cycle not allowed`), not a lint warning — unlike Java/C#/TypeScript, where circular
imports usually work and rot the design quietly. You cannot ship one. Several rules above are
cycle-prevention in disguise: `tesla` is stateless about identity precisely so it never needs to
import `account`, which would close the loop `account → tesla → account`.

So the risk is never the cycle itself — it's **how it gets resolved**. Both of these silence the
compiler and destroy the boundary; neither is acceptable here:

- **The `shared`/`common` dump** — moving the contested types into a package everything imports.
- **Merging the packages** — Go only forbids cycles *between* packages, so folding two modules
  into one makes the error disappear along with the boundary.

**First ask where the fact belongs, not how to break the cycle.**

A cycle between two modules is usually a symptom, not the problem. If module `A` owns the source
rows, module `B` needs a value derived from them, and the derivation depends on `A`'s own internal
rules — compute it in `A`, store it in `A`, and let `B` read it. The cycle then does not exist to
break.

Reach for the consumer-side interface below only when the derivation genuinely needs data from
**both** modules, so it cannot live wholly inside either.

**The microservice test.** If these two modules became separate services, would the derivation
rule have to travel between them as shared knowledge? If yes, the derivation is in the wrong
module. This test is the point of the whole boundary: a module that can become a service owns its
data *and the facts derived from it*.

> *Precedent — MAG-32 / RM52.* Monthly effective pack capacity was first placed in `analytics`,
> which already imports `charging`. That forced `analytics` to encode **why** `ESTIMATED` entries
> and `DONE_CALCULATED` sessions are unusable inputs — namely that `charging` derived them from
> its own hardcoded capacity constant, so their implied capacity is that constant. A fact about
> `charging`'s internals would have lived in `analytics`, and a consumer-side interface would have
> been needed to close the loop. Moving the whole derivation into `charging` — which owns every
> input row — removed the cycle and the leak together, and needed no interface at all.

**The fix is a consumer-side interface.** Go has no `implements` keyword — a type satisfies an
interface implicitly, so the provider never has to know the interface exists. Declare the
interface in the package that *needs* it, and wire the concrete type in at `cmd/` startup. This
is the opposite of the Java/Spring habit of defining the interface next to its implementation:

```go
// In telemetry, if it ever needed data owned by analytics (which already imports telemetry).
// analytics satisfies this without importing telemetry — no import, no cycle.
type ChargeReader interface {
	LatestCharge(ctx context.Context, vehicleID uuid.UUID) (Charge, error)
}
```

*Why: the interface stays small because the consumer shapes it to what it actually uses, rather
than inheriting the provider's whole surface.*

The one sanctioned back-edge is an **external test package** (`package telemetry_test` rather
than `package telemetry`), which may import packages that import the package under test.

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
└── analytics/               # EXAMPLE domain module
    ├── service.go           # core derived-metrics logic/math on clean domain structs
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
- **The gateway is the only place that checks whether a vehicle belongs to the
  signed-in user.** A domain module below the gateway never re-checks this — it
  trusts that a vehicle id it receives was already proven owned. The proof is a
  value, not a boolean: `internal/vehicleref` turns "the account's own vehicle
  list" plus "one requested vehicle id" into a `Ref` that only exists when the
  check passed. A handler that skips the check has no `Ref` to pass to a port
  that requires one, so the mistake fails to compile instead of failing a
  runtime check nobody notices. `internal/gateway/handlers/handlers.go`'s
  `authorizeVehicle`, next to `resolveSelectedVehicle`, is where this check runs.
  `make vehicleref-guard` enforces the call sites: it fails if `vehicleref.Authorize`
  or `vehicleref.All` is called anywhere outside `internal/vehicleref`, `_test.go`
  files, and `authorizeVehicle` itself.

---

## 6. DTO Naming — the Anti-Corruption Rule

**Any struct that mirrors an external service's JSON shape ends in that service's name;
our own domain models never carry a vendor suffix.**

- Tesla's raw payloads → `VehicleDataTesla`, `ChargeStateTesla`, `DriveStateTesla`, … in
  `internal/tesla/`. These are the *only* structs that unmarshal Tesla JSON.
- Our clean domain models (owned by domain modules, e.g. `analytics.State`) carry **no**
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
- **`Reader`** — called by read consumers to read data. For every module except
  `telemetry` this is the only port the gateway depends on at request time.

The split is a **read-optimization design**, not just a separation of concerns: it
makes it impossible for a user-facing request to trigger a write path, and it lets
the `Reader` interface be shaped purely for dashboard query patterns (batch, latest,
aggregated) without being polluted by collection concerns.

Reference implementation: `internal/telemetry/` — `Collector.CollectAll` (write,
nightly) vs `Reader.LatestSnapshotsByVehicles` (read, per dashboard load).

#### Exception: the gateway may not depend on `telemetry` at all

**`internal/gateway/` must not import `internal/telemetry` — not even `telemetry.Reader`.**
This is stricter than the rule above, and it overrides it for this one module: the
telemetry port is *not* part of the gateway's allowed vocabulary, and `telemetry.*`
types (`telemetry.Snapshot`) must not appear in gateway code.

Enforced by **`make boundary-guard`** (wired into `make check`). The guard greps
`internal/gateway/**/*.go` for the `internal/telemetry` import path: it **fails** on a
non-test file and **warns** on a `_test.go` file. Escape hatch — a trailing
`// boundary:allow: <reason>` comment on the same line as the import.

**Status: RESOLVED — the guard passes clean** (`RM40`, ticket MAG-41). The gateway
names `internal/telemetry` nowhere: not in a production file, not in a test file, and
with **zero** `// boundary:allow:` escape hatches. `grep -rn "internal/telemetry"
internal/gateway/` returns nothing.

The guard was deliberately added *before* the migration, so the boundary was visible
and could not be widened silently while it was still red. It stayed red across several
changes; that was the design working, not a defect.

How it was resolved — the answer the rule itself demanded: the gateway reaches vehicle
telemetry through **another module's interface**, and the data crosses the boundary as
**that module's own type**.

- `RM38` moved the four telemetry latest-state call sites (dashboard, vehicle
  cards, nav header, charges suggestion) to `analytics.Reader.LatestMetricsForVehicles`,
  returning `analytics.VehicleStatus`.
- `RM40` moved the last one, `SnapshotsByVehicleBetween` (the history page's battery
  chart), to **`analytics.Reader.BatteryLevelByDay`**, returning `analytics.DayBattery`.

`internal/analytics` was the right owner because it already owns `vehicle_metrics`,
the table holding the battery figures, and already served the history page's other two
charts. Reading it there is not a workaround for the guard — it is the module the data
actually lives in. The rejected alternative was a gateway-local interface still backed
by `telemetry`: that satisfies the guard's letter while keeping the runtime dependency
the guard exists to prevent.

One consequence worth knowing: `vehicle_metrics` holds a row per day analytics has
recalculated, whereas `vehicle_snapshots` held every raw capture. A day with no metric
row renders as the history page's existing empty bar. Coverage tracks snapshots 1:1
apart from the recalculator's watermark lag, so the effect is bounded to a recent day
briefly showing empty. This was accepted deliberately rather than backfilled.

Keep the guard. It now protects a clean boundary instead of tracking a migration, and
a new `internal/telemetry` import in the gateway is a regression, not a known debt.

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

2. **`DISTINCT ON` for "latest per X"** — `LatestSnapshotsByVehicles` uses
   `DISTINCT ON (tesla_id) ... ORDER BY tesla_id, captured_at DESC` to get the latest
   snapshot per vehicle in one index scan. Convention: batch reads like this replace
   N+1 per-vehicle queries at the module interface boundary.

3. **A multi-tenant table keys on one of three columns — pick by what the row means.**

   | Rule | Applies when | Tables |
   |---|---|---|
   | Key on `tesla_id` | `tesla_id` is `NOT NULL` | `telemetry.vehicle_snapshots`, `analytics.vehicle_metrics`, `analytics.vehicle_metric_watermarks`, `analytics.charge_gaps` |
   | Key on `vin` | `tesla_id` is nullable — the row can exist before the vehicle is known | `telemetry.supercharger_history`, `charging.supercharger_sessions` |
   | Keep `account_id`, demoted to an attribute | the row records who acted, not what the car did | `charging.manual_charge_entries`, `telemetry.poll_attempts` |

   `make tenancy-guard` enforces this: it fails if a module's query file outside
   `internal/account` filters on `account_id` (escape hatch: `-- tenancy:allow: <reason>`, SQL's own comment syntax).

   Do not add an index just because the old table had one. A `UNIQUE (a, b)`
   constraint already builds a btree that serves equality on `a`, point lookups on
   `(a, b)`, range scans on `b` within one `a`, and `ORDER BY b DESC` pinned to one
   `a`. A separate `(a, b DESC)` index next to it repeats work the constraint
   already does. Justify every index against a query that exists today.

4. **Append-only inserts for history** — `vehicle_snapshots` and `poll_attempts`
   are `INSERT`-only (no UPDATE/DELETE). Writes are cheap; reads are indexed.
   Convention: historical event tables are append-only.

5. **`?start=&end=` date-filtered gateway reads, never `?days=N`** — every gateway HTTP
   endpoint that filters by date bounds the read on both ends with absolute
   `?start=YYYY-MM-DD&end=YYYY-MM-DD` params, rejecting a malformed or over-wide window with
   HTTP 400 before the read port is ever called. Full contract (parse helper, per-endpoint
   default/cap set by the source table's row density, 400-on-malformed, no-selector-on-400):
   [`internal/gateway/AGENTS.md`](../internal/gateway/AGENTS.md) §"HTTP date-filter
   convention". Convention: a new date-filtered endpoint follows that contract rather than
   inventing a `days` count.

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
