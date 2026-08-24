# Design — RM29-analytics-rename-from-battery

> Numbering note: this document's decisions are numbered **D1, D2, …**, scoped to this
> change only. They are distinct from the roadmap's own **D1–D10** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md`, which are cited below as
> **roadmap D3**, **roadmap D8**, etc., to avoid collision.

## Context

`internal/battery` is the platform's derived-metrics module: it computes rolling
efficiency (Wh/km) and per-day battery-consumed percentage from data owned by
`internal/telemetry`, `internal/manualcharge` and `internal/account`. Under MAG-26
(roadmap D1) this module's scope is about to grow to own a precomputed read model
(`vehicle_metrics`) covering odometer, battery range, temperatures and TPMS pressures —
fields that have nothing to do with "battery." The roadmap's naming rule (roadmap D3,
*rename only where the name becomes wrong*) puts this rename first in the tier order
(roadmap D8) specifically so it lands as a clean, mechanical, single-purpose diff before
any real ownership change (tier 3) is built on top of it.

This is a pure package rename: five source files move with an unchanged package clause
target (`battery` → `analytics`), three test files move alongside them, and six files
outside the module re-point their import and qualifier because a Go package rename cannot
be sandboxed to the renamed package alone — every importer must move in the same commit or
the tree does not compile.

**Verified against the codebase** (not taken on the proposal's word — confirmed by
grepping for actual quoted import statements, not prose mentions of "internal/battery" in
comments, which are common throughout `internal/telemetry` and `internal/gateway` doc
comments and do **not** require a code change):

```
$ grep -rn '"[^"]*internal/battery"' --include='*.go' .
cmd/poller/main.go:34
cmd/web/main.go:18
internal/gateway/gateway.go:19
internal/gateway/handlers/handlers.go:27
internal/gateway/handlers/history.go:24
internal/gateway/handlers/history_test.go:19
```

This confirms the proposal's six-call-site list exactly — no correction needed. (Files
that only *mention* `internal/battery` in a doc comment — `internal/telemetry/telemetry.go`,
`internal/telemetry/db/models.go`, `internal/telemetry/db/query.sql.go`,
`internal/gateway/templates/fragments/history_vm.go`,
`internal/gateway/handlers/charges_test.go`,
`internal/gateway/handlers/handlers_test.go` (one line: "no battery.") — are prose, not
imports, and are Go-code-correct without any change. Two of the four listed doc files
carry many such prose mentions; see "Doc updates" below for which ones actually need
editing.)

## Goals / Non-Goals

**Goals**
- Rename `internal/battery` → `internal/analytics` (folder, package clause, doc comment)
  with zero behavior change.
- Re-point every real importer in the same commit so the tree compiles at every point a
  reviewer might check it out.
- Rename the two vocabulary artifacts that leak the old name into the gateway
  (`Deps.BatteryReader` → `AnalyticsReader`, `batteryReader` → `analyticsReader`) so the
  gateway's field names match the module it depends on, per the proposal's own rationale
  for not leaving a shim.
- Move `openspec/specs/battery/` → `openspec/specs/analytics/` and correct its `## Purpose`
  line, ahead of archiving, so `openspec sync` has a real spec to apply the `## MODIFIED
  Requirements` delta against.
- Update every doc that actually names `internal/battery`/`battery.Reader`/etc. as a
  module reference, in the same change (CLAUDE.md docs-track-structural-change rule).

**Non-Goals**
- No behavior change of any kind — not to `RecentEfficiency`, not to `ConsumedByDay`, not
  to any exported constant. This is out of scope by construction (a rename), not merely
  undesired.
- No database object of any kind (this module owns none — see "Database Changes" below).
- No new test — see "Testing" below.
- No renaming of `manualcharge` (tier 2), no `vehicle_metrics` table (tier 3), no
  `charge_gaps` ownership move (tier 5), no `internal/app` (tier 7). All out of scope per
  the proposal's own "Out of scope" section; not re-litigated here.
- No renaming of `internal/telemetry` (roadmap D3 rejects this in every tier, permanently).
- No renaming of the domain *concept* "battery" where it names a real vehicle attribute or
  UI label — e.g. `vehicle_snapshots.battery_level` (a telemetry column), the "Battery
  consumed" chart panel/i18n label, `docs/battery-consumed-graph.md`'s filename, or the
  `internal/gateway/AGENTS.md` `data-testid` example `battery-info`. Only references to the
  **module/package name** are in scope; the EV domain word "battery" is not going anywhere.

## Decisions

### D1 — File-by-file move map

| Old path | New path | Change |
|---|---|---|
| `internal/battery/battery.go` | `internal/analytics/analytics.go` | rename file; package clause `battery`→`analytics`; doc comment's "Package battery is…" → "Package analytics is…"; internal prose references to "the battery module" → "the analytics module" |
| `internal/battery/capacity.go` | `internal/analytics/capacity.go` | package clause only |
| `internal/battery/consumed.go` | `internal/analytics/consumed.go` | package clause only |
| `internal/battery/derive.go` | `internal/analytics/derive.go` | package clause only |
| `internal/battery/reader.go` | `internal/analytics/reader.go` | package clause only |
| `internal/battery/consumed_test.go` | `internal/analytics/consumed_test.go` | package clause only (external test package convention unaffected — this module uses `package battery`, in-package tests, not `battery_test`) |
| `internal/battery/derive_test.go` | `internal/analytics/derive_test.go` | package clause only |
| `internal/battery/reader_test.go` | `internal/analytics/reader_test.go` | package clause only |
| `internal/battery/AGENTS.md` | `internal/analytics/AGENTS.md` | content updated — see D5 |

Executed as a plain `mv` per file followed by a package-clause edit — **not** `git mv`,
and never a delete+recreate of the content. The module worker is forbidden from running any
git command (pipeline write-ownership rule: git belongs to the leader, who stages and
commits at wave boundaries), so it moves files with `mv` and the leader stages the result.
This costs nothing: git stores no rename records at all — `git mv` is merely `mv` plus a
staging update, and `git log --follow` / `git blame` reconstruct renames from content
similarity at read time. A `mv` + leader `git add -A` therefore produces a byte-identical
commit, with identical blame history, to a `git mv`.

### D2 — Public surface: what keeps its name vs. what renames

The proposal is explicit that the **port's shape** is unchanged — only the **package name**
and the two gateway-side vocabulary fields change. Verified against the actual source
(`internal/battery/battery.go`, `reader.go`):

**Keeps its exact name, now under `package analytics`:**
- `Reader` (interface) — `RecentEfficiency(ctx, accountID, teslaID) (Efficiency, bool, error)`, `ConsumedByDay(ctx, accountID, teslaID, start, end) ([]DayConsumption, error)`
- `Efficiency` (struct) — `WhPerKm`, `FromKm`, `ToKm`, `BatteryDeltaPct`, `Approximate`
- `DayConsumption` (struct) — `Date`, `ConsumedPct`, `DistanceKm`, `Flagged`, `MissingChargingType`, `DaysSpanned`
- `NewReader(telemetry, supercharger, manual, account, window) Reader`
- `DefaultWindow`, `GapReconciliationWindow` (both `time.Duration` constants)
- The unexported `vehicleLookup` interface in `reader.go` and the unexported
  `packCapacityKWh` map in `capacity.go` — package-private, move verbatim.

None of these identifiers contain the word "battery" in their Go name (only in surrounding
comments/docs), so no call site needs an identifier change beyond the package qualifier
itself: every `battery.Reader`, `battery.Efficiency`, `battery.DayConsumption`,
`battery.NewReader`, `battery.DefaultWindow`, `battery.GapReconciliationWindow` becomes
`analytics.<same-name>`.

**Renamed, because they spell out the old module name directly:**
- `gateway.Deps.BatteryReader` (field, `internal/gateway/gateway.go:64`) → `AnalyticsReader`
- `handlers.Deps.BatteryReader` (field, `internal/gateway/handlers/handlers.go:76`) → `AnalyticsReader`
- `handlers.Handler.batteryReader` (unexported field, `handlers.go:98`) → `analyticsReader`

These three are the only Go identifiers in the six call-site files whose *name itself*
(not just the package qualifier prefixing it) contains "battery," per the proposal's
"Deps.BatteryReader → Deps.AnalyticsReader" instruction — verified by grep, no other
`*Battery*`/`*battery*`-named symbol exists in `gateway.go` or the non-test parts of
`handlers.go`/`history.go`.

**Same-file consistency, not independently mandated but following from the above:**
`history_test.go` additionally contains `fakeBatteryReader` (type),
`newHandlerForHistoryWithBattery` (helper func), and `TestHandler_BatteryReaderDepsForwarding`
(test func) — none named in the proposal's bullet list, but each one is a local vocabulary
echo of the exact `BatteryReader` field it exists to test. Since this file is being edited
anyway (compile-forced, for the `battery.*` → `analytics.*` qualifier change), leaving
these three names stale would recreate in miniature the "two names for one thing" problem
RM29 exists to remove. They are renamed to `fakeAnalyticsReader`,
`newHandlerForHistoryWithAnalytics`, `TestHandler_AnalyticsReaderDepsForwarding` in the
same task that touches this file (tasks.md 2.6) — a same-file, same-task judgment call, not
a new decision requiring separate sign-off.

### D3 — No deprecation shim, no type alias

Considered and rejected, per the proposal's own reasoning (echoed here because design.md is
where a reader expects to find the rejected-alternative record): a temporary
`package battery` re-exporting `type Reader = analytics.Reader` etc. would leave two import
paths resolving to the same module. That is exactly the duplicated-vocabulary cost RM29
exists to remove (see roadmap "Problem" §3 — "no module boundary tells you which... kind of
fact you are looking at" generalizes to "no import path tells you which name is current").
A shim would also require a follow-up change to delete it, which nothing in the roadmap's
eight tiers schedules — it would become permanent by default. Since every importer is
inside this repository (no external consumer, no published Go module), there is no
compatibility audience a shim would serve. Straight rename, one commit, no bridge.

### D4 — specs folder move, and why it must precede archive

`specs/analytics/spec.md` (already written, this change's own delta spec) uses
`## MODIFIED Requirements` throughout. The OpenSpec CLI's sync/archive step applies a
`MODIFIED` delta against an existing main spec at `openspec/specs/<capability>/spec.md` —
it has no main spec to modify if `openspec/specs/analytics/` doesn't exist yet, because
today only `openspec/specs/battery/spec.md` exists. Two ways to resolve this were
considered:

- **Let the archiver synthesize the capability** — rejected. OpenSpec's archive step
  creates a new spec folder for capabilities it cannot find an existing main spec for,
  populating `## Purpose` with a placeholder (`TBD`) that a human must fill in later. That
  produces a spec with an incorrect/absent purpose statement and a needless follow-up
  chore.
- **`git mv openspec/specs/battery/ openspec/specs/analytics/` as an implementation task,
  before archive** (chosen). This preserves the file's full requirements content
  unchanged — deltas never touch `## Purpose`, so nothing in `spec.md`'s body needs
  editing — and only the `## Purpose` line and the `# battery Specification` H1 need a
  one-line hand-edit: `# battery Specification` → `# analytics Specification`, and "Derived
  battery analytics over stored telemetry — the platform's metrics layer…" → "Derived
  analytics over stored telemetry — the platform's metrics layer…" (drop "battery" as the
  qualifier; the purpose sentence already states what it derives without needing the old
  module name). This ordering is a **hard dependency**: the `git mv` (tasks.md 3.1) must
  land before `openspec validate --changes --strict` / archive is attempted, or the CLI's
  "MODIFIED against nonexistent spec" rejection fires. tasks.md marks this explicitly.

### D5 — `internal/analytics/AGENTS.md` charter update

`internal/battery/AGENTS.md` → `internal/analytics/AGENTS.md`, `Agent-Name: battery` →
`Agent-Name: analytics` (its own binding self-identification, referenced by
`ai/agentic-workflow.md`'s dispatch protocol), and its "Responsibility" section gains one
sentence noting the RM29 scope: the module owns what the application calculates, and from
tier 3 it will own a database of its own — a fact the file's current text explicitly denies
("This module owns no database... and no `internal/battery/db` package") and which stays
true **only** for this tier. The "Data ownership" section is otherwise left as-is: this
tier changes nothing about data ownership, so overstating tier-3's future is out of scope
here — one forward-looking sentence, not a rewrite. Every other `battery` token in the file
(imports table, testing section, etc.) is a mechanical rename to `analytics`/`Analytics`.

### D6 — Doc updates: verified against the codebase, not assumed

Each doc named in the proposal was grepped for actual `internal/battery` / `battery.`
module-reference content (as opposed to the unrelated domain word "battery" naming a real
vehicle attribute, which is never in scope — see Non-Goals). Result:

| Doc | Needs edit? | What |
|---|---|---|
| `README.md` | **Yes** | Project Structure tree (`│   ├── battery/  # Derived battery metrics...`), Architecture table row (`internal/battery` \| Derived battery metrics...), Dependency graph code block (`battery` appears 5 times as a layer/import name: `cmd/web`, `cmd/poller`, `gateway`, `handlers`, and its own `LAYER 2` row `battery ────────────► account, manualcharge, telemetry`) |
| `ai/architecture.md` §4 | **Yes** | The Target Directory Blueprint's `└── battery/ # EXAMPLE domain module` entry plus its two example files' comments (`service.go # core battery logic/math…`); the DTO-naming example `battery.State`; the import-cycle worked example in §2 (`// In telemetry, if it ever needed data owned by battery… battery satisfies this without importing telemetry`) — all four are illustrative uses of the literal module name, not the domain word. **Not edited:** §7's `battery_level` (a `vehicle_snapshots` column name, telemetry's data, unrelated to this module) |
| `internal/gateway/AGENTS.md` | **Yes** | The `Deps.BatteryReader battery.Reader` bullet and its three follow-on lines naming `battery.NewReader(...)` / `internal/battery owns no database` / `no batterydb package`. **Not edited:** the "Battery consumed" chart-panel prose, the `battery-info` `data-testid` example, and the `account, charging, battery, drives` domain-grouping example list further down — all domain-word or forward-looking examples, not references to this module's current Go identifiers. (The domain-grouping example list is illustrative prose only, doesn't compile, and isn't part of this tier's scope; left as-is.) |
| `internal/telemetry/AGENTS.md` | **Yes** | Five prose references to `internal/battery` as the `GapWriter` port's intended caller (lines documenting `charge_gaps`, `poll_attempts`'s caller boundary, the RM27 taper-curve aside). Pure prose edits — no code, no schema — since telemetry itself never imports battery (confirmed: no quoted import match) and this tier changes nothing about that boundary, only its name. |
| `docs/battery-consumed-graph.md` | **Yes, content only — not the filename** | Roughly a dozen references to `internal/battery/*.go` file paths and `battery.ConsumedByDay`/`battery.GapReconciliationWindow` qualifiers throughout the pipeline walkthrough. The **filename itself is not renamed** — "battery-consumed-graph" names the domain feature ("Battery Consumed" pipeline), not the Go package, exactly like the gateway's chart panel label; renaming the file is out of scope and not requested by the proposal. |
| `openspec/roadmaps/backlog.md` | **Yes** | §7's header (`## 7. battery / tesla / account / telemetry — Trim-exact pack capacity` → `## 7. analytics / tesla / account / telemetry — …`) and its two `internal/battery`/`internal/battery/capacity.go` path references. **Not edited:** the historical change-name citations `battery-add-efficiency-metric` and decision ID `D1b` — these name an already-archived change folder verbatim and renaming them would misrepresent history; archived change names are never retconned by a later tier. |

`cmd/README.md` was checked and carries no `battery` reference at all (its `poller` and
`web` entries describe responsibilities, not imports) — not listed as a task.

### D7 — Database gate: not applicable, explicitly

Per the leader's pre-verified fact and confirmed independently (`ls internal/battery/`
shows no `db/` subfolder; `grep battery sqlc.yaml` returns no entry): this module owns no
database object of any kind. `openspec/config.yaml`'s `rules.design` database gate ("Any
new/changed database object... MUST include the full schema...") is therefore **not
triggered** — there is no schema, no rationale, and no index plan to write, because nothing
about persistence changes. This section exists to make that omission an explicit, verified
decision rather than a silent gap a reviewer has to independently re-check.

### D8 — Test contract: not applicable, explicitly

`ai/go-conventions.md`'s "Authoring order" rule (author expected integration-test values in
design.md up front, before implementation, for tests with no fast feedback loop) has
nothing to author here: this tier writes **zero new tests** (roadmap D10 — characterization
tests only, and there is no behavior change to characterize; the proposal's own Testing
section states the compiler is the only proof this tier needs). The three existing test
files move with unchanged assertions, fixtures and test names — only the package clause and
`battery.*`/`Battery*` qualifiers inside them change (mechanically, alongside D2's
same-file renames in `history_test.go`). Stating this explicitly, as instructed, rather
than silently omitting the section: there is no test contract because there is no new
test.

### D9 — Performance profile: satisfied trivially

The project's read-heavy Performance-Profile (read performance mandatory, write
performance secondary) is not engaged by this change: "Read Paths Affected" is `None` per
the proposal, confirmed here — `RecentEfficiency` and `ConsumedByDay` keep their exact
implementations, so every query they trigger through `telemetry.Reader`,
`telemetry.SuperchargerReader`, `manualcharge.Reader` and `account.Service` is
byte-for-byte identical before and after. Stated explicitly, per instruction, rather than
omitted.

## Risks / Trade-offs

- **Cross-module blast radius for a single-module worker.** A rename this shallow still
  touches six files outside `internal/analytics`, none of which the `battery` module
  worker may edit (sandbox rule: edit only inside the assigned module). tasks.md groups
  every out-of-module edit into leader-owned tasks in a single wave alongside the module's
  own rename, so the tree is never left uncompilable at a wave boundary — see tasks.md
  Wave 2.
- **Ordering risk on the specs folder.** If the `openspec/specs/battery/` → `analytics/`
  move (D4) is skipped or done after archive is attempted, `openspec validate`/archive
  fails with a MODIFIED-against-nonexistent-spec error, or (worse) the CLI silently
  synthesizes a `TBD`-purpose spec. tasks.md places this move in its own task with an
  explicit `depends_on` gate before the final `openspec validate --changes --strict` task.
- **Silent identifier drift if `history_test.go`'s local names are left stale (D2's "same-file
  consistency" call).** Because `fakeBatteryReader`/`newHandlerForHistoryWithBattery`
  aren't named in the proposal's bullet list, a literal reading could skip them and still
  compile — Go doesn't care what a local test type is named. The risk is purely
  documentary/vocabulary drift, not a build break; tasks.md 2.6 makes the rename explicit
  so it isn't lost in a literal-proposal-only reading.
- **`gofmt`/import-grouping churn.** Renaming an import path changes where `goimports`
  would sort the import block in each of the six call-site files (alphabetical grouping:
  `analytics` sorts after `account` and before `manualcharge`/`telemetry`, same relative
  slot `battery` occupied — verified no reordering needed against the existing groups in
  `gateway.go`/`handlers.go`/`history.go`/`main.go`, since both names fall between
  `account` and `manualcharge` alphabetically). Low risk, called out so `gofmt -l` results
  are expected to be clean, not a surprise.

## Verification signals

Per the Test-Execution-Policy (verbatim in every dispatch): the assistant may run and
report on `go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make
bins`, and the standalone guards (`ui-guard`/`i18n-guard`/`money-guard` — none apply to a
non-gateway-HTML, non-i18n-string, non-monetary change, so they are expected no-ops here).
Additionally, `openspec validate --changes --strict` is available to the assistant (it is
an OpenSpec artifact-correctness check, not the Go test suite). The owner alone runs
`go test ./...` (or the narrower `go test ./internal/analytics/... ./internal/gateway/...
./cmd/...`) and reports the result — until they do, this tier's implementation status is
**awaiting-user-verification**, never "done."
