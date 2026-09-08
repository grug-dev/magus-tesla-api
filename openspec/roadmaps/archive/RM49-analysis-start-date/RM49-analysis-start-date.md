# RM49 — Analysis start date

Source ticket: MAG-55 — https://linear.app/magus-monitor/issue/MAG-55/external-charges

Give every account one stored date: the first day the app analyzes vehicle data.
Today that date is implicit — it is `account.accounts.created_at`, the moment the
user signed in for the first time. The poller never fetches data before it.

This roadmap makes the date explicit, on `account.settings`, and uses it to reject
external charges dated before it. A charge from before that day cannot be part of
our analytics model, so saving one only adds noise.

## Tier table

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM49-account-add-analysis-start-date` | `internal/account` | One migration adding `account.settings.analysis_start_date DATE NOT NULL`, backfilled from `account.accounts.created_at` in `America/Bogota`. `InsertSettingsIfMissing` takes the date as a parameter, supplied in Go from `internal/clock`. `account.Settings` gains `AnalysisStartDate`; `GetAccountSettings` and `PreferencesFor` return it. New port method `AnalysisStartDateFor`. sqlc regen. Deploy steps documented. | — | Create the OpenSpec artifacts for `RM49-account-add-analysis-start-date`. Binding decisions D1–D5 and D9 in this roadmap. design.md MUST carry the full schema, the rationale (including why `account.accounts`, `account.vehicles`, and "no new column at all" were each rejected), and the index plan against the read patterns — the `database` design gate applies. Migration order is fixed by D2: ADD COLUMN nullable → UPDATE backfill → SET NOT NULL, with a Down that drops the column. Migration, sqlc regen and the Go port change ship in the SAME tier. Verify `MIGRATIONS_DIRS`, `db-setup`/`db-reset` ownership assumptions, `sqlc`, `make migration-guard` and `make tz-guard` still hold, and record what you found. D9 requires the VPS deploy steps to be written into `docs/0-set-up/deployment.md`. |
| `[x]` | `RM49-gateway-restrict-external-charge-date` | `internal/gateway` | The `/external-charges` page reads `AnalysisStartDateFor` through the `account` port. Saving an entry whose `charged_on` is before that date is rejected server-side, reusing the existing `charged_on`-keyed validation-error map. The form shows a red label with the reason, and sets the date input's `min` attribute. ES + EN catalogue entries. | tier 1 | Create the OpenSpec artifacts for `RM49-gateway-restrict-external-charge-date`. Binding decisions D5–D8 in this roadmap. Reuse the existing validation-error map in `handlers/external_charges.go` keyed by column name (`charged_on`) — do NOT invent a second error mechanism. Read `internal/account` only through its public Go interface; never import `accountdb`. Every new user-facing string resolves through `i18n.T` with both ES and EN non-empty. `internal/charging` is NOT touched in this tier — that is D5, and it is deliberate. |

## Decisions (binding on every tier)

Settled with the user on 2026-09-08, before any artifact was written. Workers treat
these as given and never re-open them.

- **D1 — The date lives on `account.settings`, as `analysis_start_date DATE NOT NULL`.**

  ```sql
  ALTER TABLE account.settings ADD COLUMN analysis_start_date DATE NOT NULL;
  ```

  `account.settings` is already the one-row-per-account typed table, keyed by
  `account_id` as its primary key. Two alternatives were considered and rejected:

  - **A column on `account.accounts`** — rejected. `accounts` holds identity
    (email, provider, display name). This value is policy, and RM42 D3 already
    settled that per-account policy has exactly ONE home, `account.settings`.
    Splitting it again reopens the question "which table do I use?" for every
    future setting.
  - **A column on `account.vehicles`** — rejected *for now*. Per-vehicle is more
    correct the day a user adds a second Tesla: that car's analysis should start
    when it was registered, not when the account was created. But the platform is
    one-car-per-account in practice today, and per-vehicle would force every
    caller to pass `teslaID`. Revisit when real multi-vehicle support lands.
  - **No new column, read `accounts.created_at` directly** — rejected. It needs no
    migration, but the date could then never be changed. A separate column is what
    makes D7's follow-up possible.

- **D2 — Migration order: ADD nullable → UPDATE backfill → SET NOT NULL.** The
  column cannot be added as `NOT NULL` in one step, because existing rows have no
  value yet.

  ```sql
  -- +goose Up
  ALTER TABLE account.settings ADD COLUMN analysis_start_date DATE;

  UPDATE account.settings s
  SET analysis_start_date = (a.created_at AT TIME ZONE 'America/Bogota')::date
  FROM account.accounts a
  WHERE a.id = s.account_id;

  ALTER TABLE account.settings ALTER COLUMN analysis_start_date SET NOT NULL;

  -- +goose Down
  ALTER TABLE account.settings DROP COLUMN analysis_start_date;
  ```

  **No `DEFAULT` on the column, in either direction.** A `DEFAULT CURRENT_DATE`
  would resolve in the database session's time zone, which breaks the project's
  `America/Bogota` rule and `make tz-guard`. The backfill states the zone
  explicitly for the same reason: `created_at` is a `TIMESTAMPTZ`, and casting it
  to `DATE` without `AT TIME ZONE` silently uses the session zone.

- **D3 — No new index.** `account_id` is already the primary key of
  `account.settings`. Every read of this column is a primary-key lookup that the
  module already performs. `GetAccountSettings` gains one more column in its
  SELECT list, which costs nothing. This satisfies the read-heavy performance
  profile without any new object.

- **D4 — The port grows two ways, both mirroring what already exists.**
  `account.Settings` gains `AnalysisStartDate time.Time`, returned by
  `PreferencesFor` from the same single query — no extra round-trip. A dedicated
  `AnalysisStartDateFor(ctx, accountID) (time.Time, error)` is added for callers
  that need only the date, exactly mirroring `LanguageFor` and `ThemeFor`.
  `InsertSettingsIfMissing` takes the date as a parameter; the Go caller supplies
  today's date from `internal/clock`, never `CURRENT_DATE`.

- **D5 — The rule is enforced in `internal/gateway` only. `internal/charging` is
  not touched.** The user chose this deliberately, over putting the rule in
  `charging.Writer`. Recorded consequence: any future caller of `charging.Writer`
  that is not the gateway — a poller, an import script, a JSON API — can still
  save an entry dated before the limit. That is accepted for now. The trade was
  one fewer module, one fewer port, and one fewer wave.

- **D6 — The rule compares `Entry.ChargedOn` only.** `ChargedOn` is required on
  every entry, so the check always has a value. `StartedAt` is optional and is not
  used by analytics, so it is not checked. A user could set `StartedAt` before the
  limit and it would be stored — accepted, and stated here so nobody treats it as
  a bug later.

- **D7 — The date is read-only. There is no way to change it in the UI.** It is
  set once, at signup, from the account's creation date. No settings form field
  and no write port method in this roadmap. Making it editable is real work
  (form field, write port, its own validation) and is recorded in
  `openspec/roadmaps/backlog.md` as a separate item.

- **D8 — The UI reuses the validation mechanism that already exists.**
  `handlers/external_charges.go` keeps a validation-error map keyed by column
  name, and `charging.Field` values are those same column names. The new error is
  keyed `charged_on` and renders through the existing red-label path. The date
  input also gets a `min` attribute, so the browser blocks most bad input before a
  request is sent. The server check stays regardless — the `min` attribute is a
  convenience, never the rule. Both new strings go in
  `internal/gateway/i18n/catalog.go` with ES and EN non-empty.

- **D9 — Deploying this to the VPS needs no extra command.** The compose stack
  already runs a `migrate` service that applies every pending migration and exits
  before `web` and `poller` start (`docs/0-set-up/deployment.md` §8.8). So the
  normal deploy applies it:

  ```bash
  # On the VPS, from the repo root.
  git pull
  docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build

  # Check that migrate finished cleanly: it must show "Exited (0)".
  docker compose --project-directory . -f deploy/docker/compose.yaml ps
  ```

  **Rollback — corrected after tier 1 verified it.** This roadmap first said "run
  goose down inside the running stack". Tier 1 checked and that command does not
  exist: `deploy/docker/Dockerfile` never installs the `goose` CLI, and
  `cmd/migrate/main.go` only calls `provider.Up(ctx)` — there is no down path in
  the deployed images at all. The real rollback is manual SQL inside the `db`
  container: run the migration's own Down statement, then
  `DELETE FROM goose_db_version WHERE version_id = 20260908000001` so a later
  deploy re-applies it instead of skipping it. Tier 1's design.md D9 carries the
  verified commands, and `docs/0-set-up/deployment.md` documents them.

  The wider gap — no migration-down tooling in the deploy path, for any migration —
  is recorded as backlog item 28. It is out of scope here.

- **D10 — No unit tests in this roadmap.** The user chose this. Tasks cover code
  and docs only. `go build`, `go vet` and the guards still run.

## Future work

Making `analysis_start_date` editable from the settings page is deferred by D7 and
recorded in `openspec/roadmaps/backlog.md`. See that file for the trigger.
