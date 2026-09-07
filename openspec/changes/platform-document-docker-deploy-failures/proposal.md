# platform-document-docker-deploy-failures

Source: MAG-53 — https://linear.app/magus/issue/MAG-53

## Why

The owner ran the first real Docker deploy on a fresh VPS, following
`docs/0-set-up/deployment.md` §8 (written by `platform-add-docker-compose-deploy`
and `platform-harden-docker-deploy`). It failed on the very first command:

```
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build
✘ Container magus-tesla-api-db-1   Error dependency db failed to start
```

`db`'s logs showed a restart loop: `POSTGRES_PASSWORD` was empty in `.env`.
`.env.example` ships it empty, and a `.env` built for host development never
needs it — so nothing before this deploy ever forced the owner to notice the gap.

While diagnosing this, a second gap turned up. `make db-setup` and
`make env-setup` look like the right setup commands on a fresh machine — they are
the ones §1–§4 of this same doc use. They are wrong for a Docker deploy:
`make env-setup` defaults `DATABASE_URL` to `localhost` (the Docker host is `db`)
and never asks for `POSTGRES_USER`/`PASSWORD`/`DB`/`BASE_DOMAIN`; `make db-setup`
runs `psql` against a host Postgres and needs `goose` installed, while the `db`
container provisions itself on first boot. Nothing in the docs told the owner not
to reach for them.

Both gaps are real production incidents, caught only because the owner deployed by
hand and read the logs. This change closes them in the docs, so the next reader —
on a fresh machine, possibly under pressure — does not repeat either mistake.

## What Changes

- `docs/0-set-up/deployment.md` §8.5 — turn the existing "must not be empty" table
  note into a visible warning that names the exact `db` container error, so a
  reader who already hit the restart loop can search for it and find the fix.
- `docs/0-set-up/deployment.md` §8 — add a warning, placed before §8.5 (where a
  reader would first reach for `make env-setup` / `make db-setup`), that both
  commands belong to the host development path and must not be used for a Docker
  deploy, with the reason for each.
- `docs/1-deploy/docker.md` — add a troubleshooting-table row for the `db`
  restart loop: symptom, the command to see the logs, and the fix. Note that
  changing `POSTGRES_PASSWORD` after the database already has data does not
  change the existing password — confirmed against the official `postgres` image's
  documented behavior.

No code, schema, or command changes. This change only makes an existing failure
mode and an existing wrong-tool trap visible in the docs before a reader hits them.

## Impact

- **Breaking:** No. Documentation only — no package under `internal/` changes, no
  command, flag, or file path changes.
- **Modules affected:** None inside `internal/`. Cross-cutting docs change, hence
  the `platform-` prefix, same as `platform-harden-docker-deploy`.
- **Read paths affected:** None.
- **Database objects:** None. See design.md → "Database objects" — this change
  adds no table, column, index, constraint, view, or migration. The `database`
  design gate does not apply.
- **Operational impact:** None. No `docker compose` command, flag, or path
  changes. A reader following the updated runbook sets `POSTGRES_PASSWORD` and
  skips `make env-setup`/`make db-setup` for the Docker path — the deploy itself
  is unchanged.
