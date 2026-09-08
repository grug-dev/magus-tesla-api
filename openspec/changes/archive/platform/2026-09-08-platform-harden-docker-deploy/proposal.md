# platform-harden-docker-deploy

## Why

`platform-add-docker-compose-deploy` built the first working Docker stack. It runs,
but it is not ready for a real VPS yet. Four gaps are open:

1. All Docker files sit at the repo root (`Dockerfile`, `compose.yaml`,
   `.dockerignore`). This clutters the root and mixes deploy config with app code.
2. Docker's default log driver has no size cap. A crash loop can fill a small VPS
   disk with logs, with no limit.
3. No service has a resource limit. One runaway container (for example a bug that
   leaks memory) can starve PostgreSQL of RAM or CPU and take the whole stack down.
4. No service has security hardening. Every container runs with full Linux
   capabilities and a writable root filesystem, more access than any of the five
   services actually need.

This change closes all four gaps, and adds one more improvement: Docker build cache
mounts, so a deploy on the VPS does not recompile every Go package from scratch each
time.

## What Changes

- Move every Docker file into `deploy/docker/`: `Dockerfile`, a Dockerfile-specific
  `Dockerfile.dockerignore` (replaces the root `.dockerignore`), `compose.yaml`,
  `Caddyfile`, and `backup-db.sh`. The repo root keeps none of these files.
- Fix the build context, the ignore-file lookup, and `.env` discovery so the move
  does not break the build or hide secrets from the containers. See design.md for
  the exact commands.
- Add a bounded log-rotation config to every service, so container logs can never
  grow without limit.
- Add a CPU and memory limit to every service, sized for a small VPS, and easy to
  change from one place.
- Add security hardening to every service: no privilege escalation, no unneeded
  Linux capabilities, and a read-only root filesystem where the service allows it.
- Add Docker build cache mounts for the Go build and module cache, so a rebuild on
  the VPS reuses work from the last build instead of starting cold every time.
- Update every doc, `Makefile` target, and script that names a moved path or an old
  command: `docs/1-deploy/docker.md`, `docs/0-set-up/deployment.md` §8, the root
  `README.md`, `cmd/README.md`, and `make help` text.

## Impact

- **Breaking:** No public Go interface changes. This is deploy-tooling only — no
  package under `internal/` changes.
- **Modules affected:** None inside `internal/`. This is a cross-cutting change to
  repo-root deploy files, `deploy/`, `Makefile`, and docs — same shape as
  `platform-add-docker-compose-deploy`, which is why this change is also
  `platform`-prefixed.
- **Read paths affected:** None. No query, table, index, or read path changes.
- **Database objects:** None. See design.md → "Database objects" — this change adds
  no table, column, index, constraint, view, or migration. The `database` design
  gate does not apply.
- **Operational impact:** Every existing `docker compose ...` command changes its
  exact flags (the compose file moves). Every doc and `make` target that runs one is
  updated in this same change — see design.md's D1 for the exact commands.
