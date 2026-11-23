# drbrain

One codebase, one Go module and one binary for the Dr Brain games. Five games
live here on the same platform:

| game | directory | what it is |
|---|---|---|
| Mazes | `games/mazes` | tower-of-levels step maze |
| Motor Programming | `games/motor` | tape/VM programming puzzle with accounts and leaderboards |
| Tesla Road Trip | `games/tesla` | fogged road-trip grid with an MCP transport |
| Delivery | `games/delivery` | tick-based city delivery simulation |
| Music Region | `games/music` | measure-reordering classical excerpt puzzle |

## Shape

```
cmd/drbrain/           one binary: serve, games list, validate/mapgen (only for games
                       that implement the optional interfaces), import-maps,
                       backfill-delivery-history
platform/              host lifecycle and I/O shared by every game
games/<name>/          engine, GraphQL schema, resolvers, maps
web/                   one SvelteKit app, routes under /<game>/
deploy/                systemd unit, nginx vhost, workflow
```

`platform` owns the process: config, listeners, path routing, websocket limits,
graceful shutdown, static app, health, and the one GraphQL executor. A game owns
its rules, its generated schema fragment, and its UI routes. A game never starts
its own HTTP server.

Schema changes start in `graph/schema.graphqls`; regenerate everything with
`go generate ./games/... ./graph`.

## Routing

One listener, one process, one port, one GraphQL API:

| path | owner |
|---|---|
| `/graphql`, `/graphql/`, `/graphql/playground` | platform: one schema, namespaced per game |
| `/auth/...` | platform: global sign-in routes |
| `/settings`, `/u/<handle>` | global account settings and public profiles |
| `/tesla/mcp` | Tesla's MCP transport (the only game with one) |
| `/<game>/llms.txt` | platform, rendered from the game's template |
| `/<game>/...` | the single SvelteKit app |
| `/_app/...`, `/favicon.svg` | SvelteKit assets |

Queries and mutations select a game namespace (`delivery`, `mazes`,
`motorProgramming`, `tesla`, `music`) or the global `account` namespace.
Subscriptions use flat prefixed roots because GraphQL streams only at the
operation root. Games keep ordinary muxes with absolute paths for non-GraphQL
routes; the host mounts those under the game prefix with `http.StripPrefix`.

## Ground rules

- **One Go module.** Separate binaries would re-create separate toolchains; one
  module, `drbrain serve --games=a,b`, optional `--games` per deploy.
- **One GraphQL schema.** `graph/schema.graphqls` is the canonical schema;
  `cmd/split-schema` derives the per-game SDL that feeds each game's gqlgen
  package, and one executor serves the unified schema. There is no
  request-splitting gateway.
- **Global player accounts.** Sign-up, sign-in, profiles and settings live in
  `platform/account` and `platform/auth`, served under the `account` GraphQL
  namespace and `/auth`, `/settings`, `/u/<handle>`. Games consume the shared
  viewer; they do not own accounts.
- **One process.** All games share a listener; per-game `maxSessions`, ws
  limits, and panic recovery keep one game from starving the others.
- **One frontend app.** Routes are namespaced per game; the build happens in CI
  and is never committed.
- **Extract by adoption.** Move the best existing implementation, migrate one
  game, delete that game's copy. No generic version written first.

## Status

All five games are compiled into `cmd/drbrain` and share the unified GraphQL
executor, PostgreSQL stores and the single SvelteKit app. Accounts, sessions and
custom maps/cities are PostgreSQL rows; only builtin maps/cities, the music score
library and the built web app are files.

## Development

The root `justfile` is the maintained loop; per-game justfiles hold only that
game's tooling (map validation, delivery sim, Tesla RL):

```bash
just db-up               # start the dev PostgreSQL from docker-compose.yaml (:55432)
just db-down             # stop it; the pgdata volume keeps the data
just restart             # stop, regenerate schema, build web + binary, start detached
just port=8081 restart   # same on another port
just regen               # split/merge schema and regenerate Go + TypeScript
just build               # go build -o tmp/drbrain ./cmd/drbrain
just build-web           # npm run build -> web/build
just test                # go test ./...
just test-db             # go test ./... against the dev PostgreSQL
just clear-local-test-data  # wipe local rows (accounts, sessions, maps); schema kept
just verify              # gofmt check, vet, lint, tests, schema drift, web checks
(cd games/mazes && just validate)   # a game's own map validator
```

`just start`/`just stop` run the binary from `tmp/` with logs in
`tmp/drbrain.log`. See `AGENTS.md` for the full architecture and test commands.
