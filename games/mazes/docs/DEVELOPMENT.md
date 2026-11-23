# Development — Mazes

How Mazes is built, how it looks, how to maintain it, and what to reuse when
building another game. Players should start at the [README](../README.md).

Mazes is one game in the `drbrain` monorepo: the shared `drbrain` binary serves
it at `/mazes/`, the unified `/graphql` endpoint exposes the `mazes` namespace,
and PostgreSQL stores accounts, sessions and player-created maps.

- [Design principles](#design-principles)
- [Run it](#run-it)
- [Architecture](#architecture)
- [Game rules and engine semantics](#game-rules-and-engine-semantics)
- [Fog](#fog)
- [Maps and reference solutions](#maps-and-reference-solutions)
- [API for humans and AI agents](#api-for-humans-and-ai-agents)
- [Frontend architecture](#frontend-architecture)
- [UI look and feel](#ui-look-and-feel)
- [Configuration](#configuration)
- [Deployment and hosting](#deployment-and-hosting)
- [Maintaining the project](#maintaining-the-project)
- [Building a game like this](#building-a-game-like-this)

## Design principles

These decisions shape everything else. Keep them when changing the game or
starting a new one.

1. **One API for humans and AI.** The browser UI uses the same public GraphQL
   API an AI agent uses. There are no private endpoints for the UI.
2. **The server is the source of truth.** Moves are applied on the server.
   Clients only display snapshots. Two browsers, or a browser and an agent, can
   play or watch the same session and always agree.
3. **Pure rules engine.** All game rules live in one package with no I/O, no
   clock and no dependencies. It is reused by the validator and live sessions.
4. **Full snapshots, ordered by sequence number.** Every update carries the
   whole session state and a `seq` that only goes up. Clients replace state and
   drop anything older.
5. **Shipped maps are files; everything players create is PostgreSQL.** Maps are
   JSON files in `games/mazes/maps/`; sessions, accounts and player-created maps
   live in `platform_sessions`, `accounts` and `platform_maps`.
6. **Agent-readable by design.** `/mazes/llms.txt` explains the rules and every
   API call. Each game page can copy a ready-made prompt. Reference solutions
   are not in the API, the prompts or the public repo.
7. **Players see one level at a time.** The fog is enforced by the server, not
   by the UI.
8. **Puzzles are verified, not hoped for.** Every map has a reference solution
   that must win, and its shortest route (par) is computed by the engine.

## Run it

From the repository root:

```sh
just db-up             # dev PostgreSQL on :55432 (first run)
just restart           # regen + build web/binary, serve on :8181
just stop              # stop the detached server
just test              # all Go tests
(cd games/mazes && just validate)   # shipped maps + reference solutions
```

Frontend development with hot reload, against a server running on :8181:

```sh
npm run dev            # Vite on :5173, proxies /graphql (ws included)
```

Routes (all under the `/mazes` prefix):

| Path | What it is |
|---|---|
| `/mazes/` | Landing page: what the game is, example map, map picker, recent sessions |
| `/mazes/play/<session>` | Board for the current level, tower diagram, d-pad, stats, AI prompt card |
| `/mazes/multi` | Multi-session watch grid: several live sessions side by side |
| `/mazes/sessions` | All sessions: filter by name/map/status, sort, "mine only" |
| `/mazes/maps` | All maps as cards with level-0 previews |
| `/mazes/editor` | Map editor with level tabs and server-side validation (saving needs a signed-in account; shipped/legacy rows also need the admin key) |
| `/mazes/learn` | Rules, fog, keyboard, how AI plays, a generic AI prompt, tips |
| `/graphql` | The single platform GraphQL endpoint; select the `mazes` namespace |
| `/graphql/playground` | The GraphQL playground |
| `/mazes/llms.txt` | Guide for AI agents, rendered from `llms.txt.tmpl` |

## Architecture

```
games/mazes/game/engine       pure rules, no deps/IO: MapConfig/NewMap, State.Move, Simulate, Shortest, Reachable, View
games/mazes/game/service      Session type, GameService facade + impl; MapStore, SessionStore, Broadcaster; coded errors
games/mazes/game/config       MapStore impl: builtin maps/*.json + platform_maps rows (owner, DRAFT/PUBLISHED)
games/mazes/game/session      SessionStore impl: in-memory Manager + StorePersistence (PostgreSQL)
games/mazes/transport/websocket  Hub: per-session fan-out, latest-wins delivery
games/mazes/validate          map checks, finish reachability, reference-solution checks (+ cmd/validate CLI)
games/mazes/graph             resolvers, converters (fog), subscription forwarder, admin gate
games/mazes/game.go           platform adapter: Name/Prefix, Mount (wiring), Shutdown, llms template, resolver
games/mazes/api               HTTP-layer regression tests (fog, hardening, WS limits)
```

### Request and update flow

```
HTTP /graphql (platform/httpserve + graph handler) → unified resolver → mazes namespace → games/mazes/graph
   → GameService (game/service) → engine (pure rules)
   move: lock → State.Move → seq++ → Clone → unlock → Persist → Broadcast
   → websocket.Hub (8-slot queue per subscriber, drop oldest) → graph subscription forwarder (fogs the snapshot)
   → graphql-ws → web/src/lib/mazes store (keeps highest seq)
```

### Composition and dependencies

- `games/mazes/game.go` is the composition root. `Mount` builds the map store
  (`game/config`), the PostgreSQL session store (`game/session`) and the
  broadcaster (`transport/websocket`), passes them to `service.New(...)`, and
  exposes the resolver through `platform.GraphQLResolver`. On shutdown it
  persists every session.
- `game/service` defines the `MapStore`, `SessionStore` and `Broadcaster`
  interfaces it needs. Implementations import `service` and assert the
  interfaces at compile time; `service` never imports them. This keeps the core
  testable with fakes.
- `graph/` is a thin adapter: resolvers call `GameService` and convert results
  in `graph/convert.go`. `graph/generated` and `graph/model` are gqlgen output
  (committed, never hand-edited); the game SDL is generated from the canonical
  `graph/schema.graphqls`.

### Concurrency rules

- Each live `*service.Session` has its own lock. A mutating service method
  follows one pattern: lock → mutate → `touch` (seq++, last-action time) →
  `Clone` → unlock → persist → broadcast the clone.
- Never broadcast or do I/O or call `Persist` while holding a session lock.
- The hub and resolvers only ever see clones.

### Delivery guarantees

- Every broadcast is a full snapshot with a monotonic `seq`. The hub ignores
  anything with a `seq` at or below the last one it published.
- Each subscriber has a small buffer. When it is full the hub drops the *oldest*
  queued update, so the final WON/LOST frame always arrives.
- The subscription resolver subscribes first, then emits the current snapshot,
  then only newer updates.
- A blocked move (void or edge) bumps `seq`, sets `lastEvent.blocked` and is
  broadcast, but is not persisted: nothing durable changed.

### Persistence

- Sessions are written to `platform_sessions` on create, every counted move
  (once per `bulkMove`), reset, rename and graceful shutdown.
- On load, each session is validated: the map snapshot is rebuilt with
  `engine.NewMap` and the stored `path` is replayed against it (adjacency,
  stairs and portal landings, position, move count, status).
- Each session embeds a copy of its map, so editing or deleting a map never
  affects existing sessions.
- Idle sessions older than `SESSION_TTL` are removed on startup.
  `MAZES_MAX_SESSIONS` caps how many live sessions exist.

### Errors

`*service.Error{Code, Msg}` with codes `NOT_FOUND`, `INVALID_ARGUMENT`,
`SESSION_TERMINAL`, `FORBIDDEN`, `LIMIT_REACHED`, `UNAUTHENTICATED`. Resolvers
expose them as `extensions.code`; unknown errors are logged and returned as
`INTERNAL`. Looking up a missing map or session returns `null`, not an error.
Check codes with `errors.As`. Moving in a finished session is `SESSION_TERMINAL`;
a blocked move is not an error.

### Security and hardening

- Map ids match `^[a-z0-9_-]{1,64}$` because they are file names and
  `platform_maps` keys. Session ids are validated before use.
- Map authoring is account-based: `createMap`/`updateMap`/`publishMap`/
  `deleteMap`/`validateMap` need a signed-in viewer (`UNAUTHENTICATED`
  otherwise). `createMap` stores a DRAFT owned by the caller; `updateMap` is
  owner-only and resets the map to DRAFT; `publishMap` is owner-only and
  requires the finish to be reachable; `deleteMap` is owner-only for the
  caller's own map. Edits never touch running sessions.
- `X-Admin-Key` (equal to `ADMIN_API_KEY`, compared through SHA-256 digests in
  constant time) is required for the `adminMap` query and for shipped or
  legacy-ownerless map writes. `ALLOW_UNAUTHENTICATED_ADMIN=true` is honoured
  only when no key is set (local development). `adminMap` checks the key before
  looking the id up.
- `max_moves ≤ 10000`. `bulkMove` accepts at most `service.MaxDirections` (20000)
  directions per call.
- Request body caps, query-complexity cap (1000), WebSocket frame guards, init
  timeouts and origin checks live in the platform (`graph/handler.go`,
  `graph/complexity.go`, `platform/httpserve`, `platform/config`);
  `DRBRAIN_ALLOWED_ORIGINS` also drives CORS.
- Map creation is atomic (`MapStore.Create`; a conflict is
  `INVALID_ARGUMENT`). Never check-then-save.

## Game rules and engine semantics

Moves: `UP`, `RIGHT`, `DOWN`, `LEFT`, one tile each.

- Coordinates: `x` = column, `y` = row, row 0 is the top, **y grows downward**.
  Level 0 is the bottom of the tower; `U` goes to level + 1.
- A move onto void or off the grid is **blocked**: nothing changes and it is
  **not counted**. Every other move is one counted move.
- Tiles: `.` void, `o` stone, `S` start, `F` finish, `U` stairs up, `D` stairs
  down, `1`-`9` portal holes.
- Stepping onto `U`/`D` moves you at once to the same x,y one level up/down
  (`via` = `STAIRS_UP`/`STAIRS_DOWN`). Stairs are **paired and two-way**: a `U`
  at level l (x,y) needs a `D` at level l+1 (x,y) and a `D` needs a `U` one level
  below (`NewMap` enforces it). You land on the paired stairs tile. Stepping onto
  a portal hole moves you to its twin (`via` = `PORTAL`), always on the same
  level. Effects never chain: arriving on stairs or on a hole does not trigger
  it, so step off and back on to use it again. A stairs tile always triggers when
  stepped on, so it can't be walked across like a corridor; put stairs at dead
  ends.
- `State.Path` lists every tile entered, starting with the start tile. A stairs
  or portal move adds the tile stepped on and the landing tile.
- After each counted move: standing on `F` → **WON** (beats the move limit on
  the same move); otherwise `moves >= max_moves` → **LOST/MOVE_LIMIT**.
- Status: `READY` (no counted move yet), `PLAYING`, `WON`, `LOST`. Moving after
  `WON`/`LOST` is `ErrTerminal`.
- `Map.Par` is the shortest winning route by BFS over positions (every counted
  move costs 1; the move limit is ignored). `-1` means the finish is
  unreachable.
- `Map.ForcedDescents` is the fewest `D` stairs any winning route takes (BFS
  layered by descents; `-1` if unreachable). `MinLevelChanges()` is derived:
  every descent needs one extra climb, so it is
  `levels - 1 + 2*ForcedDescents`. Both are exposed on `Map`, `AdminMap` and
  `validateMap`, and printed by the validator (`just validate` in
  `games/mazes`). Every shipped map has 0
  forced descents (asserted in `TestShippedMapsGolden`).

Engine rules for code: `State.Move` mutates in place; `Clone()` anything that
leaves a lock. `Simulate(m, dirs, false)` allocates no events, so validating
reference solutions stays cheap.

### Sessions

A session is one play of one map. `move` and `bulkMove` apply directions;
session-scoped operations accept the owner token returned by `createSession`.
`bulkMove` stops when the game ends and, by default (`stopOnBlocked: true`), at
the first blocked attempt; `stopOnBlocked: false` skips blocked moves and
carries on. `reset` returns to the start and keeps `attempts` (finished games)
and `bestMoves` (fewest moves in a won game). Every update has a `seq`; clients
drop updates with `seq <=` the last seen.

## Fog

Players only ever see the level they stand on. The server enforces it:

- The service keeps the full map in each session. Only `graph/convert.go`
  decides what leaves the server.
- `Session.map` is the public catalog entry: name, size, `levelCount`,
  `maxMoves`, `parMoves` and `preview` (the rows of level 0, where every game
  starts). It has no other level's layout, no start/finish coordinates and no
  portals.
- `Session.view` is the current level: its rows and the portal holes on it, each
  with its twin's location (`b`) since both ends of a portal are always on the
  same level.
- `NewMap` guarantees start on level 0 and finish on the top level, so
  `levelCount - 1` is always the goal level.
- `adminMap` returns the full layout and is admin-only; the editor uses it.
- There is no dry-run query: players learn a map only through real moves in a
  session (`move`, `bulkMove`, `reset`).
- `state.path` and `lastEvent` carry positions on levels the player has already
  visited. That is the player's own history, not hidden information.

`games/mazes/api/fog_test.go` runs a session through every query, mutation and
the subscription and asserts no other level's rows ever appear.

## Maps and reference solutions

```json
{
  "id": "two_floors", "name": "Two Floors", "difficulty": "easy",
  "levels": [
    ["Sooo..", "...o..", ".ooooU", ".o....", ".oo...", ".o...."],
    ["F.....", "o.....", "o..ooo", "o..o..", "oooo..", ".D...."]
  ],
  "max_moves": 100
}
```

`levels[0]` is the bottom level. All levels have the same size (3–20 per side),
1–12 levels. Glyphs: `.` void, `o` stone, `S` start (exactly one, on level 0),
`F` finish (exactly one, on the top level), `U`/`D` stairs (paired: a `U` needs a
`D` at the same x,y one level up and a `D` needs a `U` one level down), `1`-`9`
portal holes (each digit used appears exactly twice, on the same level).
`max_moves` defaults to 500 (max 10000). JSON Schema: `map-schema.json`.

Shipped maps (16), with their par:

| map | levels | idea | par |
|---|---|---|---|
| `first_steps` | 1 | a winding path with a dead-end branch | 9 |
| `ladder` | 3 | one short corridor and one staircase per level | 17 |
| `portal_twins` | 1 | three portal pairs chained across void | 11 |
| `pipe_dream` | 1 | portals hop across void | 9 |
| `two_floors` | 2 | two staircases up; one lands in a dead-end pocket | 18 |
| `lost_garden` | 1 | a 13×13 perfect maze, no tricks | 90 |
| `wormholes` | 1 | a 13×13 maze with three portal pairs between dead ends | 78 |
| `four_floors` | 4 | 7×7 corridor mazes, two staircase pairs per gap | 72 |
| `portal_climb` | 3 | one staircase per gap plus two portal pairs | 80 |
| `tower_maze` | 3 | corridor maze per level; staircases at dead ends, most lead somewhere you already visited | 62 |
| `miners_shaft` | 6 | level 2 (index 1) is an 8×8 board of islands joined only by arrows and pipes; the quick ways up are traps, so both pipes are required | 71 |
| `deep_tower` | 5 | five 9×9 corridor mazes, two staircase pairs per gap, two portal pairs | 93 |
| `grand_tower` | 6 | six 9×9 corridor mazes, two staircase pairs per gap, three portal pairs | 112 |
| `switchback` | 4 | 7×7 floors split into islands; two forced descents, a decoy up stair on every floor | 34 |
| `false_summit` | 5 | stairs by the start chain straight up to a pocket beside the visible finish; the real route circles level 0 and drops once | 66 |
| `portal_locks` | 4 | every floor is islands joined only by portals; the nearest stairs are decoys; one forced descent | 46 |

### Designing a good map

- Start from the trick you want to teach (stairs, drops that undo progress,
  portals that skip walls or send you backward).
- Put dead ends where the stairs are and make the wrong stairs look attractive.
- The engine computes par; check it with `just validate` in `games/mazes`.
- Give the map a short `description`. It appears under the board.

### Reference solutions

`games/mazes/solutions/<id>.json` (`{"moves": ["UP", "RIGHT", …]}`). They are
**not** in the public repo (`solutions/` is gitignored), never served by the API,
and never put in prompts or the built web app. Without them the validator
(`just validate` in `games/mazes`) reports missing solutions and the
solution-dependent tests skip
(`requireSolutions` in `validate/validate_test.go`). A shortest route can be
generated with `Map.Shortest()`.

### Adding a map

1. Add `games/mazes/maps/<id>.json` (id = file name).
2. Add `games/mazes/solutions/<id>.json` locally.
3. Run `just validate` from `games/mazes`: it checks structure, finish
   reachability and that the reference solution wins.
4. Add its golden par to `TestShippedMapsGolden`
   (`game/engine/engine_test.go`) and to `validate/validate_test.go`.

Maps can also be created in `/mazes/editor` or with the map mutations by a
signed-in account.

## API for humans and AI agents

The canonical schema is `graph/schema.graphqls`; the game's derived SDL is
`games/mazes/graph/schema.graphqls` (generated — never edit it).

- **Queries** (inside `mazes { … }`): `maps`, `map(id)`, `adminMap(id)`
  (admin), `session(id)`, `sessions(sort, limit, mapId)`,
  `canPlaySession(id, ownerToken)`.
- **Mutations** (inside `mazes { … }`): `createSession`, `renameSession`,
  `deleteSession`, `move`, `bulkMove`, `reset`, plus the map mutations
  (`createMap`, `updateMap`, `publishMap`, `deleteMap`, `validateMap`).
  Session mutations accept the `ownerToken` returned by `createSession`.
- **Subscription** (flat root, not namespaced):
  `mazesSessionUpdated(sessionID)` emits the current snapshot, then every
  change.

Agent support:

- `/mazes/llms.txt` (`games/mazes/llms.txt.tmpl`, embedded in the binary) is the
  full guide for agents: rules, fog, endpoints and a valid example for every
  call. Keep it in sync with the schema; every example must run against the live
  API.
- The **Play with an AI** card on the home and play pages
  (`web/src/lib/mazes/prompt.ts`) builds a prompt for that exact session: id,
  the current level with an x/y ruler, portals on it, the fog explanation and
  ready-to-run GraphQL calls. Never a solution and never other levels.
- `/mazes/learn` has a generic prompt for any map.

## Frontend architecture

- One SvelteKit app for all games, `adapter-static`, built into `web/build`
  (gitignored; CI builds it). The Go binary serves it from
  `DRBRAIN_STATIC_DIR`, with the platform returning `index.html` for unknown
  paths, so a new route under `web/src/routes/mazes/` needs no Go change.
- Routes live in `web/src/routes/mazes/`; game logic and components in
  `web/src/lib/mazes/`.
- Svelte 5 runes only (`$state`, `$derived`, `$effect`, `$props`), `onclick`-style
  attributes, keyed `{#each}`.
- GraphQL: one urql client per game (`web/src/lib/mazes/graphql.ts`) wraps every
  operation in the `mazes` namespace and unwraps it with `mapExchange`, so the
  screen code keeps game-local shapes; the flat `mazesSessionUpdated`
  subscription maps back to `SessionUpdate`. `unwrap()` turns urql errors into
  exceptions; `errors.ts` maps codes to friendly titles and messages.
- Live sessions: the shared `@drbrain/ui/session` store (`createSessionStore`)
  fetches the snapshot, subscribes, and keeps only updates with a higher `seq`.
  The transport is injected from `web/src/lib/mazes/stores/source.ts`, so the
  store is tested with a fake source. Connection state (`connecting`, `live`,
  `reconnecting`, `offline`) drives the badge.
- Moves go straight to the server (`move`); the board renders whatever snapshot
  comes back.
- Logic lives in plain TypeScript modules in `web/src/lib/mazes/` (`tower.ts`,
  `levels.ts`, `fog.ts`, `shortcuts.ts`, `prompt.ts`, `maps.ts`, `sessions.ts`,
  `replay.ts`, `ownerTokens.ts`, `admin.ts`, `time.ts`, `start.ts`), each with
  Vitest tests. Components (`Board`, `Tower`, `Dpad`, `MiniGrid`, `MapCard`,
  `Stats`, `ResultModal`, `StatusLine`, `WatchCell`) stay thin.
- `fog.ts` keeps an in-page memory of levels already seen so the tower diagram
  can offer a read-only peek. It is never sent to or from the server and is
  forgotten on reload.
- GraphQL documents and types are written by hand in `queries.ts` and
  `types.ts`. Update them whenever the schema changes.
- Brand names come from `brand.ts` (`SITE`, `GAME`, `TITLE`, `pageTitle()`);
  don't hard-code them. `localStorage` keys use the `drbrain-mazes.` prefix; the
  admin key is kept in `sessionStorage` only.
- Gotcha: explicit `onchange`/`oninput` handlers run *before* `bind:value`
  updates. Read `event.currentTarget.value`, not the bound variable.

## UI look and feel

The goal: friendly, bright and calm, like a puzzle book. Readable for kids and
adults, clear enough for a screenshot to explain the game. The Dr Brain games
share one look.

### Palette and typography

- One accent family: **indigo**. Page background `#f5f6ff`, text `#1e1b4b`
  (indigo-950). Cards are white with `border-indigo-200`, `rounded-xl`, and at
  most a light `shadow-sm`.
- Primary actions: `bg-indigo-600` → `hover:bg-indigo-700`, white text.
  Secondary: `border-indigo-300`, indigo text, `hover:bg-indigo-50`. Success is
  emerald, failure is rose.
- Secondary text is `text-slate-600` at `text-xs`/`text-sm`. Section labels are
  small uppercase indigo text with wide tracking.
- System font stack. No web fonts. Navigation uses pills: `rounded-full`, the
  active page filled indigo.

### Layout

- Header: logo, **Dr Brain** with **Mazes** next to it (hidden on small
  screens), pill navigation. Footer: one-line tagline plus links to the rules,
  `/mazes/llms.txt` and the GraphQL endpoint.
- Content is centred at `max-w-6xl` with `px-4 sm:px-6`.
- Play page: two columns on large screens (board left; tower diagram, d-pad,
  stats and AI prompt card right), one column on mobile. `multi` reuses the
  board read-only for several sessions.

### The board

- An SVG scaling to the width available, so it stays sharp at any size. Pale
  indigo checkerboard; void cells are empty.
- **Stone tiles:** rounded slate squares. **Start** and **finish** are marked
  (the finish is the cookie).
- **Stairs:** a blue up arrow (▲) or rose down arrow (▼) stamped on the tile.
  **Portals:** coloured rings, one colour per digit (1 blue, 2 yellow, 3 orange,
  4 green, 5 pink, 6 purple, 7 red, 8 cyan, 9 white) with the digit as text. A
  dotted pipe joins each pair since both ends are on the same level.
- **Player:** an indigo arrowhead that glides (120 ms ease-out) and turns toward
  its last move.
- **Trail:** small translucent indigo dots on visited tiles.
- **Bump:** when the last move was blocked, a rose outline flashes on the
  player's tile.
- Moving between levels fades the board to the new level.

### The tower diagram

A white card of stacked ledges, one per level, with **FINISH** at the top and
**START** at the bottom and a "You are here" marker. Only levels the player has
seen are buttons (read-only peek, labelled "remembered"); unexplored ledges are
dashed and show `?`.

### Controls and feedback

- Arrow keys or WASD move; `R` resets; an on-screen d-pad serves touch; clicking
  a neighbouring tile steps there.
- Stats show moves, par, move limit, best and attempts. Errors appear inline as
  rose alert boxes with a friendly title and the error code in small monospace.
- On win or loss: a centred modal ("You won!" with moves versus par, or "Out of
  moves"), focus on **Reset & try again**, Escape closes it. A coloured banner
  stays under the board after the modal is closed.
- A connection badge shows whether live updates are connected.

### Accessibility and motion

- "Skip to content" link, `aria-current` on the active nav pill, labelled
  sections and button groups.
- The board has a text description of the player's position and level; results
  are announced through a polite live region.
- Visible focus everywhere: 2 px indigo outline with offset.
- `prefers-reduced-motion` turns off animations and transitions globally,
  including the glide.
- Colour is never the only signal: stairs have arrows, portals have digits,
  results have text.

## Configuration

Platform variables live in `platform/config`; Mazes reads its own section.
Production values are documented in `deploy/drbrain.env.example`.

| Variable | Default | Purpose |
|---|---|---|
| `DRBRAIN_DATABASE_URL` | required | PostgreSQL for accounts, sessions and `platform_maps` |
| `DRBRAIN_ALLOWED_ORIGINS` | empty (all) | CORS and WebSocket origin checks |
| `DRBRAIN_STATIC_DIR` | `static` | Built SPA directory (`web/build` in dev) |
| `MAZES_MAPS_DIR` | `maps` | Builtin map files (`games/mazes/maps` in the repo) |
| `MAZES_SOLUTIONS_DIR` | `solutions` | Local reference solutions (never deployed) |
| `MAZES_PUBLIC_URL` | empty | Public origin used for `/mazes/llms.txt` |
| `MAZES_MAX_SESSIONS` | `1000` | Cap on live sessions |
| `MAZES_MAX_USER_MAPS` | `200` | Cap on custom maps in `platform_maps` |
| `SESSION_TTL` | `168h` | Idle sessions older than this are removed on startup |
| `MAZES_DEBUG` | `false` | Extra game logging |
| `ADMIN_API_KEY` | empty | Key for `adminMap` and shipped/legacy map writes |
| `ALLOW_UNAUTHENTICATED_ADMIN` | `false` | Local only: admin without a key, when none is set |

Builtin maps are release-managed files in `MAZES_MAPS_DIR`. Custom maps are
PostgreSQL `platform_maps` rows with an owner and DRAFT/PUBLISHED status; there
is no `maps-user` directory. Back up custom maps with `pg_dump`. Mazes had no
player-map directory before the cutover, so there is no legacy import step.

Frontend build-time variables: `PUBLIC_GRAPHQL_URL`, `PUBLIC_WS_URL`. When unset,
the UI uses the page's own host and picks `ws`/`wss` from the page protocol.

## Deployment and hosting

**Continuous deployment.** Every push to `main` runs the root
`.github/workflows/deploy.yml`: Go and web checks, build the web app, build one
`linux/arm64` binary, upload the release, swap `/opt/drbrain/current`, restart
`drbrain.service` and run `deploy/smoke.sh` (`/healthz` plus one namespaced
GraphQL query per game).

**Host layout:** one systemd unit (`deploy/drbrain.service`), one nginx vhost
(`deploy/nginx/`), one PostgreSQL instance (`DRBRAIN_DATABASE_URL`; an optional
`deploy/docker-compose.postgres.yaml`). Builtin maps ship inside
`/opt/drbrain/current/games/mazes/maps`; accounts, sessions and custom maps live
in PostgreSQL; `/var/lib/drbrain` holds only writable file state.

## Maintaining the project

### Common changes

- **Schema change:** edit canonical `graph/schema.graphqls` → `just regen` →
  update `games/mazes/graph/convert.go` and resolvers → mirror in
  `web/src/lib/mazes/{queries,types}.ts` → update `llms.txt.tmpl` and
  `prompt.ts` if agents are affected → update the query copies in
  `games/mazes/api/hardening_test.go`. Anything that reaches a `Session` goes
  through `toSession`; never add layout data there.
- **Engine change:** update the golden pars in `game/engine/engine_test.go` and
  `validate/validate_test.go` on purpose, never just to make a failure go away.
- **UI change:** edit `web/src/routes/mazes` / `web/src/lib/mazes`, then
  `npm run check && npm test && just build-web`. `web/build` is not committed.
- **New page:** add the route under `web/src/routes/mazes/` and the nav entry in
  `+layout.svelte` if needed. No Go routing change.
- **Dependencies:** don't run `go mod tidy` casually. `gqlgen generate` rewrites
  `go.mod`; re-`go get` anything it drops.

### Testing conventions

- Go: standard `testing`, table tests with `t.Run`, `t.Helper`/`t.TempDir`/
  `t.Cleanup`. No `t.Parallel()` (tests use `t.Setenv`). Run with `-race`
  locally.
- Service tests use `newEnv` in `game/service/helpers_test.go` (fake map store,
  spy persistence, recording broadcaster). PostgreSQL-backed tests skip unless
  `TEST_DATABASE_URL` is set.
- Frontend: Vitest + jsdom + Testing Library.

```sh
just test                                        # go test ./...
just test-db                                     # with PostgreSQL
go test ./games/mazes/game/engine -run TestName  # one Go test
(cd games/mazes && just validate)                # maps + reference solutions
just verify                                      # fmt, vet, lint, tests, schema, web
npm run check && npm test                        # web type check + Vitest
cd web && npx vitest run src/lib/mazes/tower.test.ts
BASE_URL=http://127.0.0.1:8181 GAMES=mazes ./deploy/smoke.sh
```

### Before you push

`just verify`, then `just validate` in `games/mazes`; then start the server and
run the smoke test. For UI changes, open `/mazes/play/<id>` and `/mazes/multi`
in a browser and walk a map to the end. Pushing to `main` deploys.

### Repository rules

- `README.md` is for players. Technical content goes here.
- Never commit `games/mazes/solutions/`, and never put solution content in the
  API, prompts or the built web app.
- Docs and code comments describe the game on its own terms, without references
  to sibling private projects.

## Building a game like this

A checklist for the next game in the collection, based on what worked here:

1. **Write the rules as a pure engine first.** State in, step, state out. No
   I/O, no time. Add `Simulate` (run to the end) early; the validator and the
   tests use it.
2. **Pin the rules with golden tests** before building anything else: known move
   lists and their exact lengths and outcomes.
3. **Define the data as files with a JSON Schema** (maps, puzzles, levels).
   Validate on load and in CI. Keep reference solutions private and check them
   with a CLI.
4. **Put one service facade in front of the engine.** It owns sessions and
   locking. Storage and broadcasting are interfaces it defines.
5. **Make every update a full snapshot with a monotonic `seq`**, and make
   subscriptions emit the current snapshot first. This removes a whole class of
   sync bugs.
6. **Decide what players may see and enforce it on the server**, in one
   conversion layer, with end-to-end tests that probe every read path.
7. **Expose everything through one GraphQL schema** (queries, mutations, one
   subscription per live object). Add coded errors, complexity limits, body
   limits and origin checks from day one.
8. **Write `/llms.txt` alongside the schema**, plus a per-session "Play with an
   AI" prompt. Test the examples against the real server.
9. **Build the UI as a static SPA in the shared web app.** Keep logic in tested
   TypeScript modules, reuse the brand constants, and follow the look and feel
   above so games in the collection feel related.
10. **Ship as one binary + data folders** behind nginx with TLS, deployed by the
    root GitHub Action on push to `main`.
11. **Make the README about playing**, with a section on letting an AI play.
    Keep developer material in documents like this one.
