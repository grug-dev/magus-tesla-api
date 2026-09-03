## Context

Tier 4 (`RM39-telemetry-move-to-own-schema`, archived 2026-09-03) renamed telemetry's table
`supercharger_sessions` → `supercharger_history`, moved it into the `telemetry` Postgres
schema, and renamed the hand-written domain type + sqlc-generated model
`SuperchargerSession` → `SuperchargerHistory` (roadmap D5c). It deliberately left the public
port on the old vocabulary — `SuperchargerReader`, its four `SuperchargerSessions*` methods,
`NewSuperchargerReader`, and the unexported `superchargerReader` / `rowToSuperchargerSession`
/ `upsertSuperchargerSession` helpers — because the port was measured at **88 references
outside `internal/telemetry`**, spread across `charging`, `analytics`, `gateway`, `app` and
both `cmd/` binaries (tier 4 `AGENTS.md` note, roadmap tier-5 row). This tier finishes D5c by
renaming that surface.

**No database object is touched by this change** — no table, column, index, constraint, view,
or migration. `openspec/config.yaml`'s `database` design gate does not apply, and this
document has no schema/rationale/index-plan section for that reason, stated explicitly per
this dispatch's instructions rather than silently omitted.

## Goals / Non-Goals

**Goals**
- Rename telemetry's public `SuperchargerReader` port, its four methods, its constructor, and
  its three unexported helpers so every identifier matches the table's post-tier-4 name
  (`supercharger_history`) and the domain type it already returns (`SuperchargerHistory`).
- Update every real, compiler-checked consumer of the renamed identifiers so the whole repo
  builds and vets clean.
- Correct the small number of genuine comment-only mentions of the old port name outside
  `internal/telemetry`, and the KB guides that currently document the half-renamed state as
  current (several of which already, independently, record the finding this design confirms
  — see D1).

**Non-Goals**
- No SQL, migration, index, or generated-model change (tier 4 already produced the correct
  `SuperchargerHistoryBy*` sqlc query names and `SuperchargerHistory` model — see D3).
- No behavior change to any method: same parameters, same predicates, same ordering, same
  returned data.
- No change to `internal/gateway`'s or `internal/charging`'s own, differently-typed
  `SuperchargerReader`/`SessionReader` identifiers (D1) — renaming those would be a mistake,
  not a completion of this change.

## Decisions

### D1 — The measured "88 references outside `internal/telemetry`" figure is a name-collision
artifact. The real, compiler-checked cross-module surface is four files.

The roadmap's and tier 4's 88-reference count was produced by a coarse grep for
`SuperchargerReader` / `NewSuperchargerReader` / `superchargerReader` /
`rowToSuperchargerSession` / `upsertSuperchargerSession` across the repo. Investigating each
hit (not re-measuring the same grep — reading the actual code at each site) shows most of it
is not telemetry's port at all:

1. **`internal/gateway.Deps.SuperchargerReader` (and its lower-case mirror,
   `handlers.Deps.SuperchargerReader` / the private field `handlers.handlerDeps.superchargerReader`)
   is a different, permanent identifier.** Its type is `charging.SessionReader`, wired from
   `cmd/web/main.go` via `charging.NewSessionReader(pool)`. It is unrelated to
   `telemetry.SuperchargerReader` by more than coincidence: **`internal/gateway` cannot import
   `internal/telemetry` at all** — `ai/architecture.md` §"Exception: the gateway may not
   depend on `telemetry` at all", enforced by `make boundary-guard` with zero
   `// boundary:allow:` escape hatches repo-wide (verified: `grep -rn "internal/telemetry"
   internal/gateway/` returns nothing). Every hit inside `internal/gateway/gateway.go`,
   `internal/gateway/handlers/handlers.go`, `internal/gateway/handlers/supercharger.go`,
   `internal/gateway/handlers/supercharger_test.go`,
   `internal/gateway/handlers/history_test.go`, and `cmd/web/main.go`'s one wiring line
   (`SuperchargerReader: charging.NewSessionReader(pool)`) is this field — confirmed by
   reading each site's surrounding code, not by name alone. **None of these files change in
   this tier.** Renaming the gateway's field to "fix" the coincidence would itself be a
   mistake: it is not part of the vocabulary D5c is retiring, and the field name predates
   this roadmap (RM30-gateway-read-supercharger-stats-from-charging).

2. **`internal/analytics` already migrated off `telemetry.SuperchargerReader` in an earlier
   change.** `db_integration_test.go`'s own comment documents the switch: "retypes from
   telemetry.SuperchargerReader to charging.SuperchargerSessionAnalyticsReader (design.md §3)
   — this call site (telemetry.NewSuperchargerReader(pool) no longer satisfies
   NewRecalculator's ... signature)". `NewRecalculator` and `NewReader` in
   `internal/analytics/analytics.go` take a `charging.SuperchargerSessionAnalyticsReader`
   parameter, not a `telemetry.SuperchargerReader` — confirmed by reading the constructor
   signatures. The `fakeSuperchargerReader` types in `reader_test.go` and
   `recalculate_test.go`, and `recordingSuperchargerReader` in `db_integration_test.go`, all
   satisfy `charging.SuperchargerSessionAnalyticsReader` (`var _
   charging.SuperchargerSessionAnalyticsReader = (*recordingSuperchargerReader)(nil)`), never
   `telemetry.SuperchargerReader`. The KB independently confirms this at
   `kkpa/context/architecture/telemetry-ingest-only.md`: "Verified: no file under
   `internal/analytics/` names `telemetry.SuperchargerReader`." Every remaining
   "SuperchargerReader" hit in `internal/analytics/{analytics.go,gap_writer.go,
   db_integration_test.go,reader_test.go,recalculate_test.go}` is historical/comparative
   prose in a comment (e.g. "mirroring NewSuperchargerReader's identical pattern") — real to
   fix for accuracy, but never compiled code, and never a test-behavior change.

3. **`internal/charging/charging.go` imports no `internal/telemetry` package at all**
   (verified: no `internal/telemetry` import in the file). Of its 3 measured hits, 2
   (`SessionReader`'s and `SuperchargerSessionAnalyticsReader`'s doc comments) are
   self-referential — they say "internal/gateway depends on exactly this interface
   (Deps.SuperchargerReader)", i.e. they describe the **gateway's own field** from D1.1, not
   telemetry's port. The third (`session_reader.go`... actually `charging.go` line ~426: "not
   telemetry.SuperchargerReader's updated_at-ordering choice") is a genuine, comment-only
   mention. **One more genuine mention the original coarse grep missed** because it doesn't
   contain the substring "SuperchargerReader": `charging.go` line ~359, "mirroring
   telemetry.SuperchargerSessionsByVehicleBetween's end.AddDate(0,0,1)/half-open contract
   exactly" — a method-name mention, not a port-name mention. Both are comments; neither
   compiles against telemetry.

4. **The real, compiler-verified cross-module surface is confined to four files**:
   `internal/app/app.go` (constructor parameter, `telemetry.SuperchargerReader`),
   `internal/app/processor.go` (struct field of the same type, plus the one live call
   `p.superchargerReader.SuperchargerSessionsByAccount(...)`), `internal/app/processor_test.go`
   (the fake `fakeSuperchargerReader` that implements `telemetry.SuperchargerReader` with a
   `var _ telemetry.SuperchargerReader = fakeSuperchargerReader{}` assertion), and
   `cmd/poller/main.go` (the one wiring line, `telemetry.NewSuperchargerReader(pool)`). This
   matches the KB's own independent finding at `nightly-cycle.md`: "`app` |
   `telemetry.SuperchargerReader` | `telemetry` | `SuperchargerSessionsByAccount` — the
   port's only remaining caller repo-wide" and `telemetry-ingest-only.md`: "This is now the
   **only remaining caller of `telemetry.SuperchargerReader` repo-wide**."

**Consequence for tasks.md and for a future implementer:** do not treat "88 references" or
even "grep returns zero hits" as the completeness bar (unlike D18's catalog criterion for a
DB rename, a blind grep here can never return zero, because the gateway's and charging's own,
permanently different `SuperchargerReader`/`SessionReader`/`SuperchargerSessionAnalyticsReader`
identifiers will always match a substring search). The real bar is: (a) `go build ./...` and
`go vet ./...` are clean repo-wide (they resolve every real type reference, including in
`_test.go` files), and (b) every comment-only mention that is confirmed — by reading the
surrounding code, not by grep alone — to be about telemetry's port (not the gateway's or
charging's differently-typed fields) is corrected for accuracy. Tasks.md enumerates the
confirmed comment sites by exact file so an implementer does not have to re-derive this
triage.

### D2 — New names: `SuperchargerHistoryReader`, `SuperchargerHistoryBy*`, `NewSuperchargerHistoryReader`, `superchargerHistoryReader`, `rowToSuperchargerHistory`, `upsertSuperchargerHistory`

Derived directly from D5c's already-established vocabulary swap
(`SuperchargerSession`→`SuperchargerHistory`), applied consistently one layer up:

| Old | New |
|---|---|
| `SuperchargerReader` (exported interface) | `SuperchargerHistoryReader` |
| `SuperchargerSessionsByAccount` | `SuperchargerHistoryByAccount` |
| `SuperchargerSessionsByVehicle` | `SuperchargerHistoryByVehicle` |
| `SuperchargerSessionsByVehicleBetween` | `SuperchargerHistoryByVehicleBetween` |
| `SuperchargerSessionsByVehicleUpdatedSince` | `SuperchargerHistoryByVehicleUpdatedSince` |
| `NewSuperchargerReader` (constructor) | `NewSuperchargerHistoryReader` |
| `superchargerReader` (unexported impl struct) | `superchargerHistoryReader` |
| `newSuperchargerReaderImpl` (unexported ctor helper) | `newSuperchargerHistoryReaderImpl` |
| `rowToSuperchargerSession` (mapper) | `rowToSuperchargerHistory` |
| `upsertSuperchargerSession` (write-seam interface method + `dbStore` impl) | `upsertSuperchargerHistory` |

Justification, beyond "matches D5c": the four renamed method names become **byte-identical**
to the sqlc query names tier 4 already produced (`SuperchargerHistoryByAccount`,
`SuperchargerHistoryByVehicle`, `SuperchargerHistoryByVehicleBetween`,
`SuperchargerHistoryByVehicleUpdatedSince` — verified in
`internal/telemetry/db/query.sql.go`). Every method on `superchargerHistoryReader` becomes a
thin wrapper calling the identically-named `Queries` method
(`r.SuperchargerHistoryByAccount(...)` on the port literally calls
`q.SuperchargerHistoryByAccount(...)` on the generated queries) — one name to remember per
operation instead of two, a smaller closed vocabulary for an agent reading this file
(`CLAUDE.md`'s AI-efficiency criterion: "closed, small vocabularies an agent looks up instead
of re-inventing"). This mirrors the existing convention elsewhere in the module where a
`Reader` method wraps an identically-shaped store method (e.g. `LatestSnapshotsByAccount`).

`newSuperchargerReaderImpl` and `TestStore_SuperchargerReader_ReturnsBatteryPctTrioAndSnapshot`
(a test function name containing the old port name, `db_supercharger_battery_pct_integration_test.go`)
are swept along even though neither the dispatch's cross-module count nor a Go compiler
requires it (test function names and unexported helper names are free to keep any spelling) —
included for the same vocabulary-consistency reason, and because leaving one internal helper
on the old name while its siblings move would recreate exactly the half-state D5c is retiring,
one layer down.

Consumer-side local variable and field names (`superchargerReader` in `internal/app/app.go`,
`internal/app/processor.go`, `cmd/poller/main.go`) are **not** the same symbol as telemetry's
unexported `superchargerReader` struct — they are the consumer's own identifiers, free to keep
any name and not required to change for the code to compile. They are renamed to
`superchargerHistoryReader` anyway, for the same reason D5c gave for the domain type: leaving
a variable of type `telemetry.SuperchargerHistoryReader` called `superchargerReader` reads as
stale vocabulary in exactly the code most likely to be read next.

### D3 — `internal/telemetry/db/query.sql.go`'s 2 references are hand-authored prose comments, not sqlc-config-driven

The dispatch flagged `internal/telemetry/db/query.sql.go (2)` for verification: is it driven
by `sqlc.yaml`'s `gen.go.rename` (in which case the config needs a change), or incidental?

**Answer: incidental — hand-authored prose, not `gen.go.rename` output.** Both hits are
`-- name:`-block doc comments in `internal/telemetry/db/query.sql` (the hand-written SQL
source), copied verbatim into the generated `query.sql.go` by sqlc:

- `SuperchargerHistoryByVehicleBetween`'s comment: "Used by
  SuperchargerReader.SuperchargerSessionsByVehicleBetween to power RM28's..."
- `SuperchargerHistoryByVehicleUpdatedSince`'s comment: "Used by
  SuperchargerReader.SuperchargerSessionsByVehicleUpdatedSince to let..."

`gen.go.rename` (per `sqlc.yaml`'s telemetry entry, confirmed) only maps generated **struct/
model type names** (`telemetry_supercharger_history: "SuperchargerHistory"` etc.) — it has no
mechanism that touches comment text or query **function** names, which sqlc already takes
verbatim from the `-- name:` line (already correctly `SuperchargerHistoryByVehicleBetween`
etc. since tier 4; confirmed by reading `query.sql.go`'s actual generated function
signatures — none of the 5 query/function names need any change here, tier 4 already renamed
them). The fix is: edit the 2 prose comments in `query.sql` (the source of truth) to name the
new port (`SuperchargerHistoryReader.SuperchargerHistoryByVehicleBetween` /
`...ByVehicleUpdatedSince`), then run `sqlc generate` to regenerate `query.sql.go`. This is
mechanical, no `sqlc.yaml` change, no risk to any generated type.

### D4 — Task shape: one serial sweep, not parallel sub-tasks

This is a **single atomic Go compile unit**, not several independent ones. Renaming an
exported interface and its methods breaks every real consumer simultaneously — there is no
intermediate state where the port is half-renamed and the repo still compiles (that IS tier
4's current state, which is exactly what this tier exists to close). Concretely: the interface
declaration (`telemetry.go`), its implementation (`reader.go`), its consumers
(`internal/app/app.go`, `processor.go`), and its test doubles (`processor_test.go`,
`internal/telemetry`'s own `_test.go` files) must all land in the same commit for `go build
./...`/`go vet ./...` to pass at any point after the first file changes.

Splitting this across separate agents working truly in parallel is unsafe for the reason
`rules.tasks` asks to state plainly: every file in this change is on the dependency chain of
every other file (the interface rename is the root; everything else is a leaf that must
follow it). What CAN be done without breaking the build mid-flight is order the edits so the
package containing the interface changes first, in one pass, then its consumers — which is
what tasks.md's numbered, dependency-chained task list does. It is presented as sequential
tasks for tracking and resumability (per `openspec/config.yaml`'s tasks rule), not as
independent, file-disjoint units a second agent could pick up mid-change; a session interrupted
between tasks leaves the tree non-compiling until the remaining tasks in the chain land, which
is expected and acceptable for a change this small (design.md's own file count: 4 non-test +
1 comment file in `internal/telemetry`, 5 `_test.go` files in `internal/telemetry`, 3 files in
`internal/app`, 1 file in `cmd/poller`, 3 comment-only files outside the module, plus docs —
roughly 17 files total, a fraction of the 24 originally estimated because of D1's correction).

The one task genuinely independent of the compile chain, and safe to do in parallel or in
either order, is the docs/KB sweep (D5) — it touches no Go file.

### D5 — Docs and KB: the half-rename note that must be retired, and the "only remaining caller" claim that stays

`internal/telemetry/AGENTS.md`'s `SuperchargerReader` section (lines ~143-181) currently
instructs future agents: "do not rename `SuperchargerSessionsBy…`, `NewSuperchargerReader`,
`superchargerReader`, `rowToSuperchargerSession` or `upsertSuperchargerSession` — that is tier
5's job, not an omission in this file." This tier IS tier 5, so that whole note is replaced
with the new names and a short line recording that the port now matches the table (mirroring
how the module's other renamed-vocabulary sections read after their own tier landed).

KB files independently found, by grep, to name telemetry's port (not the gateway's/charging's
unrelated fields — confirmed by reading each site, same triage as D1):
`kkpa/context/architecture/telemetry-ingest-only.md`,
`kkpa/context/architecture/nightly-cycle.md`, and the still-pending
`kkpa/context/pending-spec-to-sync/telemetry.md`. Two of these documents **already record**
the D1 finding in their own words — `telemetry-ingest-only.md`: "This is now the only
remaining caller of `telemetry.SuperchargerReader` repo-wide" and "Verified: no file under
`internal/analytics/` names `telemetry.SuperchargerReader`"; `nightly-cycle.md`: "the port's
only remaining caller repo-wide" — independent corroboration this design's D1 is not a novel
re-derivation but a documented, previously-established fact this tier had only to confirm.
`kkpa/context/workflows/supercharger-stats-read.md` names a `SuperchargerReader` too, but it
is explicitly the **gateway's** field ("field NAME kept from the telemetry era; the TYPE is
the charging port since RM30") — out of scope, not touched. The `pending-spec-to-sync/applied/`
files are a historical sync record, not touched (mirrors how archived specs are never edited).

## Test Contract (authored before implementation, per `ai/go-conventions.md` §Testing)

This is a pure rename. The honest contract: **every existing test's assertions and expected
values are unchanged — only identifiers move.** Any test whose expected VALUE (not identifier)
changes as part of this tier is a bug in the change, not an intended consequence, and must be
flagged rather than silently accepted.

**Files whose test code changes, and exactly what changes in each:**

| File | What changes |
|---|---|
| `internal/telemetry/reader_test.go` | `fakeReadStore`/`fakeHistoryStore`/`fakeBetweenStore`'s `upsertSuperchargerSession` method renamed to `upsertSuperchargerHistory` (interface satisfaction only — the method still panics with the same message, updated to name the new symbol). No assertion changes. |
| `internal/telemetry/service_test.go` | `fakeStore.upsertSuperchargerSession` renamed to `upsertSuperchargerHistory`; comments updated. No assertion changes. |
| `internal/telemetry/db_supercharger_integration_test.go` | `newSuperchargerReaderImpl` → `newSuperchargerHistoryReaderImpl`; `.SuperchargerSessionsByAccount(...)` call sites → `.SuperchargerHistoryByAccount(...)`; error-message strings that echo the method name updated to match. No expected row/value changes. |
| `internal/telemetry/db_supercharger_between_integration_test.go` | Same shape: `newSuperchargerReaderImpl` → `newSuperchargerHistoryReaderImpl`; `.SuperchargerSessionsByVehicleBetween(...)` → `.SuperchargerHistoryByVehicleBetween(...)`; test function names (`TestSuperchargerSessionsByVehicleBetween_*`) may optionally follow (cosmetic, not required — see task list). No expected row/value changes. |
| `internal/telemetry/db_supercharger_battery_pct_integration_test.go` | Same shape; additionally the test function `TestStore_SuperchargerReader_ReturnsBatteryPctTrioAndSnapshot` is renamed to `TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrioAndSnapshot` (D2). No expected row/value changes. |
| `internal/app/processor_test.go` | `fakeSuperchargerReader` (the type name itself may optionally follow, e.g. `fakeSuperchargerHistoryReader` — not required for compilation) has its 4 methods renamed to match the new interface, and its `var _ telemetry.SuperchargerReader = fakeSuperchargerReader{}` assertion becomes `var _ telemetry.SuperchargerHistoryReader = ...{}`. No behavior or expected-value change in any `TestProcessVehicleData_*` test. |

No other `_test.go` file in the repo needs a code change for this tier (D1) — comment-only
fixes in `internal/analytics/db_integration_test.go` do not touch executable test code.

**Verification.** `go vet ./...` compiles every `_test.go` file above and is the assistant-
runnable signal that every renamed identifier and every fake's interface satisfaction still
line up — the same signal class the roadmap relies on throughout RM39 for pure Go renames.
Because no SQL, migration, or schema changes, there is no `DATABASE_URL`-gated behavior at
risk and no new integration-test authoring is needed — only identifier updates to tests that
already exist and already pass.

## Makefile / tooling re-check (per `CLAUDE.md`'s "reverse direction" docs rule)

No schema, migration, or codegen INPUT changes in this tier (D3: `query.sql`'s 2 comment
edits do not change any query, parameter, or table reference). Checked and found unaffected:
`make db-setup`/`db-reset` role/ownership assumptions (no schema change), `MIGRATIONS_DIRS`
order (no new migration), `make migration-guard` (no new migration version), `make
boundary-guard` (this tier makes the gateway's independence from `internal/telemetry` more
obviously safe, not less — D1 confirms the gateway never named telemetry's port to begin
with), and `sqlc` (D3: comment-only source edit, regenerate, no config change). The only
regeneration this tier triggers is `sqlc generate` after the 2 `query.sql` comment edits.

## Risks

- **Missing a real consumer.** Mitigated by D1's exhaustive, code-read (not grep-only) audit
  down to 4 files, and by `go build ./...`/`go vet ./...` being a hard, assistant-runnable
  gate that cannot silently pass with a stale reference to the old interface name (an
  unsatisfied interface or an undefined identifier is a compile error, not a warning).
- **Over-reaching into the gateway's or charging's unrelated `SuperchargerReader`/
  `SessionReader` identifiers.** This is the risk D1 exists to prevent — a naive
  "grep for SuperchargerReader and rename every hit" pass would corrupt working, unrelated
  code. Tasks.md's task list names the exact files that must NOT change alongside the ones
  that must.
- **Comment drift.** Comment-only fixes (D1.2, D1.3) carry no compiler check; a missed one is
  cosmetic debt, not a defect, and is called out as best-effort in tasks.md rather than a hard
  gate.
