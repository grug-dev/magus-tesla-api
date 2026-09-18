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
  — the `[]MigrationDir` slice `cmd/migrate` actually loops over. Added by
  `platform-add-docker-compose-deploy` (T7) to remove `cmd/migrate`'s prior
  `os.Getenv` calls, which violated `ai/go-conventions.md`'s "no `os.Getenv`
  outside `internal/config`" rule.
  - `MigrationDir` pairs a directory with the **module that owns it**, and its
    `VersionTable()` builds that module's ledger name,
    `<module>.goose_db_version`. The two always travel together because goose
    needs both, and deriving one from the other at the point of use is what let
    them disagree. `VersionTable()` is the single place that name is built —
    `internal/testdb` calls it too, so a test database records versions exactly
    where the deploy does.
  - `MigrationsDirs` comes from the optional `MIGRATIONS_DIRS` env var: a
    space-separated list of migration directories (e.g.
    `internal/account/db/migrations internal/telemetry/db/migrations ...`).
    Extra whitespace between entries is ignored — it never produces an empty
    path. Set it to run `cmd/migrate` against a repo checkout, where the
    Docker image's `<MigrationsRoot>/<module>` layout does not exist.
  - Each such path's module is resolved by `moduleForDir`, which finds the path
    segment naming a known module. One rule covers both layouts: the image's
    `<root>/account` and a checkout's `internal/account/db/migrations` each
    contain exactly one segment that names a module. A path naming none is an
    **error**, not a guess — a directory whose module cannot be named has no
    ledger to write to, and falling back to the last path segment would write
    `migrations.goose_db_version` or silently reuse another module's ledger.
  - When `MIGRATIONS_DIRS` is unset, `MigrationsDirs` falls back to
    `MigrationsRoot` + `/` + each of `defaultMigrationModules` — the
    image-default behavior, unchanged. `compose.yaml` and the `Dockerfile` need
    no change for this.
  - `defaultMigrationModules` is the **only** list of module names in this
    package, on purpose: it builds the default paths AND resolves a path back to
    its module. A second list could drift, and the module name decides which
    ledger a migration is recorded in. The order is no longer significant — each
    module's migrations create only its own objects and read nothing — and is
    kept stable only for comparable logs.
- `SaveTokens(accessToken, refreshToken string) error` — persists Tesla OAuth tokens
  back into `.env` (used by `cmd/setup`'s one-time OAuth bootstrap).
- `LoadDatabase() (string, error)` — config for a database-only tool (`cmd/monthly-capacity`).
  Same `.env`-optional loading as `Load`, but requires only `DATABASE_URL` — no Tesla
  credential check, since a tool that never calls the Fleet API has no reason to fail
  over one it never uses. Returns the bare DSN string, not a wrapping struct: it has
  exactly one fact to return, so a struct here would be pure indirection.
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

Pure, offline, table-driven tests only — no DB, no container, no network.

- **`Load()` / `LoadMigration()` / `LoadDatabase()` are tested with `t.TempDir()` + `os.Chdir`,
  restoring both in `defer`**, so each case controls its own `.env` file and real environment.
  The four cases every loader needs: `.env` present, `.env` absent with real env vars set,
  a required value missing everywhere, and a real env var beating a `.env` value.
- `MigrationsDirs` is covered for the ordered-list case, the unset fallback to the four
  `<root>/<module>` paths in order, and extra whitespace producing no empty directory.
- The small pure helpers — `pollerTimezoneOrDefault`, `envInt`, `envDuration`, `envStripped` —
  stay covered in their own test files.

Which test file covers what: `ls internal/config/*_test.go`.
