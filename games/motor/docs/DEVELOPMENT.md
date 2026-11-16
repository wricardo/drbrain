# Development

How Motor Programming is built, how it looks, how to maintain it, and how to build another game like it. Players should start at the [README](../README.md).

- [Design principles](#design-principles)
- [Run it](#run-it)
- [Architecture](#architecture)
- [Game rules and engine semantics](#game-rules-and-engine-semantics)
- [Maps and reference solutions](#maps-and-reference-solutions)
- [API for humans and AI agents](#api-for-humans-and-ai-agents)
- [Leaderboard](#leaderboard)
- [Accounts and sign-in](#accounts-and-sign-in)
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
5. **Files, not a database.** Maps are JSON files in `maps/` (shipped) and `maps-user/` (player-created); each session is one JSON file in `sessions/`; accounts are files in `accounts/`. Writes are atomic (temp file + fsync + rename). One binary plus a few folders is the whole deployment.
6. **Agent-readable by design.** `/llms.txt` explains the rules and every API call for an AI. Each game page can copy a ready-made prompt. Answers are never exposed: reference solutions are not in the API, the prompts or the public repo.
7. **Puzzles are verified, not hoped for.** Every map has a reference solution that must win, and the harder maps come with a proof that the intended trick is required.

## Run it

```sh
cp .env.example .env      # optional
just restart              # rebuild UI + binary and serve http://localhost:8000 (API + UI)
just run                  # rebuild and serve in the foreground
just dev                  # go run . -port 8000 -debug
```

Every recipe passes `-public-url http://localhost:<port>` (override with `PUBLIC_URL=... just restart`); sign-in is on by default and uses it for the same-origin check. Plain `go run .` works too on localhost, where the request's own `Host` is trusted instead, but a public host without `-public-url` refuses sign-in rather than trust a rebindable host.

Tooling lives in the `justfile` ([`just`](https://github.com/casey/just) is required): `just --list` shows every recipe. `just restart` is the everyday loop — it rebuilds the UI into `static/` and the binary, stops whatever holds the port (`lsof`, plus the pid it recorded in `.run/drbrain-<port>.pid`), and starts the server in the background with its log at `.run/drbrain-<port>.log`. `just stop`, `just status` and `just logs` act on that process, and `PORT=9000 just restart` (or `just port=9000 restart`) moves to another port. The UI build is reproducible: `svelte.config.js` pins the app version to the git commit, so rebuilding unchanged sources leaves `static/` untouched.

Frontend development with hot reload, against a server running on :8000:

```sh
cd frontend && npm run dev:local     # http://localhost:5173
```

Server flags: `-port 8000 -host 0.0.0.0 -maps-dir maps -sessions-dir sessions -accounts-dir accounts -solutions-dir solutions -debug -public-url <url>` (`-public-url` sets the base URL written into `/llms.txt`, is the base of the OAuth callbacks, and is the origin used for the sign-in same-origin check and the `Secure` cookie; it is required once a redirect provider is configured, while password sign-in — on by default — falls back to each request's own host).

Routes:

| Path | What it is |
|---|---|
| `/` | Landing page: short hero, Continue card (this browser's last unfinished session), first 3 maps, recent started sessions |
| `/play/<session>` | Board, status line, stats, run controls, program editor, call stack, help, AI prompt card, result modal |
| `/watch/<session>` | Read-only live view of any session: status line, stats, Take over, tapes, AI prompt card |
| `/sessions` | All sessions: status chips, search (name/id/map), map filter, sort, Load more, select rows to watch together; auto-refreshes every 10 s |
| `/multi?ids=a,b` | Several sessions as compact live boards (from "Watch selected" on `/sessions`) |
| `/maps` | All maps as cards: search and difficulty chips |
| `/runs` or `/runs?name=<name>` | My runs: best run, rank and solved/unsolved for every map (this browser's sessions, or a shared player by name) |
| `/leaderboard?map=<id>&order=slots` or `steps` | Per-map ranking of winners: map picker, two boards, your entries highlighted |
| `/u/<handle>` | Public profile: solved counts (official and community), best run per map, recent activity; a private profile is "not found" for everyone but its owner |
| `/settings` | Signed-in player: handle, display name, public/private profile, add guest runs, delete account |
| `/auth/<provider>/login`, `/auth/<provider>/callback`, `POST /auth/logout`, `POST /auth/password/{register,login}` | Sign-in routes; only exist when the matching sign-in is enabled (see [Accounts and sign-in](#accounts-and-sign-in)) |
| `/editor` | Map editor with server-side validation; anyone can create a map, changing a built-in one needs the admin key |
| `/learn` | How it works: the game, instructions, rules, controls, how an AI plays, a generic AI prompt, tips |
| `/graphql` | GraphQL over HTTP and WebSocket (`graphql-ws`); `/playground` when enabled |
| `/llms.txt` | Guide for AI agents, rendered from `llms.txt.tmpl` |

## Architecture

```
game/engine          pure rules, no deps/IO: MapConfig/NewMap, Program, VMState (Settle/Step), Simulate, Run
game/service         Session type, GameService facade + impl + Runner (ticker goroutine per playing session);
                     interfaces MapStore, SessionStore, Broadcaster, AccountStore; coded errors; leaderboard (recorded wins + ranking);
                     accounts, handles, logins, claims
game/account         AccountStore impl: accounts/accounts/<id>.json + accounts/logins/<sha256>.json, in-memory indexes
auth                 Sign-in: username/password (on by default), providers (Google OIDC, GitHub OAuth2, dev fake), /auth routes,
                     login cookie, same-origin check, per-IP rate limiter
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

- `main.go` is the composition root. It builds the map store (`game/config`), the session store and file persistence (`game/session`), the broadcaster (`transport/websocket`) and, only when a sign-in provider is configured (or `accounts/` already exists), the account store (`game/account`) and `auth.Auth`, passes them to `service.New(...)`, then starts `api.NewServer`. On shutdown it stops HTTP first, then the runners, persisting every session.
- `game/service` defines the `MapStore`, `SessionStore`, `Broadcaster` and `AccountStore` interfaces it needs. Implementations import `service` and assert the interfaces at compile time; `service` never imports them. This keeps the core testable with fakes.
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
- A session file also carries `account_id` and `owner_hash` when it belongs to an account or has an owner token (see [Accounts and sign-in](#accounts-and-sign-in)), and its best wins for the [leaderboard](#leaderboard) (`fewest_slots`, `fewest_steps`). Files written before leaderboards still load: a session whose VM is `WON` gets its win recovered from the program stored with it.
- The session and map stores share `internal/fsutil` for the two file-system steps that differ by platform: `SyncDir` (fsync the directory after a rename or remove; a no-op on Windows, which cannot open a directory for syncing) and `RenameReplace` (os.Rename on Linux; on Windows a short retry, because replacing a file fails with "Access is denied" while a reader, virus scanner or indexer has it open). Production is Linux, where both are the plain calls; the point is that the whole test suite also passes on a Windows developer machine.
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
- Accounts: deleting or renaming a signed-in player's session needs their login or owner token (see [Who may change a session](#who-may-change-a-session)); handles are validated and blocklisted, login tokens and owner tokens are random 32-byte values stored only as SHA-256, cookies are `HttpOnly`/`SameSite=Lax`/`Secure` and only honoured on same-origin JSON POSTs, OAuth state is single use and bound to the browser, redirects are relative-only, `/auth` is rate limited. Details in [Accounts and sign-in](#accounts-and-sign-in).
- WebSocket: 10 s init timeout, frame-size guard (`api/wslimit.go`), origin check against `ALLOWED_ORIGINS`, which also drives CORS on `/graphql`.
- Map creation is atomic (`MapStore.Create`; a conflict is `INVALID_ARGUMENT`). Never check-then-save.

## Game rules and engine semantics

Instructions: `TURN_LEFT`, `TURN_RIGHT`, `MOVE_FORWARD`, `CALL_SUB_1..10`, `EMPTY`.

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

Glyphs: `.` empty, `#` rock, `*` treat, `^ > v <` start and facing (exactly one). Grid 3–32 per side, at least one treat, main tape 1–32, 0–10 subs of 1–16 each. Defaults when omitted: main 10, subs `[10]`, max_steps 200, max_call_depth 16, recursion off. `sub_tape_lengths: []` (no subs) is different from omitting it, so the field has no `omitempty` and `[]` must never become `nil`. JSON Schema: `map-schema.json`.

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
- For recursion maps, the reference solution must lose when recursion is turned off; `just validate` checks this.
- Give the map a short `description`. It appears under the board.

### Reference solutions

`solutions/<id>.json` (`{"main": [...], "subs": [[...]]}`, exact tape lengths). They are **not** in the public repo (`solutions/` is gitignored), never served by the API, and never put in prompts or `static/`. Without them `just validate` reports missing solutions and the solution-dependent tests skip (`requireSolutions` in `validate/validate_test.go`).

### Adding a map

1. Add `maps/<id>.json` (id = file name).
2. Add `solutions/<id>.json` locally.
3. Run `just validate`: it checks structure, that every treat is reachable, and that the reference solution wins.
4. Add its golden step count to the table in `validate/validate_test.go`.

Maps can also be created by anyone in `/editor` or with the `createMap` mutation. Built-in maps (`maps/*.json`) can only be changed or deleted with the admin key; a new map lands in `maps-user/<id>.json` and is playable immediately.

## API for humans and AI agents

The schema lives in `graph/schema.graphqls`.

- **Queries:** `maps`, `map(id)`, `session(id)`, `sessions(sort, limit, mapId)`, `leaderboard(mapId, order, limit, sessionId)`, `playerRuns(sessionIds, name)`, plus `me`, `authProviders` and `profile(handle)` for [accounts](#accounts-and-sign-in).
- **Mutations:** `createSession` (its response also carries the optional secret `ownerToken`), `renameSession`, `deleteSession`, `setProgram`, `run`, `step`, `pause`, `reset`, plus `createMap` and `validateMap` (open) and `updateMap`/`deleteMap` (open for player maps, admin key for built-in ones). For signed-in browsers: `logout`, `updateAccount`, `changePassword`, `deleteAccount(confirmHandle)`, `claimSessions`.
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

**One entry per session (guests) or per account.** (Signed-in players are collapsed to one entry per account per map, see [Accounts and sign-in](#accounts-and-sign-in).) A session keeps its best win under each ordering (`Session.FewestSlots`, `Session.FewestSteps`). A later, worse win never replaces it, a better one (smaller program or fewer steps) does, and editing or clearing the program afterwards does not remove it. Both are persisted with the session. A guest who wants several entries can start several sessions: a guest has no identity to dedupe on, and a display name is not one (anyone could type someone else's). Signed-in players are the exception: all sessions of an account count as one entry. Each row links to `/watch/<sessionId>`.

**Derived, not stored separately.** `GetLeaderboard` scans the live sessions (at most `MAX_SESSIONS`, 1000 by default), reads each one's recorded win and sorts. There is no second store to keep in sync: deleting a session or the TTL sweep takes its entry with it, and a restart reloads everything from the session files. It holds each session lock only to copy a few small values and does no I/O. If the session cap ever grows by orders of magnitude, add an in-memory index maintained from `recordOutcome`.

**Only the current rules count.** A session embeds a snapshot of its map, and player maps can be edited by anyone, so wins on an older version are not comparable with the current puzzle. `Map.Fingerprint` hashes everything that changes difficulty (layout, tape lengths, `max_steps`, `max_call_depth`, the recursion rule; not name, description or difficulty tag), and only sessions whose snapshot matches the map's current fingerprint are ranked. Editing a map's rules therefore starts a fresh board; renaming it does not. A deleted map has no board (`leaderboard` returns null).

**What is exposed.** Rank, session id, display name, handle (account entries), whether it is the viewer's, slots, steps and when. Never the program, so reading a board cannot reveal a route (spectating a session already shows its program, as before; reference solutions stay private). Names go through `PublicName`: control, bidi and zero-width characters become spaces, whitespace collapses, the result is cut to `MaxDisplayName`; empty (only sessions from before guest names) means `Anonymous`. The UI escapes names like any text.

**API.** `leaderboard(mapId, order = SLOTS, limit = 20, sessionId)` returns `{ mapId, order, total, entries, yours }`. `limit` is clamped to 1..100 (negative is `INVALID_ARGUMENT`). `total` counts every ranked session. `yours` is the entry of `sessionId` even outside the returned rows (`null` if it has not won). An unknown map is `null`. Each call scans every session, so it has a flat complexity cost plus a per-row cost scaled by `limit` (`complexityRoot` in `api/server.go`), like `sessions`.

**Frontend.** `/leaderboard` (`routes/leaderboard`, in `spaRoutes` and the nav): map picker in the standard map order, two board chips, a ranked table, `?map=` and `?order=` kept in the URL so boards can be linked, refresh every 15 s. This browser's own sessions (`ownSessionIds` in `lib/sessions.ts`, filled by `rememberSession`, newest 200) get a "You" pill. After a win the result modal shows "Rank 2 of 14 · 6 slots · 9 steps" with a link to the board (`RankHint.svelte`, one request with `limit: 1`; silent if the lookup fails). Pure logic lives in `lib/leaderboard.ts`.

**Guest names.** A session created (or renamed) without a display name gets a random `<Adjective><Noun>` name from `GuestName` (`game/service/guest.go`: 40 × 40 curated, one-word, family-friendly lists, shortened to `MaxDisplayName`; no digits, since a name is not an identity and a clash is harmless). It is generated on the server, in `cleanName`, so browser players and agents calling `createSession` get the same behaviour and the existing `displayName` field carries it. The browser keeps one identity: `rememberSession(id, name)` (`lib/sessions.ts`) saves the server-assigned name as the saved name when none is saved, so the next session reuses it; a name the player typed is never replaced, and clearing the name field gives a new guest. Sessions that predate guest names and have no name still show as `Anonymous`. Changing the name on the home page (when the field loses focus or Enter is pressed) also renames every session this browser started (`renameOwnSessions` in `lib/nameSync.ts`, 4 `renameSession` calls at a time, skipping sessions already named right), because a board entry shows the name of the session that won it; without that, old runs would keep the old name and new runs would look like a second player. An empty name renames nothing. Sessions that cannot be renamed (expired, or owned by an account, whose board entries show the account name anyway) are skipped and counted in the note under the field.

**Player runs.** `playerRuns(sessionIds | name)` returns one row per map a player has won: their best session on the SLOTS board with its current rank and the board total (`GetPlayerRuns`, one scan of the sessions through the same ranking as `leaderboard`, so the fingerprint rule applies). Exactly one selector is allowed. `sessionIds` (up to 200, the number a browser remembers) is exact and is what "My runs" uses. `name` (case-insensitive) matches every session with that display name and powers the shareable link `/runs?name=BraveOtter`; it can mix people who chose the same name and says so on the page. Leaderboard rows link player names to it. `RunsQuery` is the single place that selects a player's sessions: a signed-in viewer's account is one more selector there (added to `sessionIds`, which may then be omitted; `name` stays guest-only), and `/u/<handle>` reuses the same ranking through `GetProfile`.

**My runs page.** `/runs` (`routes/runs`, in `spaRoutes` and the nav): every map in the standard order with Solved / Not yet, best run (slots · steps), rank "n of total", when, and Watch / Leaderboard links, or a Play button for unsolved maps; filter chips and a "Solved x of y" count; "Copy link to share" for the saved name. Row building and filters are in `lib/runs.ts`. Runs live as long as their sessions (`SESSION_TTL`, 7 days idle by default) and as the browser remembers their ids.

**Agents.** `/llms.txt` has a Leaderboard section, and the per-session and generic prompts (`lib/prompt.ts`) say how winners are ranked and how to read the board.

## Accounts and sign-in

**Username and password accounts are on by default**: anyone can create an account on the live site with a handle and a password, with nothing to register anywhere (`AUTH_PASSWORD=false` turns it off). Google and GitHub are each their own switch and stay off until credentials are in the environment. Guest play, the public API and the agent guide work exactly as before; a player who signs in keeps their runs on a profile, appears on the leaderboard under their own handle and gets a public page at `/u/<handle>`. With every way of signing in turned off nothing in this section is active: no `/auth` routes, no cookies, no `accounts/` directory is created, `me` is `null` and `authProviders` is empty.

Because password sign-in is on by default, `accounts/` exists on any deployment (it is created at startup). It is the one new thing to back up; see [Owner setup](#owner-setup).

### What is stored

Only the minimum, because the game may be played by children:

| Where | What |
|---|---|
| `accounts/accounts/<id>.json` | random id, provider name + the provider's opaque user id (only used to recognise the player at the next sign-in, never exposed), handle, display name, `profilePublic`, `createdAt`, `handleChangedAt`, and for [username/password accounts](#username-and-password-on-by-default) the argon2id `password_hash` |
| `accounts/logins/<sha256>.json` | account id and expiry of one signed-in browser, named by the SHA-256 of its cookie token |
| session file | `account_id` and `owner_hash` (SHA-256 of the owner token), only when set |

No email address, no avatar, no provider profile data is requested or stored (a password account stores only a salted hash of its password) (Google is asked for the `openid` scope only, GitHub for no scope: the one call made reads the numeric user id). Deleting an account removes its file and logins. The handle and provider indexes are in memory, rebuilt from the files at startup (`game/account`); writes are atomic like session files, ids and hashes are validated before they touch disk.

### Providers and the sign-in flow

`auth.Provider` is a small interface (`ID`, `Name`, `AuthURL`, `Exchange`) with three implementations in `auth/provider.go`: **Google** (OpenID Connect: authorization-code flow with PKCE S256, `state` and `nonce`; the ID token's signature, issuer, audience and expiry are verified with `go-oidc` against Google's published keys, then the nonce is compared in constant time), **GitHub** (plain OAuth2 with PKCE and `state`) and a **dev fake** (below). A provider is enabled only when both its id and secret are in the environment (`AUTH_GOOGLE_CLIENT_ID/SECRET`, `AUTH_GITHUB_CLIENT_ID/SECRET`); one half alone, or any provider without `-public-url`, stops the server at startup with a clear message.

Plain HTTP routes outside GraphQL (`auth/auth.go`):

| Route | What it does |
|---|---|
| `GET /auth/<provider>/login?next=/path` | makes `state`, `nonce`, a PKCE verifier and a browser-binding cookie, then redirects to the provider |
| `GET /auth/<provider>/callback` | consumes the state (single use, 10 minutes, bound to the browser that started it), exchanges the code, finds or creates the account, rotates the login and redirects to `next` |
| `POST /auth/logout` | same-origin only; ends the login and clears the cookie |

Pending sign-ins live in memory (bounded to 5000); a restart only cancels sign-ins in progress. A failed or cancelled sign-in returns to the site with `?signin=failed` or `?signin=cancelled`, which the layout shows as a dismissible notice. `next` must be a same-site relative path (`SafeNext`: no scheme or host, no `//`, no backslashes or control characters, never `/auth/...`). All `/auth` routes are rate limited per client IP (30 requests a minute, in memory; behind the nginx proxy the last `X-Forwarded-For` entry is used, only when the peer is loopback) and sent with `Cache-Control: no-store`.

### Username and password (on by default)

For players who do not want Google or GitHub, and the only way to sign in that needs nothing registered anywhere — which is why it is **on by default**: `AUTH_PASSWORD=false` turns it off. It runs next to the OAuth providers or alone.

Unlike a redirect provider it has no callback URL to register, so it does not require `-public-url`: without it the same-origin check compares the request's `Origin` against the request's own `Host` (with `X-Forwarded-Proto`), but **only on a local host** — otherwise a DNS-rebinding page, whose origin is the hostname it made resolve here, would match itself. So a public deployment without `-public-url` and without `ALLOWED_ORIGINS` refuses sign-in attempts rather than trusting them; the server logs a warning at startup. Production already passes `-public-url`, which is also what makes cookies `Secure` even for a request that arrives without `X-Forwarded-Proto`. `ALLOWED_ORIGINS` listing the site's own origins is an alternative, but `-public-url` is the intended one.

- **Username = the public handle** (`^[a-z0-9_]{3,20}$`, same reserved list, blocklist and case-insensitive uniqueness). Changing the handle in Settings changes the login name too. A password account is an ordinary account (profile, leaderboard, claiming guest runs, delete); its `Provider` is `password`.
- **No email, no recovery.** Nothing is stored that could reset a password, so a forgotten password means the account cannot be recovered (the player can start a new account). The UI says so before an account is created and in Settings. This was a deliberate choice for child privacy; adding recovery later would need either an email address or one-time recovery codes.
- **Passwords** are 8 to 128 bytes, not on a small embedded list of the most common passwords (`commonPasswords` in `game/service/password.go`), and not equal to the username. There are no composition rules. They are stored only as an **argon2id** hash (19 MiB, 2 passes, 1 lane, random 16-byte salt, PHC string with its own parameters, in the account file as `password_hash`); the hash is read back with bounded parameters, compared in constant time, and never leaves the service (`Account.PasswordHash` is cleared on every account the service returns; `hasPassword` is exposed instead). At most four hashes run at once.
- **Routes** (`auth/auth.go`, only mounted when enabled): `POST /auth/password/register` and `POST /auth/password/login` with `{"handle", "password"}`. Like the GraphQL cookie they need a same-origin `application/json` request, so another site cannot create accounts or probe passwords through a victim's browser; the body is capped at 4 KiB and unknown fields are refused. Success sets the same login cookie as the OAuth flow (rotating the previous one).
- **Guessing.** A wrong password and an unknown username give the identical `401` and cost one hash each (a dummy hash is verified for unknown usernames). Failures are counted per (IP, username): 5 within 15 minutes locks that pair for 15 minutes, and per username: 40 within an hour locks the username for 15 minutes, from every address. A locked key is refused before any hashing, even with the right password. Trade-off: because handles are public, someone can keep a victim's *password login* locked with enough distributed attempts; sessions already signed in, and Google/GitHub sign-in, are unaffected. The per-IP request limit on `/auth` still applies.
- **Changing the password** is the GraphQL mutation `changePassword(currentPassword, newPassword)`: it needs the current password even though the player is signed in, applies the same rules, and ends every other login of the account (the caller's own stays). Google/GitHub accounts have no password and cannot add one. `hasPassword` on `me` tells the UI which kind it is.
- The frontend shows the form in the sign-in dialog when `authProviders` contains the `password` entry (`PasswordForm.svelte`: Sign in / Create account tabs, a "No email, no reset" warning on sign-up) and a "Password" section in Settings for password accounts.

### Login state, cookies and CSRF

- A login is 32 random bytes (base64url) in the `dbmp_session` cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` whenever the public URL is https or the request came in over https (plain-http localhost is the only case without it). Only the SHA-256 is stored. Logins last 30 days from sign-in (fixed, not sliding), at most 10 per account. Every sign-in creates a fresh token and ends the one the browser arrived with, so there is no session fixation.
- The cookie is honoured by `/graphql` only for **same-origin JSON POSTs** (`Auth.Viewer`): `Origin` must be this site's own origin (from `-public-url`, or the request host when `ALLOWED_ORIGINS` is empty or `-public-url` is unset) or listed in `ALLOWED_ORIGINS`; without `Origin`, `Sec-Fetch-Site` must not say cross-site; the content type must be `application/json`. Any other request is simply anonymous, so another site can never act as the player. Subscriptions stay public and never read the cookie.
- CORS never sends `Access-Control-Allow-Credentials`, whatever `ALLOWED_ORIGINS` says, so a cross-origin page cannot send the cookie either.
- Local development against the Vite server (`npm run dev:local`, origin `http://localhost:5173`) needs `ALLOWED_ORIGINS=http://localhost:5173` for the cookie to be accepted; the built UI served by the Go server needs nothing.

### Handles and names

A handle is `^[a-z0-9_]{3,20}$`, unique case-insensitively, checked against a reserved list (`admin`, route names, ...) and a small embedded blocklist (`game/service/handles.go`; lower-casing, look-alike digits and separators are normalised first, so `sh1t_x` is caught; fragments were chosen to avoid ordinary words, and the list is a starter that the owner should extend). The first sign-in generates an `<adjective><noun>` handle from the guest-name lists (a number is appended on a clash). The player may change it, at most once a day (the first change is free); an old handle is free for anyone at once. The display name goes through the same cleaning as guest names (`PublicName`) plus the blocklist and defaults to the handle.

### Linking runs to accounts

A session id is public (anyone watching a winner sees it), so an account can **never** claim a session by id:

- A signed-in creator's `createSession` attaches the session to the account from the cookie.
- Every session made through `createSession` gets a secret **owner token**. Only its SHA-256 is stored (`Session.OwnerHash`); the token is returned once, in the `ownerToken` field of the `createSession` response, and is `null` in every query, list and subscription. Existing agents that read `id` are unaffected; the field is optional.
- After signing in the browser offers (never automatically, since a shared computer may hold someone else's guest runs) to "add my guest runs": `claimSessions([{sessionID, ownerToken}])`, up to 100 a call. A claim succeeds only when the token matches (constant-time compare of the hashes) and the session belongs to no account. A session already on another account is never re-attached; unknown sessions, wrong tokens and sessions from before owner tokens all answer `REJECTED` without saying which. The browser keeps the tokens in `localStorage` (`lib/sessions.ts`) and forgets them once handled or when the account is deleted.

### Who may change a session

Session ids are public, so what an id lets you do is limited. **Guest sessions** are open to anyone who knows the id, as they always were: a human and an agent can share one, and every existing agent keeps working. A session that belongs to a **signed-in player's account** (created while signed in, or claimed) is protected against the two destructive actions: `deleteSession` and `renameSession` need that account's login (cookie) or the secret owner token, passed as the optional `ownerToken` argument (`AuthorizeSessionChange` in `game/service/accounts.go`, called by the resolvers before `DeleteSession`/`RenameSession`; anyone else gets `FORBIDDEN`, an unknown session is still `NOT_FOUND`). Deleting would destroy the account's leaderboard entry, and renaming would let anyone put text on a child's session in the public lists.

Playing (`setProgram`, `run`, `step`, `pause`, `reset`) stays open on purpose: "Play with an AI" hands the session id to an agent, and recorded wins survive any program change, so playing someone's session cannot remove or worsen their entry. If that ever needs closing, the owner token (or a separate play token that the prompt card includes) is the mechanism. A link to an account that no longer exists, or accounts being off, counts as a guest session.

### Leaderboard and profiles

Guest rows stay one per session. Rows owned by an account are collapsed to **one entry per account per map**: the account's best under the board's ordering (`rankedBoards`, after the fingerprint filter). The entry shows the account's display name and `handle` (the UI links it to `/u/<handle>`) and `mine: true` for the signed-in viewer. A guest browser still gets `yours` for a session it remembers, even one owned by an account. **Private profiles** (`profilePublic: false`): the entry reads "Private player" with no handle and no link, but is still one entry per account and still `mine` for its owner; `profile(handle)` is `null` for everyone but the owner. `playerRuns` adds the viewer's own account runs to `sessionIds` (which may then be omitted); `name` matches guest sessions only. A session whose account no longer exists counts as a guest row.

`/u/<handle>` shows the handle, display name, join date, solved counts for the official campaign (`GameMap.builtin`) and for community maps separately, the best run per map (slots, steps, rank, link to watch it) and recent activity. Wins count when played while signed in or after claiming guest runs.

### Deleting an account

`deleteAccount(confirmHandle)` (the UI asks for the handle in an inline panel) first detaches every session the account owned (the link is cleared and the session gets a fresh random guest name), then deletes the account and its logins and clears the cookie. **Decision:** the runs stay on the leaderboards as ordinary guest entries under that new name; nothing links them to the handle or display name any more, and the old handle becomes free. Tests cover the fallout (`TestDeleteAccountFallout`, `TestAccountsEndToEnd`).

### Dev fake provider

`AUTH_DEV_FAKE=true` adds a "Dev sign-in (local only)" provider whose page lets you type any user name. It is honoured only when `-public-url` is a localhost URL (`localhost`, `127.0.0.1`, `[::1]`): otherwise the server **refuses to start**; its sign-in page also refuses non-local `Host` headers. It runs the real flow (state, cookie binding, account creation, login rotation, claim, delete), but it proves nothing about Google or GitHub: those exchanges are covered only by tests against fake provider servers. Try it with:

```sh
AUTH_DEV_FAKE=true go run . -port 8000 -host 127.0.0.1 -public-url http://localhost:8000 -accounts-dir /tmp/accounts
```

### Owner setup

Password sign-in needs no setup at all: it is on by default, so the live site offers "Sign in" with a username and password as soon as this is deployed. The steps below are only for the Google and GitHub buttons, which appear on the live site only after the owner (the repository owner, who controls the host) does them; until then those buttons are absent and the rest of the site behaves as before.

1. **Google.** In the Google Cloud console create (or pick) a project, configure the OAuth consent screen (external, publish it; no sensitive scopes are used), then *Credentials → Create credentials → OAuth client ID → Web application*. Add the authorized redirect URI `<public-url>/auth/google/callback`, for production `https://motor-programming.wricardo.net/auth/google/callback`. Copy the client id and secret.
2. **GitHub.** *Settings → Developer settings → OAuth Apps → New OAuth App*: homepage `https://motor-programming.wricardo.net`, authorization callback URL `https://motor-programming.wricardo.net/auth/github/callback`. Generate a client secret.
3. **Host `.env`** (`/opt/drbrain-motor-programming/.env`; enable either provider or both):

   ```sh
   AUTH_GOOGLE_CLIENT_ID=...
   AUTH_GOOGLE_CLIENT_SECRET=...
   AUTH_GITHUB_CLIENT_ID=...
   AUTH_GITHUB_CLIENT_SECRET=...
   ```

4. **Flags.** The systemd `ExecStart` must carry `-public-url https://motor-programming.wricardo.net` (it already does) and should add `-accounts-dir accounts`. Make sure `ALLOWED_ORIGINS` contains the site's origins (it already does in production). nginx needs nothing new: `/auth/` goes through the same proxy block; keep `X-Forwarded-Proto` and `X-Forwarded-For` set (`proxy_set_header X-Forwarded-Proto $scheme; proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`).
5. **Persistence.** `accounts/` lives next to `sessions/` and `maps-user/` in `/opt/drbrain-motor-programming` and **must survive deploys**. The deploy workflow only replaces `maps/` and `static/`, so it does; never `rm -rf` the directory, and include it in backups. Password accounts are on by default, so this directory is created on first start even without any OAuth app; losing it loses every account. Never set `AUTH_PASSWORD=false` "temporarily" on a live site and back: the accounts stay on disk, so that only hides sign-in until it is turned back on.
6. Password sign-in needs no step of its own: it is on unless `AUTH_PASSWORD=false`. It stores no email, so a forgotten password cannot be reset (a player starts a new account). Read [Username and password](#username-and-password-on-by-default) before enabling OAuth apps, since that section also explains the trade-offs of public handles.
7. Restart the service. The log shows `sign-in enabled: providers=[google github] password=true (accounts=accounts)`; the header then shows "Sign in" and the dialog offers the username/password form.

## Frontend architecture

- SvelteKit with `adapter-static`, built as a single-page app: `ssr = false`, `prerender = false`, fallback `index.html`. The Go server serves `frontend/build` if present, otherwise `static/`, and falls back to `index.html` for the routes listed in `spaRoutes` (`api/server.go`). A new top-level route must be added there too.
- `static/` is committed. CI and deploys do not build the frontend.
- Svelte 5 runes only (`$state`, `$derived`, `$effect`, `$props`), `onclick`-style attributes, keyed `{#each}`.
- GraphQL: urql for queries and mutations (`network-only`, POST); one lazily created, shared `graphql-ws` client for subscriptions (`lib/graphql.ts`). `unwrap()` turns urql errors into exceptions; `lib/errors.ts` maps error codes to friendly titles and messages.
- Live sessions: `lib/stores/session.ts` fetches the snapshot, subscribes, and keeps only updates with a higher `seq`. The transport is injected (`lib/stores/source.ts`), so the store is tested with a fake source. Connection state (`connecting`, `live`, `reconnecting`, `offline`) drives the connection badge.
- Program editing works on a local draft. The draft is saved with `setProgram` on Run or Step; saving resets the robot to the start, and the UI says so.
- Logic lives in plain TypeScript modules in `lib/` (`program.ts`, `grid.ts`, `highlight.ts`, `dnd.ts`, `shortcuts.ts`, `instructions.ts`, `prompt.ts`, `leaderboard.ts`), each with Vitest tests. Components stay thin.
- GraphQL documents and types are written by hand in `lib/queries.ts` and `lib/types.ts`. Update them whenever the schema changes.
- Accounts: `lib/stores/account.ts` holds the signed-in player and the provider list (loaded once by the root layout; a failed lookup counts as signed out, so guests are never blocked), `lib/account.ts` the pure logic (handle checks, `safeNext`, sign-in links, profile summary, claim wording). `AccountMenu` (header) and `SignInDialog` appear only when a provider exists; sign-in is a full-page navigation (`data-sveltekit-reload`), not a router link. `lib/sessions.ts` also remembers the owner tokens `createSession` returns; `ClaimBanner` and `/settings` offer to attach those runs.
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

- Header: logo, **Dr Brain** with **Motor Programming** next to it (hidden on small screens), pill navigation (Play, Maps, Leaderboard, Sessions, How it works, Editor; Sessions stays active on `/watch/*` and `/multi`; the pills wrap on narrow screens) the account menu (**Sign in**, or the handle with My profile, My runs, Settings, Sign out; hidden when the server has no provider) and the theme toggle. Footer: four columns — tagline · Play (Home, Maps, Leaderboard, Sessions, Editor) · Docs (How it works, `/llms.txt`, GraphQL endpoint) · Tools (GraphQL Playground), so agents and curious developers can find the API from any page.
- Maps come in two groups (`lib/maps.ts`): **official** (`builtin: true`, the levels that ship in `maps/`) and **community** (`builtin: false`, made by players in the editor, stored in `maps-user/`). Map order everywhere is `sortMaps`: every official map first, then community maps; inside each group difficulty, then name with numeric compare, so series numbers stay in order within a difficulty. `groupMaps` returns the non-empty groups with a label and one-line blurb; the maps page shows them as two sections with headings once a community map exists (with none, the page looks as before), and the map pickers on the leaderboard and sessions pages use `<optgroup>`. **Next map never leaves its group**: after the last official level there is no next map, so finishing the campaign does not drop the player into someone's custom map; a community map's next map is the next community map. My runs counts the campaign on its own ("Solved 5 of 17 official maps · 1 of 2 community maps") and lists community maps under their own heading. Map cards show the series label ("Patterns 3") above the title, a square thumbnail, a 2-line description, tags (difficulty, size, treats, step limit, and the recursion rule — both states are labelled: "recursion" in violet or "no recursion" in slate, each with a tooltip from `lib/recursion.ts`) and a full-width Play button.
- Times in lists are relative (`lib/time.ts`: "5m ago", "Oct 4") with the absolute time in a tooltip; bounded counters read "value / limit".
- Content is centred at `max-w-6xl` with `px-4 sm:px-6`.
- Play page: two columns on large screens (board left and sticky, sized to the viewport; side panel right), one column on mobile, where the board is pinned small at the top of the screen (about 22% of the viewport height, `max-lg:sticky`, filled with `--page-bg`) so it stays visible while you edit the tapes and press Run, and the map description moves under it. The site header gives the nav links their own horizontally scrolling row below the logo on phones instead of wrapping to three rows. The side panel order is fixed: status line (Ready / Running at N ms/step / Paused / Won in N steps / Lost: reason), stats row (Steps, Treats, Best, Runs), run controls, palette and tapes, call stack, Help (key list, "All rules →") and the AI prompt card. Winning or losing opens the result modal (Try again · Next map · All maps); Next map starts the next map in `sortMaps` order with the same player name.
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
| Call sub 1 … 10 | S1 … S10 | 1 … 9, 0 | one tone per sub, see below |
| Empty | (blank) | Backspace | white with slate border |

Call tiles cycle through `CALL_TONES` in `lib/instructions.ts` (violet, fuchsia, rose, orange, amber, emerald, sky, indigo, slate, then violet again for sub 10), so a map may declare up to `MAX_SUBS` = 10 subs without hand-picking colours; the `S<k>` glyph is the identity and keeps sub 10 distinct from sub 1. Keys are `1`–`9` and `0` for sub 10.

- Tapes are rows of slots labelled **Main**, **Sub 1**, etc. Each slot shows its index in small faded text.
- Editing: click a slot (or press Enter on it) to open an instruction menu next to it (`SlotPicker.svelte`, placed by `lib/popover.ts`; shortcut keys pick, Backspace erases, Esc closes, focus moves to the next slot); arm a palette instruction to stamp it on every slot clicked (a hint under the palette says what is armed; Esc or clicking the tile again disarms it); drag between palette and slots (a slot drop swaps; hold Alt/Ctrl/Cmd to copy, Shift to reorder within a tape via `insertSlot`; drop a placed step anywhere on the palette (an Erase tile or any other tile) to clear it — the whole palette becomes a dashed rose drop zone with a "Drop here to erase" tag while a slot is dragged, `eraseTargetFor` in `lib/dnd.ts` decides what is erasable (palette-tile drags and a locked palette are no-ops), and it goes through the same `edit()` path as any other edit; `lib/dnd.ts` `dropModeFor` picks the mode and `activeDrag` shares the in-flight drag because `dragover` can't read `dataTransfer`; the drag preview is a compact tile from `lib/dragImage.ts`), or use the keyboard (letters and numbers place, Backspace clears, arrows move between slots and tapes). Only the subs a map has are offered.
- **Clear all slots** (top right of the tape panel, shown only while a tape holds an instruction; the Undo/Redo bar beside it is always present so the first edit does not shift the slots) empties every slot on every tape immediately, keeping the tape lengths. There is no confirmation dialog: it is a draft edit like any other, so **Undo** (Ctrl+Z) restores it, and it autosaves like any other edit. `clearProgram` / `hasInstructions` live in `lib/program.ts`.
- While running, slots show execution state (`lib/highlight.ts`): **next to run** gets a thick indigo ring, a **waiting caller** (a CALL whose sub is running) gets a violet ring with an offset, and **just executed** gets an inner yellow ring. Each state is also described in the slot's accessible label.
- The call stack panel shows each frame and the depth against the map's limit, plus one line stating the map's recursion rule ("Recursion off: a call to a sub already on the stack is ignored, but the step is still spent" / "Recursion allowed: …"). When a step was a CALL that the rule ignored (`callBlocked`), an amber note under the panel names the sub and says the step was still spent, so the wasted step never looks like a bug.

### Feedback

- Run controls (`RunControls.svelte`) follow the usual puzzle-game pattern: one large primary button that is **Run**, becomes **Pause** while running and **Try again** after a win or loss, plus **Back**, **Step** and **Reset**. **Back** (key `B`) takes the robot back one step, also after a win or loss. The server has no undo-step call, so the page resets and replays the saved program for one step fewer (`lib/stepBack.ts`; the engine is deterministic, and the replay never reaches the end of the run it came from, so it does not add a run). It costs one request per replayed step and is off while there are unsaved edits. A server-side step-back mutation would be cheaper but needs a schema change and `just generate`. The buttons never move or appear and disappear; unavailable ones are disabled (Reset until the robot has moved, Step while running or finished). Run and Step are also disabled while the Main tape is empty (a run could only lose and would cost an attempt); the status line then reads "Add an instruction to Main to start", and the `S` and Ctrl+Enter keys follow the same rule. Tooltips name the keys.
- Speed is four presets (Slow 1000 ms, Normal 500, Fast 200, Turbo 50; `lib/speed.ts`). A speed set through the API that matches no preset is shown as a custom value. Speed is locked while running because the server fixes it at Run.
- There is no Save button. The program lives in a local draft that is **autosaved 600 ms after the last edit** (and flushed first on Run/Step) through `lib/serialRunner.ts`, which queues saves so two never race. Saving resets the robot to the start, so the board always matches the program on screen; the server, spectators and agents always see the current program. Undo/redo replaces the old Revert button.
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
| `AUTH_GOOGLE_CLIENT_ID`, `AUTH_GOOGLE_CLIENT_SECRET` | empty | Enable Google sign-in (both required; needs `-public-url`) |
| `AUTH_GITHUB_CLIENT_ID`, `AUTH_GITHUB_CLIENT_SECRET` | empty | Enable GitHub sign-in (both required; needs `-public-url`) |
| `AUTH_PASSWORD` | `true` | Username/password sign-up and sign-in; `false` turns it off (needs no external registration, and no `-public-url` on localhost) |
| `AUTH_DEV_FAKE` | `false` | Local only: a fake provider for trying sign-in; the server refuses to start unless `-public-url` is a localhost URL |
| `MAX_ACCOUNTS` | `10000` | Cap on accounts |
| `SESSION_TTL` | `168h` | Idle sessions older than this are removed on startup |

Frontend build-time variables: `PUBLIC_GRAPHQL_URL`, `PUBLIC_WS_URL`. When unset, the UI uses the page's own host and picks `ws`/`wss` from the page protocol.

## Deployment and hosting

Production runs at https://motor-programming.wricardo.net on a small ARM64 Ubuntu EC2 host shared with other apps.

**Continuous deployment.** Every push to `main` runs `.github/workflows/deploy.yml`: vet and test, build a static `linux/arm64` binary, upload it with `maps/` and `static/`, unpack into `/opt/drbrain-motor-programming` (keeping `sessions/`, `maps-user/`, `accounts/` and `.env`), restart the service and smoke-test GraphQL on the host. Repository secrets: `EC2_HOST`, `EC2_SSH_KEY` (a deploy-only key), `EC2_KNOWN_HOSTS`.

**One-time host setup** (not in the repo; repeat on a new host):

1. Create `/opt/drbrain-motor-programming/` owned by `ubuntu`, with `sessions/`, `maps-user/` and a `.env` (and `accounts/` once sign-in is enabled; the server creates it) (production: playground off, a random `ADMIN_API_KEY`, `ALLOWED_ORIGINS` set to the site's http and https origins).
2. Add a systemd unit `drbrain-motor-programming.service`: `WorkingDirectory=/opt/drbrain-motor-programming`, `ExecStart=… -host 127.0.0.1 -port 8086 -maps-dir maps -user-maps-dir maps-user -sessions-dir sessions -accounts-dir accounts -public-url https://motor-programming.wricardo.net`, `Restart=always`.
3. Add an nginx `server` block for the domain that proxies to `127.0.0.1:8086` with WebSocket upgrade headers and a long `proxy_read_timeout`.
4. Point DNS at the host and run `sudo certbot --nginx -d motor-programming.wricardo.net --redirect`. `certbot.timer` renews the certificate.
5. To enable sign-in follow [Owner setup](#owner-setup): register the OAuth apps, add the `AUTH_*` variables to `.env`, restart.
6. Add the deploy key's public half to `~/.ssh/authorized_keys` and set the three repository secrets. The systemd `ExecStart` already carries `-public-url https://motor-programming.wricardo.net`; keep it, because password sign-in (on by default) uses it for the same-origin check and the `Secure` cookie.

## Maintaining the project

### Common changes

- **Schema change:** edit `graph/schema.graphqls` → `just generate` → update `graph/convert.go` and resolvers → mirror in `frontend/src/lib/{queries,types}.ts` → update `llms.txt.tmpl` and `prompt.ts` if agents are affected → update the query copies in `api/hardening_test.go`.
- **Engine change:** update the golden step counts in `game/engine/engine_test.go` and `validate/validate_test.go` (7, 17, 19, 46, 23, 30, 44, 80, 32, 48, 83, 65, 77, 57, 40, 30, 42 for the shipped solutions) on purpose, never just to make a failure go away.
- **UI change:** `cd frontend && npm run check && npm test && npm run build:static` (or `just build-ui`), then commit `static/`. If you skip the build, production keeps the old UI.
- **New page:** add the route under `frontend/src/routes/`, add it to `spaRoutes` in `api/server.go`, and to the nav in `+layout.svelte` if needed.
- **Dependencies:** don't run `go mod tidy` casually. `gqlgen generate` rewrites `go.mod`; re-`go get` anything it drops.

### Testing conventions

- Go: standard `testing`, table tests with `t.Run`, `t.Helper`/`t.TempDir`/`t.Cleanup`. No `t.Parallel()` (tests use `t.Setenv`). Always run with `-race`.
- Service tests use `newEnv` in `game/service/helpers_test.go` (fake map store, spy persistence, recording broadcaster). Wait with `waitFor`, never fixed sleeps.
- API tests use `httptest.Server`.
- Frontend: Vitest + jsdom + Testing Library. `src/test/setup.ts` restores jsdom's `localStorage`/`sessionStorage`, because Node 22+ defines a `localStorage` global that stays `undefined` without `--localstorage-file` and vitest lets it shadow jsdom's.

```sh
just test                                        # go test -race ./...
go test -race -run TestName ./game/engine        # one Go test
just validate                                    # maps + reference solutions
just lint                                        # golangci-lint (needs 2.15+, matching the Go toolchain)
just verify                                      # gofmt, vet, lint, test
cd frontend && npm run check && npm test
cd frontend && npx vitest run src/lib/grid.test.ts   # one frontend test file
just smoke                                        # end-to-end HTTP smoke on :9191 (starts its own server)
scripts/smoke.sh 9191                            # the same, on an explicit port
```

### Before you push

`just verify && just validate`, then `scripts/smoke.sh 9191` (it builds and starts its own server, so leave the port free). For UI changes, open `/play/<id>` and `/watch/<id>` in a browser and run a program to the end. Pushing to `main` deploys.

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
