## Context

> **Revised 2026-08-16 (task T0) after the owner ruled on two paused design questions.**
> Roadmap **D17** confirms **D-B3** exactly as written (`DayConsumption.Date` is the row's
> own effective day — for a multi-day span, the span's last calendar day). Roadmap **D18**
> **overrules the original D-B7**: calendar-day bucketing uses the poller's configured zone
> (`Config.Location`), not UTC. The superseded UTC reasoning is preserved inside D-B7 as a
> rejected alternative. Two decisions were appended: **D-B12** (where the zone comes from —
> stamped on the row, so `NewReader`'s signature still does not change) and **D-B13** (the
> fetch-window widening the zone shift requires). **D-B1 survives intact.**

Tier 1 (`RM28-telemetry-add-charge-gap-storage`, archived) added `internal/telemetry`'s
`charge_gaps` table, `GapWriter.ReconcileWindow`, and
`SuperchargerReader.SuperchargerSessionsByVehicleBetween`. Tier 2
(`RM28-manualcharge-add-date-range-reader`, archived) added
`manualcharge.Reader.ListEntriesByVehicleBetween`. `telemetry.Reader.SnapshotsByVehicleBetween`
already existed before RM28 (RM8). This tier — the largest of the roadmap's four — is the
derivation itself: `internal/battery` combines all three read ports into one new port
method, `ConsumedByDay`, that computes a corrected per-day battery-consumed percentage
and detects the days whose numbers do not add up.

`internal/battery` already exists in this repo (`battery-add-efficiency-metric`) with one
metric, `RecentEfficiency`. This tier adds a second, independent metric to the same
`Reader` port and the same `reader` struct — no new module (D15: a new
`internal/consumption` module was considered and rejected; `battery` already is that
module and already composes the exact four dependencies this tier's derivation needs a
subset of).

Primary and only module for this worker's dispatch: **`internal/battery/`**. `cmd/poller`
wiring is specified in full below but implemented by the leader (outside this module's
sandbox — `ai/architecture.md`: a worker never edits outside its assigned module).
`internal/telemetry` and `internal/manualcharge` are read-only dependencies; neither's
code is touched here.

## Goals / Non-Goals

**Goals**

- A new `ConsumedByDay` method on the existing `battery.Reader` port, computing D13's
  formula per calendar day over a caller-supplied `[start, end]` window.
- Gap detection (D5/D5a) and inferred-type classification (D7a) as part of the same
  computation — `DayConsumption.Flagged` / `.MissingChargingType` — so `cmd/poller` needs
  no second pass over the same data to build `telemetry.ChargeGap`s.
- Source-specific charge-to-day matching (D12), generalized to cover multi-day spans (D8)
  without special-casing them.
- A precise, fully-specified `cmd/poller` wiring contract (D4/D4a) for the leader to
  implement: exact function calls, ordering, error handling, and the reconciliation
  window.
- `internal/battery/AGENTS.md` updated with the new port method.

**Non-Goals**

- No rendering. No bar chart, no flagged-day visual marker (D10), no multi-day-span
  marker. All `internal/gateway`, tier 4.
- No change to `telemetry.GapWriter`, `charge_gaps`'s schema, or either date-range read
  port's own contract (`SuperchargerSessionsByVehicleBetween`,
  `ListEntriesByVehicleBetween`) — both are consumed exactly as tiers 1–2 shipped them.
- No change to `RecentEfficiency`, `Efficiency`, `deriveEfficiency`, `socReadings`,
  `capacity.go`, or `NewReader`'s signature.
- No caching, no summary table, no new database object of any kind (D2, D15).
- No revival of Supercharger battery-% estimation (D14) — this tier accepts and documents
  D14a's limitation rather than working around it.
- No actual `cmd/poller` code change performed by this worker — the wiring section below
  is a specification for the leader's separate task.

---

## Design Decisions

### D-B1 (D15) — `ConsumedByDay` extends the existing `Reader` port; `NewReader`'s signature does not change

`ConsumedByDay` is added as a second method on `battery.Reader`, implemented on the same
`*reader` struct `RecentEfficiency` already uses. It needs exactly three of the four
dependencies `NewReader` already wires in — `telemetry.Reader`, `telemetry.SuperchargerReader`,
`manualcharge.Reader` — and **none** of the fourth (`vehicleLookup`/`account`, used only
by `RecentEfficiency`'s pack-capacity lookup). No new dependency is introduced and
`NewReader`'s parameter list is unchanged. This is a direct instance of the project's
AI-efficiency "closed vocabulary" principle: a second metric on an already-composed module
should not force every caller of `NewReader` (currently none — `battery` is not yet wired
into any `cmd/`, per its own `AGENTS.md`) to learn a second constructor or a widened one.

**Rejected alternative:** a second constructor, `NewConsumedReader(telemetry, supercharger,
manual)`, taking only the three needed ports. Rejected — it would create two constructors
for the same concrete `*reader` type and two ports (`Reader` would need splitting, or the
new constructor would return an unexported type), for a savings of one unused struct field
reference. Not worth the surface-area increase.

### D-B2 — `DayConsumption`'s "no data" signal is absence from the slice, not a field

`ConsumedByDay` returns a **sparse** `[]DayConsumption`: one entry per calendar day in
`[start, end]` that has a computable value, and **no entry at all** for a day that does
not (no snapshot exists for that day, or the day is skipped per D5a). There is no
`NoData`/`HasData` boolean field on `DayConsumption`.

**Why:** this mirrors the *exact* existing convention `internal/gateway/handlers/history.go`
already uses for the odometer and battery-level charts — `buildOdometerChart` and
`buildBatteryChart` both bucket the returned snapshot slice into a `map[time.Time]telemetry.Snapshot`
and treat a day's absence from that map as "no snapshot," emitting an empty labeled bar
(`Present: false`). Tier 4 will do the identical thing with `ConsumedByDay`'s result:
bucket into `map[time.Time]battery.DayConsumption` and treat absence as no-data. Adding a
`NoData` field would make the port's own contract disagree with the presence/absence
signal every caller already checks first — two ways to say the same thing, with the risk
they drift. See "How tier 4 renders a flagged day and a no-data day" below for the exact
consumption pattern this is designed to support with zero re-derivation.

**Rejected alternative:** a dense `[]DayConsumption` with exactly `end-start+1` entries,
carrying `HasData bool`. Rejected — it does not match the established gold-standard
pattern (`buildOdometerChart`/`buildBatteryChart`), and it would force every entry's other
fields to have a meaningless zero value on no-data days that callers must remember to
ignore, rather than the entry simply not existing.

### D-B3 (D1, D8, D17) — `DayConsumption.Date` is the row's own effective day, never shifted; resolving the roadmap's "start day" wording

> **Confirmed unchanged by roadmap D17 (owner ruling, 2026-08-16):** `DayConsumption.Date`
> is the row's own effective day, which for a multi-day span is the span's **last**
> calendar day. This decision's substance stands exactly as written. What roadmap **D18**
> changed is orthogonal: not *which* row's day or *which end* of a span, but the **zone in
> which that day's boundary is computed** — see D-B7 (rewritten) and D-B12. D17 fixes the
> row; D18 fixes the clock. Neither touches the other.

`DayConsumption.Date` is set to `effectiveDay(cur)` for the snapshot row `cur` that carries
the (possibly multi-day) delta — the same value D1 already establishes as final and
non-negotiable ("do NOT shift, re-derive, or 'fix' the stored `battery_used_pct_calc`";
"the five derived columns stay exactly where they are, on the row that carries them
today"). `effectiveDay` is defined in D-B7: the row's own `CapturedDate` (the poller's
zone, stamped at write time) minus one calendar day.

This resolves an apparent tension with roadmap D8's own wording: "plot a single bar on
the **start day**." Read literally against calendar dates, `effectiveDay(cur)` is
chronologically the *last* day of a multi-day span (the day the delayed poll's row
represents), not the first. This design deliberately does **not** re-attribute the bar to
`effectiveDay(predecessor) + 1` (the chronological first day of the gap) — doing so would
be exactly the kind of shift D1's correction explicitly overturned RM28's original tier 1
for. The reading this design adopts: "start day" means "the single day *at which the bar
starts appearing*" (as opposed to spreading the value across the span, or omitting it) —
i.e., the one real data point the derivation has, placed where D1 already says it belongs.
Every intervening calendar day between `effectiveDay(predecessor)` and `effectiveDay(cur)`
has **no snapshot row at all** (the poll was missed), so it is automatically absent from
`ConsumedByDay`'s output (D-B2) — exactly matching D8's "intervening days rendered as
no-data" with zero special-case code.

### D-B4 (extends D5a) — A row whose predecessor lies outside the fetched window is skipped, not guessed

`deriveConsumedByDay` iterates the fetched snapshots pairwise in chronological order:
`for i := 1; i < len(snapshots); i++`, treating `snapshots[i-1]` as `snapshots[i]`'s
predecessor for the purpose of computing the charge-matching interval's lower bound
(`prev.CapturedAt`, D12). `snapshots[0]` — the earliest row in the fetched window (D-B13) —
is **never** emitted as an output day, even if its own effective day already falls inside
`[start, end]` (which happens only when no snapshot exists at exactly `start−1`, e.g. a
pre-existing gap crossing the window's own boundary).

**Why:** `telemetry.Snapshot.BatteryUsedPctCalc` is computed by `telemetry` against that
row's own true DB-adjacent predecessor, wherever it actually is — but this module has no
way to learn *that* predecessor's `CapturedAt` unless it also happens to fall inside the
fetched window (D9a's one-day lookback assumes the common case: no pre-existing gap
crossing the boundary). Guessing an interval lower bound (e.g., "no lower bound at all," or
"assume exactly one day back") would either double-count a charge already attributed to an
earlier reconciliation window, or silently narrow the window and drop a real charge —
either way, a fabricated interval, not an observed one. D5a already establishes the
governing principle for exactly this shape of problem ("no predecessor means no claim
either way; flagging would be a fabricated finding") — this decision applies the same
principle one layer up, to the *charge-matching bound* rather than the *delta itself*.

**Consequence:** this is a narrow, boundary-only edge case (a gap immediately preceding
`start−1`), and its effect is that one day's `ConsumedByDay` entry is missing from *this*
window's output even though `telemetry` has a valid `BatteryUsedPctCalc` for it. A
different `[start, end]` window whose `start−1` does have a snapshot will emit it
normally. This is consistent with D14a's own accepted-limitation philosophy: `ConsumedByDay`
is not required to be complete at every possible window boundary, only to never fabricate.

### D-B5 (D9a, D12) — Supercharger matching: DB-narrow, then Go-side precise interval filter, with a one-day tail over-fetch

For each row pair `(prev, cur)`, a Supercharger session belongs to `cur`'s day when
`prev.CapturedAt <= session.ChargeStopDateTime < cur.CapturedAt` (D12's rule, generalized
from single-day to any pair — see the roadmap's own D12 language: "`snapshot[D].captured_at`"
and "`snapshot[D+1].captured_at`" index by *capture* day, which is exactly `predecessor`/
`current` in this module's own pairing, not by the day the result is filed under).

The matching rule itself is **zone-free** and unchanged by D18: it compares absolute
instants, so no choice of `Config.Location` can move a session between row pairs. Roadmap
D12 states this directly — *"This makes D6 irrelevant for Supercharger sessions."* See D-B7.

`ConsumedByDay` fetches sessions once for the whole call via
`SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, start−1, end+2)` — a
**two-day tail over-fetch** past `end`, one day more than this design originally specified.
Both days are needed and neither is slack (full derivation in D-B13): one because the last
emitted row's `CapturedAt` lands in the early hours of the calendar day *after* the day it
buckets under (the nightly poll runs ~03:30, well after midnight), and one more because
under D18's zoned bucketing that `CapturedAt` can be as late as `end+2 05:00Z`, which the
bounded-window port's half-open `[start, end+1day)` contract would otherwise exclude. The
over-fetch costs nothing beyond two extra days' rows:
`sumSuperchargerPctBetween`/`inferMissingChargingType` re-filter every fetched session
against each row-pair's own precise `[prev.CapturedAt, cur.CapturedAt)` interval in Go, so a
session outside every real interval is simply never matched to any row, regardless of what
the initial DB fetch included.

**Rejected alternative:** one `SuperchargerSessionsByVehicleBetween` call per row pair,
scoped exactly to that pair's interval. Rejected — this module's own established
`RecentEfficiency` precedent already issues "exactly one call per consumed port... never a
per-snapshot or per-session lookup" (`reader.go`'s own doc comment, `ai/architecture.md`
§7 read-heavy profile); an N-call pattern here would be new, unjustified N+1-style cost
for a window bounded at ~90 rows.

### D-B6 (D8, D12, D18) — Manual-entry matching: `(effectiveDay(predecessor), effectiveDay(current)]`, generalized for spans

A manual entry belongs to row `cur` (with predecessor `prev`) when
`effectiveDay(prev) < calendarDay(entry.ChargedOn) <= effectiveDay(cur)` — an
exclusive-start, inclusive-end calendar-day range. For the ordinary single-day case
(`DaysSpannedCalc == 1`), `effectiveDay(prev) + 1 day == effectiveDay(cur)`, so this
collapses to exactly `entry.ChargedOn == effectiveDay(cur)` — the literal D12 rule. For a
multi-day span, it naturally covers every day in the span without a second code path,
matching D8's "charges inside the span are summed regardless."

**This is the one matching rule D18 actually moves**, exactly as roadmap D12 predicted
("[D6] still governs manual entries"): the *bounds* are now the zoned `effectiveDay` (D-B7),
not a UTC `EffectiveDate`. `ChargedOn` itself needs no conversion and gets none — it is a
bare `DATE` the user picked deliberately, with no time-of-day component to project into any
zone (tier 2's own D5); `calendarDay` only normalizes its representation (D-B7,
"Representation vs. zone"). The identity `effectiveDay(cur) − effectiveDay(prev) ==
DaysSpanned` (D-B12) guarantees this range never skips or double-counts a day across
consecutive pairs.

`ConsumedByDay` fetches manual entries once via `ListEntriesByVehicleBetween(ctx, accountID,
teslaID, start−1, end)` — a **one-day lookback**, added by D18's zone shift and derived in
D-B13. (It was `[start, end]` before: under the UTC rule the union of every pair's matching
range was exactly `(start−1, end]`. Under the zoned rule the earliest fetched row can bucket
one day lower, so a span crossing the window's lower edge can legitimately reach back to
`start−1`.) The over-fetch is harmless — `sumManualPctBetween` re-filters every entry against
each pair's own precise range in Go.

### D-B7 (D6, D18) — The bucketing day is the row's own `CapturedDate` minus one day: the poller's configured zone, stamped at write time

> **Owner ruling, roadmap D18 (2026-08-16) — this decision was REWRITTEN.** The original
> D-B7 argued that UTC `EffectiveDate` should remain the single bucketing key and that
> roadmap D6 had been overtaken by D1's 2026-08-15 correction. **The owner overruled that.**
> Roadmap **D6 stands literally**: calendar-day bucketing uses the poller's configured zone
> (`Config.Location`, currently `America/Bogota`, UTC−5), not UTC. The superseded reasoning
> is preserved below as "Rejected alternative 1" — it is not deleted, and it is not
> resurrectable without a new owner ruling.

#### The rule

```
bucketDay(row) = row.CapturedDate − 1 calendar day
```

`internal/battery` buckets a snapshot row under `effectiveDay(cur)`, defined as the row's
own `telemetry.Snapshot.CapturedDate` minus one calendar day. `CapturedDate` is *already*
the calendar day `CapturedAt` falls on **computed in `Config.Location`** — telemetry stamps
it on the write path (`service.go`'s `snapshotFrom` → `dateOnly(capturedAt, loc)`, whose
own doc comment reads: *"This is the single place the poller's configured timezone
determines which calendar day a snapshot belongs to (D2)"*). Roadmap D6's own rationale
points at exactly this column: *"The same zone `captured_date` is already computed in
(`20260805000001_dedupe_vehicle_snapshots_daily.sql`, 'derive in Go, not SQL')."*

So D6 is satisfied **by construction**, with no new zone conversion, no new configuration
input, and no second clock: the day battery buckets under is byte-for-byte the day
telemetry already used to decide that row's identity.

#### T0.3 answered explicitly: the day is re-derived, and `EffectiveDate` is NOT used

`telemetry.Snapshot.EffectiveDate` is **not read by this module at all**. It is derived in
`internal/telemetry/mapping.go:119` as `CapturedAt.AddDate(0, 0, -1)` with `CapturedAt` in
UTC, so its calendar day is `utcDay(CapturedAt) − 1` — a **UTC** answer. A bare date cannot
be zone-converted after the fact (there is no instant left to re-project), so "make
`EffectiveDate` zone-aware" is not an available move; the day must be re-derived from a
value that still knows the zone. Two such values exist on the row, and they are equal:

| Source | Expression | Zone | Cost |
|---|---|---|---|
| `CapturedDate` (chosen) | `CapturedDate.AddDate(0,0,-1)` | `Config.Location`, stamped at write | none — already on `telemetry.Snapshot` |
| `CapturedAt` + an injected zone | `dateOnly(CapturedAt, loc).AddDate(0,0,-1)` | `loc`, resolved at read | a new constructor dependency (see D-B12) |

Both compute *the calendar day of `CapturedAt` in the poller's zone, minus one day*. They
differ only in **when** the zone is resolved. This design takes `CapturedDate` — see D-B12
for the full trade-off and why the injected-`*time.Location` alternative is rejected.

#### The arithmetic, worked at the edges D6 exists to protect

`America/Bogota` is UTC−5 with no DST, so for any instant `t`:
`bogotaDay(t) = utcDay(t)` when `t`'s UTC hour ≥ 05:00, and `bogotaDay(t) = utcDay(t) − 1`
when `t`'s UTC hour is in `[00:00, 05:00)`. Therefore the old (UTC) and new (zoned) bucket
day are **equal except** when `CapturedAt` lands in that 5-hour UTC band — i.e. 19:00–23:59
Bogota the previous evening.

| # | Scenario | `CapturedAt` | UTC day | Bogota day | old bucket (`EffectiveDate`, UTC) | **new bucket** (`CapturedDate − 1`) |
|---|---|---|---|---|---|---|
| 1 | Nominal nightly poll, 03:30 Bogota | `2026-08-14T08:30Z` | Aug 14 | Aug 14 | Aug 13 | **Aug 13** — agree |
| 2 | Poll delayed to 10:00 Bogota | `2026-08-14T15:00Z` | Aug 14 | Aug 14 | Aug 13 | **Aug 13** — agree |
| 3 | Manual `--once` run, 20:00 Bogota Aug 13 | `2026-08-14T01:00Z` | Aug 14 | Aug 13 | Aug 13 ✗ | **Aug 12** ✓ — differ by one day |
| 4 | Poll near UTC midnight, 23:30 Bogota Aug 13 | `2026-08-14T04:30Z` | Aug 14 | Aug 13 | Aug 13 ✗ | **Aug 12** ✓ |

Scenarios 3–4 are the failure D6 names. In scenario 3 the operator ran the collection on
the *evening of Aug 13*; the delta it carries accumulated over Aug 12→Aug 13's daytime, so
Aug 12 is the honest label under the poller's own clock, and Aug 13 — which the row does not
yet describe — is left free for the next capture. Under the old UTC rule the same run
labels the row Aug 13, and the *next* nightly poll (Aug 14 08:30Z → UTC bucket Aug 13)
collides on the same bucket day. Note this is not merely a labelling nicety: `CapturedDate`
carries a `UNIQUE (account_id, tesla_id, captured_date)` constraint, so under the zoned rule
two rows can never share a bucket day, while under the UTC rule they can.

**Supercharger sessions are unaffected by any of this** — roadmap D12 says so outright
("This makes **D6** irrelevant for Supercharger sessions; it still governs manual entries").
Their matching test is `prev.CapturedAt <= stop < cur.CapturedAt`, a comparison of absolute
**instants**, which no zone choice can move. Worked example, D6's own "session ending 01:00
local": a session stopping `2026-08-14T01:00` Bogota = `2026-08-14T06:00Z`, with
`prev.CapturedAt = 2026-08-13T08:30Z` and `cur.CapturedAt = 2026-08-14T08:30Z`, falls inside
`[prev, cur)` and is attributed to `cur` — whose bucket day is Aug 13. Correct under both
rules, by instant comparison alone. What *does* change is only the DB fetch window that must
be wide enough to contain such a session — see D-B13.

**Manual entries are where D6 bites**, exactly as D12 says. `manualcharge.Entry.ChargedOn` is
a bare `DATE` the user picked (`internal/manualcharge/manualcharge.go:37`), zone-agnostic by
construction. It is matched against the *range bounds* `(effectiveDay(prev),
effectiveDay(cur)]`, and those bounds are now zoned — so in scenario 3 an entry the user
dated Aug 12 is matched to the row, where the UTC rule would have missed it and produced a
false `Flagged` day. See D-B6.

#### Representation vs. zone — a distinction this design depends on

A **calendar date** in this platform is *represented* as a `time.Time` at **UTC midnight**
with no time-of-day component. That is the `pgtype.Date` convention (`CapturedDate`,
`ChargedOn`), the HTTP date-filter convention (`?start=`/`?end=`, `ai/go-conventions.md`
§"Read optimization"), and the shape of `ConsumedByDay`'s own `start`/`end` parameters. It
is a *storage representation for a bare date*, not a claim about a zone.

The **bucketing zone** — the clock that decides where one day ends and the next begins — is
`Config.Location`. D18 changes the zone; it does not change the representation. So a
`time.Date(..., time.UTC)` normalization still appears in this design (the `calendarDay`
helper), and it is **not** a UTC bucketing decision: it only strips a time-of-day component
off a value that is already a bare date. Any future reader grepping this design for "UTC"
should read every remaining hit through this distinction.

#### Consequence: a knowing mismatch with the gateway's existing charts

`internal/gateway/handlers/history.go`'s `effectiveDayUTC` still buckets the odometer and
battery-level charts by UTC `EffectiveDate`. After this change `internal/battery` buckets in
UTC−5. **On the ~5 hours a day where those disagree, the consumed chart and the two existing
charts will label the same underlying row differently.** The owner made the D18 call with
this consequence stated and accepted; it is inherited by tier 4
(`RM28-gateway-add-consumed-graph`), which must **not** re-bucket `ConsumedByDay`'s output
through `effectiveDayUTC` — `DayConsumption.Date` is already a final bucket key and passing
it through `effectiveDayUTC` a second time is a no-op only by accident of representation.
Reconciling the two (moving the existing charts onto the zoned rule) is deliberately out of
scope here and is a candidate follow-up ticket, not silent drift.

#### Rejected alternative 1 — the superseded UTC reasoning (kept verbatim; **overruled by the owner**)

> *Roadmap D6 ("calendar-day bucketing uses the poller's configured zone... charges and
> snapshots must bucket by identical rules or the formula breaks at the edges") was written
> against the original D1, before D1 was corrected on 2026-08-15. This design does not
> introduce a second, Bogota-zone-aware calendar-day computation for `EffectiveDate` or
> `ChargedOn` comparisons. `EffectiveDate` is computed once, in
> `internal/telemetry/mapping.go`, as `CapturedAt.AddDate(0, 0, -1)` in UTC — already the
> single bucketing key the gateway's own `effectiveDayUTC` helper uses for the
> odometer/battery charts today. `ChargedOn` is a plain `DATE` the user picked directly
> (zone-agnostic by construction — there is no time-of-day component to convert). Comparing
> both as UTC-midnight calendar values (this design's `dayUTC` helper, mirroring the
> gateway's `effectiveDayUTC`) is therefore the correct, already-established precedent —
> introducing a second, Bogota-zone-shifted comparison here would recreate exactly the "two
> clocks disagreeing at the edges" risk D6 warned against, not prevent it.*
>
> *This is safe in practice because the poller's schedule (`~03:30` local, well after
> midnight in both UTC and `America/Bogota`, a UTC−5 zone) means `EffectiveDate`'s "−1 day"
> UTC arithmetic and an equivalent Bogota-zone "−1 day" computation agree for every capture
> this platform produces today; a schedule change that moved the poll to run near midnight
> in either zone would need to revisit this, but that is an existing risk of `EffectiveDate`
> itself (`mapping.go`), not one this tier introduces.*

**Why it was overruled.** The argument rested on "the two rules agree in practice for every
capture this platform produces today." They do agree for the *scheduled* 03:30 poll
(scenarios 1–2), but not for an evening `--once` run or a badly delayed retry (scenarios
3–4) — and the argument's own escape hatch ("a schedule change ... would need to revisit
this") is a deferred correctness bug, not a decision. The owner's ruling makes the zoned
rule the one the module implements now, at zero marginal cost, rather than a future
migration triggered by an operational change nobody would connect to this file.

#### Rejected alternative 2 — a zone-aware helper that still starts from `EffectiveDate`

Rejected as unimplementable, and worth naming so nobody proposes it. `EffectiveDate`
arrives as a already-computed UTC-day-derived value; converting it "into Bogota" would mean
calling `.In(loc)` on it, which shifts it to `2026-08-12T19:00-05:00` for scenario 1 and
would bucket a *nominal* poll one day early — precisely inverting the intended fix. The
zone must be applied to the original **instant** (`CapturedAt`), or read from a value that
already had it applied to that instant (`CapturedDate`). There is no third option.

### D-B12 (D6, D18) — `internal/battery` needs no `*time.Location`; the zone reaches it stamped on the row

**Answering T0.2: where does this module obtain its `*time.Location`? It does not need one.**

The bucketing zone reaches `internal/battery` **inside the data**, on
`telemetry.Snapshot.CapturedDate`, which telemetry computed in `Config.Location` at write
time (D-B7). `deriveConsumedByDay` and `ConsumedByDay` therefore take no `loc` parameter,
`NewReader`'s signature is unchanged (**D-B1 survives intact**), and neither composition root
gains a battery-specific wiring step.

**What the composition roots DO supply** — and this is the part of T0.2's "both composition
roots must supply it" that is real: each root already resolves a zone to choose the
`[start, end]` **window bounds** it asks for, and that is unchanged by this tier.

- `cmd/poller` already loads `time.LoadLocation(cfg.PollerTimezone)` into `loc`
  (`main.go:63`) for the scheduler and for `telemetry.Config.Location`. The reconciliation
  step's `end := "yesterday"` must be computed in that same `loc` — see the updated
  `cmd/poller` wiring section. No new config is read; `loc` is already in scope three lines
  above the wiring this tier adds.
- `cmd/web` (tier 4) already resolves "today" in the **browser's** zone via
  `browserToday(c)` (`internal/gateway/handlers/history.go`, `buildHistoryPresets`), which is
  how `?start=`/`?end=` and the presets are chosen today. Unchanged.

So the location lives where the *question* is asked ("which days is the user asking
about?"), and the zone that *answers* it ("which day does this row belong to?") travels with
the row. Those are two different concerns and this design keeps them separate.

**Rejected alternative: inject a `*time.Location` and re-derive from `CapturedAt`.** The
mechanically obvious option — widen `NewReader` to
`NewReader(..., window time.Duration, loc *time.Location)` (or add a `loc` parameter to
`ConsumedByDay`) and compute `dateOnly(cur.CapturedAt, loc).AddDate(0,0,-1)` at read time.
It produces the *same* number today. Rejected on three grounds:

1. **It reintroduces the two-clocks failure D6 exists to prevent.** With an injected zone,
   the bucketing zone becomes a per-composition-root runtime input, so `cmd/web` and
   `cmd/poller` can be configured differently (different `POLLER_TIMEZONE`, a missing env
   var falling back to `time.Local` on a differently-zoned host) — and then the dashboard's
   bars and the `charge_gaps` ledger the poller writes disagree about which day a row is,
   with no error anywhere. A zone stamped once at write time cannot drift between readers.
2. **It would desynchronize `Date` from `DaysSpanned`.** `telemetry`'s own
   `deriveConsumption` computes `DaysSpannedCalc` as
   `cur.CapturedDate.Sub(prev.CapturedDate)` in whole days (`service.go:587`) — already a
   `Config.Location` quantity. Deriving `Date` from `CapturedDate` too makes
   `effectiveDay(cur) − effectiveDay(prev) == DaysSpanned` an **exact identity** for every
   emitted pair (both sides reduce to `cur.CapturedDate − prev.CapturedDate`). Deriving it
   from `CapturedAt` under a read-time zone breaks that identity by ±1 at the same edges,
   for the same reason. (Note this identity does *not* hold under the old UTC rule either —
   fixing that is a side benefit of D18, and it is asserted as a test, scenario (m).)
3. **Change-locality / AI-efficiency.** Zero new parameters, zero constructor churn, zero
   composition-root edits, `D-B1` preserved, and the leader-owned T6 `cmd/poller` contract
   untouched — versus a widened constructor every future caller must learn, threaded through
   two `cmd/` roots, to reach a value the row already carries.

**Accepted trade-off (stated, not hidden).** `CapturedDate` is frozen at write time, so if
the deployment's zone ever changed, historical rows keep the zone they were captured under
while new rows use the new one. This is accepted because the alternative is *worse*, not
merely different: re-deriving at read time would re-bucket all history under the new zone
while `captured_date`'s `UNIQUE` constraint and `DaysSpannedCalc` stayed on the old one —
manufacturing exactly the disagreement D6 forbids. Under the chosen design each row is
internally consistent forever: its bucket day, its dedupe identity, and its `DaysSpanned`
all move together or not at all. Every existing row is already stamped `America/Bogota` —
the dedupe migration backfilled `captured_date` with an explicit
`(captured_at AT TIME ZONE 'America/Bogota')::date` and the column is `NOT NULL`, so there
is no NULL/unstamped case to defend against.

### D-B8 (D5) — `minFlagDistanceKm` is a named constant

```go
// minFlagDistanceKm is the D5 gap-detection threshold: a day whose consumed pct
// computes to exactly zero is flagged only when the vehicle demonstrably drove
// more than this many km that day. Below it, "zero consumed, zero driven" is a
// plausible parked day, not a data gap.
const minFlagDistanceKm = 10.0
```

Never inlined as a literal in the comparison, per the leader's dispatch instruction and
D5's own wording.

### D-B9 (D7a) — `telemetry.MissingChargingType` is reused directly; no duplicate vocabulary

`DayConsumption.MissingChargingType` is typed `telemetry.MissingChargingType` (the exact
type + the two constants `telemetry.MissingChargingTypeManual` /
`telemetry.MissingChargingTypeSupercharger`, both already shipped in tier 1) — `battery`
already imports `internal/telemetry`, so this costs no new import and creates no second
type a caller must map between. `cmd/poller` passes `DayConsumption.MissingChargingType`
straight through into `telemetry.ChargeGap.MissingChargingType` with no conversion.

### D-B10 — Database Changes: none

**Explicit statement, as required even when the `database` design gate does not trip:**
this change adds no migration, no table, no column, no index. `internal/battery` owns no
database (D15, its own `AGENTS.md`'s "Data ownership" section). `ConsumedByDay` issues
three reads against tables and indexes tiers 1–2 already built and indexed for exactly
these access patterns; it writes nothing. The `database` design gate therefore does not
apply to this change.

### D-B11 (D14, D14a) — Carrying forward the known detection blind spot

Unchanged from the roadmap: `Flagged` catches only a *negative* corrected figure, or a
*zero* figure alongside `DistanceTraveledKmCalc > minFlagDistanceKm`. A charge that only
*partially* offsets a day's driving (e.g., a Supercharger session with NULL percentages,
D14) leaves a positive, plausible-looking, silently understated `ConsumedPct` that
`Flagged` never catches — `internal/telemetry`'s `charge_gaps` ledger this tier's flagged
days feed into is a **lower bound** on days needing attention, never the complete set.
This tier does not attempt to close that gap (out of scope, D14) and does not obscure it:
this section exists so a future reader of this design does not rediscover the limitation
and mistake it for an oversight.

### D-B13 (D6, D9a, D18) — Every DB fetch widens by one day to cover the zone shift; Go re-filters precisely

**Appended for D18.** All three read ports window on **UTC** quantities — `telemetry`'s
`SnapshotsByVehicleBetween` filters on `EffectiveDate` (UTC), and
`SuperchargerSessionsByVehicleBetween` filters on a UTC instant range. This module now
buckets in `Config.Location`. Since a zoned day can sit one calendar day **below** its UTC
counterpart (D-B7's table), a fetch scoped to the UTC window would silently drop rows that
belong in the zoned window. Every fetch is therefore widened; because each Go-side matcher
re-filters against its own precise predicate, **over-fetching can only cost rows read, never
change a result.**

Writing `U(r)` for row `r`'s UTC effective day and `B(r) = effectiveDay(r)` for its zoned
bucket day, D-B7 gives `B(r) ∈ {U(r), U(r) − 1}` for any zone with a negative UTC offset.

| Fetch | Window | Why the widening |
|---|---|---|
| `SnapshotsByVehicleBetween` | `[start−1, end+1]` | `−1`: D9a's predecessor lookback, unchanged. `+1`: a row with `U = end+1` can have `B = end` and must be emitted; without it the window's last day silently vanishes whenever the poll ran in the 00:00–05:00Z band. |
| `SuperchargerSessionsByVehicleBetween` | `[start−1, end+2]` | The last emitted row has `B = end`, so its `CapturedDate` is `end+1` and `CapturedAt ∈ [end+1 05:00Z, end+2 05:00Z)`. Sessions matched to it satisfy `stop < CapturedAt`, so coverage must reach `end+2 05:00Z`. The port covers `stop < to+1day` (UTC), so `to = end+2`. `to = end+1` would leave the 5-hour band `[end+2 00:00Z, end+2 05:00Z)` uncovered. |
| `ListEntriesByVehicleBetween` | `[start−1, end]` | The earliest fetched row can have `B = start−2` (it is fetched for `U = start−1`, and `B` may be one lower). If the row at `start−1` is missing — a multi-day span crossing the window's lower edge — that row becomes the predecessor of the first emitted row, and the matching range `(start−2, start]` legitimately includes `start−1` (D8: "charges inside the span are summed regardless"). Under the UTC rule this could not happen, which is why the original design needed no lookback here. |

**No tail over-fetch on manual entries**, deliberately: the matching range's upper bound is
`effectiveDay(cur) ≤ end` by the emit filter, so no entry dated after `end` can ever match.

**Cost.** One extra calendar day of snapshots (bounded by the SQL's own `LIMIT 400`, which a
~90-day window plus two days comes nowhere near), two extra days of Supercharger sessions,
one extra day of manual entries — all on a read path already argued negligible in D-B5 and
the roadmap's D2. This is the read-heavy `Performance-Profile`'s exact trade: read a few
more rows rather than risk a wrong number at a boundary.

**Generality.** "One day on each side" is sufficient for *any* IANA zone, not just
`America/Bogota`: no zone offset exceeds ±24h, so `|B(r) − U(r)| ≤ 1` day always. Nothing in
this rule hardcodes UTC−5, and a `POLLER_TIMEZONE` change needs no revision here.

---

## `cmd/poller` wiring (specification for the leader's task, not implemented here)

`cmd/poller/main.go` currently wires only `telemetry.Collector` (see the file's own
header comment: "all collection logic lives in internal/telemetry"). This tier's
detection work adds, to the **`--once` and nightly scheduled paths alike**, a step that
runs **after** `collector.CollectAll(ctx)` (or, in scheduled mode, after each cycle the
scheduler runs) succeeds — never before, since detection needs the night's freshly
written snapshot to exist (D4: "after snapshots are written").

**Additional constructors to wire** (all already exist; none of these are added by this
tier):

```go
superchargerReader := telemetry.NewSuperchargerReader(pool)
manualReader        := manualcharge.NewReader(pool)
gapWriter           := telemetry.NewGapWriter(pool)
batteryReader       := battery.NewReader(telemetry.NewReader(pool), superchargerReader, manualReader, acct, battery.DefaultWindow)
```

(`battery.NewReader`'s `window` argument is required by its signature but unused by
`ConsumedByDay` — only `RecentEfficiency` reads it. Passing `battery.DefaultWindow` here
is a harmless, honest placeholder; `cmd/poller` never calls `RecentEfficiency`.)

**The per-cycle reconciliation step**, run once per vehicle after collection:

```go
// GapReconciliationWindow (battery.go, this tier): 30 days, matching
// battery.DefaultWindow — generous enough to catch a manual-entry backfill
// days after the fact, cheap enough to recompute nightly (≤30 snapshot rows
// + a handful of charge rows per vehicle, per D2's read-heavy tolerance).
// "Yesterday" is computed in the POLLER'S OWN ZONE (loc, already loaded at
// main.go:63 for the scheduler and telemetry.Config.Location) -- roadmap D6/D18
// and design D-B12: the composition root resolves the zone that answers "which
// days am I asking about"; internal/battery needs no *time.Location of its own.
// Using time.Now().UTC() here instead would ask for the wrong day for 5 hours
// out of every 24. Today's data is not captured until TOMORROW's poll, so the
// window ends yesterday (same reasoning as roadmap D11).
y, m, d := time.Now().In(loc).Date()
end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
start := end.AddDate(0, 0, -int(battery.GapReconciliationWindow.Hours()/24)+1)

vehicles, err := acct.AllRegisteredVehicles(ctx)
if err != nil {
    log.Printf("gap reconciliation: listing vehicles: %v", err)
    return // whole-cycle failure, mirrors CollectAll's own enumeration-failure handling
}

for _, v := range vehicles {
    days, err := batteryReader.ConsumedByDay(ctx, v.AccountID, v.TeslaID, start, end)
    if err != nil {
        log.Printf("gap reconciliation: vehicle %d: consumed-by-day: %v", v.TeslaID, err)
        continue // per-vehicle isolation, mirrors CollectAll's own per-vehicle error handling
    }

    var flagged []telemetry.ChargeGap
    for _, d := range days {
        if !d.Flagged {
            continue
        }
        flagged = append(flagged, telemetry.ChargeGap{
            AccountID:            v.AccountID,
            TeslaID:              v.TeslaID,
            VIN:                  v.VIN,
            Date:                 d.Date,
            MissingChargingType:  d.MissingChargingType,
        })
    }

    if err := gapWriter.ReconcileWindow(ctx, v.AccountID, v.TeslaID, start, end, flagged); err != nil {
        log.Printf("gap reconciliation: vehicle %d: reconcile window: %v", v.TeslaID, err)
        continue
    }
}
```

Every `flagged` element's `Date` is guaranteed inside `[start, end]` by construction
(`deriveConsumedByDay` never emits a day outside its caller-supplied window, D-B4/D-B3),
so `ReconcileWindow`'s own defense-in-depth window-membership check (tier 1's design)
never rejects a well-formed call from this wiring.

**Error handling:** per-vehicle isolation (one vehicle's failure does not abort another's
reconciliation), mirroring `telemetry.Collector.CollectAll`'s own documented isolation
pattern (`main.go`'s own comment: "CollectAll returns nil for per-vehicle failures"). A
failure to enumerate vehicles at all (`AllRegisteredVehicles` erroring) is treated as a
whole-cycle failure, also mirroring `CollectAll`'s existing shape. Reconciliation failures
are logged, never fatal — a missed reconciliation self-heals on the next nightly run
(D7b: `ReconcileWindow` is always safe to retry from scratch, since `flagged` is freshly
recomputed every time).

---

## Go-Level Surface

### `battery.go` additions

```go
// GapReconciliationWindow is the rolling window cmd/poller re-derives and
// reconciles against telemetry's charge_gaps ledger every nightly run (D4/D4a,
// D7b) — 30 days: generous enough to catch a manual-entry backfill days after
// the fact, cheap enough to recompute nightly (bounded by ~30 snapshot rows
// and a handful of charge rows per vehicle, per the read-heavy Performance-
// Profile's tolerance for off-hours write-path cost).
const GapReconciliationWindow = 30 * 24 * time.Hour

type Reader interface {
    RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)

    // ConsumedByDay returns the corrected per-day battery-consumed percentage
    // (D13) for the given vehicle over [start, end], both whole calendar days
    // represented as UTC-midnight time.Time, end inclusive (matching this
    // platform's HTTP date-filter convention).
    //
    // Which calendar day a row falls on is decided in the POLLER'S CONFIGURED
    // ZONE (telemetry.Config.Location, roadmap D6/D18), NOT in UTC: the day is
    // the row's own telemetry.Snapshot.CapturedDate minus one day, and
    // CapturedDate was stamped in that zone on the write path. The
    // UTC-midnight bounds above are a REPRESENTATION for a bare date, not a
    // bucketing zone -- see design.md D-B7. Note this differs from
    // internal/gateway/handlers/history.go's effectiveDayUTC, which still
    // buckets the odometer/battery charts in UTC; the mismatch is known and
    // accepted (D-B7). Do NOT re-bucket this method's Date through
    // effectiveDayUTC -- it is already a final bucket key.
    //
    // Recomputed on every call -- no cache, no
    // stored state (D2). The result is SPARSE: it contains one entry per
    // calendar day that has a computable value, and NO entry for a day that
    // does not (no snapshot exists for that day, or the day is the vehicle's
    // very first-ever snapshot -- D5a). Absence from the returned slice IS
    // the "no data" signal (design.md D-B2) -- callers bucket by Date exactly
    // like internal/gateway/handlers/history.go's existing
    // buildOdometerChart/buildBatteryChart already do for the odometer and
    // battery-level charts.
    //
    // This port performs no window-size validation or capping of its own
    // (mirrors telemetry.Reader.SnapshotsByVehicleBetween's identical
    // stance) -- keeping a window reasonable is the caller's job (the HTTP
    // handler in tier 4, the fixed GapReconciliationWindow in cmd/poller).
    ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error)
}

// DayConsumption is one calendar day's corrected battery-consumed result --
// our own domain model, no vendor suffix (ai/architecture.md §6).
type DayConsumption struct {
    // Date is the calendar day this entry describes -- the underlying
    // telemetry.Snapshot's own CapturedDate minus one calendar day, i.e. that
    // row's effective day computed in the poller's configured zone (roadmap
    // D6/D18, design.md D-B7). NEVER shifted or re-attributed to another row
    // (design.md D-B3, roadmap D1/D17). Represented as UTC midnight because
    // that is this platform's bare-calendar-date representation, NOT because
    // the day boundary is UTC. For a multi-day span
    // (DaysSpanned > 1), this is the single day the one available row
    // represents; every day between the predecessor and this one has no
    // entry at all (design.md D-B2/D-B3, roadmap D8).
    Date time.Time
    // ConsumedPct is battery_used_pct_calc + sum(end_battery_pct -
    // start_battery_pct) across every matched charge event from BOTH
    // sources (roadmap D13). Raw and unrounded; MAY be negative or exactly
    // zero -- callers decide how to render that (design.md D10 is a
    // gateway/tier-4 concern; this port never clamps or hides it).
    ConsumedPct float64
    // DistanceKm is the row's DistanceTraveledKmCalc (0 when telemetry
    // stored NULL for it -- see telemetry.go: only possible when
    // BatteryUsedPctCalc is also NULL, which this port already skips via
    // D5a, so DistanceKm is effectively always populated on an emitted
    // entry).
    DistanceKm float64
    // Flagged is the D5/D5a gap-detection result: true when ConsumedPct < 0,
    // or when it is exactly 0 while DistanceKm > minFlagDistanceKm.
    Flagged bool
    // MissingChargingType is the D7a inferred source, valid only when
    // Flagged is true (zero value "" otherwise). Reuses
    // telemetry.MissingChargingType directly -- no duplicate vocabulary
    // (design.md D-B9).
    MissingChargingType telemetry.MissingChargingType
    // DaysSpanned is the row's own DaysSpannedCalc (never re-derived) -- 1
    // for a normal night-to-night poll, >1 signals a multi-day span (D8) a
    // caller may want to mark distinctly.
    DaysSpanned int
}
```

### `consumed.go` (new file)

```go
package battery

import (
    "time"

    "github.com/cristianpena/magus-tesla-api/internal/manualcharge"
    "github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// minFlagDistanceKm -- see design.md D-B8.
const minFlagDistanceKm = 10.0

// calendarDay normalizes an already-bare calendar date to this platform's
// date representation: UTC midnight, no time-of-day component (the
// pgtype.Date convention that telemetry.Snapshot.CapturedDate and
// manualcharge.Entry.ChargedOn already arrive in, and the shape of
// ConsumedByDay's own start/end parameters).
//
// This is NOT a timezone conversion and NOT a bucketing decision: it never
// moves a value across a day boundary, it only strips a stray time-of-day
// component. The zone that decides day boundaries is the poller's
// Config.Location -- see effectiveDay below and design.md D-B7
// ("Representation vs. zone").
func calendarDay(t time.Time) time.Time {
    t = t.UTC()
    return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// effectiveDay returns the calendar day snapshot s DESCRIBES: its own
// CapturedDate minus one calendar day. The nightly poller runs at ~03:30 and
// captures the state accumulated over the PRIOR day, so the row's day is one
// before its capture day.
//
// The zone is the poller's configured Config.Location (roadmap D6, owner
// ruling D18), and it arrives here already applied: telemetry stamps
// CapturedDate on the write path via dateOnly(capturedAt, loc)
// (internal/telemetry/service.go), which is the single place that zone
// decides a snapshot's calendar day. This module therefore needs no
// *time.Location of its own (design.md D-B12).
//
// Deliberately does NOT read s.EffectiveDate: that field is derived as
// CapturedAt.AddDate(0,0,-1) with CapturedAt in UTC (telemetry/mapping.go), so
// its calendar day is a UTC answer, and a bare date cannot be re-zoned after
// the fact (design.md D-B7). AddDate is calendar-day arithmetic, not a 24h
// duration, so DST cannot shift it.
//
// Invariant, asserted by test (m): for any consecutive pair,
// effectiveDay(cur) - effectiveDay(prev) == *cur.DaysSpannedCalc, because
// telemetry derives DaysSpannedCalc from the same two CapturedDate values.
func effectiveDay(s telemetry.Snapshot) time.Time {
    return calendarDay(s.CapturedDate).AddDate(0, 0, -1)
}

// deriveConsumedByDay is the pure D13 derivation, fully offline: no I/O, only
// plain telemetry.Snapshot / telemetry.SuperchargerSession / manualcharge.Entry
// values in, []DayConsumption out. snapshots MUST be ordered chronologically
// ascending and MUST include the one-day lookback row before start when it
// exists (D9a; ConsumedByDay's caller, reader.go, guarantees both, and widens
// every fetch by a day for the zone shift per design.md D-B13).
//
// start/end are inclusive bare calendar dates and are compared against each
// row's ZONED effective day (effectiveDay, design.md D-B7), which is why the
// caller may hand this function rows just outside [start, end] -- they are
// filtered here, against the same day definition the emitted Date carries.
func deriveConsumedByDay(snapshots []telemetry.Snapshot, sessions []telemetry.SuperchargerSession, entries []manualcharge.Entry, start, end time.Time) []DayConsumption {
    out := make([]DayConsumption, 0, len(snapshots))
    for i := 1; i < len(snapshots); i++ {
        prev, cur := snapshots[i-1], snapshots[i]
        day := effectiveDay(cur)
        if day.Before(start) || day.After(end) {
            continue
        }
        if cur.BatteryUsedPctCalc == nil {
            continue // D5a: no predecessor claim for this row, skip
        }

        chargePct := sumSuperchargerPctBetween(sessions, prev.CapturedAt, cur.CapturedAt) +
            sumManualPctBetween(entries, effectiveDay(prev), day)

        consumed := float64(*cur.BatteryUsedPctCalc) + chargePct

        var distanceKm float64
        if cur.DistanceTraveledKmCalc != nil {
            distanceKm = *cur.DistanceTraveledKmCalc
        }

        flagged := consumed < 0 || (consumed == 0 && distanceKm > minFlagDistanceKm)

        var missingType telemetry.MissingChargingType
        if flagged {
            missingType = inferMissingChargingType(sessions, prev.CapturedAt, cur.CapturedAt)
        }

        daysSpanned := 1
        if cur.DaysSpannedCalc != nil {
            daysSpanned = *cur.DaysSpannedCalc
        }

        out = append(out, DayConsumption{
            Date:                 day,
            ConsumedPct:          consumed,
            DistanceKm:           distanceKm,
            Flagged:              flagged,
            MissingChargingType:  missingType,
            DaysSpanned:          daysSpanned,
        })
    }
    return out
}

// sumSuperchargerPctBetween sums (EndBatteryPct - StartBatteryPct) across
// every session whose ChargeStopDateTime falls in [from, to) -- D12's
// interval rule, generalized to any predecessor/current CapturedAt pair
// (design.md D-B5). Sessions with either percentage NULL (D14: always true
// today) contribute 0 -- their absence is what inferMissingChargingType
// flags below, not something this function should estimate.
func sumSuperchargerPctBetween(sessions []telemetry.SuperchargerSession, from, to time.Time) float64 {
    var total float64
    for _, s := range sessions {
        if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
            continue
        }
        if s.StartBatteryPct != nil && s.EndBatteryPct != nil {
            total += float64(*s.EndBatteryPct - *s.StartBatteryPct)
        }
    }
    return total
}

// inferMissingChargingType implements D7a: SUPERCHARGER when a session in
// [from, to) exists with either battery percentage NULL (the exact record
// needing a fill is already known); MANUAL otherwise. Only called once a day
// is already known to be Flagged.
func inferMissingChargingType(sessions []telemetry.SuperchargerSession, from, to time.Time) telemetry.MissingChargingType {
    for _, s := range sessions {
        if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
            continue
        }
        if s.StartBatteryPct == nil || s.EndBatteryPct == nil {
            return telemetry.MissingChargingTypeSupercharger
        }
    }
    return telemetry.MissingChargingTypeManual
}

// sumManualPctBetween sums (EndBatteryPct - StartBatteryPct) across every
// entry whose ChargedOn falls in (fromDay, toDay] -- D12's date match,
// generalized the same way as sumSuperchargerPctBetween for D8 spans
// (design.md D-B6). fromDay/toDay are ZONED effective days supplied by the
// caller (effectiveDay); ChargedOn is a bare user-picked DATE needing no
// conversion, so calendarDay here only normalizes its representation
// (design.md D-B7). Entries with either percentage nil (optional fields on
// manualcharge.Entry) contribute 0, symmetric with the Supercharger
// nil-handling above.
func sumManualPctBetween(entries []manualcharge.Entry, fromDay, toDay time.Time) float64 {
    var total float64
    for _, e := range entries {
        d := calendarDay(e.ChargedOn)
        if !d.After(fromDay) || d.After(toDay) {
            continue
        }
        if e.StartBatteryPct != nil && e.EndBatteryPct != nil {
            total += float64(*e.EndBatteryPct - *e.StartBatteryPct)
        }
    }
    return total
}
```

### `reader.go` addition

```go
// Fetch windows are DELIBERATELY wider than [start, end] on every port -- see
// design.md D-B13 for the derivation. Two independent reasons stack: D9a's
// predecessor lookback, and the fact that all three ports window on UTC
// quantities while this module buckets in the poller's configured zone
// (D-B7/D18), so a row can bucket one calendar day below its UTC window
// position. deriveConsumedByDay re-filters everything against the precise
// per-pair predicates, so a wider fetch can only cost rows read, never change
// a result.
func (r *reader) ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error) {
    lookbackStart := start.AddDate(0, 0, -1) // D9a predecessor lookback

    // +1 tail: a row at UTC EffectiveDate end+1 can bucket to zoned day end.
    snapshots, err := r.telemetry.SnapshotsByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end.AddDate(0, 0, 1))
    if err != nil {
        return nil, err
    }

    // +2 tail: the last emitted row's CapturedAt can reach end+2 05:00Z, and
    // sessions match on stop < CapturedAt (design.md D-B5/D-B13).
    sessions, err := r.supercharger.SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end.AddDate(0, 0, 2))
    if err != nil {
        return nil, err
    }

    // -1 lookback: a span crossing the window's lower edge can match an entry
    // dated start-1 (design.md D-B6/D-B13). No tail -- no entry after end can
    // ever match.
    entries, err := r.manual.ListEntriesByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end)
    if err != nil {
        return nil, err
    }

    return deriveConsumedByDay(snapshots, sessions, entries, start, end), nil
}
```

No change to `NewReader`, the `reader` struct fields, or any existing method.

### How tier 4 renders a flagged day and a no-data day (for the next tier's design, not built here)

**One deliberate deviation from those two functions, for tier 4's attention:** they key
their maps on `effectiveDayUTC(s.EffectiveDate)`. Tier 4 must key on `d.Date` **directly** —
`DayConsumption.Date` is already a final, zoned bucket key (D-B7/D18), and pushing it through
`effectiveDayUTC` again is a no-op only by accident of representation while reading as though
UTC bucketing were intended. Because the two charts still bucket in UTC, the consumed chart's
bars can be labelled one day apart from the odometer/battery bars for the same underlying row
whenever a capture landed between 00:00Z and 05:00Z. That mismatch is known and accepted
(D-B7); it is a rendering fact tier 4 should decide how to present, not a bug to "fix" by
re-bucketing this port's output.

Otherwise mirroring `buildOdometerChart`/`buildBatteryChart`: bucket `ConsumedByDay`'s
result into `byDay := make(map[time.Time]battery.DayConsumption, len(days))`, then iterate
`d := start; !d.After(end); d = d.AddDate(0, 0, 1)`. A day **absent** from `byDay` renders
as today's existing "no snapshot" empty labeled bar (`Present: false`) — no-data. A day
**present** with `Flagged == false` renders a normal bar at `ConsumedPct`. A day present
with `Flagged == true` renders per D10 — a zero-height bar with a distinct warning marker,
using `MissingChargingType` to pick the bilingual tooltip ("possible missing Supercharger
record" vs. "possible missing manual entry"), regardless of `ConsumedPct`'s actual sign. A
day present with `DaysSpanned > 1` additionally carries its own span marker (independent
of `Flagged` — D8's multi-day case is not itself a data-quality flag unless its own
`ConsumedPct` also fails D5's test).

---

## Test Contract (authored before implementation, binding)

Per `ai/go-conventions.md`'s Test-Execution-Policy, these are written but not run by the
worker; `go vet ./...` compiles them as a signature-drift signal. All of this module's
tests are offline (no `DATABASE_URL`, no Docker — `AGENTS.md`'s own "Testing" section).

**Snapshot-fixture convention (binding, changed by D18).** Every `telemetry.Snapshot`
fixture MUST set both `CapturedAt` (the instant, used for Supercharger interval matching)
and **`CapturedDate`** (the zoned capture day, from which the bucket day is derived —
D-B7). Where a scenario below says "effective day `D`", the fixture sets
`CapturedDate = D + 1 day`.

`EffectiveDate` MUST be left at its zero value in every fixture **except** scenario (m),
which sets it to a deliberately wrong value. Leaving it zero is itself a guard: if any
implementation regresses to reading `EffectiveDate`, every bucket day collapses to the zero
time and effectively all of (a)–(i) fail loudly rather than subtly.

### Pure-function tests (`consumed_test.go`) — no fakes needed

**(a) Roadmap's own verified case 1 — single session, single day → 11**
- **Given** `prev` (effective day D0, i.e. `CapturedDate = D1`, CapturedAt `T0`), `cur`
  (effective day D1, i.e. `CapturedDate = D2`, CapturedAt `T1`,
  `BatteryUsedPctCalc = 22 - 73 = -51`, `DaysSpannedCalc = 1`).
- **And** one Supercharger session, `StartBatteryPct = 18`, `EndBatteryPct = 80`,
  `ChargeStopDateTime` inside `[T0, T1)`.
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, nil, D0, D1)` is
  called.
- **Then** exactly one entry is returned, `Date = D1`, `ConsumedPct = -51 + (80-18) = 11`,
  `Flagged = false`.

**(b) Roadmap's own verified case 2 — two sessions, single day → 5 (not −25, not 15)**
- **Given** `prev`/`cur` as above but `BatteryUsedPctCalc = 30 - 75 = -45`.
- **And** two Supercharger sessions inside `[T0, T1)`: session1 `20→50`
  (`ChargeStopDateTime` earlier), session2 `60→80` (`ChargeStopDateTime` later).
- **When** `deriveConsumedByDay` is called.
- **Then** `ConsumedPct = -45 + (30 + 20) = 5`. Also assert, as a regression guard: this is
  **not** `-45 + 20 = -25` (latest-session-only) and **not** `-45 + 60 = 15`
  (first-start-to-last-end) — the sum-both-sessions formula (D13) is the one under test.

**(c) A day flagged negative**
- **Given** `BatteryUsedPctCalc = 50 - 60 = -10`, no charge events matched (chargePct = 0).
- **When** `deriveConsumedByDay` is called.
- **Then** `ConsumedPct = -10`, `Flagged = true`, `MissingChargingType = MANUAL` (no
  Supercharger session present in the interval).

**(d) A day flagged zero-with-distance**
- **Given** `BatteryUsedPctCalc = -20`, one manual entry inside `(prevDay, curDay]` with
  `StartBatteryPct = 30`, `EndBatteryPct = 50` (contributes +20), `DistanceTraveledKmCalc =
  50.0`.
- **When** `deriveConsumedByDay` is called.
- **Then** `ConsumedPct = 0`, `50.0 > minFlagDistanceKm (10)`, so `Flagged = true`,
  `MissingChargingType = MANUAL`.

**(e) Zero-with-distance ≤ threshold must NOT flag (boundary at exactly 10)**
- Two subcases, same `ConsumedPct = 0` setup as (d): `DistanceTraveledKmCalc = 10.0`
  (exactly the threshold — `>` is strict, so **not** flagged) and `= 5.0` (well under).
- **Then**, in both subcases, `Flagged = false`.

**(f) NULL `BatteryUsedPctCalc` is skipped, never flagged**
- **Given** `cur.BatteryUsedPctCalc = nil` (the account's first-ever snapshot).
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, start, end)` is
  called (`prev` present so the loop still reaches `i=1`; `prev` itself is index 0, never
  emitted regardless).
- **Then** the returned slice is empty — no entry for `cur`'s day at all, not an entry
  with `Flagged = false` and a zero `ConsumedPct`.

**(g) Multi-day span (D8) — one bar, intervening days absent, charges summed regardless**
- **Given** `prev` (effective day 2026-08-10, i.e. `CapturedDate = 2026-08-11`, CapturedAt
  `T0`), `cur` (effective day 2026-08-13, i.e. `CapturedDate = 2026-08-14`, CapturedAt `T1`,
  `DaysSpannedCalc = 3`, `BatteryUsedPctCalc = -30`).
- **And** two charge events inside `[T0, T1)` — one Supercharger session (+15) and one
  manual entry dated 2026-08-12 with real percentages (+25) — summing to +40.
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, entries,
  2026-08-01, 2026-08-31)` is called.
- **Then** exactly **one** entry is returned (not three): `Date = 2026-08-13`,
  `DaysSpanned = 3`, `ConsumedPct = -30 + 40 = 10`, `Flagged = false`. No entry exists for
  2026-08-11 or 2026-08-12 (no snapshot rows for those days in the input at all — nothing
  in `deriveConsumedByDay` needs to special-case them).

**(h) Supercharger session boundary — just inside vs. just outside the interval (D12)**
- **Given** `prev.CapturedAt = T0`, `cur.CapturedAt = T1`.
- **And** session A with `ChargeStopDateTime == T0` (exactly the lower bound) and session B
  with `ChargeStopDateTime == T1` (exactly the upper bound).
- **When** `sumSuperchargerPctBetween(sessions, T0, T1)` is called directly.
- **Then** session A's delta **is** included (`[from, to)` is lower-inclusive) and session
  B's delta is **not** (upper-exclusive — it belongs to the *next* row pair instead).

**(i) Statelessness / the D7b delete-on-resolve precondition**
- **Given** the same `prev`/`cur` pair with `BatteryUsedPctCalc = -10` and no charge data
  (→ `Flagged = true`, per (c)).
- **When** `deriveConsumedByDay` is called again with the identical snapshots but now a
  manual entry contributing +10 added to `entries` (the user backfilled the missing
  record).
- **Then** the second call's `Flagged = false`, `ConsumedPct = 0` (assuming distance ≤ 10)
  or a legitimately non-negative value — proving the derivation is a pure function of its
  inputs with no memory of the first call (D2). This is the precondition that makes
  `telemetry.GapWriter.ReconcileWindow`'s own delete-on-resolve lifecycle (D7b, already
  tested in tier 1) work for free at the `cmd/poller` layer: the next nightly run's
  `flagged` slice simply omits this day, and `ReconcileWindow` deletes the row.

**(m) D18 REGRESSION — the bucket day comes from `CapturedDate`, never from `EffectiveDate`**

This is the scenario that fails under the overruled D-B7 and passes under the new one. It is
the single binding test for the owner's D18 ruling; do not weaken it.

The fixture is the D-B7 scenario-3 edge (a capture taken in the Bogota evening, so its
Bogota day and its UTC day differ):

| | `CapturedAt` | Bogota wall clock | `CapturedDate` | zoned effective day | UTC `EffectiveDate` day |
|---|---|---|---|---|---|
| `prev` | `2026-08-12T08:30:00Z` | Aug 12, 03:30 | `2026-08-12` | `2026-08-11` | Aug 11 |
| `cur` | `2026-08-14T01:00:00Z` | Aug 13, 20:00 | `2026-08-13` | **`2026-08-12`** | **Aug 13** |

- **Given** those two snapshots, with `cur.DaysSpannedCalc = 1` (consistent:
  `2026-08-13 − 2026-08-12 = 1` day) and `cur.BatteryUsedPctCalc = 20`.
- **And** `cur.EffectiveDate` explicitly set to `2026-08-13T01:00:00Z` — the value
  `telemetry/mapping.go:119` really would produce (`CapturedAt − 1 day`, UTC), i.e. the day
  the overruled D-B7 would have bucketed under. This is the ONE fixture in the contract that
  populates `EffectiveDate`, and it is populated precisely so a regression can be caught.
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, 2026-08-01,
  2026-08-31)` is called.
- **Then** exactly one entry is returned with `ConsumedPct = 20`, `Flagged = false`, and:
  - `Date == 2026-08-12` (the zoned answer, `CapturedDate − 1 day`), **and**
  - `Date != 2026-08-13` (the UTC answer `EffectiveDate` carries).

  Assert **both**. The negative assertion is the whole point: the two values coincide for
  every nominal 03:30 capture, so a test built only on nominal fixtures passes under either
  rule and proves nothing about D18.
- **And** assert the D-B12 identity on the same fixtures:
  `effectiveDay(cur)` minus `effectiveDay(prev)` equals `*cur.DaysSpannedCalc` whole days —
  here `2026-08-12 − 2026-08-11 = 1 day = 1`. ✓ It holds because both sides reduce to
  `cur.CapturedDate − prev.CapturedDate`; under the overruled UTC rule the same fixtures give
  `2026-08-13 − 2026-08-11 = 2 days ≠ 1`, so this assertion is a second, independent guard on
  the same regression.

**(n) Zone-shifted row at the window's upper edge is emitted, not dropped (D-B13)**

- **Given** `end = 2026-08-20`, and a `cur` row with `CapturedAt = 2026-08-22T02:00:00Z`
  (21:00 Bogota Aug 21) so `CapturedDate = 2026-08-21` and its zoned effective day is
  **2026-08-20** — inside the window — while its UTC `EffectiveDate` day would be
  `2026-08-21`, outside it.
- **When** `deriveConsumedByDay` is called with `start = 2026-08-01`, `end = 2026-08-20` and
  a valid predecessor.
- **Then** the row **is** emitted with `Date = 2026-08-20`. (This is why `ConsumedByDay`
  fetches snapshots through `end+1` — test (j) asserts the fetch; this asserts the filter
  keeps the row the wider fetch brought back.)

### Port-wiring tests (`reader_test.go`, extended) — fakes, no arithmetic re-verification

These prove `ConsumedByDay` fetches the right windows and propagates arguments/errors
correctly; the arithmetic itself is already proven by (a)–(i) above, so these use trivial
fixtures.

**(j) Fetch windows: every port over-fetched per D-B13** *(expected values CHANGED by D18 —
all three differ from this design's pre-D18 revision)*
- **Given** `start = 2026-08-10`, `end = 2026-08-20`.
- **When** `(*reader).ConsumedByDay(ctx, accountID, teslaID, start, end)` is called.
- **Then** the fake `telemetry.Reader` recorded `SnapshotsByVehicleBetween` called with
  `(2026-08-09, 2026-08-21)` — `start−1` lookback (D9a) and `end+1` zone tail; the fake
  `SuperchargerReader` recorded `SuperchargerSessionsByVehicleBetween` called with
  `(2026-08-09, 2026-08-22)` — `end+2` tail; the fake `manualcharge.Reader` recorded
  `ListEntriesByVehicleBetween` called with `(2026-08-09, 2026-08-20)` — `start−1` lookback,
  no tail.

**(k) accountID/teslaID scoping reaches all three ports** — mirrors
`TestRecentEfficiency_AccountIDScoping_PassedToEveryPort`'s existing pattern, for the
three ports `ConsumedByDay` actually calls.

**(l) Error propagation** — one subtest per port (`telemetry`, `supercharger`, `manual`):
an error from any one port is returned via `errors.Is`, unwrapped, matching the existing
`TestRecentEfficiency_*Error_Propagates` pattern.

**Fake updates required (same file, not new fakes):** `fakeTelemetryReader.SnapshotsByVehicleBetween`,
`fakeSuperchargerReader.SuperchargerSessionsByVehicleBetween`, and
`fakeManualReader.ListEntriesByVehicleBetween` currently `panic()` (added defensively by
tiers 1–2's own cross-module fake-update tasks, since `RecentEfficiency` never calls
them). This tier changes all three from a panicking stub to a functional one (record
args, return canned data/err) — safe, because no existing `RecentEfficiency` test
exercises any of the three `...Between` methods, so none of tiers 1–2's tests observe the
change.

---

## Migration Plan (implementation order for this module's worker(s))

1. `internal/battery/battery.go` — `GapReconciliationWindow` const,
   `Reader.ConsumedByDay` method addition, `DayConsumption` type. No dependencies. Breaks
   `go build ./...` until step 3 lands (expected, mirrors tiers 1–2's own precedent).
2. `internal/battery/consumed.go` (new file) — `calendarDay`, `effectiveDay` (D-B7/D18 —
   these REPLACE the `dayUTC` helper this design specified before the owner's ruling),
   `minFlagDistanceKm`,
   `deriveConsumedByDay`, `sumSuperchargerPctBetween`, `inferMissingChargingType`,
   `sumManualPctBetween`. Depends on step 1 (`DayConsumption` type must exist).
3. `internal/battery/reader.go` — `(*reader).ConsumedByDay`. Depends on steps 1–2.
4. `internal/battery/consumed_test.go` (new file) — test-contract scenarios (a)–(i).
   Depends on step 2 (pure functions, no fakes).
5. `internal/battery/reader_test.go` (extended) — un-panic the three `...Between` fakes;
   add scenarios (j)–(l). Depends on step 3.
6. `internal/battery/AGENTS.md` — document `ConsumedByDay`/`DayConsumption` in the
   module's "Public interface" section. Depends on step 1 (interface must be final).
7. Verification: `go build ./...`, `go vet ./...`, `gofmt -l`,
   `openspec validate RM28-battery-derive-consumed-per-day --strict`.

**LEADER-OWNED, separate task, after this module's work lands:** `cmd/poller/main.go`
wiring per the "`cmd/poller` wiring" section above — outside this module's sandbox.

Steps 1→2→3 are a strict chain (same reason tiers 1–2's own migration plans chain their
type/port additions before their implementations: the type must exist before the pure
functions can reference it, and the pure functions must exist before the port method can
call them). Steps 4 and 6 can run in parallel with each other once their own single
dependency (2, 1 respectively) lands, since they touch disjoint files. Step 5 depends on 3
only.

## Risks / Trade-offs

- **D-B4's window-boundary skip is a narrow, accepted precision gap**, not a bug: a
  pre-existing data gap crossing exactly `start−1` causes one day to be silently absent
  from that specific call's output, self-correcting for any window whose `start−1` does
  have a snapshot. Accepted for the same reason D5a itself is accepted — never fabricate
  an interval bound this module cannot actually observe.
- **The Supercharger tail over-fetch (D-B5) reads one extra day's rows on every call.**
  Negligible: Supercharger session volume per vehicle is bounded by real-world charging
  frequency, not polling cadence (tier 1's own design.md makes the identical argument for
  why that port carries no defensive `LIMIT`).
- **D14a's blind spot is unchanged and explicitly not addressed here** — see D-B11. Any
  future ticket that wants to close it needs the Supercharger battery-% verification UI
  (backlog entry 11 item 1), out of scope for this entire roadmap.
- **This module and the gateway's existing charts now bucket in different zones** (D-B7,
  owner ruling D18). `internal/battery` buckets in `Config.Location` (UTC−5);
  `internal/gateway/handlers/history.go`'s `effectiveDayUTC` still buckets the odometer and
  battery-level charts in UTC. They agree for every nominal 03:30 capture and disagree by one
  day for a capture landing between 00:00Z and 05:00Z. **The owner accepted this consequence
  explicitly when ruling on D18**; it is inherited by tier 4. Reconciling the two — moving
  the existing charts onto the zoned rule — is a candidate follow-up, deliberately not done
  here (it would touch `internal/gateway`, outside this change's module).
- **`CapturedDate` freezes the bucketing zone at write time** (D-B12). A future change to
  `POLLER_TIMEZONE` would leave historical rows stamped in the old zone. Accepted as strictly
  better than the read-time alternative, which would re-bucket history under a new zone while
  the `captured_date` `UNIQUE` constraint and `DaysSpannedCalc` stayed on the old one —
  manufacturing the two-clock disagreement D6 exists to prevent. Full argument in D-B12.
- **The D18 revision widened all three fetch windows** (D-B13). One extra day of snapshots,
  two of Supercharger sessions, one of manual entries per call. Negligible on a ≤90-day
  window (the snapshot query's own `LIMIT 400` is nowhere near binding), and every widening
  is re-filtered in Go, so it cannot change a result — only the row count read.
