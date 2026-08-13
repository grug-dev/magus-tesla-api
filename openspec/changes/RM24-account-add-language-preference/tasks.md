> **Additive, non-breaking column + port-method change** (tier 1 of roadmap
> `RM24-i18n-translations`). Adds `language TEXT NOT NULL DEFAULT 'es'` to `accounts` via a goose
> migration, adds two new port methods (`LanguageFor`, `SetLanguage`) backed by two new sqlc
> queries (`GetAccountLanguage`, `UpdateAccountLanguage`), and updates `internal/account/AGENTS.md`.
> sqlc regenerated (`package accountdb`). No Tesla API calls, no gateway wiring (tier 2, owned by
> `internal/gateway`, is a separate, dependent OpenSpec change that consumes the port methods
> added here).
>
> **Dependencies / parallelism:**
>
> - T1 (goose migration) has no dependencies. It is independent of all Go changes and MAY run in
>   parallel with T2.
> - T2 (domain constants/sentinel + port interface signatures in `account.go`) has no dependencies.
>   It is independent of T1 (disjoint files) and MAY run in parallel with it.
> - T3 (sqlc query edits + regeneration) depends on T1 (the migration is sqlc's schema source —
>   `sqlc generate` reads the goose migration files directly, so the column must exist there before
>   `make sqlc` can produce the new fields). T3 is independent of T2 (disjoint files: `query.sql`
>   vs `account.go`) and MAY run in parallel with T2.
> - T4 (`service.go` implementation + helpers) depends on **both** T2 and T3 — it implements the
>   interface methods added in T2 using the sqlc-generated types produced in T3.
> - T5 (unit + integration tests) depends on T4.
> - T6 (`AGENTS.md` docs update) depends on T2 only (it documents the interface signatures, not the
>   implementation) — MAY run in parallel with T3/T4/T5.
> - T7 (verification) depends on T1–T6.
>
> **Leader-integrated step:** run `make sqlc` after T3.1–T3.2 (the `query.sql` edits) to regenerate
> `accountdb`. The existing `sql:` entry in `sqlc.yaml` already covers the account module; no
> structural `sqlc.yaml` change is needed. Do not hand-edit `db/models.go` or `db/query.sql.go` —
> both are sqlc-generated.

---

## T1. Goose migration (`internal/account/db/migrations/`) — no dependencies

- [x] T1.1 Create `internal/account/db/migrations/<timestamp>_accounts_add_language.sql` (use the
      next chronological timestamp after `20260803000001_vehicles_add_config_fields.sql`, following
      the module's existing `<YYYYMMDDHHMMSS>_<name>.sql` convention) with the exact DDL from
      `design.md` D1 (reproduced here for implementer convenience):

      ```sql
      -- +goose Up
      ALTER TABLE accounts
          ADD COLUMN language TEXT NOT NULL DEFAULT 'es';

      -- +goose Down
      ALTER TABLE accounts
          DROP COLUMN IF EXISTS language;
      ```

      Include a header comment referencing `design.md` D1 (`NOT NULL DEFAULT 'es'` — unlike the
      nullable `access_type`/`vehicle_config` precedents, there is no "not yet captured" state for
      a preference that has a true default from row creation; no `CHECK` — vocabulary validated in
      Go at the sole write path, mirroring the unconstrained `provider` column) and D2 (no index —
      every read/write locates its row by primary key `id` or the existing
      `(provider, provider_id)` unique constraint; `language` is never a predicate).
      Acceptance: `goose status` shows the migration as applied when `make migrate-up` is run; no
      existing rows fail (the `DEFAULT` backfills every row); `goose down` (one step) removes the
      column without error.

## T2. Domain constants + port interface (`internal/account/account.go`) — no dependencies, parallel-ok with T1

- [x] T2.1 Add the closed language-code vocabulary near the top of `account.go` (after the existing
      `ErrNoTeslaConnection` declaration is a reasonable place):
      ```go
      // LanguageES and LanguageEN are the two supported language codes — the entire
      // closed vocabulary this module accepts (roadmap RM24 decision D4). Consumers
      // should reference these constants rather than the string literals.
      const (
          LanguageES = "es"
          LanguageEN = "en"
      )

      // ErrUnsupportedLanguage is returned by SetLanguage when lang is not one of the
      // two supported codes. Detect it with errors.Is.
      var ErrUnsupportedLanguage = errors.New("account: unsupported language code")
      ```
- [x] T2.2 Add `LanguageFor` and `SetLanguage` to the `Service` interface, with the doc comments
      from `design.md` D3:
      ```go
      // LanguageFor returns the account's current language preference: always exactly
      // LanguageES or LanguageEN. A stored value outside that set (a legacy row, a
      // manual DB edit, a locale removed from a future supported set) is normalized to
      // LanguageES here, at the DB→domain boundary — this method never returns an
      // unsupported code and never fails because of an unrecognized stored value; it
      // only errors on an actual lookup failure (unknown accountID, DB error).
      LanguageFor(ctx context.Context, accountID uuid.UUID) (string, error)

      // SetLanguage persists lang as the account's language preference. lang MUST be
      // LanguageES or LanguageEN — any other value returns ErrUnsupportedLanguage
      // (detect with errors.Is) WITHOUT writing, so a caller (the gateway's language
      // switch handler) does not have to duplicate this module's validation.
      SetLanguage(ctx context.Context, accountID uuid.UUID, lang string) error
      ```
      Place both after `SetVehicleConfigIfEmpty` (the last existing method) in the interface.
      Acceptance: `go build ./...` fails at this point (no implementation yet) — expected; this
      task only changes the interface + domain constants. Confirm the failure is exactly "does not
      implement Service" pointing at the two missing methods, i.e. no unrelated compile errors.

## T3. sqlc query edits + regeneration (`internal/account/db/query.sql`) — depends on T1, parallel-ok with T2

- [x] T3.1 Add two new queries to `query.sql`, in a sensible place near the other `accounts`-table
      queries:
      ```sql
      -- name: GetAccountLanguage :one
      -- The per-request read path: only the language column, not the whole account row,
      -- so a caller that only needs the language does not pay for the rest of Account.
      SELECT language FROM accounts
      WHERE id = @id;

      -- name: UpdateAccountLanguage :exec
      -- Persists an explicit language switch. Vocabulary validation happens in the Go
      -- caller (Service.SetLanguage) before this query runs — see design.md D1 for why
      -- there is no CHECK constraint doing this at the DB layer instead.
      UPDATE accounts
      SET language   = @language,
          updated_at = now()
      WHERE id = @id;
      ```
- [x] T3.2 Confirm (no edit needed unless verification fails) that `UpsertAccountFromOAuth` and
      `GetAccountByProviderID` already use `SELECT *` / `RETURNING *` and therefore automatically
      include the new `language` column once T1 lands — do not add an explicit column list to
      either query.
- [x] T3.3 Run `make sqlc` (or `sqlc generate`) to regenerate `internal/account/db/`. Confirm and
      report:
      - The Go type sqlc infers for `GetAccountLanguage`'s return value and for
        `UpdateAccountLanguageParams.Language`. **Do not assume `string`** — `design.md` D3 states
        this is the expected outcome for a `NOT NULL` column, based on this module's existing
        `NOT NULL TEXT` columns (`email`, `vin`), but the `vehicle_config` tier found sqlc's
        inferred type for a *nullable* bind param was not what was assumed, so verify by reading
        the generated `db/query.sql.go` rather than trusting the expectation. If sqlc produces
        `pgtype.Text` instead of `string` for either, use the existing `textFromString` /
        `nullableTextToPtr` helpers in `service.go` to convert, exactly as the `vehicle_config`
        tier did when its assumption didn't hold.
      - `accountdb.Account` (the sqlc row type in `models.go`) gains a `Language` field of that
        same inferred type.
      No other module's generated code should change.

## T4. Service implementation (`internal/account/service.go`) — depends on T2, T3

- [x] T4.1 Implement `LanguageFor` and `SetLanguage` on `*service`, plus the unexported
      `normalizeLanguage` and `isSupportedLanguage` helpers (design.md D3 implementation sketch):
      ```go
      func (s *service) LanguageFor(ctx context.Context, accountID uuid.UUID) (string, error) {
          lang, err := s.q.GetAccountLanguage(ctx, accountID)
          if err != nil {
              return "", fmt.Errorf("loading account language: %w", err)
          }
          return normalizeLanguage(lang), nil
      }

      func (s *service) SetLanguage(ctx context.Context, accountID uuid.UUID, lang string) error {
          if !isSupportedLanguage(lang) {
              return ErrUnsupportedLanguage
          }
          if err := s.q.UpdateAccountLanguage(ctx, accountdb.UpdateAccountLanguageParams{
              ID:       accountID,
              Language: lang,
          }); err != nil {
              return fmt.Errorf("setting account language: %w", err)
          }
          return nil
      }

      // normalizeLanguage returns lang unchanged if it is a supported code, else the
      // default LanguageES. This is the DB→domain normalization boundary (design.md D4).
      func normalizeLanguage(lang string) string {
          if isSupportedLanguage(lang) {
              return lang
          }
          return LanguageES
      }

      func isSupportedLanguage(lang string) bool {
          return lang == LanguageES || lang == LanguageEN
      }
      ```
      Adjust the exact plumbing to whatever Go type T3.3 actually confirmed for the sqlc-generated
      fields (convert with `textFromString`/`nullableTextToPtr` if sqlc produced `pgtype.Text`
      instead of `string`). Place the two methods after `SetVehicleConfigIfEmpty` (mirroring the
      interface's method order from T2.2); place the two helpers in the "pure helpers" section
      near `needsRefresh`/`accessExpiry`.
- [x] T4.2 Verify `pgtype` still does not appear in any public type signature — it stays confined
      to `service.go` and the sqlc-generated `accountdb` package, exactly as every prior
      persistence change in this module requires.
      Acceptance: `go build ./...` and `go vet ./...` pass.

## T5. Tests (`internal/account/service_test.go`, `service_integration_test.go`) — depends on T4

- [x] T5.1 Add a pure unit test (no DB) for `normalizeLanguage` in `service_test.go`, covering: both
      supported codes returned unchanged (`"es"` → `"es"`, `"en"` → `"en"`), an unrecognized value
      normalized to `"es"` (e.g. `"fr"`), and the empty string normalized to `"es"`.
- [x] T5.2 Add a `DATABASE_URL`-gated integration test `TestLanguagePreference_RoundTrip` in
      `service_integration_test.go` (self-skips when unset, per the module's existing pattern —
      mirror `TestAccessType_RoundTrip`'s setup: provision an account via `UpsertFromOAuth`, defer
      `deleteAccount`). Cover:
      - **Default on a fresh account**: immediately after `UpsertFromOAuth`, `LanguageFor` returns
        `account.LanguageES` with no explicit `SetLanguage` call.
      - **Successful switch**: `SetLanguage(ctx, acct.ID, account.LanguageEN)` succeeds; a
        subsequent `LanguageFor` returns `account.LanguageEN`.
      - **Switching back**: `SetLanguage(ctx, acct.ID, account.LanguageES)` succeeds; `LanguageFor`
        returns `account.LanguageES` again.
      - **Rejecting an unsupported code**: `SetLanguage(ctx, acct.ID, "fr")` returns an error
        satisfying `errors.Is(err, account.ErrUnsupportedLanguage)`; a subsequent `LanguageFor`
        shows the previously stored value is unchanged (no write occurred).
      - **Normalizing a legacy/out-of-band value**: after seeding the account, run a raw
        `pool.Exec(ctx, "UPDATE accounts SET language = $1 WHERE id = $2", "fr", acct.ID)` to
        simulate a value written outside this module's write path (the test lives in `package
        account`, so it can reach the pool, mirroring the `vehicle_config` tier's self-heal test
        technique). Then call `LanguageFor` and assert it returns `account.LanguageES` — proving
        the normalization guarantee (design.md D3/D4) rather than merely asserting it.
      Acceptance: test self-skips when `DATABASE_URL` is unset; passes with it set (or via the
      testcontainers auto-provisioned Postgres per the module's `AGENTS.md` testing notes); no
      Tesla API call fires.

## T6. Docs (`internal/account/AGENTS.md`) — depends on T2, parallel-ok with T3/T4/T5

- [x] T6.1 Update the "Public interface" section of `internal/account/AGENTS.md` to list
      `LanguageFor(ctx, accountID) (string, error)` and `SetLanguage(ctx, accountID, lang string)
      error` alongside the existing bullet list, with a one-line description matching the other
      entries' style (e.g. "`LanguageFor`/`SetLanguage` — read/persist a user's `{es, en}` language
      preference"). This is required by `CLAUDE.md`'s docs-track-change rule (a module's public
      surface changed in this change, not a follow-up).

## T7. Verification — depends on T1–T6

- [x] T7.1 `go build ./...` and `go vet ./...` pass repo-wide. If the widened `account.Service`
      interface breaks compilation of a fake/double in a sibling module's test file (as happened for
      the `vehicle_config` tier — see `openspec/changes/archive/account/2026-08-03-RM6-account-add-vehicle-config-fields/progress.json`
      decision D4), that is a leader-owned cross-module fix — flag it, do not edit outside
      `internal/account`.
- [x] T7.2 `go test ./...` green and fast; the two new account tests (T5.1 unit, T5.2 integration)
      pass; the integration test self-skips without `DATABASE_URL` (and passes with it set, or with
      Docker running for the testcontainers path); no Tesla API call fires from the test run.
- [x] T7.3 Boundary check: `internal/account` still does not import `internal/tesla` or
      `internal/gateway`; `pgtype` does not appear in any public type or interface; no file outside
      `internal/account` (other than a leader-owned cross-module fix per T7.1) was touched.
- [x] T7.4 `openspec validate RM24-account-add-language-preference --strict` passes and every
      tasks.md checkbox above reflects real completion.
