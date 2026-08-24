Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 5 of 8 (`analytics` takes ownership of `charge_gaps`; tiers 1–4 are archived
— `RM29-analytics-rename-from-battery`, `RM29-charging-rename-from-manualcharge`,
`RM29-analytics-add-vehicle-metrics`, `RM29-telemetry-drop-derived-columns`; tier 6
is `RM29-charging-add-charge-sessions`; tier 7 is `RM29-app-add-process-vehicle-data`;
tier 8 is parked)
Unit tests: characterization only (roadmap D10) — none apply to this tier; `charge_gaps`
has no offline/pure-function tests to begin with (`GapWriter` is DB-only), so the whole
test surface is the existing `DATABASE_URL`-gated integration suite, which moves and is
re-homed with every assertion and every expected value unchanged. See Testing below.

## Why

`charge_gaps` is a **derived conclusion analytics reaches, stored in a table telemetry
owns** — the exact boundary blur MAG-26 exists to fix, and the mirror image of what tier
4 already did for the five per-day consumption figures. Three specific symptoms:

1. **`internal/telemetry` stores a value it never computes and never reads back.**
   `charge_gaps` holds one row per vehicle-day whose battery math does not add up — a
   conclusion `internal/analytics`'s `ConsumedByDay` derivation reaches (D5/D5a: the
   corrected `consumed_pct` doesn't add up against stored charge records). Telemetry's
   own migration comment already says this plainly: "Owned by `internal/telemetry`,
   written through the `GapWriter` port by `internal/battery`" — pre-tier-1 language for
   what is `internal/analytics` today. Nothing in `internal/telemetry` ever reads
   `charge_gaps` back; its only reader-to-be is a future notification feature, out of
   scope here, and its only writer today is `cmd/poller`'s composition root calling
   straight through to `telemetry.NewGapWriter`.

2. **`internal/telemetry.go` carries three types that describe analytics' own domain
   vocabulary, not telemetry's.** `MissingChargingType` ("which charge source is
   suspected missing"), `ChargeGap` (one flagged vehicle-day), and the `GapWriter` port
   itself are all defined in `internal/telemetry/telemetry.go`, but every one of them is
   about a conclusion `internal/analytics` reaches from `internal/analytics.ConsumedByDay`
   — telemetry's own doc comments already say "internal/analytics computes it,
   internal/telemetry stores it through the `GapWriter` port". After tier 4 moved the
   derivation itself into analytics, storing its conclusion in telemetry is the one
   remaining piece of this domain sitting in the wrong module.

3. **`cmd/poller`'s composition root is the only place bridging the two.**
   `newNightlyReconciler` (`cmd/poller/main.go`) reads `internal/analytics`'s
   `ConsumedByDay` result and hands the flagged days to `internal/telemetry`'s
   `GapWriter` — a cross-module write orchestrated entirely from `cmd/`, because
   `telemetry` may not import `analytics` (dependency direction, `ai/architecture.md`
   §2) and analytics could not, until this change, write its own conclusion.

Unlike tier 4, this is **not** a re-derivation — `charge_gaps`' schema, its three
queries, its Go types and its `GapWriter` implementation are already correct and
already own-module-scoped in shape; they simply live in the wrong module. The whole
vertical slice (table → queries → types → port → implementation) moves together,
unchanged in behavior, exactly as tier 2 moved `manual_charge_entries` (commit
`fe69cc8`).

## What Changes

- **The `charge_gaps` table moves from `internal/telemetry/db/migrations/` to
  `internal/analytics/db/migrations/` via `git mv` of the single existing migration
  file — no new migration, no DROP, no CREATE, no data copy.** See design.md D1 for
  the full reasoning (goose's shared `goose_db_version` table; why the "obvious"
  DROP+CREATE shape silently loses data under this project's `MIGRATIONS_DIRS`
  ordering; the T2 precedent; the file's self-containment).
- **The three `charge_gaps` queries move** from `internal/telemetry/db/query.sql` to
  `internal/analytics/db/query.sql`: `UpsertChargeGap`, `DeleteChargeGap`,
  `ChargeGapDatesByVehicleBetween`. `make sqlc` regenerates both modules —
  `telemetrydb` loses every `ChargeGap*` symbol, `analyticsdb` gains them.
- **`ChargeGap`, `MissingChargingType` (with its two constants) and the `GapWriter`
  interface + `NewGapWriter` constructor move** from `internal/telemetry/telemetry.go`
  to `internal/analytics/analytics.go`; `internal/telemetry/gap_writer.go` (the
  concrete implementation) moves to `internal/analytics/gap_writer.go`, re-targeted at
  `analyticsdb.Queries`. The port's contract (`ReconcileWindow`'s upsert-and-delete
  window semantics, tenant-isolation validation, out-of-window rejection, single-
  transaction all-or-nothing guarantee) is **carried over unchanged** — see design.md
  T5-I2 / D2 and the Test Contract below.
- **`cmd/poller/main.go` re-wires the port**, not its shape: `newNightlyReconciler`
  now takes `analytics.GapWriter` and builds `[]analytics.ChargeGap`;
  `telemetry.NewGapWriter(pool)` becomes `analytics.NewGapWriter(pool)`. Step 1
  (`Reconcile`) and step 2 (charge-gap reconciliation) keep their exact current shape
  and order, **including the pre-existing behavior that a step-2 error only `continue`s**
  — this tier does not touch that; see design.md T5-I2.
- **`internal/gateway/handlers/history.go` re-points one type reference**:
  `chargeTypeLabel`'s parameter changes from `telemetry.MissingChargingType` to
  `analytics.MissingChargingType` — the file already imports `internal/analytics` (for
  `DayConsumption`), so no new import.
- **`internal/telemetry/AGENTS.md` and `internal/analytics/AGENTS.md` are updated** to
  reflect the new ownership — telemetry drops its `GapWriter`/"Data ownership"
  `charge_gaps` sections, analytics gains them.

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key changes, no
schema object created/altered/dropped, no stored row touched. `cmd/poller`'s nightly
gap-reconciliation behavior (order, per-vehicle isolation, error handling) is bit-for-
bit identical; only which package's symbols it calls changes.

**Yes — internally.** `telemetry.ChargeGap`, `telemetry.MissingChargingType`,
`telemetry.GapWriter` and `telemetry.NewGapWriter` cease to exist; `analytics` gains
each, identically shaped. Every in-repo consumer (`cmd/poller`,
`internal/gateway/handlers/history.go`) is re-pointed in this change.

## Modules Affected

- **`internal/analytics/`** — gains `charge_gaps`' migration, its three queries, the
  `ChargeGap`/`MissingChargingType`/`GapWriter`/`NewGapWriter` Go surface, and
  `gap_writer.go`'s implementation. `internal/analytics/AGENTS.md` gains the matching
  "Data ownership" and port-inventory sections.
- **`internal/telemetry/`** — loses all of the above; after this change `internal/
  telemetry` has zero references to `ChargeGap`, `MissingChargingType` or `GapWriter`.
  `internal/telemetry/AGENTS.md` loses the matching sections.
- **`cmd/poller/`** — its composition root re-wires `newNightlyReconciler`'s
  `GapWriter`/`ChargeGap` types from `telemetry.*` to `analytics.*`; no other change
  to its orchestration logic.
- **`internal/gateway/`** — `handlers/history.go`'s `chargeTypeLabel` re-points its
  parameter type; `handlers/history_test.go`'s five `DayConsumption` test fixtures
  re-point their `MissingChargingType` values to `analytics.*` (found during the
  artifacts pass, not named in the dispatch's binding outcomes — see design.md D3).
  No other gateway file references either symbol.

## Database Changes

**One migration file relocated; zero DDL.** `20260815000002_add_charge_gaps.sql`
moves, byte-for-byte, from `internal/telemetry/db/migrations/` to
`internal/analytics/db/migrations/`. No `-- +goose Up`/`Down` content changes. Full
reasoning for why this is safe under this project's shared-`goose_db_version`,
per-directory-ordered migration runner — and why the naive "telemetry DROPs, analytics
CREATEs + copies" shape would silently lose data — is in design.md's "Database
Changes" section. **This change trips the `database` design gate**: the owner must
confirm there is no DDL here (a file relocation, not a schema change) before Apply.

## Read Paths Affected

**None.** `charge_gaps` has no read port today (its only reader-to-be is the
out-of-scope future notification feature) and this change adds none. The one
write path — `GapWriter.ReconcileWindow`, called once per vehicle per nightly run
from `cmd/poller` — keeps its exact query shape, its exact transaction shape, and the
exact index it already uses (`idx_charge_gaps_account` plus the UNIQUE constraint's
own index); see design.md's Index Plan.

## Capabilities

### Added Capabilities

None — `charge_gaps`' behavior does not change; it changes which capability owns it.
See "Modified Capabilities" below.

### Modified Capabilities (ownership move — behavior unchanged)

- **`telemetry`'s "Charge Gap Ledger" requirement is REMOVED.** The behavior it
  described is preserved, verbatim, by `analytics`' new identically-named requirement.
  See `specs/telemetry/spec.md` `## REMOVED Requirements`.
- **`analytics` gains the "Charge Gap Ledger" requirement**, moved with every
  scenario unchanged. See `specs/analytics/spec.md` `## ADDED Requirements`.

### Out of scope (explicitly deferred)

- **`charge_sessions`** — tier 6, independent.
- **`internal/app` / `ProcessVehicleData` / `process_runs`** — tier 7.
- **A read port for `charge_gaps`** (the future notification feature). Unchanged by
  this tier — still not built, in either module.
- **Collapsing `cmd/poller`'s two-step reconciliation into one transaction.**
  Considered and rejected for this tier as a behavior change beyond an ownership move
  — see design.md T5-I2.

## Testing

Roadmap D10 in substance (characterization — behavior pinned, not redesigned), but
concretely: `charge_gaps` has no offline unit tests to move (`GapWriter` has always
been tested only via `DATABASE_URL`-gated DB-integration tests, mirroring
`SuperchargerReader`'s precedent — see design.md's Test Contract for why). The single
`DATABASE_URL`-gated integration file
(`internal/telemetry/db_gap_writer_integration_test.go`, 7 tests, 595 lines) moves to
`internal/analytics/`, repackaged from `package telemetry` to `package analytics`,
re-targeted from `newGapWriter`/`telemetrydb` to the same-named unexported constructor
against `analyticsdb`, with **every assertion and every expected value unchanged**.
design.md's Test Contract restates the seven scenarios' expected values up front, per
`ai/go-conventions.md`'s "author expected values up front" convention, even though
they are moving rather than new.

Per the Test-Execution-Policy: the assistant writes/moves these tests and runs
`go build ./...`, `go vet ./...`, `gofmt -l`, `make build`/`vet`/`bins`, the standalone
guards and `make sqlc` after the migration and query moves — never `go test ./...`.
The owner runs the suite; until they do, this tier's status is
**awaiting-user-verification**, never "done".
