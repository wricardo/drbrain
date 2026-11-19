# DrBrain Delivery

A single, hand-authored city that grows as you succeed. You drive delivery cars
by hand — one tile at a time, through the API — between restaurants (pickup) and
customers (dropoff), earning money that buys cars and builds upgrades that
physically change the map.

See [IDEA.txt](IDEA.txt) for the full design. This README tracks what actually
exists.

## Status

**Playable end to end.** P0 (city loading, validation, ASCII render,
reachability and build guards), P1 (tick loop, manual queued-move driving,
battery, charging, stranded/tow, multi-car), P2 (orders, deliveries, economy,
ledger, achievements/unlocks/builds, pacing harness), P4 (GraphQL API, session
persistence with rehydrate, per-session ticker), P6 (SvelteKit dashboard,
save picker, D-pad).

GraphQL only (no REST): `graph/schema.graphqls` is the single contract, with Go
and TypeScript both generated from it. `make check` fails if either generated type
file is stale. Frontend: 0 svelte-check errors/warnings, plus a headless-Chrome
test that verifies rendering, keyboard driving, and that nothing is clipped.

Concurrency lives in `game/session`, not the engine: the engine is
single-threaded and mutex-free, sessions serialise access through `Do(fn)` and
own the ticker goroutine. That is what keeps every game rule testable without
racing.

Known defects, with the measurements that reproduce them, are recorded in
[docs/known-issues.md](docs/known-issues.md). Owner and status live in the
`drbrain-delivery` beads board (`bd list`), not here — this README describes how
the game works, not what is currently broken.

Client types are generated from the Go DTOs (`make types`), so the TypeScript
cannot drift from the server.

**All content is intended to be reachable.** The shipped city is designed so one
scooter and zero money can earn every build, every unlock and every tile;
`TestFullProgressionIsReachableFromTheStart` is the guard that plays it through
and fails if any of that becomes unearnable. Not built: MCP tools (deliberately
skipped).

## Progression

| Stage | Gate | Opens |
|---|---|---|
| Corner Charger | 1 delivery · $150 | charger by the depot |
| East Bridge | 5 deliveries · $300 | harbour crossing + bridgehead charger |
| Alley Pavement | $500 earned · $250 | paves the southern dirt track |
| Harbour Gate | 15 deliveries · $800 | harbour district: fish market, diner, charger |
| Fleet Yard | $2000 earned · $700 | depot capacity 8 + **truck class** |
| Hills Road | $4000 earned · $1200 | hills district: pharmacy, villa, observatory |

The **van** is money-only ($400) so a second car is always reachable; the
**truck** is gated behind Fleet Yard. Three items, three districts, three
kitchens and six customers, and 118 walkable tiles start dark and end up open.

## The UI

```bash
make build-frontend    # one-time: npm install + vite build
make serve-ui          # build + serve API and UI on :8080 (PORT=... to change)
```

Then open http://localhost:8080. If that port is taken by another project, run
`make serve-ui PORT=8081` and open http://localhost:8081 — the server refuses to
start on a port already in use rather than deferring to the process holding it.

- **Start screen** lists your saved cities with tick, deliveries, reputation and
  money, or says plainly that there are none yet.
- **Drive** with `w a s d`, the arrow keys, or the on-screen D-pad (so mouse and
  touch work). A direction key queues the move *and* advances one tick, because
  pressing `s` should move the car.
- **Shift plus a direction** queues the step *without* advancing the clock. That
  is how you set up simultaneous moves for several cars: Shift into place with
  each car, then `space` to advance once and resolve them together.
- **`space`** (or `Enter`) advances a tick without moving anything.
- **`tab`** cycles cars (Shift+Tab goes backwards), **`p`** pauses/resumes.
- **[cities]** is a button in the header, not a key: it saves and returns to the
  picker.
- **Clicking an order** outlines its kitchen and customer on the map, so you can
  see where the job actually goes.
- The whole dashboard fits the viewport: panels scroll internally, the page
  never does.

**Sessions start paused.** This game is manual driving, so a live clock would
expire orders while the player was still reading the screen. Unpausing is
available but off by default; agents and spectators can drive the clock at
1×/2×/4×.

This holds for a **loaded** city too: restoring a save that was running comes
back paused, and the client opts in to the clock explicitly. Verified against a
live server — create, `setSpeed(2)`, save, unload, reload reports
`paused: true, speed: 0`.

The UI is optional. Without `frontend/build` the server runs API-only and logs
which directory it looked in — the game is fully playable over HTTP either way.
Car prices are served by the API (`car_classes`), never hardcoded in the client.

### Verifying the UI

```bash
make verify-ui    # headless Chrome: renders, asserts panels, drives the car
```

This is not a screenshot test. It dispatches real key events and asserts the
car's on-screen position changed, so a broken keybinding fails the check. It
also fails on console errors, failed requests, a page that scrolls (panels
should scroll, not the document), or a bottom panel clipped by the viewport.


## Quick start

```bash
make serve        # GraphQL API on :8080 (+ UI if frontend/build exists)
make run          # validate the city and print it
make play         # drive a car in the terminal
make sim          # run the pacing harness
make test         # unit tests
make check        # fmt + vet + test
```

`make serve` does not build the frontend — it only runs the server. On a fresh
clone it logs

```
no web UI at frontend/build (graphql only; run `make build-frontend` to add one)
```

and serves the API alone, which is a complete game. Use `make serve-ui` if you
want the browser UI on the first run.

If `:8080` is already taken — common on a machine running other projects — the
server refuses to start and names the occupant rather than rebinding. Pick a
port instead of killing the other process:

```bash
make serve PORT=8081
```

That refusal matters more than it looks. A second server cannot share the port,
so the *old* process keeps answering, and it answers with the schema it was
built from. Rebuilding and rerunning then changes nothing, while the UI fails
with

```
Cannot query field "upkeepPaid" on type "LedgerView".
```

which reads like a frontend bug and is not one. The guard reports what is
actually listening (`by a running DrBrain server (health "ok")`) so the stale
process is the obvious suspect.

```bash
curl -s -X POST localhost:8080/graphql -H 'Content-Type: application/json' \
  -d '{"query":"mutation { createSession(id:\"demo\", seed:7) { id paused } }"}'
```

Interactive playground: http://localhost:8080/graphql/playground
Full reference: [docs/graphql.md](docs/graphql.md).

**GraphQL is the only transport.** There is no REST API. The schema
(`graph/schema.graphqls`) is the contract: gqlgen generates the Go types from it
and `cmd/gentypes` generates the TypeScript client from it, so neither side can
drift.

## CLI

```
drbrain map  [-f maps/city.json] [-reveal-all] [-stats]
drbrain play [city.json]
drbrain sim  [-f city.json] [-ticks N] [-every N] [-seed N]
```

`drbrain play` is manual driving, one tick at a time:

```
w a s d   queue a move for the active car (north/west/south/east)
tab       switch active car
t / ttt   advance one / three ticks
.         print car status
q         quit
```

Queuing a move does **not** advance the clock — that is deliberate, and it is
exactly how the API will behave. You can queue moves for several cars, then
advance once, and all of them step together.

`drbrain sim` drives a scripted bot and prints a pacing table. It exists to
answer "is the queue starved or drowning?" with numbers. This is the shape of
`-ticks 1000 -every 500` on the default seed; the digits are illustrative and
were stale the last two times anyone checked them, so run the command rather than
quoting this:

```
  tick  open   tgt  deliv  late expired  strand   money    rep  box  cls
   500     3     7     37     0       2       0     423   3.64    2    0
  1000     3     8     81     1       8       0    9716   4.17    2    0
```

What is stable about it, and worth reading the columns for: `open` and `tgt`
converge by tick 1000, deliveries roughly double between the two rows, and
expiries stay in single digits this early. Re-run the same command to refresh the
numbers; they move with every pacing change.

That command prints the final row **twice**: the loop emits a row whenever
`tick % every == 0`, and then a summary row is printed unconditionally after the
loop. So whenever `ticks` is an exact multiple of `every` the same line appears
twice in a row. It is one run, not two — do not read a duplicated final row as
evidence the harness ran twice, and do not paste it into a doc as two samples.

`open` is live orders, `tgt` a re-derivation of the governor's target depth
(`tgt` counts cars, not boxes) — note the harness hardcodes ratio `1.0` and cap
`8`, so on a map configured with `target_orders_per_car: 0.75` this column runs
*higher* than the depth the spawner actually aims for. `box` is the box count of
the **largest open order** (so the table shows whether quantity bands are
actually rolling or being filtered out by the fleet), and `cls` is the number of
**active street closures** — not a car class index, despite the name.

```
$ drbrain map
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
water to the east (reachable only after the East Bridge build); the hills are
hidden to the south. One depot, three kitchens (Pizza Palace, Harbour Fish
Market, Hilltop Pharmacy), six customers, two chargers, one scooter.

Districts are declared as rectangles in `city.json` and can start hidden — a
hidden tile renders `?` and is impassable until a build reveals it. This is what
makes "explore the map" literal. 182 of 484 tiles (38%) start hidden; 118 of
them are walkable, which is 51% of all 233 walkable tiles.

## Layout

```
cmd/drbrain         CLI (map / play / sim)
cmd/drbrain-server  HTTP API + serves the built UI
cmd/gentypes        generates web/src/lib/delivery/api-types.ts from the GraphQL schema
game/engine         world model: terrain, cars, orders, tick loop, builds, closures
game/session        live sessions, persistence, rehydrate, ticker goroutine
graph               GraphQL schema, resolvers, generated code
api                 static UI serving (no API routes: GraphQL is the transport)
frontend            SvelteKit dashboard (builds into frontend/build)
scripts             genmap.py (map generator), verify-ui.sh + ui-smoke.mjs
maps/city.json      the one city, generated by scripts/genmap.py
docs/graphql.md     GraphQL API reference
docs/known-issues.md  reproduced defects, with the tickets that track them
IDEA.txt            design document + decisions
```

The engine is transport-agnostic: GraphQL, the web UI and MCP will all be
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
- **One step per tick, one command per car.** A move is *queued*, not applied;
  the tick consumes it. A newer command overwrites an older one, so a fast
  client cannot bank a backlog of steps.
- **Rejected moves cost nothing.** Blocked (water, unexplored, closed for
  works) and insufficient-battery moves leave position and battery untouched,
  and record a reason on the car.
- **Cars cannot share a tile, except where the tile is a yard.** A tile holds one
  car; a depot holds `DepotConfig.Capacity` and a charger its own `capacity`, so
  parking and fast charging still work. The limit is on *entry*: a car already on
  a full tile may always drive off, the same rule as street works.
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
  - A driver must not detour around traffic. A blocked car keeps its queued
    direction, so it waits and retries next tick, and waiting costs ticks rather
    than battery. Measured three ways over 3000 ticks on the shipped map: waiting
    behind traffic 250 delivered / 49 expired / 0 tows; detouring while pricing
    the detour 79 / 177 / 34; detouring while pricing the straight route
    198 / 89 / 84. Cost of the rule itself, same driver on both sides, three
    seeds: 247/245/240 with collision against 269/240/253 without — inside noise,
    with reputation at 4.83/5.00/4.90.
- **Charging takes time.** Depots restore 6/tick, standalone chargers 2/tick;
  a full top-up therefore takes multiple ticks. This is what makes routing a
  decision instead of a free reset.
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
  gated by chapter *and* by the fleet's largest hold, so the spawner never
  raises an order no car could carry. Pay is `base × chapter × (0.6 + 0.4×qty)`
  plus a per-tile distance bonus: linear per-box pay would make bulk strictly
  better and order choice a non-decision.
- **Orders are raised on tick boundaries through three gates**: the customer's
  own cooldown, the global cap, and a target queue depth derived from **fleet
  size** (`round(cars × 0.75)`, so one scooter targets one order). Sizing by
  cargo *boxes* was tried and is wrong — a forty-box truck would ask for thirty
  concurrent orders — so the target counts cars, not capacity. This keeps
  supply one step behind throughput instead of flooding at scale.
- **Deadlines are computed, and only ever extended on assignment.** Spawn sets a
  window from `route * slack`; assigning recomputes it from the courier's actual
  position so a player is not punished for an order appearing while the fleet
  was busy. It does not *restart* the window: the new value is applied only when
  it is later than the existing one (`if fresh > o.DeadlineTick`), so assigning a
  car that is already close cannot shorten a deadline the player was quoted.
  Slack tightens by chapter (1.8 → 1.4 across the five chapters).
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
- **Unlock ≠ build.** An achievement grants the *right* to construct; a build
  costs money and mutates the map. Achievements and unlocks are one-shot and
  idempotent.
- **Chapter is derived from builds completed.** No chapter counter is stored:
  `Chapter()` reads `Ledger.BuildsCompleted` (capped at 4) and drives the slack,
  reward and quantity tables. Progress on the map and progress in difficulty
  are therefore the same number.
- **Charging rate is a config knob**, because it is the single most
  balance-sensitive number in the game. See §"Tuning" below.
- **Unknown config fields are errors**, because most knobs are pacing
  multipliers where a silent zero would be harmful. Defaults are applied in
  `NewWorld` as well as the loader, so in-code configs behave like file ones.

## Fleet running cost

Owning a car is not free. A car standing on a **depot tile** costs nothing; a car
anywhere else is charged every `fleet.upkeep_interval_ticks`:

```
upkeep per interval = capacity / 4 * fleet.upkeep_per_box_per_interval   (min 1)
```

On the shipped map (`interval 120`, `per_box 1`) that is **scooter $1, van $3,
truck $10 per 120 ticks** — about 0.008 money per car-tick. A class can override
it with `upkeep_per_interval`.

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
  exists, and it gives `DepotConfig.Capacity` a meaning beyond gating purchases.
  The depot holds 4 (8 after `b06_depot_yard`), which is less than a full fleet,
  so not everything can be mothballed at once.
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

Measured on the shipped map with the default seed, single run, seed `0x5eed5eed`:
**244 deliveries, 64 tows**, all six builds completed. That single seed cannot
distinguish upkeep on from off — it is why the table above uses eight.

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

**Omitting the `fleet` block disables upkeep entirely** (a zero interval charges
nothing), so a map or an existing save keeps the pre-upkeep economy until someone
turns it on. A `_comment` key is *not* allowed in the block: the loader uses
`DisallowUnknownFields`, so documentation lives here rather than in the JSON.

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

The numbers in `maps/city.json` were not guessed. `drbrain sim` found:

- **A 12-battery scooter could not reach the second customer at all.** An
  earlier layout shipped a 30-step round trip on a 12-step budget, so roughly
  half of all orders were mathematically impossible. Battery is now 24 and the
  map is laid out so long routes *require* a charger detour.
- **A 1/tick charger was worse than useless.** Refilling 24 battery took 24
  ticks — longer than an entire deadline. Charger rate is now 2/tick, depot
  6/tick.
- **Deadline slack is the whole difficulty curve.** At 2.0 the bot never fails
  (0 expired, reputation maxed); at 1.4 an earlier layout collapsed. Slack now
  starts at **1.8** and tightens to 1.4 across the five chapters.
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

- **Deliveries rise monotonically** while the fleet and map grow, and plateau
  once all six builds are done and the fleet is at its cap.
- **Expiries cluster after the fleet grows**, not before: the early game has
  almost none because there are few cars and few orders.
- **Every seed completes all six builds**, and the fleet ends at 8 cars (1
  scooter + 1 truck + 6 vans) whichever seed you pick.
- **Seeds disagree by roughly ±10%** on deliveries. That spread is the reason a
  single-seed claim about a balance change is not evidence; see §"Fleet running
  cost" for the 8-seed method used when a number actually matters.

```bash
for s in 1 42 999; do ./bin/drbrain sim -ticks 6000 -every 6000 -seed $s; done
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
