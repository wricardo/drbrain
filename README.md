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
cmd/drbrain/           one binary, subcommands (serve, validate, mapgen, tui, games)
platform/              host lifecycle and I/O shared by every game
games/<name>/          engine, GraphQL schema, resolvers, maps
web/                   one SvelteKit app, routes under /<game>/
deploy/                systemd unit, nginx vhost, workflow
```

`platform` owns the process: config, listeners, path routing, websocket hub,
graceful shutdown, static app, health. A game owns its rules, its GraphQL
schema, and its UI routes. A game never starts its own HTTP server.

## Routing

One listener, one process, one port, path-based routing:

| path | owner |
|---|---|
| `/<game>/graphql`, `/<game>/graphql/` | that game's GraphQL handler |
| `/<game>/mcp` | that game's MCP transport |
| `/<game>/...` | the single SvelteKit app |
| `/_app/...`, `/favicon.svg` | SvelteKit assets |

Games keep ordinary muxes with absolute paths (`/graphql`, `/mcp`); the host
mounts only each game's declared API surface under its prefix with
`http.StripPrefix`. Everything else falls through to the app, so SPA routes are
never shadowed.

## Ground rules

- **One Go module.** Four binaries would re-create four toolchains; one module,
  `drbrain serve --games=a,b`, optional `--games` per deploy.
- **No unified GraphQL schema.** One `gqlgen.yml` and one generated package per
  game. Root types and field semantics already differ.
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
