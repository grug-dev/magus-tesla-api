Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 2 of 8 (`manualcharge`→`charging`; tier 1 `RM29-analytics-rename-from-battery` is
archived; tier 3 is `RM29-analytics-add-vehicle-metrics`, module `analytics`; tiers 4–7 are
`RM29-telemetry-drop-derived-columns`, `RM29-analytics-own-charge-gaps`,
`RM29-charging-add-charge-sessions`, `RM29-app-add-process-vehicle-data`; tier 8 is parked)
Unit tests: characterization only (roadmap D10) — none apply to this tier, see Testing below

## Why

MAG-26 asks for a `charging` module that owns everything charging-related, starting with a new
`charge_sessions` table (tier 6) that normalizes Supercharger sessions and hosts the five
battery-pct verification columns currently misplaced on `telemetry.supercharger_sessions`.
`internal/manualcharge` already owns exactly the "manual" half of that domain — `Writer`/
`Reader` over `manual_charge_entries`, isolated from the Tesla Fleet API. Nothing about its
responsibility changes under MAG-26; only its scope grows to include Tesla-reported charging
data alongside user-asserted charging data, which is what makes `manualcharge` the wrong name —
a module that will soon also own Supercharger-sourced sessions cannot keep a name that asserts
every row is manual.

The roadmap's rule (D3) is *rename only where the name becomes wrong* — the same rule tier 1
applied to `battery`→`analytics`. This tier is tier 1's direct sibling: mechanical, no behavior
change, done before tier 6 (`RM29-charging-add-charge-sessions`) so that tier's diff isn't
buried under this one's package-qualifier churn, and so the roadmap's own vocabulary
(`charging`) matches the code from tier 2 onward.

Unlike tier 1, this module **does** own a database — a `db/` folder, two goose migrations, and
a `sqlc.yaml` entry — so this rename also relocates the migrations directory and the sqlc
config, while leaving every database object (table, column, constraint, index) completely
untouched. See "Database Changes" below.

## What Changes

- **`internal/manualcharge/` → `internal/charging/`**, package `manualcharge` → package
  `charging`. Six Go source/test files move: `manualcharge.go` → `charging.go`, plus
  `service.go`, `manualcharge_test.go` → `charging_test.go`, `db_integration_test.go`,
  `testdb_test.go` (external test package `manualcharge_test` → `charging_test` — this module
  uses the sanctioned external-test-package back-edge, not in-package tests, per
  `ai/architecture.md` §2). `AGENTS.md` moves and is rewritten (`Agent-Name: manualcharge` →
  `charging`).
- **`internal/manualcharge/db/` → `internal/charging/db/`** — the whole `db/` folder, including
  both migration files (`20260718000001_add_manual_charge_entries.sql`,
  `20260720000001_require_location_kind.sql`, filenames unchanged) and the three sqlc-generated
  files (`db.go`, `models.go`, `query.sql.go`, package `manualchargedb` → `chargingdb`) plus
  the hand-written `query.sql`.
- **`sqlc.yaml`'s `manualcharge` entry** re-points `schema`/`queries`/`out` to
  `internal/charging/db/...` and renames `package: "manualchargedb"` → `"chargingdb"`.
  `make sqlc` is run after the move to regenerate the three generated files from the moved
  schema/queries (byte-identical output expected — see Database Changes).
- **`Makefile`'s `MIGRATIONS_DIRS`** and **`.air.toml`'s `exclude_dir`** re-point
  `internal/manualcharge/db/migrations` → `internal/charging/db/migrations`.
- **The public port keeps its shape.** `manualcharge.Writer`/`manualcharge.Reader` become
  `charging.Writer`/`charging.Reader` with every method unchanged in name, signature and
  semantics. `Entry` and its three derived methods (`CostPerKWh`, `BatteryDelta`,
  `SessionDuration`) keep their exact names under the new package.
- **Nine call-site files re-point** to the new import path and package qualifier:
  `cmd/poller/main.go`, `cmd/web/main.go`, `internal/gateway/gateway.go`,
  `internal/gateway/handlers/handlers.go`, `internal/gateway/handlers/charges.go`,
  `internal/gateway/handlers/charges_test.go`,
  `internal/gateway/handlers/charges_error_visibility_test.go`,
  `internal/gateway/templates/fragments/charges_vm.go`, and four `internal/analytics/*.go`
  files (`analytics.go`, `consumed.go`, `consumed_test.go`, `reader.go`, `reader_test.go`).
  The `Deps.ManualChargeWriter`/`Deps.ManualChargeReader` fields (in both `gateway.go` and
  `handlers.go`) rename to `ChargingWriter`/`ChargingReader`; the unexported
  `Handler.manualChargeWriter`/`manualChargeReader` fields rename to `chargingWriter`/
  `chargingReader` (design.md D2).
- **Two identifier families are explicitly NOT renamed**, because they name domain vocabulary
  or are sqlc-generated from an unchanged table, not this module's package: the sqlc-generated
  `chargingdb.ManualChargeEntry` struct (generated from the still-named `manual_charge_entries`
  table — renaming it by hand would be reverted by the next `sqlc generate`), and
  `csrfManualChargeKey` / `"csrf_manualcharge"` in `internal/gateway/handlers/charges.go` (names
  the "manual charge" UI feature — a description that remains accurate; tier 6 adds
  `charge_sessions` specifically to hold the *non*-manual, Supercharger-sourced rows). See
  design.md D3.
- **`openspec/specs/manual-charge-log/` is NOT renamed.** It names the capability (what the
  system does — log a manually-asserted charge), not the Go package, the same way
  `tesla-exploration`, `unit-of-measure` and `account-vehicle-registry` are capability names
  decoupled from their modules' import paths. Verified: its `spec.md` contains zero live
  Go-identifier references to `manualcharge` — its only match is a historical archived-change
  citation in the `## Purpose` line, which per tier 1 precedent (D6) is never retconned. See
  design.md D1.
- **Two OTHER capabilities' specs need delta edits**, because their requirement text embeds the
  Go qualifier form, not just a domain word: `openspec/specs/analytics/spec.md` (1 requirement,
  "No Cross-Module Database Access" — 2 references to `internal/manualcharge`/`internal/manualcharge/db`)
  and `openspec/specs/gateway/spec.md` (5 requirements, 24 references — including one whose
  **title itself** names the old package, `manualchargedb`, requiring the OpenSpec `RENAMED
  Requirements` delta operation, not `MODIFIED`). See design.md D4.
- **Docs updated in the same change** (CLAUDE.md's docs-track-structural-change rule):
  `README.md` (12 refs across the sqlc intro, Project Structure tree, Architecture table,
  Dependency graph, and Database-tables-by-module table), `Makefile` (1), `sqlc.yaml` (7),
  `.air.toml` (1 — a functional path, not prose), `docs/0-set-up/running-the-server.md` (1),
  `docs/battery-consumed-graph.md` (7 of 8 refs — the 8th, `csrf_manualcharge`, is the same
  domain-vocabulary exemption as above), `internal/analytics/AGENTS.md` (7),
  `internal/gateway/AGENTS.md` (8 of 9 — same exemption for its 1 `csrf_manualcharge` mention),
  `openspec/roadmaps/backlog.md` (6 of 8 — 2 are historical archived-change-name citations that
  stay), plus two files the leader-supplied inventory missed and this proposal adds:
  `internal/gateway/handlers/lang.go` (1 — a doc comment cross-referencing the live "D4
  amendment" pattern being renamed in `gateway/AGENTS.md`) and `internal/testdb/testdb.go` (1 —
  a doc comment listing the modules using the shared test-DB helper). See design.md D5 for the
  full verified per-file table, including the negative findings: `openspec/specs/manual-charge-log/spec.md`
  and `openspec/roadmaps/RM29-modular-monolith-boundaries.md` both contain "manualcharge" text
  but need **zero** edits (the former is a historical Purpose citation, the latter is the
  roadmap's own immutable planning record — tier 1 established this precedent by leaving its
  own analogous "internal/battery" text untouched in the same file after archiving).

## Breaking

**No — externally.** This is a compile-time-only rename plus a directory move for files goose
tracks by version number, not path (see Database Changes). No HTTP route, no rendered page, no
database object, no user-visible string and no observable behavior changes. `make build` and
`go vet ./...` are the gate: a missed reference is a compile error, not a runtime surprise.

**Yes — internally, for every importer**, which is why all nine call-site files are in this
change rather than deferred. There is no deprecation shim and no type alias left behind, for
the same reason tier 1 rejected one (roadmap "Problem" §3 — a shim would leave two import paths
resolving to one module, exactly the duplicated-vocabulary cost RM29 exists to remove).

## Modules Affected

- **`internal/manualcharge/` → `internal/charging/`** — the renamed module, including its
  database (`db/` folder, both migrations, the three sqlc-generated files, and `query.sql`).
  Sole owner of the logic and its own `manual_charge_entries` table throughout.
- **`internal/gateway/`** — import path, package qualifier, and the `Deps.ManualChargeWriter`/
  `Deps.ManualChargeReader` → `ChargingWriter`/`ChargingReader` field rename across
  `gateway.go`, `handlers/handlers.go`, `handlers/charges.go`, `handlers/charges_test.go`,
  `handlers/charges_error_visibility_test.go`, `templates/fragments/charges_vm.go`, plus its
  `AGENTS.md`. **No handler logic, no template, no view model and no i18n key changes.**
- **`internal/analytics/`** — import path and `manualcharge.Entry`/`manualcharge.Reader`
  qualifier only, across `analytics.go`, `consumed.go`, `consumed_test.go`, `reader.go`,
  `reader_test.go`, plus its `AGENTS.md`. No derivation logic changes.
- **`cmd/poller/`, `cmd/web/`** — composition roots: import path and constructor qualifier
  only.
- **`internal/telemetry/`** — **not touched** in Go code. One pre-existing historical SQL
  comment in an already-applied migration (`db/migrations/20260815000002_add_charge_gaps.sql`)
  names `internal/manualcharge`; left as-is, matching tier 1's own precedent of leaving its
  "internal/battery" mention in that identical file untouched (design.md D6).
- **`internal/account/`, `internal/tesla/`, `internal/googleauth/`** — not touched.

Cross-module by necessity: a package rename cannot be sandboxed to one module, because the
importers must move in the same commit or the tree does not compile.

## Database Changes

**No database object changes — verified.** `manual_charge_entries` keeps its exact name and
every column; no migration is added, edited, or removed; both existing migration **filenames**
are unchanged (only their containing directory moves). What moves is the **directory**
(`internal/manualcharge/db/migrations/` → `internal/charging/db/migrations/`) and the sqlc
config that points at it.

**Why this is safe:** goose records applied migrations in the shared `goose_db_version` table
keyed by the migration's **version number** (the numeric filename prefix), never by directory
path (confirmed independently: `internal/testdb/testdb.go:124` calls
`goose.NewProvider(goose.DialectPostgres, db, migrationsFS)`, which reads the embedded
migration FS's file contents/names, not any stored path; `Makefile:26-31` documents the same
shared-table, path-independent design in its own comment). Both migrations keep their exact
version-prefixed filenames, so goose sees both as already applied in every environment,
regardless of which directory currently holds them. Nothing re-runs; nothing rolls back.

Because no schema object changes, `openspec/config.yaml`'s database design gate ("Any
new/changed database object... MUST include the full schema...") is **not triggered** — see
design.md D7, which states this explicitly rather than silently omitting the section, following
tier 1's own D7 precedent.

## Read Paths Affected

**None.** No query, index, or query plan changes. `Writer.Create/Update/Delete` and
`Reader.ListEntriesByVehicle/ListEntriesByAccount/ListEntriesByVehicleBetween` keep their exact
signatures and SQL (the query text in `query.sql` is not touched — only its containing
directory moves and is regenerated byte-identically by `sqlc generate`). The Charge log page
(`/ui/charges`) and every analytics computation that reads through `charging.Reader` issue the
identical sequence of queries before and after.

## Capabilities

### Modified Capabilities

- **`analytics`** — one requirement's text updated ("No Cross-Module Database Access": the
  `internal/manualcharge`/`internal/manualcharge/db` references it names become
  `internal/charging`/`internal/charging/db`). No requirement is added or removed; behavior is
  unchanged. See `specs/analytics/spec.md`.
- **`gateway`** — one requirement is **renamed** (its title embeds the old package name:
  "Gateway Imports No manualchargedb Package" → "Gateway Imports No chargingdb Package") and,
  together with three other requirements ("Charge List Fragment", "Create Charge Entry",
  "Inline Row Editing", "Delete Charge Entry"), has its `manualcharge`/`manualchargedb`
  references updated to `charging`/`chargingdb`. No requirement is added or removed; behavior is
  unchanged. See `specs/gateway/spec.md`.

### Not modified

- **`manual-charge-log`** — verified zero requirement-text changes (see "What Changes" above
  and design.md D1). No delta spec is produced for this capability in this change.

### Out of scope (explicitly deferred)

- **`charge_sessions` and the five battery-pct columns moving off `supercharger_sessions`** —
  tier 6 (`RM29-charging-add-charge-sessions`), which is exactly why this rename happens first.
- **`internal/manualcharge` → `internal/charging` convergence with Supercharger data** — never
  scheduled in RM29 (roadmap "Future work" — manual ↔ Supercharger convergence is a distinct,
  deferred piece of domain modelling, not a refactor).
- **`internal/app`, `ProcessVehicleData` and `process_runs`** — tier 7.
- **Renaming `internal/telemetry`** — never, in any tier (roadmap D3).
- **Renaming `openspec/specs/manual-charge-log/`** — considered and rejected this tier; see
  design.md D1.

## Testing

Roadmap D10 sets **characterization tests only** for RM29 — pin current output before a move,
assert identical output after. **No characterization test is written in this tier**, for the
same reason tier 1's proposal gave: there is no behavior to pin. A package rename plus a
migrations-directory move either compiles and passes the existing suite unchanged, or fails to
build — `go vet ./...` compiles `_test.go` files too, so signature drift surfaces without
executing anything.

The five existing test files (`charges_test.go`* in the moved module — actually
`manualcharge_test.go`→`charging_test.go`, `db_integration_test.go`, `testdb_test.go` — plus
the four analytics test files and the three gateway test files that import this module) move or
are edited only for the package clause/import path/qualifier and the local-name renames listed
in design.md D2 — no assertion, fixture, or test-name change beyond those.

Per the Test-Execution-Policy in `CLAUDE.md`: the assistant runs `go build ./...`,
`go vet ./...`, `gofmt -l`, and the standalone guards. **The owner runs the suite.** Until they
do and report the result, this tier is *awaiting user verification*, never "done".

## Resolved decisions

D1–D10 were settled with the owner via `grill-me` on 2026-08-20, before tier 1's proposal was
written, and are recorded in full in
`openspec/roadmaps/RM29-modular-monolith-boundaries.md` and verbatim on MAG-26 itself.

This tier implements **D3** (rename only where the name becomes wrong) and **D8** (renames
before the tier-6 vertical slice that needs a clean diff). **D1, D2, D4, D5, D6, D7 and D9
bound what this tier must not do** — see "Out of scope" above. **D10** governs its testing
posture, as described in Testing.

No decision is re-litigated here.
