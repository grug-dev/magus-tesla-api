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

Five services run together, defined in `compose.yaml`. Only one of them is
reachable from outside the VPS.

| Service | What it does | Waits for | Reachable from outside? |
|---|---|---|---|
| `db` | PostgreSQL. Stores all app data in a named volume, so data survives a restart. | Nothing. | No. |
| `migrate` | Runs all pending database migrations, then exits. Runs once per `up`. | `db` to report healthy. | No. |
| `web` | The HTTP gateway — serves the vehicle dashboard. | `migrate` to finish successfully. | No — only through `caddy`. |
| `poller` | Collects vehicle telemetry once a night. No web server of its own. | `migrate` to finish successfully. | No. |
| `caddy` | Reverse proxy. Gets an HTTPS certificate automatically and forwards traffic to `web`. | `web` to be up (retries on its own if `web` is briefly down). | **Yes** — the only service publishing ports (80, 443). |

`web`, `poller`, and `db` publish no ports at all. If someone reaches your
app, the request always goes through `caddy` first.

---

## 2. Local Docker use vs. everyday development

### What works locally: building the images

You can build every image on your Mac, to check they compile. This is worth
doing before a deploy:

```bash
# Check compose.yaml itself is valid.
docker compose config

# Build each image. These only build — no container starts, no database is
# touched, nothing on your machine changes.
docker build --target web .
docker build --target poller .
docker build --target migrate .
```

These four commands only build. They do not start `db`, `web`, `poller`, or
`caddy`. Nothing runs, and nothing on your machine changes.

### What does NOT work locally: `docker compose up`

**Do not run `docker compose up -d --build` on your Mac.** The `caddy`
service asks Let's Encrypt for a real HTTPS certificate for your
`BASE_DOMAIN`. On your Mac there is no public domain pointing at you, and
ports 80/443 are not reachable from the internet. Caddy will fail to get a
certificate and keep retrying — you will see `caddy` stuck restarting, not a
working app.

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
locally — see §2.

| Task | Command |
|---|---|
| Start everything (build if needed) | `docker compose up -d --build` |
| Stop everything (keeps your data) | `docker compose down` |
| Restart one service, e.g. `web` | `docker compose restart web` |
| Rebuild after a code change | `docker compose up -d --build` |
| Follow one service's logs, e.g. `web` | `docker compose logs -f web` |
| Follow every service's logs | `docker compose logs -f` |
| List what is running | `docker compose ps` |
| Check a service's health status | `docker compose ps` (the `STATUS` column shows `healthy`/`unhealthy` for `db` and `web`) |

`make docker-up`, `make docker-down`, and `make docker-logs` are shortcuts
for the first three rows above (`Makefile` targets, unchanged existing ones
plus these three new ones).

---

## 5. Deploying an update

```bash
# Get the latest code.
git pull
```

```bash
# Rebuild anything that changed, and restart it. Safe to always include
# --build — Docker's build cache makes a no-op rebuild fast.
docker compose up -d --build
```

You never need to guess whether a rebuild is required. `--build` costs almost
nothing when nothing changed, so just always run it.

---

## 6. Migrations

The `migrate` service runs automatically every time you run
`docker compose up -d --build`. It checks which migrations are already
applied and only runs the ones that are missing. If nothing is pending, it
exits immediately with no changes — this is safe to run as often as you like.

```bash
# See what migrate did on the last run (which migrations it applied, or
# that there was nothing to do).
docker compose logs migrate
```

To force it to run again by hand, without restarting the whole stack:

```bash
# Re-run just the migrate service.
docker compose up migrate
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
docker compose exec db psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

Once inside `psql`, run any query, e.g.:

```sql
-- Count rows in a table, as an example ad-hoc query.
SELECT count(*) FROM vehicle_snapshots;
```

Type `\q` to exit `psql`.

```bash
# Take a one-off manual dump (not the scheduled backup — see §8 for that).
docker compose exec -T db pg_dump -U <POSTGRES_USER> <POSTGRES_DB> > manual-dump.sql
```

```bash
# Restore a dump file into the running database. This OVERWRITES existing
# rows that the dump also contains — see the safety section (§10) first.
cat manual-dump.sql | docker compose exec -T db psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

---

## 8. Backups

`deploy/backup-db.sh` runs once a day, from the cron line set up in
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
gunzip -c backups/magus-2026-09-07.sql.gz | docker compose exec -T db psql -U <POSTGRES_USER> -d <POSTGRES_DB>
```

---

## 9. Troubleshooting

| Symptom | Command to run | Likely cause |
|---|---|---|
| `web` will not start | `docker compose logs web` | `migrate` did not finish successfully (see the next row), or a bad value in `.env` (e.g. `DATABASE_URL`). |
| `migrate` exits non-zero | `docker compose logs migrate` | `DATABASE_URL` is wrong or `db` is not reachable. Check `db`'s health with `docker compose ps`. |
| Caddy cannot get a certificate | `docker compose logs caddy` | The DNS A record (deployment.md §8.2) does not point at this VPS yet, or ports 80/443 are blocked by a firewall. |
| `poller` collects nothing | `docker compose logs poller` | Check the Tesla token is valid, and that the scheduled time (`POLLER_SCHEDULE_HOUR`/`POLLER_SCHEDULE_MINUTE`) has not passed yet today. |
| Database connection refused | `docker compose ps` | `db` is not healthy yet (wait for its healthcheck), or `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` in `.env` do not match what `DATABASE_URL` expects. |

---

## 10. Safety

Always safe to run at any time:

```bash
docker compose up -d --build
docker compose logs
docker compose ps
docker compose restart <service>
```

**`docker compose down -v` is destructive. Never run it unless you mean to
lose all data.**

```bash
# DANGER: this deletes the "pgdata" named volume — every row in the
# database, permanently, with no undo unless you have a backup.
docker compose down -v
```

Plain `docker compose down` (no `-v`) is safe — it stops the containers but
keeps the named volumes (`pgdata`, `caddy_data`, `caddy_config`), so your data
and certificates survive.

---

## 11. Switching to a managed database later

You can move from the bundled `db` service to a managed Postgres (RDS, Cloud
SQL, Supabase, or similar) at any time, with no code change:

1. Change `.env`'s `DATABASE_URL` to the managed host's connection string.
2. Delete (or comment out) the `db` service block in `compose.yaml`.
3. Remove `db: condition: service_healthy` from `migrate`'s `depends_on` in
   `compose.yaml` — nothing else depends on `db` directly.
4. Run `docker compose up -d --build`.

`DATABASE_URL` is already the one thing every service reads to find the
database. Nothing else in the code needs to change.
