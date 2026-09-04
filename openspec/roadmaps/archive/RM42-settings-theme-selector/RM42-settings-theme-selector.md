# RM42 — Settings page & theme selector

Source ticket: MAG-43 — https://linear.app/magus-monitor/issue/MAG-43/theme-selector-settings-page

Turn the placeholder "Settings" nav item into a real page at `/settings`, and put a
working theme selector on it. Along the way, give per-user preferences a proper home:
a new `account.settings` table that also absorbs the existing `accounts.language`
column, so the platform has exactly ONE place for a user preference.

## Tier table

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM42-account-add-settings-table` | `internal/account` | New `account.settings` table (typed `language` + `theme` columns, PK `account_id`). Migrate `accounts.language` into it and drop the column. Row created at signup in the same transaction; existing rows backfilled. Rewrite `LanguageFor`/`SetLanguage` against the new table; add `ThemeFor`/`SetTheme` + `ErrUnsupportedTheme`. Extend the `account.Service` port. sqlc regen. | — | Create the OpenSpec artifacts for `RM42-account-add-settings-table`. Binding decisions D1–D5 and D9 in this roadmap. design.md MUST carry the full schema, the rationale (including why a column on `accounts` and a key/value EAV were both rejected), and the index plan against the read patterns — the `database` design gate applies. One migration, strict order CREATE → INSERT..SELECT backfill → DROP COLUMN, with a Down that recreates the column and copies values back. Migration and the Go rewrite ship in the SAME tier. Verify `MIGRATIONS_DIRS`, `db-setup`/`db-reset` ownership assumptions, `sqlc` and `make migration-guard` still hold, and record what you found. |
| `[x]` | `RM42-gateway-add-theme-selector` | `internal/gateway` | Real `/settings` page (drop `Placeholder: true` from the nav item). `ui.Themes` closed vocabulary + `ui.ThemeSwitcher` dropdown. `POST /ui/theme/switch` handler + `theme` cookie mirroring the `lang` cookie. `data-theme` on `base.templ` reads from ctx instead of the hardcoded `"graphite"`. Instant client-side apply in `app.js`. ES+EN catalogue entries. New `make theme-guard` + wired into `make check`. Docs: `AGENTS.md`, `README.md`. | tier 1 | Create the OpenSpec artifacts for `RM42-gateway-add-theme-selector`. Binding decisions D1, D6–D11 in this roadmap. Mirror the MAG-8 language slice file for file — `handlers/lang.go`, `ui.LangSwitcher`, the `LanguageMiddleware` context plumbing. Do NOT invent a new pattern. Theme names render as-is (proper nouns, never translated); only the dropdown label goes in the catalogue. Read `internal/account`'s port only through its public Go interface. |

## Decisions (binding on every tier)

Settled with the user on 2026-09-04, before any artifact was written. Workers treat
these as given and never re-open them.

- **D1 — Feasible as specified.** All themes are already compiled into one `app.css`
  (`internal/gateway/static/input.css` registers `apex`, `graphite`, `halloween`).
  Switching is one `data-theme` attribute on `<html>`
  (`internal/gateway/templates/layouts/base.templ:17`, hardcoded to `graphite` today).
  No per-user CSS build is needed.

- **D2 — New `account.settings` table, typed columns, never key/value EAV.**

  ```sql
  CREATE TABLE account.settings (
      account_id UUID PRIMARY KEY REFERENCES account.accounts(id) ON DELETE CASCADE,
      language   TEXT NOT NULL DEFAULT 'es',
      theme      TEXT NOT NULL DEFAULT 'graphite'
  );
  ```

  `account_id` as PK gives one row per user and a plain PK lookup — no extra index.
  EAV was rejected: it loses `NOT NULL`, defaults and types, and an agent cannot
  discover the available keys by reading the schema.

- **D3 — `accounts.language` moves into `account.settings` in tier 1, and the column is
  dropped.** Split storage (theme in the new table, language left on `accounts`) was
  explicitly considered and REJECTED — two homes for preferences is the worst outcome,
  because every future setting then starts with "which one do I use?". One migration,
  strict order: `CREATE` → `INSERT..SELECT` backfill → `DROP COLUMN`; the `Down`
  recreates the column and copies the values back. `LanguageFor` / `SetLanguage` are
  rewritten in the SAME tier — dropping the column breaks `GetAccountLanguage` the
  moment the migration runs.

- **D4 — The settings row is created at signup, in the same transaction** as the account
  insert. Reads are then a plain PK lookup with no `COALESCE` and no missing-row branch.
  Existing accounts are backfilled by the migration.

- **D5 — Module and schema: `internal/account`, schema `account`.** NOT a new `settings`
  module. A preference keyed by `account_id` cannot own a table across the module
  boundary, and the gateway would need two ports to render one page. The `account`
  module already owns the user.

- **D6 — Apply is instant, with no page reload.** JS sets
  `document.documentElement.dataset.theme` immediately, then the choice is persisted in
  the background. This deliberately does NOT copy the language switcher's `HX-Location`
  reload: language is server-rendered text and needs a re-render, a theme is CSS-only
  and does not.

- **D7 — All three themes are offered: `apex`, `graphite`, `halloween`.** The user chose
  all three knowing `halloween` is an unstyled daisyUI builtin. Tier 2 carries an
  explicit task to check the dashboard charts and tiles under `halloween` and report
  anything that looks broken — a report, not a redesign.

- **D8 — The Settings nav item loses `Placeholder: true` and its "Soon" badge** and
  becomes a real page at `/settings`
  (`internal/gateway/templates/layouts/nav.go:32`).

- **D9 — No `CHECK` constraint on `theme` (or `language`).** The vocabulary is validated
  in Go at the module's single write path and re-normalized on every read, exactly
  mirroring the existing `accounts.language` precedent. This is what makes "add a theme
  later" need no migration.

- **D10 — `ui.Themes` is the single closed vocabulary, guarded by `make theme-guard`.**
  One exported Go slice feeds BOTH the dropdown and `isSupportedTheme` validation. A new
  grep-based `theme-guard` (mirroring `boundary-guard`'s shape and escape-hatch
  convention) fails when that slice and `static/input.css` disagree, and is wired into
  `make check`. Deriving the list by parsing `input.css` at startup was rejected:
  `halloween` is not an `@import` — it lives in the `@plugin { themes: ... }` block — so
  a parser must handle two shapes, stays fragile to any CSS reshuffle, and moves errors
  from build time to run time.

  **Adding a theme later is therefore four steps**, and tier 2 MUST document them in
  `internal/gateway/AGENTS.md` and `README.md` §"Switching the theme":
  1. new `internal/gateway/static/themes/<name>.css` — one `@plugin` block, mirror `graphite.css`
  2. one `@import` line in `internal/gateway/static/input.css`
  3. add `"<name>"` to `ui.Themes`
  4. `make css`

- **D11 — Theme names are proper nouns and are never translated.** `Apex`, `Graphite`,
  `Halloween` render as-is; only the dropdown's own label ("Theme" / "Tema") is an i18n
  key. If each theme needed two catalogue entries, adding one would also touch
  `i18n-guard` — this keeps step 3 above to a single line.

## Out of scope

**Read caching is NOT part of this roadmap.** `handlers.LanguageMiddleware` does a DB
read on every request from a signed-in user — including `/static/*`, because it is
registered with `r.Use` before `r.Static`. The fix (cookie-first reads, and excluding
`/static` + `/healthz` from the middleware) is filed as **MAG-47**, blocked by MAG-43:
https://linear.app/magus-monitor/issue/MAG-47/cache-settings-in-cookies-stop-reading-the-db-on-every-request

This roadmap must not regress the current cost: because `account.settings` returns
`language` AND `theme` in one row, it stays at ONE query per request. One query, both
values. See also `openspec/roadmaps/backlog.md` §24.
