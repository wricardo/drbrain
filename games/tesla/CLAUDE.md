# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Tesla Road Trip Game — grid-based multi-session game server in Go where players control a Tesla to collect parks while managing battery. Features: per-session configuration, GraphQL API (gqlgen), Streamable HTTP MCP endpoint, WebSocket updates, and SvelteKit frontend.

## Development Commands

Tesla is one game in the Dr Brain monorepo: it shares one binary, one listener,
one GraphQL endpoint and one SvelteKit app with the other games. Run commands
from the repository root.

### Build and Run
```bash
just restart       # regen + build web/binary + start detached on :8181
just stop          # stop the detached server
just regen         # after any GraphQL schema edit
just build         # go build -o tmp/drbrain ./cmd/drbrain
```

Feature gates and paths are `TESLA_*` environment variables (see
`games/tesla/appconfig`); the rest of the process is `DRBRAIN_*`.

**Port conflicts**: the root `justfile` defaults to 8181 and refuses to start if
the port is taken. `just port=9000 restart` changes it. Check first:
`lsof -i :PORT | grep LISTEN`.

### Testing
```bash
go test ./...                          # all Go tests
go test ./games/tesla/...              # tesla only
go test -run TestName ./games/tesla/...
go run ./cmd/drbrain validate --game=tesla
```

### Code Quality
```bash
gofmt -l .
go vet ./...
npm --workspace web run check          # svelte-check
npm --workspace web run test           # Vitest
```

### Frontend (SvelteKit)
The shared app lives in `web/`; Tesla routes are `web/src/routes/tesla/` and
`web/src/lib/tesla/`.

```bash
npm run dev             # Vite on :5173, proxies /graphql (ws included)
npm --workspace web run build   # -> web/build
```

### GraphQL Code Generation
```bash
go generate ./games/... ./graph       # split schema + gqlgen + gentypes
go run ./cmd/split-schema -check
go run ./cmd/merge-schema -check
go run ./cmd/gentypes -check
```
The canonical schema is `graph/schema.graphqls` at the repository root.
`games/tesla/graph/schema.graphqls` and `graph/generated/` are generated; never
edit them by hand.

### MCP Server
MCP is mounted at `/tesla/mcp` (Streamable HTTP); stdio is disabled. Set
`TESLA_MCP_ENABLED=false` to turn it off.

### Config Analysis Tool
```bash
go run ./games/tesla/cmd/analyze   # dimensions, battery, parks, chargers, reachability
```

## Architecture

### Package Structure
```
game.go                  Platform adapter: Mount, GraphQLResolver, MCP, validate
appconfig/               TESLA_* environment
game/config/             Config file loading and the PostgreSQL map catalogue
game/engine/             Core game logic: movement, battery, victory, cell types
game/service/            GameService facade over engine+session
game/session/            Session lifecycle (PostgreSQL-backed)
graph/                   Resolvers, generated SDL/model
  schema.graphqls        Generated from graph/schema.graphqls (do not edit)
  generated/             gqlgen-generated code (do not edit)
transport/websocket/     Hub — broadcasts state updates to session subscribers
transport/mcp/           MCP tools (game_state, move, bulk_move, create_session, …)
validate/                Config winnability checks
cmd/analyze/             CLI: config heuristics analyzer
cmd/envcheck/            Environment/config checker
cmd/rlenv/               Reinforcement-learning environment
bruteforcer/             Solver CLI (part of the main module)
```
The shared web app is `web/src/routes/tesla/` and `web/src/lib/tesla/`; there is
no per-game `frontend/` or committed `static/` directory.

### Key Interfaces
- `game/service.GameService` — primary facade used by GraphQL and MCP
- `game/session.Manager` — session CRUD + PostgreSQL persistence
- `game/engine` — stateless game logic; takes/returns `GameState`

### Data Flow
1. **Session creation**: client → GraphQL `tesla.createSession` → `GameService.CreateSession` → `session.Manager` → `platform_sessions` row
2. **Move**: client → GraphQL mutation (`move`/`bulkMove`) → `GameService.Move` → state saved → WebSocket hub broadcast
3. **MCP**: HTTP `/tesla/mcp` (Streamable HTTP) → MCP tools → same `GameService` interface

### GraphQL Endpoints
- `/graphql` — one executor for every game; select the `tesla` namespace (HTTP + graphql-ws subscriptions on the same path)
- `/graphql/playground` — GraphQL playground (host-level)

### Other Routes
- `GET /tesla/llms.txt` — LLM-readable guide rendered by the platform
- `POST /tesla/mcp` — MCP transport
- `/tesla/...` — the shared SvelteKit app
- Legacy REST `/api/sessions` and `/ws` routes are gone.

### Runtime Security / Feature Gates

Configured via `TESLA_*` environment variables:
- `TESLA_GRAPHQL_INTROSPECTION` / `TESLA_GRAPHQL_PLAYGROUND` — reported in the
  mount log; the unified `/graphql` handler owns the actual switches
- `TESLA_MCP_ENABLED` (default true)
- `TESLA_ALLOWED_ORIGINS` for the WebSocket origin allowlist
- Map creation is owner-gated: `createMap`/`create_map` create a DRAFT owned by
  the signed-in caller; shipped and legacy ownerless maps need `X-Admin-Key`.
- `TESLA_ADMIN_API_KEY` (X-Admin-Key) authorizes built-in/legacy map changes
- `TESLA_ALLOW_UNAUTHENTICATED_ADMIN=true` for local-only admin bypass

### Configuration System

Configs at `maps/*.json`. Grid cell characters:
- `R` = road (passable) — **often hidden between B/W, look carefully**
- `H` = home base (passable, full battery charge)
- `P` = park (objective)
- `S` = supercharger (passable, full battery charge)
- `B` = building (impassable)
- `W` = water (impassable)

Battery: move costs 1, H/S restore to max. Victory: visit all parks.

**⚠️ R is visually similar to B in monospace. Re-parse any "fully blocked" row character by character before concluding it's impassable.**

### Non-Interactive Shell Commands

`cp`, `mv`, `rm` may be aliased with `-i` on this system — always use force flags to avoid hangs:
```bash
cp -f src dst    # not: cp src dst
mv -f src dst
rm -f file       # rm -rf dir
```

### Public URL
Set `DRBRAIN_PUBLIC_URL` to the external base URL; the platform renders it into
`/<game>/llms.txt` when exposing the service behind a tunnel or reverse proxy.

## AI Strategy Guidelines (Gameplay)

When playing the Tesla game, ask: where is the nearest charger/home? How much battery remains? Any walls nearby?

### Navigation
- Map grid character-by-character before planning routes
- Identify obstacle-free rows/columns ("golden corridors") for long-distance travel
- When direct routes are blocked, try perpendicular approaches
- Maintain 3+ battery buffer when far from charging

### Systematic Play
1. **Analyze**: map all parks, chargers, obstacle clusters
2. **Plan**: section-based routes through safe corridors
3. **Execute**: `bulkMove` for known safe paths, single `move` near obstacles
4. **Refine**: when blocked, re-parse that row, try alternative approach angle

### Common Pitfalls
- Assuming a row is fully blocked without character-by-character check
- Depleting battery without a clear path to a charger
- Confusing `R` (road) with `B` (building) — they look similar in monospace

### Map Creation Notes
- `createSession(mapID: "easy_trapped_park")` selects a builtin map by its
  `mapId`; `mapName` is a legacy alias. Always check that the returned session
  id is non-empty.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
