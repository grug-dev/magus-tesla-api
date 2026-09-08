# account.Settings — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `account settings`, `user preferences`, `per-user preferences`, `settings row`
  (UI/business)
- **Internal name:** `account.Settings` — table `account.settings`, one row per account, PK
  `account_id`. Holds three fields today: `language`, `theme`, and `analysis_start_date`
  (the first day, in `America/Bogota`, the platform analyzes the account's vehicle data —
  added by `RM49-account-add-analysis-start-date` tier 1, MAG-55). `analysis_start_date` is
  read-only: it is set once at signup from `internal/clock`, and this module has no write
  port for it yet.

## Component map

> Not mapped yet. This guide was created by a spec sync (`apply-sync`), and a capability
> `spec.md` carries behavior, not file paths. Run `/kkpa-context-curate account settings` to
> discover and record the component map.

## How maintenance works

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

## Conventions & gotchas

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
- **All three fields come back in ONE lookup.** A caller that needs more than one field for
  one render must use the combined port (`PreferencesFor`); adding a second round trip
  regresses the guarantee this requirement exists to make. _Source: spec account —
  Requirement: Settings Row Guaranteed At Account Creation._
- **`analysis_start_date` is read-only in this module.** There is no write port. A single-field
  reader, `AnalysisStartDateFor`, is built on top of `PreferencesFor` — same combined-lookup
  rule as `LanguageFor`/`ThemeFor`. The value is computed once, at signup, from
  `internal/clock` (never a raw `time.Now()` or a SQL `CURRENT_DATE`, both of which use the
  wrong time zone). _Source: `RM49-account-add-analysis-start-date` design.md D4/D5._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file.

- Architecture: `architecture/schema-per-module.md` (why `account.settings` lives in the
  `account` schema), `architecture/account-activation-gate.md` (the account module's other
  read-time rule)
- Use cases: the gateway's theme and language switches call the account module's write ports;
  see `internal/gateway/AGENTS.md` §"Exception: language switch" / §"Exception: theme switch".
- Read consumers of `AnalysisStartDateFor`: the gateway's `/external-charges` form reads it to
  reject a `charged_on` before that date — see `input-port/charging/external-charges.md`
  (RM49 tier 2, MAG-55).
