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

You can run this whole stack locally with Docker, on your Mac:

```bash
# Build and start every service locally, the same way the VPS does.
docker compose up -d --build
```

**Use this only to rehearse the exact production stack before a VPS deploy.**
For everyday development — building the UI, editing templates, fixing a bug —
use the host workflow instead:

```bash
# Hot-reload dev server: rebuilds Go code and CSS on every file save.
make dev
```

`make dev` watches your files and rebuilds instantly. Docker does not —
every code change means a full `docker compose up -d --build`, which is much
slower. Reach for Docker locally only when you specifically want to test the
containerized stack, not for normal day-to-day coding.

---

## 3. First deploy

The first deploy to a VPS is a longer, one-time runbook — DNS, `.env`,
installing Docker, the redirect URIs, and more.

See **[`docs/0-set-up/deployment.md`](../0-set-up/deployment.md) §8** for
every step. Come back here once that is done.

---

## 4. Everyday commands

Run these from the repo root on the VPS (or locally, if testing the stack
there).

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

If `migrate` exits with a non-zero code, `web` and `poller` will **not**
start — see the troubleshooting table below.

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
