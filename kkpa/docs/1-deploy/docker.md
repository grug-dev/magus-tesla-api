# Docker command reference

This is the doc you come back to **after** you already deployed once. It has
every day-to-day Docker command for this project: start, stop, logs,
migrations, database access, backups, and what to do when something breaks.

For the **first-time VPS setup** (buying a VPS, DNS, `.env`, the first
`docker compose up`), see
**[`docs/0-set-up/deployment.md`](../0-set-up/deployment.md) §8** instead.
This file does not repeat those steps.

---

## 1. The mental model

Five services run together, defined in `deploy/docker/compose.yaml`. Only one
of them is reachable from outside the VPS.

| Service | What it does | Waits for | Reachable from outside? |
|---|---|---|---|
| `db` | PostgreSQL. Stores all app data in a named volume, so data survives a restart. | Nothing. | No. |
| `migrate` | Runs all pending database migrations, then exits. Runs once per `up`. | `db` to report healthy. | No. |
| `web` | The HTTP gateway — serves the vehicle dashboard. | `migrate` to finish successfully. | No — only through `caddy`. |
| `poller` | Collects vehicle telemetry once a night. No web server of its own. | `migrate` to finish successfully. | No. |
| `caddy` | Reverse proxy. Gets an HTTPS certificate automatically and forwards traffic to `web`. | `web` to be up (retries on its own if `web` is briefly down). | **Yes** — the only service publishing ports (80, 443). |

`web`, `poller`, and `db` publish no ports at all. If someone reaches your
app, the request always goes through `caddy` first.

Every service also runs hardened: a bounded log size, a CPU and memory
limit, and reduced Linux privileges (no more than each service actually
needs). The exact numbers and the reasoning behind them live in
[`openspec/changes/platform-harden-docker-deploy/design.md`](../../openspec/changes/platform-harden-docker-deploy/design.md)
— read that file for the numbers, not this doc, so this page cannot drift
out of sync with the real `compose.yaml`.

---

## 2. Local Docker use vs. everyday development

### What works locally: building the images

You can build every image on your Mac, to check they compile. This is worth
doing before a deploy:

```bash
# Check compose.yaml itself is valid.
docker compose --project-directory . -f deploy/docker/compose.yaml config

# Build each image. These only build — no container starts, no database is
# touched, nothing on your machine changes. The build context stays the
# repo root (.) — only the -f flag points at the moved Dockerfile.
docker build -f deploy/docker/Dockerfile --target web .
docker build -f deploy/docker/Dockerfile --target poller .
docker build -f deploy/docker/Dockerfile --target migrate .
```

These four commands only build. They do not start `db`, `web`, `poller`, or
`caddy`. Nothing runs, and nothing on your machine changes.

### What does NOT work locally: `docker compose up`

**Do not run
`docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build`
on your Mac.** The `caddy` service asks Let's Encrypt for a real HTTPS
certificate for your `BASE_DOMAIN`. On your Mac there is no public domain
pointing at you, and ports 80/443 are not reachable from the internet.
Caddy will fail to get a certificate and keep retrying — you will see
`caddy` stuck restarting, not a working app.

`docker compose up` is a **VPS-only** command. It is meant for the real
deploy, where `BASE_DOMAIN` points at a real server with ports 80/443 open.
Nobody has run it locally, and no local workaround for the certificate
problem has been tested — do not assume one exists.

### What to use instead

For everyday development — building the UI, editing templates, fixing a bug —
use the host workflow:

```bash
# Hot-reload dev server: rebuilds Go code and CSS on every file save.
make dev
```

`make dev` watches your files and rebuilds instantly. Docker does not —
every code change would mean a full rebuild, which is much slower. `make dev`
is the everyday tool; Docker's role locally is only the build check above.

To rehearse the deploy's migration step locally, with no Docker at all:

```bash
# Runs the same migration program the "migrate" container runs, directly
# against your local database.
make migrate-run
```

This has been run and works. It is the real way to check a migration before
a deploy, without needing Docker or a VPS.

---

## 3. First deploy

The first deploy to a VPS is a longer, one-time runbook — DNS, `.env`,
installing Docker, the redirect URIs, and more.

See **[`docs/0-set-up/deployment.md`](../0-set-up/deployment.md) §8** for
every step. Come back here once that is done.

---

## 4. Everyday commands

Run these from the repo root on the VPS. `docker compose up` does not work
locally — see §2. Every command below needs the same two flags,
`--project-directory . -f deploy/docker/compose.yaml`, because
`compose.yaml` now lives in `deploy/docker/`, not the repo root — `make`
wraps these flags for you, so if you use `make` you can skip them (see the
shortcuts note below the table).

| Task | Command |
|---|---|
| Start everything (build if needed) | `docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build` |
| Stop everything (keeps your data) | `docker compose --project-directory . -f deploy/docker/compose.yaml down` |
| Restart one service, e.g. `web` | `docker compose --project-directory . -f deploy/docker/compose.yaml restart web` |
| Rebuild after a code change | `docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build` |
| Follow one service's logs, e.g. `web` | `docker compose --project-directory . -f deploy/docker/compose.yaml logs -f web` |
| Follow every service's logs | `docker compose --project-directory . -f deploy/docker/compose.yaml logs -f` |
| List what is running | `docker compose --project-directory . -f deploy/docker/compose.yaml ps` |
| Check a service's health status | `docker compose --project-directory . -f deploy/docker/compose.yaml ps` (the `STATUS` column shows `healthy`/`unhealthy` for `db` and `web`) |

`make docker-up`, `make docker-down`, and `make docker-logs` are shortcuts
for the first three rows above (`Makefile` targets, unchanged existing ones
plus these three new ones).

`make docker-logs` follows Docker's own log driver — the only place `db`'s
and `migrate`'s output goes. `make vps-logs` tails the named files under
`MAGUS_LOGS_DIR` instead (`web.log`, `poller.log`, `caddy.log`) — use it to
read those three on the VPS. See §10 for when to use each.

---

## 5. Deploying an update

```bash
# Get the latest code.
git pull
```

```bash
# Rebuild anything that changed, and restart it. Safe to always include
# --build — Docker's build cache makes a no-op rebuild fast.
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build
```

You never need to guess whether a rebuild is required. `--build` costs almost
nothing when nothing changed, so just always run it.

---

## 6. Migrations

The `migrate` service runs automatically every time you run
`docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build`.
It checks which migrations are already applied and only runs the ones that
are missing. If nothing is pending, it exits immediately with no changes —
this is safe to run as often as you like.

```bash
# See what migrate did on the last run (which migrations it applied, or
# that there was nothing to do).
docker compose --project-directory . -f deploy/docker/compose.yaml logs migrate
```

To force it to run again by hand, without restarting the whole stack:

```bash
# Re-run just the migrate service.
docker compose --project-directory . -f deploy/docker/compose.yaml up migrate
```

```bash
# Shortcut for the command above.
make docker-migrate
```

If `migrate` exits with a non-zero code, `web` and `poller` will **not**
start — see the troubleshooting table below.

### Running migrations locally (without Docker)

`cmd/migrate` is a small Go program, not the goose CLI (see its file's doc
comment for why). You do not need Docker to run it — `make migrate-run` runs
the exact same program directly against your `DATABASE_URL`:

```bash
# Apply pending migrations locally, using the same program the Docker
# "migrate" service runs — no goose CLI install needed.
make migrate-run
```

This differs from the older `make migrate-up`, which is still there and still
works:

| Target | Runs | Needs the `goose` CLI installed? |
|---|---|---|
| `make migrate-up` | The `goose` command-line tool, once per module directory | Yes |
| `make migrate-run` | `cmd/migrate`, the same Go program the `migrate` container runs | No |

Both apply the same migrations, in the same order (the Makefile's
`MIGRATIONS_DIRS`). `make migrate-run` exists so you can test the exact
program the deploy path uses, on your own machine, before pushing to the VPS.

---

## 7. Database access

```bash
# Open an interactive psql session inside the db container. Replace
# <POSTGRES_USER> and <POSTGRES_DB> with the values from your .env.
docker compose --project-directory . -f deploy/docker/compose.yaml exec db psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

Once inside `psql`, run any query, e.g.:

```sql
-- Count rows in a table, as an example ad-hoc query.
SELECT count(*) FROM vehicle_snapshots;
```

Type `\q` to exit `psql`.

```bash
# Take a one-off manual dump (not the scheduled backup — see §8 for that).
docker compose --project-directory . -f deploy/docker/compose.yaml exec -T db pg_dump -U <POSTGRES_USER> <POSTGRES_DB> > manual-dump.sql
```

```bash
# Restore a dump file into the running database. This OVERWRITES existing
# rows that the dump also contains — see the safety section (§11) first.
cat manual-dump.sql | docker compose --project-directory . -f deploy/docker/compose.yaml exec -T db psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

---

## 8. Backups

`deploy/docker/backup-db.sh` runs once a day, from the cron line set up in
`docs/0-set-up/deployment.md` §8.10. It writes a gzipped dump to:

```
backups/magus-YYYY-MM-DD.sql.gz
```

Files older than 7 days are deleted automatically.

```bash
# Confirm the cron job ran: lists backup files with their timestamps.
ls -la backups/
```

```bash
# Also check the cron log for errors.
cat /var/log/magus-backup.log
```

To restore a backup:

```bash
# Unzip and restore a dated backup file. Replace the filename and the
# <POSTGRES_USER>/<POSTGRES_DB> values with your own from .env.
gunzip -c backups/magus-2026-09-07.sql.gz | docker compose --project-directory . -f deploy/docker/compose.yaml exec -T db psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

---

## 9. Troubleshooting

| Symptom | Command to run | Likely cause |
|---|---|---|
| `web` will not start | `tail -100 ~/magus-logs/web.log` | `migrate` did not finish successfully (see the next row), or a bad value in `.env` (e.g. `DATABASE_URL`). |
| `web` will not start AND `web.log` is missing or empty | `docker compose --project-directory . -f deploy/docker/compose.yaml logs web` | The container died before it could write the file. Almost always the log folder is missing or not writable — see §10. The named file cannot exist in this case, so read Docker's own log instead. |
| `migrate` exits non-zero | `docker compose --project-directory . -f deploy/docker/compose.yaml logs migrate` | `DATABASE_URL` is wrong or `db` is not reachable. Check `db`'s health with `docker compose --project-directory . -f deploy/docker/compose.yaml ps`. |
| Caddy cannot get a certificate | `docker compose --project-directory . -f deploy/docker/compose.yaml logs caddy` | The DNS A record (deployment.md §8.2) does not point at this VPS yet, or ports 80/443 are blocked by a firewall. |
| `poller` collects nothing | `tail -100 ~/magus-logs/poller.log` | Check the Tesla token is valid, and that the scheduled time (`POLLER_SCHEDULE_HOUR`/`POLLER_SCHEDULE_MINUTE`) has not passed yet today. |
| Database connection refused | `docker compose --project-directory . -f deploy/docker/compose.yaml ps` | `db` is not healthy yet (wait for its healthcheck), or `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` in `.env` do not match what `DATABASE_URL` expects. |

---

## 10. Named log files and log rotation

`web` and `poller` write their output to named files on the VPS host,
`~/magus-logs/web.log` and `~/magus-logs/poller.log`, instead of Docker's
own hard-to-read path. `caddy` writes `~/magus-logs/caddy.log` and rotates it
itself. `db` and `migrate` are unchanged — read them with
`docker compose ... logs db` / `logs migrate`, as in §9.

**`caddy` needs two extra things, and both are easy to miss.** It runs as a
different user from `web` and `poller`, so the folder must be group-writable
for it (step 4 below) — without that it crash-loops and the site goes down.
And Caddy creates its log file as mode `0600` unless told otherwise, so the
Caddyfile sets `mode 0644`; without that you cannot read `caddy.log` with your
own user. `make logdir-guard` checks the mode. The folder permission is host
state, so no guard can check it — that one is on the setup steps below.

### One-time VPS setup

Do this once, the first time this stack starts on a VPS:

```bash
# 1. Create the host folder the containers write into.
mkdir -p ~/magus-logs
```

```bash
# 2. Find the real UID of the container's "app" user. Do not assume it is
# 1000 — it is a system user, and its UID is not guaranteed.
# --entrypoint is required: web's own entrypoint is a shell redirect, which
# ignores any command you pass and would start the server instead.
docker compose --project-directory . -f deploy/docker/compose.yaml run --rm --entrypoint id web -u app
```

```bash
# 3. Give that UID ownership of the folder. Replace <uid> with the number
# the command above printed.
sudo chown -R <uid>:<uid> ~/magus-logs
```

```bash
# 4. caddy runs as a different user than web and poller, so give the folder
# group 0 and group-write. Without this caddy cannot create caddy.log, it
# crash-loops, and the site goes down — caddy is the only service serving
# traffic.
sudo chgrp 0 ~/magus-logs && sudo chmod 775 ~/magus-logs
```

```bash
# 5. Install the logrotate conf that keeps 14 days of web.log/poller.log.
# caddy.log is deliberately not in it — Caddy rotates that file itself.
sudo cp deploy/docker/magus-logs.logrotate /etc/logrotate.d/magus-logs
```

```bash
# 6. Confirm the conf parses with no error (dry run — makes no change).
sudo logrotate -d /etc/logrotate.d/magus-logs
```

### If the stack will not start after this change

`web` and `poller` refuse to start when they cannot write their log file. The
container prints two lines and exits:

```
sh: /var/log/magus/web.log: Permission denied
FATAL: cannot write /var/log/magus/web.log - the host log folder is not writable by this container. Fix: docs/1-deploy/docker.md, section "Named log files and log rotation".
```

Read them with `docker compose ... logs web` — **not** with `tail`, because the
file does not exist yet. The first line names the real cause:

- **`Permission denied`** — the folder exists but the container's user does not
  own it. Redo steps 2 and 3 above.
- **`No such file or directory`** — the folder was never created. Redo step 1.

This is deliberate. An earlier version started the binary anyway and died on the
redirect, which looked like an unrelated crash.

### Reading the logs

```bash
# Tail all three named files at once.
make vps-logs
```

```bash
# Or one file at a time.
tail -100 ~/magus-logs/web.log
tail -100 ~/magus-logs/poller.log
tail -100 ~/magus-logs/caddy.log
```

### Case (a): one file grows fast and hits its 10 MB limit early

`logrotate` checks once a day, but a file also rotates as soon as it passes
10 MB, even between daily checks. If this keeps happening:

```bash
# Force an immediate rotation instead of waiting for the next check.
sudo logrotate -f /etc/logrotate.d/magus-logs
```

Find which log line repeats so often it fills the file:

```bash
tail -f ~/magus-logs/web.log
# then grep the text that keeps repeating, e.g.:
grep -c "some repeating line" ~/magus-logs/web.log
```

If the noise is expected to continue, lower `size 10M` in
`deploy/docker/magus-logs.logrotate` to rotate sooner, and re-install it
(step 4 above).

### Case (b): the VPS disk fills up

```bash
# Confirm the disk really is close to full.
df -h
```

```bash
# Confirm the logs are the cause, not backups or the database.
du -sh ~/magus-logs
```

If the logs are the cause, delete old rotated files by hand as an
immediate fix:

```bash
# Removes gzipped rotations older than 14 days — logrotate would have
# deleted these on its own eventually; this just does it now.
find ~/magus-logs -name '*.gz' -mtime +14 -delete
```

As a lasting fix, lower `rotate 14` in `deploy/docker/magus-logs.logrotate`
to keep fewer generations, and re-install it (step 4 above).

**There is no automated disk-usage alert.** A cron script that silently
stops working — a wrong path after a move, a missing dependency after an
OS update — is worse than no script: nobody notices it is broken until the
disk is already full. This runbook, read when disk use is already a
concern, has no failure mode of its own.

---

## 11. Safety

Always safe to run at any time:

```bash
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build
docker compose --project-directory . -f deploy/docker/compose.yaml logs
docker compose --project-directory . -f deploy/docker/compose.yaml ps
docker compose --project-directory . -f deploy/docker/compose.yaml restart <service>
```

**`docker compose down -v` is destructive. Never run it unless you mean to
lose all data.**

```bash
# DANGER: this deletes the "pgdata" named volume — every row in the
# database, permanently, with no undo unless you have a backup.
docker compose --project-directory . -f deploy/docker/compose.yaml down -v
```

Plain `docker compose down` (no `-v`) is safe — it stops the containers but
keeps the named volumes (`pgdata`, `caddy_data`, `caddy_config`), so your data
and certificates survive.

---

## 12. Switching to a managed database later

You can move from the bundled `db` service to a managed Postgres (RDS, Cloud
SQL, Supabase, or similar) at any time, with no code change:

1. Change `.env`'s `DATABASE_URL` to the managed host's connection string.
2. Delete (or comment out) the `db` service block in
   `deploy/docker/compose.yaml`.
3. Remove `db: condition: service_healthy` from `migrate`'s `depends_on` in
   `deploy/docker/compose.yaml` — nothing else depends on `db` directly.
4. Run `docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build`.

`DATABASE_URL` is already the one thing every service reads to find the
database. Nothing else in the code needs to change.
