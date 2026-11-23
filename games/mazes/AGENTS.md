# Repository Guidelines — Mazes

> Mazes is one game in the `drbrain` monorepo. It is compiled into the shared
> `drbrain` binary, served at `/mazes/`, and reached through the `mazes`
> namespace of the single `/graphql` endpoint. Accounts, sessions and
> player-created maps are PostgreSQL rows. The platform, not this game, owns the
> listener, HTTP hardening, the SPA and deployment; see the root `README.md`,
> `AGENTS.md` and `justfile`.

## Project Overview

Dr Brain - Mazes is a tower-of-levels step maze: the player walks one tile per
move from START on the bottom level to FINISH on the top level, using paired
stairs and portals. Players see only the level they stand on (fog). The Go
engine lives in `games/mazes`; the UI lives in `web/src/routes/mazes` and
`web/src/lib/mazes`. Rules and map semantics are in `docs/DEVELOPMENT.md`;
`README.md` is player-facing.

## Architecture & Data Flow

```
platform/host + platform/httpserve : one listener, /graphql executor, SPA
  → graph/ (unified resolver) → mazes namespace → games/mazes/graph.Resolver
  → service.GameService (game/service) → engine (pure rules)
  move: lock → State.Move → seq++ → Clone → unlock → Persist → Broadcast
  → websocket.Hub (8-slot queue per subscriber, drop oldest)
  → graph subscription forwarder (fogs the snapshot) → graphql-ws
  → web/src/lib/mazes store (keeps highest seq)
```

- **Composition root**: `games/mazes/game.go:Mount` wires `game/config.Manager`
  (builtin map files + `platform_maps` rows) + `session.Manager` /
  `session.NewPostgresPersistence` (`platform_sessions`) + `websocket.Hub` into
  `service.New(...)`. `Shutdown` persists every live session.
- **DI via interfaces** defined in `game/service` (`MapStore`, `SessionStore`,
  `Broadcaster`); implementations assert them at compile time. `service` never
  imports `session`/`websocket`.
- **Engine** (`game/engine`): no IO/deps. `NewMap`, `NewState`, `State.Move`,
  `Simulate`, `Map.Shortest`, `Map.Reachable`, `Map.View`. `Move` mutates in
  place; `Clone()` before leaving a lock.
- **Fog**: the service stores the full map snapshot; `graph/convert.go` is the
  only place that shapes what clients see (`Map` = catalog + level-0 preview,
  `LevelView` = current level, `AdminMap` = full, admin only).
- **Locking**: live `*service.Session` guarded by `Lock/Unlock`. Never
  broadcast, do I/O, or `Persist` while holding the session lock. Hub/resolvers
  only see clones.
- **Delivery**: every broadcast = full snapshot + monotonic `Seq`. The
  subscription resolver subscribes first, then emits the current snapshot, then
  only increasing `Seq`.
- **Map snapshot**: sessions embed their own map copy; map edit/delete never
  touches sessions.
- **Errors**: `*service.Error{Code,Msg}` (`NOT_FOUND`, `INVALID_ARGUMENT`,
  `SESSION_TERMINAL`, `FORBIDDEN`, `LIMIT_REACHED`, `UNAUTHENTICATED`);
  resolvers surface them as `extensions.code`; unknown errors log and return
  `INTERNAL`. Missing map/session query → GraphQL `null`. Check with
  `errors.As`.

## Key Directories

| Path | Purpose |
|---|---|
| `games/mazes/game/engine` | rules, moves, BFS solver, fog view (`types.go`, `engine.go`, `view.go`) |
| `games/mazes/game/service` | `Session`, `GameService` (`impl.go`), `errors.go` |
| `games/mazes/game/config` | map store: builtin `maps/*.json` + custom `platform_maps` rows (owner, DRAFT/PUBLISHED) |
| `games/mazes/game/session` | in-memory registry + PostgreSQL persistence (`platform_sessions`) |
| `games/mazes/transport/websocket` | `Hub` (Broadcaster impl; no raw WS clients) |
| `games/mazes/graph/` | resolver + conversions (`convert.go`), admin gate (`auth.go`, `resolver.go`); `generated/`, `model/` are gqlgen output (don't hand-edit) |
| `games/mazes/validate/`, `games/mazes/cmd/validate` | map + finish reachability + reference-solution checks |
| `games/mazes/maps/`, `games/mazes/map-schema.json` | builtin data (release-managed); `solutions/` is local-only and gitignored |
| `games/mazes/api/` | HTTP-layer regression tests (fog, hardening, WebSocket limits) using a legacy test server |
| `web/src/routes/mazes/` | SvelteKit routes: home, `play/[id]`, `multi`, `sessions`, `maps`, `editor`, `learn` |
| `web/src/lib/mazes/` | game logic, urql documents, stores and thin components |
| `graph/schema.graphqls` | canonical unified SDL (the game's split SDL is generated) |

## Development Commands

Run platform commands from the repo root; the per-game `justfile` keeps only
map validation.

```bash
just db-up / just db-down                # dev PostgreSQL (:55432)
just start / just stop / just restart    # serve the shared binary on :8181
just build-web                           # npm run build -> web/build
just regen                               # canonical SDL -> split SDL + gqlgen + TS types
just test                                # go test ./...
just test-db                             # go test ./... against PostgreSQL
just verify                              # fmt check, vet, lint, tests, schema drift, web checks
(cd games/mazes && just validate)        # map + reference-solution checks
go test ./games/mazes/...                # this game's Go packages
go test ./games/mazes/game/engine -run '^TestShippedMapsGolden$'
npm run check && npm test                # web type check + Vitest (repo root)
cd web && npx vitest run src/lib/mazes/tower.test.ts
BASE_URL=http://127.0.0.1:8181 GAMES=mazes ./deploy/smoke.sh
```

`DRBRAIN_DATABASE_URL` is required at startup. Mazes reads `MAZES_*` env:
`MAZES_MAPS_DIR`, `MAZES_SOLUTIONS_DIR`, `MAZES_PUBLIC_URL`, `MAZES_DEBUG`,
`MAZES_MAX_SESSIONS`, `MAZES_MAX_USER_MAPS`, plus `SESSION_TTL`,
`ADMIN_API_KEY` and `ALLOW_UNAUTHENTICATED_ADMIN`.

## Code Conventions & Common Patterns

- Standard `gofmt`; domain-noun types (`GameService`, `MapStore`), concrete
  impls usually `Manager`; GraphQL camelCase, enums/codes `UPPER_SNAKE`.
- Mutating service pattern: lock session → mutate → `touch` (Seq++,
  LastActionAt) → `Clone` → unlock → persist → broadcast the clone. A blocked
  move bumps Seq and broadcasts but does not persist.
- Persistence: sessions via `platform/sessionstore` into `platform_sessions`;
  builtin maps are read-only files and custom maps are `platform_maps` rows.
  Load-time validation replays the stored path against the map snapshot.
  `pg_dump` backs up custom maps.
- Schema change flow: edit the canonical `graph/schema.graphqls` → `just regen`
  → update `games/mazes/graph/convert.go` and resolvers → mirror the operations
  in `web/src/lib/mazes/queries.ts` and `types.ts` → update
  `games/mazes/llms.txt.tmpl`, `web/src/lib/mazes/prompt.ts` and the query
  copies in `games/mazes/api/hardening_test.go`.
- Frontend: Svelte 5 runes only (`$state/$derived/$effect/$props`), `onclick`
  attributes, keyed `{#each}`; urql (network-only, POST) wrapped with the
  namespace `mapExchange`, plus a lazily shared `graphql-ws` subscription
  client. **Gotcha**: explicit `onchange/oninput` handlers run *before*
  `bind:value` updates — read `event.currentTarget.value`, not the bound var.
- New route under `web/src/routes/mazes/` needs no Go change: the platform
  serves `index.html` for unknown paths under the prefix.
- Don't run `go mod tidy` casually (gqlgen generate rewrites `go.mod`).

## Important Files

`games/mazes/game.go`, `games/mazes/graph/{schema.graphqls,resolver.go,auth.go,convert.go,schema.resolvers.go}`,
`games/mazes/game/engine/{types,engine,view}.go`, `games/mazes/game/service/{impl,errors}.go`,
`games/mazes/transport/websocket/hub.go`, `games/mazes/llms.txt.tmpl`,
`games/mazes/map-schema.json`, `games/mazes/gqlgen.yml`,
`web/src/lib/mazes/{queries,types,graphql,fog,prompt,admin,ownerTokens}.ts`,
`web/src/lib/mazes/stores/source.ts` (the shared session store is
`@drbrain/ui/session`), `web/src/routes/mazes/`.

Adding a map: create `games/mazes/maps/<id>.json` (levels 1–12, each 3–20 ×
3–20, identical size; glyphs `. o S F U D 1-9`; S on level 0, F on the top
level; stairs paired: `U` at level l (x,y) needs `D` at level l+1 (x,y) and vice
versa; each portal digit exactly twice on one level) +
`games/mazes/solutions/<id>.json` (`{"moves":[…]}`), then `just validate` from
`games/mazes` and add its golden par.

## Runtime/Tooling Preferences

Go per `go.mod`; gqlgen 0.17.73; PostgreSQL is required. Frontend: **npm**
workspaces, Node 22, Vite, adapter-static; `web/build` is CI-built and
gitignored. The Go binary serves it from `DRBRAIN_STATIC_DIR`.
`graph/generated` and `games/mazes/graph/generated` are committed.

## Testing & QA

- Go: stdlib `testing`, `t.Helper/TempDir/Cleanup`, table + `t.Run`, no
  `t.Parallel()` (tests use `t.Setenv`). Service tests use `newEnv` in
  `game/service/helpers_test.go` (fake MapStore, spy persistence, broadcaster
  recorder). The `api` package tests the HTTP layer against the platform host.
  PostgreSQL-backed tests skip unless `TEST_DATABASE_URL` is set.
- Golden shortest-route lengths of all 16 shipped maps (deliberate changes
  only): `game/engine/engine_test.go` (`TestShippedMapsGolden`) and
  `validate/validate_test.go`; the table is in `docs/DEVELOPMENT.md`.
- Frontend: Vitest + jsdom + Testing Library under `web/src/lib/mazes`,
  `npm test` from the repo root.
- UI changes: load `/mazes/play/<id>` and `/mazes/multi` in a browser.

## Known Issues

- `games/mazes/scripts/smoke.sh` predates the monorepo (it builds a standalone
  binary); use `deploy/smoke.sh` instead.
