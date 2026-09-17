# platform-add-named-deploy-logs

Source: MAG-84 — https://linear.app/magus-monitor/issue/MAG-84/store-docker-deploy-logs-as-named-rotated-files-under-magus-logs

Unit tests: excluded

## Why

On the VPS, Docker stores every container's logs at
`/var/lib/docker/containers/<64-hex-id>/<id>-json.log`. The `<64-hex-id>` is a
random container ID. Nobody can read that path and know it is `web` or
`poller`. Finding a service's log means running `docker inspect` first, just
to get the path.

Disk safety is not the problem. `deploy/docker/compose.yaml` already caps
every service's log at 10 MB × 3 files = 30 MB, through the `x-logging`
anchor. This change does not touch that cap. The problem is only the name and
the location of the file.

## What Changes

- `web` and `poller` write their logs to named files — `web.log`,
  `poller.log` — under a bind-mounted host folder, `~/magus-logs`, instead of
  to Docker's own log store.
- `caddy` writes its own log to `caddy.log` in the same folder, using its
  built-in log rotation.
- `db` and `migrate` are unchanged — they keep Docker's `json-file` driver
  and stay readable with `docker compose logs`.
- A host `logrotate` job purges `web.log` and `poller.log` after 14 days.
  `caddy.log` is not touched by `logrotate` — Caddy rotates it on its own.
- A new `make vps-logs` target tails the three named files. The existing
  `make docker-logs` (Docker's own log driver) is unchanged.
- Docs: a runbook for when a log file hits its size limit or the VPS disk
  fills up, and an update to the existing troubleshooting table so it reads
  the named files, not `docker compose logs`, for `web` and `poller`.
- Knowledge base: a new guide for this concept, new index rows so
  `kkpa-context-fetch` can find it, and an update to the existing deployment
  guide, which currently describes only the log-size cap and not the naming
  or rotation.

## Impact

- **Breaking:** No. This is deploy configuration only — YAML, a Caddyfile
  block, a `logrotate` config file, one `make` target, and docs. No Go code
  changes.
- **Modules affected:** None inside `internal/`. Same as every earlier
  `platform`-prefixed change, this is cross-cutting deploy tooling, not a
  module.
- **Database objects:** None. No table, column, index, or migration. The
  `database` design gate does not apply — see design.md.
- **Operational impact:** After this change, `docker compose logs web` and
  `docker compose logs poller` show almost nothing — their output moves to
  files. `docker.md`'s troubleshooting table is updated in this same change
  so nobody hits that surprise while debugging a real incident.
