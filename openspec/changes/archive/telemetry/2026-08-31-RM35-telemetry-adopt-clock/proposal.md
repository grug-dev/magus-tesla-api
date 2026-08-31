Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 3 of 7 (`telemetry`; depends on tier 1, `RM35-clock-add-bogota-time-package`, and tier 2,
`RM35-config-adopt-clock`, both already archived and on disk)

## Why

Tiers 1–2 built `internal/clock` (the platform's single owner of the default zone,
`America/Bogota`) and made `internal/config`'s `POLLER_TIMEZONE` fallback obtain its default
from it. This tier — `RM35-telemetry-adopt-clock` — is the first **domain module** adopter:
`internal/telemetry` has its own "now" fallback and its own zone fallback, plus one of the
three copy-pasted UTC-midnight calendar-day truncators the roadmap's finding names
(`dateOnly`, the only one of the three that was already genuinely zone-aware). This tier
collapses that truncator into `clock.CalendarDay` and routes both fallbacks through `clock`.

## What Changes

- `internal/telemetry/service.go` — three call sites (roadmap D4):
  - `dateOnly(t, loc)` **deleted**, its single call site (inside `snapshotFrom`) now calls
    `clock.CalendarDay(t, loc)` directly. This is a **pure delete-and-delegate, zero behavior
    change**: `dateOnly`'s body was `y, m, d := t.In(loc).Date(); return time.Date(y, m, d, 0,
    0, 0, 0, time.UTC)` — byte-identical to `clock.CalendarDay`'s own formula (tier-1 design
    D7 built `CalendarDay` explicitly to reproduce this call site's output for every input).
    The UTC-midnight result (the `pgtype.Date` storage encoding) is unchanged — RM35 D1
    leaves that representation alone; only the *function that produces it* moved.
  - `(*service).now()`'s nil-`Config.Clock` fallback changes from `time.Now()` to
    `clock.Now()`. `clock.Now()` is `time.Now().In(Zone())` — the identical **instant**,
    re-expressed in `America/Bogota` instead of the process's own `time.Local`. Because every
    value this feeds (`CapturedAt`, `AttemptedAt`) is persisted through `pgtype.Timestamptz`,
    which stores the instant only (Postgres `timestamptz` has no zone of its own — it
    normalizes to UTC on write and any client reads it back in whatever zone it asks for),
    this call site's change is **byte-identical in every stored or externally observable
    value**. It is not merely low-risk — it changes nothing a caller can detect.
  - `(*service).location()`'s nil-`Config.Location` fallback changes from `time.Local` to
    `clock.Zone()` (`America/Bogota`). Unlike `now()`, this **would** be an observable
    behavior change for whoever hits the nil branch: `location()`'s result feeds
    `clock.CalendarDay` directly, so the zone substitution changes which calendar day a
    snapshot's `CapturedDate` is stamped with whenever the wall-clock instant sits close to a
    day boundary in one zone but not the other.
- The two deliberate test-injection seams, `Config.Clock` and `Config.Location`, are
  **unchanged** — only what each falls back to on `nil` changes. A test (or future caller)
  supplying either continues to win exactly as before.
- Stale doc comments corrected in the same change (`service.go`, `telemetry.go`,
  `internal/telemetry/AGENTS.md`, `internal/telemetry/db/query.sql` + its sqlc-regenerated
  `query.sql.go`) — each previously named `dateOnly` by name or stated the `time.Local`
  fallback; all now describe the `clock`-backed behavior. `internal/telemetry/AGENTS.md`'s
  "Allowed / forbidden imports" section gains `internal/clock` to its "May import" list.
- Root `README.md` — the dependency graph's `telemetry` row gains `clock`
  (`telemetry ──────────► account, tesla, clock, telemetry/db`), and `internal/clock`'s prose
  row's adoption list is extended to name `telemetry` alongside `config` (a review finding on
  tier 2 was that this graph edit is easy to forget — this tier does not repeat it).

### Is the nil-`Config.Location` path reachable in production? No — verified, not assumed.

`cmd/poller/main.go` is the **only** production caller that constructs a `telemetry.Config`
(confirmed: `telemetry.Config{}`/`telemetry.NewService(...)` appears nowhere else outside this
package and `internal/app/scheduler_test.go`, a test file). `cmd/poller/main.go:83-91` always
resolves `loc` via `time.LoadLocation(cfg.PollerTimezone)` — fatal on error — and always passes
it as `tcfg.Location = loc`; there is no code path in `cmd/poller` that leaves
`telemetry.Config.Location` nil. Compounding this, tier 2 (`RM35-config-adopt-clock`, already
archived) made `cfg.PollerTimezone` itself never resolve to an empty string, so
`LoadLocation` always receives either a genuine user-supplied zone name or the tier-2 default
(`"America/Bogota"`) — never `""`. **Conclusion: `location()`'s nil-fallback branch is dead
code in every current production path**, exercised only by the offline unit test that
constructs `Config{}` directly (`dedupe_test.go`'s `TestService_Location_FallsBackToClockZone`,
repaired by this tier) and by any future caller that forgets to set `Location`. The change is
still made — and still described honestly as a behavior change above — because leaving a
zone-fallback stated as `time.Local` in source that a future caller could rely on is exactly
the drift roadmap D5's `tz-guard` (tier 7) exists to prevent; "currently unreachable" is not
"safe to leave wrong."

**Breaking?** No observable production behavior changes. Both fallback swaps are either
byte-identical (`now()`, per the `pgtype.Timestamptz` argument above) or currently unreachable
(`location()`, per the reachability analysis above). `dateOnly` → `clock.CalendarDay` is a
pure refactor. Should a future caller ever construct `telemetry.Config{}` without setting
`Location`, that caller's snapshots would newly date by `America/Bogota` instead of the host's
local zone — the same class of change roadmap D1 already authorized and tier 2 already shipped
for `POLLER_TIMEZONE`, just not yet exercised here.

**Read paths affected:** none directly — `snapshotFrom`, `now()`, and `location()` are all on
the **write** path (`CollectAll`, the nightly `Collector` port), never called from `Reader`.
`ai/architecture.md` §7's Reader/Collector split is preserved: no `Reader` method is touched.

**No database object.** No migration, no schema change, no new column, no new index — this
tier only changes which function computes a value already being computed and stored exactly as
before.

## Capabilities

### Modified Capabilities

- `telemetry` — adds one requirement describing the default-timezone behavior of nightly
  snapshot capture when no explicit collection timezone is configured (previously
  unspecified — the spec was silent on the fallback, describing only the configured-timezone
  case via "Scheduled Unattended Collection"'s "timezone configurable" clause).

## Impact

- `internal/telemetry/service.go` — `dateOnly` deleted; `now()`/`location()` fallbacks
  changed; `dateFrom`'s doc comment corrected; new `internal/clock` import.
- `internal/telemetry/telemetry.go` — `Config.Location`'s doc comment corrected; one other
  stale `dateOnly` reference corrected.
- `internal/telemetry/AGENTS.md` — three stale `dateOnly` references corrected; `internal/clock`
  added to "May import".
- `internal/telemetry/dedupe_test.go` — repaired: `dateOnly` call sites → `clock.CalendarDay`
  (3 tests renamed `TestDateOnly_*` → `TestCalendarDay_*`, same expected values); the
  `time.Local`-fallback test renamed and repaired to expect `clock.Zone()`.
- `internal/telemetry/db_integration_test.go`, `db_preceding_snapshot_integration_test.go`,
  `db_read_integration_test.go`, `db_sourcea_integration_test.go`, `db_tpms_integration_test.go`
  — every `dateOnly(t, time.UTC)` call site repaired to `clock.CalendarDay(t, time.UTC)`
  (identical arguments, identical expected values — these were fixture setup, never the
  behavior under test).
- `internal/telemetry/testdb_test.go` — one stale comment reference corrected.
- `internal/telemetry/db/query.sql` (+ sqlc-regenerated `query.sql.go`) — two stale comment
  references corrected; regenerated via `sqlc generate` (no query/schema change, comment only).
- `README.md` — dependency graph + `internal/clock` prose row updated.
- No `cmd/` edit — out of scope (RM35 D4's composition-root exemption); `cmd/poller` already
  passes an explicit `Location` and needs no change for this tier to land.
