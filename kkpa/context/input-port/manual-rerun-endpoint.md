# Manual rerun endpoint

> **Adapter side:** what the outside world calls, and where it forwards to. **No backend flow
> here** — that lives in the linked use-case files.
>
> "Input port" in this KB means the *driving adapter* — the page, route, or endpoint that
> triggers a use case. (Strict hexagonal reserves "port" for the interface the core exposes;
> this KB deliberately uses the looser sense. Do not go looking for a `Port` interface in the
> code because of this filename.)
>
> All KB links below are relative to `kkpa/context/`.

## Page

- **Route / URI:** `n/a` — there is no UI page. This is an operator endpoint, called by `curl`.
- **Description:** Starts one extra vehicle-data cycle on demand, over HTTPS, without SSH. It
  exists because the VPS does not let the owner run `cmd/poller --once` by hand.
- **Module:** `n/a` — it is served by `cmd/poller`, which is not a child of `internal/` and so
  is not a module by this project's definition.

## Front-end component map

None. There is no page and no front-end file.

The only client is a shell command, documented in `docs/0-set-up/deployment.md` §8.12:

```bash
curl -i -X POST https://<domain>/internal/rerun/<token>
```

## Listener and routing

**This endpoint does not go through `internal/gateway`.** It is `cmd/poller`'s own `net/http`
listener. Nothing about it touches Gin, Templ, or the gateway's session middleware.

| File | Role |
|---|---|
| `cmd/poller/main.go` | Builds the `ServeMux` and starts the listener, only in the scheduler branch and only when `POLLER_RERUN_TOKEN` is set. Holds `rerunAddr`. |
| `cmd/poller/rerun.go` | `newRerunMux` registers the route; `rerunHandler` answers it. |
| `internal/config/config.go` | Reads `POLLER_RERUN_TOKEN`. Empty means the listener never starts. |
| `deploy/docker/compose.yaml` | `expose: ["8081"]` on the `poller` service. No `ports:`, so the host never sees it. |
| `deploy/docker/Caddyfile` | `handle /internal/rerun/*` reverse-proxies to `poller:8081`. Everything else still goes to `web:8080`. |

**Reachability needs both halves.** The container must listen, and Caddy must have the route.
Removing either one takes the endpoint offline without touching the handler.

## Endpoints

| Method + path | Purpose | Use case |
|---|---|---|
| `POST /internal/rerun/<POLLER_RERUN_TOKEN>` | Start one vehicle-data cycle now | `use-case/trigger-manual-rerun.md` |

## Conventions & gotchas

- **The last path segment is the secret.** It is part of the registered route, so a wrong value
  is a plain `404`. Never log the full path at info level.
- **Empty token means the route does not exist.** Fail-closed is the design, not a default.
- **Port 8081 is internal only.** It is `expose:`d, never published. Only Caddy dials it, by
  service name, on the compose network.
