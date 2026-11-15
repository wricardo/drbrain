# Development

How Motor Programming is built, how it looks, how to maintain it, and how to build another game like it. Players should start at the [README](../README.md).

- [Design principles](#design-principles)
- [Run it](#run-it)
- [Architecture](#architecture)
- [Game rules and engine semantics](#game-rules-and-engine-semantics)
- [Maps and reference solutions](#maps-and-reference-solutions)
- [API for humans and AI agents](#api-for-humans-and-ai-agents)
- [Leaderboard](#leaderboard)
- [Frontend architecture](#frontend-architecture)
- [UI look and feel](#ui-look-and-feel)
- [Configuration](#configuration)
- [Deployment and hosting](#deployment-and-hosting)
- [Maintaining the project](#maintaining-the-project)
- [Building a game like this](#building-a-game-like-this)

## Design principles

These decisions shape everything else. Keep them when changing the game or starting a new one.

1. **One API for humans and AI.** The browser UI uses the same public GraphQL API an AI agent uses. There are no private endpoints for the UI. If a person can do something, an agent can too.
2. **The server is the source of truth.** Programs run on the server, one step per tick. Clients only display snapshots. Two browsers, or a browser and an agent, can play or watch the same session and always agree.
3. **Pure rules engine.** All game rules live in one package with no I/O, no clock and no dependencies. It can be tested exhaustively and reused by the validator and the live runner.
4. **Full snapshots, ordered by sequence number.** Every update carries the whole session state and a `seq` that only goes up. Clients never patch state; they replace it and drop anything older. Dropped or reordered messages can't corrupt the view.
5. **Files, not a database.** Maps are JSON files in `maps/` (shipped) and `maps-user/` (player-created); each session is one JSON file in `sessions/`. Writes are atomic (temp file + fsync + rename). One binary plus three folders is the whole deployment.
6. **Agent-readable by design.** `/llms.txt` explains the rules and every API call for an AI. Each game page can copy a ready-made prompt. Answers are never exposed: reference solutions are not in the API, the prompts or the public repo.
7. **Puzzles are verified, not hoped for.** Every map has a reference solution that must win, and the harder maps come with a proof that the intended trick is required.

## Run it

```sh
cp .env.example .env      # optional
make run                  # builds and serves http://localhost:8000 (API + UI)
make dev                  # go run . -port 8000 -debug
```

Frontend development with hot reload, against a server running on :8000:

```sh
cd frontend && npm run dev:local     # http://localhost:5173
```

Server flags: `-port 8000 -host 0.0.0.0 -maps-dir maps -sessions-dir sessions -solutions-dir solutions -debug -public-url <url>` (`-public-url` sets the base URL written into `/llms.txt`; defaults to the request host).

Routes:

| Path | What it is |
|---|---|
| `/` | Landing page: short hero, Continue card (this browser's last unfinished session), first 3 maps, recent started sessions |
| `/play/<session>` | Board, status line, stats, run controls, program editor, call stack, help, AI prompt card, result modal |
| `/watch/<session>` | Read-only live view of any session: status line, stats, Take over, tapes, AI prompt card |
| `/sessions` | All sessions: status chips, search (name/id/map), map filter, sort, Load more, select rows to watch together; auto-refreshes every 10 s |
| `/multi?ids=a,b` | Several sessions as compact live boards (from "Watch selected" on `/sessions`) |
| `/maps` | All maps as cards: search and difficulty chips |
| `/leaderboard?map=<id>&order=slots` or `steps` | Per-map ranking of winners: map picker, two boards, your entries highlighted |
| `/editor` | Map editor with server-side validation; anyone can create a map, changing a built-in one needs the admin key |
| `/learn` | How it works: the game, instructions, rules, controls, how an AI plays, a generic AI prompt, tips |
| `/graphql` | GraphQL over HTTP and WebSocket (`graphql-ws`); `/playground` when enabled |
| `/llms.txt` | Guide for AI agents, rendered from `llms.txt.tmpl` |

## Architecture

```
game/engine          pure rules, no deps/IO: MapConfig/NewMap, Program, VMState (Settle/Step), Simulate, Run
game/service         Session type, GameService facade + impl + Runner (ticker goroutine per playing session);
                     interfaces MapStore, SessionStore, Broadcaster; coded errors; leaderboard (recorded wins + ranking)
game/config          MapStore impl: maps/*.json (shipped, admin-only writes) + maps-user/*.json
                     (player-created), RWMutex, atomic writes
game/session         SessionStore impl: in-memory Manager + FilePersistence (sessions/<id>.json)
transport/websocket  Hub: per-session fan-out, latest-wins delivery
validate             map checks, reachability, reference-solution checks (+ cmd/validate CLI)
graph                gqlgen schema, resolvers, converters, subscription forwarder, admin gate
                     (only for writes to built-in maps)
api                  HTTP mux, /graphql, /llms.txt, SPA serving, hardening (limits, CORS, origin checks)
main.go              flags, .env, wiring, graceful shutdown
frontend/            SvelteKit (Svelte 5) + Tailwind v4, adapter-static → static/ (committed)
```

### Request and update flow

```
HTTP /graphql (api) → gqlgen resolver (graph) → GameService (game/service)
   → engine (pure rules)
   run: per-session Runner ticks → advance one step → seq++ → Clone → unlock → Persist → Broadcast
   → websocket.Hub (8-slot queue per subscriber, drop oldest) → graph subscription forwarder
   → graphql-ws → frontend session store (keeps highest seq)
```

### Composition and dependencies

- `main.go` is the composition root. It builds the map store (`game/config`), the session store and file persistence (`game/session`) and the broadcaster (`transport/websocket`), passes them to `service.New(...)`, then starts `api.NewServer`. On shutdown it stops HTTP first, then the runners, persisting every session.
- `game/service` defines the `MapStore`, `SessionStore` and `Broadcaster` interfaces it needs. Implementations import `service` and assert the interfaces at compile time; `service` never imports them. This keeps the core testable with fakes.
- `graph/` is a thin adapter: resolvers call `GameService` and convert results in `graph/convert.go`. `graph/generated` and `graph/model` are gqlgen output (committed, never hand-edited).

### Concurrency rules

- Each live `*service.Session` has its own lock. A mutating service method follows one pattern: lock → mutate → `touch` (seq++, last-action time) → `Clone` → unlock → persist → broadcast the clone.
- Never broadcast, do I/O, call `Persist` or wait on a runner while holding a session lock. Lock order is session → runner mutex.
- The hub and resolvers only ever see clones.
- `Playing` is true exactly when a runner is registered. Every stop path (pause, reset, delete, win/loss, shutdown) goes through one `detach` function.

### Delivery guarantees

- Every broadcast is a full snapshot with a monotonic `seq`. The hub ignores anything with a `seq` at or below the last one it published for that session.
- Each subscriber has a small buffer. When it is full the hub drops the *oldest* queued update, so the final WON/LOST frame always arrives, even for a slow client.
- The subscription resolver subscribes first, then emits the current snapshot, then only newer updates. A new watcher sees the current state immediately and misses nothing.

### Persistence

- Sessions are written on create, set program, pause, reset, win/loss, rename and graceful shutdown. Not on every tick.
- After a restart, sessions load with `playing=false` and their program and VM state intact.
- Each session embeds a copy of its map, so editing or deleting a map never affects existing sessions.
- A session file also carries its best wins for the [leaderboard](#leaderboard) (`fewest_slots`, `fewest_steps`). Files written before leaderboards still load: a session whose VM is `WON` gets its win recovered from the program stored with it.
- Idle sessions older than `SESSION_TTL` are removed on startup. `MAX_SESSIONS` caps how many exist.

### Errors

`*service.Error{Code, Msg}` with codes `NOT_FOUND`, `INVALID_PROGRAM`, `INVALID_ARGUMENT`, `SESSION_PLAYING`, `SESSION_TERMINAL`, `FORBIDDEN`, `LIMIT_REACHED`. Resolvers expose them as `extensions.code`; unknown errors are logged and returned as `INTERNAL`. Looking up a missing map or session returns `null`, not an error. Check codes with `errors.As`.

### Security and hardening

- Map ids match `^[a-z0-9_-]{1,64}$` because they are file names. Session ids are 16 lowercase hex characters and are validated before touching disk.
- `createMap` and `validateMap` are open to everyone: creating a map is how players add content, and an existing id is `INVALID_ARGUMENT`, so nothing can be overwritten that way.
- `updateMap` and `deleteMap` are open for player-created maps and require `X-Admin-Key` equal to `ADMIN_API_KEY` for built-in ones (shipped in `maps/`), compared through SHA-256 digests in constant time. `ALLOW_UNAUTHENTICATED_ADMIN=true` is honoured only when no key is set (local development). Provenance is `Map.builtin`, resolved from the map store so it also answers for session snapshots.
- New maps are stored in `maps-user/` (survives deploys); `maps/` is only written by an admin editing a built-in map. `MAX_USER_MAPS` caps how many player maps may exist.
- A corrupt player map file is logged and skipped at startup; a corrupt or misnamed built-in map is fatal.
- `max_steps ≤ 10000` and `max_call_depth ≤ 64` bound the cost of a run.
- Request body capped at 1 MiB; query complexity capped at 1000 with weighted costs (`sessions` by `limit`). `api/hardening_test.go` keeps a copy of the frontend queries to make sure they stay under the cap.
- WebSocket: 10 s init timeout, frame-size guard (`api/wslimit.go`), origin check against `ALLOWED_ORIGINS`, which also drives CORS on `/graphql`.
- Map creation is atomic (`MapStore.Create`; a conflict is `INVALID_ARGUMENT`). Never check-then-save.

## Game rules and engine semantics

Instructions: `TURN_LEFT`, `TURN_RIGHT`, `MOVE_FORWARD`, `CALL_SUB_1..3`, `EMPTY`.

- `EMPTY` slots are skipped and don't count. Every other executed instruction is one **step**, including calls.
- Moving into a rock or off the grid does nothing but still counts as a step.
- Coordinates: `x` = column, `y` = row, row 0 is the top, **y grows downward**. Facing: up (y-1), right, down, left.
- Call stack: `CallStack[0]` is main; the top frame is the running tape; `PC` is the next instruction on that tape. Depth = `len(CallStack) - 1`. No tail-call elimination. When a sub tape ends, control returns to the caller.
- Checks after each step, in order: all treats collected → **WON** (beats the step limit on the same step); `steps >= max_steps` → **LOST/STEP_LIMIT**; depth `> max_call_depth` → **LOST/CALL_DEPTH**; main tape finished → **LOST/PROGRAM_ENDED** (reported on the step that ran the last real instruction).
- `allow_recursion: false`: calling a sub that is already running or anywhere on the call stack does nothing (still a step). Neither self-calls nor mutual recursion are possible. Such a step is reported as `callBlocked: true` (distinct from `blocked`, which is a move into a rock or the edge) so clients can explain the spent step.

Engine rules for code: call `vm.Settle(&program)` after `NewVMState` and after any program change. `Step` mutates in place; `Clone()` anything that leaves a lock. `Run` and the internal `step(…, nil)` allocate no events, so bulk simulation stays cheap.

### Sessions

A session is one run of one map. `setProgram` (rejected while running) resets the VM. `run(speedMs 50..5000)` starts the server-side ticker; `step`, `pause` and `reset` do what they say. Every update has a `seq`; clients drop updates with `seq <=` the last seen.

## Maps and reference solutions

```json
{
  "id": "symmetric_paths", "name": "Level 4 - Symmetric Paths", "difficulty": "easy",
  "layout": ["..........", ".*.......*", ".....#....", "..........", "..........", "..#..^..#.", "..........", "..........", ".....#....", ".*.......*"],
  "main_tape_length": 13, "sub_tape_lengths": [6, 6, 6],
  "max_steps": 200, "max_call_depth": 16, "allow_recursion": false
}
```

Glyphs: `.` empty, `#` rock, `*` treat, `^ > v <` start and facing (exactly one). Grid 3–20 per side, at least one treat, main tape 1–32, 0–3 subs of 1–16 each. Defaults when omitted: main 10, subs `[10]`, max_steps 200, max_call_depth 16, recursion off. `sub_tape_lengths: []` (no subs) is different from omitting it, so the field has no `omitempty` and `[]` must never become `nil`. JSON Schema: `map-schema.json`.

Shipped maps (11): five introductory levels (`straight_line`, `turn_challenge`, `rock_obstacle`, `symmetric_paths`, `zigzag_path`), plus puzzles whose tapes are tight enough that the intended solution is *provably* required:

| map | idea | why it's forced |
|---|---|---|
| `long_corridor`, `staircase` | recursion | non-recursive programs execute fewer forward moves than the treat distance |
| `serpentine` | mirrored row + U-turn subs that share a row sub | 61 actions needed; main-only sub calls run at most 7 × 7 = 49 |
| `powers` | each sub triples the one below (S1=2, S2=6, S3=18 moves) | 19 moves needed; two nesting levels reach at most 18 |
| `pinwheel` | one out-and-back unit repeated per arm, each ending facing the next arm | 35 actions needed; one nesting level runs at most 4 × 5 = 20 |
| `bump_spiral` | recursion; walls absorb overshoot, so one pattern follows every side | 56 actions needed; without recursion at most 3 × 10 = 30 |

### Designing a good map

- Start from the trick you want to teach (repetition, nesting, recursion), then size the tapes so the brute-force route doesn't fit.
- Prove it: count the actions the route needs and the most a program without the trick can execute (as in the table). Add that bound to `validate/validate_test.go`.
- For recursion maps, the reference solution must lose when recursion is turned off; `make validate` checks this.
- Give the map a short `description`. It appears under the board.

### Reference solutions

`solutions/<id>.json` (`{"main": [...], "subs": [[...]]}`, exact tape lengths). They are **not** in the public repo (`solutions/` is gitignored), never served by the API, and never put in prompts or `static/`. Without them `make validate` reports missing solutions and the solution-dependent tests skip (`requireSolutions` in `validate/validate_test.go`).

### Adding a map

1. Add `maps/<id>.json` (id = file name).
2. Add `solutions/<id>.json` locally.
3. Run `make validate`: it checks structure, that every treat is reachable, and that the reference solution wins.
4. Add its golden step count to the table in `validate/validate_test.go`.

Maps can also be created by anyone in `/editor` or with the `createMap` mutation. Built-in maps (`maps/*.json`) can only be changed or deleted with the admin key; a new map lands in `maps-user/<id>.json` and is playable immediately.

## API for humans and AI agents

The schema lives in `graph/schema.graphqls`.

- **Queries:** `maps`, `map(id)`, `session(id)`, `sessions(sort, limit, mapId)`, `leaderboard(mapId, order, limit, sessionId)`.
- **Mutations:** `createSession`, `renameSession`, `deleteSession`, `setProgram`, `run`, `step`, `pause`, `reset`, plus `createMap` and `validateMap` (open) and `updateMap`/`deleteMap` (open for player maps, admin key for built-in ones).
- **Subscription:** `sessionUpdated(sessionID)` emits the current snapshot, then every change.

Agent support:

- `/llms.txt` (template `llms.txt.tmpl`, embedded in the binary) is the full guide for agents: rules, endpoints and a valid example for every call. Keep it in sync with the schema; every example must run against the live API.
- The **Play with an AI** card on `/play` and `/watch` (`frontend/src/lib/prompt.ts`) builds a prompt for that exact session: id, map layout with an x/y ruler, tape lengths, recursion rule and ready-to-run GraphQL calls. Never a solution.
- `/learn` has a generic prompt for any map.

## Leaderboard

Every map ranks the sessions that won it. The point of the game is fitting a route into few slots, so the main board counts those.

**What is ranked.** For a winning run: *slots* = non-`EMPTY` instructions across the main tape and every sub tape of the program that won (`Program.SlotsUsed`), and *steps* = `vm.steps` at the win. Two boards per map, chosen with `LeaderboardOrder`:

| Order | Sorted by |
|---|---|
| `SLOTS` (default) | fewest slots, then fewest steps, then earliest |
| `STEPS` | fewest steps, then fewest slots, then earliest |

Entries that tie on both numbers share a rank (1, 2, 2, 4); the earlier win is listed first.

**Trust.** Nothing is submitted. `recordOutcome` (`game/service/impl.go`) calls `Session.recordWin` under the session lock the moment a run is won, from the session's own program and VM, so a client cannot choose its numbers. A client can only store a program and let the engine run it, as always.

**One entry per session.** A session keeps its best win under each ordering (`Session.FewestSlots`, `Session.FewestSteps`). A later, worse win never replaces it, a better one (smaller program or fewer steps) does, and editing or clearing the program afterwards does not remove it. Both are persisted with the session. A player who wants several entries can start several sessions: there are no accounts, so there is no player identity to dedupe on, and a display name is not one (anyone could type someone else's). The board is a list of *sessions*, and each row links to `/watch/<sessionId>`.

**Derived, not stored separately.** `GetLeaderboard` scans the live sessions (at most `MAX_SESSIONS`, 1000 by default), reads each one's recorded win and sorts. There is no second store to keep in sync: deleting a session or the TTL sweep takes its entry with it, and a restart reloads everything from the session files. It holds each session lock only to copy a few small values and does no I/O. If the session cap ever grows by orders of magnitude, add an in-memory index maintained from `recordOutcome`.

**Only the current rules count.** A session embeds a snapshot of its map, and player maps can be edited by anyone, so wins on an older version are not comparable with the current puzzle. `Map.Fingerprint` hashes everything that changes difficulty (layout, tape lengths, `max_steps`, `max_call_depth`, the recursion rule; not name, description or difficulty tag), and only sessions whose snapshot matches the map's current fingerprint are ranked. Editing a map's rules therefore starts a fresh board; renaming it does not. A deleted map has no board (`leaderboard` returns null).

**What is exposed.** Rank, session id, display name, slots, steps and when. Never the program, so reading a board cannot reveal a route (spectating a session already shows its program, as before; reference solutions stay private). Names go through `PublicName`: control, bidi and zero-width characters become spaces, whitespace collapses, the result is cut to `MaxDisplayName` and empty means `Anonymous`. The UI escapes names like any text.

**API.** `leaderboard(mapId, order = SLOTS, limit = 20, sessionId)` returns `{ mapId, order, total, entries, yours }`. `limit` is clamped to 1..100 (negative is `INVALID_ARGUMENT`). `total` counts every ranked session. `yours` is the entry of `sessionId` even outside the returned rows (`null` if it has not won). An unknown map is `null`. Each call scans every session, so it has a flat complexity cost plus a per-row cost scaled by `limit` (`complexityRoot` in `api/server.go`), like `sessions`.

**Frontend.** `/leaderboard` (`routes/leaderboard`, in `spaRoutes` and the nav): map picker in the standard map order, two board chips, a ranked table, `?map=` and `?order=` kept in the URL so boards can be linked, refresh every 15 s. This browser's own sessions (`ownSessionIds` in `lib/sessions.ts`, filled by `rememberSession`, newest 200) get a "You" pill. After a win the result modal shows "Rank 2 of 14 · 6 slots · 9 steps" with a link to the board (`RankHint.svelte`, one request with `limit: 1`; silent if the lookup fails). Pure logic lives in `lib/leaderboard.ts`.

**Agents.** `/llms.txt` has a Leaderboard section, and the per-session and generic prompts (`lib/prompt.ts`) say how winners are ranked and how to read the board.

## Frontend architecture

- SvelteKit with `adapter-static`, built as a single-page app: `ssr = false`, `prerender = false`, fallback `index.html`. The Go server serves `frontend/build` if present, otherwise `static/`, and falls back to `index.html` for the routes listed in `spaRoutes` (`api/server.go`). A new top-level route must be added there too.
- `static/` is committed. CI and deploys do not build the frontend.
- Svelte 5 runes only (`$state`, `$derived`, `$effect`, `$props`), `onclick`-style attributes, keyed `{#each}`.
- GraphQL: urql for queries and mutations (`network-only`, POST); one lazily created, shared `graphql-ws` client for subscriptions (`lib/graphql.ts`). `unwrap()` turns urql errors into exceptions; `lib/errors.ts` maps error codes to friendly titles and messages.
- Live sessions: `lib/stores/session.ts` fetches the snapshot, subscribes, and keeps only updates with a higher `seq`. The transport is injected (`lib/stores/source.ts`), so the store is tested with a fake source. Connection state (`connecting`, `live`, `reconnecting`, `offline`) drives the connection badge.
- Program editing works on a local draft. The draft is saved with `setProgram` on Run or Step; saving resets the robot to the start, and the UI says so.
- Logic lives in plain TypeScript modules in `lib/` (`program.ts`, `grid.ts`, `highlight.ts`, `dnd.ts`, `shortcuts.ts`, `instructions.ts`, `prompt.ts`, `leaderboard.ts`), each with Vitest tests. Components stay thin.
- GraphQL documents and types are written by hand in `lib/queries.ts` and `lib/types.ts`. Update them whenever the schema changes.
- Brand names come from `lib/brand.ts` (`SITE`, `GAME`, `TITLE`, `pageTitle()`); don't hard-code them. `localStorage` keys use the `drbrain-motor-programming.` prefix; the admin key is kept in `sessionStorage` only and is asked for only when editing a built-in map.
- Gotcha: explicit `onchange`/`oninput` handlers run *before* `bind:value` updates. Read `event.currentTarget.value`, not the bound variable.

## UI look and feel

The goal: friendly, bright and calm, like a puzzle book. There is a matching dark theme (see [Dark mode](#dark-mode)); the palette below describes light mode. Readable for kids and adults, clear enough for a screenshot to explain the game.

### Palette and typography

- One accent family: **indigo**. Page background `#f5f6ff`, text `#1e1b4b` (indigo-950). Cards are white with `border-indigo-200`, `rounded-xl`, and at most a light `shadow-sm`.
- Primary actions: `bg-indigo-600` → `hover:bg-indigo-700`, white text. Secondary: `border-indigo-300`, indigo text, `hover:bg-indigo-50`. Pause is amber. Success is emerald, failure is rose.
- Secondary text is `text-slate-600` at `text-xs`/`text-sm`. Section labels are small uppercase indigo text with wide tracking.
- System font stack (`system-ui, -apple-system, Segoe UI, Roboto`). No web fonts.
- Navigation uses pills: `rounded-full`, the active page filled indigo.

### Dark mode

- **Choice:** System (default), Light or Dark, from the three-icon toggle at the right of the header (`ThemeToggle.svelte`). System follows `prefers-color-scheme` live; Light and Dark override it. The choice is saved in `localStorage` under `drbrain-motor-programming.theme` (System removes the key; every access is wrapped in try/catch).
- **No flash:** an inline script in `src/app.html` reads the saved choice or the OS setting and sets the `dark` class, `color-scheme` and `theme-color` on `<html>` before first paint, and a dark `html` background covers the moment before the stylesheet arrives. The script repeats the logic of `lib/theme.ts` (resolution, storage, `applyTheme`); `theme.test.ts` runs it against those functions, so change them together. `lib/stores/theme.ts` holds the live state and the OS-change listener (`initTheme`, called once from the root layout).
- **How colours work:** `@custom-variant dark` makes Tailwind's `dark:` variant class-based, but components rarely need it. `app.css` re-points, under `.dark`, the Tailwind colour variables the UI already uses, so existing classes get dark values and light mode is untouched. Light fills and borders (the 50–300 steps) become deep indigo-tinted ones; dark text steps (800–950, slate 600–700) become light; solid fills (400–700: buttons, progress bars, rocks) and the instruction border colours stay as they are. A few text classes share a step with a solid fill (`text-indigo-600`, `text-indigo-700`, `text-slate-500`, …) and `bg-white` has an explicit `.dark` rule in `app.css`. When you use a Tailwind colour step the UI doesn't use yet, check it in dark mode and add it to that block.
- **Dark palette:** page `#0e0f24`; cards, header and modals `#171936` (`bg-white`); panels `#1e2149` (indigo-50); hover `#272b5e` (indigo-100); borders `#343a78` (indigo-200); body text `#e0e7ff`; secondary text `#b4bcdc` (slate-600); focus ring `#a5b4fc`. Instruction tiles keep their hue as a dark tint with light text; primary, pause, success and failure keep their solid colours. Modal backdrops use `dark:bg-black/60`.
- **Board:** SVG colours are CSS variables (`--board-*` in `app.css`: tiles, trail, rocks, robot, bump outline) that `Grid.svelte` reads with `var()`. Dark uses navy tiles (`#1b1e46` / `#23275a`), a lighter slate rock, a brighter indigo robot with its white outline, and a lighter rose bump outline. Cookies are unchanged; they read well on both.
- **Checking:** open play (mid-run and the result modal), watch, maps, sessions, learn and the editor in both themes at desktop and phone widths. Text contrast should stay at 4.5:1 or better, except on disabled controls.

### Layout

- Header: logo, **Dr Brain** with **Motor Programming** next to it (hidden on small screens), pill navigation (Play, Maps, Leaderboard, Sessions, How it works, Editor; Sessions stays active on `/watch/*` and `/multi`; the pills wrap on narrow screens) and the theme toggle. Footer: four columns — tagline · Play (Home, Maps, Leaderboard, Sessions, Editor) · Docs (How it works, `/llms.txt`, GraphQL endpoint) · Tools (GraphQL Playground), so agents and curious developers can find the API from any page.
- Map order everywhere is `sortMaps` (`lib/maps.ts`): difficulty, then name with numeric compare, so series numbers stay in order within a difficulty. Map cards show the series label ("Patterns 3") above the title, a square thumbnail, a 2-line description, tags (difficulty, size, treats, step limit, and the recursion rule — both states are labelled: "recursion" in violet or "no recursion" in slate, each with a tooltip from `lib/recursion.ts`) and a full-width Play button.
- Times in lists are relative (`lib/time.ts`: "5m ago", "Oct 4") with the absolute time in a tooltip; bounded counters read "value / limit".
- Content is centred at `max-w-6xl` with `px-4 sm:px-6`.
- Play page: two columns on large screens (board left and sticky, sized to the viewport; side panel right), one column on mobile. The side panel order is fixed: status line (Ready / Running at N ms/step / Paused / Won in N steps / Lost: reason), stats row (Steps, Treats, Best, Runs), run controls, palette and tapes, call stack, Help (key list, "All rules →") and the AI prompt card. Winning or losing opens the result modal (Try again · Next map · All maps); Next map starts the next map in `sortMaps` order with the same player name.
- Keyboard (`lib/globalKeys.ts`): **R** resets and **S** steps when no program slot is focused (inside a slot R means turn right), **?** opens the shortcuts dialog. `editKeyAction` adds **Ctrl/Cmd+Z** undo, **Ctrl/Cmd+Shift+Z** or **Ctrl+Y** redo and **Ctrl/Cmd+Enter** run/pause; these also work from a focused slot (slots never use Ctrl/Cmd) but not in form fields.
- Undo/redo (`lib/history.ts`): the play page keeps past/future stacks of whole programs (100 deep) and routes every player edit through `edit()`, which records the previous draft. A program replaced by someone else (an agent, another tab) is recorded too, so your version stays one undo away. Tapes show "n of m slots used" and flag a filled sub that nothing reachable from main calls (`unreachableSubs` in `lib/program.ts`).
- Watch page: the same layout read-only — status line, stats, **Take over** (opens `/play/<id>`), tapes, call stack, AI prompt card.

### The board

- An SVG drawn at 40 units per cell, scaling to the width available (capped at about 56 px per cell), so it stays sharp at any size.
- Checkerboard of two pale indigo shades (`#eef2ff` / `#e0e7ff`).
- **Robot:** an indigo arrowhead with a white outline. It glides and rotates with a 120 ms ease-out transition and always turns the short way (`nextAngle`).
- **Treats:** chocolate-chip cookies (`Cookie.svelte`).
- **Rocks:** rounded slate squares with a lighter highlight bar.
- **Trail:** small translucent indigo dots on visited cells.
- **Bump:** when the last step was blocked by a rock or the edge, a rose outline flashes on the robot's cell.

### Instructions and tapes

Each instruction has a fixed colour, glyph and key, defined once in `lib/instructions.ts`:

| Instruction | Glyph | Key | Colour |
|---|---|---|---|
| Move forward | ↑ | F | emerald |
| Turn left | ↺ | L | sky |
| Turn right | ↻ | R | amber |
| Call sub 1 / 2 / 3 | S1 / S2 / S3 | 1 / 2 / 3 | violet / fuchsia / rose |
| Empty | (blank) | Backspace | white with slate border |

- Tapes are rows of slots labelled **Main**, **Sub 1**, etc. Each slot shows its index in small faded text.
- Editing: click a slot (or press Enter on it) to open an instruction menu next to it (`SlotPicker.svelte`, placed by `lib/popover.ts`; shortcut keys pick, Backspace erases, Esc closes, focus moves to the next slot); arm a palette instruction to stamp it on every slot clicked; drag between palette and slots (a slot drop swaps; hold Alt/Ctrl/Cmd to copy, Shift to reorder within a tape via `insertSlot`; drop a slot on the palette's Erase tile to clear it; `lib/dnd.ts` `dropModeFor` picks the mode and `activeDrag` shares the in-flight drag because `dragover` can't read `dataTransfer`; the drag preview is a compact tile from `lib/dragImage.ts`), or use the keyboard (letters and numbers place, Backspace clears, arrows move between slots and tapes). Only the subs a map has are offered.
- **Clear all slots** (top right of the tape panel, shown only while a tape holds an instruction) empties every slot on every tape after a `confirm()`, keeping the tape lengths; the change is a draft edit, so it saves like any other (Run/Step or **Save program**). `clearProgram` / `hasInstructions` live in `lib/program.ts`.
- While running, slots show execution state (`lib/highlight.ts`): **next to run** gets a thick indigo ring, a **waiting caller** (a CALL whose sub is running) gets a violet ring with an offset, and **just executed** gets an inner yellow ring. Each state is also described in the slot's accessible label.
- The call stack panel shows each frame and the depth against the map's limit, plus one line stating the map's recursion rule ("Recursion off: a call to a sub already on the stack is ignored, but the step is still spent" / "Recursion allowed: …"). When a step was a CALL that the rule ignored (`callBlocked`), an amber note under the panel names the sub and says the step was still spent, so the wasted step never looks like a bug.

### Feedback

- Run and Step are always in the same place; **Reset** appears only once there is something to undo (a run in progress, steps taken, or a finished run), so a fresh session shows just Run and Step. Unsaved changes show **Save program** and **Revert**, and an amber note explains that saving resets the robot.
- A delay-per-step slider (50–5000 ms) sets the run speed.
- On win or loss: a centred modal ("You won!" in emerald or "Run lost" in rose, with the reason in plain words; a win also shows a one-line leaderboard rank), focus on **Reset & try again**, Escape closes it. A coloured banner stays under the board after the modal is closed.
- Errors appear inline as rose alert boxes with a friendly title, plus the error code in small monospace.
- A connection badge shows whether live updates are connected.

### Accessibility and motion

- "Skip to content" link, `aria-current` on the active nav pill, labelled sections and button groups.
- The board has a text description of the robot's position, facing and treats left; results are announced through a polite live region.
- Visible focus everywhere: 2 px indigo outline with offset (`app.css`).
- `prefers-reduced-motion` turns off animations and transitions globally, including the robot glide.
- Colour is never the only signal: instructions have glyphs, slot states have labels, and results have text.

## Configuration

`.env` (loaded at startup; see `.env.example`):

| Variable | Default | Purpose |
|---|---|---|
| `GRAPHQL_INTROSPECTION` | `true` | Allow schema introspection (keep on for agents) |
| `GRAPHQL_PLAYGROUND` | `true` | Serve `/playground` (off in production) |
| `ADMIN_API_KEY` | empty | Key for changing or deleting a built-in map |
| `ALLOW_UNAUTHENTICATED_ADMIN` | `false` | Local only: built-in map writes without a key, when none is set |
| `MAX_USER_MAPS` | `200` | Cap on player-created maps in `maps-user/` |
| `ALLOWED_ORIGINS` | empty (all) | Comma-separated origins for WebSocket upgrades and CORS |
| `MAX_SESSIONS` | `1000` | Cap on live sessions |
| `SESSION_TTL` | `168h` | Idle sessions older than this are removed on startup |

Frontend build-time variables: `PUBLIC_GRAPHQL_URL`, `PUBLIC_WS_URL`. When unset, the UI uses the page's own host and picks `ws`/`wss` from the page protocol.

## Deployment and hosting

Production runs at https://motor-programming.wricardo.net on a small ARM64 Ubuntu EC2 host shared with other apps.

**Continuous deployment.** Every push to `main` runs `.github/workflows/deploy.yml`: vet and test, build a static `linux/arm64` binary, upload it with `maps/` and `static/`, unpack into `/opt/drbrain-motor-programming` (keeping `sessions/`, `maps-user/` and `.env`), restart the service and smoke-test GraphQL on the host. Repository secrets: `EC2_HOST`, `EC2_SSH_KEY` (a deploy-only key), `EC2_KNOWN_HOSTS`.

**One-time host setup** (not in the repo; repeat on a new host):

1. Create `/opt/drbrain-motor-programming/` owned by `ubuntu`, with `sessions/`, `maps-user/` and a `.env` (production: playground off, a random `ADMIN_API_KEY`, `ALLOWED_ORIGINS` set to the site's http and https origins).
2. Add a systemd unit `drbrain-motor-programming.service`: `WorkingDirectory=/opt/drbrain-motor-programming`, `ExecStart=… -host 127.0.0.1 -port 8086 -maps-dir maps -user-maps-dir maps-user -sessions-dir sessions -public-url https://motor-programming.wricardo.net`, `Restart=always`.
3. Add an nginx `server` block for the domain that proxies to `127.0.0.1:8086` with WebSocket upgrade headers and a long `proxy_read_timeout`.
4. Point DNS at the host and run `sudo certbot --nginx -d motor-programming.wricardo.net --redirect`. `certbot.timer` renews the certificate.
5. Add the deploy key's public half to `~/.ssh/authorized_keys` and set the three repository secrets.

## Maintaining the project

### Common changes

- **Schema change:** edit `graph/schema.graphqls` → `make generate` → update `graph/convert.go` and resolvers → mirror in `frontend/src/lib/{queries,types}.ts` → update `llms.txt.tmpl` and `prompt.ts` if agents are affected → update the query copies in `api/hardening_test.go`.
- **Engine change:** update the golden step counts in `game/engine/engine_test.go` and `validate/validate_test.go` (7, 17, 19, 46, 23, 30, 44, 80, 32, 48, 83, 65, 77, 57, 40, 30, 42 for the shipped solutions) on purpose, never just to make a failure go away.
- **UI change:** `cd frontend && npm run check && npm test && npm run build:static` (or `make build-frontend`), then commit `static/`. If you skip the build, production keeps the old UI.
- **New page:** add the route under `frontend/src/routes/`, add it to `spaRoutes` in `api/server.go`, and to the nav in `+layout.svelte` if needed.
- **Dependencies:** don't run `go mod tidy` casually. `gqlgen generate` rewrites `go.mod`; re-`go get` anything it drops.

### Testing conventions

- Go: standard `testing`, table tests with `t.Run`, `t.Helper`/`t.TempDir`/`t.Cleanup`. No `t.Parallel()` (tests use `t.Setenv`). Always run with `-race`.
- Service tests use `newEnv` in `game/service/helpers_test.go` (fake map store, spy persistence, recording broadcaster). Wait with `waitFor`, never fixed sleeps.
- API tests use `httptest.Server`.
- Frontend: Vitest + jsdom + Testing Library.

```sh
make test                                        # go test -race ./...
go test -race -run TestName ./game/engine        # one Go test
make validate                                    # maps + reference solutions
make lint                                        # golangci-lint
make verify                                      # gofmt, vet, lint, test
cd frontend && npm run check && npm test
cd frontend && npx vitest run src/lib/grid.test.ts   # one frontend test file
scripts/smoke.sh 9191                            # end-to-end HTTP smoke (starts its own server)
```

### Before you push

`make verify && make validate`, then `scripts/smoke.sh 9191` (it builds and starts its own server, so leave the port free). For UI changes, open `/play/<id>` and `/watch/<id>` in a browser and run a program to the end. Pushing to `main` deploys.

### Public repo rules

- `README.md` is for players. Technical content goes here.
- Never commit `solutions/`, and never put solution content in the API, prompts or `static/`.
- Docs and code comments describe the game on its own terms, without references to other private projects.

## Building a game like this

A checklist for the next game in the collection, based on what worked here:

1. **Write the rules as a pure engine first.** State in, step, state out. No I/O, no time. Add `Run`/`Simulate` (run to the end) early; the validator and the tests use them.
2. **Pin the rules with golden tests** before building anything else: known programs and their exact step counts and outcomes.
3. **Define the data as files with a JSON Schema** (maps, puzzles, levels). Validate on load and in CI. Keep reference solutions private and check them with a CLI.
4. **Put one service facade in front of the engine.** It owns sessions, locking and the runner. Storage and broadcasting are interfaces it defines.
5. **Make every update a full snapshot with a monotonic `seq`**, and make subscriptions emit the current snapshot first. This removes a whole class of sync bugs.
6. **Expose everything through one GraphQL schema** (queries, mutations, one subscription per live object). Add coded errors, complexity limits, body limits and origin checks from day one.
7. **Write `/llms.txt` alongside the schema**, plus a per-session "Play with an AI" prompt. Test the examples against the real server.
8. **Build the UI as a static SPA served by the same binary.** Keep logic in tested TypeScript modules, reuse the brand constants, and follow the look and feel above so games in the collection feel related.
9. **Ship as one binary + data folders** behind nginx with Let's Encrypt, deployed by a GitHub Action on push to `main`.
10. **Make the README about playing**, with the live link at the top and a section on letting an AI play. Keep this kind of document for developers.
