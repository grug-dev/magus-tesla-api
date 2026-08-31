> **Scope.** Adds `make tz-guard` — the final tier of `RM35-timezone-centralization` (D5). No
> Go logic changes: a new Makefile target, six comment-only `// tz:allow: <reason>` markers, and
> doc updates. No database object. This tier produces its own OpenSpec artifacts AND its
> implementation in one dispatch (mirroring tiers 2–6).
>
> **Dependencies / parallelism:**
> - T1 (`Makefile` — `tz-guard` target, `.PHONY`, `check` wiring) has no dependencies. Disjoint
>   file from T2/T3; MAY run in parallel with them.
> - T2 (six `// tz:allow:` markers in `internal/gateway/handlers/{tz,supercharger}.go`) has no
>   dependencies — the markers are additive comments derived from design.md D-plat-6, not from
>   T1's exact grep text. Disjoint files from T1/T3; MAY run in parallel with them.
> - T3 (docs: `README.md`, `CLAUDE.md`, `ai/go-conventions.md`) has no dependencies. Disjoint
>   files from T1/T2; MAY run in parallel with them.
> - T4 (verification: both-directions proof, `go build`/`go vet`/`gofmt`, `openspec validate`)
>   depends on **T1 and T2** — the guard cannot be proven to pass on the current tree until both
>   the target and the markers exist. Does not depend on T3.
>
> **Leader-integrated step:** none — this tier adds no database object and regenerates no
> codegen (`sqlc`, `templ`, etc.).

## T1. `Makefile` — `tz-guard` target, `.PHONY`, `check` wiring — no dependencies, parallel-ok with T2/T3

- [x] T1.1 Add a `tz-guard:` target after `money-guard:`, mirroring its `@if grep ... ; then
      ... exit 1; else ... fi` shape. Three grep legs (design.md D-plat-1), each scanning
      `internal --include='*.go'` and excluding `_test\.go:`, `^internal/clock/`, lines
      containing `tz:allow`, and comment-only lines (content starting with `//` after the
      `file:line:` prefix, via `grep -vE '^[^:]+:[^:]+:[[:space:]]*//'` — **use `[^:]+`, not
      `[^:]*`**, per design.md D-plat-4's portability note; verify on this host before trusting
      it):
      1. Raw `time.Now()`: `grep -rnE 'time\.Now\(\)'`.
      2. Midnight-of-a-day construction: `grep -rnE ',[[:space:]]*0,[[:space:]]*0,[[:space:]]*0,[[:space:]]*0,[[:space:]]*[A-Za-z_][A-Za-z0-9_.]*\)'`.
      3. Hardcoded IANA zone literal: `grep -rnE '"[A-Z][a-zA-Z_]+/[A-Z][a-zA-Z_]+"'`.
      Combine with a `fail=0` accumulator (one leg's match sets `fail=1`; do not `exit` early on
      the first leg, so a single run reports every violation across all three legs at once).
      Acceptance: matches money-guard's error-message shape (what was found, why, how to fix,
      how to mark a genuine exception, "never weaken this pattern to silence a true positive").
- [x] T1.2 Add `tz-guard` to the `.PHONY` list, in the existing guard cluster
      (`ui-guard i18n-guard money-guard`).
- [x] T1.3 Update `check:` to `build vet ui-guard i18n-guard money-guard tz-guard test` — `test`
      stays last (design.md D-plat-7 / roadmap D5).
      Acceptance: `grep -n '^check:' Makefile` shows `tz-guard` immediately before `test`.

## T2. Six `// tz:allow: <reason>` markers — no dependencies, parallel-ok with T1/T3

- [x] T2.1 `internal/gateway/handlers/tz.go:108` (`browserToday`'s `time.Now()`) — append a
      trailing `// tz:allow: <reason>` citing design D-gw-3 of `RM35-gateway-adopt-clock` (a
      provable no-op swap already covered by `browserLocation`'s fallback).
- [x] T2.2 `internal/gateway/handlers/tz.go:94` (`startOfDayIn`'s `time.Date(...)`) — append a
      trailing `// tz:allow: <reason>` citing design D-gw-2 of `RM35-gateway-adopt-clock`
      (zone-parameterized, a different representation than `clock.CalendarDay`).
- [x] T2.3 `internal/gateway/handlers/supercharger.go:59` (`startOfMonth`'s `time.Date(...)`) —
      append a trailing `// tz:allow: <reason>` citing month-vs-day granularity and the
      roadmap's 2026-08-30 correction note.
- [x] T2.4 `internal/gateway/handlers/supercharger.go:134` and `:169` (both already carry
      `// design.md D9a — plain UTC, NOT browserToday(c)`) — extend the existing trailing
      comment to also include the literal substring `tz:allow` and a one-clause reason (design
      D9a of `RM30-gateway-read-supercharger-stats-from-charging`), rather than adding a second
      comment.
- [x] T2.5 `internal/gateway/handlers/supercharger.go:201` — this site currently has **no**
      trailing comment at all (unlike its two siblings at T2.4). Add the SAME explanatory
      comment those two carry, extended with the `tz:allow` marker, so all three
      `startOfDay(time.Now().UTC())` sites read identically.
      Acceptance (T2 overall): zero Go logic changes (`git diff` on these two files shows only
      comment text added/extended, confirmed by reading the diff, not by running tests);
      `gofmt -l` reports no diff on either file.

## T3. Docs — no dependencies, parallel-ok with T1/T2

- [x] T3.1 `README.md` line documenting `make check`'s composition
      (`# build + vet + ui-guard + i18n-guard + money-guard + test`) — add `+ tz-guard` in the
      correct position (before `+ test`).
- [x] T3.2 `CLAUDE.md` "Builds & local checks — PROJECT OVERRIDE" section: add `make tz-guard`
      to the allowed-guards bullet (next to `ui-guard`/`i18n-guard`/`money-guard`); update the
      "its other five phases" sentence to six and list `tz-guard`; extend the testing
      non-negotiable's guard list the same way.
- [x] T3.3 `CLAUDE.md` "Pipeline config" §Test-Execution-Policy block: add `make tz-guard` to
      the declarative policy text's allowed-guards list, so future pipeline dispatches inherit
      it (this tier's own dispatch already assumed tz-guard belongs there).
- [x] T3.4 `ai/go-conventions.md`: add `make tz-guard` to the "Testing — who writes them, who
      runs them" table's guards row and its accompanying "five individually" sentence (→ six);
      add one sentence to the time-zone convention paragraph (§Coding Rules) pointing at
      `make tz-guard` as the mechanism that now enforces it repo-wide (dispatch instruction: "Check
      whether ai/go-conventions.md's timezone rule should also point at the guard").
      Acceptance: every edit is additive/corrective prose in an existing section — no rule is
      weakened, restated in conflicting terms, or duplicated in full across files (mirrors tier
      1's D3 anti-duplication precedent).

## T4. Verification — depends on T1 and T2

- [x] T4.1 Run `make tz-guard` on the current tree (after T1 and T2 land). MUST pass — zero
      output, exit 0. Record the exact output.
- [x] T4.2 Prove the guard actually detects a violation, in BOTH directions (dispatch's explicit
      requirement): temporarily introduce one instance of each of the three violation shapes
      (design.md Test Contract (b)) into a scratch file under `internal/` (or via a crafted
      string piped through the same grep, if a scratch `.go` file risks breaking `go build`),
      run `make tz-guard`, confirm it fails and names the offending line, then remove the
      scratch violation and re-run `make tz-guard` to confirm it is clean again. Record both the
      failing and the passing output.
- [x] T4.3 `go build ./...`, `go vet ./...`, `gofmt -l` — confirm no diff and no error introduced
      by the six comment-only edits or the doc edits (Test-Execution-Policy: these ARE run by
      the implementer; `go test`/`make test`/`make check` are NOT).
- [x] T4.4 `openspec validate RM35-platform-add-tz-guard --strict` passes.
- [x] T4.5 Confirm no database object was created or changed (design.md D-plat-8): `git status`
      shows no file under any `db/migrations/` directory.
      Acceptance: report the exact suite commands (`make test`, `make check`) the owner must run
      to turn this tier's status from `awaiting-user-verification` (if any test-adjacent doubt
      remains) to `done` — though this tier adds no new `_test.go` coverage, per its own
      Non-Goals.

## T5. Review round 1 findings (R1-R5) — depends on T4

- [x] T5.1 R1 (major) FIXED, not just documented: added leg 4 to `tz-guard` catching
      day-scale `Truncate` — `.Truncate(24 * time.Hour)` and `.Truncate(time.Hour * 24)`.
      Acceptance: proved both orderings are flagged and that a sub-day `Truncate`
      (`time.Microsecond`) is NOT, so the 33 existing test-file truncations stay clean.
      Verified no `.Truncate(` exists in non-test `internal/` code today, so the new leg
      breaks nothing.
- [x] T5.2 R2-R5 documented as known blind spots in design.md's Risks / Trade-offs, with
      each marked FIXED or Open and a reason. R3 is noted as partly INTENDED — the
      `nowFn := time.Now` seam is a testability pattern the project uses on purpose.
      Acceptance: the table is declared part of the guard's contract, to be extended
      whenever a new evasion is found.
- [x] T5.3 Recorded plainly that the guard would NOT have caught tier 6's six bugs: those
      were frame mismatches between two legitimately-obtained values, not illegitimate
      calls. That class needs a compiler-checkable type, not a grep.

