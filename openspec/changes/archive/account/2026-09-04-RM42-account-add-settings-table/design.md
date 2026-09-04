## Context

The `account` module owns three tables in the `account` Postgres schema (`internal/account/db/migrations/20260902000001_move_account_to_own_schema.sql`):
`accounts`, `tesla_tokens`, `vehicles`. Since RM24, `accounts` carries a `language TEXT NOT NULL
DEFAULT 'es'` column (`20260813000001_accounts_add_language.sql`) — the platform's only per-user
preference today, read/written through `account.Service.LanguageFor`/`SetLanguage`
(`internal/account/service.go`, `internal/account/account.go`).

MAG-43 (roadmap `RM42-settings-theme-selector`) adds a theme selector. The roadmap settled, with
the user, before any artifact was written, that preferences get ONE home — a new
`account.settings` table — and that `language` moves into it in this same tier rather than
splitting storage. This design implements that for the `account` module only; the gateway's
`/settings` page, dropdown, and cookie are tier 2, a separate dependent change.

Performance profile: **read-heavy** (`ai/architecture.md` §7). A preference read happens on every
signed-in page render — today via `LanguageMiddleware`, one query per request. This tier must not
increase that cost even though it adds a second preference; the roadmap is explicit that combining
both values into one row is what keeps the per-request cost at exactly one query
(`openspec/roadmaps/RM42-settings-theme-selector.md`, "Out of scope").

The direct precedent for a preference column is `20260813000001_accounts_add_language.sql`
(`NOT NULL DEFAULT`, no `CHECK`) and its design.md
(`openspec/changes/archive/account/2026-08-13-RM24-account-add-language-preference/design.md`).
The direct precedent for gating a query by the owning account's activation status via `EXISTS`
(not a `JOIN`, so the sqlc-generated row shape is untouched) is RM34 D14/D15, already applied to
`GetAccountLanguage`, `ListVehiclesByAccount`, `GetLatestTeslaTokenByAccount`, and
`ListAllVehicles`. Both precedents are read in full before this design and carried forward here.

## Goals / Non-Goals

**Goals:**
- One row per account holding every preference, so a page render that needs more than one
  preference costs one query, not N.
- Never lose or duplicate a preference during the migration: every existing account's `language`
  value survives verbatim; `theme` starts at its true product default for every account, old and
  new, because no prior column ever held a different value for it.
- Keep the same never-error, never-return-an-unsupported-value guarantee `LanguageFor` already
  gives, and give `ThemeFor` the identical guarantee.
- Guarantee a settings row exists from the moment an account exists, so every read is a plain
  primary-key lookup with no `COALESCE`, no "insert on first read" branch, and no nullable
  preference field.
- Preserve the RM34 Inactive-account gating that already applies to `accounts.language` reads and
  writes, now that the column moves to a different table.

**Non-Goals:**
- Anything under `internal/gateway` — the `/settings` page, the dropdown, the `theme` cookie,
  `make theme-guard`, `data-theme` resolution. All tier 2, and all forbidden here by
  `ai/architecture.md` §2 ("no HTML inside domain modules").
- Cookie-first read caching (`MAG-47`, explicitly out of scope for the whole roadmap — see
  roadmap "Out of scope"). `PreferencesFor` is a real per-request DB read; making it cheaper than
  one query is a different, later change.
- A `CHECK` constraint on either column (roadmap decision D9, restated in D5 below).
- A fourth or fifth setting, a settings versioning scheme, or per-setting metadata — strict YAGNI;
  the roadmap fixes exactly two preferences.
- Changing `Account`'s own fields. `Settings` is a separate domain type returned by separate port
  methods, not a field bolted onto `Account` (mirrors RM24's own Non-Goal, for the same reason:
  the gateway's session carries only an account id, not a loaded `Account`, on most requests).

## Decisions

### D1 — Schema: `account.settings`, typed columns, no key/value table

Exact DDL (binding, from the roadmap, restated here per the design-gate requirement that this
document be self-contained):

```sql
CREATE TABLE account.settings (
    account_id UUID PRIMARY KEY REFERENCES account.accounts(id) ON DELETE CASCADE,
    language   TEXT NOT NULL DEFAULT 'es',
    theme      TEXT NOT NULL DEFAULT 'graphite'
);
```

`account_id` as the primary key gives exactly one row per account and makes every lookup a plain
PK read — no extra index, no join key to design. `ON DELETE CASCADE` means a deleted account's
settings row cannot become an orphan (the module has no account-delete port today, but the FK
makes the invariant hold automatically if one is ever added, at zero ongoing cost).

**Rejected: a `theme` column on `accounts`.** `accounts` is the identity/auth table (email,
provider, provider_id, status). Presentation preferences do not belong there, and a second
preference proves the pattern: a third would make a habit of it. Keeping identity and preferences
in separate tables, both scoped by the same `account_id`, keeps `accounts` from slowly collecting
unrelated columns.

**Rejected: key/value EAV, `(account_id, key, value)`.** This was the alternative the ticket
itself might have reached for ("just add rows"). It loses three things a typed table gives for
free: `NOT NULL` per field (an EAV row's `value` is one column shared by every key, so it can only
be nullable or a string with no per-key constraint), a real default per field (a missing EAV row
means "look up the default in application code," reintroducing exactly the missing-row branch D3
below eliminates), and schema discoverability (an agent — or a human — reading `\d account.settings`
sees the two preferences and their defaults directly; reading an EAV table shows only `(account_id,
key, value)`, forcing a grep of application code to find out what keys exist at all). This is an
AI-efficiency point as much as a correctness one: a closed, self-describing schema is cheaper for
any future agent to extend correctly than a lookup table it must reverse-engineer first.

### D2 — Migration: strict order, data-safe `Down`

```sql
-- +goose Up
CREATE TABLE account.settings (
    account_id UUID PRIMARY KEY REFERENCES account.accounts(id) ON DELETE CASCADE,
    language   TEXT NOT NULL DEFAULT 'es',
    theme      TEXT NOT NULL DEFAULT 'graphite'
);

INSERT INTO account.settings (account_id, language)
SELECT id, language FROM account.accounts;

ALTER TABLE account.accounts DROP COLUMN language;

-- +goose Down
ALTER TABLE account.accounts ADD COLUMN language TEXT NOT NULL DEFAULT 'es';

UPDATE account.accounts a
SET language = s.language
FROM account.settings s
WHERE s.account_id = a.id;

DROP TABLE account.settings;
```

Order is load-bearing in both directions. `Up`: the table must exist before the backfill can
target it, and the column must survive until the backfill has copied every value out of it — so
`CREATE` → `INSERT ... SELECT` → `DROP COLUMN`, never re-ordered. `Down`: the column must exist
again before values can be copied into it, and `account.settings` must still exist when that copy
runs — so `ADD COLUMN` → `UPDATE ... FROM` → `DROP TABLE`, the exact reverse. The backfill only
copies `language` (the only value with a prior home); `theme` takes its column `DEFAULT` for every
backfilled row, which is correct because no account has ever had a theme preference before this
migration — there is nothing to preserve.

**Same tier, not split.** `LanguageFor`/`SetLanguage`'s Go rewrite ships in this same tier as the
migration (roadmap decision D3): the moment this migration runs, `GetAccountLanguage`'s old query
(`SELECT language FROM account.accounts ...`) fails — the column is gone. `tasks.md` orders the
Go rewrite so it cannot be applied against the old schema, and nothing in this tier's task graph
lets the migration land without the Go changes.

### D3 — Settings row created at signup, via an explicit transaction (not a multi-write CTE)

`UpsertFromOAuth` is rewritten to run its two writes — the account upsert and the settings-row
insert — inside one explicit `pgx` transaction:

```go
func (s *service) UpsertFromOAuth(ctx context.Context, id OAuthIdentity) (Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	qtx := s.q.WithTx(tx)

	row, err := qtx.UpsertAccountFromOAuth(ctx, accountdb.UpsertAccountFromOAuthParams{
		Email:       id.Email,
		Provider:    id.Provider,
		ProviderID:  id.ProviderID,
		DisplayName: textFromString(id.DisplayName),
	})
	if err != nil {
		return Account{}, fmt.Errorf("upserting account from oauth: %w", err)
	}

	if err := qtx.InsertSettingsIfMissing(ctx, row.ID); err != nil {
		return Account{}, fmt.Errorf("creating account settings: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("committing tx: %w", err)
	}
	return accountFromRow(row), nil
}
```

backed by:

```sql
-- name: InsertSettingsIfMissing :exec
INSERT INTO account.settings (account_id)
VALUES (@account_id)
ON CONFLICT (account_id) DO NOTHING;
```

`ON CONFLICT (account_id) DO NOTHING` matters because `UpsertFromOAuth` is also the
resolve-existing-account path: a returning user must never have a real settings row (possibly
already changed from the defaults) silently reset. Existing accounts get their row from the
migration's backfill (D2), not from this path.

**Rejected: a single multi-write CTE** (`WITH acct AS (INSERT ... RETURNING *), ins AS (INSERT
INTO settings ...) SELECT * FROM acct`). This would also be atomic — Postgres treats one statement
as one implicit transaction — and would avoid an explicit `Begin`/`Commit`. It was rejected on an
AI-efficiency basis: this module has zero existing multi-CTE writes, but it already has an
established, working pattern for "read/write more than one thing atomically" —
`AccessTokenFor`, four methods above in the same file, already opens a `pool.Begin`, wraps
`s.q` with `WithTx`, and commits. Reusing that pattern means an implementer (human or agent)
touching this file has exactly one atomicity idiom to learn, not two, and a reviewer checking
"is this actually atomic" reads the same shape twice instead of two different SQL/Go split points.
A novel CTE would only pay for its own extra indirection here, with no offsetting benefit — the
account-provisioning path is low-frequency (login, not a hot dashboard read), so the CTE's
one-fewer-round-trip advantage is not worth the added review cost.

### D4 — Defense in depth: what happens if a settings row is somehow missing

D3 and the migration backfill together guarantee every account has exactly one settings row from
the moment it exists. If that invariant is ever violated anyway (a manual DB edit, a bug), reading
it (`GetAccountSettings`, D7) is a `:one` query — it returns `pgx.ErrNoRows`, which propagates as a
wrapped error (`"loading account settings: %w"`), exactly the same failure mode `LanguageFor`
already has *today* for an unknown or `Inactive` account id (both cases already hit
`pgx.ErrNoRows` under the existing `status = 'Active'` filter). No new special-casing is added: a
missing settings row for an otherwise-valid account is indistinguishable, from the caller's side,
from "this account id doesn't resolve" — which is already a real, handled outcome, not a panic and
not a silently-wrong default. This is a deliberate choice not to paper over a data-integrity bug
with a fabricated default; normalization (D6) only ever corrects an out-of-vocabulary *value*, never
a missing *row*.

### D5 — No `CHECK` constraint on `language` or `theme`

Restated for self-containment (roadmap decision D9). Both vocabularies are validated at this
module's sole write path (`SetLanguage`/`SetTheme`) and re-normalized on every read
(`LanguageFor`/`ThemeFor`/`PreferencesFor`), in Go, at the DB→domain boundary
(`ai/go-conventions.md` §Persistence) — exactly mirroring the already-unconstrained `language`
column's existing rule, and the still-unconstrained `provider` column on `accounts`. A `CHECK`
would force a migration to ship ahead of the Go code every time a theme (or a locale) is added;
without one, adding `"cyberpunk"` to `ui.Themes` (tier 2) plus `isSupportedTheme` here is the
entire change — see roadmap decision D10's four-step "adding a theme" list, none of which touches
the database.

**Rejected: `CHECK (theme IN ('apex','graphite','halloween'))`.** Same reasoning as D9's rejection
for `language` — real cost (a DB migration required before any Go-side vocabulary change can even
deploy) for a safety property the sole writer already provides.

### D6 — Theme port pair, mirroring the language pair exactly

New additions to `account.go`:

```go
// ThemeApex, ThemeGraphite, and ThemeHalloween are the three supported theme
// codes — the entire closed vocabulary this module accepts (roadmap RM42
// decision D7). Consumers should reference these constants rather than the
// string literals. The authoritative UI-facing list (used to render the
// dropdown) is ui.Themes in internal/gateway (tier 2); this module validates
// against its own copy of the same closed set, exactly as isSupportedLanguage
// already does for language.
const (
	ThemeApex      = "apex"
	ThemeGraphite  = "graphite"
	ThemeHalloween = "halloween"
)

// ErrUnsupportedTheme is returned by SetTheme when theme is not one of the
// three supported codes. Detect it with errors.Is.
var ErrUnsupportedTheme = errors.New("account: unsupported theme code")
```

```go
// ThemeFor returns the account's current UI theme preference: always exactly
// one of ThemeApex, ThemeGraphite, or ThemeHalloween. A stored value outside
// that set is normalized to ThemeGraphite here, at the DB→domain boundary —
// this method never returns an unsupported code and never fails because of an
// unrecognized stored value; it only errors on an actual lookup failure
// (unknown accountID, DB error). Mirrors LanguageFor exactly.
ThemeFor(ctx context.Context, accountID uuid.UUID) (string, error)

// SetTheme persists theme as the account's UI theme preference. theme MUST be
// one of the three supported codes — any other value returns
// ErrUnsupportedTheme (detect with errors.Is) WITHOUT writing. Mirrors
// SetLanguage exactly.
SetTheme(ctx context.Context, accountID uuid.UUID, theme string) error
```

Why the default is `graphite`, not `apex`: roadmap decision D7, and it matches
`internal/gateway/templates/layouts/base.templ`'s current hardcoded value — a fresh account's
first render looks identical to what every account sees today, before tier 2 ships.

### D7 — One combined read: `PreferencesFor` + `GetAccountSettings`

New domain type and port method:

```go
// Settings is an account's persisted preferences: always exactly one row per
// account (D3/D4), holding both Language (always LanguageES or LanguageEN) and
// Theme (always ThemeApex, ThemeGraphite, or ThemeHalloween).
type Settings struct {
	Language string
	Theme    string
}

// PreferencesFor returns the account's full settings row — Language and Theme —
// in a SINGLE query. Callers that need both values for one render (the
// gateway's per-request context, the /settings page) MUST use this method
// rather than calling LanguageFor and ThemeFor separately, which would cost two
// queries instead of one. Both fields are normalized exactly as
// LanguageFor/ThemeFor do.
PreferencesFor(ctx context.Context, accountID uuid.UUID) (Settings, error)
```

backed by:

```sql
-- name: GetAccountSettings :one
SELECT language, theme FROM account.settings
WHERE account_id = @account_id
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');
```

`LanguageFor` and `ThemeFor` are themselves implemented on top of `PreferencesFor` (which calls
`GetAccountSettings` once), so adding the theme pair introduces **zero** additional SQL queries
anywhere a caller only needs one value — the implementation sketch is in the Migration Plan below.

**This is the answer to the roadmap's binding "one query, both values" constraint.** Without
`PreferencesFor`, a page render needing both preferences (tier 2's per-request context, the
`/settings` page itself) would have no way to get them except calling `LanguageFor` then
`ThemeFor` — two round trips to a two-column, single-row table, a straightforward regression the
roadmap explicitly calls out as unacceptable. `design.md`'s job under this dispatch is to hand
tier 2 a port surface that makes the correct choice the only convenient one: `PreferencesFor`
exists specifically so tier 2's context-population code has a single call that returns everything
a page needs.

**Rejected: two separate single-column queries (`GetAccountLanguage`, `GetAccountTheme`), no
combined method.** This is what a naive "just add a `ThemeFor` mirroring `LanguageFor`" reading of
the ticket would produce. It technically keeps every read narrow, but leaves the "one query per
request" burden on every future caller's discipline rather than on the port's shape — the exact
failure mode the roadmap warns about. A single `GetAccountSettings` query, projected by
`PreferencesFor`/`LanguageFor`/`ThemeFor`, makes the two-query mistake structurally unavailable
through this module's own port.

### D8 — Module and schema ownership (restated for self-containment)

`internal/account`, schema `account` (roadmap decision D5). A preference keyed by `account_id`
cannot own a table across the module boundary (`ai/architecture.md` §2), and a new `settings`
module would force the gateway to depend on two ports (`account` for identity, `settings` for
preferences) to render one page that only ever needs one account's data. `account` already owns
the account row every setting is keyed to.

### D9 — Index plan

Every read and write this tier adds or changes is justified against the actual query, not assumed:

| Query | Predicate | Index used |
|---|---|---|
| `GetAccountSettings` (read, per-request + `/settings` page) | `account_id = @account_id` (PK on `account.settings`) `AND EXISTS (... a.id = ... )` (PK on `account.accounts`) | Both sides ride their table's existing primary-key index. |
| `UpdateAccountLanguage` / `UpdateAccountTheme` (write, user-initiated switch) | Same shape as `GetAccountSettings` | Same two primary-key indexes. |
| `InsertSettingsIfMissing` (write, signup, D3) | `ON CONFLICT (account_id)` | The primary key itself is the conflict target — no separate index. |
| Migration backfill (`INSERT ... SELECT`, one-time) | Full scan of `account.accounts` | No predicate; a one-time, off-hours, small-table (`accounts` has no scale problem today) scan — the read-heavy/write-heavy asymmetry (`ai/architecture.md` §7) explicitly allows this cost off the hot path. |

**Verdict: no secondary index.** Every hot-path query locates its row by a primary key already
guaranteed to exist (`account.settings.account_id` is itself the PK; the `EXISTS` subquery locates
`account.accounts` by its own PK). `theme` and `language` are never predicates, `JOIN` keys, or
`ORDER BY` targets in any query this tier adds — they are always the projected or written columns
of a row already located by PK. With only three (`theme`) or two (`language`) possible values, a
value-keyed index would also have negligible selectivity even if a future query needed one. If a
genuine "list accounts by theme" query is ever needed, add a targeted index then, justified by
that query — not preemptively here (the same YAGNI stance RM24's design.md took for `language`).

### D10 — Carrying forward RM34's Inactive-account gating (not requested by the roadmap, added for consistency)

`GetAccountLanguage` filtered `WHERE id = @id AND status = 'Active'` directly on the `accounts`
row it lived on (RM34 D14/D15): an `Inactive` account's language preference reads and writes as
though the account did not exist, exactly like its vehicles and Tesla token. The roadmap for this
tier does not mention account status at all — it predates RM34 by focus, not by time. Silently
dropping this gate when `language` moves to a different table would be a real regression (an
`Inactive` account's preference would become readable/writable again, undoing RM34), so
`GetAccountSettings`, `UpdateAccountLanguage`, and `UpdateAccountTheme` all carry the identical
`EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status =
'Active')` predicate that `ListVehiclesByAccount`/`GetLatestTeslaTokenByAccount` already use —
`EXISTS`, not a `JOIN`, so the sqlc-generated row shape (`language`, `theme`) is unaffected by the
gate, mirroring D14's own reasoning. `InsertSettingsIfMissing` is deliberately NOT gated, mirroring
`UpsertAccountFromOAuth`'s existing exemption (`internal/account/AGENTS.md`: "`UpsertFromOAuth` is
the one operation NOT filtered by [status]").

**Flag for the leader/user:** this constraint was not stated in the roadmap or the dispatch — it
is inferred from an existing, binding invariant (RM34) that would otherwise silently regress. It
does not touch any binding decision D1–D9 verbatim, but it does add predicates to queries the
roadmap's bullet list didn't itemize. Reviewed and accepted here as the consistent choice; flagged
explicitly rather than silently resolved, per this dispatch's own instruction.

## Test Contract

Authored before the implementation exists, per `ai/go-conventions.md` §Testing ("author their
expected values up front"). All values below are exact.

1. **Backfill preserves every existing account's language.** GIVEN three pre-existing accounts
   with `language` values `'es'`, `'en'`, and `'fr'` (an out-of-set legacy value) respectively,
   WHEN the migration's `Up` runs, THEN `account.settings` has exactly three rows, one per
   account, with `language` equal to `'es'`, `'en'`, and `'fr'` respectively (copied verbatim —
   normalization happens in Go at read time, never in the migration), and `theme = 'graphite'`
   for all three (the column `DEFAULT`, since no account ever had a theme before). AND
   `account.accounts` no longer has a `language` column.
2. **A fresh signup gets a settings row with the defaults.** GIVEN a brand-new `OAuthIdentity`
   with no prior account, WHEN `UpsertFromOAuth` is called, THEN `account.settings` has exactly
   one row for the new account's id with `language = 'es'` and `theme = 'graphite'`, AND
   `PreferencesFor` for that account id returns `Settings{Language: "es", Theme: "graphite"}` with
   no error.
3. **`SetTheme` rejects a value outside the closed set.** GIVEN an existing account whose theme is
   `'graphite'`, WHEN `SetTheme(ctx, id, "cyberpunk")` is called, THEN it returns an error
   satisfying `errors.Is(err, account.ErrUnsupportedTheme)`, AND a subsequent `ThemeFor` for the
   same account still returns `"graphite"` (nothing was persisted).
4. **`ThemeFor`/`LanguageFor` normalize an out-of-set stored value rather than returning it.**
   GIVEN a settings row whose `theme` was set directly via SQL (outside this module's write path)
   to `"neon"`, WHEN `ThemeFor` is called for that account, THEN it returns `"graphite"` with no
   error. GIVEN the same technique applied to `language` set to `"fr"`, WHEN `LanguageFor` is
   called, THEN it returns `"es"` with no error. GIVEN both are set this way on the same row, WHEN
   `PreferencesFor` is called, THEN it returns `Settings{Language: "es", Theme: "graphite"}`.
5. **The `Down` migration restores `accounts.language` with the current values.** GIVEN an account
   whose `language` was changed via `SetLanguage` to `'en'` after signup (so its `account.settings`
   row holds `language = 'en'`), WHEN `goose down` (one step) runs, THEN `account.accounts` has a
   `language` column again, that account's row reads `language = 'en'` (copied from its settings
   row at rollback time — not necessarily whatever value existed before the original `Up` ran, if
   it was changed in between), AND `account.settings` no longer exists.
6. **An `Inactive` account's preferences are gated exactly like its vehicles and token (D10).**
   GIVEN an account with `status = 'Inactive'`, WHEN `PreferencesFor`/`LanguageFor`/`ThemeFor` are
   called for it, THEN each returns the same wrapped-error outcome `LanguageFor` already produces
   today for an `Inactive` account (D4) — no special "inactive" sentinel, just the existing
   not-found-shaped failure. WHEN `SetLanguage`/`SetTheme` are called for it, THEN each is a silent
   no-op (matches zero rows; no error), exactly like `UpdateAccountLanguage`'s documented RM34
   behavior today.

## Migration Plan

1. Add the goose migration exactly as in D2 (`internal/account/db/migrations/20260904000001_add_account_settings.sql`)
   — timestamp chosen as the next unused value after the latest migration in ANY module directory
   as of this proposal (`20260903000004`, in `internal/charging`); confirmed no collision across
   `internal/account`, `internal/telemetry`, `internal/charging`, `internal/analytics` migration
   directories (see the collision check in the final report). Re-verify at implementation time in
   case a sibling tier lands a migration first (`make migration-guard`).
2. Add `Settings`, `ThemeApex`/`ThemeGraphite`/`ThemeHalloween`, `ErrUnsupportedTheme`, and the
   three new `Service` methods (`PreferencesFor`, `ThemeFor`, `SetTheme`) to `account.go` (D6/D7).
   Update `LanguageFor`/`SetLanguage`'s doc comments to note the new backing table (behavior
   unchanged).
3. Replace `GetAccountLanguage` with `GetAccountSettings` (D7), repoint `UpdateAccountLanguage` at
   `account.settings` (D10's `EXISTS` gate), add `UpdateAccountTheme` (mirrors it) and
   `InsertSettingsIfMissing` (D3) in `query.sql`. Run `make sqlc`. **Verify, do not assume**, the
   Go type sqlc infers for `GetAccountSettingsRow.Language`/`.Theme` and the two `Update*Params`
   structs — expected plain `string` for both (both columns are `NOT NULL`, matching every other
   `NOT NULL TEXT` column already in this module), per `ai/go-conventions.md` §Persistence's
   "verify, do not assume" rule. Report the actual generated type.
4. Implement `PreferencesFor`, `LanguageFor`, `ThemeFor`, `SetLanguage`, `SetTheme`,
   `normalizeTheme`, `isSupportedTheme` in `service.go` per the sketches in D3/D6/D7. Rewrite
   `UpsertFromOAuth` to the transactional shape in D3.
5. Add the pure unit tests for `normalizeTheme`/`isSupportedTheme` (mirroring
   `normalizeLanguage`'s existing test). Add `DATABASE_URL`-gated integration tests covering every
   numbered item in the Test Contract above.
6. Update `internal/account/AGENTS.md`'s "Public interface" section: list `PreferencesFor`,
   `ThemeFor`, `SetTheme`; note `LanguageFor`/`SetLanguage` are now backed by `account.settings`.
   Grep `kkpa/context/` for `account`/`language` and fix any guide the change invalidates.
7. Verify `MIGRATIONS_DIRS`, `db-setup`/`db-reset` (both operate at the whole-database/whole-schema
   level, not per-table — confirmed by reading `Makefile`'s `db-reset` target, which drops and
   recreates the entire database, so no hardcoded table list needs updating), `sqlc.yaml` (the
   existing `account` entry already covers the new table via its `schema:` pointing at the
   migrations directory — no structural change needed), and `make migration-guard` (timestamp
   collision check, step 1). Record findings in `tasks.md`.
8. `go build ./...`, `go vet ./...`, `gofmt -l` pass. `openspec validate
   RM42-account-add-settings-table --strict` passes.

**Rollback:** the `-- +goose Down` (D2) restores `accounts.language` with current values and drops
`account.settings`. Tier 2 (a separate, dependent change) cannot exist yet at this point in the
sequence, so no other tier's code needs to roll back first.

## Open Questions

None — roadmap decisions D2–D5, D7, and D9 (this tier's applicable binding decisions) are settled
and restated above. D10 (Inactive-account gating) is this design's own addition, not a roadmap
decision, and is flagged rather than silently resolved (see D10's closing note and item 6 of the
final report).
