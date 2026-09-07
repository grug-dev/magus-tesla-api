## ADDED Requirements

### Requirement: Deploy Runbook Names The Exact Superuser-Password Failure
The Docker Compose deploy runbook (`docs/0-set-up/deployment.md` §8.5) SHALL show
the exact error text the `db` service logs when `POSTGRES_PASSWORD` is empty, as
a visible warning next to the step where a reader fills in `.env`, not only as a
table cell.

#### Scenario: A reader who already hit the restart loop can find the fix by searching
- **GIVEN** a reader whose `db` container is stuck restarting because
  `POSTGRES_PASSWORD` is empty
- **WHEN** that reader searches the error text from their own `db` container logs
- **THEN** the search matches text in `docs/0-set-up/deployment.md` §8.5
- **AND** that text is next to the fix (set a non-empty `POSTGRES_PASSWORD`)

#### Scenario: A reader filling in `.env` for the first time sees the warning before moving on
- **GIVEN** a reader following §8.5 of the deploy runbook for the first time
- **WHEN** that reader reaches the `.env` variable table
- **THEN** a warning naming the `POSTGRES_PASSWORD` failure is visible next to the
  table, not only inside one of its cells

### Requirement: Deploy Runbook Warns Against The Host Setup Path For A Docker Deploy
The Docker Compose deploy runbook (`docs/0-set-up/deployment.md` §8) SHALL warn a
reader, before the `.env`-creation step, that `make env-setup` and `make db-setup`
belong to the host development path and must not be used for a Docker deploy, and
SHALL state the reason each one is wrong for that path.

#### Scenario: A reader reaches the warning before reaching for either command
- **GIVEN** a reader following the deploy runbook's §8 in order, top to bottom
- **WHEN** that reader reaches the point where `.env` needs values
- **THEN** a warning against using `make env-setup` or `make db-setup` for this
  deploy has already appeared earlier in §8
- **AND** the warning states why each command is wrong for the Docker path

### Requirement: Docker Troubleshooting Reference Covers The Uninitialized-Superuser-Password Failure
The Docker command reference (`docs/1-deploy/docker.md`) SHALL include the
`db`-restarts-forever failure caused by an empty `POSTGRES_PASSWORD` in its
troubleshooting table, following the table's existing Symptom / Command / Likely
cause format, and SHALL state that changing `POSTGRES_PASSWORD` after the
database volume already holds data does not change the existing password.

#### Scenario: A reader who deployed once, then hits this failure later, finds it in the reference doc
- **GIVEN** a reader who has already completed a first deploy and is now using
  `docs/1-deploy/docker.md` as a reference
- **WHEN** that reader's `db` service is stuck restarting
- **THEN** the troubleshooting table names this exact symptom, the command to
  view `db`'s logs, and the fix

#### Scenario: A reader with an already-initialized volume is warned the password fix will not apply
- **GIVEN** a reader whose `db` service's data volume was already initialized
  with an empty superuser password
- **WHEN** that reader reads the troubleshooting row for this failure
- **THEN** the row or its surrounding text states that setting `POSTGRES_PASSWORD`
  in `.env` at this point does not change the already-initialized password
