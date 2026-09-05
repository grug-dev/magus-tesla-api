# Account activation gate — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `account activation gate`, `inactive account`, `account status`, `blocked login`, `deactivated account`
- **Internal name:** `account.Account.Status` (`Active` / `Inactive`) — enforced at login by the gateway and at read time by the account module

## Component map

Gateway half (the login-time refusal):

| Layer | Symbol | File |
|---|---|---|
| Entry point | `Handler.GoogleCallback` | `internal/gateway/handlers/handlers.go` |
| The check | `rejectIfInactive(c, acct)` — free function, returns `true` when the caller must stop | `internal/gateway/handlers/handlers.go` |
| The page | `pages.AccountBlocked()` — rendered inline at HTTP 403, **no route** | `internal/gateway/templates/pages/account_blocked.templ` |
| Copy | `KeyAccountBlocked*` catalogue keys, ES + EN, contact address hardcoded | `internal/gateway/i18n/catalog.go` |

Account half (Active-filtered reads) lives inside `internal/account/` — see "How maintenance
works" below.

> Not fully mapped: the account module's own filtered queries are not listed here yet. Run
> `/kkpa-context-curate account-activation-gate` to complete the map.

## How maintenance works

- **The gate has two independent halves; changing one does not change the other.** The account module hides Inactive rows from its reads, and the gateway refuses an Inactive account a session at Google login. Neither half is a fallback for the other: an account deactivated mid-session keeps its cookie working until it expires, because the login check runs once at callback time and sessions are stateless.
- **Adding a new place that must respect the gate:** decide which half it belongs to. A new READ of accounts, vehicles or Tesla tokens filters on Active inside the account module (never in the caller). A new ENTRY point that establishes identity performs the status check in the gateway, after the account is resolved and strictly before any session value is written.
- **Changing the blocked page's wording or contact address:** both are catalogue keys in `internal/gateway/i18n/catalog.go`, ES and EN, and the address is deliberately hardcoded there. There is no config key to change and no route to the page — it is rendered directly from the OAuth callback.

## Conventions & gotchas

- **A non-Active account must be refused BEFORE any session identity is written** — the status check runs after the account is provisioned/resolved and strictly before the first session set. Ordering is the whole security property: a check placed after a session write leaves a usable session behind on the refusal path. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The gate is fail-closed: anything that is not exactly `Active` is refused** — including an empty or unrecognised status, not just the literal `Inactive`. Never rewrite the check as "reject when Inactive"; a status value nobody anticipated would then grant access. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The refusal is HTTP 403 with a real rendered page, not a redirect or a bare string** — the visitor is authenticated but not authorized, and they must be able to read the contact address and act on it. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The contact address is hardcoded in the translation catalogue, never configuration** — one support address does not earn a config lookup. Both languages must carry it non-empty, like every other user-facing string. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The blocked page has exactly ONE render site and no route** — it is rendered inline from the OAuth callback. Do not add a `/account-blocked` route "for completeness": an unauthenticated visitor could then read it directly, and it would become a second place the refusal logic has to be kept correct. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **An Inactive account never reaches the language-sync step either** — the pre-login `lang` cookie is persisted to the account only on the Active path. A refused login must leave the account row untouched, so nothing about the visitor's rejected attempt is written. _Source: spec gateway — Requirement: Google Sign-In, Scenario: A callback resolving an Inactive account never reaches the language sync or session steps._
- **Account provisioning stays UNFILTERED while every other account read filters on Active** — the upsert that resolves a Google identity cannot filter by status, because a status predicate cannot suppress an `INSERT … ON CONFLICT` conflict target; filtering it would only make it lie about what it wrote. The authorization verdict therefore belongs to the gateway, not to the query. _Source: spec gateway — Requirement: Google Sign-In (provision or resolve, then check status)._
- **New accounts default to Inactive, so a fresh Google sign-in is refused by design** — this is the platform's invite gate, not a bug report. Activating an account is a manual database operation. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login; RM34 roadmap decision D2._

- **`rejectIfInactive` is a free function on purpose, not a `Handler` method** — `h.google` is a
  concrete `*googleauth.Client` with no fake-able seam, so a method would only be reachable
  through a live OAuth round trip. Extracting the check (the same way `syncLoginLanguageCookie`
  is extracted) is what makes it testable with a hand-built `gin.Context` and a plain
  `account.Account`, with no network call. Do not fold it back into the handler.
  _Source: `internal/gateway/handlers/handlers.go`; RM34-gateway-block-inactive-login._
- **There is no status-gate middleware, and the withdrawn one is why there is no route either** —
  an `AccountActiveGate` per-request middleware was designed and then withdrawn before
  implementation (RM34 roadmap decision D26). It would have needed a `GET /account-blocked`
  redirect target and an `exemptFromStatusGate` allowlist; neither exists, because a newly-gated
  signup has never held a session and the login-time block alone satisfies the requirement.
  Revoking an **already-established** session mid-flight is a different problem — it needs its
  own design, and must not assume this shape (one inline render, no route) generalizes.
  _Source: RM34 roadmap decision D26; `internal/gateway/handlers/handlers.go`._
- **The blocked page always renders in the visitor's PRE-LOGIN language, never the account's
  stored preference — and there is no imprecision to reconcile.** `PreferencesMiddleware` branches
  on `currentUID(c)`; on `/auth/google/callback` no session exists yet (this is the request that
  would create one), so it always takes its anonymous branch — the pre-login `lang` cookie, or
  Spanish by default. `GetAccountSettings` is never consulted for this request at all.
  _Source: `internal/gateway/handlers/handlers.go`, `PreferencesMiddleware`._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file.
