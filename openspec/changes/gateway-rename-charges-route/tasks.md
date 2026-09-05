> **Unit tests: excluded.** No new test is written. The 6 existing test files that reference
> the page are renamed and updated so they keep compiling and asserting the same behaviour —
> that is maintenance of existing coverage, not new coverage.
>
> **Test execution:** the assistant does not run the suite. `go build ./...`, `go vet ./...`,
> `gofmt -l` and the standalone guards are run by the assistant; `make test` is the owner's.

## 1. Rename the files

- [ ] 1.1 `git mv internal/gateway/handlers/charges.go internal/gateway/handlers/external_charges.go`
- [ ] 1.2 `git mv internal/gateway/handlers/charges_range.go internal/gateway/handlers/external_charges_range.go`
- [ ] 1.3 `git mv internal/gateway/handlers/charges_tiles.go internal/gateway/handlers/external_charges_tiles.go`
- [ ] 1.4 `git mv internal/gateway/templates/pages/charges.templ internal/gateway/templates/pages/external_charges.templ` (and delete the stale `charges_templ.go`)
- [ ] 1.5 `git mv` the five fragment files: `charge_create_form.templ`, `charge_row.templ`, `charge_row_edit.templ`, `charges_list.templ`, `charges_vm.go` → `external_charge_create_form.templ`, `external_charge_row.templ`, `external_charge_row_edit.templ`, `external_charges_list.templ`, `external_charges_vm.go` (and delete their stale `*_templ.go`)
- [ ] 1.6 `git mv` the four test files: `handlers/charges_test.go`, `charges_in_progress_conflict_test.go`, `charges_single_edit_test.go`, `charges_error_visibility_test.go` → `external_charges*_test.go`

## 2. Rename the routes

- [ ] 2.1 In `internal/gateway/gateway.go` (lines ~176-183) change all 8 registrations: `/charges` → `/external-charges`, and `/ui/charges...` → `/ui/external-charges...` for the 7 fragment routes. Do NOT add a redirect from the old paths (design §D4).
- [ ] 2.2 In `internal/gateway/templates/layouts/nav.go:28` change `Href: "/charges"` and `Active: active == "/charges"` to `/external-charges`.
- [ ] 2.3 Update every `hx-get` / `hx-post` / `hx-put` / `hx-delete` URL literal in the five fragment templates and the page template (11 literals: `charge_create_form` 1, `charge_row` 2, `charge_row_edit` 2, `charges_list` 2, `pages/charges.templ` 2 incl. the `BaseAuth(..., "/charges")` active argument).

## 3. Rename the Go symbols

- [ ] 3.1 Rename the 8 handler methods: `ChargePage`→`ExternalChargesPage`, `ChargesContentFragment`→`ExternalChargesContentFragment`, `ChargesListFragment`→`ExternalChargesListFragment`, `ChargeRowStatic`→`ExternalChargeRowStatic`, `ChargeRowEditFragment`→`ExternalChargeRowEditFragment`, `ChargeCreate`→`ExternalChargeCreate`, `ChargeRowUpdate`→`ExternalChargeRowUpdate`, `ChargeRowDelete`→`ExternalChargeRowDelete`; update `gateway.go`'s references.
- [ ] 3.2 Rename the package-private helpers in the same files: `buildChargesPage`, `defaultChargesWindow`, `parseChargesRange`, `buildChargesPresets`, `buildChargeTiles`, `chargeEntryVMFromEntry`, `chargeEntryVMFromRawValues`, `parseChargeForm`, `inProgressConflictOn`, `recalculateAfterChargeWrite`, `fetchEntryVM`, `fetchEntryTeslaIDAndChargedOn`, `entryComplete`, `applyRawRequiredState` → `ExternalCharge…` / `externalCharge…` equivalents where the name currently says "charge(s)".
- [ ] 3.3 Rename the 9 templ components: `ChargePage`, `ChargeCreateForm`, `ChargeCreateSuccessOOB`, `ChargeRow`, `ChargeRowEdit`, `ChargesList`, `chargesRangeSelector`, `chargeTiles`, `ChargesEmptyState` → `ExternalCharge…` equivalents.
- [ ] 3.4 Rename the 4 view-model types in `external_charges_vm.go`: `ChargesPageData`, `ChargeEntryVM`, `ChargeFormValues`, `ChargeTiles` → `ExternalChargesPageData`, `ExternalChargeEntryVM`, `ExternalChargeFormValues`, `ExternalChargeTiles`. Leave `RangePreset` alone — it is shared with the dashboard-history and Supercharger selectors.
- [ ] 3.5 Rename the CSRF key: `csrfManualChargeKey` → `csrfExternalChargeKey`, value `"csrf_manualcharge"` → `"csrf_externalcharge"`. Update the comment references in `handlers/supercharger.go:36` and `handlers/preferences.go:144`.

## 4. Rename the DOM ids

- [ ] 4.1 In the templates, rename the 8 id families: `charges-list`, `charges-content`, `charges-create-form`, `charges-create-hint`, `charges-create-optional`, `charges-window-start`, `charges-window-end`, `charge-row-*` (incl. `charge-row-hint-*` and `charge-row-optional-*`) → `external-` prefixed.
- [ ] 4.2 Update every matching `hx-target`, `hx-swap-oob`, `hx-include` and `HX-Retarget` header value in the templates and in `handlers/external_charges.go`.

## 5. Rename the one i18n key

- [ ] 5.1 In `internal/gateway/i18n/catalog.go` rename `KeyNavManualRecords` → `KeyNavExternalCharges` and its key string `"nav.manual_records"` → `"nav.external_charges"`. Leave the ES/EN values ("Externas"/"External") unchanged. Do NOT touch the 86 `charges_*` keys (design §D3).
- [ ] 5.2 Update the reference in `templates/layouts/nav.go:28`.

## 6. Update the tests

- [ ] 6.1 Update the renamed test files' route literals, handler names, component names, view-model types and DOM ids. No assertion changes — same behaviour, new identifiers.
- [ ] 6.2 Update `handlers/lang_test.go` (3 refs) and `templates/ui/nav_shell_test.go` (2 refs).

## 7. Regenerate and verify

- [ ] 7.1 Run `templ generate` and confirm every `*_templ.go` for this page is regenerated under its new name and the old ones are gone.
- [ ] 7.2 Run `go build ./...`, `go vet ./...` and `gofmt -l` — all clean. (`vet` compiles the `_test.go` files, so it catches signature drift in the renamed tests.)
- [ ] 7.3 Run `make ui-guard`, `make i18n-guard`, `make money-guard`, `make tz-guard`, `make boundary-guard` — all pass.
- [ ] 7.4 Run the leftover-literal grep and confirm it returns nothing outside `openspec/changes/archive/`:
      `grep -rnE '/ui/charges|"/charges"|charges-list|charges-content|charges-create|charges-window|charge-row-' --include='*.go' --include='*.templ' --include='*.js' internal cmd`
- [ ] 7.5 Confirm `MIGRATIONS_DIRS`, `db-setup`/`db-reset` and `sqlc` are untouched by this change — record the finding in the change (design says no DB surface is involved; verify, do not assume).
- [ ] 7.6 Ask the owner to run `make test` and report the result. Until they do, this change is **awaiting the owner's verification**, not done.

## 8. Update the docs (same change — project rule)

- [ ] 8.1 `internal/gateway/AGENTS.md` — 11 refs.
- [ ] 8.2 `internal/charging/AGENTS.md` — 1 ref; and the `handlers/charges.go` path comments in `internal/analytics/analytics.go:171` and `internal/charging/charging.go:57`.
- [ ] 8.3 `README.md` "Project Structure"/"Architecture" and `cmd/README.md` — check whether either names the page or its files; update if so.
- [ ] 8.4 KB: rename `kkpa/context/input-port/charging/charges.md` → `external-charges.md` and update its route, endpoint table and file map.
- [ ] 8.5 KB: update the 10 `/charges` rows in `kkpa/context/INDEX.md`, including the `Manual Records` / `Registros manuales` / `charges page` alias rows and the renamed KB path.
- [ ] 8.6 KB: update `workflows/manual-charge-crud.md` (8 refs), `architecture/charge-record-mutation.md` (7 refs), and `use-case/charging/update-manual-charge.md` + `delete-manual-charge.md` endpoint headers.
- [ ] 8.7 `.claude/skills/kkpa-goth-scaffold-ui/` (and its `.agents/` copy) — the `charges` slice is the skill's gold-standard example in `README.md` and `references/charges-slice-pattern.md`; update the route and file names it cites.

## 9. Land it

- [ ] 9.1 Commit on `ft/CH44-gateway-rename-charges-route`, bumping `openspec/.work-counter` from 43 to 44 in the same commit.
- [ ] 9.2 After the owner confirms `make test` passes, run `openspec archive` and move the change folder under `openspec/changes/archive/gateway/`.
