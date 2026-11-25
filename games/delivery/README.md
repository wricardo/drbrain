# DrBrain Delivery

A catalogue of cities that grows as you succeed. The shipped default is a
hand-authored city; players can create, publish and start sessions on their own
cities. You drive delivery cars by hand — one tile at a time, through the API —
between restaurants (pickup) and customers (dropoff), earning money that buys
cars and builds upgrades that physically change the map.

See [IDEA.txt](IDEA.txt) for the full design. This README tracks what actually
exists.

## Status

**Playable end to end.** P0 (city loading, validation, ASCII render,
reachability and build guards), P1 (tick loop, manual queued-move driving,
battery, charging, stranded/tow, multi-car), P2 (orders, deliveries, economy,
ledger, achievements/unlocks/builds, pacing harness), P4 (GraphQL API, session
persistence with rehydrate, per-session ticker), P6 (SvelteKit dashboard,
save picker, D-pad).

GraphQL only (no REST). The canonical schema is `graph/schema.graphqls` at the
repository root; `games/delivery/graph/schema.graphqls` is generated from it and
must not be edited. `go run ./cmd/gentypes -check` fails if the TypeScript client
types are stale. Frontend: 0 svelte-check errors/warnings, plus a headless-Chrome
test that verifies rendering, keyboard driving, and that nothing is clipped.

Concurrency lives in `game/session`, not the engine: the engine is
single-threaded and mutex-free, sessions serialise access through `Do(fn)` and
own the ticker goroutine. That is what keeps every game rule testable without
racing.

Known defects, with the measurements that reproduce them, are recorded in
[docs/known-issues.md](docs/known-issues.md). Owner and status live in the
`drbrain-delivery` beads board (`bd list`), not here — this README describes how
the game works, not what is currently broken.

Client types are generated from the schema by `cmd/gentypes`
(`web/src/lib/graphql-types.ts`), so the TypeScript cannot drift from the server.

**All content is intended to be reachable.** The shipped city is designed so one
scooter and zero money can earn every build, every unlock and every tile;
`TestFullProgressionIsReachableFromTheStart` is the guard that plays it through
and fails if any of that becomes unearnable. Not built: MCP tools (deliberately
skipped).

## Progression

The city grows on a fixed tick schedule; there are no purchases or currency
gates. The schedule is also the demand ramp: the shipped map sets `ramp_ticks`
to the last stage's tick, and new customers and kitchens are placed in the newly
opened district so demand and restaurants spread out instead of cramming the
starting blocks.

The schedule ends, demand does not. `city.json` sets `max_active_orders` to 0
(no ceiling), `target_growth_per_1000_ticks: 0.5` and
`cooldown_decay_per_1000_ticks: 1`, so after tick 2400 the queue target keeps
rising by one standing order every 2000 ticks and every customer cooldown loses
one tick per 1000 ticks (down to a 1-tick floor). Measured on the default seed
with the pacing harness: 150, 202 and 250 orders spawned per 1000 ticks in the
0–6000, 6000–12000 and 12000–24000 windows — the ramp no longer flattens. The
São Paulo maps keep their explicit `max_active_orders: 6` ceiling; giving them
the same knobs is a three-line change.

| Stage | Tick | Opens |
|---|---|---|
| Corner Charger | 180 | charger + **Corner Cafe** customer |
| Harbour Gate | 420 | canal bridge + harbour district + bridgehead charger |
| Market Flats | 720 | paved south track + harbour home + **Dock Diner** |
| Hills Road | 1200 | hills district + charger + **Office Block** |
| Hilltop Kitchen | 1680 | depot charging bays + **Ridge Kitchen** + **Ridge House** |
| Night Market | 2400 | second harbour kitchen + **Night Flat** |

Every vehicle is money-only, and fleet size is money-limited: the depot accepts
any number of cars. The run starts with one scooter; every additional vehicle,
including a second scooter ($250), is bought. The **van** ($600, 24 boxes) is the
natural upgrade; the **truck** ($2100, 40 boxes) can be bought whenever the player
can afford it.
The map has **eleven customers** spread across downtown, the harbour and the
hills, and six kitchens once the growth schedule finishes. Early demand stays
downtown; later homes and restaurants open in the districts instead of piling
everything onto the starting blocks.

| Class | Hold | Price | Work it can carry |
|---|---|---|---|
| Scooter | 4 | $250 | 1–3 boxes |
| Van | 24 | $600 | 5–10 and 13–24 boxes |
| Truck | 40 | $2100 | 30–40 boxes |

## The UI

The shared SvelteKit app lives in `web/`; the delivery routes are
`web/src/routes/delivery/`. From the repository root:

```bash
just build-web     # npm run build -> web/build
just restart       # regenerate schema, rebuild web + binary, serve detached
```

Then open http://127.0.0.1:8181/delivery/ (the root `justfile` defaults to port
8181; `just port=8081 restart` changes it). The server refuses to start on a port
already in use rather than deferring to the process holding it.

- **Start screen** lists your saved cities with tick, deliveries, reputation and
  money, or says plainly that there are none yet.
- **Drive** with `w a s d`, the arrow keys, or the on-screen D-pad (so mouse and
  touch work). A direction key queues one step for the selected car; the clock
  applies it on the next tick.
- **The clock is the only thing that advances the world.** Sessions start paused,
  so nothing moves until you pick `1×`, `2×` or `4×` in the header. Queue steps
  for each car first, then resume the clock: all of them resolve together, one
  queued step per tick. There is no manual tick in the UI or the API.
- **`c`** cycles cars (Shift+C goes backwards), **`p`** pauses/resumes.
- **The `clear` link** next to the queued count drops the selected car's queue
  when traffic or a closure invalidates the plan.
- **[cities]** is a button in the header, not a key: it saves and returns to the
  picker.
- **Clicking an order** outlines its kitchen and customer on the map, so you can
  see where the job actually goes.
- The city map uses storefront, house, charger and depot icons. Red marks a
  customer's unassigned order; blue marks an assignment awaiting pickup and
  amber marks cargo in transit. Customers with both unassigned and active
  assigned orders show both indicators. Unexplored tiles remain impassable.
- The assigned fleet number appears at the kitchen until pickup and at the
  customer until delivery. Shared locations list all relevant fleet numbers.
- The whole dashboard fits the viewport: panels scroll internally, the page
  never does.
- **[analytics]** overlays up to six saved sessions on one tick axis in three
  charts: cash, cumulative boxes delivered, and reputation. It reads the indexed
  history projection, not the live session, so the last point can lag by one
  autosave interval. Reputation before the ledger migration has no history and
  starts at the session’s current value.

**Sessions start paused.** This game is manual driving, so a live clock would
expire orders while the player was still reading the screen. Unpausing is
available but off by default; agents and spectators can drive the clock at
1×/2×/4×.

This holds for a **loaded** city too: restoring a save that was running comes
back paused, and the client opts in to the clock explicitly. Verified against a
live server — create, `setSpeed(2)`, save, unload, reload reports
`paused: true, speed: 0`.

The UI is optional. Without `web/build` (or with `DRBRAIN_STATIC_DIR` pointing at
a missing directory) the server runs API-only and logs which directory it looked
in — the game is fully playable over HTTP either way.
Car prices are served by the API (`car_classes`), never hardcoded in the client.

### Verifying the UI

```bash
cd web && npx vitest run src/lib/delivery/Map.test.ts   # map rendering + keyboard driving
```

This is not a screenshot test. It dispatches real key events and asserts the
car's on-screen position changed, so a broken keybinding fails the check. It
also fails on console errors, failed requests, a page that scrolls (panels
should scroll, not the document), or a bottom panel clipped by the viewport.

## History projections

`platform_sessions` remains the authoritative session snapshot: one JSON payload
per session, loaded and saved as one aggregate. Analytics must not parse that
bytea on every request, so the save path also maintains indexed derived tables:

|table|contents|
|---|---|
|`delivery_sessions`|current city, account, tick, money, reputation, cash watermark|
|`delivery_cash_entries`|append-only checking-account rows keyed by `(session_id, seq)`|
|`delivery_order_history`|one row per order, keyed by `(session_id, order_id)`|

The projection runs after a successful save or autosave and is best-effort:
history failure never turns a saved session into an error. Cash is incremental
by `seq`; orders are upserted only when their signature changes. A reload seeds
both watermarks from the tables, so a restart does not replay the whole ledger.
Terminal orders never regress to an open state.

Rebuild the derived tables from the authoritative payloads at any time:

```bash
DRBRAIN_DATABASE_URL=... drbrain backfill-delivery-history --dry-run
DRBRAIN_DATABASE_URL=... drbrain backfill-delivery-history
```

## Per-tick snapshots

`delivery_tick_snapshots` is the replayable tick log: one row per completed
tick, keyed by `(session_id, tick)`, written from the engine tick hook rather
than from the autosave path. It exists so intra-save history — car positions,
order assignments, battery, cargo — can be inspected after the fact; the
authoritative save still holds only the latest state.

`state` is a compact `engine.SavedState`: cars, orders, balances, counters and
spawner memory. The append-only cash/reputation statements and the retained
event feed are **not** duplicated per tick; they already have their own tables
(or are rolling logs), and copying them would make storage quadratic in session
length.

The writer is asynchronous and never blocks the clock: a queue overflow drops
frames and logs once. Rows are pruned to the newest `DELIVERY_TICK_SNAPSHOTS`
ticks per session (default 1800, `0` disables the tick log) and are deleted with
their session. Because a session ticks before its first save, there is no
foreign key to `platform_sessions`; cleanup is explicit in `Manager.Delete`.

Read them with `delivery { tickSnapshots(sessionId, fromTick, toTick, afterTick,
limit) }` (inclusive bounds, exclusive keyset cursor, limit capped at 500) or
`Manager.TickSnapshots` in Go.

The backfill is idempotent. Delete `delivery_*` rows freely if a projection needs
to be rebuilt; the session payloads are the source of truth.

## Quick start

From the repository root:

```bash
just restart      # regenerate schema, build web + binary, serve detached on :8181
just stop         # stop the detached server
go test ./...     # all Go tests
```

The delivery engine tools are a small CLI under `games/delivery/cmd/drbrain`:

```bash
go run ./games/delivery/cmd/drbrain map -f games/delivery/maps/city.json
go run ./games/delivery/cmd/drbrain play games/delivery/maps/city.json
go run ./games/delivery/cmd/drbrain sim -f games/delivery/maps/city.json -ticks 3000
```

`just restart` serves every game on http://127.0.0.1:8181/delivery/ (change the
port with `just port=8081 restart`). The server refuses to start on a port
already in use rather than deferring to the process holding it; the guard reports
what is actually listening (`by a running DrBrain server (health "ok")`) so a
stale process is the obvious suspect.

```bash
curl -s -X POST http://127.0.0.1:8181/graphql -H 'Content-Type: application/json' \
  -d '{"query":"mutation { deliveryCreateSession(id:\"demo\", seed:7) { session { id paused } ownerToken } }"}'
```

Interactive playground: http://127.0.0.1:8181/graphql/playground
Full reference: [docs/graphql.md](docs/graphql.md).

**GraphQL is the only transport.** There is no REST API. The canonical schema is
`graph/schema.graphqls` at the repository root: gqlgen generates the Go types from
it and `cmd/gentypes` generates the TypeScript client, so neither side can drift.

## CLI

```
go run ./games/delivery/cmd/drbrain map  [-f maps/city.json] [-reveal-all] [-stats]
go run ./games/delivery/cmd/drbrain play [city.json]
go run ./games/delivery/cmd/drbrain sim  [-f city.json] [-ticks N] [-every N] [-seed N]
```

The defaults (`maps/city.json`) are relative to the working directory, so from
the repository root pass `-f games/delivery/maps/city.json`.

`go run ./games/delivery/cmd/drbrain play` is manual driving, one tick at a time:

```
w a s d   queue a move for the active car (north/west/south/east)
tab       switch active car
t / ttt   advance one / three ticks
.         print car status
q         quit
```

Queuing a move does **not** advance the clock — that is deliberate, and the API
behaves the same way. You can queue moves for several cars, then resume the
clock and all of them step together, one queued step per tick. This CLI is the
only place with a manual tick: the GraphQL API has no `tick` mutation.

`go run ./games/delivery/cmd/drbrain sim` drives a scripted bot and prints a pacing table. It exists to
answer "is the queue starved or drowning?" with numbers. This is the shape of
`-ticks 1000 -every 500` on the default seed; the digits are illustrative and
were stale the last two times anyone checked them, so run the command rather than
quoting this:

```
  tick  open   tgt  deliv  late expired  strand   money    rep  box  cls
   500     3     7     37     0       2       0     423   3.64    2    0
  1000     3     8     81     1       8       0    9716   4.17    2    0
```

What is stable about it, and worth reading the columns for: `open` tracks `tgt`
when the fleet can keep up, `tgt` keeps rising after the ramp on maps that set
the demand-growth knobs, deliveries roughly double between the two rows, and
expiries stay in single digits this early. Re-run the same command to refresh the
numbers; they move with every pacing change.

That command prints the final row **twice**: the loop emits a row whenever
`tick % every == 0`, and then a summary row is printed unconditionally after the
loop. So whenever `ticks` is an exact multiple of `every` the same line appears
twice in a row. It is one run, not two — do not read a duplicated final row as
evidence the harness ran twice, and do not paste it into a doc as two samples.

`open` is live orders, `tgt` is the engine's actual demand target
(`World.TargetActive()` — visible customers × `target_orders_per_customer`,
rounded, plus `floor(tick/1000 × target_growth_per_1000_ticks)`, and capped only
by a positive `max_active_orders`), `box` is the box count of the **largest open
order** (so the table shows whether quantity bands are actually rolling), and
`cls` is the number of **active street closures** — not a car class index,
despite the name.

`go run ./games/delivery/cmd/drbrain perfect` is the same harness with a planning
driver instead of the reactive bot, and is the ceiling to read the sim against.
It takes the same `-f`, `-ticks`, `-every` and `-seed`, plus `-v` to log
assignments, purchases and every expiry, and prints the same table (with `cars`
and `blocked` in place of `box` and `cls`) and a summary that adds **expiries by
cause** and how many of them were avoidable.

What it does that the sim bot does not (`cmd/drbrain/perfect.go`): every car keeps
an ordered stop list and a committed route across ticks; routes are planned in
space and time against every other car's route, so a one-car tile is never
double-booked and a head-on swap, which the engine refuses, is never planned; new
orders are inserted at the cheapest place in the cheapest car's tour after
simulating legs, battery (ground tiles cost more battery than time), charge
detours, hold space and every deadline; idle cars park on the depot; and spare
money buys chargers where they cut the trip to charge the most (up to the city's
limit of six). It still sees an order only when the engine raises it, so it is a
ceiling for planning, not an oracle.

On 3000 ticks of the shipped map, delivered / expired (avoidable) / tows:

```
seed 0x5eed5eed   344 / 0 (0) / 0
seed 1            348 / 0 (0) / 0
seed 2            344 / 0 (0) / 0
seed 3            346 / 0 (0) / 0
seed 99           346 / 0 (0) / 0
seed 7            345 / 1 (1) / 0
seed 42           345 / 0 (0) / 0
seed 123          348 / 0 (0) / 0
```

Over seeds 1 to 48 it delivers 16631 orders and expires 10, all of them orders it
accepted and then delivered a few ticks late, with 1 tow and no refused move; 40 of
the 48 seeds have no expiry at all. None of the expiries is unavoidable: the
summary classifies an order that nothing in the fleet could carry and no balance
could buy as `unavoidable`, and since the 5 to 24 box bands lost their
`early_weight` (below) there are none. On the two São Paulo maps it expires nothing
in seeds 1 to 24 (7318 and 3373 orders delivered), with a few refused moves per
run on `saopaulo.json` (8 on seed 1, each replanned the next tick). The sim bot,
on seeds 1 to 3, 99 and the default, delivers 286 to 329 and expires 14 to 28.

Three planner faults only showed up with distance and took `saopaulo.json` from 8
to 20 expiries a run down to none. A car kept a charge decided for its old tour
and drove across the map to charge before a pickup next to it. A kitchen with one
access tile left two cars each waiting for the other, because the car with the
nearer deadline planned first and the one inside then had no way out. And a car
was recharged to full at a charger from which it could not set off, because the
address was more than one battery plus its reserve away.

`-handback` lets a car return an order it has not collected to the queue when its
tour would now reach the customer late, so another car can take it. It cuts the
avoidable expiries over the same 48 seeds from 10 to 6 (43 seeds have none), and
it is **off by default** because part of that gain is a loophole: the engine
re-quotes an order's deadline on every assignment and only ever extends it, so an
order that is handed back and assigned again gets more time than it was
quoted. The default numbers above do not use it.

```
$ go run ./games/delivery/cmd/drbrain map -f games/delivery/maps/city.json
DrBrain City 22x22: 3 kitchens, 6 customers, 1 depots, 2 chargers, items [meds pizza sushi]

.............##???????
.BB.BB.BB.BB.##???????
.BB.BB.BB.BB.##???????
...1..K......##???????
...
```

Legend: `#` water · `B` building · `.` road · `,` ground · `D` depot ·
`C` charger · `K` kitchen · `H` customer · `?` unexplored · `X` closed for
works · digits = cars.

The `1 depots` in that header is real output, not a typo in this README:
`City.Summary()` does not pluralise, so it prints `1 depots` and `1 kitchens`
whenever a count is one. The map itself is 22×22 with one depot and two
chargers.

## The city

22×22 grid, generated by `scripts/genmap.py` as a Manhattan street grid (streets
every third row and column, 2×2 blocks between them, a ring road at both edges).
Downtown is split from the harbour by a two-wide canal; the harbour sits behind
water to the east (reachable only after the Harbour Gate stage); the hills are
hidden to the south. One depot, three starting kitchens (Pizza Palace, Harbour
Fish Market, Hilltop Pharmacy), eleven customers after growth, two authored
chargers, one scooter. Growth opens the harbour and hills and adds three more
kitchens, so demand and restaurants spread across districts instead of piling
onto the starting blocks.

Districts are declared as rectangles in `city.json` and can start hidden — a
hidden tile renders `?` and is impassable until a build reveals it. This is what
makes "explore the map" literal. 182 of 484 tiles (38%) start hidden; 118 of
them are walkable, which is 51% of all 233 walkable tiles.

Builtin cities ship from `games/delivery/maps`: `city` (`DrBrain City`, the default
22×22 map) and `saopaulo` (`São Paulo`, a 40×40 map with six districts). Builtin
city files live under `DELIVERY_CITIES_DIR` and are release-managed;
`DELIVERY_CITY` names the legacy default file and must live inside that
directory. Players create custom cities through `createCity`; those are
PostgreSQL `platform_maps` rows (owner, DRAFT/PUBLISHED), capped by
`DELIVERY_MAX_USER_CITIES` (default 200), and `pg_dump` is their only backup.
`cities`/`city(id)` expose the catalogue, including `builtin`/`ownerless` flags so the UI only
offers the admin key for shipped or legacy unowned rows; `createSession(cityId:)` picks the
city snapshot and inherits the city's visibility (a draft-city session is owner-only).
Legacy custom city files, if any, are imported once with
`drbrain import-maps --game=delivery --dir=<legacy dir>`; they become PUBLISHED
and ownerless (admin-managed). See [docs/graphql.md](docs/graphql.md).

## Layout

```
cmd/drbrain         engine CLI (map / play / sim)
game/engine         world model: terrain, cars, orders, tick loop, builds, closures
game/session        live sessions, persistence, rehydrate, ticker goroutine
game/config         city catalogue (builtin files + PostgreSQL rows)
graph               generated SDL, resolvers, conversions
graph/model         hand-written Snapshot/LedgerView bound in gqlgen.yml
metrics.go          Prometheus collectors for the host metrics listener
scripts             genmap.py (map generator)
maps/city.json      the shipped default city, generated by scripts/genmap.py
maps/saopaulo.json  a second, larger 40×40 city
llms.txt.tmpl       served at /delivery/llms.txt for AI agents
docs/graphql.md     GraphQL API reference
docs/known-issues.md  reproduced defects, with the tickets that track them
IDEA.txt            design document + decisions
```

The engine is transport-agnostic: GraphQL and the SvelteKit UI in `web/` are
adapters over `game/engine`, which knows nothing about HTTP or rendering.

## Engine rules encoded so far

- **Terrain**: water and buildings are impassable; ground costs one extra
  battery per step versus road.
- **Hidden tiles are impassable.** Revealing a district is a real gate.
- **Builds are atomic.** A build either applies every effect or none, and is
  rejected if the resulting city strands any kitchen or customer from all
  depots and chargers.
- **Deterministic ordering.** Cars resolve in id order, not insertion order, so
  buying a car never changes how existing cars behave on a tick. Pathfinding
  and nearest-charger queries sort by (distance, y, x). Map iteration order
  never leaks into results.
- **One step per tick per car, queued in order.** A move is *appended* to the
  car's queue, not applied; the tick consumes one queued step. A client may
  preload up to 64 steps per car and then advance the clock, which lets agents
  drive without racing the tick rate; `clearMoves` drops the plan. A rejected
  step is spent, not retried.
- **Rejected moves cost nothing.** Blocked (water, unexplored, closed for
  works) and insufficient-battery moves leave position and battery untouched,
  and record a reason on the car.
- **Cars cannot share a tile, except depots.** A road holds one car; a depot
  accepts any number of cars (fleet size is money-gated) and a charger holds its
  authored `capacity`, so fast charging still has a queue. The limit is on
  *entry*: a car already on a full tile may always drive off, the same rule as
  street works.
  Occupancy is the **last** check evaluated, because it is the only one that
  depends on what the *other* cars did this tick. A flat car aiming at an
  occupied tile therefore reports `no_battery`, not `occupied`, so the player is
  pointed at the problem they can actually fix.
  Moves are planned first and then resolved together to a fixpoint, which is what
  makes the two interesting cases fall out of "is there room" rather than out of
  processing order:
  - **Following works.** A car may enter a tile a car ahead vacated this same
    tick, so a queue moves up instead of deadlocking behind its leader.
  - **A head-on pair cannot pass through each other.** Neither destination ever
    frees, so *both* are refused rather than the lower id winning: the answer is
    "there is no room", not "the other car has right of way". Reversing one car
    breaks the stand-off, which is the player's move to make.
  Both are covered by `game/engine/occupancy_test.go`. Over the API this reads as
  `lastReject: OCCUPIED`.
  Two consequences worth knowing:
  - An idle car parked in a one-wide street is now a real roadblock. The
    harness's driver parks idle cars at the depot for that reason, and a full
    yard whose single exit is occupied seals the fleet in — `Tow` is the escape.
  - A driver must not detour around traffic. The harness issues one decision per
    tick, so a blocked car re-queues the same direction and waits, and waiting
    costs ticks rather than battery. (At the engine level a rejected step is
    spent, so a manual or agent client that preloaded a route should check
    `lastReject` and re-queue or `clearMoves` before continuing.) Measured three
    ways over 3000 ticks on the shipped map: waiting behind traffic was
    250 delivered / 49 expired / 0 tows on the engine of the time; detouring
    while pricing the detour 79 / 177 / 34; detouring while pricing the straight
    route 198 / 89 / 84. Those three were not re-run since: the same wait-behind
    driver now measures 325 delivered / 14 expired / 17 tows / reputation 5.00
    on the default seed (154 / 111 / 36 / 0.61 before the harness fixes in
    known-issues #8), and 316 / 286 / 329 / 324 delivered at seeds 1 / 2 / 3 / 99.
    The collision on/off comparison (247/245/240 against 269/240/253, reputation
    4.83/5.00/4.90) is likewise from the older engine and not re-measured.
- **Charging takes time and, away from home, money.** Depots restore the map's
  `charging.depot_rate` for free; standalone chargers restore
  `charging.charger_rate` (6 and 2 per tick on the shipped map) and bill per
  tick. A full top-up therefore takes multiple ticks and competes with the
  delivery clock. This is what makes routing a decision instead of a free reset.
- **No autopilot.** There is no pathfinding performed on the player's behalf.
  The player drives, one queued step at a time.
- **Concurrency lives in `game/session`, not the engine.** The engine is
  single-threaded and mutex-free; sessions serialise access through `Do(fn)`
  and own the ticker goroutine. This keeps every game rule testable without
  racing.
- **GraphQL is the only API.** The schema is the contract, and both the Go and
  TypeScript types are generated from it. A move command queues intent; the
  clock applies it.
- **Saves store decisions, not grids.** A save records which builds were
  completed, not the tiles they rewrote: loading replays those builds against the
  current `city.json`, so `city.json` stays the terrain truth and fixing map
  content still applies to existing saves.
- **Closures are derived, never stored.** Street works are a pure function of
  `(seed, tick, chapter)` exactly like order jitter, so a save needs no closure
  state and a replay from the seed closes the same streets on the same ticks.
  See §"Street works" below. The live list is a cache, and `ImportState` refills
  it by re-deriving the current window, so a save taken *while a street is
  closed* reloads with that same closure on the same tiles — verified at tick
  802: `cl_801` on `[(15,2) (15,3) (15,4)]` both sides, and the two worlds stay
  in step on the next tick.
- **Pickup is automatic, dropoff is automatic.** Driving onto the kitchen with
  the order assigned collects it; driving onto the customer delivers it. No
  separate verbs, so a delivery costs two routes rather than four API calls.
- **Orders carry a quantity, and pay scales sublinearly.** Quantity bands are
  paced by session progress, not by chapter or the current fleet. Only the 1–3
  box band has an early share; the 5–10, 13–24 and 30–40 box bands are
  **absent until their `from_progress` gate**. A city starts with one 4-box
  scooter and $0, and a van ($600) or truck ($2100) cannot be bought before an
  early order's 25–35 tick window closes, so an oversized order in the first
  few hundred ticks could only expire (about one run in two). The gates sit
  after the point a good run has earned the price: $650 is earned by tick
  ~200–260 in `city.json` and ~215–395 in the São Paulo maps, against van gates
  at tick 420 and 540; $2150 by ~480–840, against truck gates at 1680 and 2160. Pay is
  `base × chapter × (0.6 + 0.4×qty)` plus a per-tile distance bonus: linear
  per-box pay would make bulk strictly better and order choice a non-decision.
- **Orders are raised on tick boundaries through three gates**: the customer's
  own cooldown, an optional hard cap, and a target queue depth derived from the
  **visible customer population** (`round(customers × 0.6)`). Buying cars must
  add throughput, not create more work. Customer cooldowns also scale with
  progress, falling to 75% of their base interval at 100% ramp, so order
  frequency grows over the session instead of stepping at a chapter boundary.
- **Demand has no ceiling unless the map asks for one.** `max_active_orders: 0`
  removes the hard cap, `target_growth_per_1000_ticks` adds `floor(tick/1000 ×
  value)` standing orders forever, and `cooldown_decay_per_1000_ticks` shaves
  `floor(tick/1000 × value)` ticks off every cooldown down to a 1-tick floor.
  The target term deepens the queue; the cooldown term is what keeps the
  *arrival rate* rising once the player's fleet keeps up. Both default to 0
  (old behaviour) so existing custom cities keep their pacing; `city.json`
  enables them and the São Paulo maps keep an explicit cap.
- **The vehicle ladder is capacity-driven, with work bands on a progress
  curve.** Scooter 4 boxes, van 24, truck 40; all three are purchasable with
  money alone, and the depot parks any number of cars. The bands reach full
  weight at 0% (1–3), 17.5% (5–10), 30% (13–24) and 70% (30–40) of
  `ramp_ticks`. Scooter and van bands have a floor from tick 0; the truck band
  starts at its gate, so 30–40 box work cannot appear before a truck is
  affordable. A player who ignores the ladder sees oversized orders expire; a
  player who buys capacity can take the larger work. There is no fleet-size
  gate beyond money.
- **Deadlines are computed, and only ever extended on assignment.** Spawn sets a
  window from `route * slack`; assigning recomputes it from the courier's actual
  position so a player is not punished for an order appearing while the fleet
  was busy. It does not *restart* the window: the new value is applied only when
  it is later than the existing one (`if fresh > o.DeadlineTick`), so assigning a
  car that is already close cannot shorten a deadline the player was quoted.
  Slack starts at 3.0 and tightens to 2.6 by the last tuning step.
- **Deadlines and rewards respect closures.** Route distance comes from a
  closure-aware path query, so a street closed mid-job lengthens the deadline
  rather than silently making a reachable order impossible. A car standing *on* a
  tile that closes under it keeps its deadline: the closure-aware distance treats
  the origin as passable (only unwalkable terrain is `-1`), so the estimate is
  identical either side — measured 16 ticks with the depot tile open and 16 with
  it closed. `StepToward` agrees, returning a real direction so the car drives
  off. `RouteWalkable` still says `false` there, which is correct: it answers
  whether a car may *enter*, and it may not.
- **Expired ≠ late.** Late is delivered past the deadline (reduced pay). Expired
  is never delivered (small penalty, reputation hit). The ledger tracks which
  stage each failure happened at — unclaimed, assigned, or in transit — because
  those point at different bugs.
- **Reputation is clamped to [0, 5].**
- **Growth is scheduled, not bought.** A growth stage fires once on its tick
  and applies its effects atomically; it is derived from the session tick, not
  stored. The stage count is the chapter, which drives the slack, reward and
  quantity tables. There is no separate build currency.
- **Chapter is derived from growth stages.** No chapter counter is stored:
  `Chapter()` reads how many stages have applied, capped at the map's
  `max_chapter`, and drives the slack, reward and quantity tables. Progress in
  the city and progress in difficulty are therefore the same number.
- **Charging rate is a per-charger config knob**, because it is the single most
  balance-sensitive number in the game. See §"Charging cost" and §"Tuning".
- **Unknown config fields are errors**, because most knobs are pacing
  multipliers where a silent zero would be harmful. Every required knob is
  validated at load and in `NewWorld`, so a missing value fails the map instead
  of silently running with zero. Suggested values are documented on each
  `CityConfig` field.

## Charging cost

Depot charging is free. A standalone charger bills
`charging.charger_price_per_tick` dollars for a full tick of
`charging.charger_rate` battery. The charge is priced by the energy actually
delivered, not by wall time: a partial final tick costs proportionally less,
rounded up so a one-unit top-up is never free.

```
cost of one tick = ceil(price_per_tick * units_delivered / rate)
```

With the shipped $1/tick at 2 units, a full 24-battery scooter costs $12 over
12 ticks; topping up one unit costs $1. The bill accrues on the car and is
written as **one** `CHARGING` statement row when the car fills or leaves the
bay, so a top-up does not bury deliveries under a dozen $1 lines. A car towed
off a paid bay settles first, then pays the tow fee. `car.chargeDue` exposes
the open bill so the fleet panel can show it while charging.

A **supercharger is just a charger with a higher rate and price**; there is no
separate type or code path:

```json
{"id": "ch_super", "pos": {"x": 9, "y": 9}, "capacity": 1,
 "rate": 6, "price_per_tick": 4}
```

That fills a 24-battery scooter in 4 ticks for $16, against $12 over 12 ticks
at a normal charger: faster, and more expensive in total. On a charger entity,
omitting `rate` inherits `charging.charger_rate`; omitting `price_per_tick`
inherits `charging.charger_price_per_tick`; an explicit `0` makes that charger
free. `charging.charger_price_per_tick` itself is required: an explicit `0`
keeps a custom map's chargers free, while the shipped maps set $1/tick.

Players can buy and place a charger with `deliveryBuyCharger`. The price is
`charging.charger_purchase_price` and the per-session cap is
`charging.max_purchased_chargers`; a cap of `0` (the default when omitted)
disables buying. The shipped maps set $150 and 6.

Both are per-city, and the client can read them without the map file:
`CityView.charging` carries `depotRate`, `chargerRate`, `chargerPricePerTick`,
`chargerPurchasePrice` and `maxPurchasedChargers`, on the static `city` query
and inside every session snapshot.

Charging is not a flat per-tick fee because the last tick is usually partial:
billing a full tick for one missing unit overcharges the player. Charging per
unit of energy is the fair rule; integer dollars are preserved by rounding each
tick's fractional cost up, with the per-charger tick rate setting the unit
price.

## Fleet running cost

Owning a car is not free. A car standing on a **depot tile** costs nothing; a car
anywhere else is charged every `fleet.upkeep_interval_ticks`:

```
upkeep per interval = capacity / fleet.upkeep_box_divisor
                      * fleet.upkeep_per_box_per_interval   (min 1)
```

On the shipped map (`interval 120`, `per_box 1`) that is **scooter $1, van $6,
truck $10 per 120 ticks**; the per-car-tick rate therefore varies by class. A
class can override it with `upkeep_per_interval`.

**The rate is deliberately small, and there is a measured cliff above it.** This
is a `city.json` knob, but it is not a free one to raise. Measured over 8 seeds ×
3000 ticks (`sim -seed N`, one isolated directory per config), occupancy on, on
the tree at time of writing:

| upkeep | money/car-tick | mean deliveries | vs none | sd | min | rep | runs at rep 0 |
|---|---|---|---|---|---|---|---|
| off | 0 | 245.8 | — | 7.3 | 236 | — | 0 |
| **120 / 1 (shipped)** | **0.008** | **240.6** | **−5.1** | **13.5** | 220 | 4.65 | 0 |

Above roughly 0.02 money per car-tick the economy is **bistable, not gradual**.
Seed 6 finishes at 238 deliveries / rep 4.81 at 45 ticks and collapses to 139 /
rep **0.00** at 40 ticks, deterministically, in separate directories. The
divergence starts around tick 800: money stalls near $5k while expiries climb, the
bot cannot fund the next build from its $100 reserve, and the run never recovers.
Upkeep is a fixed cost per car against variable income, and nothing in the game
provides credit to absorb a bad patch. (Those two figures were measured on an
earlier tree; the cliff's *position* is what matters and it has been stable while
absolute delivery counts drifted ~+20.)

So the honest summary, which has survived every re-measurement: **the shipped
rate costs a few percent of deliveries and never zeroes a seed; a rate you could
actually feel occasionally destroys a run.** Do not raise
`upkeep_per_box_per_interval` without re-running seed 6.

**Do not read the per-seed deltas as noise.** They are paired and can be large in
both directions — on the tree above, seeds 2 and 8 lose 26 and 22 deliveries while
seed 7 gains 12. Changing an economy knob changes which orders the bot can afford
next, so the whole downstream path diverges; that is variance in the *run*, not
measurement error. Judge the rate on the mean, the minimum and the zero count,
never on one seed.

`game/engine/upkeep_pacing_test.go` guards the canary seed and the rate bound.

Two things about the shape are deliberate, not simplifications:

- **It is "unparked", not "idle".** `CarState` idles for reasons the player does
  not control — an empty board, a full battery, a screen still being read — and
  there is no sell or scrap action, so a per-tick charge on an unsellable asset
  would be an unrecoverable mistake with no exit. Parking is the exit, it already
  exists, and parking is always available at the depot regardless of fleet size.
  The depot is not a fleet cap; it is the free parking state.
  A literal idle-only variant (free while carrying or assigned) was measured too:
  it does **not** remove the cliff — 40/2 idle-only still collapses seed 6 to 139
  / rep 0.00 — so the rate, not the shape, is what breaks a run.
- **Rates scale with capacity.** Revenue scales with how much a car carries, so a
  hand-tuned rate that ignores capacity makes either the scooter pointless or the
  truck free money.

`towCar` charges `tow_fee_per_tile * route distance`, floored at `tow_fee_min`,
and the fee is **clamped to the balance rather than refusing the tow**: a broke
player with a stranded car must still be able to recover it, or abandoning the
save is the only exit. Towing a healthy car is allowed (it is a recall, not just
a rescue) — before this fee it was a free unconditional teleport home, which made
every running-cost rule trivially optional.

`Ledger.UpkeepPaid` and `Ledger.TowFees` are lifetime totals, exposed as
`upkeepPaid` / `towFees` / `upkeepDue` on `LedgerView`. `upkeepDue` is what the
next interval would charge at the cars' current positions, so a client can show
the drain before it lands. Neither is subtracted from `moneyEarned`: upkeep is a
cost, not negative income, and achievements read `moneyEarned`.

Every balance movement also lands in `Ledger.Cash`, the checking-account
statement surfaced as `cashEntries`: signed amount, running balance, and the car
it belongs to when there is one. Upkeep is billed per car so each interval
produces one tagged row per unparked vehicle; deliveries, charging sessions,
tows and car purchases tag their car, while builds and grants are fleet-wide.
A charging session is one row (not one per tick), written when the car fills or
leaves. Charges clamp at a zero balance, and the row records what was actually
charged so the balance column still adds up. The full statement is saved with
the session; a snapshot carries the newest three months' rows, capped at 200,
and the query and live stream carry the same window.

### Monthly report

The checking account is also summarised by accounting month. A month is the
map's **`ticks_per_month`** (600 on the shipped map): about three minutes of
unpaused 1x play, and five months across the 3000-tick canary run. The game has
no wall clock calendar, so a tick-defined month is what makes a replay produce
identical reports. Shorter months split a delivery run across rows; longer
months leave a short session with only one row.

`Ledger.Months` is derived from `Ledger.Cash`: every `recordCash` call folds the
entry into its month, and `ImportState` rebuilds the summaries from the saved
statement. It is not serialised, so old saves backfill automatically and the
statement remains the only source of truth. A snapshot carries at most 3
months, newest first; quiet months are zero rows so month numbers do not skip.

Each month exposes positive `income` and `expenses` magnitudes, `net`, and a
category breakdown. The categories group the statement kinds:

| category | kinds | side |
|---|---|---|
| `DELIVERY_INCOME` | delivery, late delivery | income |
| `REWARDS` | achievement grants | income |
| `ORDER_FINES` | expired orders | expense |
| `UPKEEP` | fleet upkeep | expense |
| `CHARGING` | paid charging sessions | expense |
| `TOWS` | tow fees | expense |
| `CAR_PURCHASES` | buying cars | expense |
| `BUILDS` | city builds | expense |
| `OTHER` | any future kind | either |
| `OPENING` | starting balance | excluded from monthly totals |

Opening capital is deliberately not income: a map that starts with $500 has not
earned $500 in month 1. The account statement still shows the opening row.

On the current demand ramp, eight seeds × 3000 ticks produced **216–266
deliveries** (mean ~236), **44–79 expiries**, and reputation **3.14–5.00**. Every
seed reached chapter 5 with 11 cars (1 scooter, 7 vans, 3 trucks); fleet size is
money-limited, not slot-limited.

A single seed is not evidence about the mechanic, and it is how this feature was
nearly mis-tuned twice. Eight seeds (`sim -seed 1..8`, one isolated directory per
config so parallel runs cannot share a map file) give the table above: mean
**240.6** deliveries at 120/1 against **245.8** with upkeep off, minimum 220
against 236, and zero collapsed runs either way.

Treat the absolute values as a snapshot and **reputation plus the tow count** as
the signals to watch: reputation is the thing that zeroes when a run collapses,
and tows move with anything that changes how often cars strand.

Treat the absolute values as a snapshot and **reputation plus the tow count** as
the signals to watch: reputation is the thing that zeroes when a run collapses,
and tows move with anything that changes how often cars strand.

**The `fleet` block is required**; set `upkeep_interval_ticks` to 0 to disable
upkeep explicitly, which keeps the pre-upkeep economy. A `_comment` key is *not*
allowed in the block: the loader uses `DisallowUnknownFields`, so documentation
lives here rather than in the JSON.

## Street works (closures)

From chapter 1 onward a stretch of street closes for a while — a burst pipe, a
parade, resurfacing — and reopens later. It is the mechanic the generated
Manhattan layout exists to make possible: on an open plaza there is nothing
worth closing.

- **Derived, not stored.** `closures: { enabled, from_chapter, every_ticks,
  duration_ticks, max_concurrent, segment_tiles }` in `city.json`. The closure
  that starts on a tick is a pure function of seed, tick and chapter, so a save
  carries no closure state and a replay from the seed closes the same streets.
  The live list (`w.closures`) is a cache of what has been announced, not saved
  state — and `ImportState` refills it with `rebuildClosures`, which walks the
  current closure window rather than replaying from tick 0. A save taken during
  street works therefore reloads with the same closure on the same tiles.
- **Nothing before chapter 1.** `from_chapter: 1` means a fresh game has no
  street works at all: 2000 ticks at chapter 0 produced zero closures, while
  chapter 1 had one active on 518 of those ticks. Traffic is a mid-game problem
  by design, so a new player is never asked to route around a closure before
  they have learned the map.
- **Never disconnects anything.** Every candidate run is checked with the same
  reachability primitive that guards builds: nothing currently reachable may
  become unreachable, and no kitchen or customer may lose its route. A closure
  that would fail the check is silently skipped and the next candidate is
  tried. The figure that motivated it — 72 of 175 orders undrivable with the
  bridge shut — is historical and cannot be re-measured without reverting the
  guard; what *can* be checked is the current guarantee. Verified by hiding the
  harbour district (and separately the hills) and running 4000 ticks: 267 orders
  spawned, 0 with an unreachable kitchen-to-customer route. The guard validates
  one candidate at a time, so the "never" holds only because `max_concurrent` is
  1 — see the next point.
- **Not a wall.** The block is on *entry*, so a car standing on a tile that
  closes under it can still drive off. Sealing a car in with a scheduled event
  the player did not choose would be a bug with a timer, not a decision. The
  movement rule and `RouteWalkable` implement this; `RouteDistance` does not —
  it reports `-1` for an origin inside a closure — so route estimates and
  movement disagree for exactly that one tile.
- **Never closes an entity tile or a charging tile**, so a pickup, dropoff or
  charger cannot be made unusable, and a car cannot be stranded by works.
- **`max_concurrent` above 1 is unsound, but also currently unreachable.** The
  guard checks each candidate against the live grid and never against a sibling
  that is already closed, so two runs that each pass can jointly cut a pocket
  off. Measured on the shipped map: all 182 candidates pass individually, but
  `[(3,0)(4,0)(5,0)] + [(0,3)(1,3)(2,3)]` disconnects 5 tiles from the depot (2 is
  the mildest case, not the worst). Note the second half of that: with the shipped
  pacing (`every_ticks: 900`, `duration_ticks: 260`) a second closure can never
  start while the first is live, so raising `max_concurrent` alone changes
  nothing. Overlap needs `duration_ticks >= every_ticks` *and*
  `max_concurrent > 1`; only then does the unsound guard matter, and the guard
  must validate the union rather than the candidate.
- **Only from chapter 1.** The early game stays quiet: a player with one scooter
  and no money has no interesting answer to a detour.
- Rendered as `X` under cars, reported on `Snapshot.closures`, and rejected
  with `RejectReason.CLOSED`.

## Tuning (what the harness found)

The numbers in `maps/city.json` were not guessed. `go run ./games/delivery/cmd/drbrain sim` found:

- **A 12-battery scooter could not reach the second customer at all.** An
  earlier layout shipped a 30-step round trip on a 12-step budget, so roughly
  half of all orders were mathematically impossible. Battery is now 24 and the
  map is laid out so long routes *require* a charger detour.
- **A 1/tick charger was worse than useless.** Refilling 24 battery took 24
  ticks — longer than an entire deadline. Charger rate is now 2/tick, depot
  6/tick.
- **Deadline slack is the whole difficulty curve.** Generous slack lets the bot
  finish almost everything; too little collapses the run. Slack now starts at
  **3.0** and tightens to 2.6 by the last tuning step.
- **Quantity pay had to be sublinear.** At linear per-box pay, a full truck
  out-earned any number of small runs and order choice stopped being a
  decision. `0.6 + 0.4×qty` keeps a big job good without making it the only job.
- **Closures had to be checked against reachability.** Reported before the guard
  existed: 72 of 175 orders undrivable while the bridge was shut (historical, and
  not reproducible now that the guard is in). A closure that fails the check is
  skipped rather than applied, and hiding a district today yields 0 impossible
  orders out of 267 spawned.
- **The queue target had to count cars, not boxes.** A capacity-based target
  asked for thirty concurrent orders the moment a truck exists, which makes
  every other tuning number meaningless.

Balance figures are not pinned here. Any digit written down has gone stale within
the hour twice today (the same table read 463/468/433, then 477/500/460, then
469/431/459 after the upkeep retune), because pacing is still being tuned and
every run of the harness is deterministic but every *tree* is not. A figure that
cannot be reproduced by the command beside it reads as a measurement and is
actually a memory, so what follows is the shape and the command instead:

- **Deliveries rise while the city and fleet grow**, then plateau when the
  schedule is exhausted and the demand queue is comfortably served.
- **Expiries cluster after a demand step**, when the city adds customers or a
  new quantity band before the player has bought the matching vehicle.
- **Every seed reaches chapter 5 with a money-limited fleet**, typically 11 cars
  (1 scooter, 7 vans, 3 trucks) because the bot stops adding throughput once the
  demand target is covered.
- **Seeds disagree by roughly ±10%** on deliveries after the demand ramp. That
  spread is the reason a single-seed claim about a balance change is not
  evidence; see §"Fleet running cost" for the 8-seed method used when a number
  actually matters.

```bash
for s in 1 42 999; do go run ./games/delivery/cmd/drbrain sim -f games/delivery/maps/city.json -ticks 6000 -every 6000 -seed $s; done
```

If you need exact figures, run that and quote it with the date and the tree state
you ran it on.

A number taken once is not evidence about the rule it seems to describe. `sim` is
deterministic: the same command on the same build reproduces exactly, so if two
consecutive runs disagree, the *tree* changed between them and neither result says
anything about balance. Check that before attributing a difference to a mechanic.

The harness bot drives optimally and never idles, so it sets a floor rather than
a promise: a human will not match it, and the late curve is deliberately not
tuned against it. Worth revisiting with a human playtest rather than more bot
data.
