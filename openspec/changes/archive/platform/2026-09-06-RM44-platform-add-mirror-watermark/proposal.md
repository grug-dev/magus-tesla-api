Source: MAG-48 — https://linear.app/magus-monitor/issue/MAG-48/nightly-reconcile-recalculates-the-whole-vehicle-metrics-history-on
Roadmap: openspec/roadmaps/RM44-incremental-supercharger-sync.md
Tier: 4 of 4 (`platform`, cross-cutting — spans `internal/telemetry` and
`internal/charging`; depends on tier 1 (`RM44-telemetry-add-query-logging`),
tier 2 (`RM44-telemetry-add-change-detecting-upsert`), and tier 3
(`RM44-charging-add-change-detecting-mirror`), all archived)
Unit tests: included. The roadmap's D10 confirms this with the user. Test
tasks are listed in tasks.md.

## Why

Tiers 2 and 3 fixed `updated_at` so it means "this row's data changed," not
"the last sync pass touched this row." That stops `internal/analytics` from
recalculating a vehicle's whole history every night.

One thing still does not scale: the mirror READ itself. Every night,
`internal/app.processChargingData` asks `internal/telemetry` for **every**
Supercharger session an account has ever had (`limit 0`, resolved to
`math.MaxInt32`), then mirrors all of them into `internal/charging`. Even
after tiers 2–3, most of those mirrored rows are now no-op upserts — but they
still get READ and still get one upsert statement run against them, every
night, forever. The cost is `users × total history`, not `users × new data`.

This tier bounds that read. `internal/charging` gains a small watermark
table, one row per account, holding the highest `updated_at` telemetry has
reported for that account's Supercharger sessions. The nightly mirror asks
telemetry only for sessions changed since that watermark (minus a 24-hour
overlap for commit skew), mirrors them, then advances the watermark to the
highest `updated_at` it actually saw.

**The one rule that must never break: an empty read must never advance the
watermark to `now()`.** If it did, a session Postgres committed one second
late would sit permanently behind the cursor and never get mirrored again —
silent, permanent data loss. `internal/analytics.Recalculator.Reconcile`
already implements exactly this rule for its own three watermarks. This tier
copies it rather than re-inventing it.

## What Changes

- **`internal/telemetry`** gains one new read method,
  `SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
  ([]SuperchargerHistory, error)`, on the existing `SuperchargerHistoryReader`
  port. Unlike the existing `SuperchargerHistoryByVehicleUpdatedSince`, this
  method takes no `teslaID` and filters on `account_id` alone — so it is the
  only updated-since query that can return a session whose `tesla_id IS
  NULL` (a session for a currently-unregistered vehicle). That gap is why
  this method exists: the per-vehicle method can never return such a row,
  which is exactly the orphan-recovery signal `processChargingData`'s
  existing comment relies on (roadmap D3, D20). One new SQL query, one new
  interface method, one new explicit method on
  `loggingSuperchargerHistoryReader` (tier 1's decorator — no interface
  embedding, per D7).
- **`internal/charging`** gains a new table, `charging.mirror_watermarks`
  (one row per account), and a new port, `MirrorWatermarkStore`, with two
  methods: `MirrorWatermark(ctx, accountID) (time.Time, error)` (a missing
  row returns the epoch — the zero `time.Time` — not an error) and
  `AdvanceMirrorWatermark(ctx, accountID, observed time.Time) error` (an
  upsert). One new migration, two new sqlc queries, one new Go file
  (`mirror_watermark.go`), the interface + constructor declared in
  `charging.go` alongside this module's other ports.
- **`internal/app.processChargingData`** (leader-owned, `internal/app` is not
  a pipeline module worker's sandbox — see "Cross-Module Wiring" in
  design.md) changes from "read everything, every night" to "read the
  cursor, read telemetry bounded by `cursor - 24h`, mirror, advance the
  cursor to the max `updated_at` observed — only when at least one row came
  back." `MirrorSessions`' own signature does not change.
- **Docs**: `internal/charging/AGENTS.md` and `internal/telemetry/AGENTS.md`
  gain sections describing the new port/method (`CLAUDE.md` "docs track
  structural change"). `openspec/specs/telemetry/spec.md`,
  `openspec/specs/charging/spec.md`, and
  `openspec/specs/charge-session-log/spec.md` each gain one ADDED
  requirement — see "Capabilities" below.

**NOT in this change:**
- `internal/analytics` — untouched, per roadmap D1.
- `internal/app`'s actual wiring edit — the leader implements it directly,
  since a pipeline worker is sandboxed to one module and this composition
  spans two ports from two different modules plus the orchestration file
  itself, which belongs to neither. Design.md gives the exact shape so the
  leader does not have to re-derive it.
- Any UI, route, or `internal/gateway` change.

## Breaking

**Not breaking for any existing caller of a stable signature.**
`charging.SessionWriter.MirrorSessions`, `SessionReader`, `SessionVerifier`,
and every `manual_charge_entries` port keep their exact current signatures.

**It IS a compile-time break for any type that fully implements
`telemetry.SuperchargerHistoryReader` by listing every method** (Go has no
`implements` keyword, so adding an interface method breaks only concrete
implementers, not callers that merely hold the interface). Two such types
exist in the repo today:
- `internal/telemetry`'s own `fakeQueryLogSCHReader` (query_log_test.go) —
  inside the telemetry worker's own sandbox; the telemetry worker adds the
  new method there in the same change.
- `internal/app`'s `fakeSuperchargerHistoryReader` (processor_test.go) —
  **outside both workers' sandboxes.** This is a finding for the leader:
  see design.md "Findings" — this file must gain the new method too, in the
  same change, or `internal/app`'s existing tests stop compiling.

## Modules Affected

- **`internal/telemetry/`** — one read method, one SQL query, one decorator
  method, `AGENTS.md`.
- **`internal/charging/`** — one migration, one table, one port, one Go
  file, `AGENTS.md`.
- **`internal/app/`** — leader-owned wiring edit to `processChargingData`,
  plus the `fakeSuperchargerHistoryReader` fix above. Not a pipeline worker
  task for either module worker on this change.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

Both new reads run only inside the **nightly batch**
(`internal/app.processChargingData`), never on a dashboard request path —
`ai/architecture.md` §7's Performance-Profile gives this path latitude a
user-facing read does not get. `SuperchargerHistoryByAccountUpdatedSince`
is served by ONE new index this change adds,
`idx_supercharger_history_account_updated (account_id, updated_at)` on
`telemetry.supercharger_history`. It matches the query exactly: `account_id`
prunes to the tenant and `updated_at` ASC satisfies both the range predicate
and the `ORDER BY` in one scan, with no sort step. The owner chose this at
the design gate over the design's original no-index recommendation — see
design.md's Index Plan for both positions. `charging.mirror_watermarks` is served entirely by its
own `UNIQUE (account_id)` index — a single-row point lookup, no scan. Neither
read is reachable from `internal/gateway`.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- **`telemetry`** — ADDED requirement: an account-wide Supercharger
  updated-since read port, so a caller who needs every changed session for
  an account (including sessions for a currently-unregistered vehicle) does
  not have to enumerate vehicles first. See `specs/telemetry/spec.md`.
- **`charging`** — ADDED requirement: the module owns a per-account
  Supercharger-mirror watermark table. See `specs/charging/spec.md`.
- **`charge-session-log`** — ADDED requirement: the Supercharger mirror
  synchronization is bounded by that watermark, with the empty-read-never-
  advances rule (D5) as its central guarantee. See
  `specs/charge-session-log/spec.md`.

## Testing

Per the Test-Execution-Policy: each worker writes tests for their own module
and runs `go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make
vet`, `make bins`, `make migration-guard` (charging worker — new migration),
and `make boundary-guard` — never `go test ./...`, `make test`, `make
test-with-db`, or `make check`. The owner runs `go test
./internal/telemetry/... ./internal/charging/...` and, once the leader wires
`internal/app`, `go test ./internal/app/...`, and reports results. Until
then this tier's status is **awaiting-user-verification**, never "done."
Design.md's Test Contract states every expected value before any test is
written.

**Owner-only verification (post-deploy, after the next poller run):** the
query-log lines tiers 1 and this tier add should show, on the second night
after deploy, a `since=` value close to `now() - 24h` rather than an absent
date bound, and a Supercharger session count far below the account's total
history size.

## Resolved decisions

Roadmap D4–D6, D10, and D20–D24 (recorded in this roadmap's
`progress.json`) are carried into this change and not re-opened: the
watermark-table cursor design and its two rejected alternatives (D4), the
never-advance-to-`now()`-on-empty-read rule (D5), the 24-hour overlap guard
(D6), the unit-tests-included instruction (D10), the new telemetry method
and its rationale (D20), the exact table shape (D21), the `internal/app`
orchestration shape and the two new charging port methods (D22), and the
two-module, one-change, `platform`-prefixed shape with its archive location
(D23, revised by D24). This design's own local decisions are numbered D1–Dn
in design.md, independent of the roadmap's numbering.

**This tier trips the `database` design gate** (`openspec/config.yaml` —
"any new/changed database object ... MUST include the full schema, the
rationale ... and an index plan"; `CLAUDE.md` Pipeline config
`Design-Gates: database`). Design.md carries the full schema of
`charging.mirror_watermarks`, both rejected alternatives from roadmap D4, and
an index plan for both the new table and the new telemetry query.
Implementation is blocked until the owner explicitly confirms design.md.
