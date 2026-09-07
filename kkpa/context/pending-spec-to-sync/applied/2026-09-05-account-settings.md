# Sync proposal — account (per-account settings)

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/account-settings/guide.md`   <!-- NEW guide; nothing in the KB covers per-account preferences today -->
Source spec:  `openspec/specs/account/spec.md`       <!-- delta added by RM42 tier 1, archived as openspec/changes/archive/account/2026-09-04-RM42-account-add-settings-table -->
Generated:    2026-09-04
Status: APPLIED 2026-09-05

---

<!--
REVIEWER NOTES — read before applying:

1. TARGET IS A NEW GUIDE. The `account` capability spec is broader than this delta, but the two
   ADDED requirements are entirely about the per-account preference row, so they are proposed as
   their own entity guide rather than smeared across the existing architecture/ topics. If you
   would rather fold them into `architecture/schema-per-module.md`, change Target guide above and
   the [index] table below before applying.

2. NO `## Component map` BLOCK — spec.md carries behavior, not file paths. Apply leaves the
   Component map untouched (empty for a new guide). Run `/kkpa-context-curate account settings`
   afterwards if you want the file map discovered and confirmed properly.

3. THE LAST TWO [index] ROWS (`theme preference`, `language preference`) are arguably the
   entity's two ATTRIBUTES rather than aliases, which the curate rules say not to index. They are
   proposed anyway because they are the words MAG-43/MAG-47 actually use and are what a future
   session will type. Delete those two rows if you disagree — the concept row still routes them.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `account settings`, `user preferences`, `per-user preferences`, `settings row`
  (UI/business)
- **Internal name:** `account.Settings` — table `account.settings`, one row per account, PK
  `account_id`. Holds exactly two preferences today: `language` and `theme`.

## [guide] ## How maintenance works — APPEND

- **Read a preference:** always through the account module's public port. There is a combined
  port that returns language AND theme from one underlying lookup; the single-preference reads
  are built on top of it, so no render path pays two lookups for two preferences.
- **Write a preference:** through the account module's public write port only. The write
  validates against the closed vocabulary and persists nothing when the value is rejected.
- **Add a new preference:** add the column to `account.settings` and extend the account module's
  combined read port to carry it — the "one lookup returns every preference" guarantee is the
  constraint that any new field must not break.
- **Add a new theme code:** the supported set is validated in the account module's write path and
  re-normalized on every read, not by a database constraint, so widening the vocabulary is a code
  change with no migration.

## [guide] ## Conventions & gotchas — APPEND

- **The theme vocabulary is closed to exactly three codes — `apex`, `graphite`, `halloween` — and
  `graphite` is the default for every account.** Anything outside the set is not a valid theme.
  _Source: spec account — Requirement: Per-Account Theme Preference._
- **Reads normalize; they never fail.** A stored theme value outside the supported set (a legacy
  row, or one written outside this module's write path) is returned as `graphite` with no error.
  Never add a read path that propagates a "bad stored value" error to the caller.
  _Source: spec account — Requirement: Per-Account Theme Preference._
- **Writes reject rather than coerce.** Persisting an unsupported code is refused and the
  previously stored value is left unchanged — a rejected write is a no-op, not a silent
  fallback to the default. _Source: spec account — Requirement: Per-Account Theme Preference._
- **No other module reads or writes the preference by any means but the account module's public
  ports.** The gateway's theme switch calls the interface; it never touches the settings table.
  _Source: spec account — Requirement: Per-Account Theme Preference._
- **Every account has exactly one settings row, always.** It is created in the same atomic
  operation as the account itself, and accounts predating the table were backfilled. There is
  therefore no "no settings row yet" state to branch on — missing-row and at-its-default are the
  same state, so a read needs no COALESCE and no missing-row path.
  _Source: spec account — Requirement: Settings Row Guaranteed At Account Creation._
- **Both preferences come back in ONE lookup.** A caller that needs language and theme for one
  render must use the combined port; adding a second round trip regresses the guarantee this
  requirement exists to make. _Source: spec account — Requirement: Settings Row Guaranteed At
  Account Creation._

## [index] ## Glossary & routing — entities — ADD ROWS

| `account settings` (the one-row-per-account preference record: `language` + `theme`, PK `account_id`) | `account.Settings` — table `account.settings` | entity | `entities/account-settings/guide.md` |
| `user preferences` | synonym of `account settings` → `entities/account-settings/guide.md` | entity | `entities/account-settings/guide.md` |
| `theme preference` | `account.settings.theme` — closed vocabulary `apex` / `graphite` / `halloween`, default `graphite` | entity | `entities/account-settings/guide.md` |
| `language preference` | `account.settings.language` — moved off `accounts.language` by RM42 tier 1, which dropped that column | entity | `entities/account-settings/guide.md` |

---

## APPLY-TIME DECISIONS (2026-09-05)

Reviewer note 1 — **kept the new entity guide** at `entities/account-settings/guide.md` rather
than folding the two requirements into `architecture/schema-per-module.md`. They describe a
record with its own table, defaults and vocabulary; that is an entity, not a schema-layout topic.

Reviewer note 3 — **kept the `theme preference` and `language preference` INDEX rows.** The
curate rules say not to index a concept's attributes, but here the two attributes ARE the whole
entity (the row holds nothing else), so there is no per-field maintenance burden to avoid, and
they are the words MAG-43/MAG-47 use.

Facts verified against the code before applying: `account.Settings{Language, Theme}`
(`internal/account/account.go`); `CREATE TABLE account.settings` with `account_id` PK,
`language` default `'es'`, `theme` default `'graphite'`, and `ALTER TABLE account.accounts DROP
COLUMN language` in the same migration (`20260904000001_add_account_settings.sql`); the theme
vocabulary is exactly `apex` / `graphite` / `halloween` in both `account`'s constants and
`ui.Themes`, reconciled by `make theme-guard`.
