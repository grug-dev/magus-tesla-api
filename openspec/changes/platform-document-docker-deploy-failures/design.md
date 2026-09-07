# Design — platform-document-docker-deploy-failures

## Context

`platform-harden-docker-deploy` (archived) shipped a docs-verified Docker
Compose deploy runbook. The owner then ran the *actual* first deploy on a fresh
VPS — the first time any of this ran outside the design author's own checking.
It failed on the first command, and diagnosing it surfaced a second gap. Both
gaps are documented here, verified against the real `Makefile` and the official
Postgres image docs, not assumed.

**Trap 3 of `platform-harden-docker-deploy`'s own design.md** is the precedent
for this change's tone: a design section that stated a wrong rule, was carried
through implementation and three doc rewrites, and was only caught when a real
command ran. This change exists because the same thing happened one level up —
this time the runbook itself, not a design doc, was trusted without a real run.

## Goals / Non-Goals

**Goals:**
- Make `POSTGRES_PASSWORD` impossible to skip: name the exact error a reader
  sees if they do, so it is searchable.
- Warn a reader away from `make env-setup` / `make db-setup` for the Docker path,
  before they reach that step, with the reason each one is wrong.
- Add the failure to `docs/1-deploy/docker.md`'s troubleshooting table, the doc a
  reader returns to after the first deploy.
- Verify every factual claim (the `Makefile` behavior, the Postgres image's
  documented behavior) instead of writing it from memory.

**Non-Goals:**
- No code, schema, `Makefile`, or `compose.yaml` change. The commands stay
  exactly as `platform-harden-docker-deploy` left them — only the docs around
  them change.
- No new `.env.example` variable. `POSTGRES_PASSWORD` already exists there,
  empty by default (`platform-add-docker-compose-deploy`'s own design). This
  change does not touch `.env.example`.
- No change to `make env-setup` or `make db-setup` themselves. They are correct
  for the host path they were built for; the fix is warning readers away from
  them on the Docker path, not changing what they do.

## Decisions

### D1 — Turn the `POSTGRES_PASSWORD` table note into a visible warning, with the exact error

**Why:** `docs/0-set-up/deployment.md` §8.5 already said, inside a table cell,
"`POSTGRES_PASSWORD` must not be empty." That was true and was not enough — a
table cell is easy to skim past while filling in six other values. The owner
skipped it and only found the mistake by reading container logs on a live VPS.

**The fix:** a warning block, placed right after the `.env` variable table in
§8.5, that quotes the real `db` container error:

```
Error: Database is uninitialized and superuser password is not specified.
       You must specify POSTGRES_PASSWORD to a non-empty value for the
       superuser.
```

A reader who already hit the restart loop can paste this line into a search and
land directly on the fix, instead of reading the whole section again. This
mirrors the project's own troubleshooting-table pattern
(`docs/0-set-up/deployment.md` §7, `docs/1-deploy/docker.md` §9): name the exact
symptom text, not a paraphrase.

**Verified, not assumed:** the error text is the owner's own log output, quoted
verbatim in the dispatch that produced this change — not reconstructed from
memory.

**Rejected:** rewording the table cell to be more emphatic ("MUST NOT be
empty, seriously"). Rejected because the problem was never the wording — it was
that a table cell is not where a reader's eye stops. A separate warning block
is.

### D2 — Warn against `make env-setup` / `make db-setup` for the Docker path, before §8.5

**Why:** both commands are the tools §1–§4 of this same doc train a reader to
reach for on a fresh machine. Nothing before this change said they are wrong
for §8's Docker path — so a reader who has just cloned the repo and is
following the doc top to bottom has every reason to try them at §8.5, right
where `.env` needs filling in.

**Verified against the `Makefile`, not assumed:**
- `env-setup` (`Makefile` lines 190+): prompts for `SESSION_SECRET` (auto-generated),
  `TESLA_CLIENT_ID`/`SECRET`, `GOOGLE_CLIENT_ID`/`SECRET`, `DATABASE_URL`
  (default `postgres://localhost:5432/magus?sslmode=disable`), `PORT`, and
  `BASE_URL`. It never asks about `POSTGRES_USER`, `POSTGRES_PASSWORD`,
  `POSTGRES_DB`, or `BASE_DOMAIN` — the four values the Docker path needs most.
- `db-setup` (`Makefile` line 134, depends on `check-goose`): runs `psql`
  against `ADMIN_DATABASE_URL` to create the `magusadmindb` role and the
  `magus` database on a **host** Postgres, then applies migrations with the
  `goose` CLI. In Docker, the `db` container creates its own role and database
  itself, from `POSTGRES_USER`/`PASSWORD`/`DB`, the first time it starts with an
  empty volume — `make db-setup` never touches that container, and running it
  provisions a database the Docker stack never reads from.
- `docs/0-set-up/deployment.md` §8's own intro (unedited by this change) already
  says the Docker path needs none of §1's tools (`sqlc`, `goose`, a local
  Postgres) — this warning makes the same fact explicit for the two `make`
  targets specifically, since a reader can reach for a `make` target without
  thinking of it as one of "§1's tools."

**Placement:** before §8.5, inside §8, right after §8.4 ("Clone the repo") and
before the reader opens `.env` for the first time — the moment they would most
plausibly reach for either command.

**Rejected:** deleting the two commands from the doc's mental model entirely
(e.g. renaming them to make the host-only scope obvious in the target name
itself). Rejected as out of scope — this change documents an existing trap, it
does not redesign the `Makefile`'s target names. That is a larger change with
its own cost (every doc and script referencing the old names would need
updating) for a problem a doc warning already solves.

### D3 — Add the failure to `docs/1-deploy/docker.md`'s troubleshooting table

**Why:** `docs/1-deploy/docker.md` is, by its own opening line, "the doc you
come back to *after* you already deployed once" — exactly where a reader who
hit this failure, walked away, and came back later would look first, more
naturally than re-reading the first-deploy runbook end to end.

**The fix — one new row** in the existing troubleshooting table (§9), matching
its existing columns (Symptom / Command to run / Likely cause):

| Symptom | Command to run | Likely cause |
|---|---|---|
| `db` restarts forever; its logs say "Database is uninitialized and superuser password is not specified" | `docker compose --project-directory . -f deploy/docker/compose.yaml logs db` | `POSTGRES_PASSWORD` is empty in `.env`. Set it, then run `up -d --build` again. |

**Claim verified against the official `postgres` Docker image documentation:**
changing `POSTGRES_PASSWORD` on a database that already has data does **not**
change the existing superuser password. The image's own docs state: "the
Docker specific variables will only have an effect if you start the container
with a data directory that is empty; any pre-existing database will be left
untouched on container startup." So this row's fix (set the password, then
`up -d` again) only works **before** the `db` volume has ever initialized — if
the volume already has data from a previous, failed-then-fixed attempt, setting
the password in `.env` has no effect on it. The new row and its neighboring text
say this explicitly, so a reader with an already-initialized empty-password
volume does not retry the same fix in a loop.

**Verification method:** `WebFetch` against `https://hub.docker.com/_/postgres`,
quoting the docs' own warning text directly (see report). Not answered from
training-data memory, per this change's own instruction to verify rather than
assume.

**Rejected:** describing the fix only in prose, without the exact log line.
Rejected for the same reason as D1 — the exact, searchable text is what turns a
troubleshooting table from something you read start to finish into something
you can search.

## Database objects

**This change creates no table, column, index, constraint, view, or migration.**
It touches no `internal/<module>/db/` folder and no `query.sql` file, and no file
under `internal/` at all. The `database` design gate does not apply — nothing
here needs the owner's schema sign-off.

## Test contract

**No unit tests for this change.** It edits only two Markdown files
(`docs/0-set-up/deployment.md`, `docs/1-deploy/docker.md`) plus the OpenSpec
artifacts. There is no code path to test. The dispatch that produced this change
states this explicitly; this line records the same decision in the artifact
itself, rather than silently omitting a test-contract section.

## Reverse-direction check — existing `make` targets and guards

Verified against the actual `Makefile`, not assumed:

- **`env-setup`, `db-setup`.** Read in full (`Makefile` lines 134–260+) to
  confirm D2's claims about what each prompts for and connects to. Neither
  target is changed by this change — only the docs warning readers away from
  using them for the Docker path.
- **`docker-up` / `docker-down` / `docker-logs` / `docker-migrate` /
  `backup-db`.** Unaffected. This change touches no `Makefile` line at all.
- **`migration-guard` / `boundary-guard` / `archive-guard` / `ui-guard` /
  `i18n-guard` / `money-guard` / `tz-guard`.** Unaffected. None scans
  `docs/0-set-up/deployment.md` or `docs/1-deploy/docker.md`, and this change
  adds no Go file, no migration, and no file under
  `openspec/changes/archive/`.
- **`sqlc`.** Unaffected. No `query.sql` file and no migration in this change.
- **`.dockerignore` / `deploy/docker/Dockerfile.dockerignore`.** Unaffected.
  This change adds no new path that a Docker build context would need to
  exclude or include.

Finding: every `make` target and guard is unaffected — verified by reading the
`Makefile`, not assumed unaffected because "it's just docs."

## File list

| File | Change |
|---|---|
| `docs/0-set-up/deployment.md` | §8.5 gains a warning block naming the exact `db` container error (D1). §8 gains a warning, before §8.5, against using `make env-setup`/`make db-setup` for the Docker path (D2). |
| `docs/1-deploy/docker.md` | §9's troubleshooting table gains one row for the `db` restart loop, plus a note on the fix only applying before first initialization (D3). |
