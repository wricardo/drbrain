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

Game prefixes are `/mazes`, `/motor`, `/tesla`, `/delivery` and `/music`. Only
tesla mounts a per-game transport (`/tesla/mcp`); every other prefix serves the
SPA and its `llms.txt`. `/music` has **no MCP mount**: the music game exposes no
per-game API surface (`API()` returns nil), so `/music/mcp` is not routed and
404s. That is deliberate for v1 — agents use the GraphQL namespace below.

## GraphQL namespaces

Queries select a namespace; Delivery mutations are flat root fields instead:

```graphql
query {
  account { me { handle } authProviders { id name } }
  delivery { sessions { id } }
  mazes { maps { id } }
  motorProgramming { maps { id } }
  tesla { maps { mapId } }
  music { excerpts { id title } }
}
```

Delivery mutations are flat `deliveryXxx` root fields (root mutation fields run
serially, so aliased mutations keep their order):

```graphql
mutation {
  m0: deliveryMoveCar(id: "…", carId: "car_1", direction: EAST, ownerToken: "…") { id }
  m1: deliveryMoveCar(id: "…", carId: "car_1", direction: EAST, ownerToken: "…") { id }
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
  musicSessionUpdated(sessionID: "…") { session { solved wins } }
}
```

Types are prefixed (`Global_User`, `Delivery_Snapshot`, `Mazes_Map`,
`Motor_Session`, `Tesla_GameState`, `Music_Excerpt`) so the schemas cannot
collide. The shared `Time` scalar is emitted once. The `account` namespace is
global: its types use the `Global_` prefix and live in `platform/account`.

## Removing the old per-game endpoints

`/<game>/graphql` and `/<game>/playground` were removed. A game's GraphQL
handler is no longer built or mounted; each game exposes its resolver root to
the platform, which serves the one executor. `/tesla/mcp` stays where it was;
music has no MCP transport at all (`/music/mcp` is not mounted).

Sign-in moved out of motor: the routes are at `/auth/...` and accounts live in
`platform/account` for every game to use. Sessions are stateless Bearer JWTs
(`Authorization: Bearer <token>`, or the `connection_init` payload on
websockets); there is no session cookie and no `logins` table. An OAuth callback
hands the SPA a one-time `signin_code` that `POST /auth/token` exchanges for a
token.

## Disabling a game

`DRBRAIN_GAMES` (or `--games`) selects which compiled-in games mount at startup.
A disabled game's prefix returns 404 instead of the app shell, and its
namespace answers `game is not enabled`. `/graphql` and the other enabled
namespaces stay up.

```sh
DRBRAIN_GAMES=mazes,motor,tesla,delivery,music   # all five
DRBRAIN_GAMES=mazes,tesla,delivery,music          # motor 404s, its namespace reports disabled
```
