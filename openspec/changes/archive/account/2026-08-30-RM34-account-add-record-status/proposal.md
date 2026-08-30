Source: MAG-33 — https://linear.app/magus-monitor/issue/MAG-33/status-to-accounts-and-vehicles
Roadmap: openspec/roadmaps/RM34-account-vehicle-status.md
Tier: 1 of 2 (account; tier 2 is `RM34-gateway-block-inactive-login`, module `gateway`,
depending on this tier)

## Why

MAG-33 turns the platform into an invite-gated service: every `accounts` and `vehicles` row must
carry an explicit `Active`/`Inactive` status, invisible rows must disappear from normal reads, and
an inactive user must be refused entry at Google login. `internal/account` is the only module
allowed to own the `accounts` and `vehicles` tables (`ai/architecture.md` §2: "no cross-module
database leaks"), so the column, the constants, and every filtered read must land here before the
`gateway` module (tier 2) can act on `account.Account.Status` to block a login.

This is **tier 1 of 2** of roadmap `RM34-account-vehicle-status`
(`openspec/roadmaps/RM34-account-vehicle-status.md`), which captures the binding decisions (D1–D7)
already agreed with the user before any artifact was written. This proposal and its sibling
artifacts implement D1 (TEXT+CHECK column, not a native enum), D2 (accounts default `Inactive`, no
backfill), D3 (vehicles default `Active`, backfilled), D4 (the auth path stays unfiltered; every
other account/vehicle read filters `status = 'Active'`), and D7 (no new unit tests; existing tests
this change breaks must be repaired) for the `account` module only. Tier 2
(`RM34-gateway-block-inactive-login`, owned by `internal/gateway`, depends on this tier) consumes
`account.Account.Status` to render the blocked page and refuse the session — none of that is in
scope here.

## What Changes

- **Migration** — one goose migration adds `status TEXT NOT NULL CHECK (status IN
  ('Active','Inactive'))` to both `accounts` (`DEFAULT 'Inactive'`) and `vehicles` (`DEFAULT
  'Active'`). No separate backfill `UPDATE` statement for either table: PostgreSQL's `ADD COLUMN
  ... DEFAULT` already assigns the literal default to every pre-existing row without a table
  rewrite (see `design.md` D8) — this alone realizes both D2's "no backfill to Active" (existing
  accounts land on `Inactive`) and D3's "backfill to Active" (existing vehicles land on `Active`).
- **sqlc regeneration** — `GetAccountByProviderID`, `GetAccountLanguage`, `UpdateAccountLanguage`,
  `ListVehiclesByAccount`, and `ListAllVehicles` in `internal/account/db/query.sql` each gain an
  `AND status = 'Active'` predicate. `UpsertAccountFromOAuth` is untouched — its `RETURNING *`
  already picks up the new column. `make sqlc` regenerates `accountdb`.
- **Domain surface** — `account.Account` gains a `Status string` field; two new exported constants,
  `StatusActive`/`StatusInactive`, follow the existing `LanguageES`/`LanguageEN` pattern in
  `account.go`. No `Service` method signature changes — every affected method's behavior changes,
  not its shape.
- **Existing-test repair** — `TestLanguagePreference_RoundTrip` is the one existing test whose
  target queries (`GetAccountLanguage`/`UpdateAccountLanguage`) become account-status-filtered; its
  test account (provisioned via `UpsertFromOAuth`, which defaults to `Inactive`) must be explicitly
  activated with a direct SQL `UPDATE` before it exercises the language read/write path, mirroring
  the file's existing out-of-band-write convention. Every other existing integration test only
  touches vehicles-scoped queries, and vehicles default `Active`, so no other test needs behavioral
  repair — only recompilation coverage from the wider `Account` struct.
- **Docs** — `internal/account/AGENTS.md` documents the new `Status` field and the module's
  activation model; this proposal's header carries the post-deploy recovery SQL (D2), also
  reproduced at the top of `tasks.md`.

**Breaking, deliberately, for one row.** The migration is additive to the schema (new column, both
with a `DEFAULT`, no dropped column, no changed type) so it is not schema-breaking. But it is a
**deliberate access-breaking change in effect**: every existing account, including the project
owner's own (`rasputin999@gmail.com`), becomes `Inactive` the instant the migration runs, and tier
2's login block (a separate, dependent change) will refuse it. This consequence was stated to and
accepted by the user in the roadmap (D2) — see the **Post-deploy recovery** callout below, which
must run immediately after `make migrate-up` or nobody can sign in.

> **Post-deploy recovery — required, not optional.**
> ```sql
> UPDATE accounts SET status = 'Active' WHERE email = 'rasputin999@gmail.com';
> ```

**Affected modules:** `internal/account` (implements this tier). `internal/gateway` is affected
only as the intended future consumer (tier 2, a separate dependent change, not touched here). No
other module reads or writes `accounts.status` or `vehicles.status`.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `account` and `account-vehicle-registry` capabilities)

### Modified Capabilities

- `account`: a new requirement, "Account Activation Status," covering the `status` column, the
  `Active`/`Inactive` vocabulary, the opposite defaults, the unfiltered provisioning path, and the
  every-other-read-excludes-inactive guarantee. Existing requirements "Social OAuth Account
  Provisioning," "Per-Account Language Preference," and "All-Accounts Registered Vehicle
  Enumeration" are modified to reflect status awareness.
- `account-vehicle-registry`: a new requirement, "Vehicle Activation Status," covering the
  vehicles-side column, its opposite (`Active`) default and backfill, and the retire-by-hand
  semantics. Existing requirement "Registered Vehicle Read Access" is modified to reflect the
  status filter.

## Impact

- `internal/account` — new migration, one new domain field (`Account.Status`) + two new domain
  constants, five sqlc queries gain a predicate, sqlc regeneration, service mapping update, one
  existing integration test repaired, `AGENTS.md` update.
- `internal/gateway` — **not touched by this tier.** Tier 2 of the roadmap
  (`RM34-gateway-block-inactive-login`) is the only consumer of `account.Account.Status`; it is a
  separate, dependent OpenSpec change and cannot meaningfully block a login until this tier's field
  exists.

**Read paths affected** (per `openspec/config.yaml`'s performance rule and the roadmap's "Affected
read paths" section):
- **Dashboard vehicle list** — `RegisteredVehicles` → `ListVehiclesByAccount`, once per dashboard
  render. Gains a `status` predicate on an already `account_id`-scoped query.
- **Nightly collector enumeration** — `AllRegisteredVehicles` → `ListAllVehicles`, a full scan
  across accounts, once per nightly run. Gains a `status` predicate.
- **Per-request language lookup** — `GetAccountLanguage`/`UpdateAccountLanguage`, once per request.
  Gains a `status` predicate on a primary-key lookup.
- **Login** — `UpsertAccountFromOAuth`, once per sign-in. Predicate unchanged; returns one extra
  column.

No new index (see `design.md` D10 for the full justification restated against these four paths):
`accounts` reads are by primary key or the existing `UNIQUE (provider, provider_id)`, `vehicles`
reads are `account_id`-scoped or a deliberate full scan, and a two-value column has no selectivity
worth an index in either case.
