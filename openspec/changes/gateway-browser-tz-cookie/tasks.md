> **Retroactive reconstruction.** This change was implemented and shipped directly to `main` in
> commit `dd364e3` ("Updating", 2026-08-11) — a single manual commit that bypassed the
> `kkpa-dev-harness-pipeline`. No `tasks.md` or `progress.json` existed for it at the time. The
> checkboxes below were reconstructed AFTER THE FACT from the shipped diff
> (`git show --stat dd364e3` / `git show dd364e3 -- <path>`) and the current state of
> `internal/gateway/handlers/tz.go`, `history.go`, `handlers.go`, and
> `templates/layouts/base.templ`, plus the existing tests in `history_test.go`. They document
> what actually happened, not a plan that was followed — every box is checked because the work
> is already done and verified against the live code, not because it was tracked live during
> implementation. `internal/gateway/handlers/charges.go`'s later reuse of `browserToday(c)` (added
> in a separate, later commit `69eb299` "Edit manual records required", entangled with the
> already-archived `2026-08-11-gateway-improve-manual-charge-form` MAG-5 change) is **excluded**
> from this list — it did not ship as part of `dd364e3` and is out of this change's scope.

## 1. Browser-timezone helper (`internal/gateway/handlers/tz.go`, new file)

- [x] 1.1 Add `browserTZCookieName = "browser_tz"` constant and `browserLocation(c *gin.Context)
      *time.Location`: reads the `browser_tz` cookie, parses it via `time.LoadLocation`, returns
      `time.UTC` on any failure (cookie absent, empty value, or a value `time.LoadLocation` cannot
      resolve as an IANA zone) — never returns an error to the caller.
- [x] 1.2 Add `browserLocationFromHeader(r *http.Request) *time.Location` — the `http.Header`-based
      variant used by tests that build a raw `*http.Request` without a `gin.Context`. Same
      fallback rules as 1.1.
- [x] 1.3 Add `startOfDayIn(t time.Time, loc *time.Location) time.Time` — generalizes the existing
      UTC-only `startOfDay` (kept, unchanged, still used elsewhere) to an arbitrary
      `*time.Location`, returning local midnight for `t` in that zone.
- [x] 1.4 Add `browserToday(c *gin.Context) time.Time` — `startOfDayIn(time.Now(),
      browserLocation(c))`; the single entry point every caller uses to get "today" in the
      browser's zone (UTC when no cookie / malformed cookie).

## 2. Wire browserToday into the history endpoint (`internal/gateway/handlers/history.go`)

Depends on: 1 (needs `browserToday`).

- [x] 2.1 `parseHistoryRange(c)`: replace `startOfDay(time.Now())` with `browserToday(c)` for both
      the default-absent window's `end` and the `end <= today` cap comparison
      (`e.After(today)`). `start`/`end` query params stay bare `YYYY-MM-DD` UTC-midnight-bounded
      strings — only the comparison target ("today") became browser-aware.
- [x] 2.2 `buildHistoryPresets(start, end time.Time)` → `buildHistoryPresets(start, end, today
      time.Time)`: `yesterday := today.AddDate(0, 0, -1)` now derives from the passed-in `today`
      (the caller's `browserToday(c)`) instead of computing `startOfDay(time.Now())` internally.
- [x] 2.3 `buildHistoryView(ctx, uid, teslaID, start, end time.Time)` → add a `today time.Time`
      parameter, threaded through to `buildHistoryPresets(start, end, today)`.
- [x] 2.4 `DashboardHistoryFragment`: pass `browserToday(c)` at both call sites that build a
      `HistoryView`/`buildHistoryView` — the no-selected-vehicle empty-state branch and the normal
      render path.

## 3. Wire browserToday into the dashboard default href (`internal/gateway/handlers/handlers.go`)

Depends on: 1.

- [x] 3.1 `defaultHistoryHref()` → `defaultHistoryHref(today time.Time)`: `end :=
      today.AddDate(0, 0, -1)` derives "yesterday" from the passed-in browser-local `today`
      instead of `startOfDay(time.Now())`.
- [x] 3.2 `dashboardFor(ctx, uid, selectedTeslaID int64)` → add a `today time.Time` parameter,
      threaded to `defaultHistoryHref(today)`.
- [x] 3.3 `Dashboard` and `DashboardFragment` handlers: pass `browserToday(c)` into
      `h.dashboardFor(...)`.

## 4. Set the cookie (`internal/gateway/templates/layouts/base.templ`)

Depends on: nothing (independent of 1–3; only the browser needs to have run this before the
server-side helpers see a cookie).

- [x] 4.1 Add a single inline `<script>` inside `BaseAuth` (the authenticated shell only — never
      `Base`, the anonymous shell) that reads
      `Intl.DateTimeFormat().resolvedOptions().timeZone` and sets
      `document.cookie = "browser_tz=" + encodeURIComponent(tz) + ";path=/;max-age=31536000;SameSite=Lax"`,
      wrapped in `try/catch` so a JS failure never breaks page rendering.
- [x] 4.2 Regenerate `base_templ.go` (`make templ`).

## 5. Tests (`internal/gateway/handlers/history_test.go`)

Depends on: 1, 2, 3, 4.

- [x] 5.1 `TestBrowserLocation_Fallbacks` — missing cookie → UTC; valid IANA name (`America/Bogota`)
      → that location; malformed name (`Not/A/Zone`) → UTC; empty value → UTC.
- [x] 5.2 `TestParseHistoryRange_DefaultUsesBrowserToday` — with a `browser_tz=America/Bogota`
      cookie, the default-absent window's `end` equals `browserToday(c)` in that zone, not UTC
      midnight.
- [x] 5.3 `TestParseHistoryRange_EndCapUsesBrowserToday` — `end` equal to browser-today is
      accepted; `end` equal to browser-tomorrow is rejected (future in the browser's frame), using
      the same cookie the handler would see.
- [x] 5.4 `TestParseHistoryRange_NoCookieFallsBackToUTC` — with no `browser_tz` cookie, the
      default-absent window's `end` equals UTC `startOfDay(time.Now())`, matching pre-change
      behavior.
- [x] 5.5 `TestBaseAuth_RendersBrowserTZScript` — renders `layouts.BaseAuth` and asserts the body
      contains `browser_tz=`, `Intl.DateTimeFormat().resolvedOptions().timeZone`, and
      `SameSite=Lax`.

## 6. Verification

- [x] 6.1 `go test ./internal/gateway/...` green (includes the pre-existing RM8 history suite,
      unaffected by the TZ threading, plus the new tests in 5.1–5.5).
- [x] 6.2 `go vet ./...` exit 0.

## 7. OpenSpec paperwork (this dispatch — retroactive, done after the fact)

- [x] 7.1 Write the missing delta spec `openspec/changes/gateway-browser-tz-cookie/specs/gateway/spec.md`
      (`## MODIFIED Requirements` → `### Requirement: Dashboard History Charts`), verified against
      the shipped source and tests rather than trusted from the proposal.
- [x] 7.2 Write this `tasks.md`, reconstructed retroactively and marked as such.
- [x] 7.3 No `design.md` — no database object is touched by this change (`openspec/config.yaml`'s
      design-required rule does not apply), and `proposal.md` already carries the design
      narrative.
