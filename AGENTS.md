# AGENTS.md

# Tesla Analytics Platform

## Mission

The objective of this project is to build a **multi-tenant** Tesla analytics platform that provides long-term insights into vehicle usage, battery health, charging habits, driving efficiency, operating costs, and overall vehicle performance — for **many users, each connecting their own Tesla account and vehicles**.

This project is **not** intended to simply display Tesla API responses. Instead, it transforms raw Tesla telemetry into meaningful historical metrics, trends, forecasts, and dashboards that help **each user** better understand **their vehicles** over months and years.

The platform should continuously evolve as Tesla exposes additional APIs or as new analytical ideas emerge.

## Vision

See VISION.md for the long-term vision of this project.

## Tenancy Model

The platform serves **multiple users**. Every user connects their own Tesla account via OAuth; their access and refresh tokens are stored **per user in a database**, owned by the `internal/account/` module. All data collection, metrics, storage, and dashboards are **scoped to a user and their vehicles** — one user's data is never mixed with another's. The `internal/tesla/` adapter is stateless about identity and is handed the credentials to use on every call. See `ai/architecture.md` for the module structure and boundary rules, and `ai/agentic-workflow.md` for how AI assistants build it.

> The multi-tenant design **is live** — `internal/account`, `internal/gateway`, `internal/tesla`, and `internal/googleauth` back `cmd/web`, the running server, which already scopes data per user and their vehicles. The `vehicle` package has been replaced by the `tesla` adapter. What remain are **setup/config helpers**, not a single-user system: `internal/config` is shared configuration reading (AWS region, Tesla endpoints) used by both `cmd/web` and `cmd/setup`; `internal/auth` is a **shared** Tesla OAuth helper — used by `cmd/setup` (one-shot token capture), `cmd/explore-tesla-api` (on-demand token refresh), and the **running server** (`internal/account` for per-user token refresh; `internal/gateway/handlers` for the live per-user Tesla connect flow). The one-shot callback server that catches Tesla's OAuth redirect during setup lives in `cmd/setup/callback.go`, alongside its only consumer.

## Coding Conventions

This document covers mission, vision, and principles — not day-to-day coding rules. Those
live in `ai/go-conventions.md`, the authoritative Go conventions file every assistant reads
before writing or editing Go code (module structure, persistence patterns, unit-conversion
and time-zone rules, testing policy). Read it alongside this file, not instead of it.

---

# Guiding Principles

The project should always prioritize:

1. Historical data over current snapshots.
2. Long-term trends over individual events.
3. Derived metrics over raw API values.
4. Time-series analytics over transactional storage.
5. Extensibility for future Tesla APIs.
6. Technology independence.
7. High cohesion and loose coupling.
8. Testability.
9. Maintainability.
10. Low operational cost.
11. **Read-optimized storage.** Reads far outnumber writes (users open dashboards many times a day; writes happen once nightly during the telemetry batch). Schema, indexes, and module interfaces must bias toward read performance. Pre-compute summaries during the nightly batch so dashboards read cheap during the day. See `ai/architecture.md` §7 for the full workload profile and concrete conventions.

Raw Tesla API responses are only an intermediate step. The primary value of the platform comes from calculated insights.

---

# Primary Objectives

For each user and their vehicles, the platform answers questions like *is my battery degrading
normally*, *how much energy do I consume per kilometre*, *what are my charging habits*, and
*how much am I saving against a petrol car*. The full set, and the metrics and dashboards that
could answer them, live in [`VISION.md`](VISION.md).

Raw Tesla API responses are only an intermediate step. The value is in the calculated insights.

---

# Data Collection Strategy

Identify Tesla APIs that provide valuable **historical** information — battery, charging
sessions, vehicle state, drive state, climate, configuration. Evaluate whether a new Tesla API
opens an analytical opportunity.

---

# Data Retention Philosophy

**Prefer storing historical events over overwriting state.** Battery and vehicle snapshots,
charging sessions, trips, daily and monthly summaries.

**Historical information is never discarded** unless a retention policy explicitly requires it.
The development database holds hand-verified history Tesla cannot re-supply, so price any
destructive operation before proposing it.

---

# Polling Strategy

Not every Tesla API should be called at the same frequency. Choose intervals from vehicle
state, charging and driving status, sleep state, data volatility, rate limits, battery impact
and cost. Adaptive polling is preferred over fixed schedules.

**The vehicle must not be woken merely to collect data.**

## Data Access Model (who may call Tesla)

**End users never trigger a Tesla API call on demand.** The only user-initiated call is listing
the account's vehicles when a Tesla account is first connected. Every other Tesla API call is
made by a server-side scheduled or background job, and **dashboards read exclusively from data
the platform has already stored**.

A scheduled wake is sanctioned only when it is the one way to collect data a platform feature
requires — the nightly anchor snapshot, for example. That is a bounded exception to the no-wake
rule above, allowed because no on-demand collection path exists. Tesla API calls are paid and
they wake the car.

---

# Storage Strategy

The persistence layer supports efficient historical analysis: prefer immutable event records,
avoid designs that lose historical information, and optimize for trend analysis, time-series
queries, aggregation and forecasting.

---

# What to propose

Think like a software architect and a data analyst. Proactively suggest new metrics, missing
historical data, better data models, aggregation opportunities, useful dashboards, performance
improvements and anomaly detection. [`VISION.md`](VISION.md) carries the standing candidate
list.

---

# Success Criteria

The project succeeds when **each user** can understand the long-term behaviour of **their
vehicles** without manually inspecting Tesla API responses — battery health, charging
behaviour, driving efficiency, utilization, operating costs, long-term trends and forecasts.

A comprehensive **multi-tenant** Tesla intelligence platform, not a Tesla API client.

<!-- CODEGRAPH_START -->
## CodeGraph

This project has a CodeGraph MCP server (`codegraph_*` tools) configured. CodeGraph is a tree-sitter-parsed knowledge graph of every symbol, edge, and file. Reads are sub-millisecond and return structural information grep cannot.

### When to prefer codegraph over native search

Use codegraph for **structural** questions — what calls what, what would break, where is X defined, what is X's signature. Use native grep/read only for **literal text** queries (string contents, comments, log messages) or after you already have a specific file open.

| Question | Tool |
|---|---|
| "Where is X defined?" / "Find symbol named X" | `codegraph_search` |
| "What calls function Y?" | `codegraph_callers` |
| "What does Y call?" | `codegraph_callees` |
| "What would break if I changed Z?" | `codegraph_impact` |
| "Show me Y's signature / source / docstring" | `codegraph_node` |
| "Give me focused context for a task/area" | `codegraph_context` |
| "See several related symbols' source at once" | `codegraph_explore` |
| "What files exist under path/" | `codegraph_files` |
| "Is the index healthy?" | `codegraph_status` |

### Rules of thumb

- **Answer directly — don't delegate exploration.** For "how does X work" / architecture / trace questions, answer with 2-3 codegraph calls: `codegraph_context` first, then ONE `codegraph_explore` for the source of the symbols it surfaces. Codegraph IS the pre-built index, so spawning a separate file-reading sub-task/agent — or running a grep + read loop — repeats work codegraph already did and costs more for the same answer.
- **Trust codegraph results.** They come from a full AST parse. Do NOT re-verify them with grep — that's slower, less accurate, and wastes context.
- **Don't grep first** when looking up a symbol by name. `codegraph_search` is faster and returns kind + location + signature in one call.
- **Don't chain `codegraph_search` + `codegraph_node`** when you just want context — `codegraph_context` is one call.
- **Don't loop `codegraph_node` over many symbols** — one `codegraph_explore` call returns several symbols' source grouped in a single capped call, while each separate node/Read call re-reads the whole context and costs far more.
- **Index lag**: the file watcher debounces ~500ms behind writes; don't re-query immediately after editing a file in the same turn.

### If `.codegraph/` doesn't exist

The MCP server returns "not initialized." Ask the user: *"I notice this project doesn't have CodeGraph initialized. Want me to run `codegraph init -i` to build the index?"*
<!-- CODEGRAPH_END -->
