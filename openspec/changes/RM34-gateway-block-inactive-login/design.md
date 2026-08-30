> **Rescoped per roadmap D26 (2026-08-30).** The per-request `AccountActiveGate` middleware
> (roadmap D16) is **withdrawn** — see `openspec/roadmaps/RM34-account-vehicle-status.md` D26.
> This tier now covers **only** the login-time block (D4/D5/D6): an Inactive account is refused
> a session at `GoogleCallback`. **D17** (`account.Service.StatusFor`/`ErrAccountNotFound`),
> **D18** (`AccountActiveGate` middleware), **D19** (`HX-Redirect` vs 3xx response-shape split),
> **D21** (fail-open/fail-closed on a `StatusFor` error), and **D22** (clearing the session on the
> mid-session gate path) are **removed** — each existed only to support the withdrawn gate. Their
> IDs are left as stubs below (renumbering nothing) so decision numbers stay stable across this
> rescope. **D20** (the blocked page) and **D24** (`rejectIfInactive`, factored for testability)
> survive, reduced to the login path only. **D23** is rewritten to state the resolved behavior for
> the login-only scope (there is no more mid-session gate for it to reason about). The account
> module's supporting work for the withdrawn gate (`StatusFor`, `ErrAccountNotFound`) was reverted
> uncommitted and never shipped — `account.Service.StatusFor` does not exist and must not be added
> by this tier.

## Context

`internal/gateway` is the only module allowed to hold HTML, sessions, or routing
(`ai/architecture.md` §2). Its sessions are `gin-contrib/sessions` cookie sessions — signed and
AES-encrypted, but with **no server-side store** (`gateway.go`: "No server-side state — sessions
survive restarts"). `currentUID` (`handlers.go`) is a pure session-cookie parse called several
times per request across handlers and fragments; it does no DB read.

Tier 1 (`RM34-account-add-record-status`, archived) added `status TEXT NOT NULL CHECK (status IN
('Active','Inactive'))` to `accounts`, gave `account.Account` a `Status` field, and filtered five
reads by `status = 'Active'` — `GetAccountByProviderID`, `GetAccountLanguage`,
`UpdateAccountLanguage`, `ListVehiclesByAccount`, `ListAllVehicles` — plus, in a later
owner-approved addition to that same tier, gated `ListVehiclesByAccount`/`ListAllVehicles` and both
Tesla-token reads on the *owning account's* status via `EXISTS` (D14/D15). `UpsertFromOAuth` stays
unfiltered by design (D4) and now returns the account's real `Status`.

None of that touches the gateway. One gap remains, named in the roadmap:

- **D4/D5/D6** — `GoogleCallback` must refuse to establish a session for an Inactive account and
  show it a reason and a contact.

The roadmap's originally-scoped second gap — a per-request gate so a session established before
deactivation stops working on the very next request (roadmap D16) — is **withdrawn** (roadmap
D26): a new signup has never held a session, so the login-time block alone covers the ticket's
actual ask (gating new signups). This tier is login-only.

Performance profile: **read-heavy** (`ai/architecture.md` §7). The affected read here is once per
login (`UpsertFromOAuth`'s existing, unfiltered read already returns `Status` — no new read is
added by this tier).

## Decisions

Tier 1 used D1–D15; the roadmap reserved D16 for a per-request gate. New decisions here start at
**D17**.

### D16 — WITHDRAWN (superseded by roadmap D26)

*(user, roadmap)* Originally: deactivation must take effect on the next request, not at next
login. Withdrawn because a new signup gated by this ticket has never held a session — the
login-time block (D4/D5/D6) covers it fully. See roadmap D26 for the full withdrawal rationale.

### D17 — REMOVED

Originally specified a new `account.Service.StatusFor` port method + `ErrAccountNotFound`
sentinel, for the withdrawn per-request gate to consult. Removed with D16/D18. `StatusFor` does
not exist on `account.Service` and must not be added by this tier — the only status information
available here is `Account.Status`, which `UpsertFromOAuth` already returns.

### D18 — REMOVED

Originally specified a new `AccountActiveGate` middleware. Removed with D16 (roadmap D26): it
broke every test double implementing `account.Service`, including `internal/telemetry`'s, which is
unrelated to this feature, for a property (revoking an already-logged-in user) the ticket never
asked for.

### D19 — REMOVED

Originally specified a `c.Redirect` vs. `HX-Redirect` response-shape split for the withdrawn
gate's htmx vs. full-page requests. Removed with D18: `GoogleCallback` is a full-page browser
navigation from Google's own redirect — it is never an htmx request — so the response is always a
plain inline render at HTTP 403; there is no htmx-fragment case to branch on.

### D20 — `pages.AccountBlocked()`: one page component, one render site

`pages.AccountBlocked()` is written and rendered **once**: `GoogleCallback`'s inactive branch
renders it inline, `renderError(c, http.StatusForbidden, pages.AccountBlocked())`, so the
callback's own response is 403 — this is what D6 requires ("render a dedicated Templ page at HTTP
403"). There is no second render site and no `GET /account-blocked` route: that route, and the
`exemptFromStatusGate` allowlist it needed, existed solely as the withdrawn `AccountActiveGate`'s
redirect target (D18/D19/D20's original scope) and are removed along with it. A visitor who lands
on the blocked page (via the 403 response body itself, never a URL) can navigate back to `/login`
via the page's own link.

### D21 — REMOVED

Originally specified fail-open on a transient `StatusFor` error, fail-closed on
`ErrAccountNotFound`, for the withdrawn gate's error handling. Removed with D17/D18: no `StatusFor`
call remains anywhere in this tier's code, so there is no error branch to specify.

### D22 — REMOVED

Originally specified clearing the session before responding on the withdrawn gate's mid-session
block path. Removed with D18: no session is ever established for an Inactive account at login (the
callback returns before any `sess.Set` call), so there is nothing to clear on this tier's one
remaining path.

### D23 — Interaction with `LanguageMiddleware` at login: resolved behavior

Tier 1 filtered `GetAccountLanguage` by `status = 'Active'` (its own D4), so `LanguageFor` returns
an error for an Inactive account whenever it is called with that account's id. That fact is,
however, **irrelevant to the blocked page's render language**, because of how `LanguageMiddleware`
resolves language for the specific request that renders it:

`LanguageMiddleware` runs before every handler, including `GoogleCallback`, and branches on
`currentUID(c)` — a pure session-cookie read. At the moment `LanguageMiddleware` runs for the
`/auth/google/callback` request, **no session has been established yet** (that is the very request
that would establish one), so `currentUID` always returns `ok=false` for this route, regardless of
whether the resolved account turns out Active or Inactive. `LanguageMiddleware` therefore always
takes its **anonymous branch** for this request: it reads the pre-login `lang` cookie (the
language the visitor picked on the login page, if any) via `normalizeLang`, or falls back to
`account.LanguageES` if no cookie is present. `LanguageFor`/`GetAccountLanguage` — and their
tier-1 status filter — are never consulted for this request at all.

**Resolved behavior:** `pages.AccountBlocked()` always renders in the visitor's own pre-login
language choice (or Spanish, the platform default, if they made none) — never a degraded or stale
value derived from the account's stored preference. This is a strictly cleaner outcome than the
withdrawn mid-session gate would have had (which, per the original D23, would have rendered the
blocked page in Spanish regardless of the account's real preference, because that path's
`AccountActiveGate` ran on an already-established session where `LanguageMiddleware` *would* have
taken the signed-in, status-filtered branch). Dropping the mid-session gate removes that
imprecision entirely for the one path this tier still has.

### D24 — Testability: `rejectIfInactive` is factored out of `GoogleCallback`, mirroring `syncLoginLanguageCookie`

`h.google` is a concrete `*googleauth.Client` (not an interface) whose `Exchange` method calls
Google's real token and userinfo endpoints — there is no seam to fake it, which is exactly why
`syncLoginLanguageCookie` already exists as its own function, callable directly with a hand-built
`gin.Context` and a plain `account.Account`, bypassing `Exchange` entirely (see its doc comment in
`lang.go`). The same constraint applies to testing the inactive-account block, so it gets the same
treatment:

```go
// rejectIfInactive renders the blocked page and returns true if acct is not Active — the
// caller (GoogleCallback) must return immediately without establishing a session. Returns
// false (and renders nothing) for an Active account. Factored out of GoogleCallback, like
// syncLoginLanguageCookie, so it is directly testable with a hand-built gin.Context and a
// plain account.Account, without a live call to h.google.Exchange (design.md D24,
// RM34-gateway-block-inactive-login). GoogleCallback is a full-page browser navigation from
// Google's own redirect, never an htmx request, so there is no HX-Redirect branch to
// consider (unlike the withdrawn per-request gate — see D19 above).
func rejectIfInactive(c *gin.Context, acct account.Account) bool {
    if acct.Status == account.StatusActive {
        return false
    }
    renderError(c, http.StatusForbidden, pages.AccountBlocked())
    return true
}
```

`GoogleCallback`'s body becomes: `... UpsertFromOAuth ... ; if rejectIfInactive(c, acct) { return
}` immediately after the upsert, before `syncLoginLanguageCookie` and before `sess.Set("uid", ...)`.
A free function, not a method — it touches no `Handler` field, matching this file's existing
`render`/`renderError`/`randomState` free-function style rather than forcing a receiver it does not
need. It no longer calls a shared `blockRequest` helper (D18/D22 removed the middleware that helper
existed to share with) — it renders directly.

## Test Contract

Authored up front per `ai/go-conventions.md` §Testing ("author expected values in design.md before
the implementation exists"). D7 forbids *new coverage for pre-existing behavior*; it does not
exempt genuinely new code paths from having their expected values pinned down before someone
writes them in the implementation wave.

**`rejectIfInactive(c, acct)` — direct calls, no HTTP round trip through `GoogleCallback`:**

| Input `acct.Status` | Return | Response written | Session |
|---|---|---|---|
| `account.StatusActive` | `false` | nothing (caller continues) | untouched |
| `account.StatusInactive` | `true` | `403`, `pages.AccountBlocked()` body | untouched — no session key was ever set on this path (the callback returns before any `sess.Set` call) |
| `""` (zero value / unrecognized) | `true` | `403`, `pages.AccountBlocked()` body | same as above — anything not exactly `StatusActive` blocks, by construction of the `==` check, not an allow-list of known-bad values |

**Overall `GoogleCallback` contract** (stated for completeness; not independently testable — see
below): an Inactive account resolved by the callback → HTTP 403, blocked page rendered, no session
cookie written. An Active account → HTTP 302 to `/dashboard`, with `uid`/`email` set in the
session.

**End-to-end via the full `GoogleCallback` handler:** not achievable without a live Google network
call (`h.google.Exchange` is a concrete client, not an interface — unchanged constraint from
`syncLoginLanguageCookie`'s own tests). The `rejectIfInactive` table above is the equivalent
coverage at the seam that actually exists, plus inspection of `GoogleCallback`'s control flow
confirming `rejectIfInactive` runs before `syncLoginLanguageCookie`/`sess.Set`.

## Index Plan

No new index and no new read. This tier consumes `Account.Status`, a field `UpsertFromOAuth`'s
existing, already-shipped, unfiltered read already returns (tier 1). Restated from tier 1's D10 /
roadmap Index Plan per the design-gate requirement: a two-value column has no selectivity worth an
index regardless.
