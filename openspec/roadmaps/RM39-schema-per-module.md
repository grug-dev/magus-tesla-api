# RM39 — One Postgres Schema per Module

Source ticket: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module

## Intention

Every `internal/` module that owns persistence gets its own Postgres schema, named after
the module. `account.accounts`, `telemetry.vehicle_snapshots`, `charging.charge_sessions`,
`analytics.vehicle_metrics`. The modular-monolith boundary — today enforced only by Go
import guards and convention — becomes visible in the database and checkable at codegen
time.

## Status

**PROPOSED — not started.** The roadmap is written and the branch exists. No tier has been
implemented. Tier 4 is blocked on a separate ticket (see D6).

## Findings that shaped this roadmap (read before touching anything)

All four were established by running the real toolchain, not by reasoning. They are the
reason the design looks the way it does.

| Finding | Evidence | Consequence |
|---|---|---|
| sqlc **tracks** `ALTER TABLE … SET SCHEMA` | `sqlc v1.31.1`, scratch repro: two migrations, second one moves the table; generated model became `TelemetryVehicleSnapshot` | Additive migrations are viable — **D1** |
| sqlc **rejects bare table names** once a table leaves `public` | same repro, bare `SELECT * FROM vehicle_snapshots` → `relation "vehicle_snapshots" does not exist`, exit 1 | Schema-qualifying every query is **forced, not chosen** — **D2** |
| Schema-qualifying **renames every generated struct** | `telemetry.vehicle_snapshots` → `TelemetryVehicleSnapshot` | Without a fix, ~144 non-test Go call sites churn — **D3** |
| `gen.go.rename` needs the **singularized** key | `telemetry_vehicle_snapshots` → ignored; `telemetry_vehicle_snapshot` → works | The obvious key form silently does nothing — **D3** |

The last one deserves emphasis: a wrong `rename` key produces **no error and exit 0**. It
just quietly fails to rename. Every tier must verify its generated `models.go` type names
rather than trusting the config.

## Decisions (binding — settled with the owner before any artifact was written)

**D1 — Additive migrations, not rewritten history.** Each module gets ONE new migration:
`CREATE SCHEMA IF NOT EXISTS <module>` + `ALTER TABLE … SET SCHEMA <module>`. No existing
migration is edited (one exception under D6). Rejected: rewriting ~25 migration files to
create tables in their schema directly — it needs `make db-reset`, and it breaks
`charging`'s backfill migration, which the additive route leaves working.

**D2 — Every query is schema-qualified.** Not a style choice: sqlc resolves names
statically from the migration files and fails codegen on a bare name. A `search_path` set
on the role cannot help, because the failure is at **generate** time, not run time.
This is a benefit, not a tax — a module reaching into another module's table must spell
`telemetry.vehicle_snapshots` in its own `query.sql`, which is greppable and reviewable.

**D3 — `gen.go.rename` keeps every Go type name exactly as it is today.** The schema move
alone would rename all 12 generated structs. Each tier adds `rename` entries under its own
`sqlc.yaml` entry (`gen.go.rename`, **not** the top-level `overrides:` block, which is
ignored here) so the public Go surface does not move at all. Type names that MUST survive
unchanged:

| Module | Types that must not change |
|---|---|
| `account` | `Account`, `TeslaToken`, `Vehicle` |
| `telemetry` | `PollAttempt`, `PollRun`, `SuperchargerSession`, `VehicleSnapshot` |
| `charging` | `ChargeSession`, `ManualChargeEntry` |
| `analytics` | `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark` |

Key form is `<schema>_<singular table>` — e.g. `telemetry_vehicle_snapshot: "VehicleSnapshot"`.

**D4 — goose is unchanged.** The shared `public.goose_db_version` table stays exactly as it
is. The Makefile passes no `-table` flag, so goose's unqualified bookkeeping table keeps
resolving through `search_path` to `public`; goose stores version numbers, not table names,
so every applied record stays valid. **No `db-reset` is needed anywhere in this roadmap.**
Consequence, stated plainly: `make migration-guard` is still required and is NOT retired by
this roadmap — version collisions come from goose keying by version number across dirs,
which is independent of table schemas. Follow-up filed as backlog #23.

**D5 — No `fleet_` prefix.** MAG-31 also proposed renaming `supercharger_sessions` and
`vehicle_snapshots` to `fleet_*`. Dropped: the schema name already carries that namespace,
and `telemetry.fleet_vehicle_snapshots` uses two mechanisms for one job. Saves ~126 SQL
line edits.

**D6 — Tier 4 (`telemetry`) is blocked on a separate boundary ticket.**
`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` reads
`public.supercharger_sessions` directly (lines 233, 269) — a real cross-module database
access, the one the architecture forbids. It is a one-time historic backfill.

The moment `telemetry` moves, that `to_regclass` guard returns NULL and the backfill
silently skips. **In production this is harmless** (it already ran; a fresh DB has nothing
to copy). **In tests it is not**: `internal/charging/testdb_test.go:61` provisions
telemetry's migrations first, so the guard trips inside the suite and the two backfill
tests break:

- `TestBackfill_RealFourRowDataset_OnePercentageBearing` (A1)
- `TestBackfill_IdempotentOnRerun_OverwritesNothing` (A2)

That test file's own header already documents the assumption this roadmap invalidates:
*"this package's TestMain always applies telemetry's migration directory first … so the
`to_regclass` guard never actually trips inside this suite."*

The owner chose to fix the boundary violation under its own ticket rather than inside
RM39. **Tier 4 must not start until that ticket lands.** `internal/analytics` also uses
`ProvisionDirs` and must be checked the same way when it does.

## Tiers

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[ ]` | `RM39-account-move-to-own-schema` | `account` | 3 tables: `accounts`, `tesla_tokens`, `vehicles`. No cross-module reads. The pilot tier — it establishes the pattern every later tier mirrors. | — | Create the OpenSpec change moving `internal/account`'s tables into an `account` schema. ONE new goose migration (`CREATE SCHEMA` + `ALTER TABLE … SET SCHEMA`), schema-qualify every table reference in `internal/account/db/query.sql`, and add `gen.go.rename` entries to `sqlc.yaml` so `Account`, `TeslaToken` and `Vehicle` keep their exact current names. Verify the generated `models.go` type names — a wrong rename key fails silently with exit 0. Follow D1–D5. |
| `[ ]` | `RM39-analytics-move-to-own-schema` | `analytics` | 3 tables: `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps`. Its `vehicle_snapshots` / `supercharger_sessions` mentions are watermark **string values** and comments, not table references — they must NOT be changed. | 1 | Mirror tier 1 for `internal/analytics`. Preserve `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark`. Take care: `vehicle_metric_watermarks.source` holds the literal strings `'vehicle_snapshots'`, `'supercharger_sessions'`, `'manual_charge_entries'` — these are data, and a CHECK constraint depends on them. Do not schema-qualify them. |
| `[ ]` | `RM39-charging-move-to-own-schema` | `charging` | 2 tables: `charge_sessions`, `manual_charge_entries`. Its backfill migration still reads `public.supercharger_sessions` and still works here, because `telemetry` has not moved yet. | 1 | Mirror tier 1 for `internal/charging`. Preserve `ChargeSession`, `ManualChargeEntry`. Do NOT touch `20260823000001_add_charge_sessions.sql` — its cross-module read is tier 4's problem and is owned by a separate boundary ticket (D6). |
| `[ ]` | `RM39-telemetry-move-to-own-schema` | `telemetry` | 4 tables: `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`, `poll_runs`. The largest tier and the one that trips the backfill guard. | 1, 3, **+ the external boundary ticket (D6)** | **BLOCKED until the boundary ticket lands.** Mirror tier 1 for `internal/telemetry`, preserving `PollAttempt`, `PollRun`, `SuperchargerSession`, `VehicleSnapshot`. Then reconcile `charging`'s backfill tests A1/A2, which break the moment this tier applies. Re-check `internal/analytics`' `ProvisionDirs` usage too. |

Tier order rationale: `account` first because it has zero cross-module entanglement, so it
proves the pattern cheaply. `telemetry` last because it is the only tier whose landing
breaks another module's tests.

## Per-tier work shape (every tier does exactly this)

1. One new goose migration — `CREATE SCHEMA IF NOT EXISTS <module>` + one
   `ALTER TABLE … SET SCHEMA <module>` per owned table, with a real `-- +goose Down`.
2. Schema-qualify every table reference in that module's `db/query.sql`.
3. Add `gen.go.rename` entries to that module's `sqlc.yaml` entry (singular keys).
4. Run `sqlc generate`; **diff `models.go` and confirm no type name changed**.
5. `go build ./... && go vet ./... && gofmt -l` — the owner runs the suite (see
   Test-Execution-Policy in `CLAUDE.md`).
6. Update the module's `AGENTS.md` and any doc naming its tables.

## Future work

Recorded in `openspec/roadmaps/backlog.md`:

- **#23** — per-module goose version table, which would retire `make migration-guard` (D4).

The `charging` → `telemetry` backfill boundary violation (D6) is the owner's separate
ticket, not a backlog item.
