# Deploy log files — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `deploy logs`, `magus-logs`, `log rotation`, `logrotate`, `named log files`
- **Internal name:** `platform` (virtual module) — realized by `deploy/docker/compose.yaml`,
  `deploy/docker/Caddyfile`, and `deploy/docker/magus-logs.logrotate`

## Component map

- `deploy/docker/compose.yaml` — `web` and `poller` services only: the `entrypoint:` write test,
  the redirect, and the `MAGUS_LOGS_DIR` bind-mount.
- `deploy/docker/magus-logs.logrotate` — the host `logrotate` conf for `web.log`/`poller.log`,
  installed to `/etc/logrotate.d/magus-logs` on the VPS.
- `.env.example` — `MAGUS_LOGS_DIR`, the host folder path (default
  `/home/magus/magus-logs`).
- `Makefile` — `vps-logs` target (tails the named files) next to the existing `docker-logs`
  target (follows Docker's own log driver).
- `docs/1-deploy/docker.md` §10 — the operator runbook: one-time VPS setup, reading the logs,
  and what to do when a file grows fast or the disk fills up.

## How maintenance works

Two of the five services write named files. The other three stay on Docker's own driver.
Getting a service's place wrong is the most common mistake when reading logs on the VPS:

| Service | Where its log goes | How to read it |
|---|---|---|
| `web` | `~/magus-logs/web.log`, a named file on the host | `tail -100 ~/magus-logs/web.log` or `make vps-logs` |
| `poller` | `~/magus-logs/poller.log`, a named file on the host | `tail -100 ~/magus-logs/poller.log` or `make vps-logs` |
| `caddy` | Docker's own `json-file` log driver | `docker compose ... logs caddy` |
| `db` | Docker's own `json-file` log driver | `docker compose ... logs db` |
| `migrate` | Docker's own `json-file` log driver | `docker compose ... logs migrate` |

**`web` and `poller` get a named file through a redirect, not a Docker feature.** Docker's own
`local` log driver was considered and rejected — it still writes to a path keyed by the
container's 64-character ID, the exact problem a named file solves. Instead, each service's
`entrypoint:` in `compose.yaml` is overridden to `exec` the compiled binary with its stdout and
stderr redirected straight into a file under `/var/log/magus/`, a folder bind-mounted from the
host's `MAGUS_LOGS_DIR`. The `exec` matters: without it, the shell running the redirect stays
process 1 inside the container, and `docker stop`'s shutdown signal never reaches the Go binary,
so graceful shutdown would silently stop working.

**`caddy` is deliberately NOT in the shared folder.** It was, for one release, and it took the
site down. Three facts collide: the folder must be owned by a single user; `caddy` runs as a
different user from `web` and `poller`; and `cap_drop: ALL` removes `DAC_OVERRIDE`, so even a
root process gets no exemption from the permission bits. Caddy could not create its file and
crash-looped. Making the folder writable for both users then produced a `caddy.log` the host
user could not read without `sudo` — a named log nobody can read is worse than no named log.
One folder cannot serve two trust boundaries; `caddy` keeps Docker's driver.

**`db` and `migrate` are deliberately left on Docker's driver.** Redirecting either risks losing
a startup error that matters (a full disk, a bad permission) before the redirect even takes
effect, and `migrate` is one-shot — Compose already reports its exit code, so a redirect buys it
nothing.

**The Docker log driver still exists as a fallback for `web`/`poller`.** The size cap from the
`x-logging` anchor (10 MB × 3 files) stays on every service, including `web` and `poller`. If
either crashes before its `entrypoint:` redirect takes effect — a bad `.env` value, a typo in
the override itself — that failure still lands in Docker's own log, reachable with
`docker compose ... logs web` / `logs poller`.

### 14-day retention, and why `copytruncate` is not optional

`web.log` and `poller.log` are rotated by a host-level `logrotate` job (not Docker), checked
daily, keeping 14 generations, gzip-compressed. The job **must** use the `copytruncate`
directive. `web` and `poller` open their log file once and never reopen it — the plain shell
redirect that sends their output there gives them no signal to reopen on. A normal
`logrotate` rotation renames the file and starts a fresh one at the old name; the running
process keeps writing to the renamed file, which nothing points at any more, so its output
becomes invisible until the process restarts. `copytruncate` avoids this: it copies the current
content to the rotated name, then truncates the *original* file in place, so the process's
already-open file stays valid.

**Accepted cost:** a line written between the copy and the truncate can be lost. This is a
known limit of `copytruncate`, accepted here because this is a log file, not a data store, and
because making the Go binaries reopen their log file on a signal is a code change, out of scope
for what is a configuration-only concern.

### Why the `logrotate` conf names files instead of a glob

`deploy/docker/magus-logs.logrotate` names `web.log` and `poller.log` explicitly — never a glob
like `*.log`. A glob picks up whatever lands in the folder later, including a file that already
rotates itself. Two rotators on one file lose data: whichever runs second finds a file the first
already renamed or truncated out from under it.

### Two `make` targets, two log sources

- **`make vps-logs`** — tails both named files under `MAGUS_LOGS_DIR` (`web.log`,
  `poller.log`). Use this for `web` or `poller` on a normal day.
- **`make docker-logs`** — unchanged; still follows Docker's own log driver. Use this for
  `caddy`, `db` or `migrate`, or to see a `web`/`poller` crash that happened *before* the
  redirect took effect. Changing `docker-logs` to read the named files instead was considered and rejected —
  it would hide `db` and `migrate`, the two services an operator reads first when the whole
  stack fails to start.

### One-time VPS setup: folder ownership

The bind-mounted folder must be writable by the container's `app` user before `web`/`poller` can
write into it. Every final Docker stage runs as a fresh Alpine **system** user (`adduser -S`),
whose UID is **not guaranteed to be `1000`** — never assume it is. Find the real UID and fix
ownership:

```bash
docker compose --project-directory . -f deploy/docker/compose.yaml run --rm --entrypoint id web -u app
sudo chown -R <that-uid>:<that-uid> ~/magus-logs
```

Skipping this step stops the container from starting. `web` and `poller` test the log file
before they exec their binary, so they exit with a `FATAL:` line naming the folder and the doc
section that fixes it. Read it with `docker compose ... logs web` — `tail` cannot work here,
because the named file is exactly what could not be created.
Full step-by-step runbook: `docs/1-deploy/docker.md` §10.

## Conventions & gotchas

- **Never assume the container `app` user's UID is `1000`.** It is a system user; its UID comes
  from Alpine's system-UID range. Discover it with `docker compose ... run --rm --entrypoint id web -u app` — the
  `--entrypoint` is required, because `web`'s own entrypoint is a shell redirect that ignores
  any command you pass and would start the server instead
  before `chown`-ing the log folder.
- **`copytruncate` is mandatory for `web.log`/`poller.log`, not a style choice.** Removing it
  would make `web`'s or `poller`'s log output vanish silently after every rotation, because
  neither process reopens its log file on a signal.
- **Never put a third service in the shared folder.** It is owned by one user on purpose. A
  service that runs as a different user either cannot write, or forces permissions loose enough
  that the files stop being readable by the host user. `caddy` already failed this way.
- **Never widen the `logrotate` conf to a glob.** It names its two files so a self-rotating file
  dropped in later can never get a second rotator.
- **`docker-logs` and `vps-logs` read different things — do not merge them.** `db` and
  `migrate` only ever appear in `docker-logs`.
- **The log folder is a hard startup dependency.** `web` and `poller` exit when they cannot
  write their file, on purpose — a container that starts but logs nowhere hides the problem
  until someone needs the log. Any new host, rebuilt VPS, or changed `MAGUS_LOGS_DIR` needs the
  ownership step first.
- **A `web`/`poller` crash before the redirect takes effect still only shows up in
  `docker compose ... logs`**, not in the named file — check both when the named file is empty.
- **`web` and `poller` must each keep a named log file at a fixed host path, readable by the
  host user without `sudo`.** An operator has to reach a service's log without first looking up
  a per-container id. `caddy`, `db` and `migrate` are exempt and stay on the container runtime's
  own log command.
  _Source: spec platform — Requirement: Named And Located Deploy Logs._
- **Named log data must be purged automatically past its retention period.** Without it the files
  grow until the host disk fills. Retention today is 14 days.
  _Source: spec platform — Requirement: Time-Bounded Named Log Retention._
- **A fast-growing log must rotate on size, not only on the daily check.** A noisy failure loop
  can fill a file long before the next scheduled run, so the size threshold has to fire on its
  own.
  _Source: spec platform — Requirement: Time-Bounded Named Log Retention._
- **Exactly one rotation mechanism may govern a log file.** A file that rotates itself is never
  also handled by the host mechanism. Two rotators on one file lose data.
  _Source: spec platform — Requirement: Time-Bounded Named Log Retention._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file — so a guide can be moved
without recounting `../..` segments.

- Architecture: `architecture/deployment-stack.md` — the five-service stack this guide's
  services belong to; the log-size-cap convention there points here for the naming/rotation
  story.
