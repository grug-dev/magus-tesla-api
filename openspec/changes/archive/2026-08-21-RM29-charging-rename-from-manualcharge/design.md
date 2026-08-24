# Design — RM29-charging-rename-from-manualcharge

> Numbering note: this document's decisions are numbered **D1, D2, …**, scoped to this
> change only. They are distinct from the roadmap's own **D1–D10** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md`, cited below as **roadmap D3**,
> **roadmap D8**, etc. They are also distinct from tier 1's own D1–D9 in
> `openspec/changes/archive/2026-08-20-RM29-analytics-rename-from-battery/design.md`, cited
> as **tier1 D2**, **tier1 D6**, etc., which this document mirrors in structure and, in D6
> and D9 below, explicitly extends with a precedent tier 1 set.

## Context

`internal/manualcharge` is the platform's user-asserted-charging module: `Writer`
(Create/Update/Delete) and `Reader` (three list methods) over its own `manual_charge_entries`
table, isolated from the Tesla Fleet API. Under MAG-26 (roadmap D4) this module's scope is
about to grow to also own `charge_sessions` — a normalized home for Supercharger-sourced
charging data, moved out of `internal/telemetry` in tier 6. A module that will soon hold both
user-asserted **and** Tesla-reported charging data cannot keep a name that asserts every row is
manual. The roadmap's naming rule (roadmap D3, *rename only where the name becomes wrong*) puts
this rename at tier 2 (roadmap D8), directly after tier 1's structurally identical
`battery`→`analytics` rename, so tier 6's real ownership change lands as a clean diff.

Unlike tier 1's subject (`internal/battery`, which owned no database at all), this module DOES
own a database: a `db/` folder, two goose migrations, and a `sqlc.yaml` entry. This tier is
therefore a strict superset of tier 1's mechanics — package rename + call-site re-point, **plus**
a migrations-directory move and an sqlc config re-point — while still changing **zero**
database objects (see D7).

**Verified against the codebase** (every count below reproduced with the exact command shown —
tier 1's design.md D6 undercounted five doc references because its sweeps matched only the
narrow `internal/battery`/`battery.Reader` forms and never the general `battery.<Identifier>`
qualifier or bare-word prose form; every sweep here uses the broad forms from the start):

```
$ grep -rn '"[^"]*internal/manualcharge"' --include='*.go' . | wc -l
13
$ grep -rn 'manualcharge\.[A-Za-z_]' --include='*.go' . | wc -l
142
$ grep -rn 'manualchargedb' --include='*.go' . | wc -l
39
$ grep -rn 'ManualCharge[A-Za-z]*' --include='*.go' . | wc -l
73
$ grep -rln 'manualcharge' --exclude-dir=.git . | wc -l   # files, any extension
103
$ grep -rn 'manualcharge' --exclude-dir=.git . | wc -l    # total occurrences, any extension
890
```

The first four numbers **match the leader-supplied inventory exactly** — no correction needed
there. The file-list and doc/config breakdown below **refines** the leader's inventory in three
ways, stated explicitly per the dispatch instruction to flag disagreement rather than silently
adopting either number:

1. **Two functional/doc files the leader's list omitted**, found by the required broad sweep:
   `.air.toml` (a build-tool config path, not prose — see D5) and
   `internal/gateway/handlers/lang.go` / `internal/testdb/testdb.go` (hand-written Go doc
   comments — see D5).
2. **The leader's "Docs/config (15)" list, counted literally, has 14 entries**, not 15 (no
   fifteenth path is identifiable in the dispatch text). This document's own file-by-file table
   (D5) is the count of record; it lists 16 doc/config files after adding the two above.
3. **`openspec/specs/manual-charge-log/spec.md` — listed by the leader as a file to touch —
   needs zero edits** (D1). **`openspec/roadmaps/RM29-modular-monolith-boundaries.md` —
   likewise listed — needs zero content edits** (D6). Both are genuine, verified negative
   findings, not omissions.

The vast majority of the 890 raw "any file, any extension" hits are inside
`openspec/changes/archive/**` (dozens of already-archived change folders spanning both the
`manualcharge` module's own history and every gateway/telemetry/platform change that ever
mentioned it) — none of those are edited, per the same "archived change content is never
retconned" rule tier 1's D6 applied to `battery-add-efficiency-metric`/`D1b`.

## Goals / Non-Goals

**Goals**
- Rename `internal/manualcharge` → `internal/charging` (folder, package clause, doc comments)
  and `internal/manualcharge/db` → `internal/charging/db` (folder, package clause) with zero
  behavior change and zero database object change.
- Re-point every real importer and the sqlc/Makefile/air config in the same commit so the tree
  compiles and `make sqlc`/`make db-setup` work at every point a reviewer might check it out.
- Rename the Go identifiers that spell out the old module name (`Deps.ManualChargeWriter/Reader`,
  the unexported `Handler.manualChargeWriter/Reader` fields, the two
  `Test*_ManualChargeError_Propagates` test functions) so the gateway's and analytics' own
  vocabulary matches the module they depend on — tier 1's own rationale for not leaving a shim,
  applied here to the two-field-plus-test-names case instead of tier 1's three-field case.
- Leave the sqlc-generated `ManualChargeEntry` struct name and the `csrfManualChargeKey`/
  `"csrf_manualcharge"` domain vocabulary untouched — explicit, verified, non-renames (D3).
- Produce delta specs for the two OTHER capabilities whose requirement text embeds the Go
  qualifier form (`analytics`, `gateway`), using OpenSpec's `RENAMED Requirements` operation
  where a requirement's title itself contains the old name (D4).
- Update every doc that actually names `internal/manualcharge`/`manualcharge.*`/`manualchargedb`
  as a module reference, in the same change (CLAUDE.md docs-track-structural-change rule).

**Non-Goals**
- No behavior change of any kind — not to `Writer`, not to `Reader`, not to any derived method
  or SQL query. Out of scope by construction (a rename), not merely undesired.
- No new, changed, or dropped database object — the table, its columns, its constraints, its
  indexes, and both migration filenames are byte-identical before and after (D7).
- No new test — see Testing in proposal.md.
- No renaming of `openspec/specs/manual-charge-log/` (D1), no `charge_sessions` table (tier 6),
  no `internal/app` (tier 7), no renaming of `internal/telemetry` (roadmap D3, permanent).
- No renaming of the domain *concept* "manual charge" where it names a real, still-accurate UI
  feature or CSRF scope — `csrfManualChargeKey`/`"csrf_manualcharge"`, the "Charge log" page,
  `docs/battery-consumed-graph.md`'s prose about "charges you assert by hand". Only references
  to the **module/package name** are in scope.

## Decisions

### D1 — `openspec/specs/manual-charge-log/` is NOT renamed

**Verified:** `openspec/specs/manual-charge-log/spec.md` contains exactly one substring match
for `manualcharge` in the whole file:

```
$ grep -n 'manualcharge' openspec/specs/manual-charge-log/spec.md
4:TBD - created by archiving change RM3-manualcharge-add-entries. Update Purpose after archive.
```

That is a citation of the **archived change name** `RM3-manualcharge-add-entries` in the
`## Purpose` line — history, not a live Go reference — and per tier1 D6 ("archived change names
are never retconned") it is left exactly as written. No requirement, no scenario, and no other
line in this 300-line spec names the Go package at all: every requirement is phrased in terms
of the domain (`account_id`, `energy_added_kwh`, `ListEntriesByVehicle`, …), never `manualcharge.*`.

**The folder itself stays `manual-charge-log`.** Three considerations, weighed against renaming
it to `charging-log` or similar:

1. **Precedent already in this repo.** `openspec/specs/tesla-exploration/`,
   `openspec/specs/unit-of-measure/`, and `openspec/specs/account-vehicle-registry/` are all
   capability names that do not match their owning Go package 1:1 (`tesla-exploration` names a
   capability of `internal/tesla`; `account-vehicle-registry` names a capability of
   `internal/account`). `openspec/config.yaml`'s "each OpenSpec domain maps to one `internal/`
   module" does not require the *name string* to match — tier 1's own analytics/battery rename
   was a coincidence of the module and its one capability sharing a name, not a hard rule that
   every capability folder must always equal its module's current Go package name.
2. **The capability name remains accurate after the rename, and gets MORE accurate as MAG-26
   proceeds.** "Manual charge log" describes exactly what this capability is: a user-asserted
   log of charges. Tier 6 adds `charge_sessions` specifically to hold the *non*-manual,
   Supercharger-sourced rows in the same (soon-to-be-`charging`) module — meaning
   `internal/charging` will own TWO capabilities side by side (`manual-charge-log` and,
   eventually, a Supercharger-sessions capability), and conflating the capability name with the
   module name would become actively misleading once that happens, not just today.
3. **Roadmap D3 itself** (*rename only where the name becomes wrong*) argues against it: the
   capability name isn't wrong, so renaming its folder would be a needless move touching zero
   requirement content, done only to chase a coincidental past alignment with the module name.

**If this decision is later reversed** (e.g. if a future tier decides capability and module
names must always match), tier1 D4's warning applies verbatim: the move MUST be a folder
`mv`/`git mv` with a `## Purpose`/H1 hand-edit before any `MODIFIED Requirements` delta is
attempted against it, never `ADDED`+`REMOVED`, or `openspec archive` will synthesize a
placeholder-`TBD`-purpose spec and silently lose the folder's real content.

**Consequence:** this change produces **no delta spec for `manual-charge-log`** — no
`specs/manual-charge-log/spec.md` file exists in this change's `specs/` folder.

### D2 — File-by-file move map

| Old path | New path | Change |
|---|---|---|
| `internal/manualcharge/manualcharge.go` | `internal/charging/charging.go` | rename file; package clause `manualcharge`→`charging`; doc comment's "Package manualcharge is…" → "Package charging is…"; internal prose "the manualcharge generated package" → "the chargingdb generated package" (see D9) |
| `internal/manualcharge/service.go` | `internal/charging/service.go` | package clause only; every `manualchargedb.*` qualifier → `chargingdb.*` (D9) |
| `internal/manualcharge/manualcharge_test.go` | `internal/charging/charging_test.go` | rename file; external test package `manualcharge_test` → `charging_test` (this module's sanctioned back-edge — `ai/architecture.md` §2 — moves verbatim, no assertion change) |
| `internal/manualcharge/db_integration_test.go` | `internal/charging/db_integration_test.go` | package clause `manualcharge_test`→`charging_test`; every `manualchargedb.*` qualifier → `chargingdb.*` |
| `internal/manualcharge/testdb_test.go` | `internal/charging/testdb_test.go` | package clause only. The `//go:embed db/migrations/*.sql` directive is **relative to the package directory and needs no text change** — it still reads `db/migrations/*.sql` correctly once the whole file moves with the package (verified: the directive has no `manualcharge`/`internal/` prefix to update) |
| `internal/manualcharge/AGENTS.md` | `internal/charging/AGENTS.md` | content updated — see D5's own-module row |
| `internal/manualcharge/db/db.go` | `internal/charging/db/db.go` | package clause `manualchargedb`→`chargingdb` only (sqlc-generated; regenerate via `make sqlc` after the move rather than hand-editing — see D9) |
| `internal/manualcharge/db/models.go` | `internal/charging/db/models.go` | package clause only; **`ManualChargeEntry` struct name unchanged** (D3) |
| `internal/manualcharge/db/query.sql.go` | `internal/charging/db/query.sql.go` | package clause only; every `ManualChargeEntry` reference unchanged (D3) |
| `internal/manualcharge/db/query.sql` | `internal/charging/db/query.sql` | no content change — hand-written SQL, no package clause |
| `internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql` | `internal/charging/db/migrations/20260718000001_add_manual_charge_entries.sql` | **no content change whatsoever** — filename and body byte-identical (D7) |
| `internal/manualcharge/db/migrations/20260720000001_require_location_kind.sql` | `internal/charging/db/migrations/20260720000001_require_location_kind.sql` | **no content change whatsoever** |

Executed as a plain `mv` per file/folder, **not** `git mv` — the module worker never runs git
(pipeline write-ownership rule); the leader stages the result with `git add -A`, which produces
a commit identical to `git mv`, blame included (tier1 D1's reasoning applies verbatim: git
stores no rename records at all).

### D3 — Public surface: what renames vs. what stays

Verified breakdown of the leader's 73 `ManualCharge[A-Za-z]*` matches (capital-M form), by
exact category:

```
$ grep -rn 'ManualChargeEntry' --include='*.go' . | wc -l          # 26 — sqlc-generated struct
$ grep -rn 'csrfManualChargeKey' --include='*.go' . | wc -l        # 12 — CSRF domain-vocab const
$ grep -rn 'ManualChargeWriter\|ManualChargeReader' --include='*.go' . | wc -l   # 31 — Deps fields
$ grep -rn 'ManualChargeError' --include='*.go' . | wc -l          # 4 — test func names + doc refs
```
`26 + 12 + 31 + 4 = 73` — reconciles exactly with the leader's figure, and splits it into two
STAYS categories (38 occurrences) and two RENAMES categories (35 occurrences). **Plus 10
additional lowercase occurrences the leader's capital-M-anchored regex could not match at all**
(`manualChargeWriter`/`manualChargeReader`, the unexported `Handler` fields) — these also
RENAME, bringing the true rename total to 45, not 35.

**Renames (spell out the old module name directly):**
- `gateway.Deps.ManualChargeWriter`/`ManualChargeReader` (`gateway.go:54,57`) →
  `ChargingWriter`/`ChargingReader`
- `handlers.Deps.ManualChargeWriter`/`ManualChargeReader` (`handlers.go:66,69`) → same
- `handlers.Handler.manualChargeWriter`/`manualChargeReader` (unexported, `handlers.go:96-97`)
  → `chargingWriter`/`chargingReader`
- Every `ManualChargeWriter:`/`ManualChargeReader:` struct-literal field usage — `cmd/web/main.go`
  (2), `handlers/charges_test.go` (14, seven test-setup call sites × 2 fields)
- Every `h.manualChargeWriter.*`/`h.manualChargeReader.*` call site — `handlers/charges.go` (6)
- `analytics/reader_test.go`'s `TestRecentEfficiency_ManualChargeError_Propagates` and
  `TestConsumedByDay_ManualChargeError_Propagates` (function names) → `..._ChargingError_Propagates`,
  plus the two doc-comment lines pointing at them by name (lines 335, 555) and the fixture error
  strings `errors.New("manualcharge: connection lost")` / the two `t.Errorf("manualcharge: ...")`
  prefixes (same-file consistency call, tier1 D2's reasoning: these files are being edited
  anyway for the `manualcharge.` → `charging.` qualifier change, and a stale error-message
  prefix inside a test whose whole point is identifying *which* port's error propagated would
  recreate the "two names for one thing" problem in miniature)
- `handlers/history_test.go:1043`'s doc-comment mention of "ManualChargeReader" (prose pointing
  at the just-renamed `Deps` field, in a file already being edited for its own `AnalyticsReader`
  history from tier 1 — no new edit to this file's assertions, just this one stale name)

**Stays — sqlc-generated, tracks the unchanged table name (D7), not this module's package name:**
- `chargingdb.ManualChargeEntry` (struct in `db/models.go`, `db/query.sql.go`; 26 occurrences) —
  sqlc names generated row structs from the SQL table name, not the Go package name. The table
  stays `manual_charge_entries`, so `sqlc generate` reproduces `ManualChargeEntry` verbatim
  after the move; hand-renaming it would only be reverted by the next regeneration and would
  misrepresent what the generator actually produces (D9).

**Stays — domain vocabulary, not this module's package name:**
- `csrfManualChargeKey` (unexported const, `internal/gateway/handlers/charges.go`) and its
  string value `"csrf_manualcharge"` — names the "manual charge" **UI feature and CSRF scope**,
  a description that remains accurate (this feature is still, and will remain, the *manual*
  charge-entry form even after tier 6 adds a separate `charge_sessions` UI surface for
  Supercharger-sourced data). This is the domain-word exemption tier1's Non-Goals established
  for "battery" as a vehicle attribute, applied here to "manual charge" as a UI/session-scope
  label. The one live-spec occurrence of this exact string
  (`openspec/specs/gateway/spec.md`'s "Delete Charge Entry" scenario, "the session's current
  `csrf_manualcharge` value") is likewise left untouched inside that requirement's otherwise-
  edited body (D4).

### D4 — OpenSpec delta specs: `analytics` and `gateway`, using `RENAMED Requirements`

Unlike tier 1 (whose rename touched no other capability's spec text), this rename's
`internal/manualcharge`/`manualcharge.*`/`manualchargedb` identifiers are quoted verbatim inside
**two other capabilities'** requirement bodies, verified by mapping every match to its
containing `### Requirement:` header:

```
$ awk '/^### Requirement:/{req=$0} /manualcharge/{print NR": "req}' openspec/specs/gateway/spec.md
```
→ 5 requirements, 25 raw line matches (24 in scope + 1 `csrf_manualcharge` exemption, D3):
"Charge List Fragment" (2), "Create Charge Entry" (6), "Inline Row Editing" (1), "Delete Charge
Entry" (6, one of which is the `csrf_manualcharge` exemption — 5 in scope), "Gateway Imports No
manualchargedb Package" (10, including its own H3 title and one scenario title).

```
$ grep -n 'manualcharge' openspec/specs/analytics/spec.md
117:  `internal/telemetry`, the public `Reader` interface of `internal/manualcharge`, and the public
119:  `internal/manualcharge/db`, or `internal/account/db`
```
→ 1 requirement, "No Cross-Module Database Access" (2 refs, both inside the same scenario body).

**One requirement's title itself contains the old package name**: "Gateway Imports No
manualchargedb Package". OpenSpec's delta mechanism matches `MODIFIED Requirements` by exact
title against the main spec (`normalizeRequirementName`, whitespace-insensitive) — a `MODIFIED`
block whose header text differs from the current title is not a modification, it is a
mismatched requirement that `openspec archive` rejects. The correct operation for a title change
is `RENAMED Requirements`, confirmed by reading the installed CLI's own parser and schema
(`@fission-ai/openspec@1.3.1`, `dist/core/specs-apply.js` and
`schemas/spec-driven/schema.yaml`):

```
## RENAMED Requirements: Name changes only - use FROM:/TO: format
  - FROM: `### Requirement: <old name>`
  - TO: `### Requirement: <new name>`
```

and operations are applied in a fixed order — **RENAMED → REMOVED → MODIFIED → ADDED** — so a
requirement can be renamed and have its body updated in the *same* delta: the `RENAMED` block
renames the header first, then a `MODIFIED` block addressed to the *new* title supplies the
updated body. This change's `specs/gateway/spec.md` therefore contains:

```
## RENAMED Requirements
- FROM: `### Requirement: Gateway Imports No manualchargedb Package`
- TO: `### Requirement: Gateway Imports No chargingdb Package`

## MODIFIED Requirements
### Requirement: Gateway Imports No chargingdb Package
... (full updated body, manualcharge→charging, manualchargedb→chargingdb) ...

### Requirement: Charge List Fragment
... (full updated body — title unchanged) ...

### Requirement: Create Charge Entry
... (full updated body — title unchanged) ...

### Requirement: Inline Row Editing
... (full updated body — title unchanged) ...

### Requirement: Delete Charge Entry
... (full updated body — title unchanged; `csrf_manualcharge` line NOT edited, D3) ...
```

`specs/analytics/spec.md` contains one `MODIFIED Requirements` block ("No Cross-Module Database
Access", title unchanged, body updated). Per schema.yaml's "MUST include full updated content"
rule, both delta files reproduce each requirement's **entire** current body (copied verbatim
from the main spec) with only the `manualcharge`/`manualchargedb` substrings replaced — no
scenario text, WHEN/THEN wording, or requirement boundary is otherwise touched.

**No delta for `manual-charge-log`** — D1.

### D5 — Doc updates: verified against the codebase, not assumed

Every doc in the leader's list, plus the two this document adds (D0/Context), grepped for
actual `internal/manualcharge`/`manualcharge.`/`manualchargedb`/`ManualCharge` module-reference
content, as opposed to the unrelated domain word "manual charge" naming a UI feature (never in
scope — see Non-Goals). Count is `grep -c 'manualcharge' <file>` (case-sensitive, matches the
lowercase package-qualifier/path form only — `ManualCharge*` Go-identifier-only refs inside
these prose docs are covered separately where they occur, e.g. `README.md`'s `manualchargedb`).

| Doc | Raw count | Needs edit? | What |
|---|---|---|---|
| `README.md` | 12 | **Yes, all 12** | sqlc intro paragraph (2: `manualchargedb`, the `internal/{account,telemetry,manualcharge}/...` glob), Project Structure tree (1), Architecture table row (1), Dependency graph code block (7: `cmd/web`, `cmd/poller`, `gateway`, `handlers` import lists, and the `LAYER 1` row `manualcharge ────────────► manualcharge/db` — 2 occurrences on that one line), Database-tables-by-module row (1: `internal/manualcharge \| manualchargedb \| manual_charge_entries \| ...`) |
| `Makefile` | 1 | **Yes** | `MIGRATIONS_DIRS` line 38: `internal/manualcharge/db/migrations` → `internal/charging/db/migrations` |
| `sqlc.yaml` | 7 | **Yes, all 7** | The module's doc-comment block (4 refs: "manualcharge module", "package (manualchargedb)", "internal/manualcharge/service.go's DB→domain boundary") plus the `sql:` entry itself (3: `schema`, `queries` paths, `package: "manualchargedb"`) and its `out:` path (not matched by the bare-word grep but edited alongside — see "What Changes") |
| `.air.toml` | 1 | **Yes — functional, not prose** | `exclude_dir` list, line 28: `internal/manualcharge/db/migrations` → `internal/charging/db/migrations`. Unlike the other docs, this is a live config value the `air` live-reload tool reads; leaving it stale means `air` no longer excludes the new migrations directory from its file watch, causing needless rebuild triggers on every future migration edit. **Missed by the leader's inventory** — caught here by the mandated broad sweep. |
| `docs/0-set-up/running-the-server.md` | 1 | **Yes** | Line 74: `internal/manualcharge/db/migrations` path reference |
| `docs/battery-consumed-graph.md` | 8 | **Yes, 7 of 8** | Table row `manual_charge_entries.*` \| `internal/manualcharge` (1), `manualcharge.ListEntriesByVehicleBetween` (1), three `manualcharge.Writer.*` route-mapping table rows (3), `internal/manualcharge` boundary-proof prose (1), `internal/manualcharge` in Entry 12's module list (1). **Not edited:** the `csrf_manualcharge` CSRF-key mention in the guard-chain prose (D3 exemption) |
| `internal/analytics/AGENTS.md` | 7 | **Yes, all 7** | Allowed-imports section: `manual manualcharge.Reader` constructor param doc, `internal/manualcharge` — `manualcharge.Reader`/`manualcharge.Entry` (3 refs), `internal/manualcharge/db` (`manualchargedb`) forbidden-import line, `manualcharge.Reader` in the testing-notes fake-list mention |
| `internal/gateway/AGENTS.md` | 9 | **Yes, 8 of 9** | `Deps.ManualChargeWriter manualcharge.Writer`/`Deps.ManualChargeReader manualcharge.Reader` bullets and their `internal/manualcharge/db` forbidden-import follow-ons (4), the "Exception: user-initiated writes" section body's `manualcharge.Writer` mentions (2) and its "Only manualcharge.Writer is permitted" line (1), the "Exception: language switch" section's cross-reference "the D4/manualcharge amendment above" (1) — renamed to "D4/charging amendment" for same-file consistency (D3's same-file-consistency reasoning; this doc's own D4-amendment section is being rewritten in the same task). **Not edited:** the `"csrf_manualcharge"` CSRF-key literal inside bullet 3 of the D4-amendment constraints (D3 exemption) |
| `internal/manualcharge/AGENTS.md` | 12 | **Yes — wholesale rewrite as part of D2's move**, not counted separately here (it becomes `internal/charging/AGENTS.md`; every reference updates as part of the file's own rename, mirroring tier1 D5/D9's own-module-AGENTS.md treatment) | |
| `openspec/roadmaps/backlog.md` | 8 | **Yes, 6 of 8** | §2 header ("## 2. manualcharge / telemetry — …" → "## 2. charging / telemetry — …") and its `internal/manualcharge` body mention (2); §3 header ("## 3. manualcharge — …" → "## 3. charging — …"), its "the `manualcharge` module is next touched" prose, and its `internal/manualcharge/db_integration_test.go` file-path mention (3); §"charging data is split across two modules" bullet's `internal/manualcharge` mention (1). **Not edited (historical archived-change-name citations, tier1 D6 precedent):** `` `RM3-manualcharge-add-entries` `specs/manual-charge-log/spec.md` `` and `` `RM3-manualcharge-add-entries` (RM3 tier 1) review finding **R3** `` — both name an already-archived change verbatim (2 refs) |
| `openspec/specs/manual-charge-log/spec.md` | 1 | **No — verified zero edits** | The sole match is the historical `## Purpose` citation of the archived change name `RM3-manualcharge-add-entries` (D1) |
| `openspec/specs/gateway/spec.md` | 25 | **Yes, 24 of 25, via `RENAMED`+`MODIFIED` delta** | See D4. The 25th (`csrf_manualcharge` in "Delete Charge Entry") is the D3 exemption |
| `openspec/specs/analytics/spec.md` | 2 | **Yes, via `MODIFIED` delta** | See D4 |
| `openspec/roadmaps/RM29-modular-monolith-boundaries.md` | 3 | **No — verified zero content edits** | Line 26 (Problem statement: "internal/battery and internal/manualcharge have outgrown their names") and line 44 (the D3 decision-table row recording "Rename manualcharge → charging and battery → analytics") are the roadmap's own frozen planning record — tier 1 already established this precedent by leaving its own identical "internal/battery" mentions in these same two lines untouched after archiving, even though tier 1 itself renamed that module. Line 99 (the T2 tier-table row, "`manualcharge`→`charging`") is a permanent transition description, correct forever once this tier completes, exactly like tier 1's own still-unedited "`battery`→`analytics`" T1 row. The leader's own pipeline bookkeeping (flipping the T2 status cell `[ ]`→`[~]`) touches a different part of the same row and is not a content edit driven by this rename. |
| `.agents/skills/kkpa-goth-scaffold-ui/references/charges-slice-pattern.md` | 1 | **Yes** | "the gateway will call (e.g. `telemetry.Reader`, `manualcharge.Reader`/`Writer`, …)" — a scaffold-template reference doc read by a future agent building new gateway slices; left stale it would instruct that agent to write code against a package that no longer exists. (The `.claude/skills/...` copy of this same file is untracked — per the dispatch, not a task.) |
| `internal/gateway/handlers/lang.go` | 1 | **Yes — missed by the leader's inventory** | Line 107's doc comment: "a deliberate, user-approved divergence from the manualcharge/D4 write-exception pattern" — cross-references the exact same live pattern-label being renamed in `internal/gateway/AGENTS.md` (this doc's own row, above). Same-file-consistency reasoning: leaving this one stale after renaming its own referent elsewhere would recreate the "two names for one thing" problem. |
| `internal/testdb/testdb.go` | 1 | **Yes — missed by the leader's inventory** | Line 2's doc comment: "used by the integration tests across modules (account, manualcharge, telemetry)" → "(account, charging, telemetry)" |

`cmd/README.md` was checked and carries no `manualcharge` reference — not listed as a task
(same negative-check discipline as tier1 D6).

### D6 — Telemetry migration file: leave stale, matching tier 1's own precedent

`internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql:45` carries a hand-written
SQL comment: `"... mirroring manual_charge_entries'/supercharger_sessions' identical precedent
(internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql:10-14, ...)"`.
This is inside an **already-applied migration file**. Two facts settle this without a new
judgment call:

1. **Tier 1 already established the precedent.** The same file, six lines earlier, still reads
   `"...internal/battery's derivation..."` and `"...the only writer (internal/battery, via
   cmd/poller, D4/D4a)..."` — tier 1's own design.md D6/2.7 explicitly excluded this file's
   prose from its doc-update sweep (deferring telemetry's sqlc-**generated** comments to tier
   5, and never mentioning this hand-written migration comment at all), and tier 1's
   implementation left it exactly as it was. `internal/battery` no longer exists as a package,
   yet this file still names it — proof the project already tolerates historical/rationale
   prose inside applied migrations going stale across a rename.
2. **Consistency**: applying a different rule to the `internal/manualcharge` mention four lines
   away, in the same comment block, in the same file, would produce a migration file with one
   stale module reference and one fresh one — a worse outcome than leaving both stale until
   whichever future tier next touches this table's migrations (tier 5, `RM29-analytics-own-charge-gaps`,
   which moves `charge_gaps` out of telemetry and rewrites these comments anyway, per tier1's
   own 2.7 note).

**Decision:** leave `internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql`
untouched. No task in tasks.md targets it.

### D7 — Database gate: not applicable, explicitly

Per the leader's pre-verified fact, confirmed independently (`find internal/manualcharge -type f`
lists two migrations and three sqlc-generated files, none of whose *content* this tier
proposes to change; `internal/testdb/testdb.go:124`'s `goose.NewProvider` call and
`Makefile:26-31`'s own comment both confirm goose tracks applied migrations by version number
in the shared `goose_db_version` table, not by directory path): this tier changes no database
object of any kind. `openspec/config.yaml`'s database design gate ("Any new/changed database
object... MUST include the full schema, the rationale... and an index plan") is **not
triggered** — there is no schema, no rationale, and no index plan to write, because nothing
about persistence changes. Stated explicitly, per tier1 D7's own precedent, rather than a
silent gap a reviewer has to independently re-check.

**The one thing that DOES need re-verification post-move**: after `internal/manualcharge/db/`
moves and `sqlc.yaml` is re-pointed, run `make sqlc` and confirm the regenerated
`internal/charging/db/{db,models,query.sql}.go` are byte-identical to the moved originals except
for the `package chargingdb` clause (i.e. `git diff` after `make sqlc` shows only the package
line changing, not any struct/method body) — this is the concrete verification step for the
"goose/sqlc safety" risk (see Risks).

### D8 — Test contract: not applicable, explicitly

`ai/go-conventions.md`'s "Authoring order" rule (author expected integration-test values in
design.md up front) has nothing to author here: this tier writes **zero new tests** (roadmap
D10; proposal Testing section). The existing test files move/are edited only for package
clause, import path, qualifier, and the local-name renames enumerated in D3 — no assertion,
fixture, or test-scenario change. Stating this explicitly, as instructed, rather than silently
omitting the section.

### D9 — sqlc regeneration mechanics: hand-move first, `sqlc generate` second, never hand-author the generated diff

Because this module (unlike tier 1's `battery`) owns generated code, tasks.md's Wave 1 does two
things in order, not one: (a) `mv` the three generated files (`db.go`, `models.go`,
`query.sql.go`) alongside the hand-written ones and fix only their `package` clause by hand, so
the tree compiles immediately after the move; (b) **separately**, after `sqlc.yaml`'s `schema`/
`queries`/`out` paths are re-pointed (a leader task — sqlc.yaml lives outside the module), run
`make sqlc` and diff the result. If `make sqlc`'s output differs from the hand-edited files by
more than the package clause, that is a signal something else changed (e.g. sqlc version drift,
or a query file edited unintentionally) and must be investigated before proceeding — it is
**not** expected, and this tier does not intentionally introduce any such difference. This
two-step approach (hand-move to keep the build green, then regenerate to prove correctness)
avoids ever hand-authoring what should be generated output, per `ai/go-conventions.md`'s
persistence conventions ("sqlc generates... No other module imports it").

### D10 — Performance profile: satisfied trivially

The project's read-heavy Performance-Profile is not engaged by this change: "Read Paths
Affected" is `None` per the proposal, confirmed here — `Writer`/`Reader`'s SQL text in
`query.sql` is not edited, only its containing directory moves, and `make sqlc` regenerates
byte-identical Go code from it (D9). Every query `charging.Reader`/`charging.Writer` issues
through `chargingdb.Queries` is byte-for-byte identical before and after. Stated explicitly, per
instruction, rather than omitted.

## Risks / Trade-offs

- **Cross-module blast radius for a single-module worker.** Nine files outside
  `internal/charging` need edits (vs. tier 1's six), none of which the `manualcharge` module
  worker may edit (sandbox rule). tasks.md groups every out-of-module edit into leader-owned
  tasks in the same atomic wave as the module's own rename — see tasks.md Wave 2.
- **sqlc/goose ordering risk, unique to this tier (tier 1 had no database).** If `sqlc.yaml` is
  re-pointed before the `db/` folder physically moves, `sqlc generate` fails (source paths don't
  exist yet); if `make sqlc` is run before `MIGRATIONS_DIRS`/`.air.toml` are updated, the build
  still succeeds (sqlc reads `sqlc.yaml` directly, not the Makefile var) but `make db-setup`/
  `make migrate-up` would silently stop applying this module's migrations from their old,
  now-stale path. tasks.md orders these three (folder move → sqlc.yaml re-point → `make sqlc`
  regenerate-and-diff) as a strict sub-sequence within Wave 1/2, and the Makefile/`.air.toml`
  edits are scheduled in the same wave, not deferred.
- **Silent identifier drift if the lowercase `manualChargeWriter`/`manualChargeReader` fields
  are missed.** These 10 occurrences fall outside the leader's capital-M-anchored inventory
  entirely (D3) — a literal reading of only the leader's 73-count could skip them and still
  compile (Go doesn't care what an unexported field is named), recreating the exact
  "two names for one thing" vocabulary drift RM29 exists to remove. tasks.md 2.4 makes this
  explicit.
- **`RENAMED Requirements` is a less-common OpenSpec operation than `MODIFIED`.** Verified
  directly against the installed CLI's parser (D4) rather than assumed from the schema
  template's `ADDED`-only example, specifically because this is the first RM29 tier where a
  requirement's own *title* contains the module name being renamed — tier 1 never hit this case.
- **`gofmt`/import-grouping churn.** Renaming an import path changes where `goimports` would
  sort the import block in each of the nine call-site files. `charging` sorts between
  `account`/`analytics` and `googleauth`/`gateway`/`tesla`/`telemetry` alphabetically — verified
  against the existing groups in each of the nine files, no reordering needed beyond the renamed
  line itself changing position from where `manualcharge` sorted (which was also mid-alphabet,
  between `googleauth` and `telemetry` in most import blocks — `charging` sorts one slot earlier,
  before `gateway`/`googleauth`, so each block's ordering does shift by one line; `gofmt -l` is
  expected to report the touched files until `goimports`/`gofmt -w` is run on them, which is
  itself part of Wave 5's verification task, not a surprise).

## Verification signals

Per the Test-Execution-Policy (verbatim in every dispatch): the assistant may run and report on
`go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, `make sqlc`
(explicitly allowed — codegen, not the test suite), and the standalone guards
(`ui-guard`/`i18n-guard`/`money-guard` — expected no-ops here, same reasoning as tier 1: no
gateway HTML, no new user-facing string, no monetary field touched). Additionally,
`openspec validate --changes --strict` is available (an OpenSpec artifact-correctness check).
The owner alone runs `go test ./...` (or the narrower
`go test ./internal/charging/... ./internal/analytics/... ./internal/gateway/... ./cmd/...`)
and reports the result — until they do, this tier's implementation status is
**awaiting-user-verification**, never "done."
