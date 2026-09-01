# Design — RM38-gateway-read-dashboard-from-metrics

## Context

Tier 1 (`RM38-analytics-add-vehicle-status-columns`, archived) added
`analytics.Reader.LatestMetricsByAccount(ctx, accountID) ([]VehicleStatus, error)` and the
`analytics.VehicleStatus` domain type:

```go
type VehicleStatus struct {
    TeslaID           int64
    BatteryLevelPct   int
    BatteryRangeKm    float64
    OdometerKm        float64
    InsideTempC       *float64
    OutsideTempC      *float64
    Locked            *bool
    SentryMode        *bool
    CarVersion        *string
    ChargingState     *string
    ChargeLimitSocPct *int
    CapturedAt        *time.Time
}
```

"Latest" means the row with the greatest `metric_date` per `(account_id, tesla_id)` — a
`DISTINCT ON` read, same contract as `telemetry.Reader.LatestSnapshotsByAccount` (empty account
→ empty non-nil slice, nil error; slice order unspecified).

This tier repoints the gateway's four `LatestSnapshotsByAccount` call sites (roadmap D3,
verified there against the actual code) onto this port, and applies the dashboard's Locked/Sentry
badge UI change (roadmap D4/D5). `h.analyticsReader analytics.Reader` already exists on
`Handler`/`Deps` — **confirmed** by reading `internal/gateway/handlers/handlers.go`'s `Deps`
struct (`AnalyticsReader analytics.Reader`) and `Handler` struct (`analyticsReader
analytics.Reader`), both already wired from `cmd/web` via `analytics.NewReader(...)` since
`RM28-gateway-add-consumed-graph`. **No `Deps` change, no constructor change, no `cmd/web`
change is needed for this tier.**

`h.telemetryReader telemetry.Reader` stays on `Handler` — `history.go:321`'s
`SnapshotsByVehicleBetween` is its one remaining caller after this tier. Do not remove the field.

The four call sites, confirmed by reading the code (matching roadmap D3's own verification):

| Site | Function | File:line (as of dispatch) | Uses from `VehicleStatus` |
|---|---|---|---|
| 1 | `dashboardFor` → `mapDashboardSnapshot` | `handlers.go:446` (site), `:471` (mapper) | all twelve fields |
| 2 | `vehiclesFor` → `mapVehicles` | `handlers.go:244` | `BatteryLevelPct`, `BatteryRangeKm`, `ChargingState`, `OdometerKm`, `InsideTempC`, `OutsideTempC`, `Locked`, `SentryMode`, `CapturedAt` |
| 3 | `navHeaderFor` | `handlers.go:712` | `BatteryLevelPct`, `CapturedAt` |
| 4 | `buildChargesPage` (battery suggestion) | `charges.go:671` | `BatteryLevelPct` only |

Site 2 (`vehiclesFor`/`mapVehicles`/`fragments.Vehicle`/`VehiclesList`) has **no route** —
`gateway.go` never mounts `/ui/vehicles`, confirmed by grep. `vehiclesFor`'s only production
effect is inside `Handler.Dashboard` (`_ = h.vehiclesFor(c.Request.Context(), uid)`), called
solely for its one-time Tesla-seed side effect; the `fragments.VehiclesData` it returns is
discarded. This tier still adapts it (D3 below) rather than leaving it broken or deleting it —
see D3's rationale.

## Goals / Non-Goals

**Goals:**
- Switch all four call sites from `telemetry.Reader.LatestSnapshotsByAccount` to
  `analytics.Reader.LatestMetricsByAccount`, with an explicit, tested nil-handling rule for
  every field that is newly a pointer (D2–D5 below).
- Apply roadmap D4/D5: remove the dashboard's Status stat tile, add Locked/Sentry `ui.Badge`
  pills to the Vehicle Status card header, 3-column stat grid.
- Decide and justify the fate of `mergeSnapshots` (D6).
- Update `internal/gateway/AGENTS.md` and `kkpa/context/use-case/gateway/read-dashboard-bento.md`
  in this same change (D10/D11).
- Author the Test Contract (fixtures + expected values) before implementation (§Test Contract).

**Non-Goals (explicitly deferred, do not implement here):**
- Adding `metric_date` to the analytics port, or retuning `stalenessThreshold`/
  `connectedFreshnessWindow` (roadmap D8 — owner's call, out of scope for this tier).
- Wiring `/ui/vehicles` to a live route, or any other change to its reachability.
- Any database object — this tier touches no migration, table, column, index, or constraint.
- Backfilling historical `vehicle_metrics` rows (tier 1 D2, `backlog.md` §22).

## Decisions

Decisions below are numbered fresh for this design document (D1…D11), independent of the
roadmap's own D1–D9 numbering — cross-referenced explicitly wherever one carries a roadmap
decision forward.

### D1 — No new dependency (confirms dispatch Fact #3)

`Deps.AnalyticsReader` / `Handler.analyticsReader` already exist and are already wired from
`cmd/web`. This tier adds no field to `Deps`, no field to `Handler`, no change to `New()`'s
signature, and no change to `cmd/web`. Every task in this change is confined to
`internal/gateway/handlers/*.go`, `internal/gateway/templates/**/*.templ` (+ generated
`*_templ.go`), `internal/gateway/AGENTS.md`, and `kkpa/context/`.

### D2 — Site 1 (`dashboardFor`/`mapDashboardSnapshot`): field-by-field nil handling

`dashboardFor` calls `h.analyticsReader.LatestMetricsByAccount(ctx, uid)` instead of
`h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)`; `mergeVehicleStatuses(statuses)[primary.
TeslaID]` (D6) replaces `mergeSnapshots(snaps)[primary.TeslaID]`. `mapDashboardSnapshot`'s
signature changes from `(ctx, *fragments.DashboardData, telemetry.Snapshot, time.Time)` to
`(ctx, *fragments.DashboardData, analytics.VehicleStatus, time.Time)`. Per-field handling,
applying the dispatch's "nil means omit, never fabricate" rule field by field:

| Field | Old (non-pointer) | New (`VehicleStatus`) | Handling |
|---|---|---|---|
| `BatteryLevelPct`, `BatteryRangeKm`, `OdometerKm` | plain | plain (unchanged) | verbatim, no change |
| `InsideTempC`, `OutsideTempC` | plain | `*float64` | nil → `"—"` (existing placeholder, reused — see below); else `fmt.Sprintf("%.0f °C", *v)`, same precision as today |
| `CarVersion` | plain `string`, checked `!= ""` | `*string` | nil **or** `*v == ""` → `vm.SoftwareVer` stays `""` (line omitted, same as today's empty-string case) |
| `ChargeLimitSocPct` | plain `int`, checked `> 0` | `*int` | nil **or** `*v <= 0` → `vm.ChargeLimit` stays `""` (line omitted) |
| `ChargingState` | plain `string` | `*string` | `dashStatus`'s new signature takes `*string`; nil collapses to "Parked", exactly like today's empty-string case (D-below) |
| `CapturedAt` | plain `time.Time` | `*time.Time` | nil → `vm.LastUpdated`/`vm.IsStale` stay at zero value (`""`/`false`) — **zero template change**, roadmap D9 |
| `Locked` | n/a (new field) | `*bool` | stored as `vm.Locked *bool` verbatim; badge logic in D4 |
| `SentryMode` | n/a (new field) | `*bool` | stored as `vm.SentryMode *bool` verbatim; badge logic in D4 |

**Why `"—"` for nil temperatures reuses the existing placeholder with zero template change:**
`dashboard.templ` already calls `dashStat(d.HasSnapshot, d.InsideTemp)`, which returns `"—"`
only when `HasSnapshot` is false. Once a `vehicle_metrics` row exists, `HasSnapshot` is `true`
even if that row's `inside_temp_c` is NULL (pre-migration row, D2 of tier 1). So
`mapDashboardSnapshot` itself must decide the `"—"` for a nil temperature — it sets
`vm.InsideTemp = "—"` directly (a tiny local helper, not exported, not shared with site 2's
own formatting — see "Rejected" below) — and `dashStat(true, "—")` then renders that string
verbatim. The template needs no new branch.

**`dashStatus`'s new signature:**

```go
// dashStatus maps a snapshot's ChargingState to the dashboard's two-state status
// vocabulary. nil (not yet recomputed since the RM38 migration) collapses to
// "Parked", identical to the pre-migration behavior for an empty string — there is
// no third visual state for "unknown charging state" on the dashboard subtitle.
func dashStatus(ctx context.Context, chargingState *string) string {
    if chargingState != nil && *chargingState == "Charging" {
        return i18n.T(ctx, i18n.KeyDashboardStatusCharging)
    }
    return i18n.T(ctx, i18n.KeyDashboardStatusParked)
}
```

Call site: `vm.StatusLabel = dashStatus(ctx, vs.ChargingState)`.

**Rejected — one shared `formatTempOrDash(*float64) string` helper reused by both site 1 and
site 2 (vehiclesFor)**: site 1's existing precision is `%.0f °C` (whole degrees, dashboard hero);
site 2's is `%.1f °C` (one decimal, vehicle-list card) — two different, already-established
display precisions for two different regions. A shared helper would need a precision parameter,
adding a layer to resolve a one-line `if v == nil` check that costs nothing to duplicate. Per
this project's AI-efficiency principle ("do not over-abstract... never hide stable, self-
describing things behind a lookup"), two three-line unexported functions, each next to its own
call site, is the right amount of abstraction — not zero (the nil check is real logic worth
naming), not a shared cross-cutting helper (the two sites' formats are not actually the same
computation, only superficially similar).

### D3 — Site 2 (`vehiclesFor`/`mapVehicles`/`fragments.Vehicle`): adapt, don't delete

**Decision: adapt this dead-route code path to the new port, do not delete it or leave it
uncompiled.** Rationale:

1. Roadmap D3 is explicit — "all four call sites move," not "the three reachable ones." The
   roadmap's own field-coverage verification counted this site in its twelve-field union.
2. `vehiclesFor` is still executed on every `GET /dashboard` render (for its one-time Tesla-seed
   side effect) even though its return value is discarded — it must keep compiling and behaving
   correctly, because a panic or a data-shape bug inside it would break the dashboard, the one
   route that IS live.
3. Deleting `VehiclesList`/`vehicles.templ`/`fragments.Vehicle` is a larger, unrelated cleanup
   (removing a template, a fragment type, and however many tests reference it) that roadmap D3
   never asked for and that changes this tier's blast radius for no requested benefit. That
   cleanup, if wanted, is its own future change.

`mapVehicles`'s signature changes from `(vs []account.Vehicle, snapMap map[int64]telemetry.
Snapshot) []fragments.Vehicle` to `(vs []account.Vehicle, statusMap map[int64]analytics.
VehicleStatus) []fragments.Vehicle`. `fragments.Vehicle.Locked` changes from `bool` to `*bool`
— it must, because the source (`VehicleStatus.Locked`) is now `*bool`, and collapsing nil to
`false` would fabricate a value the dispatch's own rule forbids. `vehicles.templ`'s `Locked`
branch is rewritten to the same three-way shape its `SentryMode` branch already uses one block
below it, reusing existing catalogue keys (no new key — `KeyVehiclesNotReported` already exists
and reads correctly for either "not reported" or "locked state unknown"):

```templ
<li>
    if v.Locked == nil {
        { i18n.T(ctx, i18n.KeyVehiclesNotReported) }
    } else if *v.Locked {
        { i18n.T(ctx, i18n.KeyVehiclesLocked) }
    } else {
        { i18n.T(ctx, i18n.KeyVehiclesUnlocked) }
    }
</li>
```

`ChargingState` (`*string` now) maps to `fv.ChargingState`: nil → `""` (same rendering as
today's already-possible empty string — no template change). `InsideTemp`/`OutsideTemp` get
their own local nil-to-`"—"` mapping at `%.1f °C` precision (this site's existing precision,
per D2's "no shared helper" rejection). `LastUpdated`/`IsStale`: nil `CapturedAt` → both stay at
zero value, exactly mirroring D2's site-1 treatment — `vehicles.templ`'s rendering of an empty
`LastUpdated` string degrades to "Last updated: " with nothing after, which is an acceptable
degradation for an unrouted page (no polish work is owed to a page nothing can navigate to).

### D4 — Site 1's badges: `dashLockedBadge`/`dashSentryBadge`, mirroring existing `dash*` helpers

Two new unexported functions in `templates/pages/dashboard.go`, alongside the existing
`dashStat`/`dashSubtitle`/`dashChargeLimit`/`dashBatteryColorClass` (same file, same "Go computes,
template only reads" pattern):

```go
// dashLockedBadge returns the ui.Badge text/kind for the locked/unlocked pill and
// whether it should render at all. nil -> show=false, no badge (roadmap D5) --
// the vehicle_metrics row predates the RM38 migration or has not been
// recomputed since.
func dashLockedBadge(ctx context.Context, locked *bool) (text, kind string, show bool) {
    if locked == nil {
        return "", "", false
    }
    if *locked {
        return i18n.T(ctx, i18n.KeyVehiclesLocked), "success", true
    }
    return i18n.T(ctx, i18n.KeyVehiclesUnlocked), "error", true
}

// dashSentryBadge mirrors dashLockedBadge for the sentry-mode pill (roadmap D5).
// The rendered text pairs the existing "Sentry:" label with On/Off, reusing three
// catalogue keys with no new key (KeyVehiclesSentryLabel + KeyVehiclesOn/Off).
func dashSentryBadge(ctx context.Context, sentry *bool) (text, kind string, show bool) {
    if sentry == nil {
        return "", "", false
    }
    state := i18n.T(ctx, i18n.KeyVehiclesOff)
    kind = "ghost"
    if *sentry {
        state = i18n.T(ctx, i18n.KeyVehiclesOn)
        kind = "warning"
    }
    return i18n.T(ctx, i18n.KeyVehiclesSentryLabel) + " " + state, kind, true
}
```

`dashboard.templ`'s header row (currently subtitle + conditional `[Stale]` badge) becomes:

```templ
<div class="flex items-center justify-between">
    <p class="text-sm text-base-content/60 uppercase tracking-wider">
        { dashSubtitle(ctx, d) }
    </p>
    <div class="flex items-center gap-2">
        if d.HasSnapshot && d.IsStale {
            @ui.Badge(ui.BadgeProps{Kind: "warning", Text: i18n.T(ctx, i18n.KeyDashboardStaleBadge)})
        }
        if lockedText, lockedKind, showLocked := dashLockedBadge(ctx, d.Locked); showLocked {
            @ui.Badge(ui.BadgeProps{Kind: lockedKind, Text: lockedText})
        }
        if sentryText, sentryKind, showSentry := dashSentryBadge(ctx, d.SentryMode); showSentry {
            @ui.Badge(ui.BadgeProps{Kind: sentryKind, Text: sentryText})
        }
    </div>
</div>
```

(Templ supports an `if a, b, c := f(); c { ... }` init-statement form identically to Go's own
`if`-with-init — verify against Context7 `/a-h/templ` at implementation time per this project's
Context7 policy; if the exact three-return-value init-statement shape is not supported by the
pinned templ version, the fallback is to call the two helpers once at the top of the component
body via a `{{ ... }}` Go-code block and bind three local `templ.Component`-scoped variables,
still zero business logic added — only the syntax differs, not the decision.)

No new `ui/` component and no `ui.Badge` prop changes — `Kind`/`Text` already cover every state
this needs (`success`/`error`/`warning`/`ghost`, all pre-existing values used elsewhere in this
same file for the `[Stale]` badge and in `nav_header.templ`).

**Rejected — a single `dashStatusBadges(ctx, d) []ui.BadgeProps` returning a slice the template
ranges over**: would hide three independent, differently-shaped conditions (stale / locked /
sentry) behind one opaque slice-building function, forcing a future reader to open the Go file
to know how many badges can appear or in what order — worse for both a human and an agent than
three named, individually-guarded blocks the template already shows in order. This is the same
"don't over-abstract a small, stable set of cases" principle D2 invoked for temperature
formatting.

### D5 — Stat grid: 2×2 → one row of three

```templ
<div class="grid grid-cols-3 gap-3">
    @ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardOdometer), Value: dashStat(d.HasSnapshot, d.Odometer)})
    @ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardInterior), Value: dashStat(d.HasSnapshot, d.InsideTemp)})
    @ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardExterior), Value: dashStat(d.HasSnapshot, d.OutsideTemp)})
</div>
```

(`grid-cols-2` → `grid-cols-3`; the fourth `ui.StatTile` call, for `KeyDashboardStatus`, is
deleted.) `i18n.KeyDashboardStatus` ("dashboard.status") is **left in the catalogue, unused** —
`TestCatalog_AllKeysHaveBothLanguages` only requires every *known* key to carry both languages,
it does not require every key to be referenced; `make i18n-guard` only flags a hardcoded string
bypassing `i18n.T`, not an orphaned key. Deleting the key is a separate, optional cleanup with no
functional benefit and a small risk of breaking an existing catalogue test that enumerates keys
by name — out of scope for this tier.

### D6 — `mergeSnapshots` → `mergeVehicleStatuses`: renamed and retyped, not removed

**This is the decision the dispatch asked this worker to make.** `mergeSnapshots` is kept as a
function, renamed to `mergeVehicleStatuses`, and retyped:

```go
// mergeVehicleStatuses builds a map from TeslaID to VehicleStatus for O(1) lookup
// per vehicle. A nil or empty slice produces an empty map (no panic on range).
// Renamed from mergeSnapshots (RM38-gateway-read-dashboard-from-metrics): same
// shape, new source type -- LatestMetricsByAccount already returns at most one
// row per TeslaID (design.md D6, tier 1), so, exactly as before with
// LatestSnapshotsByAccount, this function does no de-duplication of its own; it
// only indexes an already-unique slice for lookup by an arbitrary caller-supplied
// TeslaID (the selected/primary vehicle), which the slice's own uniqueness does
// not provide by itself.
func mergeVehicleStatuses(statuses []analytics.VehicleStatus) map[int64]analytics.VehicleStatus {
    m := make(map[int64]analytics.VehicleStatus, len(statuses))
    for _, s := range statuses {
        m[s.TeslaID] = s
    }
    return m
}
```

Used by sites 1 (`dashboardFor`), 2 (`vehiclesFor`), and 3 (`navHeaderFor`) — all three need to
pick one vehicle's row out of the account-wide batch by `TeslaID`. Site 4 (`charges.go`) does
**not** use this helper today (it already does its own linear `for _, s := range snaps { if
s.TeslaID == teslaIDFilter {...} }` scan) and continues not to — only its element type changes
from `telemetry.Snapshot` to `analytics.VehicleStatus`, a one-line retype with no logic change.

**Why keep the function at all, given the port already de-duplicates:** the "de-duplication"
`DISTINCT ON` performs happens at the SQL layer, across `metric_date` for a given `tesla_id` —
it guarantees the slice has at most one entry per `TeslaID`, which is a different guarantee from
"indexed for O(1) lookup by an arbitrary `TeslaID` the caller already knows." Without the map,
each of the three callers would need its own linear scan (exactly what site 4 already does,
tolerated there because it is a single one-off lookup, not because it is the preferred pattern)
— keeping one named, tested helper for the three call sites that share this exact need is more
readable and change-local than inlining three near-identical loops.

**Rejected — delete the helper and inline a linear scan everywhere (matching site 4's shape)**:
would triple the amount of near-identical loop code across `handlers.go` for no benefit; the
existing `mergeSnapshots` helper is proven, already unit-tested (`handlers_test.go`), and this
tier's job is to retype it, not to second-guess its reason for existing.

**Rejected — delete the helper because the DISTINCT ON already de-dupes**: conflates two
different guarantees (uniqueness vs. indexed lookup), as explained above; the dispatch's framing
("given `LatestMetricsByAccount` already returns at most one row per `tesla_id`") is about
uniqueness, not about removing the need to look a specific `TeslaID` up efficiently.

### D7 — Badge state matrix (carries roadmap D5 verbatim, restated for implementation)

| Value | Badge text (key) | `ui.Badge` Kind |
|---|---|---|
| `Locked == true` | `KeyVehiclesLocked` ("Locked"/"Bloqueado") | `success` |
| `Locked == false` | `KeyVehiclesUnlocked` ("Unlocked"/"Desbloqueado") | `error` |
| `Locked == nil` | *no badge rendered* | — |
| `SentryMode == true` | `KeyVehiclesSentryLabel` + `KeyVehiclesOn` ("Sentry: On"/"Centinela: Encendido"... — see catalog for exact ES) | `warning` |
| `SentryMode == false` | `KeyVehiclesSentryLabel` + `KeyVehiclesOff` | `ghost` |
| `SentryMode == nil` | *no badge rendered* | — |

All six catalogue keys (`vehicles.locked`, `vehicles.unlocked`, `vehicles.sentry_label`,
`vehicles.on`, `vehicles.off`, `vehicles.not_reported`) already exist in
`internal/gateway/i18n/catalog.go` with both `ES`/`EN` populated (verified by reading the file)
— **no catalogue edit in this change.**

### D8 — Site 3 (`navHeaderFor`): nil `CapturedAt` forces Asleep, no label (roadmap D9)

```go
statuses, err := h.analyticsReader.LatestMetricsByAccount(ctx, uid)
// ... existing error-degradation branch, unchanged shape ...
statusMap := mergeVehicleStatuses(statuses)
vs, ok := statusMap[primary.TeslaID]
if !ok {
    // unchanged: Awaiting state
}
if vs.CapturedAt == nil {
    // roadmap D9: a NULL CapturedAt can only mean this row predates the RM38
    // migration -- there is no fresher timestamp to prove connectivity with, so
    // this is Asleep, never Connected, and there is nothing to compute a
    // relative "Last seen" label from.
    vm.VehicleName = primary.DisplayName
    vm.Status = fragments.NavStatusAsleep
    return vm
}
now := clock.Now()
if connectedAt(*vs.CapturedAt, now) {
    // unchanged Connected branch, dereferencing *vs.CapturedAt
}
// unchanged Asleep branch, dereferencing *vs.CapturedAt for relativeLastSeen
```

**Zero template change**: `nav_header.templ`'s `if vm.LastSeenLabel != "" { ... }` already omits
the "Last seen" clause when the label is empty (confirmed by reading the template) — leaving
`vm.LastSeenLabel` at its zero value on the nil-`CapturedAt` branch produces exactly the "Asleep,
no last-seen label" rendering roadmap D9 asks for.

`isStale`, `connectedAt`, and `relativeLastSeen`'s signatures are **unchanged**
(`(capturedAt, now time.Time)` / `(capturedAt, now time.Time, ctx) string`) — every call site
now dereferences a `*time.Time` it has already nil-checked, rather than the functions
themselves taking a pointer. This keeps three small, already-tested pure functions untouched.

### D9 — Site 4 (`charges.go`): trivial retype, no nil handling needed

```go
statuses, snapErr := h.analyticsReader.LatestMetricsByAccount(ctx, uid)
if snapErr != nil {
    log.Printf("gateway: charges suggestion analytics reader error for account %s: %v", uid, snapErr)
} else {
    for _, s := range statuses {
        if s.TeslaID == teslaIDFilter {
            suggestion = fmt.Sprintf(i18n.T(ctx, i18n.KeyChargesErrorBatterySuggestion), s.BatteryLevelPct)
            break
        }
    }
}
```

`BatteryLevelPct` is a plain `int` on both `telemetry.Snapshot` and `analytics.VehicleStatus` —
no nil-handling is introduced here at all. Only the variable name/type (`snaps
[]telemetry.Snapshot` → `statuses []analytics.VehicleStatus`) and the log line's wording change.

### D10 — Database design gate does not apply

This tier adds, drops, or alters **no** database object — no migration file is created or
touched. `analytics.vehicle_metrics` and its columns/index already exist, delivered by tier 1
(archived, its own design.md carried the full schema/rationale/index-plan gate). The reviewer
should confirm no `db/migrations/` file appears in this change's diff.

### D11 — Documentation updates (docs-track-change, `CLAUDE.md`)

1. **`internal/gateway/AGENTS.md`**, `Deps.AnalyticsReader` bullet: append
   `LatestMetricsByAccount`, called by `dashboardFor`, `vehiclesFor`, `navHeaderFor`, and
   `buildChargesPage`'s battery-suggestion lookup — "the account's latest per-vehicle status,
   replacing the equivalent `telemetry.Reader.LatestSnapshotsByAccount` calls
   (RM38-gateway-read-dashboard-from-metrics)."
2. **`internal/gateway/AGENTS.md`**, `Deps.TelemetryReader` bullet: update "The gateway calls
   `LatestSnapshotsByAccount` once per dashboard render" (now stale) to state that
   `SnapshotsByVehicleBetween` (`history.go`, `/ui/dashboard/history`) is the field's **only**
   remaining caller as of this tier.
3. **`kkpa/context/use-case/gateway/read-dashboard-bento.md`** (roadmap D6, explicitly granted
   path): step 5 of "Flow" and row 2 of "Database" currently name
   `telemetry.Reader.LatestSnapshotsByAccount` / `vehicle_snapshots` — rewrite both to
   `analytics.Reader.LatestMetricsByAccount` / `vehicle_metrics`. The "Conventions & gotchas"
   bullet "The gateway must stop depending on `internal/telemetry`... this use case is one of
   the remaining violations" is now **resolved for this specific use case** (the dashboard's own
   read no longer touches `telemetry` at all) — rewrite it to say so, while noting
   `SnapshotsByVehicleBetween` (a *different* use case, `read-dashboard-history.md`) still does.
   Do not touch `read-dashboard-history.md` itself — it is untouched by this tier.
4. No other `kkpa/context/` file references `telemetry.vehicle_snapshots` in a way this tier
   makes stale (checked: `entities/vehicle-metrics/guide.md`,
   `architecture/telemetry-data-hub.md`, `architecture/nightly-cycle.md`, and the charging
   use-cases describe telemetry/analytics relationships this tier does not change).

## Test Contract

Authored before implementation, per `ai/go-conventions.md`'s "author expected values first"
rule. Implementation tests MUST assert these exact values, not whatever the code happens to
produce.

### Fixture RM38-G-Full — every pointer field populated

```go
analytics.VehicleStatus{
    TeslaID:           1001,
    BatteryLevelPct:   72,
    BatteryRangeKm:    310.4,
    OdometerKm:        18452.0,
    InsideTempC:       ptrFloat64(21.5),
    OutsideTempC:      ptrFloat64(9.0),
    Locked:            ptrBool(true),
    SentryMode:        ptrBool(true),
    CarVersion:        ptrString("2026.20.4"),
    ChargingState:     ptrString("Charging"),
    ChargeLimitSocPct: ptrInt(80),
    CapturedAt:        ptrTime(fixedNow.Add(-2 * time.Hour)), // well within both windows
}
```

Expected `DashboardData` after `mapDashboardSnapshot(ctx, &vm, fixture, fixedNow)`:

| Field | Expected |
|---|---|
| `HasSnapshot` | `true` |
| `StatusLabel` | `"Charging"` (translated) |
| `SoftwareVer` | formatted `"Software v2026.20.4"` (translated template) |
| `LastUpdated` | `fixedNow.Add(-2h).UTC().Format("2006-01-02")` |
| `IsStale` | `false` |
| `Odometer` | `"18,452 km"` (via `formatKm`) |
| `InsideTemp` | `"22 °C"` (`%.0f` of `21.5`) |
| `OutsideTemp` | `"9 °C"` |
| `Battery` / `BatteryPct` | `"72%"` / `"72"` |
| `RangeNow` | `"310 km"` |
| `ChargeLimit` | translated `"Limit 80%"`-equivalent |
| `Locked` | `*true` |
| `SentryMode` | `*true` |

Expected dashboard header badges: `[Stale]` absent (not stale), Locked badge `"Locked"` /
`success`, Sentry badge `"Sentry: On"` / `warning`.

Expected `NavHeaderVM` (via `navHeaderFor` with this fixture as the primary vehicle's status,
`fixedNow` as "now"): `Status = NavStatusConnected` (2 h < 48 h freshness window), `BatteryPct =
"72%"`, `LastSeenLabel = ""`.

### Fixture RM38-G-Nil — every pointer field nil (pre-migration row)

```go
analytics.VehicleStatus{
    TeslaID:           1001,
    BatteryLevelPct:   72,
    BatteryRangeKm:    310.4,
    OdometerKm:        18452.0,
    InsideTempC:       nil,
    OutsideTempC:      nil,
    Locked:            nil,
    SentryMode:        nil,
    CarVersion:        nil,
    ChargingState:     nil,
    ChargeLimitSocPct: nil,
    CapturedAt:        nil,
}
```

Expected `DashboardData` after `mapDashboardSnapshot(ctx, &vm, fixture, fixedNow)`:

| Field | Expected |
|---|---|
| `HasSnapshot` | `true` (the row exists — it is the eight new columns that are absent) |
| `StatusLabel` | `"Parked"` (translated) — nil `ChargingState` collapses per D2 |
| `SoftwareVer` | `""` (line omitted) |
| `LastUpdated` | `""` |
| `IsStale` | `false` |
| `InsideTemp` | `"—"` |
| `OutsideTemp` | `"—"` |
| `Odometer` / `Battery` / `RangeNow` | unaffected — always non-pointer, same as Fixture Full |
| `ChargeLimit` | `""` (line omitted) |
| `Locked` | `nil` |
| `SentryMode` | `nil` |

Expected dashboard header badges: `[Stale]` absent (IsStale is false, not merely unset — a nil
`CapturedAt` must never render `[Stale]`), Locked badge absent, Sentry badge absent — the header
row shows nothing but the subtitle.

Expected `NavHeaderVM` (same fixture as the primary vehicle's status): `Status = NavStatusAsleep`
(roadmap D9 — nil `CapturedAt` forces Asleep, never Connected), `LastSeenLabel = ""`,
`BatteryPct = ""` (the Asleep branch never sets it — unchanged pre-existing behavior for the
Asleep state).

### Badge matrix — exhaustive, drives dashboard header tests

| `Locked` | `SentryMode` | Expected badges (order: Stale?, Locked, Sentry) |
|---|---|---|
| `*true` | `*true` | Locked "Locked"/`success`; Sentry "Sentry: On"/`warning` |
| `*true` | `*false` | Locked "Locked"/`success`; Sentry "Sentry: Off"/`ghost` |
| `*true` | `nil` | Locked "Locked"/`success`; no Sentry badge |
| `*false` | `*true` | Locked "Unlocked"/`error`; Sentry "Sentry: On"/`warning` |
| `*false` | `*false` | Locked "Unlocked"/`error`; Sentry "Sentry: Off"/`ghost` |
| `*false` | `nil` | Locked "Unlocked"/`error`; no Sentry badge |
| `nil` | `*true` | no Locked badge; Sentry "Sentry: On"/`warning` |
| `nil` | `*false` | no Locked badge; Sentry "Sentry: Off"/`ghost` |
| `nil` | `nil` | no Locked badge; no Sentry badge |

Nine combinations — `dashLockedBadge`/`dashSentryBadge` unit tests (pure functions, no gin/httptest
needed) MUST cover all nine via table-driven cases; a handler-level `httptest` case only needs
Fixture Full and Fixture Nil (the two extremes) plus one mixed case (e.g. `Locked=*true,
SentryMode=nil`) to prove the two functions are actually wired into the template.

### Fixture RM38-G-Empty — no registered vehicle has a `vehicle_metrics` row yet

Unaffected by this tier — `ok := statusMap[primary.TeslaID]` returns `ok=false`, identical
control flow to today's `mergeSnapshots(snaps)[primary.TeslaID]` miss. No new test needed beyond
confirming the existing "no snapshot yet" test still passes against the renamed/retyped
`mergeVehicleStatuses` and `analytics.VehicleStatus`.

## Fake test double

`fakeAnalyticsReader` (`internal/gateway/handlers/history_test.go`, package `handlers`, visible
to `handlers_test.go`/`charges_test.go` in the same package) currently implements
`LatestMetricsByAccount` as an unconditional `panic("... is never called by the gateway's history
fragment")`. This tier makes it genuinely called by `handlers_test.go` (dashboard, nav-header,
vehicles) and `charges_test.go` (battery suggestion) tests, so the fake gains real fields:

```go
type fakeAnalyticsReader struct {
    // ... existing fields (snaps, days, err, odometerErr, ...) unchanged ...
    statuses    []analytics.VehicleStatus
    statusesErr error
}

func (f *fakeAnalyticsReader) LatestMetricsByAccount(context.Context, uuid.UUID) ([]analytics.VehicleStatus, error) {
    return f.statuses, f.statusesErr
}
```

No other fake changes — `fakeAnalyticsReader` already satisfies `analytics.Reader` in full once
this method stops panicking; its other three methods (`RecentEfficiency`, `ConsumedByDay`,
`OdometerDeltaByDay`) are untouched.
