Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 1 of 8 (`battery`→`analytics`; tier 2 is `RM29-charging-rename-from-manualcharge`,
module `manualcharge`; tier 3 is `RM29-analytics-add-vehicle-metrics`, module `analytics`;
tiers 4–7 are `RM29-telemetry-drop-derived-columns`, `RM29-analytics-own-charge-gaps`,
`RM29-charging-add-charge-sessions`, `RM29-app-add-process-vehicle-data`; tier 8 is parked)
Unit tests: characterization only (roadmap D10) — none apply to this tier, see Testing below

## Why

MAG-26 asks for an `analytics` module that owns everything the application calculates.
That module already exists in all but name: `internal/battery` is the platform's
derived-metrics module today — it computes efficiency (`derive.go`), consumed-per-day
(`consumed.go`), and the charge-gap flags that `cmd/poller` hands to telemetry's
`GapWriter`. Nothing about its responsibilities changes under MAG-26; only its scope grows.

That growth is what makes the name wrong. Under roadmap D1 the module will own
`vehicle_metrics`, a read model carrying `odometer`, `battery_range`, and eventually the
inside/outside temperatures and TPMS pressures the dashboard renders. A module called
`battery` owning TPMS pressure is a name actively working against the reader.

The roadmap's rule (D3) is *rename only where the name becomes wrong*, which is also why
`telemetry` is **not** renamed to `fleet` anywhere in RM29 — it stays accurate, and the
ticket was updated to match.

This tier is deliberately first, and deliberately mechanical. Every later tier of RM29 is a
real ownership change; doing the rename now means none of those diffs is buried under
900-plus lines of package-qualifier churn, and the roadmap's vocabulary matches the code
from tier 2 onward. It is also the safest possible starting point: `internal/battery` is the
only domain module in the repo with **no database at all** — no `db/` folder, no migrations
directory, no `sqlc.yaml` entry — so a rename here cannot touch schema, migrations or
generated code.

## What Changes

- **`internal/battery/` → `internal/analytics/`**, package `battery` → package `analytics`.
  All five source files move unchanged in content except the package clause:
  `battery.go` → `analytics.go`, plus `capacity.go`, `consumed.go`, `derive.go`,
  `reader.go`. The three test files move alongside them.
- **The public port keeps its shape.** `battery.Reader` becomes `analytics.Reader` with
  both methods unchanged in name, signature and semantics — `RecentEfficiency(ctx,
  accountID, teslaID) (Efficiency, bool, error)` and `ConsumedByDay(ctx, accountID,
  teslaID, start, end) ([]DayConsumption, error)`. `Efficiency`, `DayConsumption`,
  `NewReader`, `DefaultWindow` and `GapReconciliationWindow` likewise keep their names
  under the new package.
- **Six call sites re-point** to the new import path and package qualifier:
  `cmd/poller/main.go`, `cmd/web/main.go`, `internal/gateway/gateway.go`,
  `internal/gateway/handlers/handlers.go`, `internal/gateway/handlers/history.go`, and
  `internal/gateway/handlers/history_test.go`. The `Deps.BatteryReader` field and the
  `batteryReader` struct field are renamed to `AnalyticsReader` / `analyticsReader` so the
  gateway's vocabulary matches the module it depends on.
- **`internal/battery/AGENTS.md` → `internal/analytics/AGENTS.md`**, with its module
  charter updated to state the module's RM29 scope: it owns what the application
  calculates, and from tier 3 it will own a database of its own (which it does not have
  today — a fact its current text asserts and that tier 3, not this one, changes).
- **`openspec/specs/battery/` → `openspec/specs/analytics/`.** The capability's behavior is
  unchanged; only the domain folder is renamed, per `openspec/config.yaml`'s "each OpenSpec
  domain maps to one `internal/` module".
- **Docs updated in the same change** (CLAUDE.md's docs-track-structural-change rule): the
  root `README.md` Project Structure tree, Architecture table and Dependency graph;
  `ai/architecture.md` §4's target blueprint, which currently names `battery/` as its
  example domain module; `internal/gateway/AGENTS.md` and `internal/telemetry/AGENTS.md`
  where they reference `internal/battery`; `docs/battery-consumed-graph.md`; and
  `openspec/roadmaps/backlog.md`.

## Breaking

**No — externally.** This is a compile-time-only rename. No HTTP route, no rendered page,
no database object, no user-visible string and no observable behavior changes. `make build`
and `go vet ./...` are the gate: a missed reference is a compile error, not a runtime
surprise.

**Yes — internally, for every importer**, which is why all six call sites are in this
change rather than deferred. There is no deprecation shim and no type alias left behind:
a temporary `package battery` re-export would leave two names for one module in the
codebase, which is exactly the duplicated-vocabulary cost RM29 exists to remove.

## Modules Affected

- **`internal/battery/` → `internal/analytics/`** — the renamed module. Sole owner of the
  logic; all five source files and three test files move.
- **`internal/gateway/`** — import path, package qualifier, and the `Deps.BatteryReader` →
  `Deps.AnalyticsReader` field rename across `gateway.go`, `handlers/handlers.go`,
  `handlers/history.go`, `handlers/history_test.go`, plus its `AGENTS.md`. **No handler
  logic, no template, no view model and no i18n key changes** — the gateway's
  calculation-to-analytics move is tier 3 (D5), explicitly not this tier.
- **`cmd/poller/`, `cmd/web/`** — composition roots: import path and constructor qualifier
  only. `cmd/poller`'s `reconcilingCollector` and `newGapReconciler` keep their current
  logic verbatim; the app-layer extraction that deletes them is tier 7.
- **`internal/telemetry/`** — **not touched**, except one `AGENTS.md` prose reference.
  Telemetry does not import battery today (the dependency flows one way, `battery →
  telemetry`) and must not start.
- **`internal/manualcharge/`, `internal/account/`, `internal/tesla/`** — not touched.
  `manualcharge`'s own rename is tier 2, a separate proposal.

Cross-module by necessity: a package rename cannot be sandboxed to one module, because the
importers must move in the same commit or the tree does not compile.

## Database Changes

**None.** `internal/battery` has no `db/` folder, no migrations directory and no
`sqlc.yaml` entry — it is documented in `battery.go` as a pure read-side derivation with
"no database and no store", and that remains true after this change. No migration is
written, no migration file moves, `sqlc.yaml` is not edited, and `sqlc generate` is not
run. **`design.md` is therefore not required by the database design gate** for this tier;
tier 3, which gives analytics its first table, is where that gate applies.

## Read Paths Affected

**None.** No query, index, or query plan changes. The two port methods
(`RecentEfficiency`, `ConsumedByDay`) keep their exact signatures and implementations, so
every read they perform through `telemetry.Reader`, `telemetry.SuperchargerReader`,
`manualcharge.Reader` and `account.Service` is byte-for-byte the call it is today. The
`/ui/dashboard/history` consumed chart — the only user-facing read path that reaches this
module — issues the identical sequence of queries before and after.

## Capabilities

### Modified Capabilities

- **`battery` → `analytics`** — the capability folder is renamed, its requirements
  unchanged. No requirement is added, removed or reworded; this is a domain-folder rename
  to keep the OpenSpec domain aligned with its `internal/` module. See
  `specs/analytics/spec.md`.

### Out of scope (explicitly deferred)

- **Any new table, column or migration for analytics** — `vehicle_metrics` and the
  `updated_at` watermark are tier 3 (`RM29-analytics-add-vehicle-metrics`).
- **Moving the gateway's request-time calculation into analytics** (D5) — tier 3.
  `buildOdometerChart`, `buildBatteryChart`, the consumed chart, supercharger stats and
  `mapDashboardSnapshot` keep their current logic here, unchanged.
- **Dropping the five `_calc` columns from `vehicle_snapshots`** — tier 4, and only after
  tier 3 has re-pointed every reader.
- **`charge_gaps` moving into analytics** — tier 5. This tier leaves `telemetry.GapWriter`
  and `telemetry.ChargeGap` exactly where they are; the renamed module keeps calling the
  port through `cmd/poller` as it does today.
- **`internal/manualcharge` → `internal/charging`** — tier 2, separate proposal.
- **`internal/app`, `ProcessVehicleData` and `process_runs`** — tier 7.
- **Renaming `internal/telemetry`** — never, in any tier (D3). The ticket has been updated
  to say `telemetry`; do not re-propose `fleet`.
- **`RecentEfficiency`'s live callers.** The method is currently reachable but unused —
  `cmd/poller` and the gateway both construct the reader and call only `ConsumedByDay`.
  Whether it is dead code is a question for tier 3, which owns the port's future shape; it
  is preserved verbatim here.

## Testing

Roadmap D10 sets **characterization tests only** for RM29 — pin current output before a
move, assert identical output after. **No characterization test is written in this tier**,
because there is no behavior to pin: the compiler proves the property this change claims.
A package rename either compiles with identical logic or fails to build, and `go vet ./...`
compiles `_test.go` files too, so signature drift in the moved tests surfaces without
executing anything.

The three existing test files (`consumed_test.go`, `derive_test.go`, `reader_test.go`,
~1,342 lines) move with the package and are updated only for the package clause and
qualifiers — no assertion, fixture or test name changes.

Per the Test-Execution-Policy in `CLAUDE.md`: the assistant runs `go build ./...`,
`go vet ./...`, `gofmt -l` and the standalone guards. **The owner runs the suite.** Until
they do and report the result, this tier is *awaiting user verification*, never "done".

## Resolved decisions

D1–D10 were settled with the owner via `grill-me` on 2026-08-20, before this proposal was
written, and are recorded in full in
`openspec/roadmaps/RM29-modular-monolith-boundaries.md` and verbatim on MAG-26 itself.

This tier implements **D3** (rename only where the name becomes wrong) and **D8** (renames
first, so the gold-standard slice in tier 3 lands in a clean diff). **D1, D2, D4, D5, D6,
D7 and D9 bound what this tier must not do** — see "Out of scope" above. **D10** governs
its testing posture, as described in Testing.

No decision is re-litigated here. In particular, D3's rejection of the `telemetry` → `fleet`
rename is settled and reflected in the ticket text itself.
