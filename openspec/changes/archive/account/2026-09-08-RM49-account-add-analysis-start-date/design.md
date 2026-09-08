## Context

The `account` module owns the `account` Postgres schema: `accounts`, `tesla_tokens`,
`vehicles`, and `settings` (one row per account, PK `account_id`, holding `language` and
`theme` — `internal/account/db/migrations/20260904000001_add_account_settings.sql`,
`RM42-account-add-settings-table`). `account.settings` is the platform's one settled home
for per-account policy (RM42 D3): a preference or a policy value never gets a second home
on another table.

MAG-55 (roadmap `RM49-analysis-start-date`) wants external charges rejected when they are
dated before the day the app started analyzing that account's vehicle. That day exists
today only implicitly, as `account.accounts.created_at`. This design gives it an explicit,
stored home: `account.settings.analysis_start_date`. This tier implements that storage and
the read port only. Tier 2 (`RM49-gateway-restrict-external-charge-date`, a separate,
dependent change) is the only consumer that rejects a charge — nothing in `internal/gateway`
or `internal/charging` changes here (roadmap D5).

Performance profile: **read-heavy** (`ai/architecture.md` §7). This value is read on every
save attempt on the future `/external-charges` page (tier 2) and, in this tier, alongside
`language`/`theme` in the same `GetAccountSettings` call `LanguageFor`/`ThemeFor`/
`PreferencesFor` already make. The roadmap requires the combined-read guarantee
`RM42` established (`PreferencesFor`, one query for every preference) to keep holding as this
row grows a third column (roadmap D4).

The direct precedent for adding a `NOT NULL` column to an existing table with data already in
it is `20260906000002_add_mirror_watermarks.sql` in `internal/charging`, and — more directly —
`RM42`'s own `20260904000001_add_account_settings.sql`, which added `account.settings`
itself. The direct precedent for a `DATE` column mapped through sqlc is
`charging.manual_charge_entries.charged_on` (`pgtype.Date`, converted via `.Time` on read and
a `dateFromTime` helper on write — `internal/charging/service.go`). Both precedents are read
in full before this design and carried forward here.

## Goals / Non-Goals

**Goals:**
- Give every account exactly one, explicit, stored analysis start date, so tier 2 has a real
  value to compare a charge's `charged_on` against instead of reading `accounts.created_at`
  through a second module's table.
- Compute that date correctly in `America/Bogota`, in both directions: the migration's
  one-time backfill (SQL) and every future signup (Go, via `internal/clock`) must agree on
  what "today" or "the day this account was created" means, even for an account created in
  the few minutes around the UTC/Bogota day boundary.
- Keep `PreferencesFor`'s one-query guarantee (`RM42` D7) — adding a third column costs zero
  extra queries anywhere.
- Never regress the RM34/RM42 Inactive-account gating that already applies to
  `account.settings` reads.

**Non-Goals:**
- Anything under `internal/gateway` or `internal/charging` — the `/external-charges` rejection
  check, the `min` attribute, the validation-error wiring. All tier 2, and all forbidden here
  by `ai/architecture.md` §2 ("no HTML inside domain modules"; no cross-module DB access).
- A write port for the value. Roadmap D7: the date is read-only in this roadmap. Making it
  editable is a separate, future item (`openspec/roadmaps/backlog.md`).
- A per-vehicle start date. Roadmap D1 rejected `account.vehicles` as the home, deliberately,
  for today's one-car-per-account reality.
- Unit tests (roadmap D10, settled with the user). This design still authors the expected
  values a human checks by hand — see "Test Contract" below — but no `_test.go` work is in
  `tasks.md`.

## Decisions

### D1 — Schema: `account.settings.analysis_start_date DATE NOT NULL`

Restated from the roadmap (binding, D1), for this document's self-containment:

```sql
ALTER TABLE account.settings ADD COLUMN analysis_start_date DATE NOT NULL;
```

(The actual migration adds it in three safe steps — see D2.)

Three alternatives were considered and rejected, carried over from the roadmap with their
reasons:

- **A column on `account.accounts`** — rejected. `accounts` holds identity (email, provider,
  display name, status). `analysis_start_date` is policy that governs what the platform does
  with a vehicle's data, not a fact about who the user is. `RM42` D3 already settled that
  per-account policy has exactly ONE home, `account.settings` — splitting it again reopens
  "which table do I use?" for every future setting, exactly the question `RM42`'s typed-table
  decision (its own D1) was designed to close.
- **A column on `account.vehicles`** — rejected *for now*. Per-vehicle is more correct the
  day a user connects a second Tesla: that car's own analysis should start when it was
  registered, not when the account was created. But the platform is one-car-per-account in
  practice today, and a per-vehicle column would force every caller — including tier 2's
  gateway check — to resolve a `teslaID` just to read one date. Revisit when real
  multi-vehicle support lands; nothing in this schema blocks that later move.
- **No new column, read `accounts.created_at` directly** — rejected. It needs no migration,
  but the value could then never diverge from signup time. A separate column is what makes
  a future edit (D7's deferred follow-up) possible without another migration.

### D2 — Migration: `ADD` nullable → `UPDATE` backfill → `SET NOT NULL`, no `DEFAULT`

Restated from the roadmap (binding, D2). Exact DDL, both directions:

```sql
-- +goose Up
ALTER TABLE account.settings ADD COLUMN analysis_start_date DATE;

UPDATE account.settings s
SET analysis_start_date = (a.created_at AT TIME ZONE 'America/Bogota')::date
FROM account.accounts a
WHERE a.id = s.account_id;

ALTER TABLE account.settings ALTER COLUMN analysis_start_date SET NOT NULL;

-- +goose Down
ALTER TABLE account.settings DROP COLUMN analysis_start_date;
```

The column cannot be added `NOT NULL` in one step — existing rows have no value yet, so it
must arrive nullable, get backfilled, then be locked down. This is the same three-step shape
`ai/go-conventions.md` and this project's migration history already use for a `NOT NULL`
column added to a populated table.

**Why `AT TIME ZONE 'America/Bogota'` is load-bearing, not decorative.** `created_at` is
`TIMESTAMPTZ` — an instant, no zone attached to the stored value itself. Casting it straight
to `DATE` (`created_at::date`) would compute the date using whatever zone the **database
session** happens to be in when the migration runs, which is almost never `America/Bogota`
by default (Postgres sessions typically start in UTC or the server's configured zone). That
is exactly the bug class `ai/go-conventions.md`'s time-zone rule and `make tz-guard` exist to
prevent on the Go side; the migration states the zone explicitly because SQL has no
equivalent guard. See the Test Contract below for a concrete instant where the naive cast and
the correct cast disagree.

**Why no `DEFAULT`, in either direction.** A `DEFAULT CURRENT_DATE` on the column would also
resolve in the session's zone at insert time — the same bug, just deferred from migration-time
to every future signup instead of caught once. D5 (below) puts the zone-correct computation in
the Go caller instead, using `internal/clock`, which is the platform's one sanctioned owner of
"what day is it" (`ai/go-conventions.md`).

### D3 — No new index

Restated from the roadmap (binding, D3). `account_id` is already the primary key of
`account.settings`. Every read of `analysis_start_date` — `GetAccountSettings`,
`AnalysisStartDateFor` (via `PreferencesFor`) — is the same primary-key lookup this module
already performs for `language`/`theme`; adding one column to an existing `SELECT` list costs
nothing extra to plan or execute. See the full index plan in the table under "Index Plan"
below, which extends `RM42` D9's table with this tier's queries.

### D4 — Port shape: `Settings.AnalysisStartDate`, `AnalysisStartDateFor`, `InsertSettingsIfMissing` takes the date

Restated from the roadmap (binding, D4). Changes to `internal/account/account.go`:

```go
// Settings is an account's persisted preferences: always exactly one row per
// account, holding Language, Theme, and AnalysisStartDate.
type Settings struct {
	Language          string
	Theme             string
	// AnalysisStartDate is the first calendar day (America/Bogota) the platform
	// analyzes this account's vehicle data. Set once, at signup, from the
	// account's creation date (roadmap RM49 D1/D4/D7) — there is no write port
	// for it in this module yet; making it editable is deferred (see the
	// roadmap's "Future work").
	AnalysisStartDate time.Time
}
```

```go
// AnalysisStartDateFor returns the account's analysis start date: the first
// calendar day (America/Bogota) the platform analyzes this account's vehicle
// data. It never returns a zero time for a valid account — every account has
// exactly one settings row (RM42 D3/D4), and this column is NOT NULL. Mirrors
// LanguageFor/ThemeFor exactly: it only errors on an actual lookup failure
// (unknown accountID, DB error), never because of the stored value's shape.
AnalysisStartDateFor(ctx context.Context, accountID uuid.UUID) (time.Time, error)
```

`PreferencesFor` returns the new field from the same `GetAccountSettings` query — no second
round trip. `AnalysisStartDateFor` is implemented on top of `PreferencesFor`, exactly as
`LanguageFor`/`ThemeFor` already are, so a caller that needs only the date still costs one
query, and a caller needing more than one preference (tier 2's future context-population
code) has `PreferencesFor` as the single call that returns everything.

`InsertSettingsIfMissing` gains one parameter:

```sql
-- name: InsertSettingsIfMissing :exec
INSERT INTO account.settings (account_id, analysis_start_date)
VALUES (@account_id, @analysis_start_date)
ON CONFLICT (account_id) DO NOTHING;
```

`ON CONFLICT (account_id) DO NOTHING` is unchanged — a returning user's existing row (and its
already-set `analysis_start_date`) is never touched. See D5 for where the Go caller sources
the value.

**No normalization function.** `normalizeLanguage`/`normalizeTheme` exist because `language`
and `theme` are closed vocabularies that a legacy or out-of-band row could violate.
`analysis_start_date` is a plain calendar date with no vocabulary to violate — any valid
`DATE` value is a valid analysis start date. `AnalysisStartDateFor`/`PreferencesFor` return
the stored value unchanged (no normalization step), the same way `PreferencesFor` already
returns any other non-enum column would, if one existed.

### D5 — The Go caller sources the date from `internal/clock`, never `CURRENT_DATE`

`UpsertFromOAuth` (`internal/account/service.go`) is the sole caller of
`InsertSettingsIfMissing`. It now supplies the date explicitly:

```go
today := clock.CalendarDay(clock.Now(), clock.Zone())
if err := qtx.InsertSettingsIfMissing(ctx, accountdb.InsertSettingsIfMissingParams{
	AccountID:         row.ID,
	AnalysisStartDate: dateFromTime(today),
}); err != nil {
	return Account{}, fmt.Errorf("creating account settings: %w", err)
}
```

`clock.Now()` returns the current instant already expressed in `America/Bogota`
(`internal/clock/clock.go`). `clock.CalendarDay(t, loc)` normalizes `t` to the calendar day it
falls on **when observed in `loc`**, expressed at UTC midnight — the exact representation
`pgtype.Date` expects (`internal/clock/calendar.go`'s own doc comment; this is the same helper
`internal/analytics` already uses for its own day bucketing, e.g.
`clock.CalendarDay(clock.Now(), time.UTC)` in `internal/analytics/recalculate.go`). Passing
`clock.Zone()` (rather than `time.UTC`, as `analytics` does for its own UTC-bucketed data)
is what makes this call agree with the migration's `AT TIME ZONE 'America/Bogota'` cast (D2):
both compute "the Bogota calendar day," one in SQL for existing rows, one in Go for every new
one.

`dateFromTime` is a new small helper in `internal/account/service.go`, mirroring
`internal/charging/service.go`'s existing `dateFromTime` exactly (`pgtype.Date{Time: t, Valid:
true}`) — this module has no `DATE` column today, so the helper does not exist yet; adding it
here is the first instance in `internal/account`, not a new pattern for the codebase.

This is why `internal/account` gains a new import, `internal/clock` — it did not depend on it
before this tier. `internal/clock` imports only stdlib `time` (plus the `time/tzdata` blank
import), so this cannot create an import cycle (`ai/architecture.md`'s cycle-prevention
section).

**Why not `CURRENT_DATE` in the SQL `INSERT`, or a column `DEFAULT`.** Both resolve in the
database session's time zone at write time — exactly the bug D2 already rejected for the
backfill, just moved to a different point in the code. Computing the value in Go, through
`internal/clock`, is the one place `ai/go-conventions.md` designates as the platform's sole
owner of "what day is it," and `make tz-guard` enforces that no other `.go` file (outside
`internal/clock`) calls raw `time.Now()` to answer that question.

### D6 — sqlc-generated type: verify, do not assume

`ai/go-conventions.md` §Persistence's "verify, do not assume" rule, restated for this column.
The direct precedent, `charging.manual_charge_entries.charged_on` (`DATE NOT NULL`), generates
as `pgtype.Date` (`internal/charging/db/models.go:18`), converted at the DB→domain boundary
via `.Time` on read (`ChargedOn: r.ChargedOn.Time`, `internal/charging/service.go:472`) and a
`dateFromTime` helper on write (`internal/charging/service.go:530`). `sqlc.yaml`'s `account`
entry carries no per-column override for `DATE`, so `analysis_start_date DATE NOT NULL` is
expected to generate the same way: `pgtype.Date` in `accountdb.Settings`,
`GetAccountSettingsRow.AnalysisStartDate`, and
`InsertSettingsIfMissingParams.AnalysisStartDate`. **Task T3 (below) must confirm this by
reading the actual generated `internal/account/db/models.go` and `query.sql.go` after running
`make sqlc`, not by trusting this expectation.** If sqlc produces something else, adjust the
mapping helpers (`dateFromTime`, `.Time` accessor) accordingly and report the actual type.

### D7 — Carrying forward the existing Inactive-account gating, unchanged

`GetAccountSettings` already gates its read with `EXISTS (SELECT 1 FROM account.accounts a
WHERE a.id = settings.account_id AND a.status = 'Active')` (RM34 D14/D15, carried into
`account.settings` by RM42 D10). Adding `analysis_start_date` to the `SELECT` list does not
touch that predicate — the gate applies to the whole row, including the new column, with zero
additional code. `InsertSettingsIfMissing` stays deliberately **NOT** gated, mirroring
`UpsertAccountFromOAuth`'s own exemption (`internal/account/AGENTS.md`: "`UpsertFromOAuth` is
the one operation NOT filtered by [status]") — a brand-new account has no status concern yet.
This tier changes neither gate; it is recorded here so nobody has to re-derive it from the SQL
diff.

### D8 — Deploy: no extra command (restated from roadmap D9)

The compose stack's `migrate` service already applies every pending migration and exits before
`web`/`poller` start (`deploy/docker/compose.yaml`, `docs/0-set-up/deployment.md` §8.8). The
normal deploy flow applies this migration with no new step:

```bash
# On the VPS, from the repo root.
git pull
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build

# Check that migrate finished cleanly: it must show "Exited (0)".
docker compose --project-directory . -f deploy/docker/compose.yaml ps
```

### D9 — Rollback: the roadmap's suggested command does not exist; the verified procedure is manual SQL

The roadmap (D9) asked this tier to "run goose down inside the running stack rather than `make
migrate-down` on the host" and to verify the exact command against
`deploy/docker/compose.yaml` before writing it into `docs/0-set-up/deployment.md`. That
verification surfaced a real finding, not a rewording:

- `deploy/docker/Dockerfile`'s `migrate` stage COPYs only the compiled `cmd/migrate` binary
  and the four modules' migration directories — it never installs the `goose` CLI. The
  Dockerfile's own comment states why: building
  `github.com/pressly/goose/v3/cmd/goose` fails in this repo, because `go.sum` carries no
  entries for the CLI's optional database-driver dependencies (this project only ever
  imports goose as a library). There is **no `goose` binary inside any running container** —
  `web`, `migrate`, or `poller`.
- `cmd/migrate/main.go` — the program the `migrate` service actually runs — calls only
  `provider.Up(ctx)` (see `applyDir`). It has **no down code path at all**: no flag, no
  subcommand, nothing that reverses a migration.

So "run goose down inside the running stack" names a tool and a code path that are both
verifiably absent from the deployed images. Copying that wording into `deployment.md`
unverified — exactly what D9 warned against — would have documented a command that fails the
moment someone tries it.

**The verified, working rollback**, confirmed against the pinned `github.com/pressly/goose/v3
v3.27.3`'s own Postgres dialect (`internal/dialects/postgres.go`: the tracking table is
`goose_db_version(id, version_id, is_applied, tstamp)`, and a migration counts as applied
exactly when a row for its `version_id` exists):

```bash
# 1. Open psql inside the running db container.
docker compose --project-directory . -f deploy/docker/compose.yaml exec db \
  psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

```sql
-- 2. Run this migration's own Down SQL by hand (from the migration file's
--    "-- +goose Down" section).
ALTER TABLE account.settings DROP COLUMN analysis_start_date;

-- 3. Tell goose the migration is no longer applied, so a future deploy
--    re-runs it instead of skipping it. version_id is this migration's
--    filename timestamp.
DELETE FROM goose_db_version WHERE version_id = 20260908000001;
```

Without step 3, the next `migrate` container run believes `20260908000001` already ran and
never re-applies it — the schema and the code would silently diverge on the next roll-forward.

This gap — no rollback tooling at all in the deploy path, for any migration, not only this
one — pre-dates this tier and is out of scope to fix here (it would mean adding a `down`
subcommand to `cmd/migrate` and a matching compose invocation, real work with its own design).
It is flagged in this tier's final report for the leader, and `docs/0-set-up/deployment.md`
documents the manual procedure above as this migration's own rollback steps, per D9's
requirement, rather than silently working around the gap or copying an unverified command.

## Index Plan

Extends `RM42` D9's table with this tier's queries. Every read or write this tier adds or
changes is justified against the actual query:

| Query | Predicate | Index used |
|---|---|---|
| `GetAccountSettings` (read, unchanged predicate, one more `SELECT` column) | `account_id = @account_id` (PK on `account.settings`) `AND EXISTS (... a.id = ...)` (PK on `account.accounts`) | Both sides ride their table's existing primary-key index — unchanged from `RM42`. |
| `InsertSettingsIfMissing` (write, signup, unchanged predicate, one more inserted column) | `ON CONFLICT (account_id)` | The primary key itself is the conflict target — no separate index. |
| Migration backfill (`UPDATE ... FROM`, one-time) | `a.id = s.account_id` | Both sides are primary-key columns; a one-time, off-hours, small-table join — the read-heavy/write-heavy asymmetry (`ai/architecture.md` §7) explicitly allows this cost off the hot path. |

**Verdict: no secondary index (roadmap D3, restated as D3 above).** `analysis_start_date` is
never a predicate, `JOIN` key, or `ORDER BY` target in any query this tier adds — it is always
a projected or written column of a row already located by primary key. If a future query ever
needs to filter or sort by this column (e.g. "list accounts whose analysis window started this
month"), add a targeted index then, justified by that query — not preemptively here, mirroring
the same YAGNI stance `RM24`/`RM42` took for their own preference columns.

## Test Contract

Authored before the implementation exists, per `ai/go-conventions.md` §Testing ("author their
expected values up front") and this roadmap's D10 (no one writes these as `_test.go` files in
this tier — the owner checks these by hand, e.g. via `psql` after a local `make migrate-run`).
All values below are exact. `America/Bogota` is a fixed UTC−05:00 offset with no daylight
saving (unchanged since 1993), so every conversion below is a plain 5-hour shift.

1. **Ordinary case — well clear of the day boundary.** GIVEN an account with `created_at =
   '2026-01-15 10:00:00+00'` (10:00 UTC), WHEN the migration's `Up` backfills it, THEN
   `analysis_start_date = 2026-01-15` (Bogota local time is 2026-01-15 05:00:00 — same
   calendar day as UTC).
2. **Near-midnight boundary — the offset changes the day (the case D2's `AT TIME ZONE` cast
   exists for).** GIVEN an account with `created_at = '2026-01-15 04:59:00+00'`, WHEN the
   migration's `Up` backfills it, THEN `analysis_start_date = 2026-01-14` — **one calendar day
   earlier** than the UTC date component of `created_at` (Bogota local time is 2026-01-14
   23:59:00). A naive `created_at::date` cast, evaluated in a UTC session, would instead
   produce `2026-01-15` — the wrong day, and exactly the bug D2's explicit zone cast prevents.
3. **One minute later — across the same boundary, the other side.** GIVEN an account with
   `created_at = '2026-01-15 05:00:00+00'`, WHEN the migration's `Up` backfills it, THEN
   `analysis_start_date = 2026-01-15` (Bogota local time is exactly 2026-01-15 00:00:00).
   Together with item 2, this pins the exact instant where the backfilled date changes.
4. **A fresh signup gets today's Bogota date, computed in Go, not the database session's
   zone.** GIVEN a brand-new `OAuthIdentity` with no prior account, signing up when
   `clock.Now()` is `2026-09-08 08:00:00 America/Bogota` (`= 2026-09-08 13:00:00 UTC`), WHEN
   `UpsertFromOAuth` is called, THEN `account.settings.analysis_start_date = 2026-09-08` for
   the new account, regardless of what time zone the Postgres server session itself is
   configured to (unlike a `DEFAULT CURRENT_DATE`, which D2 rejected for exactly this reason).
5. **`PreferencesFor` and `AnalysisStartDateFor` agree, and cost what they already cost.**
   GIVEN an account whose `analysis_start_date` is `2026-01-14`, WHEN `PreferencesFor` is
   called, THEN `Settings.AnalysisStartDate` equals `time.Date(2026, 1, 14, 0, 0, 0, 0,
   time.UTC)` (the `pgtype.Date.Time` representation — UTC-midnight-of-the-day, matching
   `internal/charging`'s existing `ChargedOn` convention, not a Bogota-midnight instant). WHEN
   `AnalysisStartDateFor` is called separately for the same account, THEN it returns the
   identical value, and — because it is implemented on top of `PreferencesFor` exactly like
   `LanguageFor`/`ThemeFor` are — it costs exactly the one `GetAccountSettings` query it
   already cost before this tier, not two.
6. **An `Inactive` account's analysis start date is gated exactly like its language and theme
   (D7, unchanged behavior).** GIVEN an account with `status = 'Inactive'`, WHEN
   `PreferencesFor`/`AnalysisStartDateFor` are called for it, THEN each returns the same
   wrapped not-found-shaped error `LanguageFor`/`ThemeFor` already produce today for an
   `Inactive` account — no special-casing, because the existing `EXISTS` gate on
   `GetAccountSettings` already covers the whole row.

## Migration Plan

1. Add the goose migration exactly as in D2
   (`internal/account/db/migrations/20260908000001_settings_add_analysis_start_date.sql`) —
   this is the timestamp the roadmap names, and it was re-checked against every module's
   migrations directory for a collision (`ls internal/*/db/migrations/*.sql | xargs -n1
   basename | grep -oE '^[0-9]{14}' | sort | uniq -d`): `20260908000001` is unique; the only
   existing duplicate in the repo (`20260720000001`, between `internal/account` and
   `internal/telemetry`) is a pre-existing, already-accepted case and is untouched by this
   tier. Re-verify at implementation time in case a sibling change lands a migration first
   (`make migration-guard`).
2. Add `AnalysisStartDate time.Time` to `Settings` and `AnalysisStartDateFor` to the `Service`
   interface in `account.go` (D4), with the doc comment from D4.
3. Change `InsertSettingsIfMissing` and `GetAccountSettings` in `query.sql` (D4). Run `make
   sqlc`. **Verify, do not assume** (D6) the generated Go type for
   `Settings.AnalysisStartDate`, `GetAccountSettingsRow.AnalysisStartDate`, and
   `InsertSettingsIfMissingParams.AnalysisStartDate` by reading the regenerated
   `internal/account/db/models.go` and `query.sql.go`. Report the actual type.
4. Implement `AnalysisStartDateFor` and extend `PreferencesFor`'s mapping in `service.go`;
   rewrite `UpsertFromOAuth`'s call to `InsertSettingsIfMissing` to supply the date via
   `internal/clock` (D5); add the `dateFromTime` helper (mirrors
   `internal/charging/service.go`'s existing one).
5. Update `internal/account/AGENTS.md`'s "Public interface" section with
   `AnalysisStartDateFor`. Grep `kkpa/context/` for `account.settings` and the account port;
   fix any guide this change invalidates.
6. Write the verified deploy + rollback steps (D8/D9) into `docs/0-set-up/deployment.md`,
   under §8, alongside the existing `migrate` documentation — the manual `psql` + `goose_db_
   version` procedure from D9, not the unverified `goose down` wording the roadmap
   originally suggested.
7. Verify `MIGRATIONS_DIRS` (`Makefile`), `db-setup`/`db-reset` role-and-ownership assumptions,
   `sqlc.yaml`, `make migration-guard`, and `make tz-guard` — see "Reverse-Direction Check"
   below. Record findings in `tasks.md`.
8. `go build ./...`, `go vet ./...`, `gofmt -l` pass. `openspec validate
   RM49-account-add-analysis-start-date --strict` passes.

**Rollback:** see D9. The migration's own `-- +goose Down` (D2) plus the manual
`goose_db_version` cleanup D9 documents. Tier 2 (a separate, dependent change) cannot exist
yet at this point in the sequence, so no other tier's code needs to roll back first.

## Reverse-Direction Check

Required by `CLAUDE.md`'s "Workflow & architectural decisions are documented with their
steps" rule: a real check, not an assumption, for every mechanism this tier's schema change
could affect.

- **`MIGRATIONS_DIRS` (`Makefile`)** — already lists `internal/account/db/migrations`. This
  tier's file lands in that same directory. **No change needed.**
- **`db-setup`/`db-reset` role-and-ownership** — read `Makefile`'s `db-setup` and `db-reset`
  targets directly: `db-reset` drops and recreates the **whole database**
  (`DROP DATABASE IF EXISTS "$(DB_NAME)"` then `db-setup`), and `db-setup` creates the
  database `OWNER $$ROLE` (the app role) at the database level, not per-table or per-column.
  `account.settings` is already owned by that same role since `RM39`'s schema move; adding one
  column to an already-owned table needs no separate `GRANT`. **No change needed; confirmed by
  reading the target, not assumed.**
- **`sqlc.yaml`** — the existing `account` entry's `schema:` already points at
  `internal/account/db/migrations`, which will include this tier's file automatically once it
  exists. **No structural change needed.**
- **`make migration-guard`** — its collision check (`ls
  $(MIGRATIONS_DIRS:%=%/*.sql) | grep -oE ... | sort | uniq -d`) was reproduced manually above
  (Migration Plan step 1): `20260908000001` does not collide with any migration in
  `internal/account`, `internal/telemetry`, `internal/charging`, or `internal/analytics`.
  **Passes.**
- **`make tz-guard`** — its four checks (`time.Now()`, a hand-rolled UTC-midnight
  `time.Date(...)` construction, a 24h `.Truncate`, a hardcoded IANA zone string) all scan
  `internal --include='*.go'` only — `.sql` files are never scanned, so the migration's `AT
  TIME ZONE 'America/Bogota'` literal (D2) is outside the guard's reach by construction, same
  as every other zone-aware SQL cast already in this repo. The **Go-side** change (D5) calls
  `clock.CalendarDay(clock.Now(), clock.Zone())`, entirely inside the already-exempt
  `internal/clock` boundary rule — `clock.Now()` and `clock.Zone()` are themselves defined
  inside `internal/clock`, so no raw `time.Now()` or hardcoded zone string appears in
  `internal/account/service.go`. **Passes**, verified by re-reading `make tz-guard`'s grep
  shape (`Makefile` lines ~444–500) against the exact code this tier adds, not assumed.

## Open Questions

None — roadmap decisions D1–D4 and D9 (this tier's applicable binding decisions) are settled
and restated above. D5 (this design's own Go-caller-sources-the-date decision), D6 (sqlc type
verification), D7 (gating carried forward, unchanged), and D9's rollback finding are this
design's own additions, flagged explicitly rather than silently resolved, per this dispatch's
own instruction.
