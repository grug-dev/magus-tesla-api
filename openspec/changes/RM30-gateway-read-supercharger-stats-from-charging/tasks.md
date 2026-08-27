# Tasks — RM30-gateway-read-supercharger-stats-from-charging

Legend — `[ ]` pending · `[x]` done. Keep checkboxes live as work proceeds
(`openspec/config.yaml` §rules.tasks). Every task below is `[module: gateway worker]` unless
marked **LEADER-OWNED** (outside `internal/gateway/`, requires an explicit grant at Apply time)
or **LEADER INTEGRATION** (not a worker sub-task at all — see the callout at the end).

---

## Wave 1 — independent files, no shared state (all four fully parallel_ok)

- [x] **1.1** **[module: gateway worker]** `internal/gateway/gateway.go` — change
  `Deps.SuperchargerReader`'s type from `telemetry.SuperchargerReader` to
  `charging.SessionReader` (design.md D2). Update the field's doc comment (it currently says
  "the telemetry Supercharger-sessions read port... Injected from cmd/web via
  `telemetry.NewSuperchargerReader(pool)`" — update the port/constructor names). The
  `handlers.Deps{... SuperchargerReader: d.SuperchargerReader ...}` wiring line itself needs no
  edit (same field name, type flows through). Do NOT remove the `internal/telemetry` import —
  `TelemetryReader telemetry.Reader` still needs it.
  `depends_on`: — · `parallel_ok`: with 1.2, 1.3, 1.4

- [x] **1.2** **[module: gateway worker]** `internal/gateway/handlers/handlers.go` — same type
  change as 1.1, applied to `Deps.SuperchargerReader` AND `Handler.superchargerReader`
  (design.md D2). Update both doc comments. `New()`'s
  `superchargerReader: d.SuperchargerReader` wiring line needs no edit. Do NOT remove the
  `internal/telemetry` import — `TelemetryReader`/`Snapshot` handling elsewhere in this file
  still needs it.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.3, 1.4

- [x] **1.3** **[module: gateway worker]** `internal/gateway/templates/fragments/supercharger_vm.go`
  — `SuperchargerStatsView`: remove the `Months int` field; change `Presets` from `[]int` to
  `[]RangePreset` (the type already exists in this same package —
  `internal/gateway/templates/fragments/history_vm.go` — no import needed, design.md Context
  fact 3). `SuperchargerRowVM`: remove `CountryCode` and `BillingType` (design.md D4). Update
  every field's doc comment that references the removed/changed fields.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2, 1.4

- [x] **1.4** **[module: gateway worker]** `internal/gateway/i18n/catalog.go` — remove the
  `KeySuperchargerCountry` and `KeySuperchargerBillingType` `Key` constants AND their `ES`/`EN`
  catalogue map entries (design.md D4). Do NOT touch `KeySuperchargerMonthsPreset` — it is
  reused unchanged (design.md D4, last sentence).
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2, 1.3

---

## Wave 2 — core rewrite (depends on wave 1's shapes)

- [x] **2.1** **[module: gateway worker]** `internal/gateway/handlers/supercharger.go` — full
  rewrite per design.md D1/D3/D5/D6/D9/D10:
  - Remove `internal/telemetry` import; add `internal/charging`.
  - Remove `clampSuperchargerMonths`, `defaultSuperchargerMonths`, `superchargerReadLimit`.
  - Add `superchargerRangeDefaultMonths = 6`, `superchargerRangeMaxDays = 400`,
    `parseSuperchargerRange(c, today) (start, end time.Time, ok bool)` (design.md D1 — exact
    body given there), `monthsBackFrom(end time.Time, n int) time.Time` (design.md D5 — exact
    body given there).
  - `parseSuperchargerRange` MUST reject `end.After(today)` (design.md D9b), between the
    `end.Before(start)` check and the 400-day cap check. `end == today` is ACCEPTED — the
    boundary is strictly after, unlike `parseHistoryRange`'s browser-yesterday cap. Do not omit
    this rule: `internal/gateway/AGENTS.md` §"HTTP date-filter convention" states the 400 rules
    as a closed set, and without it a far-future `end` inside the 400-day cap renders empty
    future month bars (design.md D9b).
  - Keep `startOfMonth`, `monthsBetween` (still used by the rewritten chart builder,
    design.md D10) — do not delete or rename them.
  - Rewrite `SuperchargerStatsPage`/`SuperchargerStatsFragment` to resolve `today :=
    startOfDay(time.Now().UTC())` (design.md D9 — plain UTC, NOT `browserToday(c)`), call a
    restructured `superchargerStatsViewFor` that returns `(fragments.SuperchargerStatsView,
    int)` (view + HTTP status), and render via `render`/`renderError` (Page) or
    `renderFragment`/`renderFragmentError` (Fragment) per the returned status — mirroring
    `DashboardHistoryFragment`'s parse→400-empty-no-selector→resolve-vehicle→build shape
    (`internal/gateway/handlers/history.go`), NOT the old single-branch shape.
  - Rewrite `buildSuperchargerStatsView(ctx, uid, teslaID, start, end, today)
    fragments.SuperchargerStatsView`: one call to
    `h.superchargerReader.ListSessionsByVehicleBetween(ctx, uid, teslaID, start, end)`, reverse
    the returned slice once (design.md D3 — exact snippet given there) before building
    tiles/chart/rows, no more in-memory `since` filter.
  - Change `buildSuperchargerTiles`/`buildSuperchargerRows` parameter type from
    `[]telemetry.SuperchargerSession` to `[]charging.Session` — field names are identical
    (`EnergyKWh`, `TotalCost`, `Currency`, `SiteLocationName`, `ChargeStartDateTime`), so the
    body logic is otherwise unchanged EXCEPT `buildSuperchargerRows` no longer sets
    `CountryCode`/`BillingType` on the row (those fields no longer exist on either type,
    design.md D4).
  - Rewrite `buildSuperchargerChart` to the `(sessions []charging.Session, start, end
    time.Time)` signature and window-derived bucket-count body given verbatim in design.md D10.
  - Add `buildSuperchargerPresets(ctx context.Context, start, end, today time.Time)
    []fragments.RangePreset` mirroring `buildHistoryPresets` (`history.go`): for each `n` in
    `superchargerMonthPresets` (`{3, 6, 12}`, unchanged), `pEnd = today`, `pStart =
    monthsBackFrom(today, n)`, `Label = fmt.Sprintf(i18n.T(ctx,
    i18n.KeySuperchargerMonthsPreset), n)`, `Active = start.Equal(pStart) &&
    end.Equal(pEnd)`.
  `depends_on`: 1.2, 1.3 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: gateway worker]**
  `internal/gateway/templates/fragments/supercharger_stats.templ` — rewrite per design.md D1/D4:
  - `superchargerMonthsSelector(active int, presets []int)` → `superchargerMonthsSelector(presets
    []RangePreset)`, mirroring `historyDaysSelector` exactly
    (`internal/gateway/templates/fragments/history.templ`): `hx-get` becomes
    `fmt.Sprintf("/ui/supercharger-stats?start=%s&end=%s", p.StartStr, p.EndStr)`, `hx-target`
    stays `#supercharger-stats-content`, active preset via `p.Active` (Variant "primary" vs
    "ghost"), button label is `p.Label` (already formatted by the handler — no
    `fmt.Sprintf(i18n.T(...), p)` call in the template anymore, that formatting moved to
    `buildSuperchargerPresets`, task 2.1).
  - `superchargerTable` — remove the `KeySuperchargerCountry`/`KeySuperchargerBillingType`
    header cells and the `s.CountryCode`/`s.BillingType` `<td>` cells (design.md D4).
  - `SuperchargerStatsContent(v SuperchargerStatsView)` — change
    `@superchargerMonthsSelector(v.Months, v.Presets)` to a conditional matching
    `DashboardHistoryContent`'s pattern: `if v.Presets != nil { @superchargerMonthsSelector(v.Presets) }`
    (design.md D1 — no selector on a malformed/400 request).
  `depends_on`: 1.3, 1.4 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: gateway worker — regeneration]** Run `make templ` (regenerates
  `internal/gateway/templates/fragments/supercharger_stats_templ.go`) after 2.2 lands. Then run
  `go build ./internal/...` to confirm 2.1/2.2/1.1–1.4 compile together (full-repo
  `go build ./...` will still fail until the LEADER INTEGRATION step below updates
  `cmd/web/main.go` — that failure is expected and NOT this task's responsibility to fix).
  `depends_on`: 2.1, 2.2 · `parallel_ok`: —

---

## Wave 3 — tests

- [ ] **3.1** **[module: gateway worker]** `internal/gateway/handlers/supercharger_test.go` —
  full rewrite:
  - Rename `fakeSuperchargerReader` → `fakeSessionReader`, implementing
    `charging.SessionReader`'s single method `ListSessionsByVehicleBetween(ctx, accountID,
    teslaID, from, to) ([]charging.Session, error)`. It MUST filter to `TeslaID != nil &&
    *TeslaID == teslaID` before returning (design.md D8 — carries forward the real port's
    `NULL`-exclusion contract; do not drop this filtering when rewriting the fake).
  - Implement design.md's Test Contract T1–T14 (renumber/group as this file's own test
    functions; T12 is a compile-time note, not a runtime test — skip it as a `Test...` function,
    but do NOT add `CountryCode`/`BillingType` back to any fixture or assertion).
  - T13/T14 (design.md D9b) are the future-date boundary pair and must both exist: `end` ==
    UTC today accepted (200), `end` == UTC today + 1 day rejected (400, empty state, no
    selector). Keep T14's window ~180 days wide so the 400-day cap cannot be what rejects it —
    otherwise the test passes for the wrong reason.
  - Update every existing `TestBuildSupercharger{Tiles,Rows}_*` test's fixture element type from
    `telemetry.SuperchargerSession` to `charging.Session` — the field values/assertions
    themselves are UNCHANGED (design.md "What must NOT change").
  - Update `TestBuildSuperchargerChart_*` tests for the new `(sessions, start, end)` signature.
  - Update the HTTP-level tests (`TestSuperchargerStatsPage_*`, `TestSuperchargerStatsFragment_*`)
    for the new query contract — replace every `?months=N` URL with an equivalent `?start=&end=`
    URL computed the same way the handler computes it (mirror `history_test.go`'s
    `parseRange`-style helper: build the URL from `monthsBackFrom`/`today`, do not hardcode a
    date that will go stale).
  - Remove `internal/telemetry` from this file's imports; add `internal/charging`.
  - Grep sweep before calling this done: confirm no remaining reference in this file (or
    anywhere under `internal/gateway/`) to `telemetry.SuperchargerSession`,
    `SuperchargerSessionsByVehicle`, `clampSuperchargerMonths`, `defaultSuperchargerMonths`,
    `superchargerReadLimit`, `KeySuperchargerCountry`, or `KeySuperchargerBillingType` — a
    narrow grep on just the changed symbol names has missed refs before on this project
    (`internal/gateway` is the search root; do not scope the grep to one file).
  `depends_on`: 2.3 · `parallel_ok`: —

---

## Wave 4 — docs

- [ ] **4.1** **[module: gateway worker]** `internal/gateway/AGENTS.md`:
  - §"Public interface" — rewrite the `Deps.SuperchargerReader` bullet: type is now
    `charging.SessionReader`; it is called by `SuperchargerStatsPage`/`SuperchargerStatsFragment`
    (via `superchargerStatsViewFor`/`buildSuperchargerStatsView`) with ONE
    `ListSessionsByVehicleBetween` read per render, bounded by the requested `?start=&end=`
    window (not a row limit anymore); "NEVER import `internal/telemetry/db`" becomes "NEVER
    import `internal/charging/db` (`chargingdb`) for this path — all access through this
    interface only." Cite this change's name in place of `gateway-add-supercharger-stats`.
  - §"HTTP date-filter convention" — change the cap sentence ("or a window wider than **90
    days** (hard cap against unbounded range scans — see `historyRangeMaxDays`)") to state the
    cap is **per-endpoint, set by each table's row density**: history's dense per-day snapshots
    get 90 days (`historyRangeMaxDays`), the sparser Supercharger Stats endpoint gets 400 days
    (`superchargerRangeMaxDays`) — cite `GET /ui/supercharger-stats`
    (`RM30-gateway-read-supercharger-stats-from-charging`) as the second reference
    implementation alongside `GET /ui/dashboard/history`. **Do NOT reword or remove the bold
    "Every future date-filtered gateway endpoint follows the same contract" bullet — it is
    load-bearing** (keep its wording; only the specific cap sentence above changes, and this
    bullet's "a new endpoint... reuses the `parseHistoryRange` pattern" clause may gain a short
    parenthetical noting `parseSuperchargerRange` as a second worked example of the same
    pattern with its own constants, per design.md D1).
  `depends_on`: 2.3

- [ ] **4.2** **LEADER-OWNED / explicitly-granted** (outside `internal/gateway/` — the leader
  grants this path at Apply time) — `ai/architecture.md` §"Concrete patterns already in use":
  add one new numbered pattern entry pointing at
  `internal/gateway/AGENTS.md` §"HTTP date-filter convention" as the full handler contract
  (parse helper, default/cap-per-endpoint, 400-on-malformed, no-selector-on-400), so an agent
  reading the project-wide conventions doc discovers the pattern exists without having to already
  know to look in the gateway module's own `AGENTS.md`. Keep it a pointer, not a restatement —
  the gateway `AGENTS.md` section (task 4.1) remains the single source of truth for the
  contract's specifics.
  `depends_on`: 4.1

- [ ] **4.3** **LEADER-OWNED / explicitly-granted** (outside `internal/gateway/`) — root
  `README.md`, in the "## Web UI (gateway)" section: add a short subsection documenting the
  `?start=&end=` HTTP date-filter convention and its per-endpoint caps (history 90 days,
  Supercharger Stats 400 days), per roadmap Decision 7 (owner request). Point to
  `internal/gateway/AGENTS.md` §"HTTP date-filter convention" for the full contract rather than
  restating it.
  `depends_on`: 4.1

---

## LEADER INTEGRATION (not a worker sub-task — perform after wave 2, before running repo-wide `go build ./...`)

`cmd/web/main.go` sits outside `internal/`, so per the roadmap
(`openspec/roadmaps/RM30-supercharger-stats-read-from-charging.md`, tiers table footnote) it
"belongs to no tier" and is not dispatched to the gateway worker. It is, however, REQUIRED for
the repo to build once task 1.1/1.2's `Deps.SuperchargerReader` type change lands — until this
edit is made, `go build ./...` fails at `cmd/web/main.go`'s
`SuperchargerReader: telemetry.NewSuperchargerReader(pool)` line (a type mismatch against the
new `charging.SessionReader` field). The leader (or the user) must:

- Change `SuperchargerReader: telemetry.NewSuperchargerReader(pool)` (line ~59) to
  `SuperchargerReader: charging.NewSessionReader(pool)`.
- Leave the other two `telemetry.NewSuperchargerReader(pool)` calls UNTOUCHED — they are
  `analytics.NewReader(...)`/`analytics.NewRecalculator(...)`'s own constructor arguments
  (lines ~69, ~83), unrelated to the gateway's `Deps` and out of this change's scope (roadmap
  Decision 2).
- No new import is needed — `internal/charging` is already imported in this file.

`depends_on`: 1.1, 1.2 (either order relative to waves 3/4 is fine, but it must land before any
repo-wide `go build ./...` / `go vet ./...` is expected to pass clean).

---

## Not in this change — do not do these

- **Any change to `internal/charging`.** Tier 1 already shipped everything this tier consumes.
- **Any change to `internal/telemetry`, the dashboard tiles, or the dashboard history charts.**
  Roadmap Decision 2 keeps those on `telemetry.Reader`; that is tracked separately (roadmap
  "Future work" / backlog item 17).
- **Any database migration, index, column, or constraint.** design.md D7 / roadmap Decision 1.
- **Reproducing `parseHistoryRange`'s `browser_tz`/`browserToday(c)` machinery on this
  endpoint.** design.md D9a — `today` here is plain `startOfDay(time.Now().UTC())`.
  NOTE: the no-future-date REJECTION is in scope and REQUIRED (design.md D9b, task 2.1);
  only the browser-local timezone frame is cut.
- **Re-adding `CountryCode`/`BillingType` to `SuperchargerRowVM`, the template, or the
  catalogue.** design.md D4 — `charging.Session` has neither field; this is intended.
