# Tasks — RM29-charging-rename-from-manualcharge

Ownership legend: **[module: manualcharge worker]** — inside `internal/manualcharge/` (soon
`internal/charging/`), the sandboxed worker's own territory, including its `db/` subfolder.
**[leader]** — outside that module (nine call-site files, `sqlc.yaml`, `Makefile`, `.air.toml`,
root docs, other modules' `AGENTS.md`, the OpenSpec specs folder) — a module worker may not
edit another module's files, so these are leader-owned per the pipeline's sandbox rule. See
design.md D2–D5 for the full rationale behind each group.

**Hard ordering constraint (design.md, Risks):** the package rename (1.x), every call-site
re-point (2.x), and the sqlc/Makefile/.air.toml config re-point (2.x) touch a tree that does
not compile — or, for `make sqlc`/`make db-setup`, does not run correctly — until *all* of them
land together. There is no safe intermediate state between "package manualcharge, nine
importers, sqlc pointed at the old path" and "package charging, nine importers, sqlc pointed at
the new path." Wave 1 and Wave 2 are therefore executed as **one atomic wave**: both must
complete before anything is committed or before `go build ./...`/`make sqlc` is run for
verification. Tasks within the wave are listed with `depends_on` for planning/parallel-authoring
purposes, but none is individually buildable — the wave lands as a unit.

## Wave 1 — module rename (module: manualcharge worker)

> Moves in this wave use plain `mv`, **not** `git mv`: a module worker never runs git (staging
> and commits are the leader's, at wave boundaries). Nothing is lost — git records no renames,
> so `mv` + the leader's `git add -A` yields a commit identical to `git mv`, blame included
> (design.md D2).

- [x] **1.1** Rename `internal/manualcharge/manualcharge.go` → `internal/charging/charging.go`
  via `mv` (creating `internal/charging/` in the process). Update the package clause
  `package manualcharge` → `package charging` and the leading doc comment ("Package
  manualcharge is a new isolated domain module…" → "Package charging is…"). No identifier,
  signature, or logic change — `Entry`, `Writer`, `Reader`, `NewWriter`, `NewReader` all keep
  their exact names (design.md D2, D3).
  `depends_on`: — · `parallel_ok`: yes (own file)

- [x] **1.2** `mv internal/manualcharge/service.go internal/charging/service.go`; package
  clause `manualcharge`→`charging`; every `manualchargedb.*` qualifier (`manualchargedb.CreateEntryParams`,
  `manualchargedb.ManualChargeEntry`, etc. — 13 occurrences per design.md D2) → `chargingdb.*`.
  **Do not rename `ManualChargeEntry` itself** — only the package qualifier in front of it
  changes (design.md D3).
  `depends_on`: 1.6 (needs `chargingdb` package to exist for the qualifier to resolve, but can
  be drafted in parallel — the wave lands atomically) · `parallel_ok`: yes

- [x] **1.3** `mv internal/manualcharge/manualcharge_test.go internal/charging/charging_test.go`;
  external test package clause `package manualcharge_test` → `package charging_test` (this
  module's sanctioned back-edge — `ai/architecture.md` §2 — moves verbatim). No assertion,
  fixture, or test-name change.
  `depends_on`: 1.1 · `parallel_ok`: yes

- [x] **1.4** `mv internal/manualcharge/db_integration_test.go internal/charging/db_integration_test.go`;
  package clause `manualcharge_test`→`charging_test`; every `manualchargedb.*` qualifier →
  `chargingdb.*`. No assertion, fixture, or test-name change.
  `depends_on`: 1.6 · `parallel_ok`: yes

- [x] **1.5** `mv internal/manualcharge/testdb_test.go internal/charging/testdb_test.go`;
  package clause `manualcharge_test`→`charging_test`; update the doc comment's "Package
  manualcharge_test starts…" → "Package charging_test starts…" and its `log.Fatalf` message
  prefixes ("manualcharge testdb: …" → "charging testdb: …", 4 occurrences). **The
  `//go:embed db/migrations/*.sql` directive needs NO text change** — it is relative to the
  package directory and remains correct once the file moves (design.md D2).
  `depends_on`: — · `parallel_ok`: yes

- [x] **1.6** `mv internal/manualcharge/db internal/charging/db` (moves the whole subfolder:
  `db.go`, `models.go`, `query.sql.go`, `query.sql`, and `migrations/` with both `.sql` files
  unchanged). Hand-edit only the `package manualchargedb` clause → `package chargingdb` in
  `db.go`, `models.go`, `query.sql.go`. **Do not rename `ManualChargeEntry`, `CreateEntryParams`,
  `UpdateEntryParams`, `ListEntriesByAccountParams`, `ListEntriesByVehicleParams`,
  `ListEntriesByVehicleBetweenParams`, `DeleteEntryParams`, or `DBTX`/`Queries`/`New`** — every
  one of these is sqlc-generated from the unchanged table/query names and must be regenerated
  identically by `make sqlc` in task 2.6, not hand-renamed (design.md D3, D9). Both migration
  files move with **zero content or filename change** (design.md D2, D7) — `query.sql`'s SQL
  text is likewise unchanged, only its containing folder moves.
  `depends_on`: — · `parallel_ok`: yes

- [x] **1.7** `mv internal/manualcharge/AGENTS.md internal/charging/AGENTS.md`. Update:
  `Agent-Name: manualcharge` → `Agent-Name: charging`; every `internal/manualcharge`/
  `manualcharge.*`/`manualchargedb` reference throughout the file (Responsibility, Public
  interface, Allowed/forbidden imports, Data ownership, Testing sections) →
  `internal/charging`/`charging.*`/`chargingdb`; add one forward-looking sentence to
  Responsibility noting the RM29 scope (this module will also own `charge_sessions`,
  Supercharger-sourced charging data, from tier 6 — a fact the current text does not state
  because it predates MAG-26). Do not otherwise rewrite the file's content or claims — this
  tier changes naming and scope-anticipation only, not the module's actual behavior.
  `depends_on`: — · `parallel_ok`: yes

## Wave 2 — call-site re-point + config re-point (leader — cross-module, same atomic wave as Wave 1)

- [x] **2.1** `cmd/poller/main.go` — re-point the import
  `"github.com/cristianpena/magus-tesla-api/internal/manualcharge"` → `.../internal/charging`;
  update the `manualcharge.NewReader(pool)` call site (line 90) → `charging.NewReader(pool)`.
  `depends_on`: 1.1–1.7 (needs the new package to exist) · `parallel_ok`: with 2.2–2.5, 2.7–2.10

- [x] **2.2** `cmd/web/main.go` — re-point the import; update
  `Deps{ManualChargeWriter: manualcharge.NewWriter(pool), ManualChargeReader: manualcharge.NewReader(pool)}`
  → `Deps{ChargingWriter: charging.NewWriter(pool), ChargingReader: charging.NewReader(pool)}`
  (lines 60-61), and the standalone `manualcharge.NewReader(pool)` call at line 69 →
  `charging.NewReader(pool)`.
  `depends_on`: 1.1–1.7, 2.3 (the `Deps.ChargingWriter/Reader` fields must exist first) ·
  `parallel_ok`: no (ordered after 2.3)

- [x] **2.3** `internal/gateway/gateway.go` — re-point the import; rename the `Deps` struct
  fields `ManualChargeWriter manualcharge.Writer` → `ChargingWriter charging.Writer` and
  `ManualChargeReader manualcharge.Reader` → `ChargingReader charging.Reader` (design.md D3)
  and their two forwarding uses (`ManualChargeWriter: d.ManualChargeWriter` → `ChargingWriter:
  d.ChargingWriter`, same for Reader). Update both fields' doc comments ("ManualChargeWriter is
  the manualcharge write port…" → "ChargingWriter is the charging write port…").
  `depends_on`: 1.1–1.7 · `parallel_ok`: with 2.1, 2.4–2.5, 2.7–2.10

- [x] **2.4** `internal/gateway/handlers/handlers.go` — re-point the import; rename
  `Deps.ManualChargeWriter`/`ManualChargeReader` → `ChargingWriter`/`ChargingReader` and the
  unexported `Handler.manualChargeWriter`/`manualChargeReader` fields (design.md D3 — these two
  lowercase fields are OUTSIDE the leader's original 73-count inventory; do not skip them) →
  `chargingWriter`/`chargingReader`, plus the two forwarding lines in the constructor
  (`manualChargeWriter: d.ManualChargeWriter` → `chargingWriter: d.ChargingWriter`, same for
  Reader). Update the matching doc comments.
  `depends_on`: 1.1–1.7, 2.3 (consumes `Deps.ChargingWriter/Reader`) · `parallel_ok`: no

- [x] **2.5** `internal/gateway/handlers/charges.go` — re-point the import; update every
  `h.manualChargeWriter.*`/`h.manualChargeReader.*` call site (6 occurrences: `Create`,
  `Update`, `Delete`, `ListEntriesByVehicle`, `ListEntriesByAccount` ×2) → `h.chargingWriter.*`/
  `h.chargingReader.*`. **Do NOT rename `csrfManualChargeKey` or its value `"csrf_manualcharge"`**
  — domain vocabulary for the manual-charge-entry CSRF scope, not this module's package name
  (design.md D3).
  `depends_on`: 1.1–1.7, 2.4 (consumes the renamed `Handler.chargingWriter/Reader` fields) ·
  `parallel_ok`: no

- [x] **2.6** `sqlc.yaml` — re-point the `manualcharge` entry's `schema`/`queries` paths to
  `internal/charging/db/migrations`/`internal/charging/db/query.sql`, `out` to
  `internal/charging/db`, `package` to `"chargingdb"`; update the entry's doc-comment block
  (4 refs — design.md D5). Then run `make sqlc` and diff the regenerated
  `internal/charging/db/{db,models,query.sql}.go` against Wave 1's hand-moved versions — expect
  **zero** difference beyond what Wave 1 already hand-edited (design.md D9). If `make sqlc`
  produces any other difference, stop and investigate before proceeding — it means something
  besides the package clause changed, which this tier does not intend.
  `depends_on`: 1.6 (the `db/` folder must physically exist at the new path first) ·
  `parallel_ok`: with 2.1–2.5, 2.7 (but must run BEFORE 2.6's own `make sqlc` step can succeed,
  and BEFORE 5.1's `go build`)

- [x] **2.7** `Makefile` — `MIGRATIONS_DIRS` (line 38): `internal/manualcharge/db/migrations` →
  `internal/charging/db/migrations`.
  `depends_on`: — (independent text edit; must land before Wave 2 is considered complete, since
  it gates `make db-setup`/`make migrate-up` finding this module's migrations) ·
  `parallel_ok`: yes

- [x] **2.8** `.air.toml` — `exclude_dir` (line 28): `internal/manualcharge/db/migrations` →
  `internal/charging/db/migrations`. Functional config, not prose — missed by the leader's
  original inventory (design.md D5).
  `depends_on`: — · `parallel_ok`: yes

- [x] **2.9** `internal/gateway/templates/fragments/charges_vm.go` — re-point the import if
  present, and update the two doc-comment mentions ("no manualcharge.*" → "no charging.*"; "No
  manualcharge.Entry, pgtype, or time.Duration in this struct" → "No charging.Entry, pgtype, or
  time.Duration in this struct"). No view-model field or logic change.
  `depends_on`: 1.1–1.7 · `parallel_ok`: with 2.1–2.5, 2.7–2.8

- [x] **2.10** `internal/analytics/analytics.go`, `consumed.go`, `consumed_test.go`, `reader.go`,
  `reader_test.go` — re-point the import in each; update every `manualcharge.*` qualifier
  (`manualcharge.Entry` in all five files, `manualcharge.Reader` in `reader.go`/`reader_test.go`,
  the `manual manualcharge.Reader` constructor parameter in `reader.go`) → `charging.*`. In
  `reader_test.go`, additionally rename `TestRecentEfficiency_ManualChargeError_Propagates` →
  `TestRecentEfficiency_ChargingError_Propagates`, `TestConsumedByDay_ManualChargeError_Propagates`
  → `TestConsumedByDay_ChargingError_Propagates`, their two doc-comment cross-references (lines
  335, 555), and the fixture strings `errors.New("manualcharge: connection lost")` (2
  occurrences) plus the four `t.Errorf("manualcharge: ...")` prefix strings → `"charging: ..."`
  (design.md D3 same-file-consistency call — these names aren't in the proposal's bullet list
  literally by line number but are stale vocabulary echoes in a file already being edited for
  the qualifier change). No assertion value, fixture data, or test-scenario change beyond these
  name/string substitutions.
  `depends_on`: 1.1–1.7 · `parallel_ok`: with 2.1–2.5, 2.7–2.9

- [x] **2.11** `internal/gateway/handlers/charges_test.go` — re-point the import; update every
  `manualcharge.*` qualifier and every `ManualChargeWriter:`/`ManualChargeReader:` struct-literal
  field usage (14 occurrences across 7 test-setup call sites) → `ChargingWriter:`/
  `ChargingReader:`; update `sess.Set(csrfManualChargeKey, ...)` call sites — **the constant
  name itself is unchanged** (design.md D3), only the file's import/qualifier context around it.
  No assertion, fixture, or test-scenario change.
  `depends_on`: 1.1–1.7, 2.4 · `parallel_ok`: no

- [x] **2.12** `internal/gateway/handlers/charges_error_visibility_test.go` — re-point the
  import; update every `manualcharge.Entry` qualifier (3 occurrences) → `charging.Entry`. No
  assertion or fixture change.
  `depends_on`: 1.1–1.7 · `parallel_ok`: with 2.1–2.5, 2.7–2.10

- [x] **2.13** `internal/gateway/handlers/history_test.go` — update the one stale doc-comment
  mention of "ManualChargeReader" (line 1043, "There is no dedicated forwarding test for
  SuperchargerReader or ManualChargeReader in this suite…") → "ChargingReader". This file's
  primary import is `internal/analytics` (from tier 1), not `internal/manualcharge` — this is a
  prose-only edit, not a qualifier re-point.
  `depends_on`: 2.4 (renames the field this comment refers to) · `parallel_ok`: yes

## Wave 3 — OpenSpec delta specs (leader — same atomic wave, depends on Wave 1/2 landing)

- [x] **3.1** `openspec/changes/RM29-charging-rename-from-manualcharge/specs/gateway/spec.md` —
  author the `## RENAMED Requirements` block (`FROM: `### Requirement: Gateway Imports No
  manualchargedb Package`` / `TO: `### Requirement: Gateway Imports No chargingdb Package``)
  followed by a `## MODIFIED Requirements` block containing the full updated bodies of FIVE
  requirements: "Gateway Imports No chargingdb Package" (addressed by its new, post-rename
  title), "Charge List Fragment", "Create Charge Entry", "Inline Row Editing", "Delete Charge
  Entry" — each copied verbatim from `openspec/specs/gateway/spec.md` with `manualcharge`→
  `charging` and `manualchargedb`→`chargingdb` substituted throughout, EXCEPT the
  `csrf_manualcharge` literal in "Delete Charge Entry"'s CSRF-staleness scenario, which is left
  unchanged (design.md D3, D4). This is not a code file — no module-worker sandbox applies —
  but it is authored by the leader because it depends on the exact identifier renames landing
  in Wave 2 first, to avoid transcription drift.
  `depends_on`: 2.1–2.13 (must describe the POST-rename identifiers) · `parallel_ok`: with 3.2

- [x] **3.2** `openspec/changes/RM29-charging-rename-from-manualcharge/specs/analytics/spec.md`
  — author a `## MODIFIED Requirements` block containing the full updated body of "No
  Cross-Module Database Access" (title unchanged), copied verbatim from
  `openspec/specs/analytics/spec.md` with its two `internal/manualcharge`/
  `internal/manualcharge/db` references → `internal/charging`/`internal/charging/db`.
  `depends_on`: 2.1–2.13 · `parallel_ok`: with 3.1

  (No `specs/manual-charge-log/` delta is produced — design.md D1 verified zero requirement-text
  changes in that capability.)

## Wave 4 — documentation (leader — docs-track-structural-change, same change per CLAUDE.md)

Each of these is independent of the others (disjoint files) and independent of Wave 3, but
depends on Wave 1/2 having landed so the doc content being written describes the post-rename
state truthfully.

- [x] **4.1** `README.md` — sqlc intro paragraph (`manualchargedb` → `chargingdb`; the
  `internal/{account,telemetry,manualcharge}/...` glob → `internal/{account,telemetry,charging}/...`),
  Project Structure tree line, Architecture table row, Dependency graph code block (7
  occurrences across `cmd/web`/`cmd/poller`/`gateway`/`handlers` import lists and the `LAYER 1`
  row `manualcharge ────────────► manualcharge/db` → `charging ────────────► charging/db`), and
  the Database-tables-by-module row. Design.md D5 has the verified line-by-line list.
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes, with 4.2–4.9

- [x] **4.2** `internal/analytics/AGENTS.md` — all 7 `manualcharge`/`manualchargedb` references
  in the Allowed-imports and testing-notes sections → `charging`/`chargingdb` (design.md D5).
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

- [x] **4.3** `internal/gateway/AGENTS.md` — the `Deps.ManualChargeWriter`/`ManualChargeReader`
  bullets and their `internal/manualcharge/db` forbidden-import follow-ons (4), the
  "Exception: user-initiated writes" section body's `manualcharge.Writer` mentions (2) and its
  "Only manualcharge.Writer is permitted" line (1), and the "Exception: language switch"
  section's "D4/manualcharge amendment above" cross-reference (1) → "D4/charging amendment
  above". Do **not** touch the `"csrf_manualcharge"` CSRF-key literal in bullet 3 of the D4
  amendment (design.md D3, D5).
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

- [x] **4.4** `internal/gateway/handlers/lang.go` — line 107's doc comment: "a deliberate,
  user-approved divergence from the manualcharge/D4 write-exception pattern" → "…from the
  charging/D4 write-exception pattern" (design.md D5 — missed by the leader's original
  inventory; cross-references 4.3's renamed label).
  `depends_on`: 4.3 (should read consistently with the label it renamed) · `parallel_ok`: yes

- [x] **4.5** `internal/testdb/testdb.go` — line 2's doc comment: "used by the integration tests
  across modules (account, manualcharge, telemetry)" → "(account, charging, telemetry)"
  (design.md D5 — missed by the leader's original inventory).
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

- [x] **4.6** `docs/0-set-up/running-the-server.md` — line 74's `internal/manualcharge/db/migrations`
  path reference → `internal/charging/db/migrations`.
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

- [x] **4.7** `docs/battery-consumed-graph.md` — the table row (`internal/manualcharge` →
  `internal/charging`), `manualcharge.ListEntriesByVehicleBetween` → `charging.ListEntriesByVehicleBetween`,
  the three `manualcharge.Writer.*` route-mapping rows → `charging.Writer.*`, the
  `internal/manualcharge` boundary-proof sentence, and the Entry 12 module-list mention — 7 of
  the file's 8 references. **Do not touch** the `csrf_manualcharge` mention in the guard-chain
  prose (design.md D3, D5).
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

- [x] **4.8** `openspec/roadmaps/backlog.md` — §2 header and body mention (2), §3 header, "the
  `manualcharge` module is next touched" prose, and the `internal/manualcharge/db_integration_test.go`
  path mention (3), and the "charging data is split across two modules" bullet's
  `internal/manualcharge` mention (1) — 6 of the file's 8 references. **Do not edit** the two
  historical citations `` `RM3-manualcharge-add-entries` `specs/manual-charge-log/spec.md` `` and
  `` `RM3-manualcharge-add-entries` (RM3 tier 1) review finding **R3** `` — they name an
  already-archived change verbatim; archived change names are never retconned (design.md D5,
  tier1 D6 precedent).
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

- [x] **4.9** `.agents/skills/kkpa-goth-scaffold-ui/references/charges-slice-pattern.md` — "the
  gateway will call (e.g. `telemetry.Reader`, `manualcharge.Reader`/`Writer`, …)" →
  "`charging.Reader`/`Writer`" (design.md D5). Do not touch the untracked `.claude/skills/...`
  copy of this file (leader's dispatch instruction — not a task).
  `depends_on`: 1.1–3.2 · `parallel_ok`: yes

(`cmd/README.md`, `openspec/specs/manual-charge-log/spec.md`, and
`openspec/roadmaps/RM29-modular-monolith-boundaries.md` were checked and need **zero** edits —
design.md D1, D5, D6 — no tasks for them.)

## Wave 5 — verification (assistant-run signals, then owner-run suite)

- [x] **5.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l .` (if `gofmt -l`
  lists a touched file — expected, per design.md's import-ordering-churn risk note, since
  `charging` sorts one alphabetical slot earlier than `manualcharge` did in most import blocks —
  run `gofmt -w` on it and re-check clean). Also `make ui-guard`, `make i18n-guard`,
  `make money-guard` as standing standalone guards, expected no-ops for a non-HTML,
  non-string, non-monetary change.
  `depends_on`: 1.1–4.9 (everything must have landed for the tree to build) ·
  `parallel_ok`: no (final gate)

- [x] **5.2** Run `make sqlc` one final time (idempotency check — confirms task 2.6's
  regenerate-and-diff step is still clean after all later edits) and `make vet`/`make bins`.
  `depends_on`: 5.1 · `parallel_ok`: no

- [x] **5.3** Run `openspec validate --changes --strict` and report the result. This is the
  change's status-check substitute — `openspec status`/`openspec instructions` reject the
  uppercase `RM29-…` change name (their validator demands lowercase); `validate` does not.
  `depends_on`: 3.1, 3.2, 5.1 · `parallel_ok`: no

- [x] **5.4** Hand off to the owner. Exact commands to paste (not run by the assistant, per the
  Test-Execution-Policy):
  ```
  go test ./internal/charging/... ./internal/analytics/... ./internal/gateway/... ./cmd/...
  ```
  or the full suite:
  ```
  go test ./...
  ```
  Additionally, since this tier moves a migrations directory, the owner should confirm
  `make db-setup` (or `make migrate-up` against an existing DB) still reports both
  `manual_charge_entries` migrations as already-applied (no re-run, no error) — the concrete
  check for design.md's goose-version-tracking safety claim (D7).
  Until the owner runs these and reports the result, this tier's status is
  **awaiting-user-verification**, never "done" (design.md "Verification signals").
  `depends_on`: 5.1, 5.2, 5.3 · `parallel_ok`: no

- [x] **5.5** **Owner-run — live goose state check** (added at the design gate, owner's call).
  After the move, the owner runs:
  ```
  make migrate-status
  ```
  Expected: BOTH of this module's migrations report **applied**, read from the NEW
  `internal/charging/db/migrations` path, with nothing pending —
  `20260718000001_add_manual_charge_entries` and `20260720000001_require_location_kind`.
  This exists because the failure mode of a bad directory move is **silent**: `sqlc generate`
  reads `sqlc.yaml` directly and would still succeed, so `go build` stays green while
  `make migrate-up` quietly stops applying this module's migrations from a stale path.
  `migrate-status` is what turns that into a loud failure before the tier archives.
  A migration reported as *pending* here means the move broke goose's version tracking — stop
  and report; do NOT run `migrate-up` to "fix" it, since re-applying an already-applied
  migration against a live table is how data gets damaged.
  `depends_on`: 5.1, 5.2 · `parallel_ok`: no · **owner-run, not assistant-run**
