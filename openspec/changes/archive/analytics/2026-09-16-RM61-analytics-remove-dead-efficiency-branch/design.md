# Design — RM61-analytics-remove-dead-efficiency-branch

> **Design gate: NOT TRIPPED.** This change touches no database object — no table, column,
> index, constraint, view, or migration is added, changed, or dropped. Per `openspec/config.yaml`
> §design, a full DDL/rationale/index-plan document is required only for a change that touches
> one; this one does not, so that section is skipped rather than padded out. Implementation may
> start as soon as the proposal is accepted — no owner design confirmation gate applies.

Source ticket: MAG-40 · Roadmap: `openspec/roadmaps/RM61-manual-charge-start-derivation.md`,
tier 3 of 3. The roadmap's decision **RD9** is binding and is not re-opened here: delete the
dead efficiency branch instead of building the capacity port RD8 first planned. Where this
document says something the roadmap does not, it says so explicitly (**D1**–**D3** below).

---

## Context

Facts read from the repository on 2026-09-16, not recalled.

1. **`RecentEfficiency` has no real caller.** The grep in proposal.md §"Verification" is the
   proof this whole tier rests on. It is repeated verbatim in §"Test Contract" below as the
   post-deletion expected result, so the same command that motivated the deletion also verifies
   it landed correctly.
2. **`RecentEfficiency` is the only method reading six of the `reader` struct's seven fields.**
   `reader.go`'s `reader` struct today:
   ```go
   type reader struct {
       metrics      vehicleMetricsStore
       telemetry    telemetry.Reader
       supercharger charging.SuperchargerSessionAnalyticsReader
       manual       charging.Reader
       account      vehicleLookup
       window       time.Duration
       now          func() time.Time
   }
   ```
   `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`, and `LatestMetricsForVehicles`
   read only `r.metrics`. `telemetry`, `supercharger`, `manual`, `account`, `window`, and `now`
   are read exclusively inside `RecentEfficiency` (directly, or through `carTypeFor`,
   `sumSuperchargerKWh`, `sumManualKWh`). Deleting `RecentEfficiency` deletes every reader of
   those six fields, so the struct correctly collapses to one field.
3. **`NewRecalculator` does not share `NewReader`'s parameters.** It is declared and used
   separately (`recalculate.go`), taking its own `pool`, `telemetry.Reader`,
   `charging.SuperchargerSessionAnalyticsReader`, `charging.Reader` — none of which come from
   the `reader` struct this change shrinks. `Recalculate`/`Reconcile` are unaffected in every
   way; this change does not touch `recalculate.go`.
4. **`internal/account` is imported by exactly one file in this module: `reader.go`.**
   (`grep -rln "internal/account\"" internal/analytics/*.go` outside `_test.go` returns only
   `reader.go`.) The only symbol it uses from `account` is `account.Vehicle`, consumed by the
   `vehicleLookup` interface and `carTypeFor` — both deleted by this change. After this change,
   `internal/analytics` imports `internal/account` nowhere at all (`_test.go` files included —
   `reader_test.go`'s `fakeVehicleLookup`/`account.Vehicle` usage is deleted in the same
   change; `db_integration_test.go`'s one `&fakeVehicleLookup{}` construction is fixed as part
   of the same edit that shrinks its `newRealReader` helper — see task 1.4).
5. **One test file outside `reader_test.go`/`derive_test.go` mentions `RecentEfficiency`:**
   `db_integration_test.go:1249`, a doc comment on `recordingSuperchargerReader` contrasting it
   with `reader_test.go`'s `fakeSuperchargerReader` ("which exists to test `RecentEfficiency`'s
   own call shape"). This is prose, not code — it compiles either way — but it goes stale the
   moment `fakeSuperchargerReader` is deleted, so it is corrected in the same change (task 1.4).
6. **`db_integration_test.go`'s `newRealReader` helper calls the six-argument `NewReader`.**
   (`internal/analytics/db_integration_test.go:811`). Ten `Test*` functions in that file call
   `newRealReader(pool)`. This helper must shrink to `NewReader(pool)` in the same commit that
   shrinks `NewReader` itself, or the package fails to compile. This file is **inside**
   `internal/analytics/`, so it is the analytics worker's task, not the leader's — unlike the
   two files in `internal/gateway`/`internal/app` that the roadmap explicitly calls
   leader-owned because they sit outside this module's sandbox.
7. **`recalculate.go:33`'s comment references `chargingSourceLimit` by name**, only to say its
   own `recalcOverlap` constant "mirrors reader.go's own `chargingSourceLimit` named-constant
   convention." This is a stale cross-reference once `chargingSourceLimit` is deleted — fixed
   in the same change (task 1.5) by describing the convention without naming a constant that no
   longer exists.
8. **No `.templ` file and no non-test `internal/gateway` file names `RecentEfficiency`, `Efficiency`, or `analytics.DefaultWindow`** — confirmed by the same grep. The gateway's history
   fragment and dashboard already read only `ConsumedByDay`/`OdometerDeltaByDay`/
   `BatteryLevelByDay`/`LatestMetricsForVehicles`.

## Decisions

**D1 — Delete `sumSuperchargerKWh` and `sumManualKWh`, not just the four items the roadmap
tier description names.** The roadmap's tier-3 row lists `RecentEfficiency`, `Efficiency`,
`derive.go`, `capacity.go`, `carTypeFor`, `vehicleLookup`, `DefaultWindow`,
`chargingSourceLimit` by name but does not mention `sumSuperchargerKWh`/`sumManualKWh`. Reading
`reader.go` shows both functions have exactly one caller — `RecentEfficiency` — and no other
file references either name. Leaving them in place after `RecentEfficiency` is deleted would
leave two unreachable functions that `make lint`'s `unused` check (`golangci-lint`,
`ai/go-conventions.md` §Testing) would flag on the very next run. Deleting them is required to
leave the module in the lint-clean state this proposal's §Breaking claims, not an expansion of
scope.

**D2 — The package doc comment at the top of `analytics.go` is rewritten, not left stale.**
It currently opens: *"its one metric today is a rolling energy-per-kilometre (Wh/km) efficiency
figure over a fixed window, derived from internal/telemetry's snapshot history plus the two
charging-cost sources... corrected for pack capacity via a small in-package reference table
keyed on the vehicle's car_type."* Every clause of that sentence describes code this change
deletes. Leaving it in place would make the package's own top-of-file documentation actively
wrong about what the module does — worse than silence, because a reader trusts a package doc
comment by default. The new text names the module's real, surviving responsibility: deriving
and serving `vehicle_metrics` (the four `Reader` methods that remain) — see task 2.1 for the
exact wording.

**D3 — `internal/analytics/AGENTS.md`'s "Responsibility" section and "Public interface" table
are corrected, not left to imply a capability that no longer exists.** Its current text ("Its
first (and currently only) metric is rolling energy-per-kilometre...with a pack-capacity
correction...") and its `Reader` method table's `RecentEfficiency` row both describe deleted
code. `CLAUDE.md` §Non-negotiables "Docs track structural change" makes this the same change's
responsibility, not a follow-up. See task 3.1 for the exact rewrite.

## Spec delta

Full requirement text lives in this change's own `specs/analytics/spec.md`. Summary:

| Requirement | Action | Why |
|---|---|---|
| Recent Energy-Per-Kilometre Derivation | **REMOVED** | Describes `RecentEfficiency`'s algorithm end to end. Deleted with the code. |
| Unknown Pack Capacity Yields an Approximate Value, Never a Blank Tile | **REMOVED** | Describes `Efficiency.Approximate`, a field of the deleted struct. |
| Insufficient Data Returns ok=false, Never a Fabricated Value | **REMOVED** | Describes `RecentEfficiency`'s own three `ok=false` cases (window/snapshot count, odometer, net energy) — all `RecentEfficiency`-specific, not shared with any surviving method. |
| No Cross-Module Database Access | **MODIFIED** | Its scenario currently claims the module imports "the public `Service` interface of `internal/account`." After this change it imports no `internal/account` symbol at all (Context fact 4). The claim about never importing `internal/telemetry/db`/`internal/charging/db`/`internal/account/db` stays true and stays in the requirement. |
| Module-Scoped Database Schema | **MODIFIED** | One scenario ("The module's public interface is unaffected by the schema move") lists `RecentEfficiency` as an example method alongside `ConsumedByDay`, `OdometerDeltaByDay`, etc. `RecentEfficiency` is dropped from that example list; the requirement itself (about the schema-move migration) is otherwise untouched — this is a historical requirement about a past migration, and its outcome is unaffected by this change. |

Every other requirement in `openspec/specs/analytics/spec.md` — Per-Day Battery-Consumed
Derivation, Gap Detection, Precomputed Daily Vehicle Metrics, the watermark/reconciliation
requirements, and all the rest — is untouched. None of them mention `RecentEfficiency`,
`Efficiency`, `deriveEfficiency`, `socReadings`, `capacityFor`, `carTypeFor`, `vehicleLookup`,
`DefaultWindow`, or `chargingSourceLimit`.

## No Database Changes

Stated explicitly per this dispatch's instruction, so a reviewer does not have to re-derive it:
this change adds, modifies, or drops **no** table, column, index, constraint, view, or
migration. `internal/analytics/db/` (the `analyticsdb` sqlc package and its goose migrations)
is untouched. `make sqlc` does not need to run for this change.

## Test Contract

Fixed here, before implementation, per `ai/go-conventions.md` §Testing "author their expected
values up front." Implementation is verified against this contract, not the reverse.

**T1 — the post-deletion grep returns no production hit.**
```
grep -rn RecentEfficiency --include="*.go" --include="*.templ" internal/ cmd/
```
Expected: **zero matches.** Every occurrence this proposal's §Verification catalogued is either
deleted outright (`reader.go`, `analytics.go`, `reader_test.go`, `derive.go`, `derive_test.go`)
or edited to remove the name (`db_integration_test.go`'s stale doc-comment cross-reference,
`cmd/web/main.go` and `cmd/poller/main.go`'s doc comments, the two leader-owned test-fake
stubs). A single remaining match after this tier's tasks are all checked off means a task was
skipped, not that the grep is wrong.

**T2 — `NewReader`'s signature.**
```
grep -n "^func NewReader" internal/analytics/reader.go
```
Expected: `func NewReader(pool *pgxpool.Pool) Reader` — exactly one parameter. `NewRecalculator`'s
own declaration in `recalculate.go` is unchanged and out of scope for this check.

**T3 — the `reader` struct has exactly one field.**
```
grep -n "^type reader struct" -A 3 internal/analytics/reader.go
```
Expected:
```go
type reader struct {
	metrics vehicleMetricsStore
}
```

**T4 — `go build ./...` and `go vet ./...` are clean, repo-wide.** Not scoped to
`internal/analytics` alone — proposal.md §Breaking claims the two leader-owned call-site fixes
and the two leader-owned test-fake fixes keep the whole repo compiling. A failure anywhere
outside `internal/analytics` after every task (including the leader-owned ones) is checked off
means one of those four edits was missed or done wrong, not an acceptable side effect.

**T5 — `gofmt -l` reports nothing under `internal/analytics/`.**

**T6 — `make lint` reports no `unused` finding under `internal/analytics/`** — the direct check
that D1's `sumSuperchargerKWh`/`sumManualKWh` deletion (and every other now-unreachable helper)
actually left no dead code behind.

**T7 — no surviving test in `internal/analytics` references a deleted symbol.**
```
grep -rn "RecentEfficiency\|Efficiency{\|deriveEfficiency\|socReadings\|capacityFor\|packCapacityKWh\|carTypeFor\|vehicleLookup\|DefaultWindow\|chargingSourceLimit" internal/analytics/*_test.go
```
Expected: **zero matches.** (`Efficiency{` rather than bare `Efficiency` avoids a false hit on
the English word inside an unrelated comment, if any exists — none do today, confirmed by
Context fact 1's grep already covering the whole word.)

**T8 — every existing, non-deleted test in `internal/analytics` still compiles and is
unchanged in its own assertions.** Specifically, unaffected by this change: `TestRecalculate_*`
(`db_integration_test.go`), everything in `recalculate_test.go`, `consumed_test.go`,
`consumption_test.go`, `db_gap_writer_integration_test.go`, and the five surviving
`TestReader_*` functions plus `fakeVehicleMetricsStore` in `reader_test.go` (proposal.md
§"Tests removed" names them explicitly). None of these may change their own expected values as
a side effect of this tier — if one needs to change, that is a finding to report, not a silent
edit.
