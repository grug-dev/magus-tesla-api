# Config Sub-Agent

Agent-Name: `config`

Per-module instructions for `internal/config/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

*(empty — `internal/config` needs nothing beyond the base pack)*

## Responsibility

Loads `.env`, exposes a typed `Config` struct, and saves Tesla tokens back to `.env`. It
is the **only** package in the repo allowed to call `os.Getenv` (`ai/go-conventions.md`
§Coding Rules) — every other package receives configuration via function arguments or the
`Config` struct, never by reading the environment itself.

`internal/config` is read by `cmd/setup`, `cmd/web`, and `cmd/poller` — it has no
consumers inside `internal/`. It is a `cmd/`-level concern, not a domain module
(`ai/architecture.md`'s "Heads-up" note and RM35 tier-1 design D2), which is why other
domain modules (`telemetry`, `analytics`, `gateway`, `app`) must never import it — doing
so would invert the dependency direction (config is meant to be consumed only by the thin
`cmd/` composition roots).

## Public interface

Consumers call these — never `os.Getenv` directly (see `internal/config/config.go`):

- `Load() (*Config, error)` — reads `.env`, returns a populated `Config`; errors if
  `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET` are unset or `.env` cannot be read.
- `SaveTokens(accessToken, refreshToken string) error` — persists Tesla OAuth tokens
  back into `.env` (used by `cmd/setup`'s one-time OAuth bootstrap).
- `(*Config) GoogleRedirectURL() string` / `(*Config) TeslaConnectRedirectURL() string` —
  derived OAuth redirect URIs built from `Config.BaseURL`.
- `Config.PollerTimezone` — an unset `POLLER_TIMEZONE` env var defaults to the platform's
  default zone, obtained from `internal/clock` (`America/Bogota`) rather than hard-coded
  here — never the host's `"Local"` zone (RM35-config-adopt-clock). A set value, valid or
  not, passes through untouched: this module does not validate it — `cmd/poller` still
  does, via `time.LoadLocation`, failing fast on an invalid IANA name at startup.

## Allowed imports

- Stdlib (`fmt`, `os`, `strconv`, `time`).
- `github.com/joho/godotenv` (`.env` parsing).
- `internal/clock` — the platform's sole owner of the default time zone name
  (`ai/go-conventions.md` "The platform's default time zone…"). `internal/clock` imports
  stdlib `time` only, so this creates no import cycle.
- Never another domain module (`account`, `tesla`, `telemetry`, `analytics`, `gateway`,
  `app`) — `config` sits below all of them in the dependency direction; they may read a
  `*Config` field passed in by `cmd/`, but none may import `internal/config` itself.

## Data ownership

None. No table, no migration, no `db/` package. `.env` is a local file, not a database —
`config` only reads and rewrites it (`SaveTokens`).

## Testing

- Pure, offline, table-driven tests only — no DB, no container, no network.
  `quote_test.go` covers `envStripped`'s quote-stripping behavior.
  `timezone_test.go` covers `pollerTimezoneOrDefault`'s unset-env-var default
  (RM35-config-adopt-clock, the one case with previously zero coverage — RM35 D6's
  narrow exception for tiers 2–6, which otherwise add no new tests).
- `Load()` itself is not unit-tested directly — it depends on a real `.env` file in the
  process's working directory and on `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET` being set,
  so its env-var-dependent branches (`pollerTimezoneOrDefault`, `envInt`, `envDuration`,
  `envStripped`) are extracted into small pure functions and tested directly instead.
