# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `use-case/gateway/read-dashboard-bento.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    2026-09-08
Status: PENDING REVIEW

Scope note: derived from the two requirements `RM50-gateway-add-travel-progress-subsection`
added to the gateway spec — "Dashboard Vehicle Status Panel Groups Metrics Into Named
Subsections" and "Dashboard Travel Progress Subsection" — plus the one it modified,
"Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile".
The guide's `## Input / output` section was already corrected inside that change, so this
proposal only adds behaviour rules. No `## Component map`, `## Flow`, `## Database` or
`## Entry point` block is proposed — a spec carries behaviour, not file paths.

---

## [guide] ## Conventions & gotchas — APPEND

- **The two Travel Progress trend icons are fixed, never computed.** Distance travelled
  always shows the "increasing" icon and battery used always shows the "decreasing" icon.
  The direction states what the metric *is* — distance accumulates, charge depletes — not
  whether the value rose or fell since a previous day. Do not make them delta-driven when
  a sibling metric (tyre pressure) later gets a real delta-driven icon; the two rules live
  side by side on purpose.
  _Source: spec gateway — Requirement: Dashboard Travel Progress Subsection._
- **Trend colours are semantic tokens, never a hardcoded value.** The "increasing" and
  "decreasing" indicators each resolve to their own semantic colour so the three themes
  (apex, graphite, halloween) all render correctly. `make ui-guard` fails a raw colour.
  _Source: spec gateway — Requirement: Dashboard Travel Progress Subsection._
- **A reserved subsection renders nothing at all.** The layout may hold a position for a
  metric a later capability adds. Until that capability ships it renders no heading, no
  description and no empty tile grid. An empty section would still need a title and a
  description, and the module has no exception for "not built yet".
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Panel Groups Metrics Into
  Named Subsections._
- **Travel Progress adds no read of its own.** Both values come from the same
  `LatestMetricsByAccount` row the card already fetches. No new query, no new port method,
  no Tesla Fleet API call. Keep it that way — this is the read-heavy path.
  _Source: spec gateway — Requirement: Dashboard Travel Progress Subsection._
- **The badge requirement no longer prescribes the tile grid.** The older "Locked and
  Sentry-Mode Badges" requirement used to fix the card's tile row at four tiles in one
  desktop row. It no longer does. Its live contract is only: badges in the header, and
  never a "Status" tile. Layout belongs to the subsections requirement.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card Shows Locked and
  Sentry-Mode Badges, Not a Status Tile._

## [index] ## Input ports — ADD ROWS

| `Travel Progress` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Progreso de viaje` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
