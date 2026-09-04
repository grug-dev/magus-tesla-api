# Account Sub-Agent

Agent-Name: `account`

Per-module instructions for `internal/account/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.


- `ai/go-conventions.md` §persistence — binding here: goose migrations are the single
  sqlc schema source; convert pgtype values to domain types at the DB→domain boundary

## Responsibility

Owns user accounts and their per-user Tesla credentials: Google-identity upsert, Tesla
token storage (access token ~8 h; refresh token ~3 months, single-use), token refresh,
and the per-user vehicle registry. Multi-tenant by design — never assume a single user
or vehicle. Token refresh is THIS module's job, not the `tesla` adapter's.

## Public interface (the module's port)

Consumers (e.g. the gateway) call these — never this module's tables
(see `internal/account/service.go`):

- `UpsertFromOAuth` — create/find an account from a Google identity
- `SaveTeslaTokens(ctx, accountID, TeslaTokens)` — persist a user's Tesla tokens
- `AccessTokenFor(ctx, accountID) (string, error)` — a valid access token for Fleet API
  calls (refreshing behind the scenes when needed)
- `RegisteredVehicles` — the user's persisted vehicle registry
- `LanguageFor`/`SetLanguage` — read/persist a user's `{es, en}` language preference, now
  backed by `account.settings` (RM42 tier 1), not a column on `accounts`
- `ThemeFor`/`SetTheme` — read/persist a user's `{apex, graphite, halloween}` UI theme
  preference (RM42 tier 1); mirrors `LanguageFor`/`SetLanguage` exactly
- `PreferencesFor(ctx, accountID) (Settings, error)` — both `Language` and `Theme` in a
  single query; callers needing more than one preference for one render MUST use this
  instead of calling `LanguageFor`/`ThemeFor` separately (RM42 tier 1, design.md D7)

`Account` also carries a `Status` field (`StatusActive`/`StatusInactive` — roadmap RM34).
`UpsertFromOAuth` is the one operation NOT filtered by it; every other read in this
module's `Service` treats an `Inactive` account or vehicle as though it does not exist.
A new account defaults `Inactive` (invite-gated); a new vehicle defaults `Active`. No
port method flips a status — that is a manual, out-of-band DB update (see design.md of
`RM34-account-add-record-status`). An `Inactive` account also suppresses its own vehicles
and Tesla token reads (`RegisteredVehicles`, `AllRegisteredVehicles`, `AccessTokenFor`) via
an `EXISTS`-gated join on the owning account's status, even when the vehicle/token row
itself is `Active` (design.md D14/D15).

## Units convention

Platform-wide unit rule: `openspec/specs/unit-of-measure/spec.md` / `ai/go-conventions.md`
§Persistence — unit-suffixed column names, display units, converted once on write. `accounts`,
`tesla_tokens` and `vehicles` currently hold **no** unit-bearing columns, so the rule has nothing
to apply to today — it governs any unit-bearing column added to this module in the future.

## Boundaries

- Data lives in the `account` Postgres schema (tables `accounts`, `tesla_tokens`,
  `vehicles`, moved there by `RM39-account-move-to-own-schema`; `settings`, added by
  `RM42-account-add-settings-table` tier 1 — one row per account, PK `account_id`, holding
  `language` and `theme`), managed from `internal/account/db/` (goose migrations +
  `query.sql`, sqlc-generated code). No other module touches these tables — ever.
- Does not import other feature modules; consumers wire account and tesla together
  (e.g. `AccessTokenFor` → `tesla.Credentials` happens in the gateway, not here).
- No HTML, no HTTP handlers — that is the gateway's layer.

## Testing

- Unit tests with fakes at the DB boundary, plus integration tests
  (`service_integration_test.go`) against real Postgres. The test database is
  provisioned by `testdb_test.go` via the shared `internal/testdb` helper: when
  `DATABASE_URL` is set AND reachable, that managed Postgres is used; otherwise
  a disposable `postgres:16-alpine` container is auto-started via
  testcontainers-go, with goose migrations embedded under `db/migrations/`
  applied before the suite runs. `make check` is green with zero manual DB
  setup as long as Docker is running locally.
- Tokens are stored plaintext (local Postgres only). Encrypting at rest is a known open
  item before any non-local deploy — do not "fix" it silently inside an unrelated change.
