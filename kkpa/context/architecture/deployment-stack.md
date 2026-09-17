# Deployment stack — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `deployment stack`, `docker compose stack`, `container stack`, `deploy stack`
- **Internal name:** `platform` (OpenSpec capability) — realized by `deploy/docker/compose.yaml`

## Component map

Deliberately empty. This guide was assembled from the `platform` OpenSpec capability spec, which
carries behavior and rules but no file paths, so populating a file map here would mean inventing
one. The files this invariant governs are named by role below: `deploy/docker/compose.yaml` (the
five services and their ordering), `deploy/docker/Dockerfile` (the multi-stage build and its
final per-service stages), `deploy/docker/Caddyfile` (the reverse proxy's routing),
`deploy/docker/backup-db.sh` (the database dump), `cmd/migrate/` (the one-shot migration
program), and the `COMPOSE` variable in the `Makefile` (the flags every command needs). Ask
CodeGraph for their current contents.

## How maintenance works

The stack runs **five services**, not four. Getting the count wrong is the most common mistake,
because the fifth exits immediately and is invisible in `docker stats`:

1. **database** — PostgreSQL. Its data persists across restarts and across recreation of every
   other service.
2. **migration step** — one-shot. Runs the schema migrations to completion, exits, and is **not**
   restarted after a successful exit.
3. **web gateway** — the HTTP application.
4. **poller** — the telemetry collector.
5. **reverse proxy** — provides automatic HTTPS and is the only externally reachable service.

**Startup is strictly ordered, and the order is enforced by the stack itself:**

    database (healthy) → migration step (exits 0) → web gateway + poller

The gateway and the poller are never started while the database is unreachable or while the
migrations have not completed successfully. A failing migration therefore stops the whole
deployment rather than letting a binary run against the wrong schema.

**Restart policy is not uniform.** Every long-running service restarts automatically after a
crash or host reboot. The migration step is the single exception and must stay that way.

**Which database the stack uses is one configuration value.** Pointing that value at a different
reachable PostgreSQL instance — and dropping the bundled database service — requires no source
code change.

**Every service runs hardened.** Each one has a CPU limit, a memory limit, a cap on its on-disk
log size, and the smallest set of Linux capabilities and filesystem write access it can function
with. Limits are read from configuration, so changing one takes effect on that service's next
start with no source file edit.

**Image builds reuse cache across builds on the same host.** An unchanged dependency set is not
re-downloaded and an unchanged package is not recompiled.

## Conventions & gotchas

- **The migration step must never restart after a successful exit.** It is one-shot by design; an
  automatic restart policy on it would re-run migrations on every stack event.
  _Source: spec platform — Requirement: Containerized Deploy Stack._
- **Never start the gateway or the poller before migrations exit successfully.** The ordering is
  part of the contract, not an optimization — it is what stops a binary running against a schema
  it does not match.
  _Source: spec platform — Requirement: Ordered Startup — Database, Then Migrations, Then Application._
- **The database's data must survive restart and recreation of every other service.** Any deploy
  step that would discard the database's persistent storage violates this requirement.
  _Source: spec platform — Requirement: Containerized Deploy Stack._
- **No secret may be baked into a built image.** Every secret — database credentials, Tesla and
  Google OAuth credentials, the session secret — is supplied as an environment variable at
  container start. The local `.env` and any private key file are excluded from the build context
  and must be absent from the resulting image.
  _Source: spec platform — Requirement: No Secret Is Baked Into a Built Image._
- **Keep the database host behind a single configuration value.** Switching to an externally
  hosted PostgreSQL instance must stay a config change, never a code change.
  _Source: spec platform — Requirement: A Single Configuration Value Selects the Database Host._
- **Every service needs a log size cap.** Without one, a crash-restart loop grows log data until
  the host disk fills, which takes down every service including the database. Adding a new
  service without a log cap reintroduces this. For how `web`, `poller`, and `caddy` also get
  named, rotated log files on the VPS — not just a size cap — see
  `architecture/deploy-log-files.md`.
  _Source: spec platform — Requirement: Bounded Container Log Growth._
- **Every service needs a CPU limit and a memory limit.** The purpose is isolation, not tuning:
  one runaway service must not be able to starve another — in particular the database — of what
  it needs to stay running.
  _Source: spec platform — Requirement: Per-Service Resource Limits._
- **Resource limits are configuration, never source.** An operator changes a limit and it applies
  on that service's next start, with no source file edit.
  _Source: spec platform — Requirement: Per-Service Resource Limits._
- **Each service runs least-privilege: minimum Linux capabilities, minimum filesystem write
  access, no privilege escalation.** A process inside a container cannot gain more privilege than
  the container started with. **The practical trap:** the root filesystem is read-only, so a
  feature that writes a file to disk fails with "read-only file system" until that specific path
  is granted writable storage. Grant the narrow path; do not remove the hardening.
  _Source: spec platform — Requirement: Least-Privilege Container Execution._
- **Do not break the build cache.** Builds reuse the Go module cache and the Go build cache
  across separate builds on the same host. Reordering the image build so dependency download no
  longer has its own cached layer silently returns every deploy to a cold, full recompile.
  _Source: spec platform — Requirement: Cached Go Builds On Repeated Deploys._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file — so a guide can be moved
without recounting `../..` segments.

- Architecture: `architecture/platform-time-zone.md` — the other half of the `platform`
  capability; unrelated to the stack, but the same spec file.
- Architecture: `architecture/schema-per-module.md` — the per-module migration directories the
  one-shot migration step applies, in order.
- Architecture: `architecture/nightly-cycle.md` — what the poller service actually runs once it
  has started.
- Architecture: `architecture/deploy-log-files.md` — named, rotated log files for `web`,
  `poller`, and `caddy`, and the host `logrotate` job that keeps them.
