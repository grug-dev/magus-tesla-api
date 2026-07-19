# Tasks — platform-pin-templ-codegen

Tooling/doc-only change. No `specs/` delta, no `design.md`, no database. Each task
is atomic and independently verifiable. Keep this file updated live if applied.

## 1. Pin the templ CLI as a go.mod tool directive

- [x] 1.1 Run `go get -tool github.com/a-h/templ/cmd/templ@v0.3.1020` (same version
  as the `github.com/a-h/templ` runtime require, to satisfy templ's generator↔runtime
  version check).
- [x] 1.2 Confirm `go.mod` gains a `tool github.com/a-h/templ/cmd/templ` directive and
  that `go mod tidy` leaves it in place (the tool deps are now *declared*, not accidental).
- [x] 1.3 Verify `go tool templ --version` prints `v0.3.1020`.

**Acceptance:** `go tool templ generate ./...` runs with no `go.mod` diff afterward.

## 2. Add a Makefile codegen target

- [x] 2.1 In the "Codegen / deps" block of `Makefile` (next to `sqlc`), add:
  `templ: ## Regenerate Templ HTML code (go tool templ generate)` running
  `go tool templ generate ./...`.
- [x] 2.2 (Optional) add a `generate: sqlc templ` umbrella target that runs both.
- [x] 2.3 Run `make templ` and confirm it regenerates `*_templ.go` with no `go.mod`
  diff and `go build ./...` still passes.

**Acceptance:** `make templ` is the single canonical regen command; `git diff go.mod`
is empty after it runs.

## 3. Update the documentation references

- [x] 3.1 `ai/htmx-conventions.md` (line ~21, ~23): replace bare `templ generate` with
  `make templ`.
- [x] 3.2 `internal/gateway/AGENTS.md` (the "How to add a page" steps ~125, ~169, and the
  regeneration cheatsheet ~233): replace `templ generate` with `make templ`.
- [x] 3.3 Add one line to the codegen convention (in `ai/htmx-conventions.md` or the
  gateway AGENTS.md): "Never run a bare `go run …/templ generate` inside the module — it
  pollutes `go.mod` with the CLI's deps. Use `make templ` (pinned `go tool`)."

**Acceptance:** no doc references a bare/unpinned `templ generate`; agents pointed at
`make templ`.

## 4. Verify

- [x] 4.1 `go build ./... && go vet ./... && go test ./internal/gateway/...` all pass.
- [x] 4.2 Fresh-clone smoke: `make templ` works without a global `templ` install on PATH.
