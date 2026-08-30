Source: MAG-33 — https://linear.app/magus-monitor/issue/MAG-33/status-to-accounts-and-vehicles
Roadmap: openspec/roadmaps/RM34-account-vehicle-status.md
Tier: 2 of 2 (gateway; depends on tier 1, `RM34-account-add-record-status`, module `account`,
already archived at `openspec/changes/archive/account/2026-08-30-RM34-account-add-record-status`)

## Why

Tier 1 gave `account.Account` a `Status` field and filtered every account/vehicle read except
`UpsertFromOAuth` — but nothing in `internal/gateway` reads that field yet. Two gaps remain, both
named in the roadmap (D4/D5/D6/D16) and both owned by this module, since no HTML and no session
logic may live outside `internal/gateway` (`ai/architecture.md` §2):

1. **A newly-Inactive or still-Inactive account can complete Google login and get a session.**
   `GoogleCallback` calls `UpsertFromOAuth`, gets back an `Account` that now carries a real
   `Status`, and ignores it — every account, active or not, is signed in today.
2. **A session survives its account being deactivated.** Gin's cookie sessions are signed but
   carry no server-side state (`gateway.go`: "No server-side state — sessions survive restarts").
   Tier 1's `status`-filtered reads (`GetAccountLanguage`, `ListVehiclesByAccount`, …) make a
   deactivated user's *data* disappear, but the session cookie itself keeps working until it
   expires (up to 7 days) or the user logs out — they stay nominally signed in, hitting empty
   pages, with no acknowledgement of why.

This is **tier 2 of 2** of roadmap `RM34-account-vehicle-status`. It implements D4 (refuse the
session at the auth boundary), D5 (hardcoded contact address), D6 (dedicated 403 page reusing the
login shell), D7 (no new test coverage; repair what breaks), and D16 (a per-request gate so
deactivation takes effect on the very next request, not the next login).

## What Changes

- **`GoogleCallback` refuses an Inactive account.** After `UpsertFromOAuth` returns, a factored-out
  helper inspects `acct.Status`. `StatusActive` proceeds exactly as today. Anything else renders the
  new `pages.AccountBlocked()` component at HTTP 403 and returns *before* `sess.Set("uid", …)` runs
  — no session is established. See `design.md` D17 for why this check is a separate, directly
  testable function rather than inline code (the same reason `syncLoginLanguageCookie` was split
  out of `GoogleCallback`: `h.google` is a concrete client that hits Google's real endpoints, so an
  HTTP-level test of the whole handler cannot reach this branch without a live network call).
- **A new page, `pages.AccountBlocked()`** (`templates/pages/account_blocked.templ`): reuses
  `layouts.Base`, the login page's visual shell, and the `ui/` kit. States that the account is
  deactivated and names `cristiancamilopena@gmail.com` as the contact, hardcoded in the i18n
  catalogue (D5) — not config, not a `ui/` token. Two new catalogue keys, both ES and EN non-empty.
- **A new route, `GET /account-blocked`**, rendering the same component at 403. It exists solely as
  the redirect target for the per-request gate below (item 3) — `GoogleCallback` renders the
  component inline and never redirects here itself (D6 requires the callback's OWN response to be
  403, not a 302-then-403 hop).
- **A new per-request middleware, `AccountActiveGate`** (`handlers/account_gate.go`), registered in
  `gateway.go` immediately after `LanguageMiddleware`. For a request carrying a signed-in session
  (and not one of a small, explicit exempt set of routes — auth entry/exit points, health, static
  assets, and the blocked page itself), it calls a **new account-module port method**,
  `account.Service.StatusFor`, once. `StatusActive` passes the request through unchanged. Anything
  else clears the session and sends the caller to `/account-blocked` — a plain 302 for a full page
  load, an `HX-Redirect` header for an htmx fragment request (see `design.md` D19 for why these
  must differ: htmx does not process response headers on a 3xx status, and a bare 3xx handed to an
  in-flight `fetch`/XHR request is followed transparently by the browser before htmx ever sees it,
  landing the blocked page's full HTML inside whatever small region issued the request).
- **A new account-module port method is REQUIRED and is OUT OF SCOPE for this change.**
  `account.Service` gains `StatusFor(ctx, accountID) (string, error)` plus a new sentinel
  `ErrAccountNotFound` — both live in `internal/account`, a different module this worker may not
  edit (`ai/architecture.md` §2: no cross-module internals). `design.md` D17 specifies the exact
  signature, doc comment, and backing SQL for the leader to sequence as a small addendum before this
  tier's `AccountActiveGate` task can compile. Everything else in this proposal (the callback block,
  the blocked page, the route) needs nothing beyond tier 1's already-shipped `Account.Status`.
- **Existing test repair (D7 — no new coverage).** `fakeAccount` (`handlers/handlers_test.go`)
  gains a `StatusFor` method (compile requirement once the account module widens `Service`),
  defaulting to `StatusActive` so every pre-existing test that never sets it is behaviorally
  unchanged — mirroring how `LanguageFor` already defaults its zero value to `LanguageES`. No new
  test functions are added; `design.md`'s Test Contract lists the two direct-call tests
  (`rejectIfInactive`, mirroring the `syncLoginLanguageCookie` pattern) and the middleware tests
  (mirroring `TestLanguageMiddleware_*`) that this tier's *implementation* wave will need to write
  to cover the new code paths it introduces — "no new coverage" governs pre-existing behavior, not
  a new file that has none yet.
- **Docs** — `internal/gateway/AGENTS.md` gets a new dated entry documenting `AccountActiveGate`,
  the exempt-route set, and the `HX-Redirect`-vs-plain-redirect split (a workflow/architecture
  decision per `CLAUDE.md`'s "workflow & architectural decisions are documented with their steps").

**Not breaking.** No schema change (tier 1 owns the column), no route removed, no existing
`Service` method signature changed. `account.Service` gaining one new method is additive — every
existing caller of the interface is unaffected until it is asked to implement the widened
interface (test doubles), which D7 already accounts for.

**Affected modules:** `internal/gateway` (implements this tier). `internal/account` is affected
only as the source of one new port method, sequenced separately per D17 — no other file in that
module changes.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `gateway` capability)

### Modified Capabilities

- `gateway`: two new requirements, "Inactive Account Is Blocked At Login" (D4/D5/D6: the callback
  check, the blocked page's content and status code, no session established) and "Per-Request
  Account Activation Gate" (D16: the mid-session check, its exempt routes, and the full-page vs.
  htmx-fragment response split). The existing "Google Sign-In" requirement is modified to state
  that session establishment is conditional on the account being Active.

## Impact

- `internal/gateway` — one new Templ page, one new route, one new middleware, one modified
  handler (`GoogleCallback`), two new catalogue keys, one `AGENTS.md` decision entry, one
  extended test double.
- `internal/account` — **not touched by this tier's artifacts.** `design.md` D17 hands the leader
  the exact `StatusFor` signature, sentinel, and backing SQL to sequence into that module before
  this tier's gate code can compile; no file under `internal/account` is created or edited here.

**Read paths affected** (per `openspec/config.yaml`'s performance rule):
- **Every authenticated request** — `AccountActiveGate` adds one new primary-key-scoped read
  (`StatusFor`, `SELECT status FROM accounts WHERE id = @id`) to every request that carries a
  signed-in session and is not on the exempt list. This is in addition to `LanguageMiddleware`'s
  existing per-request `GetAccountLanguage` read — two single-row PK lookups per authenticated
  request instead of one. `design.md`'s Context section justifies why this is not merged into the
  existing language read (tier 1 deliberately filtered `GetAccountLanguage` by status, so it cannot
  also serve as an unfiltered status signal without reversing that decision) and why the added cost
  is acceptable under the read-heavy profile (an indexed single-row PK read, the cheapest read shape
  this system has).
- **Login** — `UpsertFromOAuth`, once per sign-in. Unchanged predicate and shape (tier 1); this tier
  only reads the `Status` field tier 1 already returns.

No new index: `StatusFor` reads `accounts` by primary key, exactly like every other single-account
lookup in this module. A two-value column has no selectivity worth an index (restated from tier 1's
design.md D10, which this tier does not reopen).
