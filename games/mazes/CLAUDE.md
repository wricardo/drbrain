# CLAUDE.md — Mazes

Guidance for Claude Code when working on Mazes inside the `drbrain` monorepo.

Mazes is one game in the shared `drbrain` binary: served at `/mazes/`, reached
through the `mazes` namespace of the single `/graphql` endpoint, with accounts,
sessions and player-created maps in PostgreSQL. The platform owns the listener,
HTTP hardening, the SPA and deployment; this package owns the game. Root
`README.md`, `AGENTS.md` and `justfile` are the authority for platform commands.

# Dr Brain - Mazes

Brand: **Dr Brain** is the family of reimplemented brain games; **Mazes** is this
one. User-facing names live in `web/src/lib/mazes/brand.ts`,
`games/mazes/llms.txt.tmpl` and the player-facing README.

Maze puzzle engine + UI: a tower of levels, the player walks one tile per move
from START (bottom level) to FINISH (top level) using stairs and portals, and
sees only the level they stand on (fog). Read `docs/DEVELOPMENT.md` for rules,
map format and semantics.

## Commands

Run from the repo root unless noted.

```sh
just start                                 # build + serve everything on :8181
just restart                               # stop, regen, rebuild web + binary, start
just test                                  # go test ./...
just test-db                               # Go tests against the dev PostgreSQL
just verify                                # fmt check, vet, lint, tests, schema drift, web
(cd games/mazes && just validate)          # map + reference-solution checks
go test ./games/mazes/...                  # this game's Go tests
go test ./games/mazes/game/engine -run '^TestShippedMapsGolden$'
npm run check && npm test                  # web type check + Vitest (repo root)
cd web && npx vitest run src/lib/mazes/tower.test.ts
BASE_URL=http://127.0.0.1:8181 GAMES=mazes ./deploy/smoke.sh
```

## Public repo, private solutions, unified deploy

- `README.md` is for players; technical docs go here and in
  `docs/DEVELOPMENT.md`. Describe Mazes on its own terms, without referencing
  sibling private projects.
- `games/mazes/solutions/` is gitignored and exists only locally
  (`{"moves":["UP","RIGHT",…]}` per map). Never commit it or place solution
  content in tracked files, prompts or the API. Solution-dependent tests skip via
  `requireSolutions` when it is absent.
- Every push to `main` runs the root `.github/workflows/deploy.yml`: Go and web
  checks, one `linux/arm64` build, release swap and `drbrain.service` restart.
  Production is one systemd unit + one nginx vhost (`deploy/`), path-routing all
  games; there is no per-game binary, port or deploy.

## Package map (dependency direction →)

```
games/mazes/game/engine      pure rules, no deps/IO: MapConfig/NewMap, State.Move, Simulate, Shortest/Reachable, View (fog)
games/mazes/game/service     Session type, GameService facade + impl; MapStore, SessionStore, Broadcaster; coded errors
games/mazes/game/config      MapStore impl: builtin maps/*.json + platform_maps rows (owner, DRAFT/PUBLISHED)
games/mazes/game/session     SessionStore impl: in-memory Manager + StorePersistence (PostgreSQL)
games/mazes/transport/websocket  Hub: per-session fan-out, latest-wins (no raw WS clients)
games/mazes/validate         map checks, finish reachability, reference-solution check (+ cmd/validate)
games/mazes/graph            gqlgen schema/resolvers, converters (fog), subscription forwarder, admin gate
games/mazes/game.go          platform adapter: Name/Prefix, Mount (wiring), Shutdown, llms template, resolver
games/mazes/api              HTTP-layer regression tests (fog, hardening, WS limits)
web/src/routes/mazes/        SvelteKit routes: /mazes, play/[id], multi, sessions, maps, editor, learn
web/src/lib/mazes/           game logic, urql documents, stores, components
graph/schema.graphqls        canonical unified SDL; games/mazes/graph/schema.graphqls is generated
```

`service` defines `Session`; `session` and `websocket` import `service`, never
the reverse (the service talks to them through interfaces).

## Invariants — do not break

- **Engine**: `State.Move` mutates in place; `Clone()` anything leaving the lock.
  A blocked move (void/edge) changes nothing and is not counted. Stairs are
  paired and two-way: `U` at level l (x,y) needs `D` at level l+1 (x,y) and vice
  versa (`NewMap` enforces it). You land on the paired stairs tile; effects never
  chain, so arriving on stairs or a portal hole does not trigger it. A stairs
  tile always triggers when stepped on, so it cannot be crossed like a corridor:
  put stairs at dead ends. Start is on level 0, finish on the top level
  (`NewMap` enforces both). `Simulate` with `withEvents=false` allocates no
  events.
- **Fog**: players never receive more than the current level. The service keeps
  the full map snapshot in `Session.Map`; only `graph/convert.go` turns it into
  the public `Map` (level-0 preview) and `LevelView` (current level). No
  resolver, subscription or mutation may serialize `AdminMap` data into a
  `Session`. `adminMap` is admin-gated.
- **Locking**: live `*service.Session` is guarded by `Lock/Unlock`. Never
  broadcast or do I/O while holding it. Hub/resolvers only see `Clone()`
  snapshots. Don't hold the lock when calling `SessionStore.Persist`.
- **Delivery**: every broadcast carries a full snapshot + monotonically
  increasing `Seq`; the hub drops the *oldest* queued update on a full buffer so
  terminal states are never lost. The subscription resolver emits the current
  snapshot first. A blocked move still bumps `Seq` and broadcasts but is not
  persisted.
- **Map snapshot**: sessions embed their own copy of the map; map edits/deletes
  must never touch existing sessions.
- **Security**: map ids match `^[a-z0-9_-]{1,64}$`; session ids are validated
  before use. Map authoring is account-based (`createMap`/`updateMap`/
  `publishMap`/`deleteMap`/`validateMap` need a signed-in viewer; updates are
  owner-only and reset to DRAFT). Shipped/legacy-ownerless writes and `adminMap`
  need `X-Admin-Key` == `ADMIN_API_KEY` (constant-time) unless
  `ALLOW_UNAUTHENTICATED_ADMIN=true` and no key is set. `max_moves ≤ 10000`;
  direction lists are capped (`service.MaxDirections`). Never add solutions or a
  dry-run/`simulate` query: players learn maps only through real moves and
  `reset`.
- **Origin/limits** belong to `platform/httpserve`: `DRBRAIN_ALLOWED_ORIGINS`,
  body cap, WebSocket frame guards and timeouts. Do not reintroduce per-game
  listeners or origin checks.

## Workflows

- Edit the canonical `graph/schema.graphqls` → `just regen` → update
  `games/mazes/graph/convert.go` / resolvers → mirror the operations in
  `web/src/lib/mazes/queries.ts` + `types.ts` → update
  `games/mazes/llms.txt.tmpl`, `web/src/lib/mazes/prompt.ts` and the query copies
  in `games/mazes/api/hardening_test.go`.
- New map: see `docs/DEVELOPMENT.md` "Adding a map"; `just validate` from
  `games/mazes` must pass.
- Engine change: update the golden shortest-route lengths in
  `game/engine/engine_test.go` (`TestShippedMapsGolden`) and
  `validate/validate_test.go` deliberately, never to make a failure disappear.
- UI change: edit `web/src/lib/mazes` / `web/src/routes/mazes`, then
  `npm run check && npm test && just build-web`; `web/build` is not committed.
- Don't run `go mod tidy` casually — gqlgen rewrites `go.mod`; re-`go get` any
  dropped deps.

## Verification checklist

`just verify`, plus `just validate` in `games/mazes`; then `BASE_URL=http://127.0.0.1:8181
GAMES=mazes ./deploy/smoke.sh` against a running server. For UI changes load
`/mazes/play/<id>` and `/mazes/multi` in a browser and walk a map to the end.
