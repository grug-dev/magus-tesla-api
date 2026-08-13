Source: MAG-8 — https://linear.app/magus-monitor/issue/MAG-8/i18n-translations-htmx
Roadmap: openspec/roadmaps/RM24-i18n-translations.md
Tier: 1 of 3 (account; tier 2 is `RM24-gateway-add-i18n-foundation`, tier 3 is
`RM24-gateway-translate-all-pages`, both module `gateway`, both depending on this tier)

## Why

MAG-8 makes the htmx web layer bilingual Spanish/English. That requires a persisted per-user
language preference, and `internal/account` is the only module allowed to own it: the preference
is 1:1 with an account, and `accounts` is `account`'s table — no other module may touch it
(`ai/architecture.md` §2: "no cross-module database leaks"). Without a port method exposing this
value, the `gateway` module (tier 2/3 of this roadmap) would have no way to read or persist a
signed-in user's language choice without reaching into `account`'s database directly, which the
architecture forbids.

This is **tier 1 of 3** of roadmap `RM24-i18n-translations`
(`openspec/roadmaps/RM24-i18n-translations.md`), which captures the binding decisions (D1–D5)
agreed with the user before any artifact was written. This proposal and its sibling artifacts
implement decision D1 (column, not a settings table), D3 (default `es`), and D4 (closed `{es, en}`
vocabulary, invalid-value normalization) for the `account` module only. Tier 2
(`RM24-gateway-add-i18n-foundation`, owned by `internal/gateway`, depends on this tier) consumes
the port method this tier adds to build the translation catalogue, per-request language
resolution, and the navbar selector — none of that is in scope here.

## What Changes

- **Migration** — a new goose migration adds `language TEXT NOT NULL DEFAULT 'es'` to `accounts`.
  No `CHECK` constraint (see `design.md` D1 for why), no new index (see `design.md` D2).
- **sqlc regeneration** — two new queries (`GetAccountLanguage`, `UpdateAccountLanguage`) are added
  to `internal/account/db/query.sql`; `make sqlc` regenerates `accountdb` to pick up the new
  column and queries. Since the column is `NOT NULL`, sqlc is expected to infer plain `string` for
  both the read result and the write bind param — no `pgtype.Text` conversion needed (unlike the
  module's existing nullable-column precedents). The implementer must verify this against what
  `sqlc generate` actually produces rather than assume it (`ai/go-conventions.md` §Persistence).
- **New port methods** on `account.Service` — `LanguageFor(ctx, accountID) (string, error)` (read)
  and `SetLanguage(ctx, accountID, lang string) error` (write), plus two exported constants
  (`LanguageES`, `LanguageEN`) and a new sentinel error `ErrUnsupportedLanguage`. Full signatures,
  semantics, and rationale in `design.md` D3.
- **Normalization** — `LanguageFor` normalizes any stored value outside `{"es", "en"}` to `"es"` at
  the DB→domain boundary (`ai/go-conventions.md` §Persistence), so a legacy or manually-edited row
  can never break a page render. `SetLanguage` validates its input against the same closed set
  before writing and rejects anything else with `ErrUnsupportedLanguage`.
- **Tests** — `DATABASE_URL`-gated integration round-trip tests for both new port methods,
  following the existing `TestAccessType_RoundTrip` / `TestSetVehicleConfigIfEmpty_RoundTrip`
  shape, plus a pure unit test for the normalization helper (no DB needed).
- **Docs** — `internal/account/AGENTS.md`'s "Public interface" section is updated to list the two
  new port methods (docs-track-change rule, `CLAUDE.md`).

**Not breaking.** Adding one `NOT NULL DEFAULT 'es'` column is backward-compatible: existing rows
receive `'es'` automatically at migration time, no backfill script needed. No existing method
signature changes — `account.Service` gains two new methods, which is additive to the interface,
not a change to any existing one. No other module is touched by this tier.

**Affected modules:** `internal/account` (implements this tier). `internal/gateway` is affected
only in the sense that it is the intended future consumer (tier 2, a separate dependent change,
not touched here). No other module reads or writes `accounts.language`.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `account` capability only)

### Modified Capabilities

- `account`: a new requirement, "Per-Account Language Preference," covering the `language` column,
  the closed `{es, en}` vocabulary, the `'es'` default, the read/write port methods, and the
  never-error-on-an-unrecognized-stored-value normalization guarantee.

## Impact

- `internal/account` — new migration, one new domain constant pair + one new sentinel error, two
  new port methods, two new sqlc queries, sqlc regeneration, service implementation, unit +
  integration tests, `AGENTS.md` update.
- `internal/gateway` — **not touched by this tier.** Tier 2 of the roadmap
  (`RM24-gateway-add-i18n-foundation`) is the only consumer of the new port methods; it is a
  separate, dependent OpenSpec change and cannot start until this tier's port methods exist
  (`go build` fails on the missing methods until then — the intended ordering signal, per the
  roadmap's "Ordering" section).

**Read path affected:** any future per-request account/language lookup the gateway performs
(tier 2's design, not this tier's). Today, `LanguageFor` is a new, narrowly-scoped single-column
read (`SELECT language FROM accounts WHERE id = @id`) located by the existing primary key — see
`design.md` D2 for the full index-plan justification (no new index, zero dashboard hot-path
degradation).
