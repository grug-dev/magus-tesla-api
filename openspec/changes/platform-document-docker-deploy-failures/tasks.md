# Tasks — platform-document-docker-deploy-failures

> **Dependencies / parallelism.**
> - **T1** (`docs/0-set-up/deployment.md` §8.5 warning) and **T2**
>   (`docs/0-set-up/deployment.md` §8 warning against `env-setup`/`db-setup`) edit
>   the same file but disjoint sections (§8.5 vs. the space between §8.4 and §8.5).
>   No content dependency between them — safe to do in one pass, in either order.
> - **T3** (`docs/1-deploy/docker.md` troubleshooting row) touches a different
>   file entirely. No dependency on T1/T2. MAY run in parallel.
> - **T4** (verification) depends on T1, T2, T3.
>
> **Leader-integrated step:** none. This change adds no database object, no Go
> file, and no codegen input (design.md → "Database objects" and
> "Reverse-direction check"). **No unit tests** — this is a documentation-only
> change; design.md's "Test contract" section states this explicitly instead of
> fabricating one.

## T1. `docs/0-set-up/deployment.md` §8.5 — visible `POSTGRES_PASSWORD` warning

- [x] T1.1 Add a warning block right after the `.env` variable table in §8.5,
      quoting the real `db` container error text (design.md D1):
      ```
      Error: Database is uninitialized and superuser password is not specified.
             You must specify POSTGRES_PASSWORD to a non-empty value for the
             superuser.
      ```
      State the fix: set a real, non-empty `POSTGRES_PASSWORD` before continuing.
      Acceptance: the exact error text appears in §8.5, next to the fix, not only
      inside the existing table cell.

## T2. `docs/0-set-up/deployment.md` §8 — warn against `make env-setup`/`make db-setup`

- [x] T2.1 Add a warning block between §8.4 ("Clone the repo") and §8.5 ("Create
      `.env`") stating: do not use `make env-setup` or `make db-setup` for this
      Docker deploy. State the reason for each (design.md D2): `env-setup`
      defaults `DATABASE_URL` to `localhost` and never asks for
      `POSTGRES_USER`/`PASSWORD`/`DB`/`BASE_DOMAIN`; `db-setup` provisions a host
      Postgres role/database the `db` container never uses — the container
      creates its own on first boot.
      Acceptance: the warning appears before §8.5 in reading order; it names both
      commands and gives a reason for each, not just a bare "don't use these."

## T3. `docs/1-deploy/docker.md` — troubleshooting-table row

- [x] T3.1 Add one row to §9's troubleshooting table (Symptom / Command to run /
      Likely cause columns, matching the existing rows), for the `db`
      restart-loop caused by an empty `POSTGRES_PASSWORD` (design.md D3):
      Symptom names the exact log text; Command is
      `docker compose --project-directory . -f deploy/docker/compose.yaml logs db`;
      Likely cause states the fix (set `POSTGRES_PASSWORD`, then `up -d --build`
      again).
      Acceptance: the row exists, uses the table's existing column format, and
      the Symptom column contains the exact error text.
- [x] T3.2 Next to the new row (or in a short note immediately below the table),
      state that changing `POSTGRES_PASSWORD` after the `db` volume already has
      data does NOT change the existing password — it only applies on the first
      boot of an empty volume. Only include this if verified against the
      official `postgres` image's documented behavior (design.md D3); if it
      cannot be verified, leave it out and say so in the final report.
      Acceptance: the note is present and matches the verified wording, or is
      absent with the report explaining why.

## T4. Verification — depends on T1, T2, T3

- [x] T4.1 Run `openspec validate --strict platform-document-docker-deploy-failures`
      and fix anything it reports.
      Acceptance: the command reports no errors.
- [x] T4.2 Re-read both edited files end to end to confirm no other section
      references the old, less-visible wording this change replaces, and that
      the B2 writing rules (short sentences, common words, exact command/error
      text preserved verbatim) are followed throughout the new text.
      Acceptance: both files read cleanly; no leftover reference to the
      superseded table-cell-only wording.
- [x] T4.3 Hand back to the owner: no command needs running for a docs-only
      change. If the owner wants to confirm the deploy now succeeds, the command
      is `docker compose --project-directory . -f deploy/docker/compose.yaml up
      -d --build`, from the repo root, after setting `POSTGRES_PASSWORD` — the
      pipeline does not run this (Test-Execution-Policy excludes
      `docker compose up`).
      Acceptance: none — this task's output is the command for the owner.
