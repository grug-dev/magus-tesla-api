Source: MAG-14 — https://linear.app/magus-monitor/issue/MAG-14/startend-battery-percentage
Roadmap: openspec/roadmaps/RM27-supercharger-battery-percentage.md
Tier: 1 of 2 (telemetry; tier 2 is `RM27-battery-estimate-supercharger-soc`, module `battery`)

## Why

MAG-14 asked for `start_battery_pct` / `end_battery_pct` on `supercharger_sessions`,
derived "based on the API response." That premise is disproven: `GET
/api/1/dx/charging/history` carries no state-of-charge field of any kind — verified
against the real captured response (`cmd/explore-tesla-api/output/2-ChargingHistory.json`);
every key in the payload is already mapped by `ChargingSessionTesla`. The existing
migration already documents this ("no battery percentage",
`20260716000001_add_supercharger_sessions.sql:53`), and this change corrects that stale
claim while it's here.

The values ARE recoverable — not by reading, but by solving two equations (energy delta
and the DC taper-curve time integral) in two unknowns. That solver is tier 2's job, in
`internal/battery`, computing on read. This tier's job is narrower: give the human a place
to **verify or override** the estimate, and give tier 2 a place to **read** that override.
Full rationale and the settled decisions (R1–R7) are in
`openspec/roadmaps/RM27-supercharger-battery-percentage.md`.

## What Changes

- **Add three nullable columns to `supercharger_sessions`:** `start_battery_pct SMALLINT
  CHECK (0..100)`, `end_battery_pct SMALLINT CHECK (0..100)`, `battery_pct_source TEXT
  CHECK (IN ('user_verified', 'polled'))` — the **live override trio**. These mirror the
  exact `_pct` / `SMALLINT CHECK` shape `manual_charge_entries` already uses for the same
  concept (R2).
- **Add two more nullable columns:** `start_battery_pct_est SMALLINT CHECK (0..100)`,
  `end_battery_pct_est SMALLINT CHECK (0..100)` — a **frozen, write-once verification
  snapshot** (design D6, added at the design-gate review). These record what
  `internal/battery`'s live estimate showed at the exact moment a human set the trio above
  ("model said 82, human said 79"), written once in the same write as the trio and never
  refreshed again — this is a permanent drift log, deliberately NOT the nightly-refreshed
  `_est` shape the roadmap's own rejected-alternatives list already ruled out. Full
  distinction (why staleness here is correct, not a bug, and why a nightly-refreshed pair
  has no legal writer under this project's module-ownership rule while this write-once
  pair does) is in design.md D6.
- **The trio and the snapshot pair are all a human-owned verification/override channel —
  nothing in this repository writes any of them in this tier.** The verification UI that
  will eventually write them is out of scope (R7, backlog entry 11). This tier ships
  nullable storage plus the read path only.
- **LOAD-BEARING (R3, extended by D6 to the snapshot pair): all five columns are excluded
  from `UpsertSuperchargerSession`'s `ON CONFLICT DO UPDATE SET` clause — and from its
  `INSERT` column list.** Because nothing writes any of them yet, the simplest and
  strongest way to satisfy R3 is to not reference the five columns in that query at all: a
  fresh row gets the SQL column default (`NULL`) automatically, and the nightly re-upsert —
  which mutates billing fields as Tesla finalizes them — has no way to ever touch them,
  today or after a careless future edit that forgets to special-case them. Only a
  header-comment addition documents this; no functional SQL changes to that query.
- **`battery_pct_source` never stores `'estimated'`.** R4/R6 forbid persisting the solved
  estimate anywhere — it is computed on read, in `internal/battery` (tier 2), and never
  written back to telemetry. NULL trio therefore *is* the "estimated" state (tier 2 falls
  back to its live computation, R5); a non-NULL `battery_pct_source` exists only to say
  *why* a human-owned value overrides that fallback. The frozen snapshot pair does not
  reopen this: it records what the estimate *was*, once, and is never itself an input to
  or a cache of tier 2's live computation. Full rationale in design.md D2/D6.
- **Extend the domain `SuperchargerSession` struct and its DB→domain mapper** with all five
  new fields so `SuperchargerReader` (unchanged interface — same two methods) already
  surfaces them, since both read queries (`SuperchargerSessionsByAccount`,
  `SuperchargerSessionsByVehicle`) use `SELECT *` and pick the new columns up automatically
  once sqlc regenerates.
- **No new index.** Neither existing index nor either read query filters or sorts on any of
  the five columns; they ride along on the same row fetch every consumer already pays for.
  Full justification in design.md's Index Plan.

## Breaking

**No.** Purely additive:

- Five new nullable columns on an existing table — no existing column, constraint, or
  index is altered or removed.
- `SuperchargerSession` gains five new named pointer fields — additive, compile-compatible
  with every existing named-field `SuperchargerSession{...}` struct literal.
- `SuperchargerReader`'s two method signatures are unchanged.
- `UpsertSuperchargerSession`'s `INSERT`/`VALUES`/`ON CONFLICT` SQL text is **functionally
  unchanged** — only a header comment is added.

## Modules Affected

- **`internal/telemetry/`** — sole module touched: migration, `SuperchargerSession` struct,
  `rowToSuperchargerSession` mapping, a new `pgNullableInt16AsInt` mapping helper, a
  documentation-only comment on `upsertSuperchargerSession`/`UpsertSuperchargerSession`,
  `AGENTS.md`, new DB-integration tests.
- No other `internal/` module. `internal/battery` (tier 2, `RM27-battery-estimate-
  supercharger-soc`) is explicitly **out of scope** — not created, read, or touched by this
  change; it depends on this tier's schema and read-port extension once it lands.

## Database Changes

One migration on `supercharger_sessions` (a table owned solely by `internal/telemetry/db`):
five new nullable columns (the trio plus the frozen snapshot pair, D6), five `CHECK`
constraints, updated column/table comments, no new index. design.md is REQUIRED (this
change touches the DB) and includes: the full `Up`/`Down` DDL, the rationale and rejected
alternatives for the `battery_pct_source` value-set decision, the frozen-snapshot-pair
decision (D6, including why a nightly-refreshed alternative has no legal writer under this
project's module-ownership rule), and the "exclude from the upsert entirely" mechanism
(R3, extended by D6), and an index plan justified against both of the table's two existing
indexes (verdict: no new index). The database design gate triggered, required one revision
(the frozen snapshot pair), and passes with that revision incorporated before
implementation begins.

## Read Paths Affected

`SuperchargerReader.SuperchargerSessionsByAccount` and
`SuperchargerReader.SuperchargerSessionsByVehicle` — both already `SELECT *` against
`supercharger_sessions` under `idx_supercharger_sessions_account_time` and
`idx_supercharger_sessions_vehicle_time` respectively. All five new columns ride along on
the same indexed row fetch both queries already perform: zero additional query, zero query
plan change, zero additional read cost for any existing or future caller (matches this
project's read-heavy Performance-Profile). The nightly write path
(`UpsertSuperchargerSession`) is likewise unaffected in cost — none of the five columns is
ever bound as a parameter.

## Capabilities

### Modified Capabilities

- **`telemetry`** — extends "Supercharger Session Ledger": each stored session now also
  carries a human-owned, nullable battery-percentage-verification trio
  (`start_battery_pct`, `end_battery_pct`, `battery_pct_source`) AND a frozen,
  nullable, write-once verification-time snapshot pair (`start_battery_pct_est`,
  `end_battery_pct_est`, D6) that the nightly upsert never writes or overwrites. Extends
  "Supercharger Session Read Port": both read methods now surface all five columns on
  every returned `SuperchargerSession`. See `specs/telemetry/spec.md`.

### Out of scope (explicitly deferred)

- **The verification UI that writes the trio and the snapshot pair** — backlog entry 11, a
  separate future change. This tier ships nullable storage plus the read path only (R7).
- **`internal/battery`'s taper-curve estimator** — tier 2, `RM27-battery-estimate-
  supercharger-soc`, a separate change with its own proposal. Not created, read, or
  referenced here. It must never read `start_battery_pct_est`/`end_battery_pct_est` as a
  cache of its own live computation (design D6).
- **A dedicated index on `battery_pct_source` or any of the four `SMALLINT` columns.** No
  query in this tier or its known future consumer (the read-only tier 2 estimator) filters
  or sorts on them; see design.md's Index Plan for the full argument against both existing
  indexes.
- **A separate audit table preserving every re-verification's snapshot.** Considered at the
  design gate and rejected for this tier as disproportionate (a second table/query for a
  low-volume log); logged in design.md D6 as the trigger to revisit if repeated
  re-verification of the same session becomes common.
- **A `verified_at` timestamp column.** Offered at the design gate and declined for now
  (design.md D6); can be added later as a sixth nullable column with no migration conflict.

## Resolved decisions

R1–R7 were settled with the user via grill-me before this proposal was written (recorded
verbatim in `openspec/roadmaps/RM27-supercharger-battery-percentage.md`, 2026-08-15).
design.md's decisions map directly onto them (D1 = R2/R3, D2 = R2's shape, etc.) — restated
and applied there with full schema/rationale, not re-litigated.
