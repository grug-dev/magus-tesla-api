# Architecture — magus-tesla-api

**The "Agentic Modular Monolith."** This file is the structural source of truth: what the
modules are, the boundaries between them, and where every kind of file belongs. Any AI
assistant or human **must read and follow this before adding or moving code.** Referenced
from `CLAUDE.md`. The AI-development *workflow* that maintains this structure lives in
[`agentic-workflow.md`](./agentic-workflow.md); Go code style in
[`go-conventions.md`](./go-conventions.md); the web layer in
[`htmx-conventions.md`](./htmx-conventions.md) and
[`htmx-go-integration.md`](./htmx-go-integration.md).

> **Heads-up on the current repo:** the existing `internal/config`, `internal/auth`,
> `internal/server`, and `internal/vehicle` packages were a **smoke test** to learn the
> Tesla API — they are **not** the target design and will be replaced by the module
> layout below. Treat this document, not the current code, as the intended architecture.

---

## 1. Core Architectural Pillars

1. **Modular Monolith.** The codebase is split into isolated domain modules under
   `internal/`. Each module owns its own data and business logic and exposes a **strict
   public Go interface** (its "port"). Modules never reach into each other's internals.
2. **UI Gateway.** A single presentation layer (`internal/gateway/`) is the *only* place
   HTML exists. It receives htmx requests, calls domain module **interfaces** (in-process)
   for data, pipes that data into Templ templates, and streams back **HTML fragments**.
   Its HTTP routes are prefixed `/ui`.
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
