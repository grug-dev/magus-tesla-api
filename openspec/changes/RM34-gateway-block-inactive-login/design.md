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

None of that touches the gateway. Two gaps remain, both named in the roadmap:

- **D4/D5/D6** — `GoogleCallback` must refuse to establish a session for an Inactive account and
  show it a reason and a contact.
- **D16** — a session established *before* deactivation must stop working on the very next
  request, not linger until the cookie's 7-day `MaxAge` or a manual logout. This is the harder
  problem: there is no auth middleware group today (every route is flat on `r` in `gateway.go`;
  each handler calls `currentUID(c)` itself), `account.Service` has no status-reading method beyond
  the now status-*filtered* `LanguageFor`, and the fix must not multiply per-request DB reads on
  the currently-hot `currentUID` path or push the check into where it can't be found (session
  storage at login).

Performance profile: **read-heavy** (`ai/architecture.md` §7). The affected read is "every
authenticated request" — see D18's cost accounting below — not a bulk/batch path, so there is no
index question beyond "is this a primary-key lookup" (it is).

## Decisions

Tier 1 used D1–D15; the roadmap reserved D16 for this tier. New decisions here start at **D17**.

### D16 — Deactivation takes effect on the next request (restated, binding — from the roadmap)

*(user, roadmap)* Tier 2 adds a per-request account-status check to the authenticated-route
middleware. Without it, the signed cookie session keeps a deactivated user signed in until the
cookie expires; the tier-1 read filters would empty their pages but leave them nominally logged in.
Everything below (D17–D22) is this tier's answer to *how*.

### D17 — New account-module port method: exact contract, sequenced separately

**The mechanism.** `internal/account` (a module this worker may not edit — `ai/architecture.md`
§2) must add:

```go
// ErrAccountNotFound is returned by StatusFor when no account exists for the given
// id at all — distinct from an Inactive account, which StatusFor reports as a
// normal value, never an error. Detect with errors.Is. Added for
// RM34-gateway-block-inactive-login (roadmap D16/D17).
var ErrAccountNotFound = errors.New("account: not found")

// StatusFor returns the account's current activation status: always exactly
// StatusActive or StatusInactive. Unlike every other Service read (LanguageFor,
// RegisteredVehicles, GetAccountByProviderID, ...), this method is deliberately
// NOT filtered by status: an Inactive account must be reported as Inactive, not
// treated as though the row does not exist, so a per-request authorization gate
// (internal/gateway's AccountActiveGate) can tell three outcomes apart —
// "deactivated" (a normal returned value), "no such account" (ErrAccountNotFound
// — an invariant violation for an id that came out of an existing session, since
// UpsertFromOAuth is the only way an id enters a session and it never deletes
// rows), and "lookup failed" (any other error — a transient DB failure). Added
// for RM34-gateway-block-inactive-login (roadmap D16).
StatusFor(ctx context.Context, accountID uuid.UUID) (string, error)
```

Backing SQL, added to `internal/account/db/query.sql` (for the leader to sequence):

```sql
-- name: GetAccountStatus :one
-- Unfiltered by status — StatusFor must be able to report Inactive rather than
-- behave as though the account does not exist (RM34 D16/D17). This is the ONE
-- account read that is neither the auth path (D4, also unfiltered) nor
-- status-filtered like every other Service read — it exists specifically to
-- report the value the filter on every other query is hiding.
SELECT status FROM accounts WHERE id = @id;
```

Service implementation sketch (`internal/account/service.go`):

```go
func (s *service) StatusFor(ctx context.Context, accountID uuid.UUID) (string, error) {
    status, err := s.q.GetAccountStatus(ctx, accountID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return "", ErrAccountNotFound
        }
        return "", err
    }
    return status, nil
}
```

**Why a new method rather than reusing `LanguageFor`.** `GetAccountLanguage` is `status =
'Active'`-filtered by tier 1's own D4. Calling it from the gate would make "Inactive" and "account
not found" indistinguishable (both surface as `pgx.ErrNoRows` → a generic error), which is exactly
the ambiguity D16's gate must not have — see D23 below for the full account of why that ambiguity
is *acceptable* in `LanguageMiddleware` (a display-language fallback) and *not acceptable* here (an
authorization decision).

**Why not widen `LanguageFor` itself to also return status.** That would reopen tier 1's D4/D14
filtering decision on an already-archived, already-shipped query — a larger, riskier change than
adding one new unfiltered single-column read, for a query this module already has the exact
pattern for (`UpsertAccountFromOAuth`'s `RETURNING *` is the other unfiltered read).

**Rejected: a sentinel error instead of a status value**, e.g. `ErrAccountInactive` returned in
place of a status string. Rejected by the roadmap itself for D4's identical question (a sentinel
"overloads a provisioning call with an authorization verdict and hides `Status` from callers that
will want it later"); the same reasoning applies here, and a plain value is what lets the gate's
three-way branch (Active / Inactive / error) stay a simple switch instead of two different error
types plus a happy path.

**This is a hard sequencing dependency.** Every task in this proposal that touches
`AccountActiveGate` is **BLOCKED** until `StatusFor` exists and compiles. See `tasks.md` T3.

### D18 — `AccountActiveGate`: a new, separate middleware — not `currentUID`, not `LanguageMiddleware`, not session storage

**Where it does NOT go, and why (each ruled out in the dispatch, restated with the reasoning):**

- **Not inside `currentUID`.** It is a pure session-cookie parse called several times per request
  across handlers and fragments (`resolveSelectedVehicle`, every auth guard, `LangSwitch`, …).
  Adding a DB read there multiplies it by every call site on a request, not once — directly
  contradicts the read-heavy profile's "cheap and predictable" hot path.
- **Not stored in the session at login.** A `status` value cached in the cookie at sign-in is
  exactly the bug D16 exists to close: the whole premise is that the session cannot be trusted to
  reflect a status change that happened after it was minted. Reading a stale cached value would
  make this tier a no-op with extra steps.
- **Not merged into `LanguageMiddleware`.** Tempting for cost (one fewer query), but rejected: (1)
  `LanguageMiddleware`'s per-request `GetAccountLanguage` read is status-*filtered* (tier 1), so
  its failure mode already conflates "Inactive" with "any other lookup failure" — exactly the
  ambiguity D17 exists to avoid, and folding the gate into that path would inherit it; (2) mixing
  a display-language resolution with an authorization decision in one function is a change to what
  `LanguageMiddleware` means, on a hot path with its own dedicated, already-reviewed tests
  (`TestLanguageMiddleware_*`, `lang_test.go`), for a savings of one indexed single-row read.

**Where it goes: a new, dedicated middleware, modeled directly on the existing precedent for
"a per-request middleware holding the account port"** — `LanguageMiddleware(acct account.Service)`
(`handlers/lang.go`). `AccountActiveGate` copies its shape:

```go
// AccountActiveGate is registered in gateway.go immediately after LanguageMiddleware. For a
// request carrying a signed-in session that is not on the exempt list, it calls
// acct.StatusFor(ctx, uid) ONCE and blocks the request if the account is not Active.
// Anonymous requests, and requests to an exempt route, pass through untouched — the
// per-handler currentUID auth guard is unchanged and still runs afterward for any handler
// that requires a session. Mirrors LanguageMiddleware's shape (a per-request middleware
// holding the account port, branching on currentUID) — see internal/gateway/AGENTS.md.
func AccountActiveGate(acct account.Service) gin.HandlerFunc {
    return func(c *gin.Context) {
        if exemptFromStatusGate[c.FullPath()] || strings.HasPrefix(c.FullPath(), "/static/") {
            c.Next()
            return
        }
        uid, ok := currentUID(c)
        if !ok {
            c.Next() // anonymous — nothing to gate; the handler's own guard applies.
            return
        }
        status, err := acct.StatusFor(c.Request.Context(), uid)
        switch {
        case err == nil && status == account.StatusActive:
            c.Next()
        case err == nil: // status == account.StatusInactive
            blockRequest(c)
        case errors.Is(err, account.ErrAccountNotFound):
            // A session references an id with no matching account row at all — an
            // invariant violation (UpsertFromOAuth is the only way an id enters a
            // session, and no path deletes an account row). Treat as blocked: fail
            // closed on a state that should be impossible, not open.
            blockRequest(c)
        default:
            // A transient lookup failure (DB unreachable, context cancelled, ...).
            // Fail OPEN: log and let the request through. Mirrors this module's
            // existing posture for a non-essential read that fails
            // (inProgressConflictOn, AGENTS.md: "a transient read failure must not
            // turn into a refusal"). Blocking every authenticated request site-wide
            // on a DB hiccup would trade a hypothetical stale-session window for a
            // real, total outage — the wrong trade for a gate whose failure mode is
            // "the user got one more request before being caught," not data loss.
            log.Printf("account status gate: lookup failed for %s: %v", uid, err)
            c.Next()
        }
    }
}
```

**Registration order** (`gateway.go`): `sessions.Sessions(...)` → `LanguageMiddleware(d.Account)` →
`AccountActiveGate(d.Account)`. `AccountActiveGate` must run after `LanguageMiddleware` so the
blocked page (rendered by `blockRequest`'s full-page branch, or by `GET /account-blocked`) has a
resolved render language on `ctx` — see D23 for what that language actually resolves to for a
deactivated account.

**Cost accounting (why a second per-request read is acceptable here).** This adds one indexed,
primary-key, single-row `SELECT` to every authenticated request, on top of `LanguageMiddleware`'s
existing one. Both are the cheapest read shape this system has (`ai/go-conventions.md` "Read
optimization": PK lookups need no new index). The read-heavy profile's discipline is about
*shape* (batch vs. N+1, indexed vs. scan), not about a hard cap on query count per request — and
correctness of session revocation (the entire point of this tier) outweighs saving one row-read.

### D19 — Response shape differs by request kind, and must

**Full-page request** (no `HX-Request` header): a plain HTTP redirect, `c.Redirect(http.StatusFound,
"/account-blocked")`, then `c.Abort()`. This is exactly what a normal, non-JS-driven navigation
expects, and the browser follows it exactly like any other 302.

**htmx fragment request** (`c.GetHeader("HX-Request") == "true"`): **not** the same 302. Two
independent facts, both verified against htmx 2.0.4 (the pinned version — Context7
`/bigskysoftware/htmx/v2.0.4`, `hx-push-url.md` / `hx-trigger.md`: *"Response headers are not
processed on 3xx response codes"*), rule out a plain redirect for this branch:

1. **A 3xx response to an in-flight `fetch`/XHR is followed transparently by the browser**, before
   htmx's own response-handling code ever runs. htmx would receive the *target's* HTML (the full
   `/account-blocked` page, wrapped in `layouts.Base`) as if it were the answer to the original
   small-fragment request, and swap that full-page markup into whatever narrow region
   (`#dashboard-content`, a single stat card, …) issued the request — broken, nested markup. This
   is precisely the "redirect inside an htmx swap behaves differently" risk the dispatch called
   out, and it is why `htmx-go-integration.md`'s own error-fragment rules (`HX-Error-Fragment`)
   exist for a related but distinct problem (htmx's default `responseHandling` config also refuses
   to swap 4xx/5xx bodies) — neither existing mechanism covers *this* case (a redirect, not an
   error body), so a third mechanism is needed.
2. **htmx response headers are documented as unprocessed on a 3xx status**, so even `HX-Redirect`
   itself would be silently ignored if sent alongside a 302.

The fix: respond with **`c.Status(http.StatusOK)` + `c.Header("HX-Redirect", "/account-blocked")`**,
no body, then `c.Abort()`. `HX-Redirect` is htmx's documented mechanism for exactly this — "a
sample response header that instructs the client to redirect to a specified URL, causing a full
page reload" — and, sent on a 2xx status, htmx processes it and performs a real
`window.location` navigation instead of a swap. The blocked page then loads as a genuine full page
(not a fragment), so it renders correctly regardless of which region originally asked for it.

**Detecting "htmx fragment request."** `c.GetHeader("HX-Request") == "true"` — htmx's own
identifying header, sent on every htmx-issued request. No existing Go helper does this in the
codebase today (`grep` found only the vendored `htmx.min.js`); `isHTMXRequest(c *gin.Context) bool`
is a new one-line helper in `account_gate.go`, trivial enough that no `ui/`-style wrapper is
warranted (AI-efficiency: this is a stable, self-describing one-off, not a churny surface — adding
indirection here would cost more to resolve than it saves).

### D20 — `/account-blocked`: one page component, two render call sites, one new exempt route

`pages.AccountBlocked()` is written and rendered **twice**, deliberately, rather than having
`GoogleCallback` redirect to the new route:

- **`GoogleCallback`'s inactive branch renders it inline**, `renderError(c, http.StatusForbidden,
  pages.AccountBlocked())`, so the callback's *own* response is 403 — this is what D6 requires
  ("render a dedicated Templ page at HTTP 403"), and a redirect-then-403 hop would make the
  callback's own status code 302, not 403.
- **`GET /account-blocked`** (`AccountBlockedPage`, a new handler) renders the identical component,
  also at 403, and exists solely as `AccountActiveGate`'s redirect target for a mid-session block —
  it needs its own route because a redirect (plain or `HX-Redirect`) must name a URL, and
  `GoogleCallback`'s branch is not reachable by URL (it only runs inside the OAuth callback).

**This route MUST be in the gate's own exempt set** — added to the ticket's enumerated set (below),
not replacing it — or a deactivated session redirected there would immediately be gated again
(`AccountActiveGate` still resolves `currentUID` there unless the account's session was already
cleared — see D22 — but exempting it is the unconditional, no-loop-possible guarantee and costs
nothing).

**Exempt set** (`exemptFromStatusGate`, `handlers/account_gate.go`) — the ticket's enumerated
routes plus this tier's own derived addition:

| Route | Why exempt |
|---|---|
| `/login` | Pre-auth entry point; anonymous by definition. |
| `/auth/google/login` | Pre-auth entry point; anonymous by definition. |
| `/auth/google/callback` | D4's own check runs *inline* here, before any session exists to gate — gating this route would run the check against the OLD (pre-upsert) session state, which is never what's being decided. |
| `/logout` | Must always succeed for a signed-in-but-now-inactive user too — it is the escape hatch. |
| `/healthz` | Ops liveness check; never carries a user session. |
| `/static/*filepath` | Static assets; never carries a user session in any meaningful sense, and gating them would 404-adjacent every CSS/JS/image request on a blocked page render. |
| `/account-blocked` | **Derived, this tier.** The gate's own redirect target — see above. |

Every other route (`/`, `/dashboard`, every `/ui/...` fragment, `/connect/tesla*`, `/charges*`,
`/supercharger-stats*`) is gated. `/` (`Home`) is included deliberately: today it silently bounces
an authenticated session straight to `/dashboard` with no data read at all, which is exactly the
kind of request D16 says must not go unchecked.

### D21 — Fail-open on a transient error, fail-closed on `ErrAccountNotFound`

Restated from D18's switch above, as its own decision because it is easy to get backwards: a
**transient** `StatusFor` error (DB unreachable, context cancelled) fails **open** — the request
proceeds, logged — because the alternative is a single-point-of-failure gate that can take down
every authenticated request in the app over one DB hiccup, for a security property (catching a
deactivation before the next natural read-filter failure would have shown it anyway) that is
already partially covered by tier 1's D15 read filters. `ErrAccountNotFound` fails **closed** —
because a session whose `uid` matches no account row at all is not a transient condition; it is a
state that should be structurally impossible (the only way an id enters a session is
`UpsertFromOAuth`, which creates the row it then hands back), so treating it as anything other than
"this session is not valid" would be papering over a bug, not tolerating a blip.

### D22 — The Inactive branch clears the session before responding

`blockRequest` (both call sites — the mid-session gate and, for consistency, the alternate path
inside `rejectIfInactive`'s already-no-session case needs no clearing since none was ever set) calls
`sessions.Default(c).Clear()` + `.Save()` — the same two calls `Logout` already makes — **before**
issuing the redirect or `HX-Redirect`. Rationale: D6/D4's stated intent is "no session is
established" for a blocked account; leaving a half-alive cookie around after detecting Inactive
mid-session contradicts that intent and would otherwise force the SAME request path to re-detect
and re-clear on every subsequent hit until the cookie expires naturally. Clearing once, immediately,
means a later reactivation is a clean fresh login — no stale cookie state to reconcile.

### D23 — Interaction with `LanguageMiddleware`: unchanged, and why the pre-existing imprecision there is fine

Tier 1 filtered `GetAccountLanguage` by `status = 'Active'` (its own D4). Consequence, **already
shipped, not introduced by this tier**: for an Inactive account, `LanguageFor` returns an error
(the query finds zero rows), and `LanguageMiddleware`'s existing code already handles that by
falling back to `account.LanguageES` and re-syncing the `lang` cookie to `es` — the same fallback
path it takes for a genuine transient DB error, since both look identical from `LanguageFor`'s
return shape (`("", err)`).

**Tier 2 does not change `LanguageMiddleware`.** The two mechanisms are deliberately independent:

- `LanguageMiddleware`'s ES-fallback is a **display-language degrade**, not an authorization
  decision. Getting it wrong in the "Inactive" case (falling back to the platform default instead
  of the user's actual stored preference) has zero security consequence — the visible effect is
  that a deactivated user's blocked page renders in Spanish even if their preference was English,
  until they are reactivated. This is an accepted, cosmetic side effect of tier 1, not a bug this
  tier fixes.
- `AccountActiveGate`'s `StatusFor` call is the **only** place in this system that must distinguish
  "Inactive" from "DB error" precisely, because it is the only place making an authorization call.
  That is exactly why D17 adds a dedicated, unfiltered method instead of reusing `LanguageFor`'s
  already-imprecise signal (see D17's "why not reuse `LanguageFor`" above) — conflating the two
  would import the display-language path's acceptable imprecision into a place where it is not
  acceptable.

Net effect for a live, since-deactivated session: `LanguageMiddleware` runs first, silently
degrades to `es` (as it already does today, unchanged); `AccountActiveGate` runs second, correctly
identifies `StatusInactive` via `StatusFor` (unfiltered), and blocks. The blocked page therefore
always renders in Spanish for a caught-mid-session user — a known, acceptable, and now-documented
consequence, not an indistinguishable one.

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
// RM34-gateway-block-inactive-login).
func rejectIfInactive(c *gin.Context, acct account.Account) bool {
    if acct.Status == account.StatusActive {
        return false
    }
    blockRequest(c) // clears session (no-op pre-login) + renders/redirects per D19/D22
    return true
}
```

`GoogleCallback`'s body becomes: `... UpsertFromOAuth ... ; if rejectIfInactive(c, acct) { return
}` immediately after the upsert, before `syncLoginLanguageCookie` and before `sess.Set("uid", ...)`.
A free function, not a method — it touches no `Handler` field, matching this file's existing
`render`/`renderError`/`randomState` free-function style rather than forcing a receiver it does not
need.

## Test Contract

Authored up front per `ai/go-conventions.md` §Testing ("author expected values in design.md before
the implementation exists"). D7 forbids *new coverage for pre-existing behavior*; it does not
exempt genuinely new code paths from having their expected values pinned down before someone
writes them in the implementation wave. Two independent surfaces, both directly testable without
`h.google.Exchange`:

**`rejectIfInactive(c, acct)` — direct calls, no HTTP round trip through `GoogleCallback`:**

| Input `acct.Status` | Return | Response written | Session |
|---|---|---|---|
| `account.StatusActive` | `false` | nothing (caller continues) | untouched |
| `account.StatusInactive` | `true` | `403`, `pages.AccountBlocked()` body | `Clear()`+`Save()` called (no-op: nothing was set pre-login) |
| `""` (zero value / unrecognized) | `true` | `403`, `pages.AccountBlocked()` body | same as above — anything not exactly `StatusActive` blocks, by construction of the `==` check, not an allow-list of known-bad values |

**`AccountActiveGate` — `httptest` against a minimal engine (sessions + the gate + one
downstream probe route), mirroring `langMiddlewareEngine`:**

| Scenario | `currentUID` | Route | `StatusFor` result | Expected response |
|---|---|---|---|---|
| Anonymous | none | any gated route | not called | pass-through (`c.Next()`), no redirect |
| Active, full page | set | gated, no `HX-Request` header | `(StatusActive, nil)` | pass-through |
| Active, htmx | set | gated, `HX-Request: true` | `(StatusActive, nil)` | pass-through |
| Inactive, full page | set | gated, no `HX-Request` header | `(StatusInactive, nil)` | `302` to `/account-blocked`; session cleared |
| Inactive, htmx | set | gated, `HX-Request: true` | `(StatusInactive, nil)` | `200`, header `HX-Redirect: /account-blocked`, empty/minimal body; session cleared |
| Not found | set | gated | `("", ErrAccountNotFound)` | same as Inactive (fail-closed) |
| Transient error | set | gated | `("", errors.New("db down"))` | pass-through (fail-open), logged |
| Exempt route | set | `/login`, `/auth/google/callback`, `/logout`, `/healthz`, `/static/...`, `/account-blocked` | not called | pass-through regardless of status |

**End-to-end via the full `GoogleCallback` handler:** none of the above requires it, and none is
added — an actual HTTP-level "inactive account → 403 + no session cookie" / "active account → 302
to /dashboard with session" pair through the real handler is not achievable without a live Google
network call (unchanged constraint from `syncLoginLanguageCookie`'s own tests). The
`rejectIfInactive` table above is the equivalent coverage at the seam that actually exists.

## Index Plan

No new index. `GetAccountStatus` (D17) is `WHERE id = @id` — the `accounts` primary key, already
indexed. Restated from tier 1's D10 per the design-gate requirement: a two-value column has no
selectivity worth an index regardless.
