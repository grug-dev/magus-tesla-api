Source: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module
Roadmap: openspec/roadmaps/RM39-schema-per-module.md
Tier: 1 of 5 (`account`; tier 2 is `RM39-analytics-move-to-own-schema`, tier 3 is
`RM39-charging-move-to-own-schema`, tier 4 is `RM39-telemetry-move-to-own-schema` — blocked on a
separate boundary ticket, D6 — tier 5 is `RM39-telemetry-rename-supercharger-port`, blocked
behind tier 4)

## Why

MAG-31 asks that every `internal/` module owning persistence get its own PostgreSQL schema, named
after the module, so the modular-monolith boundary — today enforced only by Go import guards
(`ai/architecture.md` §2 "no cross-module database leaks") and convention — becomes visible in the
database catalog and checkable at codegen time. `internal/account` is the pilot: it is the tier
the roadmap orders first specifically because it has **zero cross-module entanglement** (no other
module reads its tables, and its own migrations read nothing from another module), so it proves the
pattern — one additive goose migration, schema-qualified queries, `gen.go.rename` entries that
freeze the Go surface — before the pattern is repeated against modules that do have entanglement
(`charging`'s backfill, `telemetry`'s cross-module read).

This tier implements roadmap decisions **D1** (one additive migration: `CREATE SCHEMA IF NOT
EXISTS account` + one `ALTER TABLE … SET SCHEMA account` per table, real reversible `-- +goose
Down`), **D2** (every table reference in `internal/account/db/query.sql` becomes schema-qualified —
forced by sqlc, not a style choice), **D3** (`gen.go.rename` entries under the account `sqlc.yaml`
entry keep `Account`, `TeslaToken`, `Vehicle` byte-identical, with an explicit `models.go` diff
verification since a wrong rename key fails silently at exit 0), and **D4** (goose itself is
untouched — the shared `public.goose_db_version` table stays, `make migration-guard` is not
retired, no `db-reset` is needed). The table-rename decisions **D5a/D5b/D5c do not touch this
module** — `account`'s three tables (`accounts`, `tesla_tokens`, `vehicles`) keep their exact
names; only their schema changes.

## What Changes

- **Migration** — one new goose migration in `internal/account/db/migrations/` creates the
  `account` schema and moves `accounts`, `tesla_tokens`, and `vehicles` into it via `ALTER TABLE …
  SET SCHEMA account`. No existing migration is edited. Every row, primary key, foreign key,
  `UNIQUE`/`CHECK` constraint, and index carries over unchanged — `ALTER TABLE … SET SCHEMA` is a
  catalog-only rename, not a table rewrite (see `design.md` for the proof).
- **sqlc regeneration** — every table reference in `internal/account/db/query.sql`
  (`accounts`, `tesla_tokens`, `vehicles`, including the two `EXISTS (SELECT 1 FROM accounts a …)`
  subqueries) becomes schema-qualified (`account.accounts`, `account.tesla_tokens`,
  `account.vehicles`). sqlc fails codegen on a bare name once a table leaves `public` (roadmap
  "Findings" table) — this is forced, not chosen.
- **`sqlc.yaml`** — the account entry's `gen.go` block gains a `rename:` map (three entries,
  singularized `account_<table>` keys) so `Account`, `TeslaToken`, and `Vehicle` keep their exact
  current Go names through the schema move. `models.go` must be diffed after `make sqlc` to confirm
  zero type-name drift — the config accepts a wrong key silently.
- **Docs** — `internal/account/AGENTS.md`'s "Boundaries" section notes the `account` Postgres
  schema; any other doc naming these tables by bare name is checked and updated if it would now
  read as stale (design.md scopes the search).
- **No Go domain-type, port, or query *name* change.** `Account`, `TeslaToken`, `Vehicle`,
  `Service`, and every exported method keep their exact current shape — this tier is pure
  namespacing at the database layer.

**Not breaking.** The migration is additive (new schema, catalog-only table moves, no dropped
column, no changed type, no renamed table) and every constraint/index survives. It changes zero
public Go signatures, so no consumer (the gateway, `cmd/web`, `cmd/poller`) needs to change. The
only externally-observable difference is that `to_regclass('accounts')` (unqualified, relying on
`search_path`) still resolves after the move — `search_path` defaults to `"$user", public` and
`account` is not on it — so this migration also has no `search_path` dependency to introduce; see
`design.md` D-index-plan for why nothing on the runtime path issues an unqualified table reference
outside sqlc-generated code (which is regenerated schema-qualified by this same change).

**Affected modules:** `internal/account` only. No other module reads `account`'s tables (verified —
`ai/architecture.md` §2, and the roadmap names this tier "no cross-module entanglement"), so no
sibling module's code, tests, or `sqlc.yaml` entry changes.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `account`: a new requirement, "Module-Scoped Database Schema," stating that the account
  module's `accounts` and `tesla_tokens` tables live in a dedicated `account` Postgres schema, and
  that this is a namespacing change only — no stored data, constraint, or public interface
  behavior changes.
- `account-vehicle-registry`: the same new requirement, scoped to the `vehicles` table, since that
  table is owned by `internal/account` but specified under this separate capability.

## Impact

- `internal/account` — one new migration, three `query.sql` table references become
  schema-qualified (plus two `EXISTS` subqueries), `sqlc.yaml` gains a `rename:` block, `make sqlc`
  regeneration (verified as a zero-diff on type names), `AGENTS.md` update. No test *behavior*
  change — the existing integration suite exercises the same port with the same expected values
  (see `design.md`'s Test Contract), so no test assertion changes; only the underlying schema the
  test database resolves against does.
- No other module. `internal/gateway`, `internal/tesla`, `internal/telemetry`,
  `internal/charging`, `internal/analytics` are untouched — none imports `accountdb`.

**Read paths affected** (per `openspec/config.yaml`'s performance rule): every existing `account`
read path (`GetAccountByProviderID`, `GetAccountLanguage`, `GetLatestTeslaTokenByAccount`,
`GetLatestTeslaTokenByAccountForUpdate`, `ListVehiclesByAccount`, `ListAllVehicles`) is unaffected
in cost — `ALTER TABLE … SET SCHEMA` does not touch the table's physical storage, its indexes, or
their statistics, so every existing index continues to serve the same plans post-migration (see
`design.md`'s index plan). No new index is added and none is needed.
