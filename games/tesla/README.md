# Tesla Road Trip

Grid-based, multi-session game: drive a Tesla around a fogged map to visit every
park without running out of battery. It is one game in the Dr Brain monorepo,
served by the shared `drbrain` binary (GraphQL via the unified executor, MCP
over Streamable HTTP, and the SvelteKit app).

## Requirements

| Tool | Version | Needed for |
|------|---------|-----------|
| Go | 1.27.2 (see `go.mod`) | server, tests |
| Node.js + npm | 22 (CI) | rebuilding the web app (optional) |
| just | current | the root `justfile` recipes below |

The built web app is not committed; CI builds it, and `just restart` builds it
locally into `web/build`.

## Quick start

Run from the repository root:

```bash
just restart       # regenerate schema, build web + binary, serve on :8181
just stop          # stop the detached server
```

Open http://127.0.0.1:8181/tesla/ to play. `just port=8081 restart` changes the
port.

## Environment

Tesla settings are the `TESLA_*` section of the process environment; the rest of
the process is configured by `DRBRAIN_*` (see the root README). `.env` is read
from the working directory.

| Variable | Default | Purpose |
|----------|---------|---------|
| `TESLA_CONFIG_DIR` | `games/tesla/maps` (root `justfile`; app default `maps`) | Builtin map directory |
| `TESLA_MAX_USER_MAPS` | `200` | Cap on custom maps stored in `platform_maps` |
| `TESLA_ADMIN_API_KEY` | unset | `X-Admin-Key` value for shipped and legacy ownerless maps |
| `TESLA_ALLOW_UNAUTHENTICATED_ADMIN` | `false` | Local-only bypass; only the exact string `true` enables it |
| `TESLA_MCP_ENABLED` | `true` | Mount `/tesla/mcp` |
| `TESLA_ALLOWED_ORIGINS` | all | WebSocket origin allowlist (comma-separated; `*` or empty allows all) |
| `TESLA_GRAPHQL_INTROSPECTION`, `TESLA_GRAPHQL_PLAYGROUND` | `true` | Reported in the mount log; the unified `/graphql` handler owns these switches now |

`TESLA_CUSTOM_MAPS_DIR` is not a runtime setting. Existing custom maps are
imported once with `drbrain import-maps --game=tesla --dir=<legacy dir>`;
imported maps are `PUBLISHED` and ownerless, and an owner claims one with
`claimMap` and its old edit key. `pg_dump` is the only backup for custom maps.
Sessions and accounts live in PostgreSQL (`DRBRAIN_DATABASE_URL`).

Every variable is declared once in `appconfig.Config` and loaded with
[`kelseyhightower/envconfig`](https://github.com/kelseyhightower/envconfig).

## Endpoints

| Path | Purpose |
|------|---------|
| `/tesla/` | Web UI |
| `/graphql` | GraphQL (HTTP + graphql-ws); select the `tesla` namespace |
| `/graphql/playground` | GraphQL playground |
| `/tesla/mcp` | MCP server (Streamable HTTP) — tools in [MCP_TOOLS.md](MCP_TOOLS.md) |
| `/tesla/llms.txt` | Guide for LLM agents |
| `/auth/...`, `/settings`, `/u/<handle>` | Global sign-in and accounts |

The legacy REST `/api/sessions` and `/ws` routes are gone.

## Clients

- **Web UI** — served at `/tesla/`.
- **Claude Code / MCP** — point MCP clients at `http://127.0.0.1:8181/tesla/mcp`; `mcp.json` names the server `tesla-game`.
- **Any GraphQL client** — e.g. [gqlcli](https://github.com/wricardo/gqlcli), `curl`, or `/graphql/playground`.

## 🎲 Game Rules

### Objective
Navigate your Tesla to visit all parks (P) while managing battery life and avoiding obstacles.

### Mechanics
- **Movement**: Each move consumes 1 battery unit
- **Charging**: Restore battery at home tiles (H) or superchargers (S)
- **Obstacles**: Cannot move through water (W) or buildings (B)
- **Victory**: Collect all parks to win
- **Game Over**: Battery depleted with no reachable charging stations

### Grid Legend
- `T` - Tesla (your position)
- `R` - Road (passable)
- `H` - Home (passable, charging station)
- `P` - Park (passable, collectible objective)
- `S` - Supercharger (passable, charging station)
- `W` - Water (impassable obstacle)
- `B` - Building (impassable obstacle)
- `✓` - Visited park

## 📡 GraphQL API Reference

GraphQL endpoint: `http://127.0.0.1:8181/graphql` (namespace `tesla`)  
Interactive playground: `http://127.0.0.1:8181/graphql/playground`  
Subscription WebSocket endpoint: `ws://127.0.0.1:8181/graphql`  
LLM quick guide: `http://127.0.0.1:8181/tesla/llms.txt`

List maps:

```graphql
query {
  tesla {
    maps { mapId name description gridSize maxBattery }
  }
}
```

Create a session:

```graphql
mutation {
  tesla {
    createSession(mapID: "easy") {
      id
      mapName
      gameState { playerPos { x y } battery score message fogRadius nearbyGrid { x y type allowedDirections } }
    }
  }
}
```

Move:

```graphql
mutation Move($sessionID: ID!) {
  tesla {
    move(sessionID: $sessionID, direction: RIGHT) {
      success
      message
      gameState { playerPos { x y } battery score victory gameOver }
    }
  }
}
```

Subscribe to session updates (`teslaSessionUpdated` stays at the operation root):

```graphql
subscription Watch($sessionID: ID!) {
  teslaSessionUpdated(sessionID: $sessionID) {
    playerPos { x y }
    battery
    score
    victory
    gameOver
  }
}
```


See [docs/graphql.md](docs/graphql.md) for the full API reference.

### GraphQL Response Enhancements

The `move` mutation returns:
- `step`: compact summary of the move
  - Fields: `dir`, `from { x y }`, `to { x y }`, `tileChar`, `tileType`, `batteryBefore`, `batteryAfter`, `success`
- `attemptedTo`: present when move metadata is available for the attempted target
  - Fields: `x`, `y`, `tileChar`, `tileType`, `passable`
- `gameState` includes:
  - `nearbyGrid { x y type visited id allowedDirections }`: `(2r+1) x (2r+1)` window around the player with `r = fogRadius` (3x3 without fog). Each cell's `x` / `y` are its map coordinates; off-map cells read `building` with their off-map coordinates.
  - `grid(password:)`: full row-major map (`grid[y][x]`); in fog sessions it requires the grid password, and a wrong/missing password nulls the whole response. A won run (`victory: true`) is revealed without a password until it is reset.
  - `batteryRisk`: `SAFE`, `LOW`, `CAUTION`, `DANGER`, `CRITICAL`, `WARNING` (no charger), or `UNKNOWN`

The `bulkMove` mutation adds:
- Summary fields: `requestedMoves`, `movesExecuted`, `stoppedReason`, `stopReasonCode`, `stoppedOnMove`, `truncated`, `limit`
- Start/end snapshot: `startPos`, `endPos`, `startBattery`, `endBattery`, `scoreDelta`
- `steps`: compact per-step entries for this call only
- `attemptedTo`: failed/attempted target metadata when available
- Decision aids: `possibleMoves` (directions you can legally move from the end position: walls, map edge, one-way roads, battery considered; empty after game over), `batteryRisk`

Notes:
- `totalMoves` is the GraphQL field name for cumulative session moves.
- Bulk responses expose both `requestedMoves` and `movesExecuted` so agents can detect truncation (max 50 moves per call) or blocked routes.
- Cells carry `allowedDirections`; a move is allowed only if its direction is listed in every non-empty `allowedDirections` of the cell left and the cell entered. Wrong-way moves are rejected at no battery cost and do not end the game.


## Maps

Builtin maps live in `games/tesla/maps/*.json` (schema: [map-schema.json](map-schema.json), docs: [docs/config-schema.md](docs/config-schema.md)); they are release-managed files. Custom maps are PostgreSQL `platform_maps` rows (owner, DRAFT/PUBLISHED, hashed legacy edit key), capped by `TESLA_MAX_USER_MAPS`, and backed up with `pg_dump`. List them at runtime with the `maps` GraphQL query (published maps plus the caller's drafts). Validate all builtin maps from the repository root with:

```bash
go run ./cmd/drbrain validate --game=tesla
```

`go run ./games/tesla/cmd/analyze` prints heuristics (size, battery, parks, chargers, reachability) per map.

## Development

From the repository root:

```bash
just restart       # regen + build web/binary + start detached on :8181
just regen         # after any GraphQL schema edit
go test ./...      # all Go tests
go vet ./...
npm --workspace web run check
npm --workspace web run test
```

Frontend hot reload: `npm run dev` serves Vite on :5173 and proxies `/graphql`
(including websockets) to the Go server.

Schema changes start in `graph/schema.graphqls` at the repository root; run
`go generate ./games/... ./graph`. Never edit `games/tesla/graph/schema.graphqls`
or `graph/generated/` by hand.

### Layout

```
game.go              platform adapter (Mount, GraphQLResolver, MCP, validate)
appconfig            TESLA_* environment
game/config          map loading and the PostgreSQL map catalogue
game/engine          game rules (movement, battery, victory)
game/service         GameService facade used by all transports
game/session         session lifecycle (PostgreSQL-backed)
graph/               resolvers + generated SDL/model
transport/mcp        MCP tools
transport/websocket  WebSocket hub
validate/            map winnability checks
cmd/analyze          map heuristics CLI
cmd/envcheck         environment/config checker
cmd/rlenv            reinforcement-learning environment
bruteforcer/         standalone solver (part of the main module)
maps/                map JSON files
docs/                additional docs (docs/archive is historical)
```

The web UI lives in the shared app at `web/src/routes/tesla/` and `web/src/lib/tesla/`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
