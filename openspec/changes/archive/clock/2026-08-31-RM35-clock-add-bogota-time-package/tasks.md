> **Scope.** Creates `internal/clock` — a brand-new, stdlib-`time`-only package (roadmap
> `RM35-timezone-centralization`, tier 1 of 7). Not breaking: zero existing call sites change,
> because nothing imports the package yet (tiers 2–6 do that, each a separate dependent
> change). No migration, no schema, no config change. Adds `Zone()`, `Now()`,
> `LoadOrDefault(name)`, `CalendarDay(t, loc)`, their unit tests (roadmap D6, tier-1-only), the
> module's `AGENTS.md`, and the D3 doc edits (`ai/go-conventions.md`, `CLAUDE.md`, root
> `AGENTS.md`, root `README.md`).
>
> **Dependencies / parallelism:**
> - T1 (`clock.go` — `Zone`/`Now`/`LoadOrDefault` + its tests) has no dependencies. Disjoint
>   files from T2; MAY run in parallel with it.
> - T2 (`calendar.go` — `CalendarDay` + its tests) has no dependencies. Disjoint files from T1;
>   MAY run in parallel with it.
> - T3 (`internal/clock/AGENTS.md`) depends on **both** T1 and T2 — it documents the package's
>   final public signatures.
> - T4 (`ai/go-conventions.md` edit) depends on T1 and T2 (same reason as T3). Disjoint file from
>   T3, T5, T6; MAY run in parallel with them.
> - T5 (`CLAUDE.md` one-line pointer) depends on T4 — it points at the rule T4 writes, so T4's
>   exact section heading must exist first. Disjoint file from T3, T4, T6; MAY run in parallel
>   with T3 and T6 once T4 lands.
> - T6 (root `AGENTS.md` new pointer) has no dependency on the package's signatures — it only
>   adds a pointer to `ai/go-conventions.md` as a file, not to any specific rule inside it. MAY
>   run at any time, in parallel with everything else.
> - T7 (root `README.md` structure tree + architecture table) depends on T1 and T2 (needs the
>   package's real responsibility to describe accurately). Disjoint file from T3–T6; MAY run in
>   parallel with them.
> - T8 (verification) depends on T1–T7.
>
> **Leader-integrated step:** none — this tier adds no database object and regenerates no
> codegen (`sqlc`, `templ`, etc.).

## T1. `internal/clock/clock.go` — `Zone`, `Now`, `LoadOrDefault` — no dependencies, parallel-ok with T2

- [x] T1.1 Create `internal/clock/clock.go` with a package doc comment stating the module's one
      defining constraint: it imports stdlib `time` and **nothing else, with no exception**,
      and is never extended with an unrelated helper (design.md D2).
- [x] T1.2 Implement `Zone() *time.Location`, computed once via a package-level
      `time.LoadLocation("America/Bogota")`. **Do NOT blank-import `time/tzdata`** — the owner
      settled this on 2026-08-30 (design.md D9): the project ships no containers, every
      deployment target already has a system tzdata, and D2's stdlib-`time`-only rule is worth
      more absolute than with one standing exception. Panic if the load errors (design.md D9 —
      a silent UTC fallback would defeat the roadmap without anyone noticing).
      Acceptance: `clock.Zone().String() == "America/Bogota"`, and the file's import block
      contains exactly one import, `"time"`.
- [x] T1.3 Implement `Now() time.Time` as `time.Now().In(Zone())`.
      Acceptance: `clock.Now().Location().String() == "America/Bogota"` and the returned instant
      matches `time.Now()` within ordinary test-execution slack.
- [x] T1.4 Implement `LoadOrDefault(name string) *time.Location` — `time.LoadLocation(name)`,
      falling back to `Zone()` only on a non-nil error (design.md D10 — no special-casing beyond
      what `time.LoadLocation` itself already does for `""`/`"UTC"`/`"Local"`).
      Acceptance: matches every case in design.md's Test Contract `LoadOrDefault` table,
      including the two gotcha cases (`""` → `"UTC"`, `"Local"` → host-local, neither → Bogota).
- [x] T1.5 `internal/clock/clock_test.go` — transcribe design.md's Test Contract cases for
      `Zone()`, `Now()`, and `LoadOrDefault()` verbatim into table-driven or individual test
      functions.
      Acceptance: `go vet ./internal/clock/...` compiles the test file cleanly (this tier does
      not run the tests — `Test-Execution-Policy`).

## T2. `internal/clock/calendar.go` — `CalendarDay` — no dependencies, parallel-ok with T1

- [x] T2.1 Implement `CalendarDay(t time.Time, loc *time.Location) time.Time` exactly per
      design.md D7: `y, m, d := t.In(loc).Date(); return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)`.
      No nil-guard on `loc` — passing `nil` panics exactly as `t.In(nil)` would (design.md D7's
      explicit "no hidden default substitution" rationale). Doc comment states this is the
      single replacement for the three now-collapsed truncators (design.md "Context" — note the
      correction that gateway's two roadmap-listed line references are one shared function, not
      two).
      Acceptance: matches every one of design.md's four `CalendarDay` Test Contract cases exactly
      (UTC self-truncation, Bogota cross-day, DST same-day either side of the transition, DST
      cross-day).
- [x] T2.2 `internal/clock/calendar_test.go` — transcribe design.md's four `CalendarDay` cases
      verbatim, including the DST cases against `America/New_York` and the Bogota
      always-UTC-5-no-DST case.
      Acceptance: `go vet ./internal/clock/...` compiles the test file cleanly (owner runs it —
      `Test-Execution-Policy`).

## T3. `internal/clock/AGENTS.md` — depends on T1 and T2

- [x] T3.1 Create `internal/clock/AGENTS.md` mirroring the shape of `internal/account/AGENTS.md`
      (the project's gold-standard module doc): an `Agent-Name: clock` header, a
      `## Doc-Pack (module)` section (empty — `internal/clock` needs nothing beyond the base
      pack), the module's responsibility (the platform's single default time zone and calendar-
      day normalization), its public interface (`Zone`, `Now`, `LoadOrDefault`, `CalendarDay` —
      final signatures from T1/T2), its one import rule stated as the module's defining
      constraint ("stdlib `time` only — never add an unrelated helper here, no matter how small"),
      data ownership (none — this module owns no table, no config, no external state), and
      testing notes (pure, offline, table-driven, no DB, no container).
      Acceptance: every exported symbol T1/T2 produced is documented; the import rule is stated
      as a standalone, unmissable sentence an agent or reviewer can check by inspection.

## T4. `ai/go-conventions.md` §Coding Rules — depends on T1 and T2, parallel-ok with T3/T5/T6/T7

- [x] T4.1 Add the full time-zone convention to `ai/go-conventions.md` §Coding Rules,
      immediately next to the existing `_km`/`_c`/`_psi` display-units rule (design.md D3 — same
      shape of cross-cutting value convention, in the file every worker and reviewer already
      re-reads on every dispatch per the pipeline's base Doc-Pack). State: `internal/clock` owns
      the platform's default zone (`America/Bogota`); a raw `time.Now()`, a hard-coded zone
      fallback, or a hand-rolled UTC-midnight truncator outside `internal/clock` is non-compliant
      once the adopting tiers (RM35 tiers 2–6) land; `cmd/*` is exempt (the composition root,
      already calling `time.LoadLocation` explicitly and on purpose — roadmap D4).
      Acceptance: the new rule sits in the same §Coding Rules section, in the same prose style,
      as the existing units rule it mirrors.

## T5. `CLAUDE.md` one-line non-negotiable — depends on T4, parallel-ok with T3/T6/T7

- [x] T5.1 Add one line to `CLAUDE.md`'s "Non-negotiables" list (next to the existing
      `_km`/`_c`/`_psi` bullet) pointing at the full rule T4 just wrote in
      `ai/go-conventions.md` (design.md D3).
      Acceptance: one bullet, consistent in length and style with the existing non-negotiables
      list; it links to the section T4 created rather than restating the rule in full (the
      roadmap explicitly rejects duplicating the full rule into both root files).

## T6. Root `AGENTS.md` — new pointer to `ai/go-conventions.md` — no dependencies, parallel-ok with everything

- [x] T6.1 Add a pointer from the root `AGENTS.md` to `ai/go-conventions.md` — a gap the roadmap
      identifies explicitly (`AGENTS.md` has no such pointer today, which is why the time-zone
      rule cannot live only in `CLAUDE.md` per D3). Keep it to a short paragraph or bullet
      consistent with `AGENTS.md`'s existing prose style (it is a mission/vision/principles
      document, not a rules list like `CLAUDE.md` — do not restructure it into one).
      Acceptance: `ai/go-conventions.md` is referenced at least once, by path, from
      `AGENTS.md`.

## T7. Root `README.md` — Project Structure tree + Architecture table — depends on T1 and T2, parallel-ok with T3–T6

- [x] T7.1 Add `internal/clock/` to the "Project Structure" tree (`README.md` §Project
      Structure), in whichever position matches the tree's existing ordering convention.
- [x] T7.2 Add a row for `internal/clock` to the "Architecture" table (`README.md` §Architecture),
      one line, describing it as the platform's default-time-zone-and-calendar-day-normalization
      package — mirroring the terse, one-line style every other row in that table already uses.
      Acceptance: both edits land in the same change per `CLAUDE.md`'s docs-track-structural-
      change rule (a new module, same change, never a follow-up).

## T8. Verification — depends on T1–T7

- [x] T8.1 `go build ./...` and `go vet ./...` pass repo-wide.
- [x] T8.2 `gofmt -l` reports no diffs for any file this tier touched.
- [x] T8.3 Boundary check: every file in `internal/clock` imports **only** `time` (no
      `time/tzdata` — D9) — confirm by inspecting the import blocks, not by running
      `make tz-guard` (tier 7, does not exist yet). No file outside `internal/clock` and the four doc files (`ai/go-conventions.md`,
      `CLAUDE.md`, `AGENTS.md`, `README.md`) was touched.
- [x] T8.4 Confirm zero adopting call sites exist yet — `grep -rl "internal/clock" --include=*.go
      internal/ cmd/` (outside `internal/clock` itself) returns nothing. This is the concrete
      check that the "not breaking" claim in `proposal.md` holds.
- [x] T8.5 `openspec validate RM35-clock-add-bogota-time-package --strict` passes and every
      tasks.md checkbox above reflects real completion.
- [x] T8.6 Report the exact test-suite commands the owner must run
      (`go test ./internal/clock/...` and the full `go test ./...`) — this tier writes tests but
      does not execute them (`Test-Execution-Policy`); the owner's run is what turns T1.5/T2.2
      from `awaiting-user-verification` into `done`.
