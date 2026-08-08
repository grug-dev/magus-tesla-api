## Context

`telemetry.Snapshot` is the DTO returned by `telemetry.Reader.LatestSnapshotsByAccount` and
`telemetry.Reader.SnapshotsByVehicleSince`. It already carries two time fields:

- `CapturedAt` — the precise read instant (the moment the nightly poller read the vehicle).
- `CapturedDate` — the **calendar** date of `CapturedAt` (UTC midnight), used purely for the
  `UNIQUE (account_id, tesla_id, captured_date)` dedupe constraint so repeated same-day captures
  collapse into one row.

Neither conveys "which day does this snapshot *represent*." The nightly poller runs at ≈03:30 local
and reads the vehicle's accumulated state since the prior capture — so a row captured at
`2026-08-08 03:30` describes `2026-08-07`'s driving/charging. Today every consumer that wants the
"represented" day has to subtract a day from `CapturedAt` itself, with no shared convention.

The single DB→domain mapper `rowToSnapshot` (`internal/telemetry/mapping.go:78`) is where every
`Snapshot` is built on read; the write path (`insertSnapshot`, `service.go:538`) builds the sqlc
params from a `Snapshot` and has no use for a read-derived field.

## Goals / Non-Goals

**Goals:**
- Give `Snapshot` a self-describing "day this row represents" field, computed once, available to
  every read consumer without each re-deriving it.
- Keep the change additive: no DB migration, no method-signature change, no behavior change for
  existing callers.
- Preserve `CapturedAt` as the authoritative "when we read it" — `EffectiveDate` is the derived
  "what day it describes", a separate concept.

**Non-Goals:**
- NOT changing the nightly capture schedule, the dedupe `captured_date`, or any DB schema.
- NOT formatting the date (MM-DD, locale, etc.) — that is a presentation concern owned by the
  gateway in tier 2. `EffectiveDate` is a full `time.Time`, same type as `CapturedAt`.
- NOT touching the write path (no `EffectiveDate` column exists to store).
- NOT changing `CapturedDate`'s role (dedupe) — `EffectiveDate` is a distinct, separate field.

## Decisions

### D1 — Field name: `EffectiveDate` (grill-me outcome)

**Decision:** Name the new field `EffectiveDate` (`time.Time`).

**Alternatives considered (grill-me MAG-6 Q2):**
- `SummaryDate` — rejected: ties the name to the "nightly summary" *batch* role rather than the
  field's semantics; reads awkwardly if a future non-summary consumer appears.
- `CoverageDate` — rejected: "coverage" reads as insurance/time-range to some audiences.
- `ReportedDate` — rejected: "reported" overlaps with "captured" for some readers.
- `DisplayCapturedAt` — rejected: locks the name to a *display* concern; the field is not display-
  formatted, it is a full date object.

**Rationale:** `EffectiveDate` cleanly separates "when we read it" (`CapturedAt`) from "which day
it effectively describes" (`EffectiveDate`), and does not lock the name to the history-graph use
case, so other future consumers of `telemetry.Reader` get a self-describing field.

### D2 — Where the offset lives: telemetry DTO, not gateway (grill-me outcome)

**Decision:** Add `EffectiveDate` to the `telemetry.Snapshot` DTO, populated in `rowToSnapshot`.

**Alternatives considered (grill-me MAG-6 Q1):**
- Gateway-only: compute `CapturedAt - 1d` inline in the history handler when building tooltip/label
  strings. Rejected: the offset is a property of the *data's meaning*, not presentation; keeping it
  in the owning module means all callers stay consistent and the offset can't diverge if another
  consumer surfaces raw `CapturedAt` later.
- Stored DB column written by the nightly batch. Rejected: adds schema migration cost for what is a
  cheap read-time derivation; the offset is deterministic from `CapturedAt` so persisting it
  duplicates information and creates a write-path maintenance burden.

**Rationale:** the offset belongs to the module that owns `CapturedAt`. This is what makes tier 2
(a pure gateway/presentation change) possible without re-deriving dates in markup.

### D3 — Preserve full `time.Time`, no format baked in (grill-me outcome)

**Decision:** `EffectiveDate = r.CapturedAt.Time.AddDate(0, 0, -1)` — a full `time.Time` retaining
the time-of-day, same type as `CapturedAt`. NO MM-DD string formatting here.

**Rationale (grill-me MAG-6 Q3):** the user confirmed the backend keeps the same datetime object
shape as `CapturedAt` (including the year); the MM-DD format is a cosmetic change owned by the UI
(gateway) module in tier 2. Keeping the field unformatted preserves reuse — any future caller can
format `EffectiveDate` however it needs without re-parsing a string.

### D4 — Computed in the single read mapper, not per-method

**Decision:** Set `EffectiveDate` inside `rowToSnapshot` (`internal/telemetry/mapping.go`), the one
function both `LatestSnapshotsByAccount` and `SnapshotsByVehicleSince` already use to build a
`Snapshot` from a sqlc `VehicleSnapshot` row.

**Rationale:** single source of truth; both read methods automatically gain the field, and there is
no per-method duplication to forget. The write path (`insertSnapshot`) does not go through
`rowToSnapshot`, so write-built `Snapshot`s simply leave `EffectiveDate` at its zero value — which
is fine because nothing reads it before storage.

## Risks / Trade-offs

- **[Risk] A future caller mistakes `EffectiveDate` for `CapturedDate`.** They differ by intent:
  `CapturedDate` is the capture calendar day (dedupe); `EffectiveDate` is the represented day
  (capture − 1). → **Mitigation:** doc comment on the field (and in `rowToSnapshot`) states the
  semantics and the relationship explicitly, mirroring the existing `CapturedDate` comment block.
- **[Risk] DST / timezone: is "− 1 day" the right offset?** The nightly batch is a calendar-day
  cadence, not a 24-hour interval; using `AddDate(0,0,-1)` (calendar-day arithmetic) rather than
  `Add(-24*time.Hour)` (duration arithmetic) keeps it a calendar-day offset regardless of DST.
  → **Mitigation:** use `AddDate(0,0,-1)`, documented.
- **[Trade-off] Every read row pays one `AddDate` call.** Cheaper than the `pgtype`→`time.Time`
  conversions already happening on the same struct; negligible on the hot paths. Accepted.
- **[Trade-off] Write-path `Snapshot`s carry a zero `EffectiveDate`.** Acceptable: nothing reads it
  on the write path, and the zero value is never persisted (no column). Documented in the field
  comment so a future maintainer doesn't assume it's populated pre-store.

## Migration Plan

None. No schema change, no migration, no config. The change is purely Go code in the telemetry
module. Deploy and existing readers automatically start seeing `EffectiveDate` on returned
`Snapshot`s; existing callers that ignore the field are unaffected.

## Open Questions

None. All three design questions (D1 name, D2 location, D3 type/format) were resolved in the
grill-me interview with the user (Linear MAG-6).