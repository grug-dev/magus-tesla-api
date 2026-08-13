# Tasks — RM24-gateway-translate-all-pages

> Full rationale for every decision referenced below (D1–D5) lives in `design.md`. Group T1 is a
> hard, single-writer prerequisite for every other group (design.md D4 — avoids concurrent edits
> to `internal/gateway/i18n/catalog.go`). Every group from T2 onward depends ONLY on T1, not on
> each other, and touches a disjoint file (or file pair) — so T2–T15 can run in parallel, **with
> one exception: T11 additionally depends on T9**, because T9.3 and T11 both edit
> `internal/gateway/handlers/handlers.go` (leader amendment at artifact reconcile — the same
> same-file collision class D4 serializes `catalog.go` for).
> Group T16 (codegen) depends on every template-touching group; T17 (final verification) depends
> on everything.

## T1. Add every new catalogue key — no dependencies (BLOCKS all other groups)

Single-writer task: adds every `Key` constant + `catalog` entry this tier needs to
`internal/gateway/i18n/catalog.go` in one pass, so no other task ever edits this file again
(design.md D4). Organize with the same comment-header-per-surface style as the existing file.
Reuse an existing tier-2 key instead of minting a duplicate wherever design.md D2's reuse table
applies (`KeyNavHeaderConnectLink`, `KeyNavHeaderNoTesla`, `KeyNavLogout`, `KeyNavDashboard`,
`KeyNavSuperchargerStats`).

- [x] T1.1 Brand/proper nouns (design.md D2): `KeyBrandMagus` (`{ES: "Magus", EN: "Magus"}`),
      `KeyBrandTeslaCore` (`{ES: "TESLA CORE", EN: "TESLA CORE"}`).
- [x] T1.2 Home page keys (`home.*`): signed-in-as prefix, "View your vehicles", "Sign in with
      Google".
- [x] T1.3 Login page keys (`login.*`): "Precision Fleet Monitoring", "Continue with Google",
      "Privacy", "Terms", "Support".
- [x] T1.4 Dashboard page keys (`dashboard.*`): "Stale" badge, "Odometer", "Interior", "Exterior",
      "Status", "Last updated" prefix, "Range" prefix, "Vehicle Status" card title, "Battery" card
      title, "Awaiting first snapshot".
- [x] T1.5 Dashboard status keys (`dashboard_status.*`, design.md D5): "Charging", "Parked",
      `dashboard_status.software_version` = `{ES: "Software v%s", EN: "Software v%s"}` (D3
      interpolated).
- [x] T1.6 Shared charge-form keys (`charges_form.*`, design.md D2 — one namespace for both
      `charge_create_form.templ` and `charge_row_edit.templ`): "Log a charge", "Date", "Energy
      added (kWh)", "Price", "Currency", "Location", "Home", "Work", "Other", "Started at", "Ended
      at", "Start battery %", "End battery %", "More details", "Charging type", "AC", "DC",
      "Location label", "Notes", "Log charge", plus edit-form-only: "Vehicle", "Location kind",
      "Save", "Cancel".
- [x] T1.7 Charge-row keys (`charges_row.*`): "Edit", "Delete", `charges_row.confirm_message` =
      `{ES: "¿Eliminar la carga de %s registrada el %s? Esta acción no se puede deshacer.", EN:
      "Delete the %s charge logged on %s? This cannot be undone."}` (D3 interpolated, two `%s`
      args), "Delete charge entry" (confirm title), "Delete entry" (confirm label).
- [x] T1.8 Charges-list keys (`charges_list.*`): "Your entries", "Refresh", the empty-state
      sentence.
- [x] T1.9 Charges-page keys (`charges_page.*`): "Charge log", "Back to dashboard" (also reused by
      the Supercharger Stats page — design.md D2).
- [x] T1.10 History keys (`history.*`): "Awaiting nightly snapshots", "Odometer history", "Battery
      history", `history.days_preset` = `{ES: "%d días", EN: "%d days"}` (D3), and
      `history.no_snapshot_tooltip` = `{ES: "%s · sin dato", EN: "%s · no snapshot"}` (D3).
- [x] T1.11 Supercharger keys (`supercharger.*`): `supercharger.months_preset` = `{ES: "%d meses",
      EN: "%d months"}` (D3), "Sessions", "Energy", "Cost", "Avg kWh / session", "kWh per month",
      "No Supercharger sessions in this window.", "Date", "Site", "Country", "Billing Type".
- [x] T1.12 Vehicles-fragment keys (`vehicles.*`, design.md Discoveries #3): "Battery:" / "Range:"
      labels, "Charging:", "Odometer:", "Inside:" / "Outside:", "Locked", "Unlocked", "Sentry:",
      "Not reported", "On", "Off", "Last updated:", "Stale" badge text, "No data yet — awaiting
      first nightly snapshot." (reuse `KeyNavHeaderNoTesla`/`KeyNavHeaderConnectLink` for the
      no-Tesla branch per D2).
- [x] T1.13 Nav-header last-seen keys (`nav_header.*`, design.md D3/Discoveries #2): "Last seen"
      prefix, plus the six `relativeLastSeen` phrasing keys: `nav_header.last_seen_days`,
      `nav_header.last_seen_days_plural`, `nav_header.last_seen_hours`,
      `nav_header.last_seen_hours_plural`, `nav_header.last_seen_minutes_plural`,
      `nav_header.last_seen_just_now` — exact ES/EN values per design.md D3's table.
- [x] T1.14 Confirm-dialog keys (`confirm_dialog.*`): "Are you sure?", "Cancel", "Confirm",
      "Delete", "Close" (aria-label).
- [x] T1.15 Handler notice/error keys: `vehicles_notice.*` (6 strings — could-not-load-vehicles,
      telemetry-unavailable, could-not-reach-tesla, session-expired, could-not-load-from-tesla,
      no-vehicles-found), `dashboard_notice.*` (1 — could-not-load-dashboard), `oauth_error.*` (11
      bare-text strings from `handlers.go`'s `c.String` calls), `charges_error.*` (11 validation
      messages + 4 notices + `charges_error.battery_suggestion` = `{ES: "Última: %d%%", EN:
      "Latest: %d%%"}` D3-interpolated + 7 bare-text strings from `charges.go`), `lang_switch_error.*`
      (3 bare-text strings from `lang.go`).
- [x] T1.16 `go build ./internal/gateway/i18n/...` compiles clean; `go test
      ./internal/gateway/i18n/...` green (`TestCatalog_AllKeysHaveBothLanguages` passes for every
      new key).

## T2. Home + Login pages — depends on T1

- [x] T2.1 `templates/pages/home.templ`: replace every literal with `i18n.T(ctx, i18n.KeyXxx)`
      per T1.1–T1.3 + reused `KeyNavHeaderConnectLink`/`KeyNavLogout`.
- [x] T2.2 `templates/pages/login.templ`: same, per T1.1/T1.3.
- [x] T2.3 `templates/layouts/base.templ`: `<h1>`/nav-bar "Magus" text → `i18n.T(ctx,
      i18n.KeyBrandMagus)` (both the `home.templ` and `base.templ` occurrences share this key).

## T3. Dashboard page + helper — depends on T1

- [x] T3.1 `templates/pages/dashboard.templ`: replace every literal per T1.4, reusing
      `KeyNavHeaderConnectLink`/`KeyNavHeaderNoTesla`/`KeyNavDashboard` per D2's reuse table.
- [x] T3.2 `templates/pages/dashboard.go`: `dashSubtitle` gains an explicit `ctx` parameter (its
      one call site is inside a `.templ` block, mirroring `navItems(ctx, active)`); replace
      "Awaiting first snapshot" with `i18n.T(ctx, i18n.KeyDashboardAwaitingSnapshot)`. Update the
      `dashSubtitle(d)` call site in `dashboard.templ` to `dashSubtitle(ctx, d)`.

## T4. Charge forms (create + inline edit) — depends on T1

- [x] T4.1 `templates/fragments/charge_create_form.templ`: replace every field label / option
      text / submit button per T1.6, using the shared `charges_form.*` keys.
- [x] T4.2 `templates/fragments/charge_row_edit.templ`: same fields reuse the identical
      `charges_form.*` keys T4.1 used; "Vehicle", "Location kind", "Save", "Cancel" use their own
      T1.6 keys.

## T5. Charge rows, list, and page — depends on T1

- [x] T5.1 `templates/fragments/charge_row.templ`: "Edit"/"Delete" buttons and the three
      `data-confirm-*` attributes per T1.7; the `hx-confirm` message becomes
      `fmt.Sprintf(i18n.T(ctx, i18n.KeyChargesRowConfirmMessage), vm.EnergyKWh,
      vm.ChargedOnLabel)` (D3).
- [x] T5.2 `templates/fragments/charges_list.templ`: "Your entries"/"Refresh"/empty-state per
      T1.8, plus the `ui.Table` `Headers` slice (8 columns) now wired to the
      `i18n.KeyChargesListHeader*` keys added for T18.1. Completed by T18.1.
- [x] T5.3 `templates/pages/charges.templ`: "Charge log"/"Back to dashboard" per T1.9.

## T6. History chart + preset handler — depends on T1

- [x] T6.1 `templates/fragments/history.templ`: "Awaiting nightly snapshots"/"Odometer
      history"/"Battery history" per T1.10.
- [x] T6.2 `internal/gateway/handlers/history.go`: the preset-button `Label` becomes
      `fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryDaysPreset), n)`; the missing-snapshot bar
      `Tooltip` becomes `fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryNoSnapshotTooltip), label)` (both
      call sites — the numDays≥2 and the single-day branch). Thread `ctx` into the two chart-
      building functions if not already present (their callers already have it).

## T7. Supercharger Stats fragment — depends on T1

- [x] T7.1 `templates/fragments/supercharger_stats.templ`: month-preset label via
      `fmt.Sprintf(i18n.T(ctx, i18n.KeySuperchargerMonthsPreset), p)` (both branches); "Sessions",
      "Energy", "Cost", "Avg kWh / session", "kWh per month", the empty-state sentence, and the
      table headers ("Date", "Site", "Country", "Billing Type"; "Energy"/"Cost" reuse the tile
      labels) per T1.11.
      **Explicitly out of scope (design.md Non-Goals / Discoveries #4):** `DateLabel` and
      `BillingType` in `internal/gateway/handlers/supercharger.go` stay as-is — do not touch.

## T8. Vehicles fragment (dead code, in scope per roadmap) — depends on T1

- [x] T8.1 `templates/fragments/vehicles.templ`: every literal per T1.12 (design.md Discoveries
      #3 — translate despite no current caller; do not delete or special-case).

## T9. Foundation fixes: `<html lang>` bug + nav-header "Last seen" — depends on T1

- [x] T9.1 `templates/layouts/base.templ`: fix `<html lang="en" ...>` →
      `<html lang={ i18n.FromContext(ctx) } ...>` (design.md Discoveries #1 — a real bug, not a
      missing translation).
- [x] T9.2 `templates/fragments/nav_header.templ`: replace the hardcoded "Last seen" prefix with
      `i18n.T(ctx, i18n.KeyNavHeaderLastSeen)` (T1.13).
- [x] T9.3 `internal/gateway/handlers/handlers.go`: `relativeLastSeen` gains an explicit `ctx`
      third parameter (its one call site, inside `navHeaderFor`, already has `ctx` in scope);
      each of its six branches returns the corresponding T1.13 key via `i18n.T(ctx, key)` or
      `fmt.Sprintf(i18n.T(ctx, key), n)` per design.md D3's table (closes tier 2's D9-flagged
      item).

## T10. Confirm dialog — depends on T1

- [x] T10.1 `templates/ui/confirm_dialog.templ`: "Are you sure?", "Cancel", "Confirm", "Delete",
      "Close" per T1.14.

## T11. `handlers.go` — depends on T1 **and T9**

> **Leader amendment (artifact reconcile).** T9.3 also edits `internal/gateway/handlers/handlers.go`
> (`relativeLastSeen`). T11 therefore CANNOT run in parallel with T9 — that is the same
> same-file merge-collision class design.md D4 invoked to serialize `catalog.go`. Run T9 first,
> then T11. Every other group remains parallel-safe.

- [x] T11.1 `vehiclesFor`: replace all 6 `Notice` literals with `i18n.T(ctx, key)` per T1.15
      (`vehicles_notice.*`).
- [x] T11.2 `dashboardFor`: replace the `Notice` literal per T1.15 (`dashboard_notice.*`).
- [x] T11.3 `dashStatus`: gains an explicit `ctx` parameter (mirrors D5); returns `i18n.T(ctx,
      i18n.KeyDashboardStatusCharging)` / `...Parked` (T1.5). `mapDashboardSnapshot` threads `ctx`
      through to `dashStatus` and wraps the `"Software v" + snap.CarVersion` concatenation as
      `fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardStatusSoftwareVersion), snap.CarVersion)` (T1.5,
      D3). `dashboardFor` (which already has `ctx`) passes it to `mapDashboardSnapshot`.
- [x] T11.4 Every bare-text `c.String(http.Status[45]xx, "...")` call in `handlers.go` (11 sites:
      invalid vehicle ×2, invalid vehicle id, could not validate vehicle, vehicle not in your
      account, could not start Tesla connect, invalid oauth state, Tesla connect failed, could not
      save Tesla connection, could not start login, google login failed, could not provision
      account) becomes `c.String(http.Status[45]xx, i18n.T(c.Request.Context(), i18n.KeyXxx))`
      per T1.15 (`oauth_error.*`).
- [x] T11.5 Mark `Healthz`'s `c.String(http.StatusServiceUnavailable, "unhealthy: %v", err)` line
      with `// i18n:allow: ops health-check response, not user-facing UI` (design.md Discoveries
      #5) — leave the string as-is, do not translate.
- [x] T11.6 `go build ./internal/gateway/...` compiles clean after this task (confirms `ctx`
      threading didn't break any call site).

## T12. `charges.go` — depends on T1

- [x] T12.1 All 11 validation-message assignments (`errs["..."] = "..."`) become `errs["..."] =
      i18n.T(ctx, i18n.KeyXxx)` per T1.15 (`charges_error.*`).
- [x] T12.2 The 4 `Notice`/`Error`/`pageError` literal assignments become `i18n.T(ctx, key)` calls.
- [x] T12.3 The `"Latest: %d%%"` suggestion becomes `fmt.Sprintf(i18n.T(ctx,
      i18n.KeyChargesErrorBatterySuggestion), s.BatteryLevelPct)` (D3).
- [x] T12.4 All 7 bare-text `c.String(http.Status[45]xx, "...")` calls become `i18n.T(ctx, key)`
      wrapped per T11.4's pattern.

## T13. `lang.go` — depends on T1

- [x] T13.1 All 3 bare-text `c.String(...)` calls in `LangSwitch` become `i18n.T(c.Request.Context(),
      key)` per T1.15 (`lang_switch_error.*`).

## T14. `make i18n-guard` — depends on T1 (build); full clean run depends on T2–T13

- [x] T14.1 Add the `i18n-guard` target to the `Makefile`, mirroring `ui-guard`'s shape exactly
      (design.md D1): pass 1 (two `grep -rnE` patterns over
      `internal/gateway/templates/{pages,fragments,ui}/*.templ`, excluding lines containing
      `i18n.T(`, and excluding a match whose own line **or the line immediately above it** carries
      `i18n:allow` — see design.md D1's marker-placement amendment: in `.templ` the marker MUST sit
      on the preceding line, so a line-local `grep -v` filter leaves the escape hatch inert and is
      a bug); pass 2 (`grep -rnE` over `internal/gateway/handlers/*.go`
      excluding `_test.go`, for the four message-sink patterns in design.md D1, excluding lines
      containing `i18n:allow` — same-line marker is correct here). Non-zero exit + a clear error
      message (mirroring `ui-guard`'s "ERROR: ..." block) on any match.
- [x] T14.1b Prove the escape hatch fires: temporarily mark one deliberately-flagged `.templ` line
      with a preceding-line `// i18n:allow: <reason>` comment, confirm `make i18n-guard` goes from
      red to green, then revert the temporary marker. A hatch that was never exercised is not
      verified.
- [x] T14.2 Wire it into the gate: `check: build vet ui-guard i18n-guard test`.
- [x] T14.3 Run `make i18n-guard` against the fully-swept tree (after T2–T13 complete) — MUST
      pass clean. If it flags a false positive on a legitimate literal not yet covered by this
      tier's Discoveries (e.g. a genuinely non-translatable value), add an inline `// i18n:allow:
      <reason>` marker rather than weakening the grep pattern.
      **Closed by T19**: `KeyDashboardChargeLimit` added to `catalog.go` (T19.1) and wired at
      `internal/gateway/handlers/handlers.go:423` (T19.2). `make i18n-guard` now passes clean
      against the fully-swept tree.

## T15. Docs — depends on T1 (references T14's new target)

- [x] T15.1 `internal/gateway/AGENTS.md`: extend the existing "i18n" section with a short
      addendum — `make i18n-guard` is the mechanical companion to
      `TestCatalog_AllKeysHaveBothLanguages`, now part of `make check`; document the
      `// i18n:allow: <reason>` marker as the sole exemption mechanism (no separate allowlist
      file).

## T16. Codegen — depends on T2, T3, T4, T5, T6, T7, T8, T9, T10 (every template-touching group)

- [x] T16.1 `make templ` (regenerates every touched `*_templ.go`).
- [x] T16.2 `make css`; `git diff --stat internal/gateway/static/app.css` — confirm it changed, or
      document that no new Tailwind/DaisyUI class was introduced (this tier is text-only; no new
      markup structure is expected) per the module's "Gotcha — stale CSS" CI guard.
      **Resolution:** `git diff --stat internal/gateway/static/app.css` is EMPTY (no diff) — no new
      Tailwind/DaisyUI class was introduced, which is the documented-alternative branch of this
      criterion. This tier's edits are text-only (i18n.T call sites + fmt.Sprintf wrapping).
      *(Original criterion text restored by the leader at review round 1, finding F2: it had been
      replaced in place rather than appended to. Append-only discipline — nothing above is edited.)*

## T17. Full verification — depends on T11, T12, T13, T14, T15, T16

- [x] T17.1 `go build ./...` clean repo-wide.
- [x] T17.2 `go vet ./...` clean repo-wide.
- [x] T17.3 `go test ./...` green, including `TestCatalog_AllKeysHaveBothLanguages` and every
      pre-existing test whose assertions touched a now-translated string (update any test that
      asserted on the literal English text to assert on the resolved-language-appropriate string
      instead, mirroring tier 2's T6.4 precedent). 17 tests updated across gateway_test.go,
      handlers_test.go, charges_test.go, charges_error_visibility_test.go, history_test.go,
      supercharger_test.go — see worker report for the full list + rationale per test.
- [x] T17.4 `make i18n-guard` passes clean (re-run after T16's codegen, in case codegen touched
      any `.templ` source — it shouldn't, but confirm).
      Re-run after T19's catalog key + call-site fix: passes clean.
- [x] T17.5 `make check` passes clean end-to-end (`build vet ui-guard i18n-guard test`).
      Full `make check` run (T19.5) — build, vet, ui-guard, i18n-guard, test all pass.
- [x] T17.6 `openspec validate RM24-gateway-translate-all-pages --strict` passes and every
      `tasks.md` checkbox above is checked, matching `progress.json`.
      `openspec validate --strict` passes; all tasks.md checkboxes (T1–T19) now checked.

## T18. Inventory-gap closure: page titles + charges-list table headers — depends on T1, T5

> **Added by the leader at the wave-2 boundary** (append-only; nothing above was edited). Two
> classes of user-facing English were missed by this tier's original inventory and surfaced by
> workers who blocked rather than inventing keys. The catalogue keys already exist (172 total).

- [x] T18.1 `templates/fragments/charges_list.templ`: the `ui.Table{Headers: []string{...}}` slice
      still holds 8 hardcoded English column headings. Wire each to its
      `i18n.KeyChargesListHeader*` key. **This completes T5.2** — tick T5.2 and flip T5 out of
      `blocked` once done.
- [x] T18.2 Page `<title>` strings passed to `layouts.Base`/`layouts.BaseAuth` in all five pages.
      `home.templ` → bare `i18n.T(ctx, i18n.KeyBrandMagus)`. The other four →
      `fmt.Sprintf(i18n.T(ctx, i18n.KeyBrandPageTitle), i18n.T(ctx, <page key>))` where the page
      key is `KeyLoginSignIn` (login), `KeyNavDashboard` (dashboard), `KeyChargesPageTitle`
      (charges), `KeyNavSuperchargerStats` (supercharger stats). One interpolated brand key, not
      five literal title keys — the wordmark/separator is one editorial decision.
      Note `templates/pages/supercharger_stats.templ` IS in scope here despite the proposal's
      inventory recording it as needing zero changes.
- [x] T18.3 `go build ./internal/gateway/...` compiles (run AFTER T16's codegen, since these are
      `.templ` edits).

## T19. Inventory-gap closure: dashboard charge limit — depends on T1, T14

> **Added by the leader at the wave-3 boundary** (append-only; nothing above was edited). The
> `make i18n-guard` target built in T14 found one genuine hardcoded-English string that this
> tier's original inventory missed, and the wave-3 worker correctly blocked rather than either
> suppressing it with `i18n:allow` (it is real app copy, not an exemption) or minting a catalog
> key outside catalog.go's single-writer discipline (design.md D4). This group carries the key
> addition and unblocks T14.3 + T17.4–T17.6.

- [x] T19.1 Add ONE key to `internal/gateway/i18n/catalog.go`, in both the `Key` const block and
      the catalog map, placed with the other `dashboard.*` keys and following the file's existing
      ordering and same-line `{ES: …, EN: …}` style:
      `KeyDashboardChargeLimit Key = "dashboard.charge_limit"` →
      `{ES: "Límite %d%%", EN: "Limit %d%%"}`. The `%d%%` verb is required in BOTH languages —
      it is an interpolated count, mirroring `KeyDashboardStatusSoftwareVersion`'s `%s`.
- [x] T19.2 Wire the call site: `internal/gateway/handlers/handlers.go:423` becomes
      `vm.ChargeLimit = fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardChargeLimit), snap.ChargeLimitSocPct)`
      inside `mapDashboardSnapshot` (ctx is already a parameter — do not add plumbing).
- [x] T19.3 `go test ./internal/gateway/...` green, including
      `TestCatalog_AllKeysHaveBothLanguages`. Any pre-existing test asserting the English
      `"Limit …"` literal is updated to the resolved-language string, mirroring the T17.3/T6.4
      precedent.
      No test update needed: `TestDashboardFor_EnrichedBento` runs under an English `ctx`
      (`i18n.WithLang(ctx, account.LanguageEN)`) and the EN catalog value `"Limit %d%%"` formats
      to the identical `"Limit 80%"` the pre-existing assertion already expected — the test's own
      comment (handlers_test.go:617-618) anticipated exactly this ("ChargeLimit which are
      identical or not yet translated").
- [x] T19.4 Complete **T14.3**: re-run `make i18n-guard` against the swept tree — it MUST now pass
      clean. Tick T14.3 above and flip T14 out of `blocked`.
- [x] T19.5 Complete **T17.4–T17.6**: re-run `make i18n-guard` after codegen, then `make check`
      end-to-end (`build vet ui-guard i18n-guard test`), then
      `openspec validate RM24-gateway-translate-all-pages --strict`. Tick T17.4, T17.5, T17.6 and
      flip T17 out of `blocked`. If T19.1's key changed any `.templ`-adjacent output, re-run
      `make templ && make css` (T16) and report whether `static/app.css` changed.
      No `.templ` file was touched by T19 (only `catalog.go` and `handlers.go`), so no re-run of
      `make templ`/`make css` was needed; `git status` on `internal/gateway/static/` and
      `internal/gateway/templates/` shows no changes — `static/app.css` did NOT change.
