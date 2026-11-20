# Routing

One listener, one port, one GraphQL API, path-based routing for everything
else. The platform mounts the unified GraphQL surface at the root and each
game's non-GraphQL API under its prefix; everything else falls through to the
single SvelteKit app.

| path | owner |
|---|---|
| `/graphql`, `/graphql/` | platform: one gqlgen executor, namespaced per game |
| `/graphql/playground` | platform: GraphQL playground |
| `/<game>/mcp` | that game's MCP transport (where it has one) |
| `/<game>/llms.txt` | platform, rendered from the game's template |
| `/<game>/auth/...` | motor sign-in routes |
| `/<game>/...` | the SvelteKit app |

## GraphQL namespaces

Queries and mutations select a namespace:

```graphql
query {
  delivery { sessions { id } }
  mazes { maps { id } }
  motorProgramming { maps { id } }
  tesla { maps { mapId } }
}
```

Subscriptions are flat prefixed root fields, because GraphQL only permits a
stream at the operation root:

```graphql
subscription {
  deliverySession(id: "…") { id tick }
  mazesSessionUpdated(sessionID: "…") { seq }
  motorProgrammingSessionUpdated(sessionID: "…") { seq }
  teslaSessionUpdated(sessionID: "…") { battery }
  teslaLobbyUpdated { mapName }
}
```

Types are prefixed (`Delivery_Snapshot`, `Mazes_Map`, `Motor_Session`,
`Tesla_GameState`) so the four schemas cannot collide. The shared `Time` scalar
is emitted once.

## Removing the old per-game endpoints

`/<game>/graphql` and `/<game>/playground` were removed. A game's GraphQL
handler is no longer built or mounted; each game exposes its resolver root to
the platform, which serves the one executor. `/motor/auth/...` and
`/tesla/mcp` stay where they were.

Motor's login cookie is now origin-scoped (`Path=/`) so `/graphql` can read it;
the cookie name (`motor_session`) still distinguishes it from other games.

## Disabling a game

`DRBRAIN_GAMES` (or `--games`) selects which compiled-in games mount at startup.
A disabled game's prefix returns 404 instead of the app shell, and its
namespace answers `game is not enabled`. `/graphql` and the other enabled
namespaces stay up.

```sh
DRBRAIN_GAMES=mazes,motor,tesla,delivery   # all four
DRBRAIN_GAMES=mazes,tesla,delivery          # motor 404s, its namespace reports disabled
```
