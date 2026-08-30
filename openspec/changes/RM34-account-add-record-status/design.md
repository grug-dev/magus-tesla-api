## Context

The `account` module owns two tables relevant here:

- `accounts` (`internal/account/db/migrations/20260707000001_init_account.sql`, plus the
  `language` column added in `20260813000001_accounts_add_language.sql`): `id`, `email`,
  `provider`, `provider_id`, `display_name`, `language`, `created_at`, `updated_at`, plus
  `UNIQUE (provider, provider_id)`.
- `vehicles` (`internal/account/db/migrations/20260710000001_vehicles.sql`, plus `access_type`
  from `20260720000001_vehicles_add_access_type.sql` and `exterior_color`/`car_type` from
  `20260803000001_vehicles_add_config_fields.sql`): `id`, `account_id` (FK, `ON DELETE CASCADE`),
  `tesla_id`, `vin`, `display_name`, `access_type`, `exterior_color`, `car_type`, `created_at`,
  `updated_at`, plus `UNIQUE (account_id, tesla_id)`.

MAG-33 (roadmap `RM34-account-vehicle-status`) turns the platform into an invite-gated service: a
row that is not `Active` must be invisible to every normal read, and a newly-provisioned account
starts invisible until the owner activates it by hand. This tier adds the column and the filtered
queries; tier 2 (`internal/gateway`) is the only consumer of the resulting `Account.Status` field
and is out of scope here.

Performance profile: **read-heavy** (`ai/architecture.md` §7). The affected reads are exactly the
four listed in the roadmap's "Affected read paths" section and restated in `proposal.md` "Impact":
the dashboard vehicle list (per render), the nightly collector enumeration (once nightly), the
per-request language lookup (per request), and login (per sign-in, unfiltered). The migration
itself is a one-time DDL cost, not a recurring read/write path, so it is evaluated on its own terms
(lock duration, scan cost), separately from the read-heavy steady-state guidance.

The direct schema precedent is `20260720000001_vehicles_add_access_type.sql`: `TEXT` + `CHECK`,
nullable, no index. This change's `status` column differs in two ways the roadmap already settled:
it is `NOT NULL DEFAULT` (like `language`, not nullable like `access_type`), and the two tables
take **opposite** defaults — the first same-migration case in this module where two tables sharing
one migration file diverge on default value for the same column name.

## Goals / Non-Goals

**Goals:**
- Give every `accounts` and `vehicles` row an explicit, closed-vocabulary status the way `vehicles`
  already models `access_type` and `accounts` already models `language`: `TEXT NOT NULL CHECK`,
  no native enum (mirrors D1 of RM4's `access_type` precedent, generalized).
- Make an `Inactive` row genuinely invisible to every read except the one path that must still see
  it to make the activation decision (`UpsertFromOAuth`).
- Land the schema and the domain field before `internal/gateway` (tier 2) can build the login block
  that is the actual point of MAG-33 — this tier is infrastructure for that tier, not the user-
  facing behavior itself.
- Keep `pgtype` confined to `service.go` / the generated `accountdb` package, exactly as every
  prior persistence change in this module does.

**Non-Goals:**
- Blocking a login, rendering a blocked page, or any i18n catalog key — all tier 2
  (`RM34-gateway-block-inactive-login`), and all forbidden inside this module by
  `ai/architecture.md` §2 ("no HTML inside domain modules").
- An admin UI or API for flipping a row's status by hand — explicitly deferred by the roadmap's
  "Future work" section to a separate change, if ever wanted. This tier's only "write" of `status`
  is the migration's own `DEFAULT`; the post-deploy recovery SQL is a manual, one-time, out-of-band
  `UPDATE` run by the owner, not a port method.
- A third status value, a status-transition audit log, or a state machine — the roadmap fixes the
  vocabulary at exactly `{Active, Inactive}` (D1); no transition rules exist beyond "the owner sets
  it by hand."
- Filtering `AccessTokenFor` / the `tesla_tokens` reads by account status. The roadmap's D4
  enumerates exactly five queries to filter (`GetAccountByProviderID`, `GetAccountLanguage`,
  `UpdateAccountLanguage`, `ListVehiclesByAccount`, `ListAllVehicles`); token refresh is not among
  them, and tier 2's login block is what actually prevents an inactive account from reaching any
  authenticated request in the first place — filtering token refresh too would be redundant
  defense this tier's mandate does not ask for.

## Decisions

The following restate the roadmap's binding decisions (`openspec/roadmaps/RM34-account-vehicle-status.md`)
as they apply to this tier, per the design-gate requirement that this document be self-contained,
followed by this tier's own implementation decisions (D8–D10).

### D1 — Column type: `TEXT NOT NULL CHECK (status IN ('Active','Inactive'))`, not a native enum (restated, binding)

Exact DDL (goose migration, both directions) — one migration file, both tables:

```sql
-- +goose Up
ALTER TABLE accounts
    ADD COLUMN status TEXT NOT NULL DEFAULT 'Inactive'
    CHECK (status IN ('Active','Inactive'));

ALTER TABLE vehicles
    ADD COLUMN status TEXT NOT NULL DEFAULT 'Active'
    CHECK (status IN ('Active','Inactive'));

-- +goose Down
ALTER TABLE vehicles DROP COLUMN IF EXISTS status;
ALTER TABLE accounts DROP COLUMN IF EXISTS status;
```

**Why `TEXT` + `CHECK`, not `CREATE TYPE ... AS ENUM`.** The project has zero native Postgres
enums today; every closed vocabulary already in this schema is `TEXT` + `CHECK` — `access_type IN
('OWNER','DRIVER')` on this same `vehicles` table is the direct precedent. A native enum also
cannot drop or rename a value without `ALTER TYPE ... DROP VALUE` workarounds (unsupported before
PG12, and awkward even after — it requires recreating the type), while `CHECK (status IN
(...))` is `ALTER TABLE ... DROP CONSTRAINT` / `ADD CONSTRAINT` — a plain, additive DDL change with
no dependent-type recreation. Rejected: `CREATE TYPE status_type AS ENUM ('Active','Inactive')` —
inconsistent with the codebase's established vocabulary and strictly harder to evolve later.

**Why title-case `'Active'`/`'Inactive'`, deviating from the module's dominant UPPERCASE
vocabulary (`'OWNER'`/`'DRIVER'`).** By explicit request in the ticket: the value must be readable
as English prose directly in the row (`psql` output, an admin's ad-hoc `SELECT`), not a code. This
is a one-off exception, not a new convention — `access_type` and any future closed vocabulary in
this module keep the UPPERCASE style unless a ticket says otherwise for the same reason.

**Why one migration file for both tables.** The ticket's Step 1 is one column addition applied to
two tables in the same module in the same deploy; splitting it into two migrations would buy
nothing (both changes are needed together for D3's vehicle-registry-seeding-loop prevention to
hold — see D3 below) and would cost an extra timestamp/file for no independent deployability
benefit (goose applies migrations from one module in file order regardless).

### D2 — Accounts default `Inactive`, NO backfill (restated, binding)

The `ADD COLUMN ... DEFAULT 'Inactive'` above is the entire mechanism — there is no separate
backfill statement, and none is wanted. See D8 for why the `DEFAULT` clause alone is sufficient to
apply `'Inactive'` to every pre-existing row.

**Consequence, accepted by the user before this document was written:** every existing account,
including the project owner's own (`rasputin999@gmail.com`), becomes `Inactive` the moment this
migration runs. This is the invite gate the ticket is asking for — tier 2's login block (not built
here) is what makes the consequence bite, but the schema-level consequence exists from this tier's
migration alone, before tier 2 ships.

> **Post-deploy recovery — required, not optional.** Immediately after `make migrate-up`:
> ```sql
> UPDATE accounts SET status = 'Active' WHERE email = 'rasputin999@gmail.com';
> ```
> Until that runs, nobody can sign in once tier 2 is deployed; and even before tier 2 ships, the
> owner's own account row already reads `Inactive` the moment this tier's migration applies.

### D3 — Vehicles default `Active`, existing rows backfilled to `Active` (restated, binding)

The `ADD COLUMN ... DEFAULT 'Active'` above is the entire mechanism (see D8) — this is not a
simplification to match D2's accounts default; it is the opposite default, deliberately.

**Why the two tables cannot share a default (this is a correctness gate, not a style choice).**
`vehiclesFor` (`internal/gateway/handlers/handlers.go`) decides whether to seed vehicles from Tesla
by testing `len(RegisteredVehicles(uid)) > 0`, and `SeedVehicles` inserts vehicles with `ON
CONFLICT (account_id, tesla_id) DO NOTHING` (unchanged by this tier). Trace what happens if
`vehicles.status` defaulted to `Inactive` while `ListVehiclesByAccount` filters `status = 'Active'`
(both true after this tier, if the default were flipped):

1. `RegisteredVehicles` returns empty forever (every row is `Inactive`, filtered out) — even though
   rows exist.
2. The gateway reads "zero vehicles" and calls `tesla.ListVehicles` to seed — a **paid Fleet API
   call that wakes the car**.
3. `SeedVehicles` → `InsertVehicleIfMissing` hits `ON CONFLICT DO NOTHING` against the already-
   present (but invisible) `Inactive` rows — no new row, no error, no change.
4. The next dashboard load repeats the cycle. Forever. On every page load.

Defaulting `vehicles.status` to `Active` makes this loop unreachable: a vehicle, once seeded, stays
visible to `RegisteredVehicles` until someone flips it to `Inactive` by hand. `Inactive` on a
vehicle is therefore a deliberate **retire** switch (e.g. sold the car, decommissioned a test
vehicle), never a state a row passes through by default.

### D4 — The auth path is the one exempt read; five queries gain the filter (restated, binding)

`UpsertAccountFromOAuth` stays unfiltered and its `RETURNING *` now also returns `status`
(automatic — no query.sql change needed, it already uses `*`). `account.Account` gains a `Status
string` field so the gateway (tier 2) can inspect it immediately after the upsert and decide
whether to establish a session, without a second read.

**Why filtering `UpsertAccountFromOAuth` itself cannot work.** It is a single `INSERT ... ON
CONFLICT (provider, provider_id) DO UPDATE`. A `WHERE`-style status predicate has no place to
attach to an `INSERT ... ON CONFLICT` statement that would "suppress" resolving to an existing
inactive row — the conflict target is `(provider, provider_id)`, not a filterable read; the row is
resolved and returned (or updated) regardless of its status. Adding a predicate here would only
make the statement lie about what it does, not actually hide the account.

**Why not a sentinel `ErrAccountInactive` from the upsert instead of a `Status` field.** Rejected:
it overloads a provisioning call (whose only job is "resolve or create the row") with an
authorization verdict, and it hides `Status` from any future caller that wants the raw value (e.g.
a later admin-facing read) — a plain field is the more honest, more reusable shape.

The other five queries in `internal/account/db/query.sql` each gain `AND status = 'Active'`:

```sql
-- name: GetAccountByProviderID :one
SELECT * FROM accounts
WHERE provider = @provider AND provider_id = @provider_id AND status = 'Active';

-- name: GetAccountLanguage :one
SELECT language FROM accounts
WHERE id = @id AND status = 'Active';

-- name: UpdateAccountLanguage :exec
UPDATE accounts
SET language   = @language,
    updated_at = now()
WHERE id = @id AND status = 'Active';

-- name: ListVehiclesByAccount :many
SELECT * FROM vehicles
WHERE account_id = @account_id AND status = 'Active'
ORDER BY tesla_id;

-- name: ListAllVehicles :many
SELECT account_id, tesla_id, vin, display_name, access_type, exterior_color, car_type FROM vehicles
WHERE status = 'Active'
ORDER BY account_id, tesla_id;
```

`GetAccountByProviderID` is currently unused by `service.go` (verified: no caller in this module or
elsewhere in the repo) — it is generated but dead code today. It is filtered anyway, per the
roadmap's explicit instruction, so any future consumer inherits the invisibility guarantee for
free rather than becoming a second place someone has to remember to add the predicate.

**Behavioral consequence for `UpdateAccountLanguage` against an inactive account (worth stating
explicitly, since it is a silent effect, not an error).** `UPDATE ... WHERE id = @id AND status =
'Active'` against an inactive account's id matches zero rows. Postgres does not error on an
`UPDATE` matching zero rows; it is a no-op. `SetLanguage` (`account.go`) does not currently inspect
affected-row count, so calling it against an inactive account **succeeds with no error and no
effect**. This is acceptable and not a bug to fix in this tier: in production, `SetLanguage` is
only reachable from an authenticated request, and tier 2's login block is what prevents an inactive
account from ever reaching an authenticated request in the first place. It is called out here so
the test-contract section below can specify it precisely rather than leave it implicit.

### D7 — No new unit tests; repair what this change breaks (restated, binding)

No new test coverage is added for `status` itself (the user's standing default). Existing tests
that break — by compilation (an added `Account.Status` field, alone, does not break compilation:
no existing test constructs an `Account{}` positionally or with a full field list — verified by
inspecting `service_integration_test.go`, which contains zero `Account{` struct literals) or by
newly-introduced filtering behavior — must be repaired as part of this tier. Exactly one existing
test is affected; see the "Test Contract" section below for its exact repair and expected values.

### D8 — `ADD COLUMN ... DEFAULT` realizes both D2 and D3 with no explicit `UPDATE` (tier-1 decision)

Verified against current PostgreSQL documentation (not assumed): adding a column with a
**non-volatile** `DEFAULT` does not rewrite the table — PostgreSQL stores the literal default as
per-attribute metadata (the "missing" value) and substitutes it for every pre-existing row at read
time, exactly as if that row had always had that value. This is what makes `ADD COLUMN ...
DEFAULT 'Inactive'` on `accounts` and `ADD COLUMN ... DEFAULT 'Active'` on `vehicles` sufficient,
by themselves, to realize D2's "no backfill, existing accounts become Inactive" and D3's "backfill
existing vehicles to Active" — no separate `UPDATE accounts SET status = ...` or `UPDATE vehicles
SET status = ...` statement is needed or written. A hand-written `UPDATE` would be strictly
redundant with what `ADD COLUMN ... DEFAULT` already guarantees, and — for `accounts` specifically
— actively wrong to add, since D2 explicitly forbids backfilling `accounts` to `Active`.

**What does still cost a scan (accurate, not overclaimed).** PostgreSQL's documentation states
that adding a `CHECK` (or `NOT NULL`) constraint requires a table scan to verify existing rows,
even though it does not require a rewrite. Combining `DEFAULT`, `NOT NULL`, and `CHECK` in one
`ADD COLUMN` statement, as this migration does, therefore does run a validation scan over
`accounts` and `vehicles` — but it is a lock-held, no-I/O-beyond-heap-read scan validating a single
substituted literal per row, not a data rewrite. Given this project's account/vehicle table sizes
(a handful to low hundreds of rows, not the read-heavy telemetry tables), the scan is effectively
instant. This is a one-time migration cost, not a recurring read-path cost, so it is evaluated
against "is this migration safe to run," not against the read-heavy performance profile that
governs steady-state queries (`ai/architecture.md` §7). No `NOT VALID` / later `VALIDATE
CONSTRAINT` split is used — that technique exists to avoid holding a long lock on a large,
actively-written table, which does not describe `accounts` or `vehicles` here, and no existing
migration in this module uses it either.

### D9 — Existing-test repair: activate the one affected test's account via direct SQL (tier-1 decision)

`TestLanguagePreference_RoundTrip` (`internal/account/service_integration_test.go`) is the only
existing test that exercises a now-status-filtered query pair (`GetAccountLanguage` /
`UpdateAccountLanguage`, via `LanguageFor`/`SetLanguage`). Its test account is provisioned via
`UpsertFromOAuth`, which — after this tier — defaults to `status = 'Inactive'` like every account.
Without repair, its very first assertion (`LanguageFor` on a fresh account) breaks: the filtered
`GetAccountLanguage` finds zero rows and `LanguageFor` returns a wrapped `pgx.ErrNoRows`.

**The fix:** immediately after provisioning the account and before exercising any language
read/write, activate it with a direct SQL statement — `pool.Exec(ctx, "UPDATE accounts SET status
= 'Active' WHERE id = $1", acct.ID)` — mirroring the same test file's existing convention of
reaching the pool directly for out-of-band setup (it already does exactly this to simulate a
legacy/out-of-band language value later in the same test). This is a test-only concession
(`ai/go-conventions.md` §Persistence "Seeding another module's tables" — same rationale, applied
within-module here since there is no port method whose job is "activate an account" and adding one
solely for test convenience would be scope creep the roadmap's "Future work" section explicitly
defers).

**Why no other existing test needs this repair.** Every other existing integration test
(`TestUpsertFromOAuth_Idempotent`, `TestSaveTeslaTokens_ReplacesExistingConnection`,
`TestAccessTokenFor_*`, `TestRegisteredVehicles_EmptyForNewAccount`, `TestAllRegisteredVehicles_*`,
`TestSeedVehicles_*`, `TestAccessType_*`, `TestSetVehicleConfigIfEmpty_RoundTrip`) either touches
only `tesla_tokens` (no `status` column at all) or only vehicles-scoped queries
(`ListVehiclesByAccount`/`ListAllVehicles`/`InsertVehicleIfMissing`) — and every vehicle these
tests insert takes the `DEFAULT 'Active'` from D3, so the new `status = 'Active'` predicate matches
every row these tests create. No account-status activation is needed for any of them.

### D10 — Index plan: no new index (restated, binding — restated per the design-gate requirement)

Justified against the four read paths this change touches (`proposal.md` "Impact", mirroring the
roadmap's "Affected read paths"):

- **`ListVehiclesByAccount`** (dashboard vehicle list, per render) — `WHERE account_id = @account_id
  AND status = 'Active' ORDER BY tesla_id`. The row set is already located by `account_id`; no
  index exists on `account_id` alone today (the table relies on the `UNIQUE (account_id,
  tesla_id)` constraint's index, which covers this predicate as a leading-column scan). Adding
  `status` as a second predicate on an already-narrow, already-tiny per-account row set (a handful
  of vehicles per user) costs nothing extra to filter in-memory after the index scan locates the
  account's rows.
- **`ListAllVehicles`** (nightly collector enumeration, once nightly) — a full-table scan by design
  (no `WHERE account_id`); adding `status = 'Active'` is one more boolean check per row already
  being scanned, not an additional scan.
- **`GetAccountLanguage` / `UpdateAccountLanguage`** (per-request language lookup) — both locate
  their row by the primary key `id`, already indexed by construction. `status` is a same-row
  predicate evaluated after the row is already fetched by its PK index entry — no additional index
  lookup.
- **`UpsertAccountFromOAuth`** (login, per sign-in) — unfiltered, locates its row via the existing
  `UNIQUE (provider, provider_id)` index. Unaffected.

**Verdict: no new index**, matching the roadmap's own conclusion. `status` is never the sole or
leading predicate of any query above — it always rides along with an existing primary-key,
unique-constraint, or already-scoped `account_id` lookup. A two-value column also has essentially
no selectivity: an index on `status` alone would not meaningfully narrow either table's row set
(most rows will, in steady state, be `Active`), so even a hypothetical future "list all inactive
accounts" admin query would gain little from a dedicated `status` index versus a plain sequential
scan on these table sizes. If such a query becomes real and the tables have grown enough to matter,
add a targeted `(status)` or composite index then, justified by that query — not preemptively here,
consistent with `ai/architecture.md` §7's "add a summary/index only when a real read pattern needs
it."

### D14 — Gate vehicle/token reads on the owning account's status via `EXISTS`, not a `JOIN` (owner-approved scope addition, binding)

**Problem.** T1–T10 above filter `vehicles.status` and `accounts.status` independently, but nothing
stops a vehicle owned by a deactivated (`Inactive`) account from still being enumerated: an
`Active` vehicle row under an `Inactive` account still passes `ListAllVehicles`'s `WHERE status =
'Active'` and `ListVehiclesByAccount`'s equivalent, because neither query looks at the *owning
account's* status at all. Concretely this means the nightly poller keeps enumerating and polling a
deactivated user's vehicles — spending billed Fleet API calls and waking their car — and a
deactivated user's existing (stateless, cookie-based) session keeps rendering real vehicles, since
cookie sessions have no server-side revocation hook and survive the account flipping to `Inactive`.
Token reuse has the same hole: `AccessTokenFor` (via `GetLatestTeslaTokenByAccount` /
`GetLatestTeslaTokenByAccountForUpdate`) would still hand out — and refresh — a token for a
revoked account, burning a single-use refresh token for no purpose.

**Decision.** Add an account-status predicate to exactly four queries in `query.sql`:
`ListAllVehicles`, `ListVehiclesByAccount`, `GetLatestTeslaTokenByAccount`, and
`GetLatestTeslaTokenByAccountForUpdate`. Each keeps its existing predicate(s) and adds:

```sql
AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = <account id column> AND a.status = 'Active')
```

— `vehicles.account_id` for the first two, `tesla_tokens.account_id` for the token pair.

**Why `EXISTS` (a semi-join) and not a real `JOIN`.** `GetLatestTeslaTokenByAccount` and
`ListVehiclesByAccount` are `SELECT * FROM <table>`. A real `JOIN accounts a ON a.id =
<table>.account_id` would make `*` span both tables' columns, changing the sqlc-generated row
struct shape (`accountdb.TeslaTokens`/`accountdb.Vehicle` would gain `accounts`' columns, or sqlc
would need an explicit projection everywhere) and breaking `service.go`'s row-mapping helpers
(`vehicleFromRow`, and the token-row field access in `AccessTokenFor`). `EXISTS` is a semi-join: it
tests row *existence* without projecting the joined table's columns, so `SELECT *`'s meaning is
unchanged and every generated struct stays byte-identical. PostgreSQL's planner produces a
semi-join execution plan for `EXISTS` the same as it would for an equivalent `IN`/`JOIN` form
here — there is no performance cost to choosing `EXISTS`, only a projection-safety benefit.
`EXISTS` is used uniformly across all four queries (including `ListAllVehicles`, whose explicit
column list would have tolerated a `JOIN` without a struct change) so the pattern reads identically
everywhere a reader encounters it in this file, rather than varying by query shape.

**Verification performed:** ran `make sqlc` after adding the predicate to all four queries and
diffed the regenerated `internal/account/db/models.go` and `internal/account/db/query.sql.go`
against their pre-change versions. `models.go` has **zero** diff. `query.sql.go` differs only in
the embedded SQL query-string constants and their doc comments — no field, type, or struct-shape
change in any generated Go type. This confirms the EXISTS construct, not a JOIN, was used.

**Index plan: no new index.** Every lookup the `EXISTS` subquery performs is already index-backed —
`accounts.id` is the primary key (unique index by construction); the four affected queries were
already locating their own rows by an indexed path (`vehicles`' `UNIQUE (account_id, tesla_id)`,
`tesla_tokens`' `UNIQUE (account_id)`). The `EXISTS` correlated subquery is therefore a single
primary-key point lookup per outer row, not a scan. For `ListAllVehicles` specifically — the one
query with no `WHERE account_id` and therefore a full-table scan already — this adds one indexed
PK lookup per scanned row; the query runs once nightly (`ai/architecture.md` §7's read-heavy
profile explicitly gives write/batch paths latitude to be slower), so even a full-table scan
plus one PK lookup per row is negligible. No index is added.

**What stays unfiltered, deliberately.** `UpsertAccountFromOAuth` remains completely unfiltered —
D4's exemption for the auth path is untouched by this addition; gating the account-status check
belongs to tier 2's login block (`RM34-gateway-block-inactive-login`), not to this provisioning
call. The four write queries — `UpsertTeslaToken`, `UpdateTeslaToken`, `InsertVehicleIfMissing`,
`UpdateVehicleConfigIfEmpty` — are deliberately **not** gated by this decision: each is only
reachable from an authenticated request path that itself depends on a read this change already
gates (a token write follows a successful `AccessTokenFor`/connect flow; a vehicle write follows
`RegisteredVehicles` returning empty and the gateway calling `SeedVehicles`/`SetVehicleConfigIfEmpty`
for a vehicle the caller already resolved through a gated read). Gating the writes too would be
redundant defense with no reachable code path left to defend against, once the reads are gated —
the same reasoning the roadmap already applied to excluding token refresh from D4's original five.

### D15 — Account status gates vehicle and token reads; the auth path stays exempt (owner-approved scope addition, binding)

Restating the resulting invariant precisely, since D14 is mechanism and this is the behavioral
contract it establishes: an `Inactive` account's vehicles and Tesla token connection become
invisible to every read in this module except `UpsertAccountFromOAuth`, exactly the same exemption
D4 already established for the account row itself. This closes the gap left after T1–T10: a vehicle
or token row can be individually `Active` and still be invisible, if its *owning account* is
`Inactive`. The two status checks (the row's own `status` and its owning account's `status`)
compose with `AND` — both queries retain their pre-existing `status = 'Active'` predicate on the
row they're reading (`vehicles.status`, unchanged) alongside the new `EXISTS` check on the owning
account. `UpsertAccountFromOAuth` is unaffected — D4's rationale (the conflict target is
`(provider, provider_id)`, not a filterable read) applies unchanged here.

## Test Contract

D7 adds no new tests, but the one existing test this tier repairs (`TestLanguagePreference_RoundTrip`)
must, after repair, produce exactly these values. This is the behavioral contract the repaired test
must satisfy — authored here, before the implementation, per `ai/go-conventions.md` §Testing
"author their expected values up front."

**Setup (new, added to the test):**
```go
acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{...})
// acct.Status == StatusInactive  (new assertion: the default, unfiltered upsert result)
deleteAccount(t, pool, acct.ID)
if _, err := pool.Exec(ctx, "UPDATE accounts SET status = 'Active' WHERE id = $1", acct.ID); err != nil {
    t.Fatalf("activating test account: %v", err)
}
```

**Then, unchanged from the existing test body** (now against an `Active` account, so every
existing assertion holds exactly as before):
- `LanguageFor` on the freshly-activated account returns `LanguageES` (default).
- `SetLanguage(ctx, acct.ID, LanguageEN)` succeeds; `LanguageFor` returns `LanguageEN`.
- `SetLanguage(ctx, acct.ID, LanguageES)` succeeds; `LanguageFor` returns `LanguageES`.
- `SetLanguage(ctx, acct.ID, "fr")` returns `ErrUnsupportedLanguage`; `LanguageFor` still returns
  `LanguageES` (unchanged).
- Out-of-band `UPDATE accounts SET language = 'fr' WHERE id = $1` followed by `LanguageFor` returns
  `LanguageES` (normalization, unaffected by this tier).

**New scenario this tier's design implies but does not itself add as a test (documented for tier 2
and any future reader, per D4's "worth stating explicitly" note above)** — given an `Inactive`
account:
- `LanguageFor(ctx, inactiveAcctID)` returns an error satisfying `errors.Is(err, pgx.ErrNoRows)`
  once unwrapped (via the `%w`-wrapped `"loading account language: %w"`).
- `SetLanguage(ctx, inactiveAcctID, LanguageEN)` returns `nil` (no error) but persists nothing —
  a subsequent (status-bypassing, raw-SQL) read of the row shows `language` unchanged.

**Other existing tests — no behavioral change, restated as the compliance bar tasks.md's T5
verifies against:**
- `TestUpsertFromOAuth_Idempotent`: both calls return `Status == StatusInactive` (new assertion,
  optional but recommended to lock in D2's default at the one call site every other test relies
  on).
- `TestAllRegisteredVehicles_*`, `TestSeedVehicles_*`, `TestAccessType_*`,
  `TestSetVehicleConfigIfEmpty_RoundTrip`, `TestRegisteredVehicles_EmptyForNewAccount`: every
  assertion's expected value is unchanged, because every vehicle row these tests create takes the
  new column's `DEFAULT 'Active'` and every query they exercise now includes a `status = 'Active'`
  predicate that matches by construction.

**New behaviour introduced by D14/D15 (owner-approved scope addition) — documented for the record,
per D7's "no new test coverage" default; these are the expected outcomes any future test or manual
verification should observe, not assertions added in this change:**

Given an account flipped to `Inactive` by hand (out-of-band `UPDATE accounts SET status =
'Inactive' WHERE id = $1`) that owns at least one `Active` vehicle and an active Tesla token
connection:
- `AllRegisteredVehicles(ctx)` returns **zero** `OwnedVehicle` rows for that account (the
  `ListAllVehicles` `EXISTS` check excludes it), even though the vehicle row itself is still
  `status = 'Active'`. Other accounts' `Active` vehicles are unaffected.
- `RegisteredVehicles(ctx, accountID)` for that account returns **zero** `Vehicle` rows (the
  `ListVehiclesByAccount` `EXISTS` check excludes it) — this is what closes the stateless-cookie-
  session hole: an existing session for the now-`Inactive` account renders an empty vehicle list
  instead of the real one.
- `AccessTokenFor(ctx, accountID)` takes the no-connection sentinel path: both
  `GetLatestTeslaTokenByAccountForUpdate` and (were it called directly)
  `GetLatestTeslaTokenByAccount` now match zero rows for that account regardless of whether a token
  row physically exists, so `AccessTokenFor` returns `ErrNoTeslaConnection` (via the existing
  `errors.Is(err, pgx.ErrNoRows)` branch) — no refresh is attempted, no single-use refresh token is
  spent.
- Every other account's reads are unaffected — the `EXISTS` predicate is correlated per-row on
  `account_id`, not a global condition.

## Risks / Trade-offs

- **[Risk]** The owner's own account becomes `Inactive` the instant this tier's migration runs, in
  any environment it is applied to (including local dev) → **Mitigation**: this is D2's accepted,
  deliberate consequence; the post-deploy recovery SQL is documented in three places (roadmap,
  `proposal.md`, `tasks.md`) so it cannot be missed at deploy time.
- **[Risk]** `SetLanguage` silently no-ops against an inactive account (D4's stated consequence) →
  **Accepted**: production traffic reaching `SetLanguage` is gated by tier 2's login block before
  this tier's login-adjacent behavior matters; documented in the Test Contract above so it is not
  rediscovered as a surprise later.
- **[Trade-off]** `GetAccountByProviderID` is filtered even though it has no current caller →
  **Accepted**: costs nothing today (dead code stays dead code, just filtered), and prevents a
  future consumer from having to remember the predicate independently.
- **[Trade-off]** One combined migration touches two tables with opposite defaults in one file →
  **Accepted**: both changes are needed together for D3's loop-prevention argument to hold; see D1
  for why splitting them would not add independent value.

## Migration Plan

1. Add goose migration `internal/account/db/migrations/<timestamp>_accounts_vehicles_add_status.sql`
   with the exact DDL from D1 (both `ALTER TABLE` statements, both directions of Down).
2. Add `StatusActive`/`StatusInactive` constants to `account.go`, and a `Status string` field on
   `Account` (D4).
3. Add the `AND status = 'Active'` predicate to the five queries in `query.sql` listed in D4. Run
   `make sqlc`; confirm the generated `Status` field on `accountdb.Account` is plain `string` (it
   is `NOT NULL`, matching every other `NOT NULL TEXT` column in this module's generated code) and
   report if it differs.
4. Update `accountFromRow` in `service.go` to map `Status: a.Status`.
5. Repair `TestLanguagePreference_RoundTrip` per D9/"Test Contract" above.
6. Update `internal/account/AGENTS.md` to document `Account.Status` and the activation model.
7. `go build ./...`, `go vet ./...` pass. `go test ./...` is the owner's step
   (`Test-Execution-Policy`).

**Rollback:** the `-- +goose Down` drops both columns in reverse dependency order (`vehicles`
first, then `accounts` — neither actually depends on the other's `status` column, but this mirrors
the Up order for symmetry). Tier 2's gateway code (a separate, dependent change) would need to be
rolled back first if it has shipped, since it reads `Account.Status`.

## Open Questions

None — roadmap decisions D1–D4 and D7 (this tier's applicable binding decisions) are settled and
restated above. D5 and D6 belong to tier 2 (`internal/gateway`), not this design.
