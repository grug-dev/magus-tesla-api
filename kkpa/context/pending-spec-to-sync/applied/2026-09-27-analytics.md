# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. Draft of KB edits derived from one approved
> OpenSpec capability spec. Nothing here touches the canonical KB until applied. Self-contained.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    `2026-09-27`
Status: APPLIED 2026-09-27

Delta scope: the one requirement added by `RM69-analytics-add-unfinished-for-date` —
"Unfinished Vehicle Detection For A Date". The How-maintenance-works bullet and the Component
map row were already written by the implementing change; this adds the gotchas and routing.

---

## [guide] ## Conventions & gotchas — APPEND

- **`UnfinishedForDate` means "no `vehicle_metrics` row for that `metric_date`".** An id that was never registered, or never had a snapshot, is returned as unfinished — the read does not check registration.
  _Source: spec analytics — Requirement: Unfinished Vehicle Detection For A Date._
- **It takes plain `[]int64`, not `vehicleref.Ref`, and checks no ownership.** Only server-side jobs that span every account may call it; never call it from the gateway.
  _Source: spec analytics — Requirement: Unfinished Vehicle Detection For A Date._
- **One query for the whole list; empty input returns an empty slice with no query.** Duplicate ids come back once, in ascending `tesla_id` order.
  _Source: spec analytics — Requirement: Unfinished Vehicle Detection For A Date._

## [index] ## Glossary & routing — entities — ADD ROWS

| `unfinished vehicles` | `analytics.UnfinishedReader.UnfinishedForDate` — cars with no `vehicle_metrics` row for a date (the nightly retry's done check) | entity | `entities/vehicle-metrics/guide.md` |
