# Design — platform-harden-docker-deploy

## Context

`platform-add-docker-compose-deploy` (archived) built the first Docker stack: five
services (`db`, `migrate`, `web`, `poller`, `caddy`) in `compose.yaml`, plus a
`Dockerfile`, `.dockerignore`, `deploy/Caddyfile`, and `deploy/backup-db.sh`, all
described in `openspec/changes/archive/platform/2026-09-07-platform-add-docker-compose-deploy/design.md`
(decisions D1–D11, read but never edited — that folder is immutable).

That stack works, but it is not production-hardened. The owner asked for five
specific improvements before the first real VPS deploy: move the Docker files into
one folder, cap log growth, add resource limits, add security hardening, and speed
up rebuilds with a build cache. The owner has **not bought the VPS yet** — every
sizing choice below assumes a small box: **4 GB RAM, 2 vCPU**. State this assumption
next to every number that depends on it, and make the numbers easy to change.

## Goals / Non-Goals

**Goals:**
- Every Docker-related file lives under one folder, `deploy/docker/`.
- The move does not break the build, the `.dockerignore` exclusions, or `.env`
  secret loading — verify each one, do not guess.
- No service can grow its logs without limit.
- No service can starve another of CPU or RAM.
- Every service runs with the least Linux privilege it needs to actually work.
- A VPS rebuild reuses the Go build cache instead of recompiling from scratch.
- Every doc, script, and `make` target that names an old path or an old command is
  updated in this same change.

**Non-Goals:**
- No change to which services exist, what they do, or their `depends_on` /
  healthcheck graph. That graph is unchanged from the archived design.
- No new database object of any kind (see "Database objects" below).
- No registry, no CI/CD pipeline — still out of scope, same as the archived change.
- No change to `internal/config` or any other Go package. This change touches no
  `internal/` file.

## Target file layout

```text
deploy/docker/
├── Dockerfile               # was repo-root Dockerfile
├── Dockerfile.dockerignore  # was repo-root .dockerignore
├── compose.yaml              # was repo-root compose.yaml
├── Caddyfile                 # was deploy/Caddyfile
└── backup-db.sh               # was deploy/backup-db.sh
```

The repo root keeps **no** `Dockerfile`, **no** `compose.yaml`, **no**
`.dockerignore`. `deploy/` itself still exists as the parent folder, now holding
only the `docker/` subfolder (no other file lived directly in `deploy/` before this
change).

## Decisions

### D1 — Move every Docker file into `deploy/docker/`

**Why:** the owner asked for it — a clean repo root, with deploy config grouped in
one place instead of scattered between the root and `deploy/`. This mirrors how
`internal/<module>/db/` groups a module's own persistence files: one folder per
concern.

**Rejected:** leaving `compose.yaml` at the root while moving only `Dockerfile` and
`.dockerignore`. Rejected because it splits one deploy concern across two
locations for no reason — the owner asked to move "ALL docker files, including
`compose.yaml`."

**The cost, accepted by the owner:** every `docker compose ...` command now needs
two extra flags (below). This is real, ongoing friction, not a one-time cost — every
doc, script, and `make` target that runs one of these commands must carry the flags
forever. This change updates every such place in the same commit, so nothing is
left silently broken.

The move touches four things that would otherwise break. Each is its own
sub-decision below, verified against Docker's own documentation, not assumed.

#### Trap 1 — the build context must stay the repo root

**Resolved. The `context:` value shown here was CORRECTED — see Trap 3.** The first
version of this section said `context: ../..`, on the same wrong resolution model
that broke Trap 3. `build.context` is a relative path like any other, so it resolves
from the **one base path** Trap 3 describes, which `--project-directory .` sets to
the repo root. The correct value is therefore `.`, not `../..`.

`web`, `poller`, and `migrate` are all built from `cmd/...`, which needs `go.mod`,
`go.sum`, `internal/`, and `cmd/` — all at the repo root. So the build **context**
(the set of files Docker can see during a build) must stay the repo root, even
though the `Dockerfile` itself moves.

`build.dockerfile:` is the one field that does NOT use the base path: it resolves
**relative to `build.context:`**. That is why it keeps the real folder prefix.

```yaml
build:
  context: .                            # the repo root — the base path (Trap 3)
  dockerfile: deploy/docker/Dockerfile  # relative to context, NOT to the base path
```

Get this backwards — `dockerfile: Dockerfile` with `context: .` — and Docker looks
for `<repo-root>/Dockerfile`, which no longer exists. Two different rules meet in
these two lines, which is why both traps got it wrong the first time.

#### Trap 2 — the ignore file needs a new name and a new home

**Resolved, verified against Docker's current documentation.** With the build
context at the repo root, Docker looks for `.dockerignore` at the **context root**
by default — i.e. `<repo-root>/.dockerignore`. This change deletes that file, so a
plain `.dockerignore` would no longer apply to anything.

Docker supports a **Dockerfile-specific ignore file**: name it
`<dockerfile-filename>.dockerignore` and place it **next to the Dockerfile**, not
next to the build context. For this move: `deploy/docker/Dockerfile.dockerignore`,
next to `deploy/docker/Dockerfile`. Docker's own docs state this file "takes
precedence over the `.dockerignore` file at the root of the build context if both
exist" — since there is no root one left, this is simply the only ignore file in
effect.

**One more detail, also verified:** the *patterns inside* the ignore file are still
matched against the **build context root** (the repo root), exactly like an
ordinary `.dockerignore` — only the file's own *lookup location* changes with this
naming convention, not what its patterns mean. So every existing pattern (`.git`,
`.env`, `*.pem`, `private-key*`, `bin/`, `tmp/`, `.air/`,
`internal/gateway/tools/tailwindcss`, `magus-public-key-netlify/`,
`cmd/explore-tesla-api/output/`, `kkpa/`, `openspec/changes/archive/`) moves into
`deploy/docker/Dockerfile.dockerignore` **unchanged**, character for character.

**Rejected:** keeping a root `.dockerignore` "just in case." Rejected because a
second, unused ignore file is exactly the kind of drift this project's docs rules
warn about — a file nobody updates that quietly stops matching reality.

#### Trap 3 — `.env` discovery (the trap that actually bit)

**CORRECTED after implementation. The first version of this section was wrong, and
the error it caused is recorded below.** Keep the correction visible: the wrong rule
is easy to re-derive from a partial reading of the Compose docs.

**What this section first claimed (WRONG).** That Compose reads `.env` through two
independent mechanisms with two different base paths: `${VAR}` interpolation from
the *project directory*, and `env_file:` from the *compose file's own folder*,
"regardless of `--project-directory`". On that reasoning the fix was
`env_file: ../../.env` plus the `--project-directory .` flag.

**What actually happened.** With `env_file: ../../.env` and
`--project-directory .`, run from the repo root:

```
$ docker compose --project-directory . -f deploy/docker/compose.yaml config
env file /Users/cristianpena/cpena/sw/.env not found
```

The repo root is `.../sw/github/magus-tesla-api`. Compose resolved `../../.env`
from the **repo root**, giving `.../sw/.env` — two levels above the repo. If
`env_file` had resolved from the compose file's folder, it would have found
`.../magus-tesla-api/.env` and worked. It did not.

**The real rule, from Docker's own documentation:**

> "all paths in the files are relative to the first configuration file specified
> with `-f`. You can use the `--project-directory` option to override this base
> path."

There is **one** base path, not two. It covers `env_file`, `build.context`, and
bind-mount volumes together. It defaults to the compose file's own folder, and
`--project-directory` overrides it. Because this project always passes
`--project-directory .` from the repo root, **the base path is the repo root**, and
every relative path in `compose.yaml` is written exactly as it was before the move:

```yaml
# deploy/docker/compose.yaml — resolved from the repo root, not from this folder:
env_file: .env
build:
  context: .
  dockerfile: deploy/docker/Dockerfile   # relative to context, so it keeps the prefix
volumes:
  - ./deploy/docker/Caddyfile:/etc/caddy/Caddyfile:ro
```

```bash
# Run from the repo root, always. Both flags are required.
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build
```

`build.dockerfile` is the one exception: it resolves against `build.context`, not
against the base path, so it carries the real `deploy/docker/` prefix.

**Why the flag is not optional.** Drop `--project-directory .` and the base path
falls back to `deploy/docker/`. Then `.env`, the build context, and the `Caddyfile`
all resolve to the wrong place at once. The `Makefile`'s `COMPOSE` variable and
`backup-db.sh` both carry the flags so no one has to remember them.

**Side effect, checked and harmless:** `--project-directory .` also sets Compose's
default *project name* (the prefix on container and volume names) to the repo-root
folder's name — the same value it defaulted to before the move, when the compose
file lived at the repo root. No container or volume is renamed by this change.

**Rejected:** `--env-file ./.env` instead of `--project-directory .`. It overrides
only the interpolation file. It does not move the base path, so the build context
and the Caddyfile would still resolve against `deploy/docker/`.

**Lesson for the reviewer.** This error survived design, implementation, and three
doc rewrites, because every one of them trusted the design. It was caught by the
first command that actually ran. A path rule that no command has executed is a
hypothesis, not a decision.

**One-time exception:** `docker build` (a plain image build, not via compose) takes
no `.env` at all — it never did, and this change does not touch that.

#### Trap 4 — every relative path inside compose and the scripts

**Resolved.** Every relative path in the moved files is checked and, where needed,
updated:

- `compose.yaml`'s Caddy volume mount was
  `./deploy/Caddyfile:/etc/caddy/Caddyfile:ro` (relative to the old repo-root
  compose file). The `Caddyfile` now sits right next to `compose.yaml`, so this
  becomes `./Caddyfile:/etc/caddy/Caddyfile:ro`.
- `backup-db.sh` computed its output folder as
  `BACKUP_DIR="$(dirname "$0")/../backups"`. At the old path
  (`deploy/backup-db.sh`), `../backups` reached the repo-root `backups/` folder.
  At the new path (`deploy/docker/backup-db.sh`), the same `../backups` would
  reach `deploy/backups/` instead — wrong. It becomes
  `BACKUP_DIR="$(dirname "$0")/../../backups"`.
- `backup-db.sh` calls `docker compose exec -T db pg_dump ...`. Plain
  `docker compose` with no `-f`/`--project-directory` looks for `compose.yaml` in
  the current folder — which no longer has one at the repo root. The script must
  pass the same two flags as every other Compose command (Trap 3): it now runs
  `docker compose --project-directory . -f deploy/docker/compose.yaml exec -T db
  pg_dump ...`, unchanged otherwise. It is still invoked the same way, from the
  repo root, by `make backup-db` and by the VPS cron line.

**Single source of the compose flags — the `Makefile`.** Rather than repeat
`--project-directory . -f deploy/docker/compose.yaml` in five places (four `make`
targets plus the backup script) and risk one of them drifting, the `Makefile` gets
one variable:

```makefile
COMPOSE = docker compose --project-directory . -f deploy/docker/compose.yaml
```

`docker-up`, `docker-down`, `docker-logs`, `docker-migrate` all call `$(COMPOSE)
...` instead of `docker compose ...`. `backup-db.sh` is a shell script, not a `make`
recipe, so it cannot read a `make` variable directly — it repeats the same literal
flags in its own `docker compose` call, with a comment pointing back to the
`Makefile`'s `COMPOSE` variable as the source of truth for what the flags must be,
so a future path change is a two-place, documented fact instead of a silent one.
This mirrors the project's own existing pattern for the `MIGRATIONS_DIRS` two-place
fact in `ai/go-conventions.md`.

### D2 — Bounded log rotation on every service

**Why:** Docker's default `json-file` log driver has no size limit. A service stuck
in a crash-restart loop, or one that logs more than expected, can fill a small VPS
disk with log data alone, with no cap. This is a real risk on the 4 GB / 2 vCPU box
this design assumes — disk is the resource most likely to be small too.

**The fix — one YAML anchor, applied to every service:**

```yaml
x-logging: &default-logging
  driver: json-file
  options:
    max-size: "10m"
    max-file: "3"

services:
  db:
    logging: *default-logging
  # ... same line in migrate, web, poller, caddy
```

`max-size: "10m"` caps each individual log file at 10 MB; `max-file: "3"` keeps at
most 3 rotated files per service. That is at most 30 MB of logs per service, 150 MB
total across all five — a small, fixed ceiling instead of no ceiling at all.

**Why a YAML anchor, not five copies:** the block is defined once
(`x-logging: &default-logging`) and referenced five times (`logging:
*default-logging`). One number to change later, not five to keep in sync — the same
AI-efficiency reasoning `CLAUDE.md` states for closed vocabularies: a value written
once and looked up, not re-typed per service.

**Rejected:** a different log driver (e.g. shipping logs to a remote collector).
Rejected as infrastructure the owner does not have and does not need yet for a
single-VPS hobby deploy — `docker compose logs` already covers the documented
troubleshooting workflow in `docs/1-deploy/docker.md`. Bounded `json-file` keeps
that workflow working exactly as documented, just with a cap.

### D3 — Resource limits on every service

**Why:** with no limit, one runaway container (a memory leak, a bug that spins the
CPU) can starve every other container on the same host, including `db` — the one
service every other service depends on. A small VPS has little room to absorb that;
a big cloud host might not notice, this one would.

**Assumption, stated once and reused everywhere it matters:** the owner has not
bought the VPS yet. Every number below assumes a **small VPS: 4 GB RAM, 2 vCPU** —
close to Hostinger's cheapest tier, already named in `docs/0-set-up/deployment.md`
§8.1. If the real VPS is bigger or smaller, these are the numbers to change.

**Sizing, largest to smallest — `db` gets the largest share, `migrate` is
short-lived, `caddy` is small:**

| Service | CPU limit | Memory limit | Why this size |
|---|---|---|---|
| `db` | 1.0 | 1536M | Postgres is the one service every other service depends on; give it the most headroom. |
| `migrate` | 0.5 | 256M | Runs once, briefly, then exits — needs enough to apply migrations quickly, not to run continuously. |
| `web` | 0.5 | 256M | Handles all user HTTP traffic; a normal Go web server footprint. |
| `poller` | 0.25 | 128M | Runs once a night, briefly; the lightest continuous service. |
| `caddy` | 0.25 | 128M | A reverse proxy has a small, well-known footprint. |

Limits total 2.5 vCPU and 2304M memory. The CPU total (2.5) exceeds the assumed
2 vCPU host on paper — this is **intentional and safe**: a CPU limit is a ceiling on
how much of the host's CPU time a container may use *when it is running*, not a
reservation carved out of the host, and these five services do not all peak at the
same moment (the nightly poller runs once, `migrate` runs once per deploy, `web` and
`caddy` handle request traffic). Memory is different — a limit is a hard cap a
container is killed for exceeding, so the memory column is kept safely under the
assumed 4 GB total (2304M, leaving over 1.5 GB for the host OS and the Docker
daemon itself).

**Easy to change from one obvious place — the `.env` file, with defaults baked in:**

```yaml
services:
  db:
    deploy:
      resources:
        limits:
          cpus: "${DB_CPU_LIMIT:-1.0}"
          memory: "${DB_MEM_LIMIT:-1536M}"
  # ... one matching pair of env vars per service
```

`.env.example` documents each variable with the table above as a comment, so
changing a limit later is one line in `.env` — no `compose.yaml` edit, no rebuild
needed (only a `docker compose up -d`, since `.env` values apply at container
start).

**Verified: `deploy.resources.limits` (`cpus`, `memory`) is enforced by plain
`docker compose up` on a single host, not only by `docker stack deploy` under
Swarm** — this project runs neither Swarm nor `docker stack deploy` anywhere.
`deploy.resources.reservations` (a *minimum* guarantee), by contrast, is a
Swarm-only scheduling hint and has **no effect** under plain `docker compose up` —
so this design uses only `limits`, not `reservations`, to avoid documenting a
setting that would silently do nothing.

**Rejected:** no limits at all (today's state) — this is exactly the gap this
decision closes. **Rejected:** hardcoded numbers with no `.env` override — the owner
explicitly asked for the values to be "easy to change in one obvious place," and a
number buried in `compose.yaml` is not that; a documented `.env` variable with a
sensible default is.

### D4 — Security hardening per service

**Why:** every container in the archived design runs with Docker's full default
Linux capability set and a writable root filesystem — more privilege than any of
the five services uses. Hardening reduces what a compromised or buggy container can
do to the host or to the other containers, at effectively zero cost when done
correctly. **Done wrong, a container simply fails to start — worse than not
hardening it at all** — so each service's actual needs are checked below, not
assumed uniform.

**Every service gets, at minimum:**

```yaml
security_opt:
  - no-new-privileges:true
cap_drop:
  - ALL
```

`no-new-privileges:true` blocks a process from gaining more privilege than it
started with (for example via a setuid binary) — safe for every service, since none
of the five relies on privilege escalation. `cap_drop: ALL` removes every Linux
capability by default; each service then gets back only the specific capability it
actually needs, listed below.

**Per-service table — what each service gets beyond the baseline, and why:**

| Service | `cap_add` | `read_only` | `tmpfs` | Why |
|---|---|---|---|---|
| `db` | `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `SETUID`, `SETGID` | `true` | `/var/run/postgresql`, `/tmp` | The official `postgres` image's own startup script must, on first boot, take ownership of the data directory and switch from root to the `postgres` user — that needs exactly these five capabilities, the minimum the image's entrypoint requires. `pgdata` (the data directory) stays writable through its own named volume, unaffected by `read_only`; the socket directory and temp files need small `tmpfs` mounts since the rest of the root filesystem is read-only. |
| `migrate` | none | `true` | none | A static Go binary that reads migration files baked into the image and talks to Postgres over the network. It writes nothing to local disk, so it needs no extra capability and no writable path at all. |
| `web` | none | `true` | `/tmp` (small) | A static Go binary; all templates and static assets are embedded in the binary (`go:embed`), so nothing is read from or written to disk at runtime. A small `/tmp` is kept as cheap defensive headroom, not because a current code path needs it. |
| `poller` | none | `true` | `/tmp` (small) | Same reasoning as `web` — a static Go binary with no known disk-write path. |
| `caddy` | `NET_BIND_SERVICE` | `true` | `/tmp` | Caddy binds ports 80 and 443, both below 1024 — without `NET_BIND_SERVICE` a non-root-privileged process cannot open them, and `cap_drop: ALL` removes that capability by default. Caddy's certificate data (`/data`) and its autosaved config (`/config`) both stay writable through their own named volumes (`caddy_data`, `caddy_config`), unaffected by `read_only` — a named volume mount is writable regardless of the container's root-filesystem mode. |

**Key point, stated once because it explains four of the five rows:** `read_only:
true` only makes the container's own root filesystem read-only. Anything mounted as
a **named volume** (`pgdata`, `caddy_data`, `caddy_config`) or a **bind mount**
(the `Caddyfile`) stays writable or readable exactly as its own mount options say,
regardless of `read_only`. This is why `db` and `caddy` can be fully read-only on
their root filesystem while still writing real data — the write targets are volumes,
not the root filesystem.

**Rejected, considered and set aside:** running every service as a fixed non-root
numeric UID via `user:`. `web`, `poller`, and `migrate` already run as a non-root
`app` user, baked into the image by the existing `Dockerfile` (`USER app`) — no
change needed there. `db` and `caddy` are third-party images whose own entrypoints
manage their user-switching internally (`postgres` via `gosu`, `caddy` needing root
briefly to bind privileged ports); forcing a `user:` override on either would fight
the image's own startup logic and is more likely to break the container than to add
real security, for no benefit `cap_drop`/`cap_add` does not already provide.

### D5 — Build cache mounts for the Go build

**Why:** the owner builds the image **on the VPS itself** (unchanged from the
archived design's D4 — no registry, no CI). Every deploy today pays the full
compile cost from scratch: `go mod download` re-fetches every module, and each `go
build` recompiles every package, even when only one file changed.

**The fix — two BuildKit cache mounts, one for the module cache, one for the Go
build cache:**

```dockerfile
# syntax=docker/dockerfile:1
...
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -o /out/web    ./cmd/web
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -o /out/poller ./cmd/poller
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -o /out/migrate ./cmd/migrate
```

`/go/pkg/mod` is the Go module download cache (`GOMODCACHE`'s default location in
the `golang` base image); `/root/.cache/go-build` is the Go build cache (compiled
package objects, keyed by source hash). A `type=cache` mount persists both across
separate `docker build` runs on the same host — unlike a normal layer, it is not
invalidated just because an earlier `COPY` step changed. An unchanged dependency
set skips re-downloading entirely; an unchanged package skips recompiling.

**BuildKit requirement, stated so the owner is not surprised:** `--mount=type=cache`
is a BuildKit-only feature — it needs the `# syntax=docker/dockerfile:1` line at
the top of the `Dockerfile` (added by this change) and a Docker Engine new enough to
default to BuildKit (Docker 23.0+, already what `docs/0-set-up/deployment.md` §8.3
installs via `get.docker.com`, so no extra VPS setup step is needed).

**Rejected:** a separate, persistent named volume for `/go/pkg/mod` mounted into a
long-lived "builder" container. Rejected as unnecessary machinery — BuildKit's own
`type=cache` mount already gives the same persistence across builds, with no extra
service, no extra volume to manage, and no change to how `docker compose up
--build` is invoked.

## Per-service hardening — summary table (restated from D2–D4, one place to see it all)

| Service | `logging` | `deploy.resources.limits` | `security_opt` | `cap_drop` | `cap_add` | `read_only` | `tmpfs` |
|---|---|---|---|---|---|---|---|
| `db` | bounded (D2) | cpus 1.0, mem 1536M | `no-new-privileges:true` | `ALL` | `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `SETUID`, `SETGID` | `true` | `/var/run/postgresql`, `/tmp` |
| `migrate` | bounded (D2) | cpus 0.5, mem 256M | `no-new-privileges:true` | `ALL` | none | `true` | none |
| `web` | bounded (D2) | cpus 0.5, mem 256M | `no-new-privileges:true` | `ALL` | none | `true` | `/tmp` |
| `poller` | bounded (D2) | cpus 0.25, mem 128M | `no-new-privileges:true` | `ALL` | none | `true` | `/tmp` |
| `caddy` | bounded (D2) | cpus 0.25, mem 128M | `no-new-privileges:true` | `ALL` | `NET_BIND_SERVICE` | `true` | `/tmp` |

## Database objects

**This change creates no table, column, index, constraint, view, or migration.** It
touches no `internal/<module>/db/` folder and no `query.sql` file. The `database`
design gate does not apply — nothing here needs the owner's schema sign-off.

## Reverse-direction check — existing `make` targets and guards

Verified against the actual `Makefile` and `openspec/config.yaml`, not assumed:

- **`docker-up`, `docker-down`, `docker-logs`, `docker-migrate` (`Makefile` lines
  771–781).** All four call plain `docker compose ...` today. Each is updated to use
  the new `$(COMPOSE)` variable (D1, Trap 4) instead — the only change to these
  targets. Their `## ...` help text (shown by `make help`) is updated too, to name
  the new `deploy/docker/compose.yaml` location.
- **`backup-db` (`Makefile` line 783).** Unchanged itself — it still calls
  `./deploy/docker/backup-db.sh` (path updated for the move). The flag fix lives
  inside the script (D1, Trap 4).
- **`migrate-run` (`Makefile` line 131).** Unaffected. It runs `cmd/migrate`
  directly against `DATABASE_URL`, with no Docker or Compose involved at all — this
  change does not touch it.
- **`migration-guard`.** Unaffected. It scans the four `internal/<module>/db/
  migrations` directories on disk for duplicate version numbers; it has no
  knowledge of where `Dockerfile` or `compose.yaml` live.
- **`boundary-guard`.** Unaffected. It scans `internal/gateway/**/*.go` for an
  `internal/telemetry` import. This change touches no Go file at all.
- **`archive-guard`.** Unaffected. This change adds no file under
  `openspec/changes/archive/` and modifies none.
- **`sqlc`.** Unaffected. No `query.sql` file and no migration changes in this
  change, so no sqlc-generated type changes shape. `sqlc.yaml`'s four `sql:`
  entries are untouched.
- **`ui-guard` / `i18n-guard` / `money-guard` / `tz-guard` / `theme-guard`.**
  Unaffected — this change touches no `internal/gateway/templates` file and no
  `internal/gateway/handlers` file.
- **`.dockerignore` build-context exclusion of `openspec/changes/archive/`
  (archived design's own note).** Still true and still irrelevant to
  `archive-guard` — excluding a path from the Docker build context has no bearing
  on a git-history check. The exclusion itself moves unchanged into
  `deploy/docker/Dockerfile.dockerignore` (D1, Trap 2).

Finding: all of the above are unaffected or updated in this same change — verified
by reading the `Makefile` and the guard implementations, not assumed unaffected
because "it's just Docker."

## File list

| File | Change |
|---|---|
| `deploy/docker/Dockerfile` | Moved from repo root. Gains `# syntax=docker/dockerfile:1` and the `--mount=type=cache` lines (D5). Stage names, binaries, and non-root users unchanged. |
| `deploy/docker/Dockerfile.dockerignore` | Moved and renamed from the repo-root `.dockerignore` (D1, Trap 2). Same patterns, unchanged. |
| `deploy/docker/compose.yaml` | Moved from repo root. Because `--project-directory .` sets the base path to the repo root, every relative path stays as it was before the move — `env_file: .env`, `context: .`, `./deploy/docker/Caddyfile` (Trap 3, corrected). Only `build.dockerfile` gains the `deploy/docker/` prefix (Trap 1). `x-logging` anchor added (D2), `deploy.resources.limits` added per service (D3), `security_opt`/`cap_drop`/`cap_add`/`read_only`/`tmpfs` added per service (D4). Service list, `depends_on`, and healthchecks unchanged. |
| `deploy/docker/Caddyfile` | Moved from `deploy/Caddyfile`, unchanged content. |
| `deploy/docker/backup-db.sh` | Moved from `deploy/backup-db.sh`. `BACKUP_DIR` path fixed (Trap 4); `docker compose` call gains the `--project-directory . -f deploy/docker/compose.yaml` flags. |
| `Makefile` | New `COMPOSE` variable (D1, Trap 4). `docker-up`/`docker-down`/`docker-logs`/`docker-migrate`/`backup-db` targets updated to use it or the new script path. `## ...` help text updated. |
| `.env.example` | Adds the per-service `*_CPU_LIMIT`/`*_MEM_LIMIT` variables (D3), each commented with its default and the sizing table's rationale. |
| `docs/1-deploy/docker.md` | Every `docker compose ...` / `docker build ...` command updated to the new flags and paths. New short note on log rotation, resource limits, and hardening, each linking back to this design for the full rationale. |
| `docs/0-set-up/deployment.md` §8 | Every Docker command in the runbook updated to the new flags and paths. |
| `README.md` | "Project Structure" tree updated: `Dockerfile`, `compose.yaml`, `.dockerignore` move out of the root listing into `deploy/docker/`. |
| `cmd/README.md` | `cmd/migrate`'s row updated: `make docker-migrate` still the documented command (unchanged name), the linked doc path stays `docs/1-deploy/docker.md`. |
