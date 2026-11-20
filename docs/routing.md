# Routing

One listener, one port, one GraphQL API, path-based routing for everything
else. The platform mounts the unified GraphQL surface at the root and each
game's non-GraphQL API under its prefix; everything else falls through to the
single SvelteKit app.

| path | owner |
|---|---|
| `/graphql`, `/graphql/` | platform: one gqlgen executor, namespaced per game |
| `/graphql/playground` | platform: GraphQL playground |
| `/auth/...` | platform: global sign-in routes (OAuth callbacks, dev provider) |
| `/<game>/mcp` | that game's MCP transport (where it has one) |
| `/<game>/llms.txt` | platform, rendered from the game's template |
| `/<game>/...` | the SvelteKit app |
| `/settings`, `/u/<handle>` | global account settings and public profiles |

## GraphQL namespaces

Queries and mutations select a namespace:

```graphql
query {
  account { me { handle } authProviders { id name } }
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

Types are prefixed (`Global_User`, `Delivery_Snapshot`, `Mazes_Map`,
`Motor_Session`, `Tesla_GameState`) so the schemas cannot collide. The shared
`Time` scalar is emitted once. The `account` namespace is global: its types use
the `Global_` prefix and live in `platform/account`.

## Removing the old per-game endpoints

`/<game>/graphql` and `/<game>/playground` were removed. A game's GraphQL
handler is no longer built or mounted; each game exposes its resolver root to
the platform, which serves the one executor. `/tesla/mcp` stays where it was.

Sign-in moved out of motor: the routes are at `/auth/...`, the cookie is
`drbrain_session` with `Path=/`, and accounts live in `platform/account` for
every game to use.

## Disabling a game

`DRBRAIN_GAMES` (or `--games`) selects which compiled-in games mount at startup.
A disabled game's prefix returns 404 instead of the app shell, and its
namespace answers `game is not enabled`. `/graphql` and the other enabled
namespaces stay up.

```sh
DRBRAIN_GAMES=mazes,motor,tesla,delivery   # all four
DRBRAIN_GAMES=mazes,tesla,delivery          # motor 404s, its namespace reports disabled
```
