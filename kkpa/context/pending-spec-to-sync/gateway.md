# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `use-case/gateway/read-dashboard-bento.md`
Source spec:  `openspec/specs/gateway/spec.md` (delta archived at `openspec/changes/archive/2026-09-01-RM38-gateway-read-dashboard-from-metrics/specs/gateway/spec.md` — 1 ADDED, 2 MODIFIED requirements)
Generated:    2026-09-01
Status: PENDING REVIEW

---

## Curator notes — read before applying

**Scope of this proposal is narrow on purpose.** The target guide was already rewritten by
RM38 tier 2's own wave 6, so most of the spec's behaviour is present. Only rules the spec
states and the guide does **not** yet carry are proposed below. Nothing here restates the
guide's existing bullets.

**Two findings that need a human decision — this proposal does NOT act on either:**

1. **The `Navigation Vehicle Header` requirement has no use-case file in the KB.** The spec's
   second MODIFIED requirement governs `GET /ui/nav-header` and `POST /ui/vehicle/select`, and
   `INDEX.md` routes neither. Creating that use case needs a real call-path trace and the human
   gate that `use-case <endpoint>` mode carries, so it is reported, not auto-created. Suggested
   follow-up: `/kkpa-context-curate use-case GET /ui/nav-header`.
2. **No `[index]` rows are proposed.** `INDEX.md` already routes `Vehicle Status panel`,
   `Estado del vehículo`, `dashboard vehicle status` and three more aliases to this guide. The
   new vocabulary the spec introduces (`locked badge`, `sentry-mode badge`) is *attributes of
   the concept*, not synonyms for it, and the skill's rules forbid a row per attribute — those
   questions already route here through the existing rows.

---

## [guide] ## Conventions & gotchas — APPEND

- **The Vehicle Status card shows locked/sentry as header badges, and has no "Status" stat
  tile.** The card header row carries the locked badge, the sentry badge and the staleness
  badge together; none suppresses the others, and each appears purely on its own value. The
  mini-stat grid is a single row of exactly three tiles — odometer, interior temp, exterior
  temp. The card **subtitle keeps** the charging-derived status word ("Parked • Software
  v11.1.2"); only the tile was removed. Do not "restore" a Status tile, and do not drop the
  subtitle's status word.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile._
- **Badge styles are fixed and three-state.** Locked → success (green); unlocked → error
  (red); sentry on → warning; sentry off → ghost; **absent → no badge at all**. Absence is
  never rendered as "Unlocked" or "Sentry: Off" — the two are different facts and must stay
  visually distinct.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile._
- **Badge text, colour and show/hide are computed in Go, never in the template.** A helper
  returns the triple before the template runs; the template only conditionally renders a
  pre-built `ui.Badge`. No nil-check-driven text or colour selection inside the template.
  This is the same "templates contain no business logic" rule the card's number formatting,
  staleness computation and timestamp formatting already follow.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card … / Scenario: Templates contain no business logic for badge selection._
- **The badges introduced no new catalogue keys, and must not.** Their text resolves through
  keys that already existed for the vehicle-list card (locked / unlocked / sentry-on /
  sentry-off / not-reported), with `ES` and `EN` both already complete. If you extend the
  badges, reuse before you add.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card … / Scenario: No new translation keys are introduced._
- **Both freshness limits are named constants, not magic numbers.** Staleness is 36 h (one
  missed nightly cycle: 24 h + 12 h buffer) and the nav header's connected-freshness window
  is 48 h. The spec requires them to stay named constants in handler code.
  _Source: spec gateway — Requirements: Dashboard renders enriched vehicle cards… / Navigation Vehicle Header._
- **"Row predates status tracking" is NOT the same state as "no row yet".** A row that exists
  but whose status observations are all absent still renders a **normal** card — battery,
  range and odometer show as usual, and only the absent fields degrade individually. It must
  not fall back to the `HasSnapshot=false` placeholder card. Conflating the two hides data the
  row actually has.
  _Source: spec gateway — Requirement: Dashboard renders enriched vehicle cards… / Scenario: A precomputed row that predates status-observation tracking degrades per field, not per card._
- **The gateway performs no unit conversion, in the handler or the template.** Every value
  arrives from the analytics read port already in its display unit (km, °C), because the
  conversion happened once on write. Adding a conversion here is a bug, not a fix.
  _Source: spec gateway — Requirement: Dashboard renders enriched vehicle cards from stored telemetry._
- **No `pgtype` type may appear in any gateway file, and no `db` package may be imported.**
  Neither `internal/telemetry/db` nor `internal/analytics/db`. All access is through the
  `analytics.Reader` and `account.Service` public interfaces.
  _Source: spec gateway — Scenario: Gateway never imports telemetrydb or analyticsdb for this read._

## [guide] ## Input / output — APPEND

- The rendered Vehicle Status card's status-bearing outputs are: locked badge (present /
  absent), sentry badge (present / absent), staleness badge (present / absent), the subtitle's
  charging-derived status word, and a three-tile stat row. Each badge is independently
  driven by its own `*bool`; absence of a value means absence of the badge.

## [guide] ## Related use cases — APPEND

- `GET /ui/nav-header` — the navigation vehicle header reads the **same**
  `analytics.Reader.LatestMetricsByAccount` port and shares this use case's nil-`CapturedAt`
  rule (absent capture instant ⇒ Asleep, never Connected, and no relative "last seen" label).
  Not yet curated as its own use-case file — see the curator note at the top of this proposal.
