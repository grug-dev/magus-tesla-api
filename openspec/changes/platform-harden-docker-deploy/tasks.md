# Tasks — platform-harden-docker-deploy

> **Dependencies / parallelism.**
> - **T1** (`Dockerfile` move + build cache mounts) touches only
>   `deploy/docker/Dockerfile` and `deploy/docker/Dockerfile.dockerignore`. No
>   dependency. MAY run in parallel with T2a, T2b.
> - **T2a** (`Caddyfile` move) and **T2b** (`backup-db.sh` move + path fix) each
>   touch one file, disjoint from T1 and from each other. No dependency. MAY run in
>   parallel with T1 and with each other.
> - **T3** (`compose.yaml` move — the four traps: build context/dockerfile path,
>   `env_file` path, Caddy volume path) is the **first** edit to `compose.yaml`. No
>   dependency (design.md fully fixes every path already). MAY run after or
>   alongside T1/T2a/T2b, but **must land before T4**, which edits the same file.
> - **T4** (`compose.yaml` hardening — `x-logging` anchor, resource limits,
>   security hardening) is the **second** edit to `compose.yaml`. **Depends on T3.**
>   This is a deliberate sequencing, not a missed parallel opportunity — two workers
>   cannot safely edit the same file at once.
> - **T5** (`Makefile` — `COMPOSE` variable, target updates, help text) depends on
>   **T3** and **T2b** (needs the final `compose.yaml` and `backup-db.sh` paths to
>   reference). Does not depend on T4 — the hardening content inside `compose.yaml`
>   does not change any path the `Makefile` needs. MAY run in parallel with T4.
> - **T6** (`.env.example` — resource-limit variables) has no dependency — design.md
>   already fixes every variable name and default. MAY run in parallel with
>   T1–T5.
> - **T7a** (`docs/1-deploy/docker.md`), **T7b** (`docs/0-set-up/deployment.md` §8),
>   **T7c** (`README.md` + `cmd/README.md`) each depend on **T1, T2a, T2b, T3, T4,
>   T5, T6** — they document exact final commands and paths, which must already be
>   final. Disjoint files; MAY run in parallel with each other once T1–T6 are done.
> - **T8** (verification) depends on **everything**. Final wave.
>
> **Leader-integrated step:** none. This change adds no database object and no sqlc
> input (design.md → "Database objects"), so no codegen re-run is needed beyond the
> ordinary `go build`/`go vet`/guard signals in T8.

## T1. `Dockerfile` move + build cache mounts — no dependency, parallel-ok with T2a/T2b

- [x] T1.1 Create `deploy/docker/Dockerfile`, moved from the repo-root `Dockerfile`.
      Add `# syntax=docker/dockerfile:1` as the first line (required for
      `--mount=type=cache`, design.md D5). Add `--mount=type=cache,target=/go/pkg/mod`
      to the `go mod download` line, and both
      `--mount=type=cache,target=/root/.cache/go-build` and
      `--mount=type=cache,target=/go/pkg/mod` to each of the three `go build` lines
      (`web`, `poller`, `migrate`). Stage names, binary output paths, base images,
      and non-root `USER app` lines are otherwise unchanged from the current file.
      Delete the repo-root `Dockerfile`.
      Acceptance: `deploy/docker/Dockerfile` exists; repo-root `Dockerfile` does
      not; the three build stage names (`web`, `poller`, `migrate`) are unchanged
      (`compose.yaml`'s `target:` values, updated in T3/T4, depend on these exact
      names).
- [x] T1.2 Create `deploy/docker/Dockerfile.dockerignore`, moved and renamed from
      the repo-root `.dockerignore` (design.md D1, Trap 2). Copy every pattern
      unchanged — patterns still match against the build context root (the repo
      root), not against this file's own folder. Delete the repo-root
      `.dockerignore`.
      Acceptance: `deploy/docker/Dockerfile.dockerignore` exists with the same
      patterns as the old `.dockerignore` (`.git`, `.env`, `.env.example`, `*.pem`,
      `private-key*`, `bin/`, `tmp/`, `.air/`,
      `internal/gateway/tools/tailwindcss`, `magus-public-key-netlify/`,
      `cmd/explore-tesla-api/output/`, `kkpa/`, `openspec/changes/archive/`); the
      repo-root `.dockerignore` does not exist.

## T2a. `Caddyfile` move — no dependency, parallel-ok with T1/T2b

- [x] T2a.1 Create `deploy/docker/Caddyfile`, moved from `deploy/Caddyfile`, content
      unchanged. Delete the old `deploy/Caddyfile`.
      Acceptance: `deploy/docker/Caddyfile` exists with unchanged content; the old
      path does not exist.

## T2b. `backup-db.sh` move + path fix — no dependency, parallel-ok with T1/T2a

- [x] T2b.1 Create `deploy/docker/backup-db.sh`, moved from `deploy/backup-db.sh`.
      Fix `BACKUP_DIR` from `"$(dirname "$0")/../backups"` to
      `"$(dirname "$0")/../../backups"` (design.md D1, Trap 4 — the script moved one
      folder deeper, so it needs one more `../` to still reach the repo-root
      `backups/` folder). Update the `docker compose exec -T db pg_dump ...` line to
      `docker compose --project-directory . -f deploy/docker/compose.yaml exec -T db
      pg_dump ...` (the same flags every other Compose command needs after the move
      — design.md D1, Trap 3). Add a comment noting these flags must match the
      `Makefile`'s `COMPOSE` variable (added in T5) if the path ever changes again.
      Preserve the executable bit. Delete the old `deploy/backup-db.sh`.
      Acceptance: `deploy/docker/backup-db.sh` exists, is executable
      (`ls -la` shows `x`), and its `BACKUP_DIR` and `docker compose` lines match
      the values above; the old path does not exist.

## T3. `compose.yaml` move (the four traps) — no dependency; MUST land before T4

- [x] T3.1 Create `deploy/docker/compose.yaml`, moved from the repo-root
      `compose.yaml`. Fix `build.context` to `../..` and `build.dockerfile` to
      `deploy/docker/Dockerfile` (relative to `context`, not to this file — design.md
      D1, Trap 1) on all three build services (`migrate`, `web`, `poller`). Keep
      each service's `target:` (`migrate`, `web`, `poller`) unchanged — these must
      match T1's stage names exactly.
      Acceptance: every `build:` block has `context: ../..` and
      `dockerfile: deploy/docker/Dockerfile`; `target:` values unchanged.
- [x] T3.2 Fix every `env_file: .env` line to `env_file: ../../.env` (relative to
      this file's own folder, not the project directory — design.md D1, Trap 3,
      mechanism 2). Do not add `--project-directory` inside the file itself — that
      flag is passed on the command line (T5), not written into `compose.yaml`.
      Acceptance: every service that had `env_file: .env` now has
      `env_file: ../../.env`.
- [x] T3.3 Fix the Caddy volume mount from
      `./deploy/Caddyfile:/etc/caddy/Caddyfile:ro` to
      `./Caddyfile:/etc/caddy/Caddyfile:ro` (the `Caddyfile` now sits next to this
      compose file — design.md D1, Trap 4). Named volumes (`pgdata`, `caddy_data`,
      `caddy_config`) are unchanged — they are Docker-managed, not paths.
      Acceptance: the Caddy volume line reads `./Caddyfile:/etc/caddy/Caddyfile:ro`.
- [x] T3.4 Delete the repo-root `compose.yaml`.
      Acceptance: `deploy/docker/compose.yaml` exists with T3.1–T3.3's fixes; the
      repo-root `compose.yaml` does not exist. `depends_on`, healthchecks, `image:`
      values, `ports:`, and the `volumes:` top-level block are otherwise unchanged
      from the current file — this task only fixes the four path traps.

## T4. `compose.yaml` hardening — depends on T3 (same file, sequential)

- [x] T4.1 Add the `x-logging` YAML anchor at the top level (design.md D2) and
      `logging: *default-logging` to all five services.
      Acceptance: `x-logging: &default-logging` exists once, with
      `driver: json-file`, `options: {max-size: "10m", max-file: "3"}`; all five
      services (`db`, `migrate`, `web`, `poller`, `caddy`) reference it via
      `logging: *default-logging`.
- [x] T4.2 Add `deploy.resources.limits.cpus` / `.memory` to all five services,
      each reading from a `${VAR:-default}` pair per design.md's sizing table:
      `db` → `${DB_CPU_LIMIT:-1.0}` / `${DB_MEM_LIMIT:-1536M}`; `migrate` →
      `${MIGRATE_CPU_LIMIT:-0.5}` / `${MIGRATE_MEM_LIMIT:-256M}`; `web` →
      `${WEB_CPU_LIMIT:-0.5}` / `${WEB_MEM_LIMIT:-256M}`; `poller` →
      `${POLLER_CPU_LIMIT:-0.25}` / `${POLLER_MEM_LIMIT:-128M}`; `caddy` →
      `${CADDY_CPU_LIMIT:-0.25}` / `${CADDY_MEM_LIMIT:-128M}`. Do not add
      `deploy.resources.reservations` (design.md D3 — Swarm-only, no effect under
      plain `docker compose up`).
      Acceptance: every service has both a `cpus` and a `memory` limit, using the
      exact variable names above; no `reservations` block is present.
- [x] T4.3 Add the D4 security hardening block to every service, per design.md's
      per-service table:
      - All five: `security_opt: [no-new-privileges:true]`, `cap_drop: [ALL]`.
      - `db`: `cap_add: [CHOWN, DAC_OVERRIDE, FOWNER, SETUID, SETGID]`,
        `read_only: true`, `tmpfs: [/var/run/postgresql, /tmp]`.
      - `migrate`: `read_only: true`, no `cap_add`, no `tmpfs`.
      - `web`: `read_only: true`, `tmpfs: [/tmp]`, no `cap_add`.
      - `poller`: `read_only: true`, `tmpfs: [/tmp]`, no `cap_add`.
      - `caddy`: `cap_add: [NET_BIND_SERVICE]`, `read_only: true`,
        `tmpfs: [/tmp]`.
      Acceptance: every service's block matches design.md's per-service hardening
      table exactly, including which services get no `cap_add`/`tmpfs` at all.

## T5. `Makefile` updates — depends on T3, T2b; parallel-ok with T4

- [x] T5.1 Add `COMPOSE = docker compose --project-directory . -f
      deploy/docker/compose.yaml` near the top of the Docker section (design.md D1,
      Trap 4).
      Acceptance: the variable exists with this exact value.
- [x] T5.2 Update `docker-up`, `docker-down`, `docker-logs`, `docker-migrate` to
      call `$(COMPOSE) ...` instead of `docker compose ...` (same subcommand and
      flags as today — `up -d --build`, `down`, `logs -f`,
      `run --rm migrate`). Update `backup-db` to call
      `./deploy/docker/backup-db.sh` (new path).
      Acceptance: `grep 'docker compose ' Makefile` finds no remaining bare
      `docker compose` call inside these five targets (only `$(COMPOSE)` or the
      script call remain).
- [x] T5.3 Update each target's `## ...` help comment (shown by `make help`) to
      name the new `deploy/docker/compose.yaml` location where it currently implies
      a repo-root file.
      Acceptance: `make help`'s Docker section text no longer implies a repo-root
      `compose.yaml`.

## T6. `.env.example` — resource-limit variables, no dependency, parallel-ok with T1–T5

- [x] T6.1 Add the ten resource-limit variables from T4.2
      (`DB_CPU_LIMIT`/`DB_MEM_LIMIT`, `MIGRATE_CPU_LIMIT`/`MIGRATE_MEM_LIMIT`,
      `WEB_CPU_LIMIT`/`WEB_MEM_LIMIT`, `POLLER_CPU_LIMIT`/`POLLER_MEM_LIMIT`,
      `CADDY_CPU_LIMIT`/`CADDY_MEM_LIMIT`), each commented out with its default
      value shown and a one-line comment on what it controls and the "assumes a
      4 GB RAM / 2 vCPU VPS" note (design.md D3).
      Acceptance: all ten variables appear, each with its default value visible in
      a comment; none are left uncommented with a value that would silently
      override the `compose.yaml` default.

## T7a. `docs/1-deploy/docker.md` updates — depends on T1, T2a, T2b, T3, T4, T5, T6

- [ ] T7a.1 Update every `docker compose ...` command in the file (the "Local Docker
      use" section, "Everyday commands" table, "Migrations" section, "Database
      access" section, "Backups" section, "Troubleshooting" table, "Safety"
      section, "Switching to a managed database later" section) to the new
      `--project-directory . -f deploy/docker/compose.yaml` form. Update the two
      `docker build --target ...` example commands to
      `docker build -f deploy/docker/Dockerfile --target <stage> .` (context stays
      `.`, only `-f` changes — design.md D1, Trap 1). Update `docker compose config`
      similarly.
      Acceptance: no command in the file omits the two new flags where a
      `compose.yaml`-based command is shown; both `docker build` examples name
      `-f deploy/docker/Dockerfile`.
- [ ] T7a.2 Add a short paragraph (§1, near the mental-model table) stating: every
      service now has a bounded log size, a CPU/memory limit, and reduced Linux
      privileges, and that the exact numbers and rationale live in this change's
      `design.md`. Do not restate the numbers in this doc — link to design.md's
      per-service table so the doc cannot drift out of sync with the actual
      `compose.yaml`.
      Acceptance: the paragraph exists and links to
      `openspec/changes/platform-harden-docker-deploy/design.md` (or, after
      archiving, its archived path).

## T7b. `docs/0-set-up/deployment.md` §8 updates — depends on T1, T2a, T2b, T3, T4, T5, T6

- [ ] T7b.1 Update every Docker command in §8 (8.8 first deploy, 8.9 verify, 8.11
      deploying an update) to the new `--project-directory . -f
      deploy/docker/compose.yaml` form.
      Acceptance: `docker compose up -d --build` and every other Compose command in
      §8 carries both new flags.

## T7c. `README.md` + `cmd/README.md` updates — depends on T1, T2a, T2b, T3, T4, T5, T6

- [ ] T7c.1 Update `README.md`'s "Project Structure" tree: remove the repo-root
      `Dockerfile`, `.dockerignore`, `compose.yaml` lines; add a `deploy/docker/`
      entry describing the five moved files, in the same place `deploy/` is
      currently listed.
      Acceptance: the tree shows no repo-root Docker file and shows
      `deploy/docker/` with its five files named or summarized.
- [ ] T7c.2 Update `cmd/README.md`'s `cmd/migrate` row and the paragraph below the
      table: the commands (`make migrate-run`, `make docker-migrate`) are unchanged
      by name, but the paragraph naming "the repo-root `Dockerfile`" is updated to
      say `deploy/docker/Dockerfile`.
      Acceptance: `cmd/README.md` names `deploy/docker/Dockerfile`, not a repo-root
      one.

## T8. Verification — depends on everything

- [ ] T8.1 Run `go build ./...`, `go vet ./...`, `gofmt -l .`. This change touches
      no Go file, so these are expected to pass unchanged — run them anyway as a
      regression check that nothing was accidentally broken.
      Acceptance: all three are clean.
- [ ] T8.2 Run `make migration-guard`, `make boundary-guard`, `make archive-guard`
      to confirm design.md's "Reverse-direction check" claims of "unaffected" hold
      in practice, not just on paper.
      Acceptance: all three guards pass.
- [ ] T8.3 Confirm no file remains at any of the five old paths (`Dockerfile`,
      `.dockerignore`, `compose.yaml`, `deploy/Caddyfile`, `deploy/backup-db.sh`)
      and that all five exist at their new `deploy/docker/` path.
      Acceptance: `ls Dockerfile .dockerignore compose.yaml deploy/Caddyfile
      deploy/backup-db.sh` all report "No such file"; `ls deploy/docker/` lists all
      five moved files.
- [ ] T8.4 Hand back to the owner, as commands to run (this pipeline does not run
      them — Test-Execution-Policy excludes `docker build`, `docker compose up`,
      and `docker compose config`):
      - `docker compose --project-directory . -f deploy/docker/compose.yaml config`
        — confirms the moved file parses and every path/variable resolves.
      - `docker compose --project-directory . -f deploy/docker/compose.yaml up -d
        --build`, followed by `docker compose --project-directory . -f
        deploy/docker/compose.yaml ps` — confirms every service starts, `db` and
        `web` report healthy, and `migrate` exits `0` under the new hardening
        (in particular: `db`'s five added capabilities are enough for its
        first-boot ownership fix, and `caddy` can still bind ports 80/443 with
        only `NET_BIND_SERVICE`).
      Acceptance: none — this task's output is the exact command list for the
      owner; the pipeline reports these tasks as `awaiting-user-verification`.
