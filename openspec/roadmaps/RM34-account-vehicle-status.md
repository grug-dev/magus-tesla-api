# RM34 — Account & Vehicle Record Status

Source ticket: MAG-33 — https://linear.app/magus-monitor/issue/MAG-33/status-to-accounts-and-vehicles

## Intention

Every `accounts` and `vehicles` row carries an explicit `Active` / `Inactive` status.
Inactive rows are invisible to normal reads, and an inactive user is refused entry at
Google login with a bilingual message telling them who to contact. This turns the platform
into an **invite-gated** service: a new Google signup provisions an account but cannot get
in until the owner activates it by hand.

## Decisions (binding — settled with the user before any artifact was written)

**D1 — Column type: `TEXT NOT NULL CHECK (status IN ('Active','Inactive'))`, not a native
enum.** Resolved from the codebase, not asked: the project has zero `CREATE TYPE … AS
ENUM`; every closed vocabulary is TEXT+CHECK, including `access_type IN ('OWNER','DRIVER')`
on the `vehicles` table itself (`20260720000001_vehicles_add_access_type.sql`). A native
enum also cannot drop a value without recreating the type. Values are title-case
`Active`/`Inactive` exactly as the ticket asks, deviating from the dominant UPPERCASE
vocabulary style by explicit request — the ticket wants that text readable in the row.

**D2 — Accounts default `Inactive` with NO backfill.** Chosen by the user with the
lockout consequence stated up front and accepted. Every existing account, the owner's
included, becomes Inactive the moment the migration runs, and access is restored only by
hand. This is the invite gate the ticket is really asking for.

> **Post-deploy recovery — required, not optional.** Immediately after `make migrate-up`:
> ```sql
> UPDATE accounts SET status = 'Active' WHERE email = 'rasputin999@gmail.com';
> ```
> Until that runs, nobody can sign in. Tier 1 must surface this in its task list, and it
> must be in the tier's completion report.

**D3 — Vehicles default `Active`, existing rows backfilled to `Active`.** This is a
correctness gate, not a preference. `vehiclesFor` (`internal/gateway/handlers/handlers.go`)
decides whether to seed from Tesla by testing `len(RegisteredVehicles(uid)) > 0`, and
`SeedVehicles` inserts `ON CONFLICT DO NOTHING`. Had vehicles defaulted to `Inactive`
while that read filtered `status='Active'`, the list would return empty forever → every
dashboard load would call `tesla.ListVehicles` → the insert would no-op against the
still-present Inactive rows → empty again. That is a **paid Fleet API call that wakes the
car on every page load, permanently**. Defaulting Active makes the loop unreachable.
`Inactive` on a vehicle is therefore a *retire* switch, set by hand.

**D4 — The auth path is the one exempt read.** `UpsertFromOAuth` stays unfiltered and
`account.Account` gains a `Status` field; the gateway inspects it right after the upsert
and renders the blocked page without establishing a session. Every *other* account and
vehicle read filters `status = 'Active'`.

Why the alternatives lose: filtering `UpsertFromOAuth` itself cannot work — it is a single
`INSERT … ON CONFLICT (provider, provider_id) DO UPDATE`, so a status predicate cannot
suppress the conflict target; an inactive user's row is still resolved by the upsert, and
adding a filter would only make the statement lie. A sentinel `ErrAccountInactive` was
rejected because it overloads a provisioning call with an authorization verdict and hides
`Status` from callers that will want it later (an admin screen).

**D5 — Contact address `cristiancamilopena@gmail.com`, hardcoded in the i18n catalog.**
*(autopilot)* The ticket renders it with stray backticks — formatting noise, stripped. A
single support address does not earn config indirection; a `ui/` token or config key would
cost a lookup on every read and buy nothing until there is a second address.

**D6 — Blocked page is a dedicated full page at HTTP 403, no session set.** *(autopilot)*
Reuses the login shell for visual consistency. Not a bare `c.String`, because this is a
state a real user lands on and must be able to read and act on. Bilingual per the
project's ES/EN non-negotiable.

**D7 — Unit tests: EXCLUDED for new coverage; EXISTING tests are updated where this change
breaks them.** The user's standing default is no new tests; adding a field to
`account.Account` and changing `GoogleCallback`'s control flow will break compilation and
assertions in existing suites, and those must be repaired as part of the work.

## Tiers

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[ ]` | `RM34-account-add-record-status` | `account` | Migration adding `status` to `accounts` (DEFAULT `Inactive`, no backfill) and `vehicles` (DEFAULT `Active`, backfill existing to `Active`), both `TEXT NOT NULL CHECK (status IN ('Active','Inactive'))`. Add `StatusActive`/`StatusInactive` constants and a `Status` field on `account.Account`. Filter `status='Active'` in `GetAccountByProviderID`, `GetAccountLanguage`, `UpdateAccountLanguage`, `ListVehiclesByAccount`, `ListAllVehicles`; leave `UpsertAccountFromOAuth` unfiltered and have it return `status`. Regenerate sqlc. Update existing account tests. | — | Implement tier 1 of RM34 per the Decisions above. Owning module `internal/account`. D1–D4 and D7 bind. The migration MUST NOT backfill `accounts`, and MUST backfill `vehicles` to `Active`. Surface the post-deploy recovery SQL from D2 in tasks.md and in your final report. |
| `[ ]` | `RM34-gateway-block-inactive-login` | `gateway` | In `GoogleCallback`, after `UpsertFromOAuth`, refuse an account whose `Status != Active`: render a 403 blocked page naming `cristiancamilopena@gmail.com`, and do NOT set `uid`/`email` on the session. New Templ page + ES/EN catalog keys. Update existing gateway tests that assume every callback ends in a session. | 1 | Implement tier 2 of RM34 per the Decisions above. Owning module `internal/gateway`. D4, D5, D6, D7 bind. Depends on tier 1's `account.Account.Status`. Both catalogue languages must be non-empty (`make i18n-guard`). |

Legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

## Affected read paths

Per `openspec/config.yaml`'s performance rule, the reads this change touches:

- **Dashboard vehicle list** — `RegisteredVehicles` → `ListVehiclesByAccount`, once per
  dashboard render. Gains a `status` predicate on an already `account_id`-scoped query.
- **Nightly collector enumeration** — `AllRegisteredVehicles` → `ListAllVehicles`, a full
  scan across accounts, once per nightly run. Gains a `status` predicate.
- **Per-request language lookup** — `GetAccountLanguage`, once per request. Gains a
  `status` predicate on a primary-key lookup.
- **Login** — `UpsertAccountFromOAuth`, once per sign-in. Unchanged predicate; returns one
  extra column.

No new index is warranted: `accounts` reads are by primary key or the existing
`UNIQUE (provider, provider_id)`, and `vehicles` reads are `account_id`-scoped or a
deliberate full scan. A two-value column has no selectivity worth an index, and the
project is read-heavy at midnight-poller write volumes — an extra index would cost writes
to buy nothing. Tier 1's design.md must restate this as its index plan.

## Future work

None deferred. If a per-vehicle or per-account admin UI for flipping status is wanted, that
is a separate change — record it in `openspec/roadmaps/backlog.md` at that point, not here.
