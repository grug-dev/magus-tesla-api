# platform-sweep-tenancy-rule-docs

> Source: MAG-70 — https://linear.app/magus-monitor/issue/MAG-70/7-sweep-the-conventions-specs-and-kb
> Step 7 of the MAG-63 re-key decision record
> (https://linear.app/magus-monitor/issue/MAG-63/re-key-vehicle-data-on-vehicle-identity-not-account-id).
> Steps 1-6 re-keyed `charge_gaps`, `vehicle_snapshots`, built the `vehicleref` authorization
> seam, and re-keyed `supercharger_history`, `supercharger_sessions`, and
> `manual_charge_entries`/`vehicle_metrics`. All are Done or Canceled. This step touches no
> table — it corrects the docs, specs, and KB guides that still teach the rule MAG-63 reversed.

## Why

MAG-63 reversed the platform's leading rule for multi-tenant tables. The old rule was:
`account_id` is the leading index column on every multi-tenant table. Steps 1-6 replaced it
with three rules — key on `tesla_id` when it is `NOT NULL`, key on `vin` when `tesla_id` can
still be absent, or keep `account_id` demoted to an attribute when the row records who acted
rather than what the car did — and moved tenancy proof to the gateway
(`internal/vehicleref`, `openspec/changes/archive/platform/2026-09-12-platform-add-vehicle-authorization-seam`).

Several places still teach the old rule, or describe ports and watermarks the way they worked
before the six prior steps changed them:

- `ai/go-conventions.md` and `ai/architecture.md` both still state "`account_id` is the leading
  index column on every multi-tenant table" as the binding convention.
- `openspec/specs/analytics/spec.md` carries two near-identically-named requirements that
  contradict each other — one still says the capability passes an account identifier to every
  underlying read, the other (already correct) says it does not. A third requirement in the
  same file, for a different derivation, ends with the same stale claim.
- `openspec/specs/charge-session-log/spec.md` still describes the Supercharger mirror's
  watermark as one per account. The table backing it,
  `internal/charging/db/migrations/20260912000003_rekey_mirror_watermarks_on_tesla_id.sql`,
  dropped `account_id` and keys on `tesla_id` now.
- Two Go interface methods on `telemetry.SuperchargerHistoryReader` —
  `SuperchargerHistoryByVehicle` and `SuperchargerHistoryByVehicleBetween` — have no caller
  left outside `internal/telemetry` itself. They predate the bounded, watermark-driven read
  (`SuperchargerHistoryByVehicleUpdatedSince`) that replaced their only real use.
- Five `kkpa/context/` guides describe the old port shape or the old index key in their
  file/component maps.
- No guard enforces the new rule yet, so a future query could reintroduce
  `WHERE account_id = $1` on a module outside `internal/account` without anything catching it.

A stale KB guide is worse than a missing one — `kkpa-context-fetch` presents it as
authoritative, so an agent trusts it instead of reading the code
(`CLAUDE.md` §"Docs track structural change").

## What Changes

- **`ai/go-conventions.md`** (~lines 263-266) — replace the single `account_id`-leading rule
  with the three-rule keying table (`tesla_id` / `vin` / demoted `account_id`), plus the
  index-redundancy note: a `UNIQUE (a, b)` constraint already serves equality on `a`, point
  lookups on `(a, b)`, range scans on `b` within one `a`, and `ORDER BY b DESC` pinned to one
  `a` — do not add a second index that only restates it.
- **`ai/architecture.md`** (~lines 315-319) — the same table, plus the new rule: the gateway
  authorizes the vehicle; modules below it do not check tenancy.
  `internal/vehicleref` makes the check a compile-time value (`Ref`'s unexported field),
  enforced by `make vehicleref-guard`.
- **Delete two dead ports from `internal/telemetry`**: `SuperchargerHistoryByVehicle` and
  `SuperchargerHistoryByVehicleBetween` — the interface method (`telemetry.go`), the
  implementation (`reader.go`), the logging decorator (`query_log.go`), the query
  (`db/query.sql`, regenerated via `sqlc generate`), and their integration tests.
  `SuperchargerHistoryByVehicleUpdatedSince` is **kept** — `internal/app/processor.go:223` is
  its live, only caller, the nightly Supercharger mirror's bounded read. The fake
  `SuperchargerHistoryReader` in `internal/app/processor_test.go` narrows in the same wave.
  `SuperchargerHistoryByAccount` no longer exists as a Go symbol — nothing to do there.
- **New `make tenancy-guard`**, wired into `make check`, mirroring the shape of the project's
  eight existing guards. Fails when a module's query file, outside `internal/account`, filters
  on `account_id` with a word-boundary match — deliberately excluding the demoted attribute
  columns `created_by_account_id` and `polled_by_account_id`, which record who acted, not what
  the car did. Escape hatch: `// tenancy:allow: <reason>`. Measured today: the guard would
  pass with zero violations — this change only adds the guard so a future regression is caught.
- **`openspec/specs/analytics/spec.md`** — two corrections:
  - Remove the stale "Multi-Tenant Scoping on Every Underlying Read" requirement. An
    already-correct requirement of the same name (capitalized differently) already states the
    platform's current rule for the capability's other derivation; keeping both taught two
    contradictory rules.
  - Correct "Recent Energy-Per-Kilometre Derivation" — the requirement's closing sentence said
    the capability scopes every underlying read to the given account, "as defense-in-depth
    tenant isolation." Checked against `internal/analytics/reader.go:134-161`: three of the
    four calls (`SnapshotsByVehicleSince`, `ListSessionsByVehicle`, `ListEntriesByVehicle`) take
    the vehicle identifier alone; the account identifier reaches exactly one call,
    `carTypeFor`, resolving the vehicle's car type — never a scoping filter. The requirement now
    says exactly that: vehicle-scoped reads, account-identified car-type lookup. `RecentEfficiency`
    still takes and uses `accountID` — this is not "no account identifier," it is "not a scoping
    parameter." `internal/analytics/reader.go` itself is unchanged; this is a spec-only fix
    (`design.md` D3).
- **`openspec/specs/charge-session-log/spec.md`** — rewrite "Supercharger Mirror
  Synchronization Is Bounded By An Account Watermark" as a per-vehicle watermark requirement,
  keeping its two central guarantees (no advance on an empty read; advance only to the highest
  instant actually observed, never to the run's own instant).
- **Five `kkpa/context/` guides** reconciled: `entities/vehicle-metrics/guide.md`,
  `architecture/telemetry-ingest-only.md`, `architecture/nightly-cycle.md`,
  `architecture/schema-per-module.md`, `workflows/supercharger-stats-read.md`. Component-map
  and file-map facts (e.g. `nightly-cycle.md`'s "Table effects" row for `vehicle_snapshots`,
  which still names `(account_id, tesla_id, captured_date)` as the on-conflict target —
  verified stale, the real constraint is `(tesla_id, captured_date)` since
  `20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`) are corrected directly. Glossary,
  gotcha, and `INDEX.md` changes are staged under `kkpa/context/pending-spec-to-sync/` for the
  user's review, per the standing KB-sync rule — never applied directly.
- **Not changed** — checked and found nothing stale: `openspec/specs/telemetry/spec.md` (no
  account-scoping requirement; its account-level headings are about polling, which stays
  legitimately account-level), `openspec/specs/charging/spec.md` (no such requirement),
  `openspec/specs/manual-charge-log/spec.md:256` "Multi-tenant isolation" (already corrected
  by MAG-68), `openspec/specs/gateway/spec.md:947` "Tenant Isolation" (out of scope for this
  change). Three stale table/column comments are also left alone deliberately — see design.md
  D4.
- **Also not changed, on purpose:** `telemetry.vehicle_snapshots.account_id` — the column is
  dead (nullable since MAG-65, unwritten, unread, uncovered by any index), but dropping it is
  `MAG-83`'s job (https://linear.app/magus-monitor/issue/MAG-83/squash-migrations-to-one-baseline-per-module-each-with-its-own-goose),
  which squashes each module's migrations to one baseline and removes the column by leaving it
  out. Dropping it here would add a migration and an edit to
  `internal/analytics/db/migrations/20260908000002_add_tpms_pressure_columns.sql:82` that
  `MAG-83` then deletes anyway — see design.md D8.

**No unit tests** — the source ticket excludes them. The only test files touched are the
integration tests and fake that must shrink alongside the two deleted port methods; that is a
deletion ripple, not new test authorship.

**Breaking, narrowly.** `telemetry.SuperchargerHistoryReader` loses two exported methods. No
other module calls either one today (verified by repo-wide search) — every production caller
already uses `SuperchargerHistoryByVehicleUpdatedSince` exclusively — so no other module's
code changes as a result. A future consumer of the removed methods would need
`SuperchargerHistoryByVehicleUpdatedSince` instead. No database object changes: no table,
column, index, constraint, view, or migration.

**Affected modules:** `internal/telemetry` (port deletion), `internal/app` (test fake only, no
production code change — it already calls only the kept method), `internal/charging` (one doc
comment only — it named a deleted telemetry method; no code change). Docs: `ai/`,
`openspec/specs/`, `kkpa/context/`, `Makefile`, `CLAUDE.md`, root `README.md`.

**No read path is touched.** The two deleted methods have no caller today, so no request or
batch read changes shape, latency, or query count. The new guard is a build-time check, not a
runtime one.

## Capabilities

### Modified Capabilities

- `analytics` — spec correction only: removes one stale, self-contradicting requirement, and
  corrects a second requirement's closing sentence (the Wh/km derivation's account-scoping
  claim). Capability behavior does not change; the spec now says what the capability already
  does.
- `charge-session-log` — spec correction only: the Supercharger-mirror synchronization
  requirement is rewritten to describe the per-vehicle watermark the capability already uses.
  Capability behavior does not change.

### New Capabilities

(none)

## Impact

- `internal/telemetry` — `telemetry.go`, `reader.go`, `query_log.go`, `db/query.sql` (+
  regenerated `db/query.sql.go`), and the two integration test files for the deleted methods.
- `internal/app` — `processor_test.go`'s fake `SuperchargerHistoryReader` narrows; no
  production file changes.
- `ai/go-conventions.md`, `ai/architecture.md` — the keying-rule section rewritten;
  `architecture.md` gains the gateway-authorizes-tenancy rule.
- `Makefile` — one new guard target, added to `check`'s dependency list.
- `CLAUDE.md`, root `README.md` — the new guard added to the allowed-commands / build
  instructions, per the project's "workflow decisions are documented with their steps" rule.
- `openspec/specs/analytics/spec.md` — one requirement removed, one requirement corrected.
  `openspec/specs/charge-session-log/spec.md` — one requirement rewritten. (Deltas in this
  change's `specs/`.)
- `kkpa/context/` — five guides corrected or reviewed; any glossary/gotcha/`INDEX.md` fix
  staged for review, not applied.
- No database object of any kind is touched — no migration, no schema, no query shape change
  beyond dropping the two dead SQL queries.
