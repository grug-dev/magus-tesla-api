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
├── poller/             # binary: go run ./cmd/poller
│   └── main.go         # package main; func main()
├── migrate/            # binary: go run ./cmd/migrate
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
| `cmd/poller` | `go run ./cmd/poller` | **Wiring only** since RM29 tier 7. It composes `internal/app`'s `Processor` and starts `internal/app`'s `Scheduler`; the cycle itself — sync fleet data → process charging data → recalculate analytics (metrics before charge gaps, an order gap detection depends on) — lives in `internal/app`, not here. `--once` calls the same `ProcessVehicleData` the scheduler's tick calls, so the two paths cannot diverge. It also serves the manual-rerun HTTP endpoint, `POST /internal/rerun/<POLLER_RERUN_TOKEN>` — off by default, and it starts only when `POLLER_RERUN_TOKEN` is set in `.env` (`platform-add-manual-rerun-api`). | Long-running nightly, or one-shot with `--once` |
| `cmd/migrate` | `make migrate-run` (local) / `make docker-migrate` (Docker) | Applies every module's goose migrations, in order, then exits. Uses the goose library directly (not the goose CLI — see the file's doc comment for why). It is the `migrate` service's `ENTRYPOINT` in the Docker deploy. `make migrate-run` runs it locally against `DATABASE_URL`, no goose CLI needed; `make docker-migrate` runs the one-shot `migrate` service in Compose. | Run once per deploy, exits |
| `cmd/explore-tesla-api` | `go run ./cmd/explore-tesla-api` | On-demand inspector for raw Tesla Fleet API JSON payloads | Run on-demand, exits |

**`cmd/web` and `cmd/poller` are the two long-running binaries built into containers** by
`deploy/docker/Dockerfile`, for production. `cmd/migrate` is also built into a container
(the one-shot `migrate` service). See
[`docs/1-deploy/docker.md`](../docs/1-deploy/docker.md) for the full Docker deploy
reference.

For details on a specific binary, see its own README (when present):

- [explore-tesla-api/README.md](explore-tesla-api/README.md) — raw JSON inspector tool

## Adding a new binary

1. Create `cmd/<name>/main.go` with `package main` and a `func main()`.
2. Keep it thin — wire up `internal/` packages, don't implement logic here.
3. Add a row to the table above and a README if the binary needs usage docs.
4. `go build ./...` compiles it automatically alongside everything else.

This pattern scales — `cmd/migrate` is one example (a migration runner), and a future
background worker or cron job would get its own `cmd/<name>/` subdirectory the same way.
They all share the same `internal/` libraries but produce independent binaries.