> **Additive schema change + read port extension** (tier 1 of roadmap
> `RM49-analysis-start-date`). Adds `account.settings.analysis_start_date DATE NOT NULL`,
> backfilled in `America/Bogota` from `account.accounts.created_at` — one migration, strict
> order (`design.md` D2). Adds `AnalysisStartDateFor` to `account.Service`; extends
> `PreferencesFor`/`Settings`; changes `InsertSettingsIfMissing`'s signature and its one call
> site (`design.md` D4/D5). No write port for the value (roadmap D7). No gateway wiring — tier
> 2 is a separate, dependent OpenSpec change. **No unit tests in this tier** (roadmap D10) —
> every task below is code, sqlc regen, docs, or verification; `design.md`'s Test Contract
> authors the expected values for the owner to check by hand.
>
> **Dependencies / parallelism:**
> - T1 (goose migration) has no dependencies. Independent of all Go-only changes; MAY run in
>   parallel with T2.
> - T2 (domain type/port interface signature in `account.go`) has no dependencies. Independent
>   of T1 (disjoint files) and MAY run in parallel with it.
> - T3 (sqlc query edits + regeneration) depends on T1 — the migration is sqlc's schema source,
>   so `analysis_start_date` must exist there before `make sqlc` can produce a field for it. T3
>   is independent of T2 (disjoint files: `query.sql` vs `account.go`) and MAY run in parallel
>   with T2.
> - T4 (`service.go` implementation) depends on **both** T2 and T3 — it implements the
>   interface method added in T2 using the sqlc-generated types produced in T3, and rewrites
>   `UpsertFromOAuth`'s call site.
> - T5 (docs: `AGENTS.md`, KB grep, `deployment.md` deploy + rollback) depends on T1 (needs the
>   final migration filename for the deploy docs) and T2 (documents the interface signature).
>   MAY run in parallel with T3/T4.
> - T6 (Makefile/guard verification) depends on T1 (needs the final migration filename/timestamp
>   to check for collisions) — MAY run in parallel with T2/T3/T4/T5.
> - T7 (verification) depends on T1–T6.
>
> **Leader-integrated step:** run `make sqlc` after T3.1–T3.2 (the `query.sql` edits) to
> regenerate `accountdb`. The existing `sql:` entry in `sqlc.yaml` already covers the account
> module; no structural `sqlc.yaml` change is needed (confirmed in T6). Do not hand-edit
> `db/models.go` or `db/query.sql.go` — both are sqlc-generated.

## T1. Goose migration (`internal/account/db/migrations/`) — no dependencies

- [x] T1.1 Create
      `internal/account/db/migrations/20260908000001_settings_add_analysis_start_date.sql` with
      the exact DDL from `design.md` D2:

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

      Include a header comment referencing `design.md` D1 (why `account.settings`, not
      `accounts` or `vehicles`), D2 (why the three-step order and no `DEFAULT`), and the
      `AT TIME ZONE 'America/Bogota'` cast's purpose (`design.md` D2's "load-bearing, not
      decorative" note and Test Contract items 1–3).

      Before creating the file, re-run the collision check from `design.md`'s Migration Plan
      step 1 (`ls internal/*/db/migrations/*.sql | xargs -n1 basename | grep -oE
      '^[0-9]{14}' | sort | uniq -d`) in case a sibling change landed a same-timestamped
      migration since this proposal was written. Bump the timestamp if a collision is found and
      note it in the final report.

      Acceptance: `goose status` shows the migration applied when `make migrate-up` (or `make
      migrate-run`) runs; every pre-existing account has `analysis_start_date` equal to its
      `created_at`'s calendar day in `America/Bogota` (design.md Test Contract items 1–3);
      `goose down` (one step) drops the column without error. Verify against a disposable
      Postgres — never the dev database with real data — per this project's existing migration
      verification pattern (see the `RM42` precedent's T1.1 verification note).

## T2. Domain type + port interface (`internal/account/account.go`) — no dependencies, parallel-ok with T1

- [x] T2.1 Add `AnalysisStartDate time.Time` to the `Settings` struct, with the doc comment
      from `design.md` D4.
- [x] T2.2 Add `AnalysisStartDateFor` to the `Service` interface, placed after `SetTheme` (the
      current last method), with the doc comment from `design.md` D4:
      ```go
      AnalysisStartDateFor(ctx context.Context, accountID uuid.UUID) (time.Time, error)
      ```
      Acceptance: `go build ./...` fails at this point (no implementation yet) — expected;
      confirm the failure is exactly "does not implement Service" pointing at the missing
      method, no unrelated compile errors.

## T3. sqlc query edits + regeneration (`internal/account/db/query.sql`) — depends on T1, parallel-ok with T2

- [x] T3.1 Widen `GetAccountSettings` to return the new column (design.md D4/D7 gating
      unchanged):
      ```sql
      -- name: GetAccountSettings :one
      SELECT language, theme, analysis_start_date FROM account.settings
      WHERE account_id = @account_id
        AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');
      ```
- [x] T3.2 Change `InsertSettingsIfMissing` to take the date as a parameter (design.md D4):
      ```sql
      -- name: InsertSettingsIfMissing :exec
      INSERT INTO account.settings (account_id, analysis_start_date)
      VALUES (@account_id, @analysis_start_date)
      ON CONFLICT (account_id) DO NOTHING;
      ```
- [x] T3.3 Run `make sqlc` (or `sqlc generate`) to regenerate `internal/account/db/`. Confirm
      and report the Go type sqlc infers for `Settings.AnalysisStartDate`,
      `GetAccountSettingsRow.AnalysisStartDate`, and
      `InsertSettingsIfMissingParams.AnalysisStartDate`. **Do not assume `pgtype.Date`** —
      `design.md` D6 states this is expected (mirroring `charging.manual_charge_entries.
      charged_on`), but verify by reading the generated `db/models.go`/`db/query.sql.go` rather
      than trusting the expectation, per `ai/go-conventions.md` §Persistence. No other module's
      generated code should change.

## T4. Service implementation (`internal/account/service.go`) — depends on T2, T3

- [x] T4.1 Add `internal/clock` to this file's imports (new dependency for this module —
      `design.md` D5).
- [x] T4.2 Add a `dateFromTime` helper, mirroring `internal/charging/service.go`'s existing one
      exactly:
      ```go
      func dateFromTime(t time.Time) pgtype.Date {
          return pgtype.Date{Time: t, Valid: true}
      }
      ```
      (Adjust the return type to whatever T3.3 actually confirmed, if it differs from
      `pgtype.Date`.)
- [x] T4.3 Rewrite `UpsertFromOAuth`'s call to `InsertSettingsIfMissing` per `design.md` D5:
      ```go
      today := clock.CalendarDay(clock.Now(), clock.Zone())
      if err := qtx.InsertSettingsIfMissing(ctx, accountdb.InsertSettingsIfMissingParams{
          AccountID:         row.ID,
          AnalysisStartDate: dateFromTime(today),
      }); err != nil {
          return Account{}, fmt.Errorf("creating account settings: %w", err)
      }
      ```
- [x] T4.4 Extend `PreferencesFor`'s mapping to include the new field, and implement
      `AnalysisStartDateFor` on top of it, mirroring `LanguageFor`/`ThemeFor` exactly:
      ```go
      func (s *service) PreferencesFor(ctx context.Context, accountID uuid.UUID) (Settings, error) {
          row, err := s.q.GetAccountSettings(ctx, accountID)
          if err != nil {
              return Settings{}, fmt.Errorf("loading account settings: %w", err)
          }
          return Settings{
              Language:          normalizeLanguage(row.Language),
              Theme:             normalizeTheme(row.Theme),
              AnalysisStartDate: row.AnalysisStartDate.Time,
          }, nil
      }

      func (s *service) AnalysisStartDateFor(ctx context.Context, accountID uuid.UUID) (time.Time, error) {
          prefs, err := s.PreferencesFor(ctx, accountID)
          if err != nil {
              return time.Time{}, err
          }
          return prefs.AnalysisStartDate, nil
      }
      ```
      Adjust field access to whatever T3.3 actually confirmed for the sqlc-generated struct.
      Acceptance: `go build ./...` and `go vet ./...` pass; `pgtype` still does not appear in
      any public type signature (only inside `service.go`'s private mapping helpers, matching
      the existing pattern for `language`/`theme`/`AccessExpiresAt`).

## T5. Docs (`internal/account/AGENTS.md`, `kkpa/context/`, `docs/0-set-up/deployment.md`) — depends on T1, T2, parallel-ok with T3/T4/T6

- [x] T5.1 Update `internal/account/AGENTS.md`'s "Public interface" section to list
      `AnalysisStartDateFor(ctx, accountID) (time.Time, error)`, and note that `Settings`/
      `PreferencesFor` now also carry `AnalysisStartDate` — required by `CLAUDE.md`'s
      docs-track-change rule (a module's public surface changed in this change, not a
      follow-up).
- [x] T5.2 Grep `kkpa/context/` for `account.settings`, `PreferencesFor`, and the account port
      (`grep -rl "account\.settings\|PreferencesFor\|account\.Service" kkpa/context/`) and read
      every match. Fix any guide whose consumer map, file map, or described behavior this
      change invalidates. Report which files were checked and which (if any) were edited, per
      `CLAUDE.md`'s KB rule.
- [x] T5.3 Add the verified deploy + rollback steps for this migration to
      `docs/0-set-up/deployment.md`, near the existing §8.8 "First deploy" / §8.11 "Deploying an
      update" sections — the exact commands from `design.md` D8 (deploy, no extra step) and D9
      (the verified manual `psql` + `goose_db_version` rollback procedure, NOT the roadmap's
      original "run goose down inside the running stack" wording, which `design.md` D9 found
      does not correspond to any actual tool in the deployed containers). State plainly, in the
      doc, why a plain `goose down` does not work here (no `goose` CLI in any container image;
      `cmd/migrate` only implements `Up`), so a future reader does not try it and get confused.

## T6. Makefile / guard verification — depends on T1, parallel-ok with T2/T3/T4/T5

- [x] T6.1 Confirm `MIGRATIONS_DIRS` in the `Makefile` needs no change — `internal/account/db/
      migrations` is already listed, and T1's new file lives in that same directory.
- [x] T6.2 Confirm `db-setup`/`db-reset` role-and-ownership assumptions hold: read the
      `db-reset`/`db-setup` targets and confirm they operate at the whole-database level (drop
      + recreate `OWNER $ROLE`), so the new column needs no separate ownership handling — it is
      added to a table the app role already owns since `RM39`'s schema move. Report what was
      read and concluded, not just "unaffected" (`design.md`'s "Reverse-Direction Check").
- [x] T6.3 Confirm `sqlc.yaml` needs no structural change — the existing `account` module
      `sql:` entry's `schema:` already points at `internal/account/db/migrations`, which now
      includes T1's file automatically.
- [x] T6.4 Run `make migration-guard` (or reproduce its collision check manually) after T1
      lands, to confirm `20260908000001` (or whatever timestamp T1.1 actually used, if bumped
      for a collision) does not collide with any migration in `internal/telemetry`,
      `internal/charging`, or `internal/analytics`. Report the result.
- [x] T6.5 Run `make tz-guard` after T4 lands. Confirm it passes: the migration's `AT TIME ZONE
      'America/Bogota'` literal is a `.sql` file and outside the guard's scan (`internal
      --include='*.go'`); the Go-side date computation (`clock.CalendarDay(clock.Now(),
      clock.Zone())`) calls only functions defined inside `internal/clock`, so no raw
      `time.Now()` or hardcoded zone string appears in `internal/account/service.go`. Report
      the result (`design.md`'s "Reverse-Direction Check").

## T7. Verification — depends on T1–T6

- [x] T7.1 `go build ./...` and `go vet ./...` pass repo-wide. If the widened `account.Service`
      interface breaks compilation of a fake/double in a sibling module's test file, that is a
      leader-owned cross-module fix — flag it, do not edit outside `internal/account`.
- [x] T7.2 `gofmt -l` reports no diff for any file this tier touched.
- [x] T7.3 Boundary check: `internal/account` still does not import `internal/tesla` or
      `internal/gateway`; the new `internal/clock` import creates no cycle (`internal/clock`
      imports only stdlib); `pgtype` does not appear in any public type or interface signature;
      no file outside `internal/account` (other than a leader-owned cross-module fix per T7.1),
      `openspec/changes/RM49-account-add-analysis-start-date/`, `docs/0-set-up/deployment.md`,
      or `kkpa/context/` was touched.
- [x] T7.4 Report the exact test-suite commands the owner may run to exercise this change by
      hand if they choose (`go test ./internal/account/...` and the full `go test ./...` /
      `make test-with-db`) — this tier adds no new `_test.go` file (roadmap D10), so there is no
      new test to point at; existing tests must still compile and the owner's run is what
      confirms nothing broke.
- [x] T7.5 `openspec validate RM49-account-add-analysis-start-date --strict` passes and every
      `tasks.md` checkbox above reflects real completion.
