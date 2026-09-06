Source: MAG-48 — https://linear.app/magus-monitor/issue/MAG-48/nightly-reconcile-recalculates-the-whole-vehicle-metrics-history-on
Roadmap: openspec/roadmaps/RM44-incremental-supercharger-sync.md
Tier: 3 of 4 (`charging`; depends on tier 1, `RM44-telemetry-add-query-logging`,
already archived)
Unit tests: included. The roadmap's D10 confirms this with the user. Test tasks
are listed in tasks.md.

## Why

Every night, the poller re-mirrors every Supercharger session into
`charging.supercharger_sessions`. Today it sets `updated_at = now()` on every
session, every night, even when nothing about the session changed.

`internal/analytics` reads this column to decide which vehicles need their
metrics recalculated. Because `updated_at` always looks "just changed,"
analytics recalculates every vehicle's full history every night.

Measured on 2026-09-05: one vehicle with sessions rewrote 51 of 51 metric
rows over a 70-day window. A vehicle needed only 4 days of new data. The cost
grows with total history, not with new data — and it will only get worse as
more history builds up.

This tier fixes layer 2 of the three-layer cascade the roadmap describes (see
`openspec/roadmaps/RM44-incremental-supercharger-sync.md` §Decisions, D1–D3).
Tier 1 already fixed the same problem in `internal/telemetry`, one layer
upstream. **This is the tier that actually stops the blow-up**, because
`internal/analytics` reads `charging.supercharger_sessions.updated_at`
directly, not telemetry's copy.

## What Changes

- **`internal/charging/db/query.sql`** — `MirrorSuperchargerSession`'s
  `ON CONFLICT DO UPDATE SET` clause changes so `updated_at` only advances
  when the row's real data changed. The comparison is structural: it builds
  a JSON snapshot of the whole row, drops a fixed list of columns, and
  compares before-and-after. See design.md for the exact SQL.
- **The query's doc comment is rewritten.** Today it says the opposite of
  what this change does — it explains why a change-detecting comparison was
  rejected. That reasoning no longer holds; the comment must say why the new
  design fixes what it warned about, not just delete the old warning.
- **A new deny-list of 14 columns** the comparison ignores. The rule is
  simple: the comparison covers exactly the 5 columns the `SET` clause
  writes, so the deny-list is every other column in the table. Full list
  and per-column reason in design.md.
- **A new self-checking database test.** It reads the table's real columns
  at runtime and checks every column is either compared or explicitly
  excluded. If a future migration adds a column and nobody updates the
  deny-list, this test fails — not a silent regression.
- **Regenerate `internal/charging/db/{models.go,query.sql.go}`** via
  `make sqlc`. The leader already confirmed with sqlc v1.31.1 that this
  exact SQL shape parses cleanly and `MirrorSuperchargerSessionParams` does
  not change — so no Go call site changes.
- **`internal/charging/AGENTS.md`** — the section describing the refresh set
  and the "NO WHERE PREDICATE" reasoning is rewritten to match the new
  behavior (`CLAUDE.md` "docs track structural change").
- **`openspec/specs/charge-session-log/spec.md`** — one MODIFIED
  requirement, describing the new rule in plain capability terms: a sync
  pass that changes nothing does not count as a modification.

**NOT in this change:**
- `internal/telemetry` — tier 1's own change, already archived.
- `internal/analytics` — its read logic is already correct (roadmap D1); it
  is only ever fed bad data today. No change needed here.
- The watermark/bounded-read work — tier 4, depends on this tier.
- Anything in `internal/gateway` — no HTML, no route, no UI in this module.

## Breaking

**No.** `MirrorSuperchargerSessionParams` is unchanged — confirmed by the
leader running `sqlc generate` against the exact SQL this change ships.
`charging.SessionMirror`, `SessionWriter`, `SessionReader`, and
`SessionVerifier` keep their current signatures. The only external effect is
that `updated_at` now moves less often — a behavior a caller could observe,
but no interface changes shape.

## Modules Affected

- **`internal/charging/`** — the only code module touched: the migration-free
  query change (no schema change, no new migration), `AGENTS.md`, a new test
  file.
- **`openspec/specs/charge-session-log/spec.md`** — spec delta, not a code
  module.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

`internal/analytics.Recalculator.Reconcile` reads
`SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince`, which
filters on `charging.supercharger_sessions.updated_at`. This change is what
makes that filter useful: after this lands, an unchanged session no longer
appears in that read's result, so `Reconcile`'s own recalculation window
narrows to real changes instead of the whole account history. No new query,
no new index — the existing `idx_supercharger_sessions_vehicle_stop` index
still drives the scan; see design.md's Index Plan.

## Capabilities

### Modified Capabilities

- **Charge Sessions Are Retrievable For A Vehicle By Recency Of Update
  (`charge-session-log`)** — a sync pass that leaves a record's data
  unchanged no longer counts as a modification of that record. See
  `specs/charge-session-log/spec.md`.

## Testing

Per the Test-Execution-Policy: the implementing worker writes tests and runs
`go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`,
`make bins`, and the standalone guards relevant to this change (no schema
change, so `make migration-guard` is a formality; `make boundary-guard` still
applies) — never `go test ./...`, `make test`, `make test-with-db`, or
`make check`. The owner runs `go test ./internal/charging/...` and reports
results; until then this tier's status is **awaiting-user-verification**,
never "done." Design.md's Test Contract states every expected value before
any test is written.

**Owner-only verification (post-deploy, after the next poller run):** confirm
via the query-log lines tier 1 added that a re-sync of an unchanged account
logs no rows with a new `updated_at`, and that `SELECT updated_at, count(*)
FROM charging.supercharger_sessions GROUP BY updated_at` no longer shows one
timestamp shared by every row after a night with no real changes.

## Resolved decisions

Roadmap D1–D3 and D10 are carried from the roadmap and not re-opened here:
the cascade shape (D1), the meaning of `updated_at` (D2), and the structural
deny-list requirement with its two load-bearing exclusion rules — `tesla_id`
must be compared, the four human-owned columns must not (D3) — plus D10's
"unit tests are included" instruction. This design's own local decisions
(mechanism choice, deny-list contents, the self-checking test's exact shape,
the index plan) are numbered D1–D9 in design.md, independent of the
roadmap's own numbering.

**This tier trips the `database` design gate** (`openspec/config.yaml` —
"any new/changed database object ... MUST include the full schema, the
rationale ... and an index plan"; `CLAUDE.md` Pipeline config
`Design-Gates: database`). This change alters a query's write semantics, not
the table schema — no new migration ships. Design.md still carries the full
SQL, the rationale, and the index plan, per the gate. Implementation is
blocked until the owner explicitly confirms design.md.
