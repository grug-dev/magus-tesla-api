Source: MAG-55 — https://linear.app/magus-monitor/issue/MAG-55/external-charges
Roadmap: openspec/roadmaps/RM49-analysis-start-date.md
Tier: 1 of 2 (account; tier 2 is `RM49-gateway-restrict-external-charge-date`, module
`gateway`, depending on this tier)

## Why

MAG-55 wants external charges rejected when they are dated before the account's analysis
window starts. Today that start date is implicit: it is `account.accounts.created_at`, the
moment a user first signed in. Nothing stores it as its own value, so tier 2 (the gateway
check) has nothing explicit to read.

This tier gives every account one stored date, `analysis_start_date`, on
`account.settings`. It is the settled home for per-account policy (roadmap D1, carrying
forward `RM42` D3's rule: one table for policy, not a new column split across tables). The
value is set once, from `created_at`, and stays read-only in this roadmap (D7) — there is
no UI and no write port for it here.

This is **tier 1 of 2**. It implements roadmap decisions D1–D4 and D9 for the `account`
module only. Tier 2 (`RM49-gateway-restrict-external-charge-date`, depends on this tier)
consumes the new port method to reject a bad `charged_on` on the `/external-charges` page —
none of that gateway work is in scope here.

## What Changes

- **One migration**
  (`internal/account/db/migrations/20260908000001_settings_add_analysis_start_date.sql`),
  strict order (roadmap D2): `ALTER TABLE ... ADD COLUMN` (nullable) → `UPDATE ... FROM`
  backfill, casting `created_at` through `AT TIME ZONE 'America/Bogota'` before taking the
  date → `ALTER TABLE ... SET NOT NULL`. No `DEFAULT` in either direction — a
  `DEFAULT CURRENT_DATE` would resolve in the database session's zone, breaking the
  project's `America/Bogota` rule. The `Down` drops the column. Full DDL, rationale, and
  index plan in `design.md` (DB-touching — the `database` design gate applies).
- **`account.Settings` gains `AnalysisStartDate time.Time`**, returned by `PreferencesFor`
  from the same single query — no extra round-trip (roadmap D4).
- **New port method `AnalysisStartDateFor(ctx, accountID) (time.Time, error)`**, mirroring
  `LanguageFor`/`ThemeFor` exactly, including their doc-comment depth, for callers that need
  only the date.
- **sqlc query changes + regen**: `GetAccountSettings` returns the new column;
  `InsertSettingsIfMissing` takes `analysis_start_date` as a parameter. `make sqlc`
  regenerates `accountdb`.
- **The Go caller of `InsertSettingsIfMissing` (inside `UpsertFromOAuth`) supplies today's
  date from `internal/clock`** — `clock.CalendarDay(clock.Now(), clock.Zone())`, the same
  UTC-midnight-of-the-Bogota-day shape the migration backfill computes in SQL. Never
  `CURRENT_DATE`, never a raw `time.Now()` (`ai/go-conventions.md`'s time-zone rule,
  `make tz-guard`).
- **No write port for the value in this tier** (roadmap D7). It is set once, at signup, and
  the value is otherwise read-only.
- **Docs**: `docs/0-set-up/deployment.md` gets the verified VPS deploy AND rollback steps
  for this migration (roadmap D9) — including a real finding that the roadmap's suggested
  rollback command does not work as written; see `design.md` "Reverse-direction check".
  `internal/account/AGENTS.md`'s "Public interface" section gains the new port method.
  `kkpa/context/` is grepped for `account.settings` / the account port and any stale guide
  is fixed in this same change (`CLAUDE.md` docs-track-change rule).
- **Makefile/guard verification** — `MIGRATIONS_DIRS`, `db-setup`/`db-reset`
  role-and-ownership assumptions, `sqlc.yaml`, `make migration-guard`, and `make tz-guard`
  are checked against this change; findings recorded in `design.md` and `tasks.md`, never
  assumed.
- **No unit tests in this tier** (roadmap D10, settled with the user). `tasks.md` covers
  code and docs only. `go build`, `go vet`, `gofmt -l`, and the guards still run.

**Not breaking.** `account.Service` only gains one method (`AnalysisStartDateFor`);
`LanguageFor`/`SetLanguage`/`ThemeFor`/`SetTheme` keep their existing signatures and
behavior. `PreferencesFor`'s return type (`Settings`) gains a field, which is source-additive
for any existing caller that only reads `.Language`/`.Theme`. `InsertSettingsIfMissing`'s
sqlc-generated parameter struct changes shape, but it has exactly one caller
(`UpsertFromOAuth`, inside this same module), updated in this same tier.

**Affected modules:** `internal/account` (implements this tier). `internal/gateway` is
affected only as the intended future consumer (tier 2, a separate dependent change, not
touched here). `internal/charging` is explicitly NOT touched (roadmap D5) — the write path
for external charges stays unchanged in this tier.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `account` capability only)

### Modified Capabilities

- `account`: adds a new requirement, "Per-Account Analysis Start Date," describing the
  stored value, its one-time signup-set origin, its read-only nature in this roadmap, and
  the combined-read guarantee through `PreferencesFor`. The existing "Settings Row
  Guaranteed At Account Creation" requirement's behavior is unchanged in substance (a
  settings row still exists atomically with the account) — this proposal only widens what
  that row carries.

## Impact

- `internal/account` — new migration, `Settings` gains one field, one new port method
  (`AnalysisStartDateFor`), one sqlc query signature change (`InsertSettingsIfMissing`),
  `GetAccountSettings` widened, `UpsertFromOAuth`'s call site updated to supply the date via
  `internal/clock`, sqlc regeneration, `AGENTS.md` update. New import: `internal/clock` (the
  account module did not previously depend on it).
- `internal/gateway` — **not touched by this tier.** Tier 2 is the only consumer of the new
  port method; nothing in the gateway fails to compile in the meantime.
- `internal/charging` — **not touched** (roadmap D5, deliberate).

**Read path affected:** the same per-request `GetAccountSettings` read tier 1 of `RM42`
already established (one query, primary-key lookup on `account.settings.account_id`, gated
by the owning account's `Active` status via `EXISTS`). This tier adds one more column to
that same query's `SELECT` list — no new query, no new index, no change to the query's
predicate or plan shape. See `design.md`'s index plan for the full justification.
