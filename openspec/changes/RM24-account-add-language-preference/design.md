## Context

The `account` module owns the `accounts` table (`internal/account/db/migrations/20260707000001_init_account.sql`):
`id`, `email`, `provider`, `provider_id`, `display_name`, `created_at`, `updated_at`, plus
`UNIQUE (provider, provider_id)`. It has no unit-bearing columns and no unit-suffix rule applies
here (`ai/go-conventions.md` §Persistence, `internal/account/AGENTS.md` "Units convention" — this
change adds nothing to that rule's scope, since `language` carries no unit).

MAG-8 (roadmap `RM24-i18n-translations`) needs every signed-in page render to know the user's
language. The `account` module is the only place this preference can live: it is 1:1 with an
account, and `ai/architecture.md` §2 forbids any other module from reading `accounts` directly.
This tier adds the column and the two port methods a consumer (the `gateway` module, tier 2 of the
roadmap) will call — it does not touch `gateway` at all.

Performance profile: **read-heavy** (`ai/architecture.md` §7). This preference is read on every
signed-in page render / htmx fragment — the single most frequent read this module serves, more
frequent than the vehicle registry reads the `access_type`/`vehicle_config` precedents optimized
for. Writes happen only when a user explicitly switches language via the navbar selector (tier 2)
— rare, user-initiated, latency-insensitive.

The direct schema precedents are the module's two prior additive-column changes:
`20260720000001_vehicles_add_access_type.sql` (nullable, `CHECK`-constrained closed vocabulary) and
`20260803000001_vehicles_add_config_fields.sql` (nullable, no `CHECK`, open vocabulary). Read both
before this design — this tier's shape differs from both (a `NOT NULL DEFAULT` column, not
nullable) because, unlike a vehicle attribute copied from an external API, every account has a
truthful default value for its language preference from the moment the row exists.

## Goals / Non-Goals

**Goals:**
- Persist the language preference where it is read: on the `accounts` row itself, so the
  already-loaded row costs nothing extra to also carry the preference, and a dedicated
  single-column lookup (`LanguageFor`) is a single indexed-by-primary-key `SELECT`.
- Guarantee the value handed to a caller is always one of exactly two supported codes — never an
  error, never an unrecognized string — so an invalid or legacy stored value can never break a
  page render (roadmap decision D4).
- Give the `gateway` module (tier 2) a minimal, symmetric read/write pair that never requires it to
  touch the `accounts` table.
- Keep `pgtype` confined to `service.go` / the generated `accountdb` package, exactly as every
  prior persistence change in this module does.

**Non-Goals:**
- Anonymous/cookie-based language resolution — roadmap decision D2, entirely a `gateway`-tier
  concern (no account row exists for an anonymous visitor). Not designed here.
- The translation catalogue, the navbar selector, the htmx switch mechanism, or any Templ/HTML —
  all tier 2/3, and all forbidden inside this module by `ai/architecture.md` §2 ("no HTML inside
  domain modules").
- A third locale, a locale-negotiation header, or per-field translation metadata — strict YAGNI;
  the roadmap fixes the vocabulary at exactly `{es, en}` (decision D4).
- A general-purpose "fetch the whole `Account` by id" port method. The gateway's session today
  stores only the account id (`internal/gateway/handlers/handlers.go` `currentUID`), never a full
  `Account` struct, on every subsequent request after login — only `UpsertFromOAuth` (at login)
  returns one. Adding a broad `AccountByID` method would be scope creep this tier's mandate
  ("read the current language... and persist a change to it") does not ask for, and how the
  gateway fetches per-request context is tier 2's design call, not this tier's.

## Decisions

### D1 — Schema: `language TEXT NOT NULL DEFAULT 'es'` on `accounts`, no `CHECK`

Exact DDL (goose migration, both directions):

```sql
-- +goose Up
ALTER TABLE accounts
    ADD COLUMN language TEXT NOT NULL DEFAULT 'es';

-- +goose Down
ALTER TABLE accounts
    DROP COLUMN IF EXISTS language;
```

**Why a column on `accounts`, not a new `account_settings` table (roadmap decision D1 — binding,
restated here per the design-gate requirement that this document be self-contained).** There is
exactly one preference today, it is strictly 1:1 with the account, and it is read on **every**
signed-in page render — the hottest read this module serves. A column keeps it on the row that is
either already loaded (once the gateway's per-request account fetch exists) or trivially fetched
by primary key, with zero joins. The rejected alternative, a separate `account_settings` table
keyed by `account_id`, was considered and rejected: for a single field it buys nothing and costs
(a) a `JOIN` or a second query on every render, (b) a second sqlc query set duplicating what a
plain column gives for free, and (c) a "settings row does not exist yet" fallback case that a
`NOT NULL DEFAULT` column makes structurally impossible. Revisit only if per-account preferences
genuinely multiply (units, timezone, theme) — a future change, not this one, and specifically not
speculatively built ahead of a second real preference (YAGNI).

**Why `NOT NULL DEFAULT 'es'`, not nullable (the deliberate divergence from the `access_type` /
`vehicle_config` precedents).** Those two prior columns are nullable with no `DEFAULT` because a
migration cannot honestly assign a value Tesla hasn't reported yet — `NULL` there means "not yet
observed from the external API." A language preference has no such external-observation gap: the
product's default **is** `'es'` (roadmap decision D3, stated by the ticket), so every existing row
can take it truthfully and immediately, with zero backfill and zero "not yet set" state to model.
Making the column `NOT NULL` also removes an entire class of caller-side nil-handling that the
`*string` fields on `Vehicle`/`OwnedVehicle` require — every reader gets a real, always-populated
`string`.

**Why no `CHECK` constraint.** The supported vocabulary (`{"es", "en"}`) is validated at the
module's sole write path (`Service.SetLanguage`, D3 below) and defensively re-normalized on every
read (`Service.LanguageFor`, D3 below) — both in Go, at the DB→domain boundary
(`ai/go-conventions.md` §Persistence). A `CHECK (language IN ('es','en'))` would enforce the same
rule one layer lower, but at a real cost: adding a third locale later (plausible — the roadmap
itself notes "revisit only if... multiply" for settings in general, and locale count is exactly
the kind of thing that grows) would need a migration to widen the `CHECK` before the Go code could
even be deployed, serializing an app-level change behind a DB change for no additional safety this
column doesn't already have from its sole writer. This mirrors an existing precedent in the same
table: `provider` is not `CHECK`-constrained to `'google'` even though it currently has exactly one
value — the closed set is enforced by the code that writes it, not the schema. Rejected: a
`CHECK (language IN ('es','en'))` constraint, for the reason above.

### D2 — No index on `language`

Justified against the actual and planned queries that touch `accounts`:

- **`GetAccountLanguage`** (new, D3) — `SELECT language FROM accounts WHERE id = @id`. The row is
  located by the primary key `id` — already indexed (the PK index every Postgres table has by
  construction). `language` is the projected column, never a predicate.
- **`UpdateAccountLanguage`** (new, D3) — `UPDATE accounts SET language = @language, updated_at =
  now() WHERE id = @id`. Same primary-key lookup; `language` is the column being written, never a
  predicate.
- **`UpsertAccountFromOAuth`** / **`GetAccountByProviderID`** (existing) — both already locate their
  row via `id` or the `UNIQUE (provider, provider_id)` constraint. `SELECT *` / `RETURNING *`
  already include the new column automatically (both use `*`, not an explicit column list) —
  adding `language` to what they return costs nothing beyond fetching a few more bytes from a row
  already located.

**Verdict: no new index.** `language` is never a `WHERE`, `JOIN`, or `ORDER BY` predicate in any
existing or planned query — it is read and written exclusively as part of a row already located by
`id` (primary key) or `(provider, provider_id)` (existing unique constraint). An index on
`language` would add write cost to every future `UPDATE`/`INSERT` on `accounts` for zero read
benefit: with only two possible values, a `language`-keyed index would also have essentially no
selectivity, so even a hypothetical future "list accounts by language" query would not benefit from
one. If such a query is ever genuinely needed, add a targeted index then, justified by that query —
not preemptively here.

### D3 — Port methods: `LanguageFor` (read) + `SetLanguage` (write), plus a closed constant pair

New additions to `account.Service` (`internal/account/account.go`):

```go
// LanguageES and LanguageEN are the two supported language codes — the entire
// closed vocabulary this module accepts (roadmap RM24 decision D4). Consumers
// should reference these constants rather than the string literals.
const (
	LanguageES = "es"
	LanguageEN = "en"
)

// ErrUnsupportedLanguage is returned by SetLanguage when lang is not one of the
// two supported codes. Detect it with errors.Is.
var ErrUnsupportedLanguage = errors.New("account: unsupported language code")
```

```go
// LanguageFor returns the account's current language preference: always exactly
// LanguageES or LanguageEN. A stored value outside that set (a legacy row, a
// manual DB edit, a locale removed from a future supported set) is normalized to
// LanguageES here, at the DB→domain boundary — this method never returns an
// unsupported code and never fails because of an unrecognized stored value; it
// only errors on an actual lookup failure (unknown accountID, DB error).
LanguageFor(ctx context.Context, accountID uuid.UUID) (string, error)

// SetLanguage persists lang as the account's language preference. lang MUST be
// LanguageES or LanguageEN — any other value returns ErrUnsupportedLanguage
// (detect with errors.Is) WITHOUT writing, so a caller (the gateway's language
// switch handler) does not have to duplicate this module's validation.
SetLanguage(ctx context.Context, accountID uuid.UUID, lang string) error
```

Backing sqlc queries (`internal/account/db/query.sql`):

```sql
-- name: GetAccountLanguage :one
-- The per-request read path: only the language column, not the whole account row,
-- so a caller that only needs the language does not pay for the rest of Account.
SELECT language FROM accounts
WHERE id = @id;

-- name: UpdateAccountLanguage :exec
-- Persists an explicit language switch. Vocabulary validation happens in the Go
-- caller (Service.SetLanguage) before this query runs — see D1 for why there is
-- no CHECK constraint doing this at the DB layer instead.
UPDATE accounts
SET language   = @language,
    updated_at = now()
WHERE id = @id;
```

**Expected sqlc output — verify, do not assume.** Because `language` is `NOT NULL`, sqlc is
expected to infer plain `string` for both `GetAccountLanguage`'s return value and
`UpdateAccountLanguageParams.Language` — no `pgtype.Text` involved, unlike every nullable-column
precedent in this module (`AccessType`, `ExteriorColor`, `CarType`, `DisplayName`). This is a
reasonable expectation based on how sqlc has handled this module's other `NOT NULL TEXT` columns
(e.g. `email`, `vin` are plain `string` in the generated `Account`/`Vehicle` structs), but per the
`vehicle_config` tier's precedent (which found sqlc's inferred type for a nullable bind param was
not what was assumed), the implementer MUST inspect what `sqlc generate` actually produces and
adjust the service implementation accordingly rather than assume `string` — and report which one it
produced.

**Implementation sketch** (for the implementer; not itself a spec requirement):

```go
func (s *service) LanguageFor(ctx context.Context, accountID uuid.UUID) (string, error) {
	lang, err := s.q.GetAccountLanguage(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("loading account language: %w", err)
	}
	return normalizeLanguage(lang), nil
}

func (s *service) SetLanguage(ctx context.Context, accountID uuid.UUID, lang string) error {
	if !isSupportedLanguage(lang) {
		return ErrUnsupportedLanguage
	}
	if err := s.q.UpdateAccountLanguage(ctx, accountdb.UpdateAccountLanguageParams{
		ID:       accountID,
		Language: lang,
	}); err != nil {
		return fmt.Errorf("setting account language: %w", err)
	}
	return nil
}

// normalizeLanguage returns lang unchanged if it is a supported code, else the
// default LanguageES. This is the DB→domain normalization boundary (D4).
func normalizeLanguage(lang string) string {
	if isSupportedLanguage(lang) {
		return lang
	}
	return LanguageES
}

func isSupportedLanguage(lang string) bool {
	return lang == LanguageES || lang == LanguageEN
}
```

**Why two narrow methods, not one method bundled onto `Account`.** The tier's mandate (roadmap
RM24 tier-1 scope, `proposal.md` "What Changes") is exactly "read the current language for an
account and persist a change to it" — a symmetric read/write pair. Adding `Language string` to the
`Account` struct instead was considered: it would make the value free whenever an `Account` is
already loaded, but today the gateway's session carries only the account id after login (see
Non-Goals) — there is no existing "loaded `Account`" for `LanguageFor` to ride along with on a
typical page render, so bundling it into `Account` would not save a query today and would still
require tier 2 to decide how/when it re-fetches the full `Account` per request, which is exactly
the scope-creep this design avoids. `LanguageFor` is deliberately a single-column, single-row
lookup so tier 2 can call it as cheaply as possible however it ends up resolving language per
request.

**Why `ErrUnsupportedLanguage` on the write path but silent normalization on the read path.** A
write is a deliberate caller action (the roadmap's own request: "an unrecognized stored value must
resolve to `es` rather than error" is explicitly about *stored*, i.e. already-persisted, values,
not about a caller's live input) — rejecting an invalid write outright gives the caller a clear,
actionable signal (e.g. a malformed request body) rather than silently coercing a mistake to `es`
and hiding a bug. A read, by contrast, must never fail or surface an invalid value to a page
render (roadmap D4, verbatim: "an invalid stored value must never be able to break a page render")
— normalizing silently is the correct behavior there because the caller did not just make a
mistake; historical or externally-mutated data did.

## Risks / Trade-offs

- **[Risk]** A future third locale requires both a code change (widen `isSupportedLanguage`,
  `normalizeLanguage`, the two constants) and, separately, no DB migration at all (no `CHECK` to
  widen) → **Mitigation**: this is the intended benefit of D1's "no `CHECK`" choice — the DB change
  needed to add a locale is zero; only Go code changes, which is the desired minimal-friction
  surface for extending the vocabulary later.
- **[Trade-off]** Without a `CHECK`, a direct SQL edit (outside this module's Go code) could store
  a value other than `'es'`/`'en'` → accepted: `LanguageFor`'s normalization makes this harmless at
  read time by design (D3/D4) — the whole point of the normalization guarantee is that no stored
  value, however it got there, can break a render. This is the same trade-off already accepted for
  `provider` on the same table.
- **[Risk]** `LanguageFor` is called on every page render; a lookup failure (DB down, unknown
  accountID) surfaces as an error to a caller that may not have a graceful fallback → **Mitigation**:
  out of scope for this tier — how the gateway degrades on a language-lookup failure (e.g.
  falling back to `es` rather than failing the whole render) is tier 2's design call, exactly as it
  owns the cookie fallback for anonymous visitors (roadmap D2).

## Migration Plan

1. Add goose migration: `ALTER TABLE accounts ADD COLUMN language TEXT NOT NULL DEFAULT 'es'` (D1)
   — no `CHECK`, no index (D2).
2. Add `LanguageES`/`LanguageEN` constants and `ErrUnsupportedLanguage` to `account.go`. Add
   `LanguageFor` and `SetLanguage` to the `Service` interface (D3).
3. Add `GetAccountLanguage` and `UpdateAccountLanguage` to `query.sql` (D3). Run `make sqlc`.
   Confirm and report the actual Go type sqlc infers for both (expected: plain `string`, per D3's
   "verify, do not assume" note).
4. Implement `LanguageFor` and `SetLanguage` in `service.go`, plus the unexported
   `normalizeLanguage`/`isSupportedLanguage` helpers.
5. Add a pure unit test for `normalizeLanguage` (no DB) covering both supported codes and at least
   one unrecognized value. Add `DATABASE_URL`-gated integration tests covering: a fresh account's
   default (`'es'` with no explicit write), a successful `SetLanguage` + `LanguageFor` round-trip
   for both codes, `SetLanguage` rejecting an unsupported code (`ErrUnsupportedLanguage`, no write
   occurs), and `LanguageFor` normalizing a value written directly via `pool.Exec` outside the
   `{es, en}` set back to `'es'`.
6. Update `internal/account/AGENTS.md` "Public interface" section to list `LanguageFor` and
   `SetLanguage`.
7. `go build ./...`, `go vet ./...`, `go test ./...` pass (integration tests self-skip without
   `DATABASE_URL`).

**Rollback:** the `-- +goose Down` drops the column; any tier-2/3 code depending on it (separate,
dependent changes) would need to be rolled back first — this tier's Down migration is
self-contained and does not touch any other table.

## Open Questions

None — roadmap decisions D1, D3, and D4 (this tier's applicable binding decisions) are settled and
restated above. D2 (anonymous cookie fallback) and D5 (no re-grill needed) belong to the roadmap
and tier 2 respectively, not this design.
