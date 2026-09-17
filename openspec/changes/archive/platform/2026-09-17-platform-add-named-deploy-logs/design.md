# Design — platform-add-named-deploy-logs

## Context

`platform-harden-docker-deploy` (archived) added the `x-logging` anchor that
caps every service's log at 10 MB × 3 files. That cap still stands and this
change does not touch it. What it did not solve is naming: every service's
log still sits at Docker's own path,
`/var/lib/docker/containers/<64-hex-id>/<id>-json.log`. That path is Docker's
internal storage for the `json-file` driver and is not configurable per
service — you cannot ask Docker to name that file `web.log`.

The owner asked for named, rotated files under `~/magus-logs` on the VPS:
`web.log`, `poller.log`, `caddy.log`. This design turns that request into
exact YAML, a Caddyfile block, and a `logrotate` config.

**Database objects: none.** This change adds no table, column, index,
constraint, view, or migration. The `database` design gate does not apply.

## Goals / Non-Goals

**Goals:**
- `web`'s and `poller`'s logs land in named files a human can read without
  running `docker inspect` first.
- `caddy`'s log lands in the same folder, named, using Caddy's own rotation.
- `db` and `migrate` keep working exactly as they do today.
- Old log data is purged automatically — nobody has to remember to clean
  `~/magus-logs` by hand.
- Every doc and `make` target that already points at `web`'s or `poller`'s
  logs is updated in this same change, not left to silently mislead.

**Non-Goals:**
- No change to the `x-logging` 10 MB × 3 file cap on any service.
- No change to which services exist or their startup order.
- No cron script that watches disk usage and alerts. Rejected below (D13).
- No change to `/var/log/magus-backup.log`, the unrelated host-cron backup
  log. Rejected below (D10).

## Decisions

### D1 — Named files for `web`/`poller` via bind-mount + redirected entrypoint

Bind-mount a host folder into `web` and `poller` at `/var/log/magus`, and
override each service's `entrypoint` so the compiled binary's stdout/stderr
go straight into a named file there:

```yaml
# web
entrypoint: ["sh", "-c", "exec /usr/local/bin/web >> /var/log/magus/web.log 2>&1"]
volumes:
  - ${MAGUS_LOGS_DIR:-/home/magus/magus-logs}:/var/log/magus

# poller
entrypoint: ["sh", "-c", "exec /usr/local/bin/poller >> /var/log/magus/poller.log 2>&1"]
volumes:
  - ${MAGUS_LOGS_DIR:-/home/magus/magus-logs}:/var/log/magus
```

Both final Docker stages are `alpine:3.20` (`deploy/docker/Dockerfile`), so
`sh` (busybox) is present — no extra package needed.

**Why a bind-mount, not a Docker `local` log driver.** The `local` driver was
considered and **rejected**: it still writes to a Docker-managed path keyed
by the container ID, the exact 64-hex-ID problem this change exists to fix.
A bind-mount to a plain host folder is the only option that produces a file
name and location a human chooses.

**Why `${MAGUS_LOGS_DIR}`, not a literal `~/magus-logs`.** The owner's
request names `~/magus-logs`, but Compose does not expand `~` — it is a
shell feature, and Compose reads its YAML directly, not through a shell.
Writing `~/magus-logs` into `compose.yaml` would bind-mount a folder named
literally `~`. `${MAGUS_LOGS_DIR:-/home/magus/magus-logs}` reads a new `.env`
variable, defaulting to the VPS's actual home directory
(`/home/magus`, per the existing deploy — `deploy/docker/backup-db.sh` and
`docs/vps-installation.md` both already assume the `magus` user). This
follows the project's existing pattern: `DATABASE_URL` is the one `.env`
value every service reads for the database, and this is the same shape for
the log folder.

### D2 — `exec` in the redirect command is mandatory

The redirect must be `exec /usr/local/bin/web ...`, never a plain
`/usr/local/bin/web ...` without `exec`. Without `exec`, `sh` stays PID 1 and
the Go binary becomes a **child** process. A container's `docker stop` sends
`SIGTERM` to PID 1 only — busybox `sh` does not forward signals to a plain
child, so the Go binary would never see the shutdown signal, and `web`'s and
`poller`'s graceful-shutdown code (closing DB connections, letting an
in-flight request finish) would never run. `exec` replaces `sh` with the Go
binary as PID 1, so `SIGTERM` reaches it directly, exactly like today. Health
checks, `restart: unless-stopped`, and `docker compose stop` all depend on
this and are otherwise unaffected by this change.

### D3 — `db` and `migrate` keep the `json-file` driver, untouched

Both stay exactly as they are today, reachable with
`docker compose ... logs db` / `logs migrate`. Two reasons:

- Their entrypoints are the base Postgres image's own entrypoint and this
  project's `migrate` binary, both fragile to redirect — Postgres in
  particular does its own internal log-destination handling, and getting
  that wrong risks losing startup errors that matter (e.g. "port already in
  use", "data directory has wrong permissions").
- `migrate` is one-shot and already exits with a code Compose reports; a
  redirect buys it nothing.

**Rejected:** redirecting all five services for consistency. Rejected
because it adds real risk to `db` and `migrate` for no benefit — their logs
are read exactly the same way today, and this change does not need to touch
them to solve the naming problem.

### D4 — `caddy` uses its own native `log` directive, not a redirect

Caddy has first-class file logging with rotation built in — no redirect
hack needed. Add to `deploy/docker/Caddyfile`, inside the site block:

```caddyfile
{$BASE_DOMAIN} {
	log {
		output file /var/log/magus/caddy.log {
			roll_size 10mb
			roll_keep 3
			roll_keep_for 336h
		}
	}
	handle /internal/rerun/* {
		reverse_proxy poller:8081
	}
	handle {
		reverse_proxy web:8080
	}
}
```

(Syntax verified against Caddy's own Caddyfile `log` directive docs:
`output file <path> { roll_size <size> roll_keep <num> roll_keep_for
<duration> }`. `roll_keep_for` takes a Go duration string, hence `336h` for
14 days — matching `web.log`'s and `poller.log`'s own 14-day retention, D6.)

`caddy` needs the same bind-mount as `web`/`poller`:

```yaml
volumes:
  - ./deploy/docker/Caddyfile:/etc/caddy/Caddyfile:ro   # unchanged
  - caddy_data:/data                                     # unchanged
  - caddy_config:/config                                 # unchanged
  - ${MAGUS_LOGS_DIR:-/home/magus/magus-logs}:/var/log/magus   # new
```

**Rejected:** redirecting `caddy`'s own stdout/stderr like `web`/`poller`.
Rejected because Caddy's own rotation is more correct than a raw file
redirect — it understands its own log format and rotates on size, not only
disk-fill risk, with no `logrotate` involved at all (see D7).

### D5 — `x-logging` anchor stays on every service, including `web`/`poller`

Even though `web` and `poller` will emit almost nothing to Docker's own log
driver after the redirect (D1), the `x-logging` anchor (`json-file`, 10 MB ×
3 files, from `platform-harden-docker-deploy`) stays on every service. A
crash **before** the redirect takes effect — a bad `.env` value, a syntax
error in the entrypoint override itself — still lands in Docker's own log,
which is the only place `docker compose logs web` can show it. Removing the
anchor would silence that failure mode entirely.

### D6 — Host folder: creation, ownership, and path resolution

Three things must be true on the VPS before `web`/`poller` can write into
the bind-mounted folder:

1. **The folder must exist.** `docker compose up` does not create a missing
   bind-mount source folder's *parent* reliably across all Docker versions,
   and an operator should not depend on that. The setup docs add an explicit
   step: `mkdir -p /home/magus/magus-logs`.
2. **The folder must be writable by the container's `app` user.** Every
   final Dockerfile stage runs `addgroup -S app && adduser -S app -G app`,
   then `USER app` — a fresh Alpine **system** user, whose UID is assigned
   by `adduser -S` from Alpine's system-UID range, **not** guaranteed to be
   `1000`. The setup docs must not assume a UID match. The verified way to
   find the real one and fix ownership:
   ```bash
   docker compose --project-directory . -f deploy/docker/compose.yaml run --rm web id -u app
   sudo chown -R <that-uid>:<that-uid> /home/magus/magus-logs
   ```
   **Rejected:** assuming host UID 1000 matches and skipping this step.
   Rejected because it is not guaranteed for a `-S` (system) user, and a
   silent permission failure inside a `read_only: true` container is hard to
   diagnose — the binary would fail to open its log file and never start.
3. **`read_only: true` does not block this.** `web`, `poller`, and `caddy`
   all run with `read_only: true` on the container root filesystem
   (`platform-harden-docker-deploy`, unchanged by this design). A bind mount
   is not part of the root filesystem — it stays writable under
   `read_only: true` exactly like the existing `pgdata`, `caddy_data`, and
   `caddy_config` named volumes do. No change to `read_only` is needed
   anywhere.

**Path resolution note.** `compose.yaml`'s own header comment states every
relative path in the file resolves against ONE base path, set by
`--project-directory .` to the repo root — never against `deploy/docker/`,
the file's own folder. `${MAGUS_LOGS_DIR}` (D1) is written as an **absolute**
host path precisely to sidestep this rule rather than fight it: an absolute
bind-mount source is used as-is by Compose and never resolved against the
project directory. If `MAGUS_LOGS_DIR` is ever changed to a relative value,
it resolves against the repo root, exactly like `env_file: .env` already
does — the same rule that bit `platform-harden-docker-deploy` (its design.md
Trap 3) applies here without change.

### D7 — 14-day retention for `web.log`/`poller.log` via host `logrotate`

Ship `deploy/docker/magus-logs.logrotate` (installed to
`/etc/logrotate.d/magus-logs` on the VPS, per the setup docs), matching only
the two redirected files:

```
/home/magus/magus-logs/web.log
/home/magus/magus-logs/poller.log
{
	daily
	rotate 14
	size 10M
	compress
	delaycompress
	copytruncate
	missingok
	notifempty
}
```

Each directive, and why it is exactly this one:

| Directive | Why |
|---|---|
| `daily` | Check once a day — matches the nightly poller's own cadence and is `logrotate`'s standard check interval. |
| `rotate 14` | Keep 14 rotated generations — the owner's stated retention window. |
| `size 10M` | Also rotate early if a file passes 10 MB before its daily slot — mirrors the existing `x-logging` per-file cap, so a noisy day cannot make one file balloon between daily checks. |
| `compress` | Rotated files are gzipped — cuts disk use for anything kept past the first rotation. |
| `delaycompress` | Skips compressing the **most recently** rotated file for one more cycle. Needed together with `copytruncate` below: a process that still has the file open briefly after rotation should not have that file's content vanish under `gzip` mid-write. |
| `copytruncate` | **Mandatory — see the risk below.** |
| `missingok` | Do not error if a file is briefly absent, e.g. right after a fresh deploy before the first log line is written. |
| `notifempty` | Skip rotating an empty file — do not manufacture an empty `.gz` every day when a service is quiet. |

**Why `copytruncate` is mandatory, and its accepted cost.** The Go binaries
inside `web` and `poller` never reopen their output file — they hold one
open file descriptor to `/var/log/magus/web.log` (or `poller.log`) for their
whole process lifetime, because a plain shell redirect (D1) gives them no
signal to reopen on. `logrotate`'s default rotation **renames** the file and
creates a new empty one at the old name; the running process keeps writing
to the renamed file, which no longer has any name pointing at it — its
output becomes permanently invisible, still consuming disk, until the
process restarts. `copytruncate` avoids this: it **copies** the current
content to the rotated name, then **truncates the original file in place**
— the file the process already has open. The open file descriptor stays
valid throughout, because the file's identity (its inode) never changes,
only its length.

The accepted cost: **a short window exists between the copy and the
truncate where a log line written by the process can be lost** — a line
written after the copy snapshot but before the truncate lands in neither
the rotated file nor the fresh one. This is a known, standard limitation of
`copytruncate` and is accepted here: this is a log file, not a data store,
and a rare lost line during a nightly rotation is a fair trade against
making the Go binaries reopen files on a signal, which they do not do today
and which is out of scope for a configuration-only change.

**Rejected:** teaching `web`/`poller` to reopen their log file on `SIGHUP`
(the usual alternative to `copytruncate`). Rejected as out of scope — it is
a Go code change, and this change is deliberately configuration-only
(D-tests below). If `web`/`poller` ever grow real structured logging, that
is the moment to revisit this trade-off, not now.

### D8 — `caddy` is excluded from the `logrotate` conf

The `logrotate` file in D7 names `web.log` and `poller.log` explicitly, never
a glob like `*.log`. `caddy.log` is rotated by Caddy's own `roll_size` /
`roll_keep` / `roll_keep_for` directive (D4). Two rotators fighting over one
file corrupts or loses data — whichever runs second finds a file the first
already renamed or truncated out from under it. Keeping the glob narrow is
what prevents this, not a coincidence of file naming.

### D9 — `make vps-logs`, new; `make docker-logs`, unchanged

Add one `Makefile` target:

```make
vps-logs: ## Tail the named log files under MAGUS_LOGS_DIR (web.log, poller.log, caddy.log) — VPS only
	tail -f $(MAGUS_LOGS_DIR)/*.log
```

`docker-logs` (`$(COMPOSE) logs -f`) is **not changed**. It still follows
Docker's own log driver — the only place `db`'s and `migrate`'s output ever
goes (D3), and still a valid way to see a crash in `web`/`poller` **before**
the redirect took effect (D5). Two commands, two clear sources:
`make docker-logs` for the Docker driver, `make vps-logs` for the named
files.

**Rejected:** changing `docker-logs` to tail the named files instead.
Rejected because it would hide `db` and `migrate` entirely — exactly the two
services you read first when the stack will not start at all. That is a
regression, not a simplification.

### D10 — `/var/log/magus-backup.log` is out of scope; it stays where it is

`deploy/docker/backup-db.sh` runs on the **host**, from a daily cron line
(`docs/vps-installation.md`, `docs/0-set-up/deployment.md` §8), not inside
any container:

```
0 1 * * * cd /home/magus/magus-tesla-api && make backup-db >> /var/log/magus-backup.log 2>&1
```

This is a completely different mechanism from D1–D9: it is the host shell's
own `>>` redirect of a cron job's output, not a container log at all. Moving
it into `~/magus-logs` would mean also changing the `sudo touch` +
`sudo chown` setup step and the crontab line themselves — real work this
ticket never asked for. `docs/vps-installation.md` already tracks this file
as its own open follow-up, **F6 — "Log rotation for
`/var/log/magus-backup.log`"**, with its own stated reasoning ("small file,
slow growth, low urgency"). This change leaves F6 exactly as it stands and
does not fold it in.

### D11 — Unit tests are excluded

The owner chose this. Every file this change touches is configuration:
YAML, a Caddyfile block, a `logrotate` config, a `Makefile` target, and
docs. There is no new Go code, so there is nothing for `go test` to run.
Verification happens on the VPS instead: confirm `web.log`/`poller.log`/
`caddy.log` exist and have content after a deploy, and run
`logrotate -d /etc/logrotate.d/magus-logs` (dry run) to confirm the conf
parses and matches the right files.

### D12 — Knowledge-base documentation is in scope

The owner asked for it explicitly. Two pieces, both in English:

- A new guide, `kkpa/context/architecture/deploy-log-files.md`, in the same
  shape as its sibling `deployment-stack.md` (Glossary / Component map / How
  maintenance works / Conventions & gotchas / Related KB).
- New rows in `kkpa/context/INDEX.md`'s `## Architecture topics` table, so
  `kkpa-context-fetch` resolves `deploy logs`, `magus-logs`, `log rotation`,
  and `logrotate` to the new guide.

And one existing file needs an update, not just an addition:
`kkpa/context/architecture/deployment-stack.md`'s "Conventions & gotchas"
entry "Every service needs a log size cap" currently describes only the
`x-logging` cap from `platform-harden-docker-deploy`. It says nothing about
naming or the host-side `logrotate` job this change adds — it is now
incomplete, not merely silent. Update it to point at the new guide rather
than duplicating its content.

### D13 — A runbook for both size limits, no monitoring script

Add a new section to `docs/1-deploy/docker.md` covering two distinct
situations an operator can hit:

- **(a) One file grows fast and hits `size 10M` before its daily
  `logrotate` slot.** How to force an immediate rotation
  (`logrotate -f /etc/logrotate.d/magus-logs`), how to lower `size` if this
  keeps happening, and how to find which log line is repeating so fast
  (`tail -f`, then grep the repeating text).
- **(b) The VPS disk itself fills up.** `df -h` to confirm, `du -sh
  ~/magus-logs` to confirm the logs are the cause (vs. `backups/` or
  `pgdata`), delete old `.gz` files by hand as an immediate fix, and lower
  `rotate 14` to a smaller number as a lasting fix.

**Rejected: a cron script that watches disk usage and alerts.** The owner
considered and rejected this. A cron script that silently stops working
(wrong path after a move, a dependency missing after an OS update) is worse
than no script at all — nobody notices its absence until the disk is
already full, whereas a runbook a person reads when they already suspect a
problem has no failure mode of its own.

## What this change does NOT do

- It does not change `x-logging`'s 10 MB × 3 file cap on any service.
- It does not change `db`'s or `migrate`'s logging at all.
- It does not add log shipping to any external service (no Loki, no
  CloudWatch, nothing off-box). Purely local, rotated files.
- It does not change Go code in `internal/` or `cmd/` — every file touched
  is deploy configuration or documentation.
