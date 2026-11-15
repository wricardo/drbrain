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
and TypeScript both generated from it. 112 Go tests, race detector clean.
Frontend: 0 svelte-check errors/warnings, plus a headless-Chrome test that
verifies rendering, keyboard driving, and that nothing is clipped.

Client types are generated from the Go DTOs (`make types`), and `make check`
fails if they are stale — so the TypeScript cannot drift from the server.

**All content is reachable.** `TestFullProgressionIsReachableFromTheStart` plays
the shipped city from one scooter and zero money, and asserts every build,
every unlock and every tile is eventually earned. Not built: MCP tools
(deliberately skipped).

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
**truck** is gated behind Fleet Yard. Three items, three districts, six
customers, and 23% of the map starts dark and ends up open.

## The UI

```bash
make build-frontend    # one-time: npm install + vite build
make serve-ui          # build + serve API and UI on :8080
```

Then open http://localhost:8080.

- **Start screen** lists your saved cities with tick, deliveries, reputation and
  money, or says plainly that there are none yet.
- **Drive** with `w a s d` or the on-screen D-pad (so mouse and touch work). A
  direction key queues the move *and* advances one tick, because pressing `s`
  should move the car. `space` advances without moving, which is how you set up
  simultaneous moves for several cars.
- **`tab`** switches cars, **`p`** pauses/resumes, **`cities`** saves and returns
  to the picker.
- **Clicking an order** outlines its kitchen and customer on the map, so you can
  see where the job actually goes.
- The whole dashboard fits the viewport: panels scroll internally, the page
  never does.

**Sessions start paused.** This game is manual driving, so a live clock would
expire orders while the player was still reading the screen. Unpausing is
available but off by default; agents and spectators can drive the clock at
1×/2×/4×.

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
make serve        # GraphQL API + UI on :8080
make run          # validate the city and print it
make play         # drive a car in the terminal
make sim          # run the pacing harness
make test         # unit tests
make check        # fmt + vet + test
```

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
answer "is the queue starved or drowning?" with numbers:

```
  tick  open   tgt  deliv  late expired  strand   money    rep
  1000     1     2     25     0      11       0    1695   3.05
```

```
$ drbrain map
DrBrain City 20x20: 1 kitchens, 2 customers, 1 depots, 2 chargers, items [meds pizza sushi]

####################
#.....#.....##?????#
#.1DD.#..K..##?????#
...
```

Legend: `#` water · `B` building · `.` road · `,` ground · `D` depot ·
`C` charger · `K` kitchen · `H` customer · `?` unexplored · digits = cars.

## The city

20×20 grid. Downtown is split by a canal; the harbour sits behind water to the
east (reachable only after the East Bridge build); the hills are hidden to the
south. One depot, one kitchen (Pizza Palace), two customers, two chargers, one
scooter.

Districts are declared as rectangles in `city.json` and can start hidden — a
hidden tile renders `?` and is impassable until a build reveals it. This is what
makes "explore the map" literal.

## Layout

```
cmd/drbrain         CLI (map / play / sim)
cmd/drbrain-server  HTTP API + serves the built UI
cmd/gentypes        generates frontend/src/lib/api-types.ts from the Go DTOs
game/engine         world model: terrain, cars, orders, tick loop, builds
game/session        live sessions, persistence, rehydrate, ticker goroutine
cmd/gentypes        generates TypeScript from the GraphQL schema
graph               GraphQL schema, resolvers, generated code
api                 static UI serving (no API routes: GraphQL is the transport)
frontend            SvelteKit dashboard (builds into frontend/build)
scripts             verify-ui.sh + ui-smoke.mjs (headless Chrome driver)
maps/city.json      the one city
docs/graphql.md     GraphQL API reference
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
- **Rejected moves cost nothing.** Blocked (water, unexplored) and
  insufficient-battery moves leave position and battery untouched, and record
  a reason on the car.
- **Charging takes time.** Depots restore 6/tick, standalone chargers 2/tick;
  a full top-up therefore takes multiple ticks. This is what makes routing a
  decision instead of a free reset.
- **No autopilot.** There is no pathfinding performed on the player's behalf.
  The player drives, one queued step at a time.
- **Concurrency lives in `game/session`, not the engine.** The engine is
  single-threaded and mutex-free; sessions serialise access through `Do(fn)`
  and own the ticker goroutine. This keeps every game rule testable without
  racing, and the race detector is clean.
- **GraphQL is the only API.** The schema is the contract, and both the Go and
  TypeScript types are generated from it. A move command queues intent; the
  clock applies it.
- **Saves store decisions, not grids.** A save records which builds were
  completed, not the tiles they rewrote: loading replays those builds against the
  current `city.json`, so `city.json` stays the terrain truth and fixing map
  content still applies to existing saves.
- **Pickup is automatic, dropoff is automatic.** Driving onto the kitchen with
  the order assigned collects it; driving onto the customer delivers it. No
  separate verbs, so a delivery costs two routes rather than four API calls.
- **Orders are raised on tick boundaries through three gates**: the customer's
  own cooldown, the global cap, and a target queue depth derived from fleet
  capacity (`round(capacity * ratio)`). Sizing the queue by fleet rather than by
  customer count is what keeps supply one step behind throughput.
- **Deadlines are computed, and refreshed on assignment.** Spawn sets a window
  from `route * slack`; assigning restarts it from the courier's actual
  position, so a player is not punished for an order appearing while the fleet
  was busy.
- **Expired ≠ late.** Late is delivered past the deadline (reduced pay). Expired
  is never delivered (small penalty, reputation hit). The ledger tracks which
  stage each failure happened at — unclaimed, assigned, or in transit — because
  those point at different bugs.
- **Reputation is clamped to [0, 5].**
- **Unlock ≠ build.** An achievement grants the *right* to construct; a build
  costs money and mutates the map. Achievements and unlocks are one-shot and
  idempotent.
- **Charging rate is a config knob**, because it is the single most
  balance-sensitive number in the game. See §"Tuning" below.
- **Unknown config fields are errors**, because most knobs are pacing
  multipliers where a silent zero would be harmful. Defaults are applied in
  `NewWorld` as well as the loader, so in-code configs behave like file ones.

## Tuning (what the harness found)

The numbers in `maps/city.json` were not guessed. `drbrain sim` found:

- **A 12-battery scooter could not reach the second customer at all.** The
  layout shipped with a 30-step round trip on a 12-step budget, so roughly half
  of all orders were mathematically impossible. Battery is now 24 and the
  customers are placed so the far one *requires* a charger detour: Anna (10-step
  round trip) is free, Ben (30-step) is impossible without topping up.
- **A 1/tick charger was worse than useless.** Refilling 24 battery took 24
  ticks — longer than an entire deadline. Charger rate is now 2/tick, depot
  6/tick.
- **Deadline slack is the whole difficulty curve.** At 2.0 the bot never fails
  (0 expired, reputation maxed); at 1.4 it collapses (123 expired). The tuned
  value is **1.6**.

Measured after the content pass, 6k ticks, three seeds:

| seed | delivered | expired | builds | cars |
|---|---|---|---|---|
| 1 | 423 | 39 | 6/6 | 6 |
| 42 | 426 | 40 | 6/6 | 6 |
| 999 | 436 | 30 | 6/6 | 6 |

The early game is the interesting part (12 deliveries and only 2 expiries by
tick 400). Late game is easy for the harness bot because it drives optimally and
never idles — a human will not, so the late curve is deliberately not tuned
against it. Worth revisiting with a human playtest rather than more bot data.

Re-run the sweep after any balance change:

```bash
for s in 1 42 12345 99999; do ./bin/drbrain sim -ticks 4000 -seed $s; done
```
