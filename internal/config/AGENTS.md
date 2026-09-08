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

- `Load() (*Config, error)` — loads `.env` if present (a missing `.env` file is
  **not** an error — a container has no `.env` and gets real environment variables
  instead; see below), then returns a populated `Config`; still errors if
  `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET` are unset, or if `.env` exists but cannot
  be read (e.g. a permission error). A real environment variable always wins over a
  `.env` value.
- `LoadMigration() (*MigrationConfig, error)` — config for the standalone
  `cmd/migrate` tool. Same `.env`-optional loading as `Load`, but does **not**
  require any Tesla credential — a migration-only tool has no reason to validate
  one it never uses. Returns `DatabaseURL` (required; errors if empty),
  `MigrationsRoot` (defaults to `/migrations` when unset), and `MigrationsDirs`
  — the ordered directory slice `cmd/migrate` actually loops over. Added by
  `platform-add-docker-compose-deploy` (T7) to remove `cmd/migrate`'s prior
  `os.Getenv` calls, which violated `ai/go-conventions.md`'s "no `os.Getenv`
  outside `internal/config`" rule.
  - `MigrationsDirs` comes from the optional `MIGRATIONS_DIRS` env var: a
    space-separated, ORDERED list of migration directories (e.g.
    `internal/account/db/migrations internal/telemetry/db/migrations ...`).
    Extra whitespace between entries is ignored — it never produces an empty
    path. Set it to run `cmd/migrate` against a repo checkout, where the
    Docker image's `<MigrationsRoot>/<module>` layout does not exist (T8).
  - When `MIGRATIONS_DIRS` is unset, `MigrationsDirs` falls back to
    `MigrationsRoot` + `/` + each of the four module names, in order
    (account, telemetry, charging, analytics) — the image-default behavior,
    unchanged. `compose.yaml` and the `Dockerfile` need no change for this.
- `SaveTokens(accessToken, refreshToken string) error` — persists Tesla OAuth tokens
  back into `.env` (used by `cmd/setup`'s one-time OAuth bootstrap).
- `(*Config) GoogleRedirectURL() string` / `(*Config) TeslaConnectRedirectURL() string` —
  derived OAuth redirect URIs built from `Config.BaseURL`.
- `Config.PollerTimezone` — an unset `POLLER_TIMEZONE` env var defaults to the platform's
  default zone, obtained from `internal/clock` (`America/Bogota`) rather than hard-coded
  here — never the host's `"Local"` zone (RM35-config-adopt-clock). A set value, valid or
  not, passes through untouched: this module does not validate it — `cmd/poller` still
  does, via `time.LoadLocation`, failing fast on an invalid IANA name at startup.
- `Config.PollerRerunToken` — read from `POLLER_RERUN_TOKEN`. Empty means
  `cmd/poller`'s manual-rerun HTTP listener does not start at all: no port opens,
  no route exists (`platform-add-manual-rerun-api` design.md D2, "fail-closed").
  This module does not validate its shape — any non-empty string is accepted.

### `.env` is optional (missing-file behavior)

A missing `.env` file is not fatal for `Load()` or `LoadMigration()` — both fall
back to real environment variables, which is how the Docker Compose deploy passes
config (`platform-add-docker-compose-deploy`). A present `.env` file still loads
normally, and a real environment variable always wins over a `.env` value
(`godotenv.Load()`'s existing non-overriding behavior). Any other error reading
`.env` (a permission error, a malformed file) is still fatal. Shared by both
loaders through the internal `loadDotEnv()` helper.

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
- `config_test.go` tests `Load()` and `LoadMigration()` directly, using
  `t.TempDir()` + `os.Chdir` (restoring both in `defer`) to control the `.env`
  file and the real environment for each case: `.env` present, `.env` absent
  with real env vars set, required values missing, and a real env var beating
  a `.env` value. Added by `platform-add-docker-compose-deploy` (T1, T7).
  `pollerTimezoneOrDefault`, `envInt`, `envDuration`, and `envStripped` stay
  covered as small pure functions in their own test files, unchanged.
- `config_test.go` also covers `MigrationsDirs` (T8): `MIGRATIONS_DIRS` set to
  an ordered list, `MIGRATIONS_DIRS` unset falling back to the four
  `<root>/<module>` default paths in order, and extra whitespace between
  entries producing no empty directory.
