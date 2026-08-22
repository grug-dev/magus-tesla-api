# Tasks — RM29-analytics-own-charge-gaps

Ownership legend: **[module: analytics worker]** — inside `internal/analytics/` only.
**[module: telemetry worker]** — inside `internal/telemetry/` only. **[leader]** —
outside every module's sandbox (a file whose path spans two modules, `cmd/`, the
gateway, root docs, `openspec/`). No sub-task below edits both
`internal/telemetry/` and `internal/analytics/`, and no sub-task edits a file outside
its own module. See design.md D1–D3 for the rationale behind each group.

**Hard ordering constraint — this is an ATOMIC wave, not a phased one (design.md
"Risks").** Unlike tier 4 (which could add the new telemetry port *before* removing
the old columns, keeping the tree green at every intermediate commit), this change
has no safe intermediate compiling state:

- `internal/analytics/db/query.sql` cannot reference `charge_gaps` — and `make sqlc`
  cannot regenerate `analyticsdb` with the new query methods — until the migration
  that creates the table sits inside `internal/analytics/db/migrations/` (sqlc
  validates a module's queries against that module's own schema directory,
  `sqlc.yaml`). The instant the migration file leaves `internal/telemetry/db/migrations/`,
  `internal/telemetry/db/query.sql`'s three `charge_gaps` queries stop validating
  there too — `make sqlc` for the telemetry entry then fails.
- **So: 1.1 (the migration move) must land before 1.2 AND 1.3 can each succeed.**
  1.2 and 1.3 touch disjoint files (`analytics/db/query.sql` vs `telemetry/db/query.sql`)
  so they are `parallel_ok` with each other, but both are blocked on 1.1.
- **Sequencing hazard inside the wave: analytics must GAIN the Go-level types
  (`ChargeGap`, `MissingChargingType`, `GapWriter`, `NewGapWriter`, `gap_writer.go`)
  before telemetry DROPS them, or the tree does not compile in between.** `cmd/poller`
  and `internal/gateway/handlers/history.go` reference the package directly (no
  interface insulates them from which package owns the symbol), so 1.7 (telemetry
  drops), 1.8, 1.9 and 1.10 (leader re-points) are each blocked on 1.4/1.5/1.6
  (analytics gains) having landed first — never the reverse order. 1.10 in
  particular depends on 1.6, not 1.7: it breaks the moment `DayConsumption`'s field
  type changes, before telemetry's symbols are even deleted.
- **The whole group (1.1–1.10) is committed as one unit**, mirroring tier 2's
  identical "no safe intermediate state" call for its own package-rename wave
  (`fe69cc8`). `depends_on` below is for planning and parallel-authoring only — none
  of 1.1–1.9 is individually `go build`-clean until the whole group lands.
- **`make sqlc` runs twice inside this wave**: once for the `analytics` entry (after
  1.2) and once for the `telemetry` entry (after 1.3). Both must be re-run; running
  only one leaves the other module's generated code stale.

---

## Wave 1 — the atomic ownership move (module: analytics worker + module: telemetry worker + leader)

- [x] **1.1** **[leader]** `git mv internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql`.
  **Zero content change** — same filename, same `-- +goose Up`/`-- +goose Down` SQL,
  same comments (design.md D1: goose's global `goose_db_version` table means the
  already-applied version is recognized as applied in its new location; no re-run,
  no data loss). This is a `git mv` (not a worker `mv`) because the leader is the
  only role whose sandbox spans both `internal/telemetry/` and `internal/analytics/`.
  `depends_on`: — · `parallel_ok`: no (blocks 1.2, 1.3)

- [x] **1.2** **[module: analytics worker]** `internal/analytics/db/query.sql` — append
  the three `charge_gaps` queries, moved **verbatim** (identical SQL text and doc
  comments) from `internal/telemetry/db/query.sql`: `UpsertChargeGap :exec`,
  `DeleteChargeGap :exec`, `ChargeGapDatesByVehicleBetween :many`. Run `make sqlc`
  for the analytics entry — this generates `analyticsdb`'s
  `UpsertChargeGapParams`/`DeleteChargeGapParams`/`ChargeGapDatesByVehicleBetweenParams`
  and the `ChargeGapDatesByVehicleBetween` row type. Do not simplify or re-word the
  moved doc comments — they carry load-bearing rationale (e.g. why the read is a
  single-column `SELECT gap_date`, why no new index is needed).
  `depends_on`: 1.1 · `parallel_ok`: with 1.3

- [x] **1.3** **[module: telemetry worker]** `internal/telemetry/db/query.sql` —
  delete the same three queries and their doc comments (design.md's full inventory
  table). Run `make sqlc` for the telemetry entry — `telemetrydb` loses every
  `ChargeGap*` symbol. Verify no other query in this file references
  `charge_gaps` (design.md D1's self-containment check already confirmed none does).
  `depends_on`: 1.1 · `parallel_ok`: with 1.2

- [x] **1.4** **[module: analytics worker]** `internal/analytics/analytics.go` — add,
  moved verbatim from `internal/telemetry/telemetry.go` (identical doc comments,
  updated only where they name the owning/calling module — e.g. "internal/analytics
  computes it, internal/telemetry stores it" becomes a self-description now that
  both live here): `MissingChargingType` (type + its two constants
  `MissingChargingTypeManual`/`MissingChargingTypeSupercharger`), `ChargeGap` (struct,
  all four fields), `GapWriter` (interface, one method `ReconcileWindow`, same
  five-paragraph doc comment), `NewGapWriter(pool *pgxpool.Pool) GapWriter`
  (forward-declaring constructor). `ChargeGap`'s doc comment's "no vendor suffix" line
  and "internal/telemetry never computes one itself" framing should be updated to
  describe analytics computing AND storing it, since this is no longer a two-module
  split.
  `depends_on`: 1.2 · `parallel_ok`: no

- [x] **1.5** **[module: analytics worker]** `internal/analytics/gap_writer.go` (new
  file) — move `internal/telemetry/gap_writer.go`'s `gapWriter` struct,
  `newGapWriter`, the compile-time `var _ GapWriter = (*gapWriter)(nil)` assertion,
  and `ReconcileWindow`'s full implementation (validate-loop, `tx.Begin`,
  `ChargeGapDatesByVehicleBetween` read-and-diff, `DeleteChargeGap` loop,
  `UpsertChargeGap` loop, `tx.Commit`) **verbatim**, retargeted from
  `telemetrydb.Queries`/`telemetrydb.New(pool)` to `analyticsdb.Queries`/
  `analyticsdb.New(pool)`. Reuse this module's existing `dateFrom`
  (`internal/analytics/mapping.go`) for the `Start`/`EndDate`/`GapDate` bindings —
  do NOT add a second `dateFrom` helper; `dateFromPg` already exists here too for the
  existing-dates diff loop. No logic change: same validate-before-transaction order,
  same error-string wording, same single-transaction all-or-nothing guarantee
  (design.md D2, Test Contract).
  `depends_on`: 1.4 · `parallel_ok`: no

- [x] **1.6** **[module: analytics worker]** Drop the now-redundant `telemetry.`
  qualifier on `MissingChargingType` across the module — it is self-referential
  after 1.4:
  - `internal/analytics/analytics.go` — `DayConsumption.MissingChargingType`'s field
    type and its doc comment ("telemetry.MissingChargingType directly" → "the type
    above directly").
  - `internal/analytics/consumed.go` — `inferMissingChargingType`'s parameter and
    return type, and the `ChargeGap`-shaped struct-literal field's type (5
    occurrences per design.md's inventory).
  - `internal/analytics/mapping.go` — `pgTextFromMissingType`/
    `missingChargingTypeFromPg`'s signatures and doc comments; remove the now-unused
    `import "github.com/cristianpena/magus-tesla-api/internal/telemetry"` if this was
    its only remaining use in the file (verify with `go build`/`go vet`, do not
    assume).
  - `internal/analytics/recalculate.go` — the doc-comment mention ("mirrors
    telemetry.MissingChargingType's..."); no code reference in this file (it only
    calls `pgTextFromMissingType`, unqualified).
  - `internal/analytics/consumed_test.go` — five `telemetry.MissingChargingTypeManual`/
    `telemetry.MissingChargingTypeSupercharger` fixture/assertion values (found during
    this artifacts pass, not in the dispatch's binding outcomes — `grep -n
    telemetry.MissingChargingType internal/analytics/consumed_test.go`) →
    `MissingChargingTypeManual`/`MissingChargingTypeSupercharger` (unqualified,
    self-referential). The `internal/telemetry` import stays (still used for
    `telemetry.Snapshot`/`telemetry.SuperchargerSession` fixtures elsewhere in this
    file). No assertion or fixture value change beyond the qualifier.
  - `internal/analytics/db_integration_test.go` — two occurrences, same substitution
    (`grep -n telemetry.MissingChargingType internal/analytics/db_integration_test.go`).
    Import stays for the same reason.
  `depends_on`: 1.4 · `parallel_ok`: with 1.5

- [x] **1.7** **[module: telemetry worker]** `internal/telemetry/telemetry.go` —
  delete `MissingChargingType` (type + both constants), `ChargeGap`, `GapWriter`,
  `NewGapWriter`, and the `"--- charge_gaps ledger ..."` section-header comment
  above them. Delete `internal/telemetry/gap_writer.go` entirely (moved to analytics
  by 1.5). After this task, `grep -rn "MissingChargingType\|ChargeGap\|GapWriter"
  internal/telemetry/*.go` (excluding `_test.go`, handled by Wave 3) must return
  nothing.
  `depends_on`: 1.4, 1.5 (analytics must already define these symbols before
  telemetry stops) · `parallel_ok`: no

- [x] **1.8** **[leader]** `cmd/poller/main.go` — `newNightlyReconciler`'s
  `gapWriter telemetry.GapWriter` parameter becomes `gapWriter analytics.GapWriter`;
  the `var flagged []telemetry.ChargeGap` local and its `telemetry.ChargeGap{...}`
  struct literal become `[]analytics.ChargeGap`/`analytics.ChargeGap{...}`; the call
  site `telemetry.NewGapWriter(pool)` (inside the `reconcilingCollector` construction)
  becomes `analytics.NewGapWriter(pool)`. Update the doc comment above
  `newNightlyReconciler` and the file's own package-level doc comment, both of which
  currently say "hands the flagged days to internal/telemetry's gap writer" /
  "telemetry's `GapWriter`" — reword to "internal/analytics's own gap writer". **No
  other change**: the two-step order, the per-vehicle isolation, and step 2's
  `continue`-on-error behavior are unchanged (design.md D2 — this is a pre-existing
  behavior, not something this tier alters). The `internal/telemetry` import stays
  (still used for `telemetry.NewReader`, `telemetry.NewSuperchargerReader`,
  `telemetry.NewService`, `telemetry.Config`).
  `depends_on`: 1.4, 1.5 · `parallel_ok`: with 1.9

- [x] **1.9** **[leader]** `internal/gateway/handlers/history.go` —
  `chargeTypeLabel`'s parameter `t telemetry.MissingChargingType` becomes
  `t analytics.MissingChargingType`; update its doc comment ("telemetry.
  MissingChargingType is a closed 2-value enum" → "analytics.MissingChargingType is
  a closed 2-value enum"). The file already imports `internal/analytics` (for
  `DayConsumption`) — no new import. The `internal/telemetry` import stays (still
  used for `telemetry.Snapshot` in `buildBatteryChart`/`buildOdometerChart`).
  `depends_on`: 1.4 · `parallel_ok`: with 1.8

- [x] **1.10** **[leader]** `internal/gateway/handlers/history_test.go` — five
  `analytics.DayConsumption{...}` fixture literals set
  `MissingChargingType: telemetry.MissingChargingTypeManual` or
  `telemetry.MissingChargingTypeSupercharger`
  (`TestBuildConsumedChart_FlaggedNonSpan_Manual_HidesValue`,
  `TestBuildConsumedChart_FlaggedNonSpan_Supercharger_HidesValue`, and three others
  per `grep -n MissingChargingType internal/gateway/handlers/history_test.go`) — all
  five become `analytics.MissingChargingTypeManual`/
  `analytics.MissingChargingTypeSupercharger`. This is a **compile dependency of
  1.6, not of 1.7**: `DayConsumption.MissingChargingType`'s field type becomes
  `analytics.MissingChargingType` (self-referential) the moment 1.6 lands, and Go
  does not implicitly convert between two distinct named string types even while
  both `telemetry.MissingChargingType` and `analytics.MissingChargingType` still
  exist side by side — so this task cannot wait for 1.7. The `internal/telemetry`
  import stays (still used for `telemetry.Snapshot` fakes throughout this file, e.g.
  `fakeHistoryReader`). No assertion, fixture value, or test-name change.
  `depends_on`: 1.6 · `parallel_ok`: with 1.8, 1.9

---

## Wave 2 — offline tests: not applicable

`charge_gaps`/`GapWriter` has never had an offline/pure-function test — it is DB-only
by nature (`ReconcileWindow` is a transactional read-diff-write, not a pure
function), mirroring `SuperchargerReader`'s identical precedent noted in
`internal/analytics/AGENTS.md`'s Testing section. There is no early-wave offline
test to author or move for this change. The entire test surface is the
`DATABASE_URL`-gated integration suite moved in Wave 3.

---

## Wave 3 — DB-integration test move (module: analytics worker + module: telemetry worker — DB-gated, FINAL wave)

> Cannot compile before Wave 1 lands in full: the moved test file references
> `ChargeGap`/`MissingChargingType`/`newGapWriter`, which exist in `internal/analytics`
> only after 1.4/1.5.

- [x] **3.1** **[module: analytics worker]** Create
  `internal/analytics/db_gap_writer_integration_test.go` by moving
  `internal/telemetry/db_gap_writer_integration_test.go` **verbatim**, with exactly
  two mechanical changes: (a) `package telemetry` → `package analytics`; (b) every
  occurrence of `_, pool := newTestStore(t)` (7 occurrences, one per test function)
  → `pool := newTestPool(t)` — `internal/analytics/db_integration_test.go`'s
  existing `newTestPool(t) *pgxpool.Pool` helper is this module's equivalent of
  telemetry's `newTestStore` (same skip-when-no-DB behavior, same
  `t.Cleanup(pool.Close)`), and this file only ever used the pool half of
  `newTestStore`'s `(*dbStore, *pgxpool.Pool)` return, never the store. **No other
  line changes** — `ChargeGap`, `MissingChargingType`, `MissingChargingTypeManual`,
  `MissingChargingTypeSupercharger`, and `newGapWriter` are all unqualified
  identifiers that already resolve correctly once the file is `package analytics`
  (they are defined in this package by 1.4/1.5) — no qualifier rewrite needed,
  unlike a cross-package move. All 7 tests, their names, their fixture data
  (account/vehicle IDs, dates, expected row counts), and their helper functions
  (`chargeGapRow`, `fetchChargeGap`, `countChargeGaps`, `cleanupChargeGaps`,
  `cleanupChargeGapsByAccount`) move unchanged — see design.md's Test Contract for
  the full restated list of what each of the 7 tests must still assert.
  `depends_on`: 1.2, 1.4, 1.5, 1.6 (needs `analyticsdb`'s regenerated query methods
  AND the moved Go types to both exist) · `parallel_ok`: no

- [x] **3.2** **[module: telemetry worker]** Delete
  `internal/telemetry/db_gap_writer_integration_test.go`. Confirm 3.1 has landed
  first (its 7 tests, names and assertions intact in `internal/analytics/`) so no
  coverage is lost in the gap, mirroring tier 4's identical "confirm the port task
  landed before deleting" caution (task 4.7 there).
  `depends_on`: 3.1, 1.3, 1.7 · `parallel_ok`: no

---

## Wave 4 — documentation (docs-track-structural-change, same change per CLAUDE.md)

- [x] **4.1** **[module: telemetry worker]** `internal/telemetry/AGENTS.md` — remove
  the `GapWriter` bullet from "Public interface (the port)" (the
  `ReconcileWindow`/`NewGapWriter` paragraph), the `charge_gaps` bullet from "Data
  ownership" (the full column-by-column description), and the "###
  `GapWriter`'s upsert-and-delete lifecycle" subsection immediately below it. Add one
  short sentence in "Data ownership" noting `charge_gaps` moved to
  `internal/analytics` in this change and why (telemetry never read it back — mirrors
  the existing sentence this file already carries for the five dropped `_calc`
  columns, tier 4's precedent one paragraph above it). Do not touch the
  `vehicle_snapshots`/`poll_attempts`/`supercharger_sessions` entries — unaffected.
  `depends_on`: 1.7, 3.2 · `parallel_ok`: with 4.2

- [x] **4.2** **[module: analytics worker]** `internal/analytics/AGENTS.md` — add a
  `GapWriter` entry to "Public interface (the port)" (mirroring the bullet removed
  from telemetry's AGENTS.md in 4.1, reworded to describe analytics as sole
  owner/caller rather than a two-module split — `cmd/poller` is still the only
  caller), and add a `charge_gaps` entry to "Data ownership" alongside
  `vehicle_metrics`/`vehicle_metric_watermarks` (full column-by-column description,
  moved from telemetry's AGENTS.md). Add a short "Testing" note that
  `db_gap_writer_integration_test.go`'s 7 tests are DB-integration-only (no offline
  counterpart — mirrors this file's existing `SuperchargerReader` precedent
  language). No change to "Allowed / forbidden imports" — this move adds no new
  cross-module dependency.
  `depends_on`: 1.4, 1.5, 1.6, 3.1 · `parallel_ok`: with 4.1

- [x] **4.3** **[leader]** `README.md` — three mentions (verified by grep): (1) the
  `internal/analytics` Architecture-table row already says "the gap detection it
  hands to telemetry's `GapWriter`" — reword to "...the gap detection it stores
  through its own `GapWriter`"; (2) the Database-tables-by-module row for
  `charge_gaps` — move it from telemetry's column to analytics' column in whatever
  table structure lists tables per module, and reword "Written by
  internal/analytics through telemetry's `GapWriter` port" → "Written by
  internal/analytics through its own `GapWriter` port"; (3) confirm no other
  `README.md` reference needs updating (no module is added, removed, or renamed by
  this change — only `charge_gaps`' owning column in the existing table changes).
  `depends_on`: Wave 1 landed · `parallel_ok`: with 4.4

- [x] **4.4** **[leader]** `docs/battery-consumed-graph.md` — update every reference
  that names `internal/telemetry` as `charge_gaps`'/`GapWriter`'s owner (verified by
  grep: the module-ownership table row "`charge_gaps` | `internal/telemetry`" →
  "`internal/analytics`"; the sequence-diagram line
  `telemetry.GapWriter.ReconcileWindow(...)` → `analytics.GapWriter.ReconcileWindow(...)`;
  the migration file-path reference
  `internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` →
  `internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql`; "Written by
  internal/analytics through telemetry's GapWriter port" → "through its own
  GapWriter port"; "internal/telemetry/telemetry.go exposes only GapWriter" →
  "internal/analytics/analytics.go exposes only GapWriter"). Do not alter any
  narrative content about the derivation itself (D5/D5a, the 30-day window, the
  lower-bound caveat D14a) — those describe analytics' existing behavior, unchanged
  by this tier.
  `depends_on`: Wave 1 landed · `parallel_ok`: with 4.3

- [x] **4.5** **[leader]** `openspec/roadmaps/RM29-modular-monolith-boundaries.md` —
  flip tier 5's status `[ ]` → `[~]` when these artifacts are created and `[~]` →
  `[x]` at archive; update the "Status" section's "Next unblocked" line. Leader
  bookkeeping, not a worker task; noted here per the roadmap's own status-legend
  convention.
  `depends_on`: — · `parallel_ok`: n/a

---

## Wave 5 — verification (assistant-run signals, then owner-run suite)

- [x] **5.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l .` (expect
  clean); `make build`, `make vet`, `make bins`; `make ui-guard`, `make i18n-guard`,
  `make money-guard` (all three expected to be no-ops — this change touches no
  gateway markup beyond two type-name re-points (1.9, 1.10), no new user-facing
  string, and no monetary column; report that they ran, not that they were
  skipped).
  `depends_on`: Waves 1–4 · `parallel_ok`: no (final gate)

- [x] **5.2** Re-run `make sqlc` one final time for both the `telemetry` and
  `analytics` entries and confirm a clean tree (no diff — everything was already
  regenerated in Wave 1). Then run `make migrate-status` and confirm
  `20260815000002_add_charge_gaps` is listed as applied, read from its NEW path
  under `internal/analytics/db/migrations/`, with nothing pending — the check that
  catches a bad directory move while the build stays green (tier 2's lesson,
  design.md's Risks).
  `depends_on`: 5.1 · `parallel_ok`: no

- [x] **5.3** Run `openspec validate --changes --strict` and report the result
  verbatim.
  `depends_on`: all artifact edits · `parallel_ok`: no

- [ ] **5.4** Hand off to the owner. Exact commands to paste (not run by the
  assistant, per the Test-Execution-Policy):
  ```
  go test ./internal/telemetry/... ./internal/analytics/... ./internal/gateway/... ./cmd/...
  ```
  or the full suite:
  ```
  go test ./...
  ```
  Note for the owner: the DB-backed tests **self-skip** when no Postgres is reachable
  and no Docker daemon can provision one, so a green run does not by itself prove
  Wave 3's moved tests executed. To exercise them, start Docker and run `make test`,
  or run the moved file directly, e.g.
  `env -u DATABASE_URL go test ./internal/analytics/ -run TestGapWriter_ -v`, and
  confirm the output says `PASS` (7 tests, several with sub-tests) rather than
  `SKIP`. Until the owner runs one of these and reports the result, this tier's
  status is **awaiting-user-verification**, never "done" (design.md "Verification
  signals").
  `depends_on`: 5.1, 5.2, 5.3 · `parallel_ok`: no
