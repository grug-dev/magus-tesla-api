# cmd/ — Executable Entry Points

In Go projects, `cmd/` holds the **executable entry points** — things you actually run as programs. Each subdirectory under `cmd/` is a separate binary with its own `package main` and a `func main()`.

## Convention

```
cmd/
├── setup/              # binary: go run ./cmd/setup
│   ├── main.go         # package main; func main()
│   └── callback.go     # package main (helpers for this binary only)
├── web/                # binary: go run ./cmd/web
│   └── main.go         # package main; func main()
└── explore-tesla-api/  # binary: go run ./cmd/explore-tesla-api
    ├── main.go         # package main; func main()
    └── README.md       # detailed guide for this tool
```

## Scope rules

| Lives in `cmd/` | Lives in `internal/` (or `pkg/`) |
|---|---|
| `package main` only | Library packages (`package account`, `package tesla`, etc.) |
| One `func main()` per binary | Never `func main()` |
| Wires dependencies together, starts the process | Business logic, domain rules, reusable code |
| Thin — calls into `internal/*` packages | Imported by `cmd/*` binaries and each other |

**Key principle:** `cmd/` binaries should be **thin orchestration layers**. They parse config, wire up the dependencies (DB pool, services, router), call `ListenAndServe` or similar, and handle graceful shutdown. The actual logic lives in `internal/`. If you find yourself writing business logic in `cmd/`, it usually belongs in a library package instead.

## This project's binaries

| Binary | Command | Purpose | Lifetime |
|---|---|---|---|
| `cmd/setup` | `go run ./cmd/setup` | One-time Tesla OAuth token capture → saves to `.env` | Run once, exits |
| `cmd/web` | `go run ./cmd/web` | The production multi-tenant HTTP gateway (vehicle dashboard) | Long-running, deployed |
| `cmd/explore-tesla-api` | `go run ./cmd/explore-tesla-api` | On-demand inspector for raw Tesla Fleet API JSON payloads | Run on-demand, exits |

For details on a specific binary, see its own README (when present):

- [explore-tesla-api/README.md](explore-tesla-api/README.md) — raw JSON inspector tool

## Adding a new binary

1. Create `cmd/<name>/main.go` with `package main` and a `func main()`.
2. Keep it thin — wire up `internal/` packages, don't implement logic here.
3. Add a row to the table above and a README if the binary needs usage docs.
4. `go build ./...` compiles it automatically alongside everything else.

This pattern scales — if you later add a CLI for migrations, a background worker, or a cron job, each gets its own `cmd/<name>/` subdirectory. They all share the same `internal/` libraries but produce independent binaries.