# Clock Sub-Agent

Agent-Name: `clock`

Per-module instructions for `internal/clock/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

*(empty — `internal/clock` needs nothing beyond the base pack)*

## Responsibility

Owns the platform's single default time zone (`America/Bogota`) and the primitives every
other module uses to obtain "now" or normalize a moment to its calendar day. It is a pure,
in-memory function library — no table, no query, no config, no external state.

**It is adopted platform-wide and `make tz-guard` enforces it.** The guard fails any raw
`time.Now()`, hardcoded zone name, or hand-rolled UTC-midnight truncation under `internal/`.
The escape hatch is a `// tz:allow: <reason>` comment, and there are two standing exemptions:
the `pgtype.Date` UTC-midnight storage encoding, and the gateway's per-user `browser_tz`
cookie, which still wins for a signed-in user.

## Public interface (the module's port)

Consumers call these functions directly — this module has no data to protect behind an
interface (see `internal/clock/clock.go` and `internal/clock/calendar.go`):

- `Zone() *time.Location` — the platform's default zone, `America/Bogota`, computed once at
  package init via `time.LoadLocation`. **Panics at init if the load fails** — there is no
  silent UTC fallback (design.md D9 of `RM35-clock-add-bogota-time-package`).
- `Now() time.Time` — the current moment, expressed in `Zone()` (`time.Now().In(Zone())`).
- `LoadOrDefault(name string) *time.Location` — resolves `name` via `time.LoadLocation`,
  falling back to `Zone()` only on a non-nil error, with no special-casing beyond that. Note
  the stdlib gotcha this inherits unchanged: `""` and `"UTC"` both resolve to UTC, and
  `"Local"` resolves to the host's local zone — **none of these three fall back to Bogota**,
  only a genuinely unresolvable name does.
- `CalendarDay(t time.Time, loc *time.Location) time.Time` — the calendar day `t` falls on
  when observed in `loc`, expressed at UTC midnight (the `pgtype.Date` storage encoding).
  `loc` is **mandatory and not nil-checked** — passing `nil` panics exactly as `t.In(nil)`
  would; this function performs no hidden default substitution.

## The one import rule — this module's defining constraint

**This package imports stdlib `time` and NOTHING ELSE — no exception, ever.** Never add a
non-time helper here, no matter how small or convenient it seems. A package named `clock`
states exactly what may live here; a `util`/`shared` dump does not — that is precisely the
anti-pattern `ai/architecture.md:72` names. A new concern, however small, gets its own
concern-named package under `internal/`, never a corner of this one.

## Data ownership

None. No table, no migration, no `db/` package, no config, no env var, no external state of
any kind. This module cannot create an import cycle with any other module because it imports
nothing project-local.

## Boundaries

- Does not read config, environment variables, or any other module's internals.
- Adopted across `internal/` and `cmd/`. `make tz-guard` is what keeps it that way — read
  its three legs in the `Makefile` before adding a `tz:allow` marker.

## Testing

- Pure, offline, table-driven tests only — no DB, no container, no network, no test-fixture
  seeding. `clock_test.go` covers `Zone`, `Now`, and `LoadOrDefault`; `calendar_test.go`
  covers `CalendarDay`, including a UTC self-truncation case, a Bogota (UTC-5, no DST)
  cross-day case, and two `America/New_York` DST-transition cases. Expected values are
  transcribed verbatim from `design.md`'s Test Contract, authored before the implementation
  existed (`ai/go-conventions.md` §Testing "author their expected values up front").
