> **Scope.** Changes the `POLLER_TIMEZONE` unset-fallback in `internal/config/config.go` from
> the literal `"Local"` to the platform default obtained from `internal/clock` (roadmap
> `RM35-timezone-centralization`, tier 2 of 7, depends on tier 1 — already archived). One
> production file changed, one new test file, one new `AGENTS.md`. No migration, no schema, no
> `cmd/` edit.
>
> **Dependencies / parallelism:**
> - T1 (`config.go` fallback + `pollerTimezoneOrDefault`) has no dependencies beyond tier 1
>   (`internal/clock`, already on disk). Must land before T2 and T3.
> - T2 (`timezone_test.go`) depends on T1 — it tests the function T1 introduces.
> - T3 (`internal/config/AGENTS.md`) depends on T1 — it documents the final public surface
>   (including the new import) T1 produces. Disjoint file from T2; MAY run in parallel with it.
> - T4 (doc sweep — confirm no stale "Local" default elsewhere) has no dependency on T1–T3; MAY
>   run at any time. (Completed as a check: no doc found stating the old default — see
>   `proposal.md`.)
> - T5 (verification) depends on T1–T4.
>
> **Leader-integrated step:** none — this tier adds no database object and regenerates no
> codegen (`sqlc`, `templ`, etc.).

## T1. `internal/config/config.go` — swap the `POLLER_TIMEZONE` fallback — depends on tier 1 (`internal/clock`, already archived)

- [x] T1.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock`.
      Acceptance: `internal/clock` is `internal/config`'s only new project-local import; no
      import cycle (`clock` imports nothing project-local — RM35 D2).
- [x] T1.2 Add `pollerTimezoneOrDefault(v string) string`: returns `v` unchanged when
      non-empty; returns `clock.Zone().String()` when `v == ""`. Deliberately does **not**
      call `clock.LoadOrDefault(v)` — routing an empty string through it would silently
      resolve to UTC (tier-1 design D10's documented `time.LoadLocation("")` gotcha), the
      exact outcome this roadmap exists to prevent (design.md D-config-1).
      Acceptance: matches design.md's Test Contract — unset → `"America/Bogota"`; any
      non-empty input, valid or not, → itself, untouched.
- [x] T1.3 Replace `config.go:87-90`'s
      `cfg.PollerTimezone = envStripped("POLLER_TIMEZONE"); if cfg.PollerTimezone == "" { cfg.PollerTimezone = "Local" }`
      with `cfg.PollerTimezone = pollerTimezoneOrDefault(envStripped("POLLER_TIMEZONE"))`.
      Acceptance: the literal string `"Local"` no longer appears in `config.go`'s
      `POLLER_TIMEZONE` handling.
- [x] T1.4 Update the `PollerTimezone` field's doc comment on the `Config` struct to state the
      new default and that validation still happens in `cmd/poller`, not here.
      Acceptance: comment accurately describes the behavior T1.2/T1.3 implement.

## T2. `internal/config/timezone_test.go` — depends on T1

- [x] T2.1 Add `TestPollerTimezoneOrDefault_Unset`, transcribing design.md's Test Contract:
      `pollerTimezoneOrDefault("")` must equal `clock.Zone().String()`, which must equal
      `"America/Bogota"`. This is RM35 D6's one permitted new test for this tier — the branch
      previously had zero coverage.
      Acceptance: `go vet ./internal/config/...` compiles the test file cleanly (this tier does
      not run the tests — `Test-Execution-Policy`; the owner's `go test` run is what turns this
      from `awaiting-user-verification` into `done`).
- [x] T2.2 Do **not** add tests for the set-valid or set-invalid pass-through cases (design.md
      "Test Contract" — RM35 D6 confines new coverage to the previously-uncovered unset case
      only; both pass-through behaviors are unchanged, trivial, and documented in design.md
      instead of tested).

## T3. `internal/config/AGENTS.md` — depends on T1

- [x] T3.1 Create `internal/config/AGENTS.md`, mirroring `internal/account/AGENTS.md`'s shape:
      `Agent-Name: config` header, `## Doc-Pack (module)` (empty), responsibility (loads
      `.env`, the only package allowed to call `os.Getenv`, a `cmd/`-level concern read by
      `cmd/setup`/`cmd/web`/`cmd/poller`, no `internal/` consumers), public interface (`Load`,
      `SaveTokens`, the two redirect-URL methods, the `PollerTimezone` default behavior),
      allowed imports (stdlib + `godotenv` + `internal/clock`; never another domain module),
      data ownership (none), and testing notes (pure/offline only; `Load()` itself untestable
      without a real `.env`, so its branch logic is extracted into small pure functions).
      Acceptance: every exported symbol in `config.go` is documented; the file states plainly
      that `config` is read by `cmd/*` and owns no data.

## T4. Doc sweep — confirm no stale `"Local"` default statement — no dependencies

- [x] T4.1 Search the repo (`README.md`, `docs/0-set-up/*.md`, `.env`, `.env.example`, every
      `AGENTS.md`) for any statement of `POLLER_TIMEZONE`'s default value. Fix any that state
      the old `"Local"` default in this same change (`CLAUDE.md`'s docs-track-change rule).
      Acceptance (completed): no such statement exists outside this roadmap's own planning docs
      and `internal/telemetry/AGENTS.md` (which mentions the env var but states no default) —
      no doc edit was needed. Recorded in `proposal.md`.

## T5. Verification — depends on T1–T4

- [x] T5.1 `go build ./...` passes repo-wide.
- [x] T5.2 `go vet ./...` passes repo-wide.
- [x] T5.3 `gofmt -l` reports no diff for `internal/config/config.go`,
      `internal/config/timezone_test.go`, or `internal/config/AGENTS.md`.
- [x] T5.4 Confirm no other file was touched: only `internal/config/config.go` (edited),
      `internal/config/timezone_test.go` (new), `internal/config/AGENTS.md` (new), plus this
      change's own OpenSpec artifacts — no `cmd/`, no other module.
- [ ] T5.5 `openspec validate RM35-config-adopt-clock --strict` passes (owner/leader step).
- [ ] T5.6 Owner runs the exact suite commands and reports the result, turning T2.1 from
      `awaiting-user-verification` into `done`:
      - `go test ./internal/config/...`
      - `go test ./...` (full repo regression net — RM35 D4's "behavior-preserving except the
        documented default" claim for this tier)
