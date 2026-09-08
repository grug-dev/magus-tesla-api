# Tasks — platform-add-manual-rerun-api

Unit tests: excluded

> **Dependencies / parallelism.**
> - **T1** (`internal/config`) has no dependencies. Disjoint file from every other
>   task; MAY run in parallel with T3/T4.
> - **T2** (`cmd/poller` — the listener, the lock, the handler) depends on **T1**
>   (needs `cfg.PollerRerunToken` to exist). This is where all the new logic in this
>   change lives.
> - **T3** (`deploy/docker/compose.yaml`, `deploy/docker/Caddyfile`) has no code
>   dependency — the port (`8081`) and the path shape are already fixed by
>   design.md D6/D8. Disjoint files from T1/T2/T4; MAY run in parallel with them.
> - **T4** (`.env.example`) has no dependencies. Disjoint file; MAY run in parallel
>   with T1/T2/T3.
> - **T5** (doc sweep: `internal/app/AGENTS.md`, `internal/app/app.go`,
>   `cmd/poller/main.go`, `internal/telemetry/telemetry.go`, root `README.md`,
>   `cmd/README.md`, `kkpa/context/architecture/nightly-cycle.md`) depends on
>   **T1, T2, T3** — it describes exact behavior, file names, and env vars that must
>   already be final. Its files are disjoint from T1-T4's; MAY run in parallel with T6
>   once T1-T3 are done.
> - **T6** (`docs/0-set-up/deployment.md` — the new deploy subsection with the
>   `curl` command) depends on **T1, T2, T3** — same reason as T5. Disjoint file;
>   MAY run in parallel with T5.
> - **T7** (verification) depends on **everything** — it is the final wave.
>
> **Leader-integrated step:** none. This change adds no database object and no sqlc
> input (design.md → "Database objects"), so no codegen re-run is needed beyond the
> ordinary `go build`/`go vet` signals in T7.

## T1. `internal/config` — read `POLLER_RERUN_TOKEN` — no dependencies, parallel-ok with T3/T4

- [x] T1.1 Add `PollerRerunToken string` to `Config` in `internal/config/config.go`,
      with a doc comment mirroring the style of the existing `PollerTimezone` field:
      state that an empty value means the manual-rerun HTTP listener does not start
      at all (design.md D2), and that this module does not validate its shape — any
      non-empty string is accepted.
- [x] T1.2 In `Load()`, add `cfg.PollerRerunToken = envStripped("POLLER_RERUN_TOKEN")`.
      No default, no validation — an empty value is a valid, working "disabled"
      state, not an error.
- [x] T1.3 Update `internal/config/AGENTS.md` → "Public interface" with a bullet for
      `Config.PollerRerunToken`, mirroring the existing `Config.PollerTimezone`
      bullet's shape and citing design.md D2.
      Acceptance: `go build ./internal/config/...` and `go vet ./internal/config/...`
      are clean; `gofmt -l internal/config/config.go` prints nothing.

## T2. `cmd/poller` — the HTTP listener, the shared lock, the handler — depends on T1

- [x] T2.1 Create `cmd/poller/rerun.go` (`package main`) with the `guardedProcessor`
      type from design.md D3 verbatim: `ProcessVehicleData` (satisfies
      `app.Processor`, used by the scheduler) and `TryStartAPIRun` (used by the HTTP
      handler), sharing one `sync.Mutex`. Define `errCycleBusy` as a sentinel error
      in the same file. Its message must read as a SKIP, not as a crash — the
      scheduler path returns it, and `telemetry.LogCycle` prints it as that night's
      only record. Use wording like `"a vehicle-data cycle is already running; this
      scheduled run was skipped"`. This is what satisfies the spec scenario "The
      scheduler skips its turn when a manual cycle is running" (design.md D3,
      "Accepted cost").
- [x] T2.2 In the same file, add the HTTP handler: built as a closure over the root
      `ctx` (the `signal.NotifyContext` value from `main()`) and the `*guardedProcessor`
      — NEVER over `r.Context()` (design.md D4(b) — read that section before writing
      this function; it is the most likely bug in this change). On
      `TryStartAPIRun(ctx)` returning `ok == false`, write `409` and return. On
      `ok == true`, write `202` with body `{"status":"started"}` and
      `Content-Type: application/json`, THEN `go start()`.
- [x] T2.3 In `cmd/poller/main.go`: after building `processor := app.NewProcessor(...)`
      (existing line, unconditional — before the `if !*once` branch), wrap it —
      `guarded := &guardedProcessor{inner: processor}` — once, unconditionally. Use
      `guarded` in BOTH branches below it, not the raw `processor`: pass `guarded` to
      `app.NewScheduler(guarded, ...)` in the `!*once` branch, and call
      `guarded.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)` (not
      `processor.ProcessVehicleData(...)`) in the `else` (`--once`) branch — this is
      inert for `--once` today (nothing else can contend for the lock in a one-shot
      CLI run) but keeps exactly one code path that calls `ProcessVehicleData`
      un-guarded, which is zero. Only inside the `!*once` branch (the long-running
      scheduler path — `--once` exits immediately and gets no listener), if
      `cfg.PollerRerunToken != ""`: build the `net/http.ServeMux`, register
      `mux.HandleFunc("POST /internal/rerun/"+cfg.PollerRerunToken, handler)`
      (design.md D5 — the literal, secret-bearing path IS the registered pattern; no
      manual comparison), start `http.ListenAndServe(rerunAddr, mux)` in a goroutine
      with the error logged (not fatal — this is an optional capability, never worth
      taking the poller itself down), and log one clear startup line stating the
      listener is up (or, symmetrically, that it is OFF because the token is unset).
      Define `const rerunAddr = ":8081"` (design.md D6) at package level.
- [x] T2.4 Update `cmd/poller/main.go`'s package doc comment: the "a future
      manual-rerun API would pass 'api'" sentence is no longer future — state plainly
      that both entry points (the scheduler's tick and this listener) call
      `ProcessVehicleData` through the same `guardedProcessor`, so neither can run
      while the other is running (design.md D3).
      Acceptance: `go build ./...` and `go vet ./...` are clean; `gofmt -l
      cmd/poller/*.go` prints nothing.

## T3. `deploy/docker/compose.yaml` + `deploy/docker/Caddyfile` — no code dependency, parallel-ok with T1/T2/T4

- [x] T3.1 Add `expose: ["8081"]` to the `poller` service in
      `deploy/docker/compose.yaml` (design.md D8 — documentation only; no `ports:`
      entry, never published to the host). Add a short comment next to it stating why
      (only Caddy dials it, over the compose network, by service name).
- [x] T3.2 Rewrite `deploy/docker/Caddyfile` into the two-`handle`-block shape from
      design.md D8: `/internal/rerun/*` → `reverse_proxy poller:8081`, fallback
      `handle {}` → `reverse_proxy web:8080`. Keep the file's existing header comment
      about `BASE_DOMAIN`, extended with one line noting the new path-scoped route.
      Acceptance: the file is valid Caddyfile syntax (two `handle` blocks inside one
      site block, as verified against Caddy's own docs in design.md D8) — no
      automated check exists for this in this repo; the owner's own V2/V5 manual
      checks (design.md) are the real signal.

## T4. `.env.example` — no dependencies, parallel-ok with T1/T2/T3

- [x] T4.1 Add `POLLER_RERUN_TOKEN=` near the other poller-related settings, with a
      comment: what it is for, that leaving it empty disables the endpoint entirely
      (design.md D2), how to generate a value (`openssl rand -hex 16` or similar —
      any random string works, per the owner's own "hard to guess" ask), and that
      rotating it is an `.env` edit plus a restart, no rebuild.

## T5. Doc sweep — depends on T1, T2, T3

- [x] T5.1 `internal/app/AGENTS.md` — replace "the tier-8 API will live in its own
      `cmd/` binary" (in the `## Responsibility` section, under "Why here rather than
      `cmd/poller`") with the corrected fact: the manual-rerun API lives inside
      `cmd/poller` (design.md D1), calling `Processor` through a `cmd/poller`-local
      lock (design.md D3), not inside `internal/app` itself.
- [x] T5.2 `internal/app/app.go`'s package doc comment — the line "Scheduler and the
      future manual-rerun API (roadmap tier 8, parked) are peer driving adapters"
      drops "future" and "parked": the API adapter now exists, in `cmd/poller`.
- [x] T5.3 `internal/telemetry/telemetry.go` — the `TriggeredByAPI` constant's doc
      comment ("a future manual re-run via HTTP (RM29 tier 8, parked)") drops
      "future" and "parked" and points at this change by name.
- [x] T5.4 Root `README.md` — two spots: the `internal/app` row in the Architecture
      table ("Called by `cmd/poller` and, later, the parked manual-rerun API")
      becomes "Called by `cmd/poller`'s scheduler and its manual-rerun HTTP
      listener"; the `poll_attempts` row's "`api` once the parked manual-rerun API
      exists" becomes "`api` for a manual rerun via `cmd/poller`'s HTTP listener
      (platform-add-manual-rerun-api)".
- [x] T5.5 `cmd/README.md` — the `cmd/poller` row gains its second responsibility:
      state plainly that it also serves the manual-rerun HTTP endpoint (path,
      `POLLER_RERUN_TOKEN` gate, off-by-default) alongside the scheduler.
- [x] T5.6 `kkpa/context/architecture/nightly-cycle.md` → "Related KB" — the line
      "Use cases: (none — the cycle has no external HTTP trigger; the tier-8
      manual-rerun API is parked)" is now false: the cycle DOES have an external HTTP
      trigger. Correct it to state the trigger exists (`cmd/poller`'s
      `/internal/rerun/<token>`, design.md of this change) — do not invent a full
      use-case guide here; a fuller KB sync is staged separately after archive
      (`~/.claude/rules/openspec-archive-kb-sync.md`), this is only the one-line
      correction `CLAUDE.md`'s own doc-tracking rule requires in the same change.
      Republish the linked artifact only if this guide is normally kept synced with
      one — check the guide's own "It does not self-update" note before deciding.

## T6. `docs/0-set-up/deployment.md` — depends on T1, T2, T3

- [x] T6.1 Add a new numbered subsection after the existing §8.11 ("Deploying an
      update, from now on"): how to set `POLLER_RERUN_TOKEN` in `.env`, restart the
      stack, and the exact `curl -i -X POST https://<domain>/internal/rerun/<token>`
      command with its expected `202 Accepted` / `{"status":"started"}` response and
      the `409 Conflict` case. State plainly that the response carries no run ID and
      point at `telemetry.poll_runs WHERE triggered_by = 'api' ORDER BY started_at
      DESC LIMIT 1` (design.md's Manual verification contract, V2-V4) as how to find
      the run afterward.

## T7. Verification — depends on everything

- [ ] T7.1 `go build ./...` — clean.
- [ ] T7.2 `go vet ./...` — clean (also compiles any test files touched; there
      should be none in this change per "Unit tests: excluded").
- [ ] T7.3 `gofmt -l` over every file this change touched — no output.
- [ ] T7.4 `make migration-guard`, `make boundary-guard`, `make archive-guard` —
      clean (design.md D8's reverse-direction check already predicts this; run them
      to confirm, not to discover something new).
- [ ] T7.5 Hand off to the owner: the exact commands from `go test ./...` /
      `make test` / `make check` this change never runs, plus the design.md Manual
      verification contract (V1-V5) to run on the VPS after deploy.
