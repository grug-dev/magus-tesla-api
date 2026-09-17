Source: MAG-57 — https://linear.app/magus-monitor/issue/MAG-57/nightly-job-tests
Roadmap: openspec/roadmaps/RM62-nightly-cycle-observability.md
Tier: 3 of 4 (`RM62-app-improve-cycle-step-logging` and `RM62-analytics-add-query-logging`
already archived; `RM62-platform-add-nightly-cycle-diagram` remains). No technical
dependency on the other tiers — the roadmap orders them app → analytics → charging →
platform only because the user wanted the debug setup first.

## Why

`internal/charging` has zero `logging.Note` calls. `internal/telemetry` and
`internal/analytics` already log every nightly-path query through their own
`query_log.go`. Reading a `cmd/poller --once` log today shows telemetry's and
analytics's work, but nothing `internal/charging` does in step 2 (mirroring
Supercharger sessions, advancing the mirror watermark) or step 4 (measuring monthly
pack capacity) of the nightly cycle. Those steps run and write real rows, but the log
gives no trace of what they did.

## What Changes

- **New file `internal/charging/query_log.go`** — explicit, non-embedding logging
  decorators, mirroring `internal/telemetry/query_log.go`'s and
  `internal/analytics/query_log.go`'s shape exactly (compile-time assertion per
  decorator, no interface embedding, message topic `charging query:`).
- **Five of the module's eight public ports get a decorator; three do not.** design.md
  decides and justifies each port and each method — see its port table. In short:
  - `SessionWriter`, `MirrorWatermarkStore`, `MonthlyCapacityCalculator` — every method
    logs. Every one of these ports' callers is the nightly cycle (`internal/app`) or,
    for `MonthlyCapacityCalculator`, the manual `cmd/monthly-capacity` CLI tool. Neither
    has a gateway caller.
  - `Reader` — only `ListEntriesByVehicleUpdatedSince` logs (its one caller anywhere is
    `analytics.Recalculator.Reconcile`, nightly-only). The other three methods stay
    silent: `ListEntriesByVehicleBetween` and `ListEntriesByVehicles` are dominated by
    live gateway reads (a manual-charges page render, a form's conflict check),
    and `ListEntriesByVehicle` has no caller at all today.
  - `SuperchargerSessionAnalyticsReader` — `ListSessionsByVehicleBetween` and
    `ListSessionsByVehicleUpdatedSince` log; `ListSessionsByVehicle` stays silent (no
    caller today). This port has its own exported constructor
    (`NewSuperchargerSessionAnalyticsReader`), separate from the gateway-facing
    `NewSessionReader`, even though both build the same concrete type — so decorating
    only this constructor reaches every analytics-driven call (nightly `Reconcile` and
    the write-triggered `Recalculate`) without adding a line to the gateway's own
    Supercharger-stats page, which is wired through the OTHER constructor.
  - `Writer`, `SessionReader`, `SessionVerifier` — **excluded entirely, no decorator.**
    Every caller of all three is `internal/gateway`; none has any nightly-path caller,
    direct or indirect.
- **Five one-line production wiring changes**, all in `charging.go`: `NewReader`,
  `NewSessionWriter`, `NewSuperchargerSessionAnalyticsReader`, `NewMirrorWatermarkStore`,
  `NewMonthlyCapacityCalculator` each wrap their concrete implementation in the matching
  decorator before returning it. `NewWriter`, `NewSessionReader`, `NewSessionVerifier`
  are unchanged. `cmd/poller`, `cmd/web`, and `cmd/monthly-capacity` all call these
  exported constructors directly, so all three get the decorators with no `cmd/` change.
- **No unit tests** — roadmap Decision 1. The compile-time assertion per decorator is
  the safety net: a method added to a decorated port without a matching override here
  fails to build, not silently skips logging.

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key, no schema object.
`internal/charging`'s public port surface gains no method and loses no method — every
signature is unchanged.

**No — internally.** The five wrapped constructors keep their existing signatures; only
the concrete value they construct and return changes (now decorator-wrapped for five of
eight ports), invisible to every caller because callers already depend on the interface,
never the concrete type.

**Yes — behavior-visible only in logs.** Every call through a newly-logging method now
prints one `logging.Note` line it did not print before. This is the entire point of the
tier and produces no other observable change. No behavior, return value, or error
changes for any caller, on any port — decorated or not.

## Modules Affected

- **`internal/charging/`** — the sole owning module for this tier. New file
  (`query_log.go`), five one-line wiring edits (`charging.go`).
- **`internal/telemetry/`, `internal/analytics/`, `internal/account/`, `internal/tesla/`**
  — **not touched.** Both already have their own query logging (tiers 1–2, archived).
- **`internal/app/`** — **not touched.** Tier 1 (already archived) already labels the
  per-vehicle steps that call into this module; this tier only changes what `charging`
  itself logs underneath those calls.
- **`internal/gateway/`** — **not touched, and not made noisier.** Every port and
  method this tier excludes is exactly the set `internal/gateway` calls on a live
  request; the ports and methods this tier logs are exactly the set it never reaches.

## Out of Scope

- `internal/tesla` and `internal/account` query logging — not part of this roadmap's
  four tiers (see the roadmap's own "Out of scope" section).
- Any schema, table, column, index, or migration change — there is none in this tier.
- Structured logging / `slog` adoption — `ai/go-conventions.md` §Logging: stdlib `log`
  only, project-wide, unrelated to this tier.
- Widening `Writer`, `SessionReader`, or `SessionVerifier` with a decorator "for
  completeness." Each is gateway-only end to end; logging any of them adds a line to a
  live user request for no nightly-cycle benefit — exactly the noise the roadmap's tier
  row warns against.
