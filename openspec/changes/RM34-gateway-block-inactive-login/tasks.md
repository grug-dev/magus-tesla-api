> **Scope.** Tier 2 of roadmap `RM34-account-vehicle-status` (MAG-33). Blocks login for an Inactive
> account (D4/D5/D6) and adds a per-request gate so deactivation takes effect on the very next
> request, not the next login (D16). Owning module: `internal/gateway`. D7 binds: no new test
> coverage for pre-existing behavior; the code this tier adds gets the coverage `design.md`'s Test
> Contract specifies, and any existing test this tier's changes break gets repaired.
>
> **Hard external dependency.** T3 below is a change to `internal/account` — a **different
> module** the gateway worker may not edit (`ai/architecture.md` §2). T4–T7 cannot compile without
> it. The leader MUST sequence T3 as a small addendum (to tier 1, already archived, or as its own
> mini-change) before dispatching T4 onward. T1, T2, and T8 (docs) do NOT depend on T3 and may
> proceed immediately — they only consume `account.Account.Status`, which tier 1 already shipped.
>
> **Dependency graph:**
> - T1 (blocked page + catalogue keys) — no dependencies. Parallel-ok with T2's early steps, but
>   T2's render call needs T1's component to exist, so sequence T1 before T2 in practice even
>   though they touch disjoint files.
> - T2 (`GoogleCallback` inactive check) — depends on T1. Independent of T3/T4 entirely (uses only
>   tier 1's `Account.Status`, not the new port method).
> - **T3 (account-module port method) — BLOCKED, outside this worker's sandbox.** No dependencies
>   of its own; the leader dispatches it to the `account` module worker whenever convenient, ideally
>   before T4.
> - T4 (`AccountActiveGate` middleware + route wiring) — depends on **T3** (compile) and T1 (the
>   blocked page/route it redirects to).
> - T5 (existing test-double repair) — depends on T3 (the interface only exists to implement once
>   T3 lands) and, for its `rejectIfInactive`/gate-specific test additions, on T2 and T4.
> - T6 (`internal/gateway/AGENTS.md` docs) — depends on T4 (documents the shipped middleware/route).
> - T7 (`go build`/`go vet`/`gofmt`/`i18n-guard` verification) — depends on T1–T6.

## T1. Blocked page + catalogue keys — no dependencies, parallel-ok with T2's non-render steps

- [ ] T1.1 Add two new catalogue keys to `internal/gateway/i18n/catalog.go`, in a new `Key` block
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
      only — not run per Test-Execution-Policy); `make i18n-guard` passes.
- [ ] T1.2 Create `internal/gateway/templates/pages/account_blocked.templ`, mirroring
      `templates/pages/login.templ`'s shell (`layouts.Base`, the login gradient background, `ui.Card`)
      but with no Google button — just the title, the message, and a plain link back to `/login`.
      No view model (static markup + `ctx`-driven `i18n.T` calls, exactly like `Login()`):
      ```templ
      templ AccountBlocked() {
          @layouts.Base(fmt.Sprintf(i18n.T(ctx, i18n.KeyBrandPageTitle), i18n.T(ctx, i18n.KeyAccountBlockedTitle))) {
              <div class="fixed inset-0 -z-10 bg-gradient-to-br from-base-200 via-base-100 to-base-300"></div>
              <main class="flex min-h-screen flex-col items-center justify-center gap-8 px-4 py-16">
                  <div class="flex flex-col items-center gap-2 text-center">
                      <h1 class="text-3xl font-bold text-base-content">{ i18n.T(ctx, i18n.KeyAccountBlockedTitle) }</h1>
                  </div>
                  @ui.Card(ui.CardProps{}) {
                      <p class="text-base-content/80">{ i18n.T(ctx, i18n.KeyAccountBlockedMessage) }</p>
                  }
                  <a href="/login" class="link">{ i18n.T(ctx, i18n.KeyLoginSignIn) }</a>
              </main>
          }
      }
      ```
      Reuses `KeyLoginSignIn` for the back-link text rather than a new key (D6: "reuses the login
      shell" — extend that to reusing its existing vocabulary where the meaning is identical).
      Acceptance: `make templ` regenerates `account_blocked_templ.go` with no manual edits;
      `make ui-guard` passes (no raw DaisyUI class outside `ui/`; only `ui.Card` is composed, plus
      the same bare-utility/layout classes `Login()` already uses).
- [ ] T1.3 Run `make css` if `account_blocked.templ` introduces any Tailwind/DaisyUI class not
      already present in `static/app.css` (unlikely — it reuses `Login()`'s exact class set).
      Commit `app.css` alongside the template per `internal/gateway/AGENTS.md`'s codegen rule if it
      changed; report "no diff" if it did not.

## T2. `GoogleCallback` inactive-account check — depends on T1

- [ ] T2.1 Add `rejectIfInactive` as a new free function in `internal/gateway/handlers/handlers.go`
      (near `render`/`renderError`, same free-function style — no `Handler` receiver needed), per
      `design.md` D24:
      ```go
      func rejectIfInactive(c *gin.Context, acct account.Account) bool {
          if acct.Status == account.StatusActive {
              return false
          }
          blockRequest(c)
          return true
      }
      ```
      (`blockRequest` is introduced in T4 alongside the middleware, since both the callback path and
      the mid-session gate share it — see T4.1. If T4 has not landed yet when this task is
      implemented, inline the two calls `sessions.Default(c).Clear(); _ = sessions.Default(c).Save()`
      then `renderError(c, http.StatusForbidden, pages.AccountBlocked())` directly here and fold them
      into `blockRequest` when T4 lands, to avoid a forward reference across an external
      dependency's landing order.)
- [ ] T2.2 In `GoogleCallback`, call it immediately after `UpsertFromOAuth` succeeds and before
      `h.syncLoginLanguageCookie` / `sess.Set("uid", ...)`:
      ```go
      acct, err := h.acct.UpsertFromOAuth(c.Request.Context(), account.OAuthIdentity{...})
      if err != nil {
          ...
      }
      if rejectIfInactive(c, acct) {
          return
      }
      h.syncLoginLanguageCookie(c, acct.ID)
      sess.Set("uid", acct.ID.String())
      ...
      ```
      Acceptance: reading the function top to bottom, an Inactive account never reaches
      `syncLoginLanguageCookie` or `sess.Set` — confirm by inspection (design.md's spec delta
      scenario "A callback resolving an Inactive account never reaches the language sync or session
      steps").
- [ ] T2.3 Write the direct-call test table from `design.md`'s Test Contract
      (`rejectIfInactive(c, acct)` for `StatusActive` / `StatusInactive` / `""`), in
      `internal/gateway/handlers/handlers_test.go` or a new `account_gate_test.go` (either is fine —
      prefer colocating with T4's gate tests in one new file since both are new-in-this-tier
      coverage for the same feature). Each case asserts: return value, response status, whether a
      response body was written, and that a hand-built pre-populated session ends up cleared for
      the two blocking cases.

## T3. Account-module port method — BLOCKED, owning module `internal/account`, NOT this worker's sandbox

- [ ] T3.1 *(account module worker, not gateway)* Add `ErrAccountNotFound` and
      `StatusFor(ctx, accountID) (string, error)` to `account.Service` (`internal/account/account.go`)
      exactly per `design.md` D17's signature and doc comment.
- [ ] T3.2 *(account module worker)* Add `GetAccountStatus :one` to
      `internal/account/db/query.sql` per D17's exact SQL, run `make sqlc`.
- [ ] T3.3 *(account module worker)* Implement `StatusFor` on the concrete `*service` type in
      `internal/account/service.go` per D17's sketch, mapping `pgx.ErrNoRows` to
      `account.ErrAccountNotFound`.
- [ ] T3.4 *(account module worker)* `go build ./...`, `go vet ./...`, `gofmt -l` pass repo-wide
      after this addition (it will not yet break `internal/gateway` compilation, since T4/T5 haven't
      consumed the new method yet — but it WILL break compilation of any OTHER existing
      `account.Service` implementer/fake outside this module the moment `internal/gateway`'s own
      test doubles are updated in T5; sequence T3 to completion before dispatching T4/T5).
      Acceptance: this task's own report states it is complete and the new method is usable; the
      leader dispatches T4 only after this is confirmed `done`.

## T4. `AccountActiveGate` middleware + `/account-blocked` route — depends on T3, T1

- [ ] T4.1 Create `internal/gateway/handlers/account_gate.go` with: the `exemptFromStatusGate` map
      (D20's exact seven-route table), `isHTMXRequest(c) bool`, `blockRequest(c *gin.Context)` (the
      shared session-clear + full-page-redirect-or-HX-Redirect logic per D19/D22, used by both this
      middleware and T2's `rejectIfInactive`), and `AccountActiveGate(acct account.Service)
      gin.HandlerFunc` per `design.md` D18's full code listing (including the fail-open/fail-closed
      switch from D21).
- [ ] T4.2 Add `AccountBlockedPage(c *gin.Context)` to `handlers.go` (or `account_gate.go`):
      `renderError(c, http.StatusForbidden, pages.AccountBlocked())`, no auth guard, no session
      logic — a plain terminal page.
- [ ] T4.3 Wire in `internal/gateway/gateway.go`: `r.Use(handlers.AccountActiveGate(d.Account))`
      immediately after `r.Use(handlers.LanguageMiddleware(d.Account))`, and
      `r.GET("/account-blocked", h.AccountBlockedPage)` alongside the other public routes
      (`/login`, `/healthz`).
      Acceptance: `go build ./...` succeeds; route ordering in `gateway.go` matches — sessions,
      then LanguageMiddleware, then AccountActiveGate, before any `r.GET`/`r.POST` registration.
- [ ] T4.4 Write the `AccountActiveGate` middleware test table from `design.md`'s Test Contract
      (anonymous / active-full-page / active-htmx / inactive-full-page / inactive-htmx / not-found /
      transient-error / each exempt route), in the new file from T2.3, mirroring
      `langMiddlewareEngine` (`lang_test.go`) — a minimal engine with sessions + `AccountActiveGate`
      + a downstream probe route, `httptest`-driven, no real DB.

## T5. Existing test-double repair — depends on T3 (compile), T2 and T4 (functional coverage)

- [ ] T5.1 Add `StatusFor` to `fakeAccount` (`internal/gateway/handlers/handlers_test.go`),
      defaulting to `account.StatusActive` when unset — mirroring the existing `language`/`language`
      zero-value-defaults-to-ES pattern on the same struct — plus a `statusErr` field for the
      not-found/transient-error test cases:
      ```go
      // status is the value StatusFor returns. The zero value ("") is treated as
      // StatusActive so every pre-existing test (which never sets this field) keeps
      // its original signed-in-and-unblocked behavior unchanged.
      status    string
      statusErr error
      ```
      ```go
      func (f *fakeAccount) StatusFor(context.Context, uuid.UUID) (string, error) {
          if f.statusErr != nil {
              return "", f.statusErr
          }
          if f.status == "" {
              return account.StatusActive, nil
          }
          return f.status, nil
      }
      ```
      Acceptance: `go build ./...` and `go vet ./...` succeed on `internal/gateway/...` once T3 has
      landed; every pre-existing test in this package that never sets `status`/`statusErr` is
      unaffected (inspection: none of them build an engine that wires `AccountActiveGate`, since
      that only happens inside `gateway.NewEngine`, package `gateway` — a different package from
      `handlers_test.go`'s ad-hoc engines).
- [ ] T5.2 Confirm by inspection whether `gateway_test.go` (`package gateway_test`, uses the REAL
      `account.NewService(pool, "", "")` against an unreachable pool) needs any change. Every
      existing test there only exercises anonymous-redirect paths (no `uid` ever set in session)
      per the file's current tests, so `AccountActiveGate`'s `StatusFor` call is never reached and
      no fake/stub is needed there. Report which test functions were reviewed and confirmed
      unaffected — do not add coverage here (D7); T4.4's own new engine covers the gate.
- [ ] T5.3 Confirm no other file in `internal/gateway` defines a second, independent
      `account.Service` test double that also needs `StatusFor` (searched during artifact creation:
      `fakeAccount` in `handlers_test.go` is the only one; re-confirm at implementation time in case
      a file was added between artifact creation and implementation).

## T6. Docs (`internal/gateway/AGENTS.md`) — depends on T4

- [ ] T6.1 Add a new dated entry documenting: `AccountActiveGate`'s existence and registration
      point, the `exemptFromStatusGate` table (so a future new route's author knows to consider
      whether it needs to be added), the `/account-blocked` route, and the full-page-redirect vs.
      `HX-Redirect` split with a one-line pointer to `design.md` D19's htmx-header-on-3xx rationale
      (`CLAUDE.md`'s "workflow & architectural decisions are documented with their steps" — this is
      exactly that kind of decision: a new required middleware/route, not just a page).
      Keep it proportional to the existing RD8-style entries (RD9–RD13) in that file — rationale +
      rejected alternative in a few paragraphs, not a restatement of all of `design.md`.
- [ ] T6.2 Update the "Public interface" section to note `Deps.Account` now also backs
      `AccountActiveGate` (not just `LanguageMiddleware` and the write-path handlers), so a future
      reader does not assume `account.Service` is only consulted per-handler.

## T7. Verification — depends on T1–T6

- [ ] T7.1 `go build ./...`, `go vet ./...` pass repo-wide.
- [ ] T7.2 `gofmt -l` reports no diffs for any file this tier touched.
- [ ] T7.3 `make i18n-guard` passes (T1's two new keys go through `i18n.T`, no bypass).
- [ ] T7.4 `make ui-guard` passes (`account_blocked.templ` composes `ui.Card`, no raw DaisyUI class
      inlined).
- [ ] T7.5 Boundary check: no file under `internal/account` was created or edited by this worker
      (T3 is explicitly out of sandbox); `internal/gateway` still calls `account.Service` only
      through the interface, never a database.
- [ ] T7.6 `openspec validate RM34-gateway-block-inactive-login --strict` passes and every
      checkbox above reflects real completion.
- [ ] T7.7 Report the exact test-suite commands the owner must run:
      `go test ./internal/gateway/...` (covers T2.3's and T4.4's new tests) and the full
      `go test ./...` / `make test-with-db` — this tier writes tests but does not execute them
      (`Test-Execution-Policy`); the owner's run is what turns T2/T4/T5 from
      `awaiting-user-verification` into `done`.
