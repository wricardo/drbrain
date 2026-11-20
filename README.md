# drbrain

One codebase and one binary for the Dr Brain games. Four games live here as
separate modules of the same platform:

| game | directory | what it is |
|---|---|---|
| Mazes | `games/mazes` | tower-of-levels step maze |
| Motor Programming | `games/motor` | tape/VM programming puzzle with accounts and leaderboards |
| Tesla Road Trip | `games/tesla` | fogged road-trip grid with an MCP transport |
| Delivery | `games/delivery` | tick-based city delivery simulation |

## Shape

```
cmd/drbrain/           one binary, subcommands (serve, validate, mapgen, games)
platform/              host lifecycle and I/O shared by every game
games/<name>/          engine, GraphQL schema, resolvers, maps
web/                   one SvelteKit app, routes under /<game>/
deploy/                systemd unit, nginx vhost, workflow
```

`platform` owns the process: config, listeners, path routing, websocket hub,
graceful shutdown, static app, health, and the one GraphQL executor. A game owns
its rules, its schema fragment, and its UI routes. A game never starts its own
HTTP server.

Schema changes start in `graph/schema.graphqls`; regenerate everything with
`go generate ./games/... ./graph`.

## Routing

One listener, one process, one port, one GraphQL API:

| path | owner |
|---|---|
| `/graphql`, `/graphql/`, `/graphql/playground` | platform: one schema, namespaced per game |
| `/<game>/mcp` | that game's MCP transport |
| `/<game>/llms.txt` | platform, rendered from the game's template |
| `/<game>/...` | the single SvelteKit app |
| `/_app/...`, `/favicon.svg` | SvelteKit assets |

Queries and mutations select a game namespace (`delivery`, `mazes`,
`motorProgramming`, `tesla`). Subscriptions use flat prefixed roots because
GraphQL streams only at the operation root. Games keep ordinary muxes with
absolute paths for non-GraphQL routes; the host mounts those under the game
prefix with `http.StripPrefix`.

## Ground rules

- **One Go module.** Four binaries would re-create four toolchains; one module,
  `drbrain serve --games=a,b`, optional `--games` per deploy.
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

## Migration status

The four source repositories are being imported with history, then migrated one
at a time. See `bd show drbrain-<epic>` for the live plan; this repository starts
empty by design.
