Source: MAG-43 — https://linear.app/magus-monitor/issue/MAG-43/theme-selector-settings-page
Roadmap: openspec/roadmaps/RM42-settings-theme-selector.md
Tier: 1 of 2 (account; tier 2 is `RM42-gateway-add-theme-selector`, module `gateway`, depending
on this tier)

## Why

MAG-43 asks for a theme selector on a new `/settings` page. Before any UI can exist, the platform
needs ONE place to store per-user preferences — today `accounts.language` is the only preference,
living directly on the identity table, and there is no home at all for a theme choice. Roadmap
`RM42-settings-theme-selector` settled (with the user, 2026-09-04, before any artifact was
written) that a new `account.settings` table — not a column on `accounts`, not a `settings`
module, not a key/value table — is that one place, and that `accounts.language` moves into it in
this same tier so the platform never has two homes for a preference (roadmap decision D3).

This is **tier 1 of 2**. It implements roadmap decisions D2 (schema), D3 (language migrates in,
column dropped), D4 (settings row at signup), D5 (module/schema ownership), D7 (theme vocabulary),
and D9 (no `CHECK` constraint) for the `account` module only. Tier 2
(`RM42-gateway-add-theme-selector`, depends on this tier) consumes the port this tier adds to
build the `/settings` page, the dropdown, the `theme` cookie, and `make theme-guard` — none of
that is in scope here.

## What Changes

- **One migration** (`internal/account/db/migrations/20260904000001_add_account_settings.sql`),
  strict order: `CREATE TABLE account.settings` → `INSERT ... SELECT` backfill (every existing
  account gets a settings row carrying its current `language`; `theme` takes its `DEFAULT
  'graphite'`, since no prior column held it) → `DROP COLUMN accounts.language`. The `Down`
  reverses data-safely: re-add the column, copy values back from `settings`, then drop the table.
  Full DDL, rationale, and index plan in `design.md` (this change is DB-touching — the `database`
  design gate applies).
- **`account.settings` row is created at signup**, in the same transaction as the account
  upsert (`UpsertFromOAuth` now runs inside an explicit `pgx` transaction, mirroring the pattern
  `AccessTokenFor` already uses in this file, rather than a new multi-statement SQL idiom). A
  returning user's existing row is left untouched (`ON CONFLICT (account_id) DO NOTHING`).
- **`LanguageFor`/`SetLanguage` are rewritten** against `account.settings` — same signatures, same
  external behavior (default `es`, normalize-on-read, reject-on-write), different backing table.
- **New `ThemeFor`/`SetTheme` + `ErrUnsupportedTheme`**, mirroring the language pair exactly:
  vocabulary `{apex, graphite, halloween}`, default `graphite`, normalize-on-read, reject unknown
  values on write without persisting.
- **New `PreferencesFor(ctx, accountID) (Settings, error)`** — returns language AND theme in a
  SINGLE query. This is the port method any per-request caller (tier 2's context-population
  middleware, the `/settings` page) MUST use instead of calling `LanguageFor` + `ThemeFor`
  separately, which would double the per-request query count. `LanguageFor`/`ThemeFor` are
  themselves implemented on top of `PreferencesFor` internally, so no second SQL query is
  introduced anywhere by adding the theme pair. See `design.md` D7.
- **sqlc query changes + regen**: `GetAccountLanguage` is replaced by `GetAccountSettings`
  (returns both columns, one query); `UpdateAccountLanguage` is repointed at `account.settings`;
  a new `UpdateAccountTheme` mirrors it; a new `InsertSettingsIfMissing` backs the signup path.
  `make sqlc` regenerates `accountdb`.
- **RM34's Inactive-account gating is carried forward.** `GetAccountLanguage` was gated
  `status = 'Active'` directly on the `accounts` row it lived on (RM34 D14/D15). Now that the
  preference lives in a separate table, the same gate is expressed as an `EXISTS` against
  `account.accounts`, exactly mirroring the existing `ListVehiclesByAccount` /
  `GetLatestTeslaTokenByAccount` pattern — this is not requested verbatim by the roadmap, but
  dropping it would silently regress an existing invariant; see `design.md` D10.
- **Tests** — pure unit tests for `normalizeTheme`/`isSupportedTheme` (no DB), and
  `DATABASE_URL`-gated integration tests covering the contract `design.md` authors up front (see
  its "Test Contract" section): backfill preservation, fresh-signup defaults, unsupported-theme
  rejection, out-of-set normalization on read, and the `Down` migration's restore.
- **Docs** — `internal/account/AGENTS.md`'s "Public interface" section gains the new port methods;
  `kkpa/context/` is grepped for `account`/`language` and any stale guide is fixed in this same
  change (`CLAUDE.md` docs-track-change rule).
- **Makefile/guard verification** — `MIGRATIONS_DIRS`, `db-setup`/`db-reset` role-and-ownership
  assumptions, `sqlc.yaml`, and `make migration-guard` are checked against this change; findings
  recorded in `tasks.md`/the final report rather than assumed unaffected.

**Not breaking.** `account.Service` only gains methods (`PreferencesFor`, `ThemeFor`, `SetTheme`);
`LanguageFor`/`SetLanguage` keep their existing signatures and external behavior. The schema change
is additive-then-destructive to a column no consumer reads directly (only through the port), so no
caller outside this module needs to change to keep compiling — tier 2 is a separate, dependent
change that is free to start once this tier's port exists.

**Affected modules:** `internal/account` (implements this tier). `internal/gateway` is affected
only as the intended future consumer (tier 2, a separate dependent change, not touched here).

## Capabilities

### New Capabilities

(none — this proposal extends the existing `account` capability only)

### Modified Capabilities

- `account`: adds a new requirement, "Per-Account Theme Preference," mirroring the existing
  "Per-Account Language Preference" requirement's shape (closed vocabulary, default, normalize
  on read, reject on write). Adds a new requirement, "Settings Row Guaranteed At Account
  Creation," covering the signup-time row creation, the migration backfill, and the one-query
  combined read guarantee. The existing "Per-Account Language Preference" requirement's *external*
  behavior is unchanged (same defaults, same vocabulary, same normalize/reject semantics) — only
  its backing storage moves, which is a `design.md` concern, not a spec-level behavior change.

## Impact

- `internal/account` — new migration, new domain type (`Settings`) + three new constants +
  one new sentinel error, three new port methods (`PreferencesFor`, `ThemeFor`, `SetTheme`),
  `UpsertFromOAuth` rewritten to run inside an explicit transaction, four sqlc query changes,
  sqlc regeneration, service implementation, unit + integration tests, `AGENTS.md` update.
- `internal/gateway` — **not touched by this tier.** Tier 2 is the only consumer of the new port
  surface; it cannot start rendering `/settings` until this tier's methods exist, but nothing in
  the gateway fails to compile in the meantime (no existing gateway code calls any of the new
  methods yet).

**Read path affected:** the per-request preference resolution the gateway's `LanguageMiddleware`
already performs today (one query per request), and the future combined resolution tier 2 will
build for both language and theme. This tier's `PreferencesFor` is a two-column read located by
primary key (`account_id`) plus one `EXISTS` on the owning account's own primary key — see
`design.md`'s index plan for the full justification (no new index; zero degradation versus
today's single-column read).
