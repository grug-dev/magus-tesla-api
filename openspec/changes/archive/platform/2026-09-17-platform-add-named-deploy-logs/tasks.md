# Tasks — platform-add-named-deploy-logs

> **Dependencies / parallelism.**
> - **T1** (`compose.yaml` + `Caddyfile` + `.env.example`) has no dependency.
>   It is the **first** edit to `compose.yaml` and to `Caddyfile` for this
>   change. MAY run alongside T2 and T3 — disjoint files.
> - **T2** (the `logrotate` conf, new file) has no dependency — design.md
>   already fixes every directive. MAY run in parallel with T1 and T3.
> - **T3** (`Makefile` — new `vps-logs` target) has no dependency — design.md
>   already fixes the target's shape. MAY run in parallel with T1 and T2.
> - **T4** (`docs/1-deploy/docker.md` + the two other doc references found)
>   depends on **T1, T2, T3** — it documents the final env var name, file
>   paths, the `logrotate` conf's install path, and the exact `make` target
>   name, all of which must already be final.
> - **T5** (the KB guide + `INDEX.md` rows + `deployment-stack.md` update)
>   depends on **T1, T2, T3** for the same reason as T4 — it documents real
>   paths and conventions, not a description of intent. MAY run in parallel
>   with T4 — disjoint files.
> - **Leader-integrated step:** none. This change adds no database object
>   and no sqlc input (design.md → "Database objects: none"), so no codegen
>   re-run is needed. There is no Go code anywhere in this change, so no
>   `go build`/`go vet` signal applies to T1–T3 — the only checks available
>   are `docker compose config` (validates `compose.yaml` parses) and a
>   `logrotate -d` dry run (T2's own acceptance criterion), both left to the
>   owner to run on read-only grounds stated in T6.

## T1. `compose.yaml` + `Caddyfile` + `.env.example` — no dependency, parallel-ok with T2/T3

- [x] T1.1 Add `MAGUS_LOGS_DIR=/home/magus/magus-logs` to `.env.example`, with
      a one-line comment: the host folder bind-mounted into `web`, `poller`,
      and `caddy` for named log files (design.md D1).
      Acceptance: `.env.example` has the new line; the default value matches
      the VPS's actual home directory (`/home/magus`, per
      `docs/vps-installation.md`).
- [x] T1.2 In `deploy/docker/compose.yaml`, add to the `web` service:
      `entrypoint: ["sh", "-c", "exec /usr/local/bin/web >> /var/log/magus/web.log 2>&1"]`
      and a `volumes:` entry `${MAGUS_LOGS_DIR:-/home/magus/magus-logs}:/var/log/magus`.
      Do not remove or change `logging: *default-logging`, `read_only: true`,
      or any existing `tmpfs:` entry (design.md D5, D6.3).
      Acceptance: `docker compose --project-directory . -f
      deploy/docker/compose.yaml config` parses the file without error and
      shows the new `entrypoint` and `volumes` lines for `web`.
- [x] T1.3 Same as T1.2, for the `poller` service: `entrypoint` redirects to
      `/var/log/magus/poller.log`, same `volumes:` entry, same guard against
      removing `logging:`/`read_only:`/`tmpfs:`.
      Acceptance: same as T1.2, for `poller`.
- [x] T1.4 In `deploy/docker/compose.yaml`, add the same
      `${MAGUS_LOGS_DIR:-/home/magus/magus-logs}:/var/log/magus` line to the
      `caddy` service's existing `volumes:` list (alongside the Caddyfile
      mount and the two named volumes — do not remove those).
      Acceptance: `docker compose ... config` shows all four `caddy` volume
      entries, including the new one.
- [x] T1.5 In `deploy/docker/Caddyfile`, add a `log { output file
      /var/log/magus/caddy.log { roll_size 10mb roll_keep 3 roll_keep_for
      336h } }` block inside the `{$BASE_DOMAIN} { ... }` site block, above
      the existing `handle` blocks. Do not change either `handle` block.
      Acceptance: `docker compose ... config` still parses; the Caddyfile's
      `handle /internal/rerun/*` and `handle { }` blocks are byte-for-byte
      unchanged except for the new `log` block above them.

## T2. `logrotate` conf — no dependency, parallel-ok with T1/T3

- [x] T2.1 Create `deploy/docker/magus-logs.logrotate`, matching exactly
      `/home/magus/magus-logs/web.log` and
      `/home/magus/magus-logs/poller.log` (never a glob — design.md D8),
      with the block: `daily`, `rotate 14`, `size 10M`, `compress`,
      `delaycompress`, `copytruncate`, `missingok`, `notifempty` — every
      directive design.md D7 lists, none omitted, none added.
      Acceptance: `logrotate -d deploy/docker/magus-logs.logrotate` (dry
      run) parses the file with no syntax error (path existence errors are
      expected off the VPS and are not a failure here).

## T3. `Makefile` — new `vps-logs` target — no dependency, parallel-ok with T1/T2

- [x] T3.1 Add `vps-logs: ## Tail the named log files under MAGUS_LOGS_DIR
      (web.log, poller.log, caddy.log) — VPS only` running
      `tail -f $(MAGUS_LOGS_DIR)/*.log`, next to the existing `docker-logs`
      target. Add `vps-logs` to the `.PHONY` list alongside `docker-logs`.
      Do not change `docker-logs` itself (design.md D9).
      Acceptance: `make help` lists `vps-logs` with its description;
      `docker-logs`'s target body is unchanged (`$(COMPOSE) logs -f`).

## T4. Docs — depends on T1, T2, T3

- [x] T4.1 In `docs/1-deploy/docker.md`, update the troubleshooting table
      (§9): the `web` and `poller` rows' command column changes from
      `docker compose ... logs web` / `logs poller` to
      `tail -100 ~/magus-logs/web.log` / `tail -100 ~/magus-logs/poller.log`
      (design.md — required, catch 1 in the dispatch). Leave the `migrate`,
      `caddy`, and database rows unchanged — they still use `docker compose
      ... logs <service>` (design.md D3).
      Acceptance: the two updated rows no longer contain `docker compose ...
      logs web` / `logs poller`; the other three rows are unchanged.
- [x] T4.2 In `docs/1-deploy/docker.md`, add a new numbered section (after
      §9 Troubleshooting) with the two-case runbook from design.md D13: (a)
      one file hits `size 10M` early — force `logrotate -f`, lower `size`,
      find the noisy log line; (b) the VPS disk fills up — `df -h`, `du -sh
      ~/magus-logs`, delete old `.gz` files, lower `rotate 14`. State
      explicitly that no automated disk-usage alert exists (design.md D13
      rejected one) and why.
      Acceptance: the new section exists, covers both cases (a) and (b) by
      name, and states the no-cron-script decision with its reason.
- [x] T4.3 In `docs/1-deploy/docker.md`'s §4 "Everyday commands" table (or
      immediately after it), add a row/line for `make vps-logs`
      alongside the existing `make docker-logs` shortcut note, and one
      sentence distinguishing the two (Docker's own driver vs. the named
      files) — mirrors design.md D9.
      Acceptance: `make vps-logs` appears in the doc, with a one-line
      description distinct from `make docker-logs`'s existing description.
- [x] T4.4 Add the VPS setup steps from design.md D6 to
      `docs/1-deploy/docker.md`'s new section (T4.2) or its own short
      subsection: `mkdir -p ~/magus-logs`; discover the container `app`
      user's real UID with `docker compose ... run --rm web id -u app`
      (do not assume it is `1000`); `sudo chown -R <uid>:<uid>
      ~/magus-logs`; install `deploy/docker/magus-logs.logrotate` to
      `/etc/logrotate.d/magus-logs`; verify with
      `logrotate -d /etc/logrotate.d/magus-logs`.
      Acceptance: every one of the five steps above appears, in this order,
      with a runnable command for each.
- [x] T4.5 Found while grounding this change (dispatch catch 7): two more
      doc lines point at `docker compose ... logs poller` and will mislead
      once `poller`'s output moves to a named file. Update both to the
      named-file command from T4.1:
      - `docs/0-set-up/deployment.md`, the "Check the poller log to confirm
        the listener started" step (`logs poller --tail=20`) — change to
        `tail -100 ~/magus-logs/poller.log`.
      - `docs/vps-installation.md`, follow-up **F4 — "First poller run"**
        (`logs poller`) — change to `tail -100 ~/magus-logs/poller.log`.
      Do **not** touch `docs/vps-installation.md`'s follow-up **F6 — "Log
      rotation for `/var/log/magus-backup.log`"**: that file is a host cron
      redirect, not a container log, and out of scope for this change
      (design.md D10). Leave F6's text and its ⬜ status exactly as they
      stand.
      Acceptance: both updated lines show the `tail` command instead of
      `docker compose ... logs poller`; `grep -n "logs poller"
      docs/0-set-up/deployment.md docs/vps-installation.md` returns nothing;
      F6's text in `docs/vps-installation.md` is byte-for-byte unchanged.

## T5. Knowledge base — depends on T1, T2, T3

- [x] T5.1 Create `kkpa/context/architecture/deploy-log-files.md`, in the
      same section shape as `kkpa/context/architecture/deployment-stack.md`
      (Glossary / Component map / How maintenance works / Conventions &
      gotchas / Related KB). Cover: which services write named files and
      which stay on Docker's driver (design.md D1, D3, D4); the 14-day
      `logrotate` retention and why `copytruncate` is mandatory, including
      its accepted lost-line window (design.md D7); why `caddy` is excluded
      from the `logrotate` conf (design.md D8); the two `make` targets and
      when to use each (design.md D9); the UID-ownership step (design.md
      D6). Link back to `deployment-stack.md` under "Related KB".
      Acceptance: the file exists with all five section headers; every
      named service (`web`, `poller`, `caddy`, `db`, `migrate`) is
      mentioned with its actual log destination.
- [x] T5.2 Add rows to `kkpa/context/INDEX.md`'s `## Architecture topics`
      table for `deploy logs`, `magus-logs`, `log rotation`, and
      `logrotate`, each pointing at
      `architecture/deploy-log-files.md`, in the same row format as the
      existing entries in that table (topic name, one-line description,
      `→ path` or `| path |` per the table's own column style).
      Acceptance: `grep -n "deploy-log-files.md" kkpa/context/INDEX.md`
      shows at least four rows.
- [x] T5.3 Update `kkpa/context/architecture/deployment-stack.md`'s
      "Conventions & gotchas" entry "Every service needs a log size cap" —
      it currently describes only the `x-logging` cap and says nothing
      about naming or rotation, which is now incomplete. Add one sentence
      pointing to the new guide (`architecture/deploy-log-files.md`) for the
      naming/rotation/retention story, without duplicating that guide's
      content here. Add `architecture/deploy-log-files.md` to this file's
      "Related KB" list.
      Acceptance: the "Every service needs a log size cap" bullet
      references the new guide; "Related KB" lists it.

## T6. Verification — depends on T1–T5

- [x] T6.1 Run `docker compose --project-directory . -f
      deploy/docker/compose.yaml config` and confirm it exits 0 with no
      warning about the new `entrypoint`, `volumes`, or `${MAGUS_LOGS_DIR}`
      lines.
- [ ] T6.2 Run `logrotate -d deploy/docker/magus-logs.logrotate` and confirm
      no syntax error.
- [x] T6.3 Run `make help` and confirm `vps-logs` is listed with its
      description, next to `docker-logs`.
- [x] T6.4 Grep the repo for `docker compose ... logs web` and
      `docker compose ... logs poller` outside
      `openspec/changes/archive/` and confirm every remaining hit is
      intentional (the unchanged database/migrate/caddy rows in
      `docs/1-deploy/docker.md` §9, and nowhere else).
      This is **not** `go test`/`make test`/`make check` — those stay the
      owner's, per the Test-Execution-Policy. There is no Go code in this
      change for them to exercise.
      **On the VPS, after deploy** (owner-run, not part of this checklist):
      confirm `~/magus-logs/web.log`, `poller.log`, and `caddy.log` exist
      and have content; confirm `/etc/logrotate.d/magus-logs` dry-runs
      clean against the real files.
