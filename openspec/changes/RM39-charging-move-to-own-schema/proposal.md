Source: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module
Roadmap: openspec/roadmaps/RM39-schema-per-module.md
Tier: 3 of 5 (`account` and `analytics` are archived; the stopper gate — an owner-run
`make db-reset` — sits immediately after this tier; tier 4 is
`RM39-telemetry-move-to-own-schema`, blocked on a separate boundary ticket, D6; tier 5 is
`RM39-telemetry-rename-supercharger-port`, blocked behind tier 4)

## Why

MAG-31 asks that every `internal/` module owning persistence get its own PostgreSQL schema,
named after the module, so the modular-monolith boundary — today enforced only by Go import
guards (`ai/architecture.md` §2) and convention — becomes visible in the database catalog
and checkable at codegen time. Tiers 1 (`account`) and 2 (`analytics`) proved the pattern:
one additive goose migration, schema-qualified queries, `gen.go.rename` entries that freeze
the Go surface, and a `models.go` diff as the mandatory verification step. This tier repeats
that pattern for `internal/charging`, with one addition tiers 1–2 did not carry: the owner
reopened roadmap decision D5 on 2026-09-02 and kept MAG-31's original table-rename request,
in a sharpened form (D5a/D5b/D5c) — so this tier also renames `charge_sessions` to
`supercharger_sessions`.

**Why the rename, not just the schema move.** `charge_sessions` over-claims: the table is a
dense, Supercharger-only mirror of `internal/telemetry`'s data (its own migration header
says so explicitly — "this table originates no value"), yet its name reads as *all*
charging. `internal/charging/charging.go` already carries the correction as a documented
"deliberate divergence": `SuperchargerSessionAnalyticsReader` was named "Supercharger" for
call-site readability at its one caller, `internal/analytics` (RM31 D8). D5b makes the table
agree with a name the module already chose — it removes a divergence rather than creating
one, and the comment that currently calls it a divergence needs updating once the table
agrees.

**Why this must run before tier 4, not after.** `internal/telemetry` still owns
`public.supercharger_sessions` at this point in the roadmap — tier 4 is blocked on a separate
boundary ticket (D6) with no ETA. Renaming charging's table to the same bare name while both
tables sit in `public` would collide. Roadmap D7 sequences the one migration this tier ships
so the rename happens *inside* the new `charging` schema, after the `SET SCHEMA` move, so the
two same-named tables coexist under different schema qualifiers
(`charging.supercharger_sessions` vs `public.supercharger_sessions`) until tier 4 moves
telemetry's copy. This also frees D5b from D6's block — charging's rename does not wait for
the boundary ticket.

**The vocabulary collision this rename creates.** `analytics.vehicle_metric_watermarks.source`
holds a closed 3-string vocabulary naming the physical table each of `Recalculator.Reconcile`'s
three cursors tracks. Migration `20260828000001` already rewrote that vocabulary once, from
`'supercharger_sessions'` (when the string named telemetry's table) to `'charge_sessions'`
(when analytics moved its Supercharger read onto `internal/charging`, RM31). This tier's rename
means the table analytics should now be naming is `supercharger_sessions` again — the *same*
string the vocabulary held before `20260828000001`, but now naming a different physical table
than it did then. Roadmap D8 requires deleting the stale `'charge_sessions'`-labelled watermark
rows and rewriting the CHECK constraint to accept `'supercharger_sessions'` again, copying
`20260828000001`'s own approach rather than inventing a new one.

**This proposal surfaces, rather than silently follows, a boundary question the roadmap's
tier-3 row does not resolve**: `vehicle_metric_watermarks` is `internal/analytics`'s table
(moved into the `analytics` schema by tier 2), not `internal/charging`'s. `design.md`'s
"D8 — Boundary" section states the finding and this worker's recommendation in full: the
DELETE + CHECK rewrite, and the companion Go source-label change in
`internal/analytics/recalculate.go`, are **analytics-owned work**, sequenced after this
tier by `MIGRATIONS_DIRS` order (`account → telemetry → charging → analytics`) rather than
by same-directory chronology — not a task this change's own sandbox (`internal/charging/`
plus this artifacts folder) can complete. The exact SQL and Go change are fully specified in
`design.md` so nothing is lost; `tasks.md` marks that task as owned outside this change.

This tier also carries forward **D9** (raw SQL in `_test.go` files is invisible to sqlc/`go
vet`; the roadmap's own re-measurement puts this module at **32** statements across **10**
files — confirmed by this worker's own quote-agnostic grep, unchanged from the roadmap's
figure) and checks for **D12**'s out-of-order-migration-replay pattern (not present in this
module — see `design.md`).

## What Changes

- **Migration** — one new goose migration,
  `internal/charging/db/migrations/20260902000003_move_charging_to_own_schema.sql`, creates
  the `charging` schema, moves `charge_sessions` and `manual_charge_entries` into it via
  `ALTER TABLE … SET SCHEMA`, then renames `charging.charge_sessions` to
  `charging.supercharger_sessions`, then renames `idx_charge_sessions_vehicle_stop` and the
  `charge_sessions_pct_source_required` CHECK constraint to match — in the mandatory D7
  order. No existing migration is edited (`20260823000001_add_charge_sessions.sql` is
  explicitly left alone — see design.md's "Do NOT touch" section — its cross-module
  backfill read is tier 4's problem, D6). Every row, every remaining constraint (PK, the
  `UNIQUE (account_id, session_id)` constraint), and every index survives, catalog-only.
- **sqlc regeneration** — every table reference in `internal/charging/db/query.sql` (13
  statements across both tables) becomes schema-qualified. The two query names that embed
  the old table name, `MirrorChargeSession` and `VerifyChargeSession`, are renamed to
  `MirrorSuperchargerSession` and `VerifySuperchargerSession` (their generated `*Params`
  types follow automatically); `LockSessionForVerification` is unaffected — no
  "ChargeSession" in its name.
- **`sqlc.yaml`** — the charging entry's `gen.go` block gains a `rename:` map: the
  singularized `charging_manual_charge_entry` key keeps `ManualChargeEntry` unchanged (D3),
  while `charging_supercharger_session` is a **new** key mapping to `SuperchargerSession` —
  deliberately NOT preserving `ChargeSession` (D5c: the Go type follows the table rename,
  the opposite of D3's freeze, because the whole point of D5a/D5b is to retire stale
  vocabulary, not leave it half-fixed in the code that gets read most).
- **Test files (D9)** — 32 hand-written SQL statements across 10 `_test.go` files that
  reference `charge_sessions`/`manual_charge_entries` are schema- and (for
  `charge_sessions`) name-qualified. `db_backfill_integration_test.go`'s references to
  `supercharger_sessions` are telemetry's own table and stay bare — telemetry has not moved.
  One integration test (`db_session_reader_by_vehicle_integration_test.go`, T-Order2) asserts
  literal `EXPLAIN` plan text containing `idx_charge_sessions_vehicle_stop`; its expected
  string updates to `idx_supercharger_sessions_vehicle_stop`.
- **Docs** — `internal/charging/AGENTS.md`'s Data Ownership section, and the RM31 D8
  "deliberate divergence" comment on `SuperchargerSessionAnalyticsReader` in
  `internal/charging/charging.go` (the divergence no longer exists once the table agrees).
  Several `kkpa/context/` files name `charge_sessions`/`ChargeSession` by string — see
  `tasks.md`'s KB sub-task for the full, grep-verified list.
- **Not in this change's sandbox** (see design.md + tasks.md): the
  `vehicle_metric_watermarks` CHECK rewrite + DELETE (D8) and the companion
  `sourceChargeSessions` label change in `internal/analytics/recalculate.go`.

**Breaking?** Not for any Go consumer that already goes through this module's public ports —
every exported `Writer`/`Reader`/`SessionWriter`/`SessionReader`/`SuperchargerSessionAnalyticsReader`/`SessionVerifier`
method name and signature is unchanged; only the underlying sqlc-generated `ChargeSession`
struct name changes, to `SuperchargerSession`, and that type is private to
`internal/charging`'s four DB-facing files (`service.go`, `session_writer.go`,
`session_reader.go`, `session_verifier.go`) — no other module or `_test.go` file imports
`chargingdb`. Database-level: additive and reversible (catalog-only `SET SCHEMA`/`RENAME`
statements), but it does retire the `charge_sessions` table/index/constraint names — a
consumer with a raw SQL dependency outside this module (there is none inside this
repository) would break.

**Affected modules:** `internal/charging` for the schema/rename work. `internal/analytics`
is the recommended (not this change's) owner of the D8 watermark cleanup — see design.md.
`internal/gateway` and `internal/app` are unaffected: both call this module only through its
public interfaces, none of which changes name or signature.

## Capabilities

### New Capabilities

- `charging`: a new requirement, "Module-Scoped Database Schema," stating that
  `manual_charge_entries` and (renamed) `supercharger_sessions` live in a dedicated
  `charging` Postgres schema, and a second requirement, "Supercharger Sessions Table
  Renamed," stating the `charge_sessions` → `supercharger_sessions` rename and its Go/index/
  constraint consequences.

### Modified Capabilities

(none — `charge-session-log` and `manual-charge-log`'s existing requirements describe
capability *behavior*, which this migration does not change; see `ai/go-conventions.md`
§Testing "Read optimization" and the openspec config rule "implementation (HOW) lives in
design.md")

## Impact

- `internal/charging` — one new migration, 13 `query.sql` table references become
  schema-qualified, 2 query names renamed, `sqlc.yaml` gains a `rename:` block (one
  preserving, one new), `make sqlc` regeneration (verified against an expected non-empty,
  fully-accounted diff — `ChargeSession` → `SuperchargerSession` only), 32 hand-written test
  SQL statements schema/name-qualified, one EXPLAIN-text assertion updated,
  `AGENTS.md`/`charging.go` doc updates.
- `internal/analytics` — NOT touched by this change's own sandbox; design.md fully specifies
  the companion migration + Go label change this tier's rename requires, recommended to land
  as analytics-owned work sequenced after this tier.
- No other module. `internal/gateway`, `internal/tesla`, `internal/telemetry`,
  `internal/account` are untouched — none imports `chargingdb`.

**Read paths affected** (per `openspec/config.yaml`'s performance rule): every existing
`charging` read path (`Reader.ListEntriesBy*`, `SessionReader.ListSessionsByVehicleBetween`,
`SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince`/`ListSessionsByVehicle`)
is unaffected in cost — `ALTER TABLE … SET SCHEMA` and `RENAME` are catalog-only; the
renamed index continues to serve the identical query plans post-migration (see design.md's
index plan). No new index is added and none is needed.
