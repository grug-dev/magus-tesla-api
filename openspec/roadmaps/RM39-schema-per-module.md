# RM39 — One Postgres Schema per Module

Source ticket: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module

## Intention

Every `internal/` module that owns persistence gets its own Postgres schema, named after
the module. `account.accounts`, `telemetry.vehicle_snapshots`, `charging.charge_sessions`,
`analytics.vehicle_metrics`. The modular-monolith boundary — today enforced only by Go
import guards and convention — becomes visible in the database and checkable at codegen
time.

## Status

**IN PROGRESS.** Tiers 1 (`account`), 2 (`analytics`), 3 (`charging`) and 3b
(`analytics` watermark vocabulary) are archived. The owner-run `db-reset` stopper gate was
**waived** on 2026-09-03 (D24), and D6 — which parked tiers 4 and 5 behind an external
boundary ticket — was **superseded** on the same day (D25) after that ticket was found never
to have existed. **Tier 4 (`telemetry`) is the current unblocked work**; tier 5 follows it.

Scope grew on 2026-09-02: the owner reopened D5 and kept MAG-31's table renames, in a
different form (D5a/D5b/D5c). That added tier 5 and reshaped tiers 3 and 4.

## Findings that shaped this roadmap (read before touching anything)

All four were established by running the real toolchain, not by reasoning. They are the
reason the design looks the way it does.

| Finding | Evidence | Consequence |
|---|---|---|
| sqlc **tracks** `ALTER TABLE … SET SCHEMA` | `sqlc v1.31.1`, scratch repro: two migrations, second one moves the table; generated model became `TelemetryVehicleSnapshot` | Additive migrations are viable — **D1** |
| sqlc **rejects bare table names** once a table leaves `public` | same repro, bare `SELECT * FROM vehicle_snapshots` → `relation "vehicle_snapshots" does not exist`, exit 1 | Schema-qualifying every query is **forced, not chosen** — **D2** |
| Schema-qualifying **renames every generated struct** | `telemetry.vehicle_snapshots` → `TelemetryVehicleSnapshot` | Without a fix, ~144 non-test Go call sites churn — **D3** |
| `gen.go.rename` needs the **singularized** key | `telemetry_vehicle_snapshots` → ignored; `telemetry_vehicle_snapshot` → works | The obvious key form silently does nothing — **D3** |
| Raw SQL in `_test.go` breaks, and **no Claude-runnable signal catches it** | tier 1: query.sql fully qualified, build/vet/gofmt/guards all clean, yet 10 account integration tests failed on `relation "accounts" does not exist` | Every tier must qualify its tests' hand-written SQL — step 2b of the work shape |

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

**D3 — `gen.go.rename` keeps every Go type name unchanged *through the schema move*.**
(D5c is the deliberate exception: two names change because the owner asked for them to.) The schema move
alone would rename all 12 generated structs. Each tier adds `rename` entries under its own
`sqlc.yaml` entry (`gen.go.rename`, **not** the top-level `overrides:` block, which is
ignored here) so the public Go surface does not move at all. Type names that MUST survive
unchanged:

| Module | Types that must not change |
|---|---|
| `account` | `Account`, `TeslaToken`, `Vehicle` |
| `telemetry` | `PollAttempt`, `PollRun`, `VehicleSnapshot` — but **not** `SuperchargerSession`, which D5c deliberately renames |
| `charging` | `ManualChargeEntry` — but **not** `ChargeSession`, which D5c deliberately renames |
| `analytics` | `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark` |

Key form is `<schema>_<singular table>` — e.g. `telemetry_vehicle_snapshot: "VehicleSnapshot"`.

**D4 — goose is unchanged.** The shared `public.goose_db_version` table stays exactly as it
is. The Makefile passes no `-table` flag, so goose's unqualified bookkeeping table keeps
resolving through `search_path` to `public`; goose stores version numbers, not table names,
so every applied record stays valid. **No `db-reset` is needed anywhere in this roadmap.**
Consequence, stated plainly: `make migration-guard` is still required and is NOT retired by
this roadmap — version collisions come from goose keying by version number across dirs,
which is independent of table schemas. Follow-up filed as backlog #23.

**D5a — `telemetry.supercharger_sessions` → `telemetry.supercharger_history`.** MAG-31
originally proposed a `fleet_` prefix on both telemetry tables. The prefix is dropped, but
the rename is kept and sharpened. The owner's real problem was that *two* tables were both
called `*_sessions`, one mirroring the other. A prefix leaves both called "sessions" and adds
a word; changing the **suffix** removes the collision instead, and the schema name already
carries the namespace the prefix would have added.

`_history` rather than `_snapshots` — a correctness point, not taste. `vehicle_snapshots` is
append-only, one row per poll. This table is **upserted**: rows change as Tesla settles fees
after a session ends. Calling both "snapshots" teaches an agent a false rule and invites a
query that double-counts. `_history` also names the Fleet endpoint the rows come from
(`dx/charging/history`).

**D5b — `charging.charge_sessions` → `charging.supercharger_sessions`.** The old name
over-claims. That migration's own header states the table is a **dense mirror**,
Supercharger-only, one that "originates no value" — yet `charge_sessions` reads as *all*
charging. The distinction is load-bearing: `internal/analytics` reads this table and
`manual_charge_entries` as two independent watermark sources. The new pair splits on
provenance: `supercharger_sessions` / `manual_charge_entries`.

This **reuses** the name D5a frees. Accepted with eyes open: 133 doc files and the frozen
archived specs still use `supercharger_sessions` to mean *telemetry's* table, so a grep across
history is era-ambiguous. The schema qualifier disambiguates everything written from here on.

Supporting evidence this is the right word: `internal/charging/charging.go` already declares
`SuperchargerSessionAnalyticsReader`, whose comment records "Supercharger" as the owner's
**deliberate divergence** (RM31 D8) chosen for readability at the analytics call sites. D5b
makes the table agree with a name the owner already picked — it removes a divergence rather
than creating one.

**D5c — Generated Go type names follow the tables; they are NOT frozen.** Deliberately the
opposite of D3. D3 freezes names because the schema move is pure namespacing, where a rename
would be churn for nothing. D5a/D5b are the reverse case: their entire purpose is to remove
stale vocabulary, so leaving Go on the old names would half-fix the confusion in exactly the
code that gets read most.

Note the consequence — `SuperchargerSession` is not renamed, it **moves** between modules:

| | before | after |
|---|---|---|
| `telemetry` domain type + db model | `SuperchargerSession` | `SuperchargerHistory` |
| `charging` db model | `ChargeSession` | `SuperchargerSession` |

Verified safe: `internal/charging` never imports `telemetry.SuperchargerSession` (only a
comment in `charging.go` mentions it), so the swap crosses **no import edge**, and the two
names live in different packages.

**D6 — Tier 4 (`telemetry`) is blocked on a separate boundary ticket. — SUPERSEDED by D25, see below.**
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

**D7 — Tier 3 moves the schema BEFORE it renames, inside one migration.** Not cosmetic. Tier
3 (`charging`) runs before tier 4 (`telemetry`), so at that point telemetry still holds
`public.supercharger_sessions`. Renaming charging's table while both sit in `public` collides.
The statement order that avoids it:

```sql
CREATE SCHEMA IF NOT EXISTS charging;
ALTER TABLE charge_sessions SET SCHEMA charging;
ALTER TABLE charging.charge_sessions RENAME TO supercharger_sessions;
```

`charging.supercharger_sessions` and `public.supercharger_sessions` then coexist until tier 4
moves telemetry. **Consequence worth stating plainly: this frees D5b from the D6 block.**
Charging's rename does not wait for tier 4.

**D8 — The rename DELETES the affected `vehicle_metric_watermarks` rows.** After D5a+D5b the
CHECK set becomes `('vehicle_snapshots','supercharger_sessions','manual_charge_entries')` —
byte-identical to the set that existed *before* migration `20260828000001`, back when
`'supercharger_sessions'` meant **telemetry's** table. Same stored string, two meanings
depending on era, and `analytics.Recalculator` keys its recompute cursor on that string.

So the migration deletes those rows rather than rewriting them. Safe by the table's own
documented contract: "No watermark row yet for a (account_id, tesla_id, source) means epoch —
Reconcile backfills the vehicle's full history in one pass." Self-healing, and data loss is
acceptable (MAG-31). Precedent for the CHECK swap itself:
`20260828000001_migrate_vehicle_metric_watermarks_source.sql` already performed this exact
operation once. **Copy that file; do not invent it.**

**D8 CORRECTED (2026-09-02, owner-confirmed) — this work is NOT tier 3's, and never was.**
`vehicle_metric_watermarks` belongs to **`analytics`**, which moved it into the `analytics`
schema in tier 2. Putting the DELETE and the CHECK rewrite in *charging's* migration directory
would have charging's migration mutating another module's table — the same class of violation as
D6, which already blocks tier 4. The precedent settles it: `20260828000001` was itself triggered
by a charging-side change (RM31) and still lives in `internal/analytics/db/migrations/`.

There is also a knock-on the original D8 missed: `internal/analytics/recalculate.go` holds
`sourceChargeSessions = "charge_sessions"`, the key `Recalculator` reads its cursor by. It must
change with the CHECK, and it is analytics' code.

So the work moves to its own analytics-owned tier, **3b**. Sequencing costs nothing:
`MIGRATIONS_DIRS` replays per directory (`account → telemetry → charging → analytics`), so any
analytics migration runs after charging's entire directory whatever its timestamp.

**D9 — Every tier also schema-qualifies the raw SQL in its `_test.go` files.** Not foreseen
when D1–D8 were written; learned from tier 1. `query.sql` was fully qualified and `go build`,
`go vet`, `gofmt`, `make migration-guard` and `make boundary-guard` were all clean — yet ten
`account` integration tests failed with `relation "accounts" does not exist`. Integration tests
hand-write their setup and assertions (`DELETE FROM …`, `UPDATE … SET …`, `SELECT count(*) FROM
…`); sqlc never parses those strings, and `go vet` compiles the test while treating the SQL as
an opaque string.

**No assistant-runnable signal catches this** — only the owner's suite does. So it is a
mandatory step (work shape 2b), not a nice-to-have. Migration files are the exception and stay
bare: they run before the schema move and must keep resolving through `search_path` to
`public`.

**D12 — A test that replays a historic migration out of order needs a `search_path` on its own
connection, never a migration edit.** Found in tier 2, not anticipated. `internal/analytics`'s
`db_watermark_migration_integration_test.go` drives `goose.Provider.ApplyVersion` to replay
migration `20260828000001` *after* `TestMain` has already applied every migration — including the
schema move. That historic file names the table bare, correctly so under D1/D9, and must not be
edited to "fix" it.

The resolution is scoped to the one test connection: open it with
`?search_path=public,<module>` so the bare name resolves wherever the table currently lives.
`public` stays first, so the same test still passes against a database that has not yet applied
the schema move. Tiers 3 and 4 must check their own modules for the same replay pattern before
assuming it does not apply.

**D13 — D9's grep pattern matches TABLE NAMES, never an opening quote.** The first form
(`'"[^"]*\b(FROM|…)'`) only sees double-quoted single-line SQL and silently misses every
backtick-delimited multi-line string. It predicted 5 statements for `analytics`; the real
number was 18, and it missed a whole test file. Re-measured with the quote-agnostic pattern:
`charging` 32 in 10 files, `telemetry` **41 in 7 files**.

**D17 — Work-shape step 2b covers OTHER modules' `_test.go` files, not just the renamed
module's.** Tier 3 renamed charging's table and broke two `analytics` tests that seed it by
bare name. A pure schema move stays inside the module sandbox; a **rename escapes it**. Tier 4
renames, so it must sweep `internal/charging` and `internal/analytics` too — concretely
`internal/charging/db_backfill_integration_test.go` L158/187/307 and
`internal/analytics/db_integration_test.go` L415/443.

**D18 — A rename tier's completeness criterion is the CATALOG, never a hand-written object
list.** Tier 3 shipped, passed review and archived with five constraints still named
`charge_sessions_*`. Postgres auto-names one CHECK per inline column constraint, and those
names exist **only in the catalog** — no grep over the repo can find them. Tier 4 must query
`pg_constraint` / `pg_indexes` on a migrated database and rename every object still carrying
the old table name. For `supercharger_sessions` that is **9 objects: 7 constraints + 2
indexes**, measured, not listed from memory.

**D23 — RM39's data-preservation claim (D1) is proven on real data.** `make migrate-up`
applied tiers 2, 3 and 3b to the owner's live `magus` database — charging `20260902000003`
(23 ms), analytics `20260902000002` (5 ms) and `20260902000004` (6 ms) — and every row count
was byte-identical before and after. D8's "deliberate data loss" cost **zero**: the database
held no `source='charge_sessions'` watermark rows at all.

**D24 — The stopper gate (`make db-reset`) is WAIVED.** D23 already proved RM39 on real data,
so the wipe's only remaining effect was destroying irreplaceable history — 174
`vehicle_snapshots` back to 2026-07-13 and 12 hand-entered `manual_charge_entries`, neither
recoverable from Tesla. Cold-start testing moves to a throwaway/pre-prod database, and a fresh
`magus` database is built before the production deploy instead.

**D25 — D6 is SUPERSEDED: tier 4 is unblocked, and charging's backfill violation is fixed
inside tier 4.** No ticket ever covered D6's violation. MAG-31's only Linear `blockedBy` is
MAG-41 ("Fix boundary-guard"), which is Done — but MAG-41 was the *gateway → telemetry Go
import* violation fixed by RM38/RM40, an entirely different one. The backfill violation is
real and still present: `20260823000001` reads `public.supercharger_sessions` inside a
PL/pgSQL `DO` block, where neither `make boundary-guard` (Go imports only) nor sqlc (a
PL/pgSQL body is an opaque string) can see it. But its residual cost to tier 4 is small: **one
comment that becomes false**, plus test SQL that step 2b already owned. On a fresh database
the post-rename guard skips a backfill that would have copied zero rows. So tier 4 folds in
the comment correction — **comment only; D1 still forbids touching that migration's SQL**.

**D25 corollary — D12's `search_path` fix is UNSAFE for tier 4.** D12 resolves a
replayed-migration test by opening its connection with `?search_path=public,<module>`. That
works when the table only *moves*. Tier 4 also **renames**, so a bare `supercharger_sessions`
resolving through `search_path` would silently find *charging's* table — the name D5b reused —
instead of failing loudly. Tier 4 must qualify explicitly and must not reach for D12.


## Tiers

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM39-account-move-to-own-schema` | `account` | 3 tables: `accounts`, `tesla_tokens`, `vehicles`. No cross-module reads. The pilot tier — it establishes the pattern every later tier mirrors. | — | Create the OpenSpec change moving `internal/account`'s tables into an `account` schema. ONE new goose migration (`CREATE SCHEMA` + `ALTER TABLE … SET SCHEMA`), schema-qualify every table reference in `internal/account/db/query.sql`, and add `gen.go.rename` entries to `sqlc.yaml` so `Account`, `TeslaToken` and `Vehicle` keep their exact current names. Verify the generated `models.go` type names — a wrong rename key fails silently with exit 0. Follow D1–D4. The renames (D5a/D5b/D5c) do not touch this module. |
| `[x]` | `RM39-analytics-move-to-own-schema` | `analytics` | 3 tables: `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps`. Its `vehicle_snapshots` / `supercharger_sessions` mentions are watermark **string values** and comments, not table references — they must NOT be changed. | 1 | Mirror tier 1 for `internal/analytics`. Preserve `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark`. Take care: `vehicle_metric_watermarks.source` holds the literal strings `'vehicle_snapshots'`, `'supercharger_sessions'`, `'manual_charge_entries'` — these are data, and a CHECK constraint depends on them. Do not schema-qualify them. Note for later: tier 3 rewrites that CHECK and deletes these rows under D8 — leave both alone here. |
| `[x]` | `RM39-charging-move-to-own-schema` | `charging` | 2 tables: `charge_sessions`, `manual_charge_entries`. **Also renames `charge_sessions` → `supercharger_sessions` (D5b).** Its backfill migration still reads `public.supercharger_sessions` and still works here, because `telemetry` has not moved yet. | 1 | Mirror tier 1 for `internal/charging`, then apply D5b. **Statement order inside the one migration is mandatory (D7): CREATE SCHEMA → SET SCHEMA → RENAME TO.** Renaming before the schema move collides with telemetry's still-unmoved table. Rename the db model `ChargeSession` → `SuperchargerSession` (D5c) and the 2 sqlc query names (`MirrorChargeSession`, `VerifyChargeSession`). Preserve `ManualChargeEntry`. **Do NOT touch `vehicle_metric_watermarks` — that is tier 3b (D8 CORRECTED).** Rename EVERY catalog object carrying the old name — expanded from four to NINE after a pg_constraint query found five auto-named column CHECKs (D18). The completeness criterion is the catalog, not a list: the index `idx_charge_sessions_vehicle_stop`, the CHECK `charge_sessions_pct_source_required`, the primary key `charge_sessions_pkey`, and the unique constraint `charge_sessions_account_session_unique` — so a constraint violation names the table it came from. Update the RM31 D8 comment on `SuperchargerSessionAnalyticsReader` — its "deliberate divergence" note is no longer a divergence. Do NOT touch `20260823000001_add_charge_sessions.sql` — its cross-module read is tier 4's problem and is owned by a separate boundary ticket (D6). |
| `[x]` | `RM39-analytics-fix-watermark-vocabulary` | `analytics` | No schema move — that happened in tier 2. Rewrites the `vehicle_metric_watermarks` CHECK to the post-rename vocabulary and DELETEs the affected rows (D8 CORRECTED), plus the `sourceChargeSessions` constant in `recalculate.go`. Recorded as **tier 3b**; its id in the progress JSON is `6`, because tier ids are never renumbered. | 3 | Owner-confirmed split out of tier 3, because the table is analytics's and charging must not write it. Copy `internal/analytics/db/migrations/20260828000001_migrate_vehicle_metric_watermarks_source.sql` — it performed this exact CHECK swap once already; do not invent it. New CHECK set: `('vehicle_snapshots','supercharger_sessions','manual_charge_entries')`. DELETE the affected rows rather than rewriting them: the new set is byte-identical to the pre-`20260828000001` set, when `'supercharger_sessions'` meant telemetry's table, so a rewrite could silently map a cursor to the wrong era. A missing row means epoch by the table's own contract, so Reconcile self-heals in one pass. Also update `internal/analytics/recalculate.go`'s `sourceChargeSessions = "charge_sessions"` to the new value. |
| `[x]` | **— STOPPER GATE: owner resets the database — WAIVED 2026-09-03 —** | — | Not a change and not code. **WAIVED by the owner on 2026-09-03**, who will instead create a fresh `magus` database before deploying to prod. The gate's purpose — see RM39 fresh data on a clean database — was met another way: tiers 2/3/3b were applied to the live database with `make migrate-up` and preserved every row (roadmap D23), so a wipe would have destroyed 174 `vehicle_snapshots` going back to 2026-07-13 and 12 hand-entered `manual_charge_entries` — neither recoverable from Tesla — to buy an empty database RM39 no longer needed. Original text: a hard stop after tier 3b where the owner runs `make db-reset` to start testing on fresh data. See §"Stopper gate" below for the exact steps. The pipeline **must not** dispatch tier 4 until the owner confirms this gate is done or explicitly waives it. | 3 | HALT. Report that tiers 1–3 are archived and the gate is now the owner's to run. Do not run `make db-reset` yourself — it is destructive and owner-only per `CLAUDE.md` §"Builds & local checks". |
| `[x]` | `RM39-telemetry-move-to-own-schema` | `telemetry` | 4 tables: `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`, `poll_runs`. **Also renames `supercharger_sessions` → `supercharger_history` (D5a).** The largest tier and the one that trips the backfill guard. | 1, 3, 3b | **UNBLOCKED 2026-09-03 by D25** (D6 superseded; gate G1 waived by D24). Mirror tier 1 for `internal/telemetry`, preserving `PollAttempt`, `PollRun`, `VehicleSnapshot`, then apply D5a. Rename the db model and the hand-written domain type `SuperchargerSession` → `SuperchargerHistory` (D5c) — the domain type in `telemetry.go` is NOT sqlc-generated, so no config touches it. Rename the 5 sqlc query names (`UpsertSuperchargerSession`, `SuperchargerSessionsByAccount`/`ByVehicle`/`ByVehicleBetween`/`ByVehicleUpdatedSince`); their `*Params` types follow. **Keep the public port `SuperchargerReader` unchanged here — it is tier 5.** Then reconcile `charging`'s backfill tests A1/A2, which break the moment this tier applies. Re-check `internal/analytics`' `ProvisionDirs` usage too. **D18 binds: completeness is measured on the CATALOG (9 objects — 7 constraints + 2 indexes), not a hand-written list.** **D17 binds: step 2b crosses modules — 41 statements in telemetry's own tests, plus charging's backfill test and analytics' db integration test.** **D25 folds in** correcting `20260823000001`'s now-false comment (comment only). **D12's `search_path` fix is UNSAFE here** — see the D25 corollary. |
| `[x]` | `RM39-telemetry-rename-supercharger-port` | `telemetry` | No schema change. Renames telemetry's **public port** to finish D5c: `SuperchargerReader` -> `SuperchargerHistoryReader`, its 4 methods, `NewSuperchargerReader`, and the internal `superchargerReader` / `rowToSuperchargerSession` / `upsertSuperchargerSession` helpers. | 4 | **ARCHIVED 2026-09-03** (reviewer-approved, zero findings; owner-reported suite pass). **D31 CORRECTED THIS ROW'S SCOPE**: the "88 references outside `internal/telemetry`" figure was a name-collision artifact and is WRONG. The real cross-module surface is **4 files** -- `internal/app/{app.go,processor.go,processor_test.go}` and `cmd/poller/main.go`. `internal/gateway`'s `SuperchargerReader` field is `charging.SessionReader`-typed and unrelated (gateway cannot import telemetry -- `make boundary-guard`); `internal/analytics`' hits are its own local test fakes plus historical comments. **A blind grep-and-rename would corrupt working gateway code.** Purely mechanical and compiler-checked; no SQL, no migration. Verify with `go build ./... && go vet ./... && gofmt -l` plus a repo-wide grep of the old identifiers returning zero real hits (D18's catalog principle, applied to Go: the compiler is the authority, never a hand-written list). |

Tier order rationale: `account` first because it has zero cross-module entanglement, so it
proves the pattern cheaply. `telemetry` last because it is the only tier whose landing
breaks another module's tests. Tier 5 trails tier 4 because a public-port rename touching
five other packages should not share a commit with a migration.

Counts behind the tier sizes, measured on the live tree (archived specs and
`.claude/worktrees/` excluded), not estimated:

| Symbol / table | non-test Go | test Go | real SQL lines |
|---|---|---|---|
| `supercharger_sessions` (telemetry) | 50 | 146 | 82 |
| `charge_sessions` (charging) | 81 | 164 | 37 |
| `SuperchargerSession` | 80 | 64 | — |
| `ChargeSession` | 30 | 2 | — |
| telemetry public port, refs OUTSIDE the module | — | — | 88 total (tier 5) |

## Stopper gate — owner resets the database (after tier 3)

**Why it exists.** RM39 preserves every row by design: `ALTER TABLE … SET SCHEMA` and
`… RENAME TO` are catalog-only operations, and D1/D4 deliberately avoid needing a reset. But
the owner *wants* a wipe — a clean database to start testing the app on fresh data (MAG-31:
"It's OK if we lost data"). Since nothing in the roadmap produces that, it is an explicit
owner-run gate rather than a side effect.

**Placement: after tier 3, not at the end.** Tiers 1–3 are the unblocked run; tier 4 waits on
the D6 boundary ticket, which may be far off. Resetting after tier 3 gives a clean database
whose schema already carries the new `charging` names, without waiting. It also avoids
re-establishing the Tesla connection twice.

**Claude does not run this.** `make db-reset` drops the database. It is on the gated list in
`CLAUDE.md` §"Builds & local checks" — the owner runs it and reports the result.

### Steps (owner)

1. Confirm tiers 1–3 are archived and `make migrate-status` shows no pending migration.
2. `make db-reset` — drops the database, recreates it owned by `APP_ROLE`, and re-applies
   every migration directory in `MIGRATIONS_DIRS` order.
3. **Re-establish the Tesla connection.** `db-reset` drops `accounts` and `tesla_tokens`
   along with everything else, so the per-user Tesla link is gone. Reconnect through
   `cmd/web` (sign-in, then the Tesla OAuth consent) — see `docs/layer2-user-vehicle-access.md`.
   Note `make cmd-setup` is the **single-user `.env`** capture tool, not the multi-tenant path.
4. Run the poller once to repopulate, then verify the pages render.

### Replay is coherent — verified, not assumed

`db-reset` re-applies migrations **per directory**, in `MIGRATIONS_DIRS` order
(`account → telemetry → charging → analytics`), not merged by timestamp. Within charging's
directory the historic backfill `20260823000001` still runs *before* tier 3's migration, so at
that moment the table is still `public.charge_sessions` and the backfill's own name resolution
is unchanged. It copies 0 rows on an empty database, which is the harmless case D6 describes.

## Per-tier work shape (every tier does exactly this)

1. One new goose migration — `CREATE SCHEMA IF NOT EXISTS <module>` + one
   `ALTER TABLE … SET SCHEMA <module>` per owned table, with a real `-- +goose Down`.
   On tiers 3 and 4 the same migration also carries the D5a/D5b `RENAME TO`, and on
   tier 3 the statement order of D7 is mandatory.
2. Schema-qualify every table reference in that module's `db/query.sql`.
2b. **Schema-qualify the raw SQL in that module's `_test.go` files too.** Learned the hard
   way on tier 1: `query.sql` was fully qualified and `go build`, `go vet`, `gofmt` and both
   guards were all clean, yet 10 account integration tests failed with
   `relation "accounts" does not exist`. Integration tests hand-write `DELETE FROM …`,
   `UPDATE …` and `SELECT count(*) FROM …` for setup and assertions. sqlc never sees those
   strings, and `go vet` compiles the test but a SQL string is just a string to it — **no
   Claude-runnable signal catches this**. Only the owner's suite does. Find them with:

   ```
   grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(<table1>|<table2>)\b' --include='*_test.go' internal/<module>
   ```

   **Match on the TABLE NAMES, never on an opening quote.** Tier 2 proved why: a
   quote-anchored pattern (`'"[^"]*\b(FROM|…)'`) sees only double-quoted single-line SQL and
   silently misses every backtick-delimited multi-line string. It reported 5 statements for
   `analytics`; the real number was **18**, and it missed one whole test file.

   Counts re-measured with the quote-agnostic pattern: `analytics` 18 (done),
   `charging` **32** across 10 files, `telemetry` **41** across 7 files.
   **Migration files are the exception — never qualify those.** Every historic migration ran
   before the schema move and must keep resolving through `search_path` to `public`.
3. Add `gen.go.rename` entries to that module's `sqlc.yaml` entry (singular keys).
4. Run `sqlc generate`; **diff `models.go` and confirm no type name changed**.
5. `go build ./... && go vet ./... && gofmt -l` — the owner runs the suite (see
   Test-Execution-Policy in `CLAUDE.md`).
6. Update the module's `AGENTS.md` and any doc naming its tables. On tiers 3–5 this
   also means the KB under `kkpa/context/` — see "Knowledge-base debt" below.

## The Makefile needs no changes — audited, not assumed

A schema move usually drags the build tooling with it. Here it does not, and the reason is
worth recording so no tier re-derives it:

| Checked | Result |
|---|---|
| `schema` / `public` / `GRANT` / `search_path` in `Makefile` | **zero** occurrences |
| Bare table names in `Makefile` | **zero** (two hits are comments about `poll_runs`) |
| Guard scripts referencing table names | none — the guards are Go/grep over source, not SQL |
| Who applies migrations | `db-setup` exports `PGUSER=$(APP_ROLE)` and migrates a database **owned by that role** |

That last row is the load-bearing one: because migrations run **as the app role**, a
`CREATE SCHEMA <module>` inside a migration makes the app role the schema **owner**, so it
needs no `GRANT` and no `search_path` change. `db-reset`, `db-setup`, `migrate-up/down/status`
and all six guards are schema-agnostic and stay as they are.

Two caveats that *are* each tier's job:

- **Every tier's `-- +goose Down` must reverse in the opposite order** — rename back first,
  then `SET SCHEMA public`, then drop the schema. `make migrate-down` depends on it, and D7's
  ordering logic applies in reverse.
- **A pre-existing database migrated by a role other than the app role** would end up with
  schemas the app role cannot use. The Makefile already warns on that ownership mismatch in
  `db-setup`, and the stopper gate's `db-reset` eliminates it outright.

## Knowledge-base debt this roadmap must clear

`kkpa/context/architecture/telemetry-data-hub.md` is **already wrong**, independently of
this roadmap, and D5a/D5b would make it worse. Its "Consumers" table still lists the
gateway reading `telemetry.Reader` through `Deps.TelemetryReader` (`gateway.go`,
`handlers.go`, `history.go`, `charges.go`) and still says `cmd/web/main.go` injects two
telemetry readers into `gateway.Deps`. All of that was removed by **RM38** and **RM40** —
`internal/gateway/AGENTS.md` and `ai/architecture.md` §"Exception: the gateway may not
depend on `telemetry` at all" now forbid it, and `make boundary-guard` enforces it with
zero escape hatches.

The module split the KB must state, which the current file's own title ("Telemetry as the
data hub") contradicts:

| Module | Role | Who may read it |
|---|---|---|
| `telemetry` | **Ingest only.** Fetches the Fleet API and writes what it fetched. Nothing else. | `internal/analytics` (snapshots) and `internal/app/processor.go` (the Supercharger mirror). **Never the gateway.** |
| `charging` | Mirrors telemetry's Supercharger rows, **and** owns human-entered `manual_charge_entries` — so it is not a pure mirror. | gateway, analytics |
| `analytics` | Derived read model. Recomputes `vehicle_metrics` from three watermark sources. | gateway |

Tier 3 renames a table that file names throughout, so the correction lands there at the
latest. Retitle it away from "data hub" — telemetry is a source, not a hub.

## Future work

Recorded in `openspec/roadmaps/backlog.md`:

- **#23** — per-module goose version table, which would retire `make migration-guard` (D4).

The `charging` → `telemetry` backfill boundary violation (D6) is the owner's separate
ticket, not a backlog item.
