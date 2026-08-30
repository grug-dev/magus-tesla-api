> **Scope.** Tier 2 of roadmap `RM34-account-vehicle-status` (MAG-33). Blocks login for an Inactive
> account (D4/D5/D6). Owning module: `internal/gateway`. D7 binds: no new test coverage for
> pre-existing behavior; the code this tier adds gets the coverage `design.md`'s Test Contract
> specifies, and any existing test this tier's changes break gets repaired.
>
> **Rescoped per roadmap D26 (2026-08-30).** The per-request `AccountActiveGate` middleware
> (roadmap D16) is withdrawn — see `design.md`'s top-of-file note and roadmap D26. The former T3
> (an `internal/account` port method, outside this worker's sandbox), T4 (the middleware + its
> route), and the gate-specific parts of T5/T6 are **dropped**. This tier no longer has any
> external module dependency — it depends only on tier 1's already-shipped `account.Account.Status`.
>
> **Dependency graph (post-rescope):**
> - T1 (blocked page + catalogue keys) — no dependencies.
> - T2 (`GoogleCallback` inactive check) — depends on T1. Uses only tier 1's `Account.Status`.
> - T5 (existing test-double repair) — depends on T1, T2. Confirms no existing test needs a new
>   fake method (there is none to add — no new port method exists in this scope).
> - T6 (`internal/gateway/AGENTS.md` docs) — depends on T2 (documents the shipped login block).
> - T7 (`go build`/`go vet`/`gofmt`/`i18n-guard`/`ui-guard` verification) — depends on T1, T2, T5, T6.

## T1. Blocked page + catalogue keys — no dependencies

- [x] T1.1 Add two new catalogue keys to `internal/gateway/i18n/catalog.go`, in a new `Key` block
      near `KeyLogin*` (mirror the existing group-comment style), both `ES`/`EN` on the catalog map
      literal in the same line per `design.md` D1's existing convention:
      ```go
      KeyAccountBlockedTitle   Key = "account_blocked.title"
      KeyAccountBlockedMessage Key = "account_blocked.message"
      ```
      ```go
      KeyAccountBlockedTitle:   {ES: "Cuenta desactivada", EN: "Account deactivated"},
      KeyAccountBlockedMessage: {ES: "Tu cuenta está desactivada. Escribe a cristiancamilopena@gmail.com para solicitar acceso.", EN: "Your account is deactivated. Contact cristiancamilopena@gmail.com to request access."},
      ```
      The contact address is baked directly into both strings (D5: hardcoded, not config, not a
      format placeholder — there is exactly one address and no variation to parameterize).
      Acceptance: `TestCatalog_AllKeysHaveBothLanguages` covers these by construction (inspection
      only — not run per Test-Execution-Policy); `make i18n-guard` passes. **Verified: `make
      i18n-guard` passes.**
- [x] T1.2 Create `internal/gateway/templates/pages/account_blocked.templ`, mirroring
      `templates/pages/login.templ`'s shell (`layouts.Base`, the login gradient background, `ui.Card`)
      but with no Google button — just the title, the message, and a plain link back to `/login`.
      No view model (static markup + `ctx`-driven `i18n.T` calls, exactly like `Login()`).
      Reuses `KeyLoginSignIn` for the back-link text rather than a new key (D6: "reuses the login
      shell" — extend that to reusing its existing vocabulary where the meaning is identical).
      Acceptance: `make templ` regenerates `account_blocked_templ.go` with no manual edits;
      `make ui-guard` passes (no raw DaisyUI class outside `ui/`; only `ui.Card` is composed, plus
      the same bare-utility/layout classes `Login()` already uses). **Verified: `make templ` and
      `make ui-guard` both pass.**
- [x] T1.3 Run `make css` if `account_blocked.templ` introduces any Tailwind/DaisyUI class not
      already present in `static/app.css`. **No diff** — the page reuses `Login()`'s exact class
      set, so `make css` was not run.

## T2. `GoogleCallback` inactive-account check — depends on T1

- [x] T2.1 Add `rejectIfInactive` as a new free function in `internal/gateway/handlers/handlers.go`
      (near `render`/`renderError`/`randomState`, same free-function style — no `Handler` receiver
      needed), per `design.md` D24. No `blockRequest` helper — that existed only to be shared with
      the now-withdrawn middleware; `rejectIfInactive` renders directly.
- [x] T2.2 In `GoogleCallback`, call it immediately after `UpsertFromOAuth` succeeds and before
      `h.syncLoginLanguageCookie` / `sess.Set("uid", ...)`. Acceptance: reading the function top to
      bottom, an Inactive account never reaches `syncLoginLanguageCookie` or `sess.Set` — confirmed
      by inspection (design.md's spec delta scenario "A callback resolving an Inactive account
      never reaches the language sync or session steps").
- [x] T2.3 Write the direct-call test table from `design.md`'s Test Contract
      (`rejectIfInactive(c, acct)` for `StatusActive` / `StatusInactive` / `""`), in
      `internal/gateway/handlers/account_gate_test.go`. Each case asserts: return value, response
      status, and whether a response body was written. **Written — awaiting-user-verification**
      (Test-Execution-Policy: tests are written, not run, by this worker).

## T3. REMOVED — was an `internal/account` port method, outside this worker's sandbox

Dropped with roadmap D16/D26. `account.Service.StatusFor` is not added by this change.

## T4. REMOVED — was the `AccountActiveGate` middleware + `/account-blocked` route

Dropped with roadmap D16/D26. No new middleware, no new route.

## T5. Existing test-double repair — depends on T1, T2

- [x] T5.1 Confirm no existing test double needs a new method: this tier adds no new
      `account.Service` port method (T3/D17 dropped), so `fakeAccount`
      (`internal/gateway/handlers/handlers_test.go`) needs no change. Verified by inspection: no
      test in the repo calls `GoogleCallback` directly today (`grep -rn "\.GoogleCallback(" 
      internal/gateway/` returns nothing) — the only httptest coverage of `/auth/google/callback`
      is `TestGoogleCallback_RejectsMismatchedState` (`gateway_test.go`), which returns 400 before
      `UpsertFromOAuth` is ever reached, so it is unaffected by this tier's change.
- [x] T5.2 Confirm `gateway_test.go` (`package gateway_test`, uses the REAL
      `account.NewService(pool, "", "")` against an unreachable pool) needs no change. Every
      existing test there only exercises anonymous-redirect or mismatched-state paths — none
      reaches the `rejectIfInactive` call. No fake/stub needed there.
- [x] T5.3 Confirm no other file in `internal/gateway` defines a second, independent
      `account.Service` test double. `fakeAccount` in `handlers_test.go` is the only one.

## T6. Docs (`internal/gateway/AGENTS.md`) — depends on T2

- [ ] T6.1 Add a dated entry (or extend an existing one) noting `rejectIfInactive`'s existence,
      where it runs in `GoogleCallback`, and that `pages.AccountBlocked()` has exactly one render
      site (no `/account-blocked` route exists — the withdrawn gate's route was never built).

## T7. Verification — depends on T1, T2, T5, T6

- [x] T7.1 `go build ./...`, `go vet ./...` pass repo-wide.
- [x] T7.2 `gofmt -l` reports no new diffs introduced by this tier's files.
- [x] T7.3 `make i18n-guard` passes (T1's two new keys go through `i18n.T`, no bypass).
- [x] T7.4 `make ui-guard` passes (`account_blocked.templ` composes `ui.Card`, no raw DaisyUI class
      inlined).
- [x] T7.5 Boundary check: no file under `internal/account` was created or edited by this worker;
      `internal/gateway` still calls `account.Service` only through the interface, never a
      database, and calls no method that does not exist on that interface.
- [ ] T7.6 `openspec validate RM34-gateway-block-inactive-login --strict` passes and every
      checkbox above reflects real completion.
- [x] T7.7 Report the exact test-suite commands the owner must run:
      `go test ./internal/gateway/...` (covers T2.3's new tests) and the full `go test ./...` /
      `make test-with-db` — this tier writes tests but does not execute them
      (`Test-Execution-Policy`); the owner's run is what turns T2 from
      `awaiting-user-verification` into `done`.
