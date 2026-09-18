> **Scope.** Adds the delta-column naming rule (`ai/go-conventions.md`) and
> `make delta-guard` — tier 1 of `RM66-travel-progress-trends` (D-D, D-E). No Go
> logic change, no database object. This dispatch wrote the OpenSpec artifacts
> only; the tasks below are for the implementation dispatch that follows.
>
> **Dependencies / parallelism:**
> - T1 (`Makefile` — `delta-guard` target, `.PHONY`, `check` wiring) has no
>   dependencies. Disjoint file from T2/T3; MAY run in parallel with them.
> - T2 (`ai/go-conventions.md` — the naming rule prose) has no dependencies.
>   Disjoint file from T1/T3; MAY run in parallel with them.
> - T3 (docs: `README.md`, `CLAUDE.md`) has no dependencies. Disjoint files from
>   T1/T2; MAY run in parallel with them.
> - T4 (verification) depends on **T1** — the guard cannot be proven to pass or
>   fail until the target exists. Does not depend on T2/T3.
>
> **Leader-integrated step:** none — this tier adds no database object and
> regenerates no codegen (`sqlc`, `templ`, etc.).

## T1. `Makefile` — `delta-guard` target, `.PHONY`, `check` wiring — no dependencies, parallel-ok with T2/T3

- [ ] T1.1 Before writing the target, re-run design.md's two measurement greps
      against the then-current tree and diff the result against design.md
      D-plat-5/D-plat-6's baseline lists. If the tree has changed since this
      proposal (a later archived change added or renamed a `_calc` name),
      update the baseline lists to match reality — do not implement a stale
      baseline. Record what you found, even if it matches exactly.
- [ ] T1.2 Add a `delta-guard:` target after `naming-guard:`, mirroring its
      `@pattern=... baseline=... hits=... warn=... fail=...` shape (two legs —
      SQL and Go — each with its own pattern/baseline pair, design.md D-plat-1
      through D-plat-6):
      - SQL leg: `pattern='^[[:space:]]*(ADD COLUMN[[:space:]]+)?[a-z][a-z0-9_]*_calc\b'`
        scanning `--include='*.sql' internal/*/db/migrations`, excluding matches
        of `deltapattern='^[[:space:]]*(ADD COLUMN[[:space:]]+)?[a-z][a-z0-9_]*_delta_calc\b'`
        and lines containing `delta:allow`.
      - Go leg: `gopattern='^\t+[A-Z][A-Za-z0-9]*Calc\b'` scanning
        `--include='*.go' internal cmd`, excluding `_test.go` files, matches of
        `deltago='^\t+[A-Z][A-Za-z0-9]*DeltaCalc\b'`, and lines containing
        `delta:allow`.
      - Baseline regex per leg from T1.1's re-verified lists (design.md D-plat-5
        has the starting point: 10 SQL names, 15 Go names).
      - Combine both legs' warn/fail the way `naming-guard` combines its single
        leg: print warnings, then print failures and `exit 1` if any fail
        bucket is non-empty, else print a success line naming the current
        baseline size (mirrors `naming-guard`'s own success message, which
        reports how many legacy names remain).
      Acceptance: matches the existing guards' error-message shape (what was
      found, why, how to fix, how to mark a genuine exception, "never weaken
      this pattern to silence a true positive").
- [ ] T1.3 Add `delta-guard` to the `.PHONY` list, in the existing guard cluster.
- [ ] T1.4 Update `check:` to insert `delta-guard` immediately before `test`
      (design.md D-plat-7), and update `check:`'s own `##` help comment to list
      it. Acceptance: `grep -n '^check:' Makefile` shows `delta-guard`
      immediately before `test`.

## T2. `ai/go-conventions.md` — the naming rule — no dependencies, parallel-ok with T1/T3

- [ ] T2.1 Add the delta-column naming rule as its own bullet beside the
      existing "Unit-bearing columns are named with their unit suffix" bullet
      (~line 370, under "Read optimization (project-wide)"). State: a column or
      Go field storing a day-over-day change (today's value of a metric minus
      yesterday's value of the same metric) carries `_delta_calc`
      (Go: `DeltaCalc`), never a bare `_calc`/`Calc`. A bare `_calc`/`Calc` name
      stays valid for a derived value that is not itself a day-over-day
      difference (name `km_per_pct_calc` and `inferred_capacity_kwh_calc` as the
      two existing examples). Point at `make delta-guard` as the enforcement
      mechanism and at `openspec/specs/unit-of-measure/spec.md` for the full
      rule.
      Acceptance: the bullet is additive prose next to the existing unit-suffix
      bullet — it does not restate or weaken that bullet, mirroring the
      anti-duplication precedent `RM35-platform-add-tz-guard` tasks.md T3.4
      followed for its own conventions-doc edit.

## T3. Docs — no dependencies, parallel-ok with T1/T2

- [ ] T3.1 `README.md`'s `make check` composition comment (`README.md:93-94`):
      add `+ delta-guard` in the correct position (before `+ test`). This
      comment is already missing `logging-guard`, `naming-guard`, and
      `logdir-guard` — a pre-existing drift outside this tier's scope; add only
      this tier's own entry, do not attempt the unrelated backfill.
- [ ] T3.2 `CLAUDE.md` "Builds & local checks — PROJECT OVERRIDE" section: add
      `make delta-guard` to the allowed-guards bullet, and to the "its other
      phases" sentence's guard list.
- [ ] T3.3 `CLAUDE.md` "Pipeline config" §Test-Execution-Policy block: add
      `make delta-guard` to the declarative policy text's allowed-guards list,
      so later pipeline dispatches inherit it automatically.
      Acceptance: every edit is additive to an existing list — no rule is
      restated in conflicting terms or duplicated in full across files.

## T4. Verification — depends on T1

- [ ] T4.1 Run `make delta-guard` on the current tree (after T1 lands). MUST
      pass — zero failures, exit 0 (warnings for the baseline names are
      expected and fine). Record the exact output.
- [ ] T4.2 Prove the guard detects a violation, in both directions (design.md
      Test Contract (c)): temporarily add one new bare `_calc` SQL column
      definition and one new bare `Calc` Go field declaration (design.md's
      `odometer_km_calc` / `OdometerKmCalc` examples work) to scratch locations,
      run `make delta-guard`, confirm it fails and names the line, then remove
      the scratch additions and re-run to confirm it is clean again. Record
      both outputs.
- [ ] T4.3 `go build ./...`, `go vet ./...`, `gofmt -l` — confirm no diff and no
      error from the doc/prose edits (Test-Execution-Policy: these ARE run by
      the implementer; `go test`/`make test`/`make check` are NOT).
- [ ] T4.4 `openspec validate RM66-platform-add-delta-column-naming-rule --strict`
      passes.
- [ ] T4.5 Confirm no database object was touched: `git status` shows no file
      under any `db/migrations/` directory.
      Acceptance: report the exact suite commands (`make test`, `make check`)
      the owner must run to move this tier from `awaiting-user-verification` to
      `done` — this tier adds no new `_test.go` coverage, per its own
      Non-Goals.
