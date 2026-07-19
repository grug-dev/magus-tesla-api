# Proposal — platform-pin-templ-codegen

Pin the Templ code generator so `templ generate` is reproducible and stops
silently mutating `go.mod`.

## Why

While implementing `RM3-gateway-add-manual-charge-ui`, running the bare
`go run github.com/a-h/templ/cmd/templ generate ./...` inside the module pulled
the templ **CLI's** transitive dependencies (`a-h/parse`, `brotli`,
`cli/browser`, `fatih/color`, `fsnotify`, `x/mod`, `x/tools`, …) into `go.mod`
as `// indirect` requires — none of which the application imports. It had to be
undone with `go mod tidy` before committing. There is no canonical, version-
pinned way to regenerate templates: the docs say bare `templ generate` (4
places), which assumes an unpinned CLI on `PATH` and risks a generator↔runtime
version mismatch that templ's own version check warns about.

## What changes

Adopt Templ's officially-recommended reproducible invocation for Go 1.24+ (this
repo is on **Go 1.25**): a **`go tool` directive** pinned to the same version as
the `github.com/a-h/templ` runtime already in `go.mod` (currently `v0.3.1020`),
plus a single `make` target and doc updates so every human and agent uses it.

- **go.mod** — add the tool directive: `go get -tool github.com/a-h/templ/cmd/templ@v0.3.1020`
  (records the CLI + its tool-only deps under a dedicated `tool` directive; declared
  once and committed, so subsequent runs never re-mutate `go.mod`).
- **Makefile** — add a `templ` (a.k.a. `generate`) target: `go tool templ generate ./...`,
  in the "Codegen / deps" block next to `sqlc`.
- **Docs** — replace the 4 bare `templ generate` references with `make templ`
  (`ai/htmx-conventions.md`, `internal/gateway/AGENTS.md` ×3, incl. the
  regeneration cheatsheet).
- **Pipeline guidance** — note in the codegen convention that agents must use the
  pinned target, never a bare `go run …/templ generate` that mutates `go.mod`.

**Recommended:** the `go tool` directive (above) — idiomatic Go 1.25, guarantees
generator↔runtime version parity, CI-reproducible, and structurally prevents the
accidental mutation because the deps are declared once.

**Alternative (documented, not chosen):** `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate ./...`
— zero `go.mod` footprint (ephemeral module context), but the version lives only
in the Makefile string (drift risk vs. the runtime) and it rebuilds the CLI each
run. This is the quick fix already used in RM3; the tool directive supersedes it.

**Rejected:** `go install …@latest` on `PATH` (matches the current `sqlc`
pattern) — unpinned, drifts across machines, no generator↔runtime guarantee.

## Impact

- **Breaking:** No. Generated output is unchanged; only how it is produced.
- **Modules affected:** none in `internal/` directly — this is cross-cutting
  build tooling (`platform-`). Touches `go.mod`, `Makefile`, `ai/htmx-conventions.md`,
  `internal/gateway/AGENTS.md`.
- **No capability spec change** and **no database change** — tooling/doc-only, so
  no `specs/` delta and no `design.md` (archive with `--skip-specs`).
- **Read paths / performance:** N/A.
- **Follow-on:** the same pinned pattern could later cover `sqlc` for consistency
  (out of scope here; note only).
