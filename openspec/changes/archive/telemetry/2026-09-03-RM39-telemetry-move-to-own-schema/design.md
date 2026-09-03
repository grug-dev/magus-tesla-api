> **Reading the D-numbers.** `D1`–`D15` below are **local to this file**. Every reference to a
> roadmap decision is spelled `roadmap D<n>` (e.g. `roadmap D18`). The two numbering spaces are
> unrelated; tier 3's design.md reused the roadmap's numbers and that proved confusing to audit.

## Context

`internal/telemetry` owns **four** tables, all currently in the `public` schema. They are the
platform's ingest tables — the module fetches the Tesla Fleet API and writes what it fetched,
nothing more (`kkpa/context/architecture/telemetry-ingest-only.md`).

| Table | Created by | Shape | Constraints / indexes today |
|---|---|---|---|
| `vehicle_snapshots` | `20260710000002_init_telemetry.sql` (+ 6 later migrations) | one row per (account, vehicle, calendar day); UPSERT on `(account_id, tesla_id, captured_date)` since `20260805000001` | `vehicle_snapshots_pkey`, `vehicle_snapshots_account_tesla_date_unique`, `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` |
| `supercharger_sessions` | `20260716000001_add_supercharger_sessions.sql` (+ `20260815000001`) | one row per Tesla `session_id`; **UPSERT, not append-only** — billing state mutates post-session | `supercharger_sessions_pkey`, `supercharger_sessions_session_id_unique`, 5 auto-named column CHECKs, `idx_supercharger_sessions_vehicle_time`, `idx_supercharger_sessions_account_time` |
| `poll_attempts` | `20260710000002_init_telemetry.sql` (+ `20260823000002`) | append-only, one row per (vehicle, run) | pkey only; no index by design |
| `poll_runs` | `20260830000002_add_poll_runs.sql` | one row per `run_id` (`run_id` **is** the PK, no surrogate `id`) | pkey only; no index by design |

Roadmap `RM39-schema-per-module.md` establishes, by running the real toolchain rather than by
reasoning, the findings this tier inherits: sqlc **tracks** `ALTER TABLE … SET SCHEMA`; sqlc
**rejects a bare table name** once a table leaves `public`, at *generate* time; schema
qualification renames every generated struct unless `gen.go.rename` freezes it; a wrong
`rename` key produces **no error and exit 0**; and raw SQL in `_test.go` files breaks with no
assistant-runnable signal to catch it. This tier additionally carries **roadmap D5a/D5c** (the
table + Go-type rename), **roadmap D18** (catalog-derived completeness), **roadmap D17**
(the sweep crosses module lines because a rename, unlike a move, escapes the sandbox),
**roadmap D25** (D6 superseded; the charging comment folded in) and the **D25 corollary**
(roadmap D12's `search_path` fix is unsafe here).

Performance profile: **read-heavy** (`ai/architecture.md` §7, `CLAUDE.md`
§Performance-Profile). This migration is a one-time DDL event, not a recurring read or write
path.

## Goals / Non-Goals

**Goals**

- Move all four tables into a dedicated `telemetry` Postgres schema via **one** additive goose
  migration with a real, reversible `-- +goose Down`.
- In the SAME migration, rename `supercharger_sessions` → `supercharger_history` (roadmap D5a).
- Rename **every catalog object** still carrying the old table name — the completeness
  criterion is `pg_constraint` / `pg_indexes`, not this document's list (roadmap D18).
- Keep `PollAttempt`, `PollRun` and `VehicleSnapshot` byte-for-byte unchanged as Go type names
  (roadmap D3), and let the Supercharger model + hand-written domain type follow the table
  (roadmap D5c).
- Preserve every row, every constraint semantic and every index's coverage — catalog-only.
- Leave the public port `SuperchargerReader` **entirely unchanged** (tier 5), and say so
  explicitly enough that a reviewer reads the half-state as intended.
- Specify, precisely and up front, every edit needed **outside** this module, so the leader can
  dispatch them without rediscovery.

**Non-Goals**

- No column is added, dropped or retyped on any of the four tables.
- **No existing migration's SQL is edited** (roadmap D1). `20260823000001_add_charge_sessions.sql`
  gets a **comment-only** correction (D12 below) and nothing else.
- No `db-reset` (roadmap D24 waived gate G1; roadmap D23 already proved the pattern preserves
  every row on the owner's live database).
- goose itself is untouched: `public.goose_db_version` stays exactly where it is (roadmap D4),
  and `make migration-guard` stays required.
- **The public port rename is out of scope entirely** — `SuperchargerReader`, its four method
  names, `NewSuperchargerReader`, `superchargerReader`, `rowToSuperchargerSession` and
  `upsertSuperchargerSession` all keep their current names. That is roadmap tier 5, split out
  because the surface has 88 references outside this module.
- No new index, no query-shape change (see Index Plan).

## Decisions

### D1 — One additive migration: `CREATE SCHEMA` → `SET SCHEMA` ×4 → `RENAME TO` → 9 catalog renames → `COMMENT ON` refresh

The migration is one new goose file,
`internal/telemetry/db/migrations/20260903000001_move_telemetry_to_own_schema.sql`
(see "Migration filename" for how the timestamp is chosen and re-verified). **That file is the
single source of truth for the DDL. This section specifies the ORDER and the reasoning, not a
second copy of the SQL.** Tier 3's design.md embedded the full statement text under a heading
reading "Exact DDL" and it drifted three times — it never gained the `COMMENT ON` refresh and
kept a four-object rename after the scope grew to nine — so "exact" became false in two
directions while the shipped migration was correct. Per `CLAUDE.md`'s AI-efficiency rule, a
duplicated volatile artifact is not worth the tokens it costs to re-verify.

**Why the pointer cannot go stale.** A shipped migration is never edited again in this project
(roadmap D1, restated in `internal/telemetry/AGENTS.md` and `ai/go-conventions.md`). The file
this section points at is immutable *by rule*, so the archived design.md and the migration
cannot drift apart.

**Mandatory Up order:**

1. `CREATE SCHEMA IF NOT EXISTS telemetry`
2. `ALTER TABLE … SET SCHEMA telemetry` — `vehicle_snapshots`, `supercharger_sessions`,
   `poll_attempts`, `poll_runs`
3. `ALTER TABLE telemetry.supercharger_sessions RENAME TO supercharger_history`
4. the 9 catalog renames — 7 `ALTER TABLE telemetry.supercharger_history RENAME CONSTRAINT …`,
   then 2 `ALTER INDEX telemetry.… RENAME TO …` (see D7)
5. `COMMENT ON TABLE` ×1 + `COMMENT ON COLUMN` ×3, refreshing the text sqlc copies into
   `models.go` (see D8)

**Down reverses in the EXACT opposite order** and ends `DROP SCHEMA IF EXISTS telemetry`. A
non-empty schema cannot be dropped without `CASCADE`; reversing the order means `CASCADE` is
never needed, so a Down can never silently destroy an object it did not create. `make
migrate-down` depends on this (roadmap §"The Makefile needs no changes", caveat 1).

**Why the move must precede the rename, even though tier 3's collision is gone.** Tier 3
needed the D7 order because `public.supercharger_sessions` (telemetry's) still existed while
it renamed its own table into that name. Here the direction is inverted: `charging` already
holds `charging.supercharger_sessions`, and this tier renames *away* from that base name, so
no collision is possible in either order. The order is kept anyway, for two reasons that are
not cosmetic: (a) every statement after step 2 can then address the table by its final schema,
so a reviewer reads one namespace rather than two; (b) it is the order all four tiers use, and
a tier that deviates for no stated benefit costs a reader a diff.

**Why additive, not rewriting the 12 existing migrations.** Rewriting history desynchronises
`goose_db_version` from reality on every environment that already applied those files, and
roadmap D23 has already proved the additive route preserves every row on the owner's live
database. Rejected alternative: rewriting the historic files to create the tables under
`telemetry.` directly — it needs `make db-reset` (waived, roadmap D24) and it would force a
mid-file rework of `20260823000001`'s `to_regclass` guard, which roadmap D1 forbids touching.

**Why one migration for all four tables and the rename.** They deploy together; splitting the
move from the rename would create an intermediate state
(`telemetry.supercharger_sessions`, not yet renamed) that no environment needs to pass
through, and it would need its own ordering argument for no benefit.

**Why `IF NOT EXISTS` / `IF EXISTS`.** Tiers 1–3's own precedent — idempotent DDL that
tolerates re-running against a partially-applied state without erroring.

### D2 — Every table reference in `db/query.sql` is schema-qualified

Not a style choice (roadmap D2): sqlc resolves names statically against the migration files at
**generate** time and fails codegen with `relation "…" does not exist`, exit 1, once a table
has left `public`. A role-level `search_path` cannot help, because the failure is at generate
time, not run time.

Measured directly on `internal/telemetry/db/query.sql` with the quote-agnostic pattern:
**15 table references** across 12 `-- name:` blocks.

| Table | Refs | Becomes |
|---|---|---|
| `vehicle_snapshots` | 7 — `InsertVehicleSnapshot` (INSERT), `ListSnapshotsByVehicle`, `SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`, `SnapshotsByVehicleUpdatedSince`, `LatestSnapshotsByAccount`, `SnapshotPrecedingDay` | `telemetry.vehicle_snapshots` |
| `poll_attempts` | 2 — `InsertPollAttempt` (INSERT), `ListPollAttemptsByVehicle` | `telemetry.poll_attempts` |
| `supercharger_sessions` | 5 — `UpsertSuperchargerSession` (INSERT), and the four `SuperchargerSessionsBy*` SELECTs | `telemetry.supercharger_history` (schema **and** new name) |
| `poll_runs` | 1 — `InsertPollRun` (INSERT) | `telemetry.poll_runs` |

`query.sql`'s prose comments also name `idx_supercharger_sessions_vehicle_time` /
`idx_supercharger_sessions_account_time` and the old table name in several index-reuse notes;
they are updated to the post-rename names in the same pass, because a comment that names a
non-existent index is exactly the stale vocabulary roadmap D5a exists to retire. sqlc copies
`-- name:` block comments into `query.sql.go`, so this is a real generated artifact, not just
a source file.

### D3 — `gen.go.rename`: freeze three type names, let the fourth follow its table

The telemetry entry in the root `sqlc.yaml` has **no `rename:` block at all** today (unlike
`account`, `charging` and `analytics`, which gained one in tiers 1–3). It gains one, under the
entry's existing `gen.go` block — **not** the top-level `overrides:` block, which sqlc ignores
for this purpose:

```yaml
        rename:
          telemetry_poll_attempt:          "PollAttempt"
          telemetry_poll_run:              "PollRun"
          telemetry_supercharger_history:  "SuperchargerHistory"
          telemetry_vehicle_snapshot:      "VehicleSnapshot"
```

Key form is the **singularised** `<schema>_<table>` — `poll_attempts` → `poll_attempt`,
`vehicle_snapshots` → `vehicle_snapshot`, and `supercharger_history` (the table's name **after**
this migration's rename; `history` is already singular, so the key is
`telemetry_supercharger_history`, not `telemetry_supercharger_historie` or
`telemetry_supercharger_histories`).

**The `supercharger_history` key is a special risk and must be verified, not assumed.** The
roadmap's finding — a wrong key form is ignored with **no error and exit 0** — was measured on
a regular plural (`vehicle_snapshots` → `vehicle_snapshot` works, `…_snapshots` does not).
`history` is an *irregular* noun whose plural is `histories`, so sqlc's inflector may or may
not treat the singular form as already-singular. **Do not reason about this.** Run `make sqlc`,
then read `models.go`: if the struct is named `TelemetrySuperchargerHistory` the key did not
match and the correct key must be found empirically (try `telemetry_supercharger_histories`
next) before the task is complete. This is exactly the failure mode roadmap D3 was written
about, arriving on the one table where the inflection is not obvious.

**Three keys are pure preservation (roadmap D3).** Without them the schema move alone would
produce `TelemetryPollAttempt`, `TelemetryPollRun`, `TelemetryVehicleSnapshot` — churn for
nothing, across ~144 non-test Go call sites repo-wide.

**The fourth is a deliberate NEW mapping (roadmap D5c), the exception to D3.** Roadmap D3
freezes names because a schema move is pure namespacing. Roadmap D5a is the reverse case: its
whole purpose is to retire stale vocabulary, so leaving the Go model on `SuperchargerSession`
would half-fix the confusion in exactly the files that get read most (`reader.go`,
`mapping.go`, `service.go`).

Note the consequence, which is a *move* of a name between packages rather than a plain rename:
`SuperchargerSession` does not disappear from the repository — tier 3 gave it to
`internal/charging`'s db model. The two live in different packages and cross no import edge
(`internal/charging` never imports `telemetrydb`, and no module outside `internal/telemetry`
may import it — `ai/architecture.md` §2).

### D4 — The five query-name renames are a SEPARATE sqlc mechanism from `gen.go.rename`

`gen.go.rename` remaps only **table-derived** struct names. Query names come from the
`-- name: <Name> :one/:many/:exec` comment in `query.sql` — a different mechanism with no
`rename:` equivalent — so renaming them means editing the `-- name:` line itself:

| Current | Becomes |
|---|---|
| `UpsertSuperchargerSession` | `UpsertSuperchargerHistory` |
| `SuperchargerSessionsByAccount` | `SuperchargerHistoryByAccount` |
| `SuperchargerSessionsByVehicle` | `SuperchargerHistoryByVehicle` |
| `SuperchargerSessionsByVehicleBetween` | `SuperchargerHistoryByVehicleBetween` |
| `SuperchargerSessionsByVehicleUpdatedSince` | `SuperchargerHistoryByVehicleUpdatedSince` |

sqlc derives both the generated method and its params struct from the query name, so
`SuperchargerSessionsByAccountParams` → `SuperchargerHistoryByAccountParams` follows
automatically for all four `*Params` types (`UpsertSuperchargerHistoryParams` likewise).

**`History` singular, not `HistoryEntries` or `Histories`.** The four SELECTs return many rows,
so the plural `Sessions` was doing real work in the old names. `SuperchargerHistoryByAccount`
reads correctly anyway because "history" is a mass noun — "the Supercharger history for this
account" is already the whole set. Rejected: `SuperchargerHistoryEntriesByAccount` (longer, and
invents an "entry" noun that appears nowhere in the schema) and `SuperchargerHistoriesByAccount`
(wrong — it implies several separate histories).

Every call site is in `reader.go` (4) and `service.go` (1), all inside this module. Unlike the
`gen.go.rename` key-typo failure mode, **missing one is a compile error, not silent drift** —
`go build ./...` catches it.

### D5 — The hand-written domain type follows the table too

`internal/telemetry/telemetry.go` declares `type SuperchargerSession struct { … }` (currently
~line 427) — the module's own domain model, no vendor suffix (`ai/architecture.md` §6). It is
**not** sqlc-generated, so `gen.go.rename` does not reach it; it is renamed by hand to
`SuperchargerHistory`, with its doc comment updated to name the new table.

This is the type that crosses the module boundary — it is the element type of all four
`SuperchargerReader` method return slices — so renaming it is the one part of this tier with
consumers outside the module. There are exactly **7** such references (roadmap D26), all in
`_test.go` files, all enumerated in `tasks.md` as leader-owned. `go vet` compiles `_test.go`
files, so every one of them is caught by an assistant-runnable signal.

`mapping.go`'s `rowToSuperchargerSession` and `service.go`'s `upsertSuperchargerSession` change
their *signatures* (they now take/return `SuperchargerHistory`) but **keep their names** — both
are on tier 5's explicit list.

### D6 — The public port stays unchanged: an intentional half-state

After this tier `internal/telemetry` exposes a port named `SuperchargerReader`, whose four
methods are named `SuperchargerSessionsBy…`, returning `[]SuperchargerHistory`. **That reads
wrong, and it is correct.** Roadmap tier 5 (`RM39-telemetry-rename-supercharger-port`) owns the
port rename, split out because the surface has **88 references outside this module** — in
`charging`, `analytics`, `gateway`, `app` and both `cmd/` binaries — and folding it in would
make the roadmap's largest tier unreviewable while also putting a migration and a five-package
mechanical rename in one commit.

A reviewer of this change must not flag the mismatch as an omission, and no worker may "finish
the job". The rule for telling the two apart: **if the identifier is reachable from outside
`internal/telemetry`, it is tier 5's** — except `SuperchargerSession` → `SuperchargerHistory`
itself, which is this tier's because it is the type the *table* rename is about and because its
external footprint is 7 test-only references rather than 88.

### D7 — Rename scope is derived from the CATALOG: 9 objects

Roadmap D18 is binding, and this tier is the reason it exists. Tier 3 shipped, passed review
and archived with **five** constraints still named `charge_sessions_*`, because Postgres
auto-names one CHECK per inline column constraint as `<table>_<column>_check` and those names
exist **only** in `pg_constraint` — no grep over this repository can find them. The owner has
already run the catalog query for this tier against a migrated database. The result:

| # | Object | Kind | New name | Statement |
|---|---|---|---|---|
| 1 | `supercharger_sessions_pkey` | PRIMARY KEY on `id` | `supercharger_history_pkey` | `ALTER TABLE … RENAME CONSTRAINT` |
| 2 | `supercharger_sessions_session_id_unique` | UNIQUE `(session_id)` | `supercharger_history_session_id_unique` | `ALTER TABLE … RENAME CONSTRAINT` |
| 3 | `supercharger_sessions_battery_pct_source_check` | CHECK (auto-named) | `supercharger_history_battery_pct_source_check` | `ALTER TABLE … RENAME CONSTRAINT` |
| 4 | `supercharger_sessions_start_battery_pct_check` | CHECK (auto-named) | `supercharger_history_start_battery_pct_check` | `ALTER TABLE … RENAME CONSTRAINT` |
| 5 | `supercharger_sessions_end_battery_pct_check` | CHECK (auto-named) | `supercharger_history_end_battery_pct_check` | `ALTER TABLE … RENAME CONSTRAINT` |
| 6 | `supercharger_sessions_start_battery_pct_est_check` | CHECK (auto-named) | `supercharger_history_start_battery_pct_est_check` | `ALTER TABLE … RENAME CONSTRAINT` |
| 7 | `supercharger_sessions_end_battery_pct_est_check` | CHECK (auto-named) | `supercharger_history_end_battery_pct_est_check` | `ALTER TABLE … RENAME CONSTRAINT` |
| 8 | `idx_supercharger_sessions_vehicle_time` | index `(account_id, tesla_id, charge_start_date_time DESC)` | `idx_supercharger_history_vehicle_time` | `ALTER INDEX … RENAME TO` |
| 9 | `idx_supercharger_sessions_account_time` | index `(account_id, charge_start_date_time DESC)` | `idx_supercharger_history_account_time` | `ALTER INDEX … RENAME TO` |

**The pkey's and the unique constraint's BACKING indexes are NOT renamed separately.** In
PostgreSQL a constraint-backed index shares the constraint's name, and
`ALTER TABLE … RENAME CONSTRAINT` renames both together. Issuing a separate
`ALTER INDEX supercharger_sessions_pkey RENAME TO …` after the constraint rename would fail on
a name that no longer exists — a loud failure, but a needless one. Only the **two standalone**
`CREATE INDEX` objects (#8, #9) need `ALTER INDEX`.

**Why every one of them, not just the ones with consumers.** Nothing in Go, SQL or docs
references constraints #1–#7 by name. They are renamed anyway because a constraint name is
read in exactly one place — an error message, under pressure. A duplicate-key violation
printing `supercharger_sessions_session_id_unique` against a table the whole system calls
`supercharger_history` is precisely the stale-vocabulary confusion roadmap D5a exists to
remove, preserved in the worst possible location.

**Why an explicit list rather than a `pg_constraint` loop.** A literal statement fails loudly
if its object is absent; a pattern loop silently renames whatever it happens to match. Keep
the loud-failure property — but note the correction tier 3 paid for: **a literal list is silent
about objects it never knew existed.** The list above is therefore *derived from the catalog of
a migrated database*, not from reading the `CREATE TABLE` text. `tasks.md`'s verification task
re-runs that query after the migration and treats **any** surviving
`conname LIKE 'supercharger\_sessions%'` / `indexname LIKE '%supercharger_sessions%'` as a
failure — the negative assertion is the binding one, because it cannot go stale as the table
gains objects.

**Cost.** Nine catalog-only statements in Up, nine in Down. No table rewrite, no data touched,
no lock beyond the brief `ACCESS EXCLUSIVE` the migration already takes per statement.

### D8 — The new migration refreshes the `COMMENT ON` text, closing debt tier 3 left open

sqlc copies a table's and a column's Postgres comments into `models.go` as Go doc comments.
Four of telemetry's shipped comments will name things that no longer exist after this tier:

- `COMMENT ON TABLE supercharger_sessions` (set by `20260716000001`, replaced by
  `20260815000001`) — its text names the table and says "Owned by internal/telemetry; no other
  module reads this table directly", and it mentions "the nightly UPSERT".
- `COMMENT ON COLUMN supercharger_sessions.start_battery_pct`, `.start_battery_pct_est` and
  `.end_battery_pct_est` (set by `20260815000001`) — all three name
  **`UpsertSuperchargerSession`**, a query that ceases to exist under D4.

Tier 3 hit exactly this and left it: its final task records *"Did NOT touch
`internal/charging/db/models.go` (its two stale comments are sqlc-generated from the historic
migration's `COMMENT ON` statements — D1/D9 forbid editing that migration; left for the owner
to decide)."* That conclusion was half right. Editing the **historic** migration is forbidden;
issuing a **new** `COMMENT ON` in the **new** migration is not — a comment is catalog state
like a name, the later statement simply wins, and tier 3's own migration already did this for
two columns. This tier does it for the table and the three columns, so `models.go` comes out of
`make sqlc` with no stale reference and the owner is never asked to adjudicate generated text.

The refreshed text is otherwise **verbatim** — same wording, same `R3`/`D6` citations, same
NULL conventions — with only `supercharger_sessions` → `supercharger_history` and
`UpsertSuperchargerSession` → `UpsertSuperchargerHistory` substituted. Rewriting the prose is
out of scope; this is a name refresh, not an editorial pass.

`Down` does **not** restore the superseded comment text. That is this module's established
precedent, stated in `20260815000001`'s own Down section: *"Down intentionally does not restore
the pre-migration `COMMENT ON TABLE` text — goose Down migrations in this module have never
restored superseded comments."* Down must still rename the objects and move the tables back;
only the comment text is left at its newest version, which is harmless because a comment
constrains nothing.

### D9 — Raw SQL in `_test.go` files (roadmap D9/D13/D17): 41 statements, 7 files, plus 7 outside the module

sqlc never parses a Go string, and `go vet` compiles the test while treating its SQL as an
opaque string. **No signal available to this assistant catches a missed statement** — the
owner's suite is the only one that does. That makes this a mandatory step, not a nice-to-have.

Re-measured independently with the roadmap's quote-agnostic pattern (match TABLE NAMES, never
an opening quote — a quote-anchored pattern sees only double-quoted single-line SQL and misses
every backtick multi-line string, which is how tier 2's first count came out 5 instead of 18):

```
grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(vehicle_snapshots|supercharger_sessions|poll_attempts|poll_runs)\b' \
  --include='*_test.go' internal/telemetry
```

**Result: 41 statements across 7 files — the roadmap's number, confirmed exactly.**

| File | `vehicle_snapshots` | `supercharger_sessions` | `poll_attempts` | `poll_runs` |
|---|---|---|---|---|
| `db_integration_test.go` | 1 | — | 5 | — |
| `db_poll_run_integration_test.go` | — | — | — | 5 |
| `db_preceding_snapshot_integration_test.go` | 1 | — | — | — |
| `db_sourcea_integration_test.go` | 2 | — | — | — |
| `db_supercharger_battery_pct_integration_test.go` | — | 17 | — | — |
| `db_supercharger_between_integration_test.go` | — | 3 | — | — |
| `db_supercharger_integration_test.go` | — | 7 | — | — |

Every reference gains `telemetry.`; every `supercharger_sessions` reference **also** takes the
new name. Separately, the same files carry `telemetry.SuperchargerSession` / `SuperchargerSession`
Go type references that must become `SuperchargerHistory` — those are compile-checked by
`go vet`, unlike the SQL strings.

**Outside `internal/telemetry` — roadmap D17: a rename escapes the module sandbox where a
pure move would not.** Measured with the same pattern across every other module:

| File | What | Count |
|---|---|---|
| `internal/analytics/db_integration_test.go` | raw SQL: `INSERT INTO vehicle_snapshots` (`seedSnapshot`), `INSERT INTO supercharger_sessions` (`seedSuperchargerSession`), `UPDATE supercharger_sessions` (`reviseSuperchargerSession`), `UPDATE vehicle_snapshots` (same-day-recapture simulation) | **4** |
| `internal/analytics/db_integration_test.go` | Go: `telemetry.SuperchargerSession` type refs | **3** |
| `internal/app/processor_test.go` | Go: `telemetry.SuperchargerSession` in `fakeSuperchargerReader`'s four methods | **4** |
| `internal/charging/db_backfill_integration_test.go` | raw SQL seeding/cleaning/reading **telemetry's** table (`insertSuperchargerSessionFixture`, `cleanupSuperchargerSessions`, the source-row read-back) | **3** |

**The roadmap under-counted analytics.** Roadmap D17 names
`internal/analytics/db_integration_test.go` L415/L443 — the two `supercharger_sessions`
statements. The two `vehicle_snapshots` statements (`seedSnapshot`'s INSERT and the same-day
recapture `UPDATE`) are equally affected by the schema move and are equally invisible to every
runnable signal. The real number is **4**, not 2. Recorded here rather than silently fixed.

`internal/charging` additionally carries `telemetry.SuperchargerSession` / telemetry-table
mentions in **comments only** (`charging.go`, `session_reader.go`, `testdb_test.go`,
`db_backfill_integration_test.go`'s header) — a sweep-for-accuracy, enumerated in `tasks.md`.

**Migration files stay bare.** Every historic migration ran before the schema move and must
keep resolving through `search_path` to `public`. This is roadmap D9's own rule and it is
absolute here.

### D10 — Roadmap D12's `search_path` fix is FORBIDDEN in this tier

Roadmap D12 resolved a replayed-migration test in tier 2 by opening that one test connection
with `?search_path=public,<module>`, so a bare name in a historic migration resolves wherever
the table now lives. **That fix is unsafe here**, and the roadmap says so as the D25 corollary.

Tier 4 does not only move; it **renames**, and tier 3 already reused the freed base name for
`charging.supercharger_sessions`. A bare `supercharger_sessions` resolved through any
`search_path` that includes `charging` would find *charging's* table — a different table, with
a different row population — and the statement would succeed while reading the wrong data.
Silent wrongness, not a loud failure. **Qualify explicitly everywhere; never reach for D12.**

**Checked: the replay pattern is not present in `internal/telemetry`.**

```
grep -rn "ApplyVersion\|goose\.Provider\|goose\.NewProvider\|search_path" internal/telemetry/
```

returns nothing. `internal/telemetry/testdb_test.go` uses the ordinary
`testdb.Provision(ctx, fsys)` form over its own `//go:embed db/migrations/*.sql` — forward,
in-order, whole-directory provisioning.

**Checked: `internal/analytics`' two replay tests are unaffected.**
`db_watermark_migration_integration_test.go` and
`db_watermark_supercharger_migration_integration_test.go` drive a `goose.Provider` scoped to
`os.DirFS("db/migrations")` — **analytics'** own directory — replaying migrations that touch
only `vehicle_metric_watermarks`. Neither replays a telemetry migration, and neither statement
names a telemetry table, so their `search_path` DSN is untouched by this tier. Verified by
inspection; recorded so tier 5 and any later reader need not re-derive it.

### D11 — NEW: charging's backfill replay test must map THREE names forward, not one

**This is not in the roadmap and it is the single most likely way this tier ships broken.**

`internal/charging/db_backfill_integration_test.go` proves the one-time backfill inside the
historic `20260823000001` migration by extracting the shipped statement at runtime between
`-- BACKFILL-BEGIN` / `-- BACKFILL-END` sentinels and re-executing that exact text against a
database `TestMain` has already seeded. Because historic migrations are never edited, the
extracted text names tables as they were when it ran, so `runBackfill` rewrites names forward
before executing. Today it rewrites exactly one:

```go
insertTargetOld = "INSERT INTO charge_sessions ("
insertTargetNew = "INSERT INTO charging.supercharger_sessions ("
```

and its comment states, correctly for today: *"ONLY the INSERT target is rewritten. The
statement's `FROM supercharger_sessions` reads telemetry's table, which is still
`public.supercharger_sessions` until RM39 tier 4 moves it — so the bare name there is already
correct and must be left alone."*

This tier invalidates that. The extracted statement contains **three** references to
telemetry's table, and after this migration all three are wrong:

1. `IF to_regclass('public.supercharger_sessions') IS NULL THEN … RETURN;` — the guard.
2. `FROM supercharger_sessions s` — the source of the copy.
3. (unchanged) the `INSERT INTO charge_sessions (` target, already rewritten today.

**The guard is the dangerous one.** If only the `FROM` is fixed, the guard still evaluates
`to_regclass('public.supercharger_sessions')`, which is `NULL` forever after this tier, so the
`DO` block `RETURN`s early. No error is raised — the block emits a `NOTICE` and succeeds. The
backfill inserts nothing, and A1/A2 (`TestBackfill_RealFourRowDataset_OnePercentageBearing`,
`TestBackfill_IdempotentOnRerun_OverwritesNothing`) fail on *empty assertions* — a confusing
failure that reads like a data problem rather than a name problem. The fix is to add two more
forward mappings, each guarded by the same "exactly one occurrence, else `t.Fatalf`" check the
existing one uses, so a future change of the migration's shape fails loudly:

| Old text | New text |
|---|---|
| `to_regclass('public.supercharger_sessions')` | `to_regclass('telemetry.supercharger_history')` |
| `FROM supercharger_sessions s` | `FROM telemetry.supercharger_history s` |

The `NOTICE` string inside the guard also names the table; it is prose inside a `RAISE`, so
rewriting it is optional and **not** required — leaving it avoids a fourth fragile
string-match. The file's own header comment explaining the one-mapping rule must be rewritten
to explain the three-mapping rule, including why `search_path` is still the wrong tool here
(D10) — the existing comment already makes that argument and it survives this tier intact.

The three seed/clean/read helpers in the same file (`insertSuperchargerSessionFixture`'s
`INSERT INTO supercharger_sessions`, `cleanupSuperchargerSessions`'s `DELETE FROM …`, and the
source-row read-back's `FROM supercharger_sessions`) are ordinary D9 work and become
`telemetry.supercharger_history`.

`internal/charging/testdb_test.go`'s `ProvisionDirs(ctx, "../telemetry/db/migrations",
"db/migrations")` call needs **no change** — it takes directories, not names, and the ordering
it depends on is unchanged.

### D12 — Roadmap D25's comment correction, and two more the roadmap did not name

`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` is a shipped
migration. **Roadmap D1 forbids editing its SQL. This tier edits comments only.**

The one roadmap D25 names, at ~line 213:

> `--     the guard returns first. In a real database the guard always passes: MIGRATIONS_DIRS`
> `--     runs telemetry before charging.`

After this tier that is false in every database: `to_regclass('public.supercharger_sessions')`
is `NULL` permanently, so the guard *always* skips. The corrected comment must say so and say
why it is harmless — on any database that already ran this migration the backfill has long
since committed its rows, and on a fresh database there is nothing to copy because telemetry's
table is created empty in the same `goose up`.

**Two more comments in the same file become false and the roadmap did not name them** (found
by inspection during this design; recorded rather than silently fixed). Both are in the
`-- +goose Down` header, ~lines 277 and 284:

> `-- and every column dropped here still exists in telemetry.supercharger_sessions,`
> `-- is_paid — stay in supercharger_sessions permanently, so they are never at risk.`

They name `telemetry.supercharger_sessions`, which ceases to exist. They are pure prose about
data safety, so the correction is a name substitution to `telemetry.supercharger_history`. The
leader may fold them into the same comment-only task or defer them; **either is defensible, but
choosing silently is not** — whichever is chosen must be recorded, because "I did not think
about it" and "it is unaffected" are different findings and only the second one is a finding
(`CLAUDE.md`, docs rule).

This whole task is **leader-owned**: `internal/charging` is not this worker's sandbox.

### D13 — Index Plan

**Claim: `ALTER TABLE … SET SCHEMA`, `ALTER TABLE … RENAME TO`, `ALTER INDEX … RENAME TO` and
`ALTER TABLE … RENAME CONSTRAINT` are all catalog-only.** They update `pg_class.relnamespace`,
`pg_class.relname` and `pg_constraint.conname`; they never touch a heap or index page. Every
object that references a table by OID — each remaining constraint, each index, the
`gen_random_uuid()` default — keeps referencing the same OID.

**No index is added by this tier, and none is needed.** This is the explicit statement the
project's DB rule requires, with its justification against the read-heavy profile
(`CLAUDE.md` §Performance-Profile, `ai/architecture.md` §7): the migration changes **zero query
predicates, zero access patterns, zero column sets and zero row volume**. Every read this
module serves keeps its exact plan. An index added here would be an index added for no measured
read — the opposite of the profile's instruction, which is to index aggressively *for a read
that exists*.

Two indexes are **renamed**, and a rename is not a rebuild:

| Object | Before | After | Preserved because |
|---|---|---|---|
| standalone index | `idx_supercharger_sessions_vehicle_time (account_id, tesla_id, charge_start_date_time DESC)` | `idx_supercharger_history_vehicle_time` | catalog-only; same physical B-tree, same OID, same column order and direction |
| standalone index | `idx_supercharger_sessions_account_time (account_id, charge_start_date_time DESC)` | `idx_supercharger_history_account_time` | same |
| PK / UNIQUE backing indexes | `supercharger_sessions_pkey`, `…_session_id_unique` | follow their constraint | renamed *by* `RENAME CONSTRAINT`; never renamed separately (D7) |
| `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` | — | unchanged name | table is moved, not renamed; index name carries no schema |
| `vehicle_snapshots_account_tesla_date_unique`, `vehicle_snapshots_pkey`, `poll_attempts_pkey`, `poll_runs_pkey` | — | unchanged | same |
| every row in all four tables | — | unchanged | the migration contains no `INSERT`/`UPDATE`/`DELETE` |

**Read-path-by-read-path, the plan is identical.** `LatestSnapshotsByAccount`'s
`DISTINCT ON (tesla_id)` index scan; `SnapshotsByVehicleSince` / `…Between`'s forward range
scan on `idx_vehicle_snapshots_vehicle_time`; `SnapshotPrecedingDay`'s backward scan off the
same index's two leading equality columns; `SnapshotsByVehicleUpdatedSince`'s residual filter
within that scan; `SuperchargerHistoryByAccount`/`ByVehicle`'s sortless DESC scans on the two
renamed indexes; `…ByVehicleBetween`/`…ByVehicleUpdatedSince`'s documented residual filters —
all resolve to the same OIDs with the same statistics. Nothing in `EXPLAIN` changes except the
two index **names**, and no telemetry test asserts on `EXPLAIN` text (tier 3 had one such
assertion in `internal/charging`; this module has none — verified by grep for `EXPLAIN` under
`internal/telemetry`, see Test Contract point 6).

**No cross-table foreign key exists** on any of the four tables — `account_id` and `tesla_id`
are plain columns, deliberately (`ai/architecture.md` §2, and the `20260716000001` migration's
own header). So there is no FK-to-a-renamed-table case to reason about, in either direction.

**Lock consideration.** Each statement takes a brief `ACCESS EXCLUSIVE` lock for its catalog
update — metadata only, no page rewrite. Fifteen short statements run once, during a deploy's
migration step, on a database whose largest table held 174 rows when roadmap D24 was written.

**Rejected alternative:** `CREATE TABLE telemetry.supercharger_history AS SELECT * FROM
public.supercharger_sessions` plus manually re-adding every constraint and index, then
`DROP TABLE`. Strictly worse for the reasons tiers 1–3 already recorded — an explicit
index/constraint rebuild step, a window with two copies of the data, a follow-up `DROP`, and it
loses the `id` column's default and every comment.

### D14 — Migration filename

Latest migration timestamp across **all** module migration directories at the time this design
was written, checked by listing every directory rather than assumed:
`20260902000004_migrate_vehicle_metric_watermarks_source_supercharger.sql`
(`internal/analytics`, roadmap tier 3b). This module's own latest is
`20260830000002_add_poll_runs.sql`. **`20260903000001` collides with neither.**

`make migration-guard` fails on a duplicate version number **across modules** (they share one
`goose_db_version` table), so the timestamp must be re-verified against every module's
directory immediately before the file is created — another tier may have claimed a number in
between. The guard is Claude-runnable and is listed in `tasks.md` as an acceptance check.

### D15 — Docs and knowledge base

`CLAUDE.md`'s docs rule makes a doc update part of *this* change, never a follow-up, for any
change that alters a module's structure or public surface — and it names `kkpa/context/`
explicitly as the doc most often forgotten and most expensive to leave wrong, because
`kkpa-context-fetch` presents it as authoritative.

**A roadmap correction, stated plainly.** The roadmap's §"Knowledge-base debt" says
`kkpa/context/architecture/telemetry-data-hub.md` is already wrong and must be corrected and
retitled away from "data hub". **That file no longer exists.** Tier 3 already did the work: the
guide is now `kkpa/context/architecture/telemetry-ingest-only.md`, titled "Telemetry is
ingest-only", with the corrected module-split table and the gateway-consumer error fixed. What
remains for this tier is the explicit **PENDING banner** at the top of that file, which parks
the `telemetry.supercharger_sessions` → `supercharger_history` half of the rename on "a
separate, blocked boundary ticket (roadmap D6)". Roadmap D25 retired that block, so this tier
resolves the banner and the mentions it guards. No retitling is needed.

The full grep-verified file list is in `tasks.md`. Everything outside `internal/telemetry/` is
recorded as leader-owned or leader-granted; this worker's sandbox does not include the root
`README.md`, `ai/`, `docs/` or `kkpa/`.

## Test Contract (authored before implementation, per `ai/go-conventions.md` §Testing)

Written now, from the design, so that the tests this tier relies on assert what the design
*specifies* rather than what an implementation turns out to do. This tier adds **no new
`_test.go` file**: `internal/testdb` already provisions from this migrations directory, so any
pre-existing test that fails to run against the new schema and name **is itself the signal**.
That is the same reasoning tiers 1–3 used and it held.

**1. `internal/telemetry/db/models.go` after `make sqlc` — an exact expected diff, not a "no
diff" claim.**
`PollAttempt`, `PollRun` and `VehicleSnapshot`: struct bodies **byte-identical** — same type
name, same field names, same field types, same order. `SuperchargerSession` **entirely
replaced** by a struct named `SuperchargerHistory` with an **identical** field list, types and
order (`ID uuid.UUID`, `SessionID int64`, `AccountID uuid.UUID`, `Vin string`,
`TeslaID pgtype.Int8`, … `EndBatteryPctEst pgtype.Int2`). `git diff
internal/telemetry/db/models.go` MUST show a type-identifier rename plus the D8 comment
refresh, and **zero field-level changes**.

A `TelemetrySuperchargerHistory`, `TelemetryPollAttempt`, `TelemetryPollRun` or
`TelemetryVehicleSnapshot` in the output means a `rename` key did not match (D3) — exit 0 and
no error is the documented failure mode, so this diff is the only detector.

**2. Catalog resolution after `make migrate-up`.**
```sql
SELECT to_regclass('telemetry.vehicle_snapshots'),
       to_regclass('telemetry.supercharger_history'),
       to_regclass('telemetry.poll_attempts'),
       to_regclass('telemetry.poll_runs');
```
Expected: all four non-`NULL`.
```sql
SELECT to_regclass('public.vehicle_snapshots'),
       to_regclass('public.supercharger_sessions'),
       to_regclass('public.poll_attempts'),
       to_regclass('public.poll_runs'),
       to_regclass('telemetry.supercharger_sessions');
```
Expected: **all five `NULL`** — neither the old location nor the old name under the new schema
resolves.
```sql
SELECT to_regclass('charging.supercharger_sessions');
```
Expected: **non-`NULL`, unchanged.** Charging's table, renamed into that name by tier 3, is
untouched by this tier. This assertion exists because the two names are one word apart and the
D10 hazard is precisely that they can be confused.

**3. Renamed catalog objects resolve under their new names — the NEGATIVE assertion binds.**
```sql
SELECT conname FROM pg_constraint
WHERE conrelid = 'telemetry.supercharger_history'::regclass ORDER BY conname;
```
Expected set, exactly: `supercharger_history_battery_pct_source_check`,
`supercharger_history_end_battery_pct_check`,
`supercharger_history_end_battery_pct_est_check`, `supercharger_history_pkey`,
`supercharger_history_session_id_unique`, `supercharger_history_start_battery_pct_check`,
`supercharger_history_start_battery_pct_est_check`.
```sql
SELECT indexname FROM pg_indexes
WHERE schemaname = 'telemetry' AND tablename = 'supercharger_history' ORDER BY indexname;
```
Expected: `idx_supercharger_history_account_time`, `idx_supercharger_history_vehicle_time`,
`supercharger_history_pkey`, `supercharger_history_session_id_unique`.
```sql
SELECT conname FROM pg_constraint WHERE conname LIKE 'supercharger\_sessions%'
UNION ALL
SELECT indexname FROM pg_indexes WHERE indexname LIKE '%supercharger\_sessions%'
                                   AND schemaname = 'telemetry';
```
Expected: **zero rows.** This is the binding assertion (roadmap D18): it cannot go stale as the
table gains objects, and it is the check tier 3 did not run before archiving.

**4. Row counts are byte-identical across the migration.** For each of the four tables, the
count taken against `telemetry.<new name>` immediately after `make migrate-up` MUST equal the
count taken against `public.<old name>` immediately before it. Manual verification, not a new
automated test — roadmap D23 already proved this class of migration on the owner's live data.

**5. Down round-trips.** `goose down` (one step) restores all four tables to `public`, restores
the `supercharger_sessions` name, restores all nine object names, and drops the `telemetry`
schema — without `CASCADE`. Re-running `goose up` afterwards lands the same catalog state as
point 3. This is what makes the D1 statement-order requirement checkable rather than
aspirational.

**6. The existing suites pass with ZERO assertion-value changes.** Every test in
`internal/telemetry`, `internal/analytics`, `internal/charging` and `internal/app` MUST
continue to pass with its **exact same expected values**. This tier changes names, not
behavior. Two consequences worth stating because they are what a reviewer will look for:

- **`internal/telemetry` contains no `EXPLAIN`-text assertion naming a renamed index** —
  unlike tier 3, which had one in `internal/charging`. **Corrected at the design gate (leader,
  2026-09-03): the `EXPLAIN` grep is NOT clean — it returns hits, including a real assertion in
  `db_preceding_snapshot_integration_test.go` (`TestReader_SnapshotPrecedingDay_UsesIndexBackwardScan`).
  The substantive claim still holds: that test asserts `Index Scan Backward`,
  `idx_vehicle_snapshots_vehicle_time` and a negative `Seq Scan`, and none of those names a
  renamed object — `vehicle_snapshots` is MOVED, not renamed, so its index name is untouched.
  But its embedded `FROM vehicle_snapshots` is one of D9's 41 statements and must be
  schema-qualified.** The binding check is therefore not "the grep is clean" but: no `EXPLAIN`
  assertion names `idx_supercharger_sessions_*`. A hit that did would be a genuine
  expected-**value** change and must be updated, not treated as a regression. `tasks.md` T5.3
  carries this corrected form.
- **`internal/charging`'s A1/A2 backfill tests must still pass with their existing expected
  values**, which is only possible once D11's three forward mappings are all in place. A1/A2
  failing on *empty* results — rather than on a missing relation — is the specific signature of
  a missed `to_regclass` guard mapping.

**7. `internal/analytics`' two watermark replay tests are unaffected** (D10) and must pass
unchanged, with no edit to their `search_path` DSN. Their provider is scoped to analytics' own
migrations directory and replays statements naming only `vehicle_metric_watermarks`.

**8. `go build ./...`, `go vet ./...`, `gofmt -l` are clean repo-wide, and
`make migration-guard` passes.** `go vet` compiles `_test.go` files, so it is the signal that
catches every `telemetry.SuperchargerSession` reference this tier renames — all 7 outside the
module and all of them inside it. It catches **none** of the raw SQL work in D9, which is
exactly why D9 is a task with its own grep-based acceptance criterion rather than a build step.

## Makefile / tooling re-check (per `CLAUDE.md`'s "reverse direction" docs rule)

The obligation is to *check* and record a finding, not to assume. Checked for this tier:

| Checked | Result |
|---|---|
| `MIGRATIONS_DIRS` order and membership | `account → telemetry → charging → analytics`; unchanged. This tier adds one file to an already-listed directory, no new directory. |
| `db-setup` / `db-reset` role-and-ownership assumptions | Unchanged. Migrations run as `APP_ROLE`, so `CREATE SCHEMA telemetry` inside a migration makes the app role the schema **owner** — no `GRANT`, no `search_path` change needed. |
| `make migration-guard` | Still required (roadmap D4) and still relevant: it keys on version numbers across directories, independent of schemas. `20260903000001` was checked for global uniqueness across every module directory, not only this module's. |
| Other guards (`ui`, `i18n`, `money`, `tz`, `boundary`) | Schema-agnostic — grep/Go over source, never over SQL catalogs. `boundary-guard` in particular greps `internal/gateway/**` for the `internal/telemetry` **import path**, which this tier does not touch. |
| `sqlc.yaml` structure | The existing telemetry `sql:` entry is edited **in place** (a `rename:` map added under its `gen.go`); no new entry, no new `out:` path. |
| `internal/testdb` | No change. `telemetry` uses `Provision(ctx, fsys)` over its own `//go:embed db/migrations/*.sql`; `analytics` and `charging` use `ProvisionDirs` with **directory** arguments, which this tier does not rename or move. |

**Finding recorded:** nothing in the build tooling requires a change for this tier. That is a
checked result, not an assumption — the roadmap's own §"The Makefile needs no changes" audit
found zero `schema` / `public` / `GRANT` / `search_path` occurrences in the `Makefile`, and
this tier's scoped re-check confirms nothing in `telemetry` invalidates it.

## Risks

- **A silently-wrong `gen.go.rename` key on an irregular noun (D3).** Highest-probability
  failure, and the one with no error message. Mitigated by making the `models.go` diff a task
  with an explicit expected shape, and by naming the fallback key to try.
- **A missed raw SQL statement in a `_test.go` file (D9).** No assistant-runnable signal. The
  grep is the acceptance criterion, re-run after editing and required to return zero matches.
- **A missed `to_regclass` guard mapping in charging's backfill replay (D11).** Fails as empty
  assertions rather than a missing relation, so it reads like a data bug. Mitigated by the
  "exactly one occurrence, else `t.Fatalf`" guard on each mapping and by Test Contract point 6.
- **A surviving catalog object under the old name (D7).** Invisible to every grep. Mitigated by
  the negative catalog assertion in Test Contract point 3, which tier 3 did not run.
- **Confusing `telemetry.supercharger_history` with `charging.supercharger_sessions`.** One word
  apart, adjacent in every doc. Mitigated by Test Contract point 2's explicit
  `charging.supercharger_sessions` **must still exist** assertion, and by D10's ban on
  `search_path`.
