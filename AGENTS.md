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

> The multi-tenant design **is live** — `internal/account`, `internal/gateway`, `internal/tesla`, and `internal/googleauth` back `cmd/web`, the running server, which already scopes data per user and their vehicles. The `vehicle` package has been replaced by the `tesla` adapter. What remain are **setup/config helpers**, not a single-user system: `internal/config` is shared configuration reading (AWS region, Tesla endpoints) used by both `cmd/web` and `cmd/setup`; `internal/auth` and `internal/server` are used only by `cmd/setup` — the standalone one-time OAuth-capture tool — and are not part of the running server.

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

Raw Tesla API responses are only an intermediate step. The primary value of the platform comes from calculated insights.

---

# Primary Objectives

For each user and their vehicles, the platform should answer questions such as:

* Is my battery degrading normally?
* How has my battery capacity changed over time?
* How much energy do I consume per kilometer?
* How efficient is my driving?
* What affects my efficiency the most?
* How much money am I saving compared to a gasoline vehicle?
* What are my charging habits?
* How much phantom drain does the vehicle experience?
* How accurate are Tesla's range predictions?
* Are there unusual behaviors that deserve investigation?
* How does weather affect energy consumption?
* How does driving style affect efficiency?
* How often do I fast charge?
* How healthy are my charging habits?

The platform should always evolve toward answering more meaningful questions.

---

# Data Collection Strategy

The platform should identify Tesla APIs that provide valuable historical information.

Typical categories include:

* Battery information
* Charging sessions
* Vehicle state
* Drive state
* Climate state
* Vehicle configuration


Agents should continuously evaluate whether new Tesla APIs introduce useful analytical opportunities.

---

# Data Retention Philosophy

Prefer storing historical events rather than overwriting state.

Examples include:

* Battery snapshots
* Charging sessions
* Vehicle snapshots
* Climate snapshots
* Trips
* Daily or weekly summaries (The best that cannot consume too much tesla API quota)
* Monthly summaries

Historical information should never be discarded unless retention policies explicitly require it.

---

# Metrics

Whenever possible, derive metrics instead of storing only raw values.

Examples include:

Battery

* Estimated battery health
* Capacity degradation
* Range degradation
* Battery aging
* Battery efficiency

Charging

* Home charging ratio
* Other charging ration
* Supercharger ratio
* Charging efficiency
* Average charging speed
* Charging duration
* Energy added
* Charging costs

Driving

* Energy per kilometer
* Energy per trip
* Average speed
* Daily distance
* Monthly distance
* Seasonal efficiency
* Driving efficiency score

Cost

* Electricity costs
* Estimated gasoline equivalent
* Savings
* Cost per kilometer
* Cost per month

Vehicle Usage

* Daily utilization
* Idle time
* Sleep time
* Phantom drain
* Vehicle availability

Forecasting

* Predicted battery degradation
* Estimated remaining battery capacity
* Projected yearly energy costs
* Charging recommendations

Agents are encouraged to identify and implement additional derived metrics whenever they provide meaningful insights.

---

# Polling Strategy

Not every Tesla API should be called at the same frequency.

The platform should intelligently determine polling intervals based on:

* Vehicle state
* Charging status
* Driving status
* Sleep state
* Data volatility
* API rate limits
* Battery impact
* Cost

The vehicle should not be unnecessarily awakened solely for data collection.

Adaptive polling is preferred over fixed schedules.

---

# Storage Strategy

The persistence layer should support efficient historical analysis.

Prefer immutable event records.

Avoid designs that lose historical information.

Optimize for:

* Trend analysis
* Time-series queries
* Aggregations
* Forecasting
* Dashboards

---

# Dashboard Philosophy

Dashboards should explain the vehicle's behavior rather than merely displaying values.

Useful visualizations include:

Battery

* Battery health over time
* Capacity degradation
* Range degradation
* Battery temperature history

Charging

* Charging sessions
* Energy added
* Charging locations
* Charger type distribution
* Charging efficiency

Driving

* Daily distance
* Monthly distance
* Energy consumption
* Efficiency trends
* Energy per kilometer

Cost

* Electricity cost
* Cost per kilometer
* Monthly expenses
* Savings versus gasoline

Vehicle Usage

* Daily activity
* Idle time
* Phantom drain
* Vehicle availability

Forecasts

* Battery lifespan
* Expected degradation
* Annual charging costs
* Future range estimates

Agents are encouraged to propose new dashboards whenever historical data supports additional insights.

---

# AI Responsibilities

AI coding assistants should proactively:

* Suggest new metrics.
* Identify missing historical data.
* Recommend better data models.
* Detect opportunities for aggregation.
* Recommend useful dashboards.
* Identify performance optimizations.
* Suggest statistical analyses.
* Recommend forecasting models.
* Detect anomalies.
* Improve maintainability.

Agents should think like both a software architect and a data analyst.

---

# Success Criteria

The project succeeds when **each user** can understand the long-term behavior of **their vehicles** without manually inspecting Tesla API responses.

Every feature should increase the user's understanding of:

* Battery health
* Charging behavior
* Driving efficiency
* Vehicle utilization
* Operating costs
* Long-term trends
* Future predictions

The project should evolve into a comprehensive **multi-tenant** Tesla intelligence platform rather than a simple Tesla API client.

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
