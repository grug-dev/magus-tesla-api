> **Additive-then-destructive schema change + port extension** (tier 1 of roadmap
> `RM42-settings-theme-selector`). Adds `account.settings` (typed `language` + `theme` columns,
> PK `account_id`), migrates `accounts.language` into it, and drops the column — one migration,
> strict order (`design.md` D2). Adds `PreferencesFor`/`ThemeFor`/`SetTheme` +
> `ErrUnsupportedTheme` to `account.Service`; rewrites `LanguageFor`/`SetLanguage` against the new
> table; rewrites `UpsertFromOAuth` to create the settings row atomically (`design.md` D3). No
> gateway wiring — tier 2 is a separate, dependent OpenSpec change.
>
> **Dependencies / parallelism:**
> - T1 (goose migration) has no dependencies. Independent of all Go-only changes; MAY run in
>   parallel with T2.
> - T2 (domain type/constants/sentinel + port interface signatures in `account.go`) has no
>   dependencies. Independent of T1 (disjoint files) and MAY run in parallel with it.
> - T3 (sqlc query edits + regeneration) depends on T1 — the migration is sqlc's schema source, so
>   `account.settings` must exist there before `make sqlc` can produce fields for it. T3 is
>   independent of T2 (disjoint files: `query.sql` vs `account.go`) and MAY run in parallel with T2.
> - T4 (`service.go` implementation) depends on **both** T2 and T3 — it implements the interface
>   methods added in T2 using the sqlc-generated types produced in T3.
> - T5 (unit + integration tests) depends on T4.
> - T6 (Makefile/guard verification) depends on T1 (needs the final migration filename/timestamp
>   to check for collisions) — MAY run in parallel with T2/T3/T4/T5.
> - T7 (docs: `AGENTS.md` + KB grep) depends on T2 only (documents interface signatures, not the
>   implementation) — MAY run in parallel with T3/T4/T5/T6.
> - T8 (verification) depends on T1–T7.
>
> **Leader-integrated step:** run `make sqlc` after T3.1–T3.3 (the `query.sql` edits) to regenerate
> `accountdb`. The existing `sql:` entry in `sqlc.yaml` already covers the account module; no
> structural `sqlc.yaml` change is needed (confirmed in T6). Do not hand-edit `db/models.go` or
> `db/query.sql.go` — both are sqlc-generated.

## T1. Goose migration (`internal/account/db/migrations/`) — no dependencies

- [x] T1.1 Create `internal/account/db/migrations/20260904000001_add_account_settings.sql` with
      the exact DDL from `design.md` D2:

      ```sql
      -- +goose Up
      CREATE TABLE account.settings (
          account_id UUID PRIMARY KEY REFERENCES account.accounts(id) ON DELETE CASCADE,
          language   TEXT NOT NULL DEFAULT 'es',
          theme      TEXT NOT NULL DEFAULT 'graphite'
      );

      INSERT INTO account.settings (account_id, language)
      SELECT id, language FROM account.accounts;

      ALTER TABLE account.accounts DROP COLUMN language;

      -- +goose Down
      ALTER TABLE account.accounts ADD COLUMN language TEXT NOT NULL DEFAULT 'es';

      UPDATE account.accounts a
      SET language = s.language
      FROM account.settings s
      WHERE s.account_id = a.id;

      DROP TABLE account.settings;
      ```

      Include a header comment referencing `design.md` D1 (typed columns, no key/value table — an
      EAV shape loses `NOT NULL`, defaults, and schema discoverability), D2 (strict order: the
      table must exist before the backfill targets it, and the column must survive until the
      backfill has read it), and D5 (no `CHECK` — vocabulary validated in Go at the sole write
      path, mirroring the unconstrained `language`/`provider` precedent).

      Before creating the file, re-run the collision check from `design.md`'s Migration Plan step
      1 (`ls internal/*/db/migrations | grep -oE '^[0-9]{14}' | sort | uniq -d`, or inspect each
      module's migrations directory) in case a sibling tier landed a same-timestamped migration
      since this proposal was written. Bump the timestamp if a collision is found and note it in
      the final report.

      Acceptance: `goose status` shows the migration applied when `make migrate-up` runs; every
      pre-existing account has exactly one `account.settings` row with `language` equal to its
      prior `accounts.language` value and `theme = 'graphite'`; `account.accounts` no longer has a
      `language` column; `goose down` (one step) restores the column with current values and drops
      `account.settings` without error (Test Contract items 1 and 5).

      VERIFIED 2026-09-04 (review round 1, finding F1) against a disposable `postgres:16-alpine`
      container — never the dev database. Seeded three accounts with `es`/`en`/`fr`, applied the
      migration, and confirmed three `account.settings` rows carrying those exact languages with
      `theme = 'graphite'`, and `accounts.language` dropped (item 1). Then mutated one settings row
      `es` -> `en`, ran `goose down`, and confirmed `accounts.language` came back holding the
      CURRENT values (the mutated `en`, not the original `es`) with `account.settings` dropped
      (item 5). Re-ran `up` to confirm the cycle is repeatable. This closes the gap decision D6
      recorded for item 1 as well: the constraint was only that the *Go test harness* cannot seed
      pre-migration rows, not that the behavior is unverifiable.

## T2. Domain type + port interface (`internal/account/account.go`) — no dependencies, parallel-ok with T1

- [x] T2.1 Add the closed theme vocabulary + sentinel error to `account.go`, near the existing
      `LanguageES`/`LanguageEN`/`ErrUnsupportedLanguage` declarations, with the doc comments from
      `design.md` D6:
      ```go
      const (
          ThemeApex      = "apex"
          ThemeGraphite  = "graphite"
          ThemeHalloween = "halloween"
      )

      var ErrUnsupportedTheme = errors.New("account: unsupported theme code")
      ```
- [x] T2.2 Add the `Settings` domain type from `design.md` D7:
      ```go
      type Settings struct {
          Language string
          Theme    string
      }
      ```
- [x] T2.3 Add `PreferencesFor`, `ThemeFor`, and `SetTheme` to the `Service` interface, with the
      doc comments from `design.md` D6/D7. Update `LanguageFor`/`SetLanguage`'s existing doc
      comments to note they are now backed by `account.settings` rather than a column on
      `accounts` (external behavior/signature unchanged). Place the three new methods after
      `SetLanguage` (the current last method) in the interface.
      Acceptance: `go build ./...` fails at this point (no implementation yet) — expected; confirm
      the failure is exactly "does not implement Service" pointing at the three missing methods,
      no unrelated compile errors.

## T3. sqlc query edits + regeneration (`internal/account/db/query.sql`) — depends on T1, parallel-ok with T2

- [x] T3.1 Replace `GetAccountLanguage` with `GetAccountSettings` (design.md D7/D10):
      ```sql
      -- name: GetAccountSettings :one
      SELECT language, theme FROM account.settings
      WHERE account_id = @account_id
        AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');
      ```
- [x] T3.2 Repoint `UpdateAccountLanguage` at `account.settings` and add `UpdateAccountTheme`
      mirroring it (design.md D7/D10):
      ```sql
      -- name: UpdateAccountLanguage :exec
      UPDATE account.settings
      SET language = @language
      WHERE account_id = @account_id
        AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');

      -- name: UpdateAccountTheme :exec
      UPDATE account.settings
      SET theme = @theme
      WHERE account_id = @account_id
        AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');
      ```
- [x] T3.3 Add `InsertSettingsIfMissing` (design.md D3):
      ```sql
      -- name: InsertSettingsIfMissing :exec
      INSERT INTO account.settings (account_id)
      VALUES (@account_id)
      ON CONFLICT (account_id) DO NOTHING;
      ```
- [x] T3.4 Run `make sqlc` (or `sqlc generate`) to regenerate `internal/account/db/`. Confirm and
      report the Go type sqlc infers for `GetAccountSettingsRow.Language`/`.Theme` and for
      `UpdateAccountLanguageParams.Language` / `UpdateAccountThemeParams.Theme`. **Do not assume
      `string`** — `design.md` D7/Migration Plan states this is expected (both columns are `NOT
      NULL`), but verify by reading the generated `db/query.sql.go` rather than trusting the
      expectation, per `ai/go-conventions.md` §Persistence. If sqlc produces `pgtype.Text` instead,
      use the existing `textFromString`/`nullableTextToPtr` helpers to convert.
      No other module's generated code should change.

## T4. Service implementation (`internal/account/service.go`) — depends on T2, T3

- [x] T4.1 Rewrite `UpsertFromOAuth` to the transactional shape in `design.md` D3 (mirrors
      `AccessTokenFor`'s existing `pool.Begin`/`WithTx`/`Commit` pattern in this same file — do not
      invent a new atomicity idiom):
      ```go
      func (s *service) UpsertFromOAuth(ctx context.Context, id OAuthIdentity) (Account, error) {
          tx, err := s.pool.Begin(ctx)
          if err != nil {
              return Account{}, fmt.Errorf("beginning tx: %w", err)
          }
          defer tx.Rollback(ctx)

          qtx := s.q.WithTx(tx)

          row, err := qtx.UpsertAccountFromOAuth(ctx, accountdb.UpsertAccountFromOAuthParams{
              Email:       id.Email,
              Provider:    id.Provider,
              ProviderID:  id.ProviderID,
              DisplayName: textFromString(id.DisplayName),
          })
          if err != nil {
              return Account{}, fmt.Errorf("upserting account from oauth: %w", err)
          }

          if err := qtx.InsertSettingsIfMissing(ctx, row.ID); err != nil {
              return Account{}, fmt.Errorf("creating account settings: %w", err)
          }

          if err := tx.Commit(ctx); err != nil {
              return Account{}, fmt.Errorf("committing tx: %w", err)
          }
          return accountFromRow(row), nil
      }
      ```
- [x] T4.2 Implement `PreferencesFor`, and rewrite `LanguageFor`/`SetLanguage`, plus implement
      `ThemeFor`/`SetTheme`, per `design.md` D6/D7:
      ```go
      func (s *service) PreferencesFor(ctx context.Context, accountID uuid.UUID) (Settings, error) {
          row, err := s.q.GetAccountSettings(ctx, accountID)
          if err != nil {
              return Settings{}, fmt.Errorf("loading account settings: %w", err)
          }
          return Settings{
              Language: normalizeLanguage(row.Language),
              Theme:    normalizeTheme(row.Theme),
          }, nil
      }

      func (s *service) LanguageFor(ctx context.Context, accountID uuid.UUID) (string, error) {
          prefs, err := s.PreferencesFor(ctx, accountID)
          if err != nil {
              return "", err
          }
          return prefs.Language, nil
      }

      func (s *service) SetLanguage(ctx context.Context, accountID uuid.UUID, lang string) error {
          if !isSupportedLanguage(lang) {
              return ErrUnsupportedLanguage
          }
          if err := s.q.UpdateAccountLanguage(ctx, accountdb.UpdateAccountLanguageParams{
              AccountID: accountID,
              Language:  lang,
          }); err != nil {
              return fmt.Errorf("setting account language: %w", err)
          }
          return nil
      }

      func (s *service) ThemeFor(ctx context.Context, accountID uuid.UUID) (string, error) {
          prefs, err := s.PreferencesFor(ctx, accountID)
          if err != nil {
              return "", err
          }
          return prefs.Theme, nil
      }

      func (s *service) SetTheme(ctx context.Context, accountID uuid.UUID, theme string) error {
          if !isSupportedTheme(theme) {
              return ErrUnsupportedTheme
          }
          if err := s.q.UpdateAccountTheme(ctx, accountdb.UpdateAccountThemeParams{
              AccountID: accountID,
              Theme:     theme,
          }); err != nil {
              return fmt.Errorf("setting account theme: %w", err)
          }
          return nil
      }
      ```
      Add the pure helpers near `normalizeLanguage`/`isSupportedLanguage`:
      ```go
      func normalizeTheme(theme string) string {
          if isSupportedTheme(theme) {
              return theme
          }
          return ThemeGraphite
      }

      func isSupportedTheme(theme string) bool {
          return theme == ThemeApex || theme == ThemeGraphite || theme == ThemeHalloween
      }
      ```
      Adjust field names/types to whatever T3.4 actually confirmed for the sqlc-generated structs.
      Acceptance: `go build ./...` and `go vet ./...` pass; `pgtype` still does not appear in any
      public type signature.

## T5. Tests (`internal/account/service_test.go`, `service_integration_test.go`) — depends on T4

- [x] T5.1 Add a pure unit test (no DB) for `normalizeTheme`/`isSupportedTheme` in
      `service_test.go`, mirroring the existing `normalizeLanguage` test: both supported codes
      returned unchanged, an unrecognized value (`"cyberpunk"`) normalized to `"graphite"`, and the
      empty string normalized to `"graphite"`.
- [x] T5.2 Add a `DATABASE_URL`-gated integration test `TestAccountSettings_RoundTrip` in
      `service_integration_test.go` (self-skips when unset, mirroring the module's existing
      pattern), covering Test Contract items 2, 3, 4, and 6 from `design.md`:
      - Fresh-signup defaults: after `UpsertFromOAuth`, `PreferencesFor` returns
        `account.Settings{Language: account.LanguageES, Theme: account.ThemeGraphite}` with no
        explicit write.
      - `SetTheme` round-trip: `SetTheme(ctx, acct.ID, account.ThemeHalloween)` succeeds;
        `ThemeFor` returns `account.ThemeHalloween`.
      - `SetTheme` rejects an unsupported code: `SetTheme(ctx, acct.ID, "cyberpunk")` returns an
        error satisfying `errors.Is(err, account.ErrUnsupportedTheme)`; a subsequent `ThemeFor`
        shows the previously stored value is unchanged.
      - Normalizing an out-of-band value: `pool.Exec(ctx, "UPDATE account.settings SET theme =
        $1, language = $2 WHERE account_id = $3", "neon", "fr", acct.ID)`, then assert `ThemeFor`
        returns `account.ThemeGraphite`, `LanguageFor` returns `account.LanguageES`, and
        `PreferencesFor` returns both normalized together in one call.
      - Inactive-account gating (design.md D10, Test Contract item 6): deactivate the test account
        (`pool.Exec(ctx, "UPDATE account.accounts SET status = 'Inactive' WHERE id = $1",
        acct.ID)`), then assert `PreferencesFor`/`LanguageFor`/`ThemeFor` each return an error
        (the same not-found-shaped failure as an unknown account id), and `SetLanguage`/`SetTheme`
        each return no error but do not change the stored value (verify by reactivating the
        account and reading it back).
- [x] T5.3 Add or repair a `TestLanguagePreference_RoundTrip`-equivalent covering Test Contract
      item 1 (backfill) at the migration level: since this is exercised by the migration itself
      rather than by `Service`, cover it as part of `T5.2`'s fixture setup if the existing test
      harness seeds accounts before migrations run, or note in the final report if backfill
      correctness is verified only by manual/CI migration testing rather than a Go test (state
      which approach was taken and why).

## T6. Makefile / guard verification — depends on T1, parallel-ok with T2/T3/T4/T5

- [x] T6.1 Confirm `MIGRATIONS_DIRS` in the `Makefile` needs no change — `internal/account/db/
      migrations` is already listed, and T1's new file lives in that same directory.
- [x] T6.2 Confirm `db-setup`/`db-reset` role-and-ownership assumptions hold: read the `db-reset`
      target and confirm it drops and recreates the whole database (not per-table), so the new
      `account.settings` table needs no separate ownership handling — it is created by the same
      app role that already owns the `account` schema (established by
      `20260902000001_move_account_to_own_schema.sql`). Report what was read and concluded, not
      just "unaffected."
- [x] T6.3 Confirm `sqlc.yaml` needs no structural change — the existing `account` module `sql:`
      entry's `schema:` already points at `internal/account/db/migrations`, which now includes
      T1's file automatically.
- [x] T6.4 Run `make migration-guard` (or reproduce its collision check manually) after T1 lands,
      to confirm `20260904000001` (or whatever timestamp T1.1 actually used, if bumped for a
      collision) does not collide with any migration in `internal/telemetry`, `internal/charging`,
      or `internal/analytics`. Report the result.

## T7. Docs (`internal/account/AGENTS.md`, `kkpa/context/`) — depends on T2, parallel-ok with T3/T4/T5/T6

- [x] T7.1 Update `internal/account/AGENTS.md`'s "Public interface" section to list
      `PreferencesFor(ctx, accountID) (Settings, error)`, `ThemeFor`/`SetTheme`, and note that
      `LanguageFor`/`SetLanguage` are now backed by `account.settings` rather than a column on
      `accounts` — required by `CLAUDE.md`'s docs-track-change rule (a module's public surface
      changed in this change, not a follow-up).
- [x] T7.2 Grep `kkpa/context/` for `account` and `language` (`grep -rl "account\|language"
      kkpa/context/`) and read every match. Fix any guide whose consumer map, file map, or
      described behavior this change invalidates (e.g. a guide describing `accounts.language` as a
      column, or `LanguageFor` as reading `accounts` directly). Report which files were checked and
      which (if any) were edited, per `CLAUDE.md`'s KB rule.

## T8. Verification — depends on T1–T7

- [x] T8.1 `go build ./...` and `go vet ./...` pass repo-wide. If the widened `account.Service`
      interface breaks compilation of a fake/double in a sibling module's test file (the same class
      of issue prior tiers hit — see RM6/RM24's precedent), that is a leader-owned cross-module fix
      — flag it, do not edit outside `internal/account`.
- [x] T8.2 `gofmt -l` reports no diff for any file this tier touched.
- [x] T8.3 Boundary check: `internal/account` still does not import `internal/tesla` or
      `internal/gateway`; `pgtype` does not appear in any public type or interface signature; no
      file outside `internal/account` (other than a leader-owned cross-module fix per T8.1) was
      touched.
- [x] T8.4 Report the exact test-suite commands the owner must run (`go test
      ./internal/account/... -run TestAccountSettings_RoundTrip` and the full `go test ./...` /
      `make test-with-db`) — this tier writes tests but does not execute them
      (`Test-Execution-Policy`); the owner's run is what turns T5 from `awaiting-user-verification`
      into `done`.
- [x] T8.5 `openspec validate RM42-account-add-settings-table --strict` passes and every tasks.md
      checkbox above reflects real completion.
