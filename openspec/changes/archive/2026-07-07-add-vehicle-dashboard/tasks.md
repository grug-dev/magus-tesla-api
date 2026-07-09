# Tasks — add-vehicle-dashboard

> Capstone: wires account → tesla → Templ fragment end-to-end. Uses existing interfaces
> (`account.AccessTokenFor`, `tesla.ListVehicles`). Builds/codegen run by the assistant.

## 1. Wire the tesla adapter
- [x] 1.1 Add `Tesla tesla.VehicleService` to gateway `Deps` and the handlers `Deps`/`Handler`; build `tesla.NewClient()` in `cmd/web`

## 2. Templates
- [x] 2.1 `templates/fragments/vehicles.templ` — `Vehicle{DisplayName,VIN,State}` + `VehiclesData{Vehicles,Notice,NeedsConnect}` + `VehiclesList` (list / connect-prompt / notice)
- [x] 2.2 `templates/pages/dashboard.templ` — page wrapping the list in `@templ.Fragment("vehicles")` + a Refresh button (`hx-get /ui/vehicles`)
- [x] 2.3 `home.templ` — add "View your vehicles" link when signed in
- [x] 2.4 `templ generate`

## 3. Handlers & routes
- [x] 3.1 `vehiclesFor(ctx, uid) fragments.VehiclesData` — `AccessTokenFor` → `tesla.ListVehicles`; map DTOs; handle `account.ErrNoTeslaConnection` (connect) and `tesla.ErrUnauthorized` (reconnect) and other errors
- [x] 3.2 `Dashboard` (`GET /dashboard`) + `VehiclesFragment` (`GET /ui/vehicles`) — auth-guard, render page/fragment
- [x] 3.3 Register routes in `gateway.NewEngine`

## 4. Verify
- [x] 4.1 `templ generate` + `make build` + `make vet`
- [x] 4.2 `make test` — handlers unit tests (fakes for account+tesla): lists vehicles; no-connection → connect prompt; unauthorized → reconnect; gateway httptest: anonymous `/dashboard` → `/login`; `/ui/vehicles` returns fragment only
- [x] 4.3 `openspec validate add-vehicle-dashboard --strict`
