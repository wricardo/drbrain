# DrBrain Delivery — GraphQL API

GraphQL is the **only** transport. There is no REST surface: the schema at
`graph/schema.graphqls` is the contract, and both the Go server types and the
TypeScript client types are generated from it.

- Endpoint: `POST /graphql`
- Playground: `GET /graphql/playground` (interactive query editor)
- Schema: [`graph/schema.graphqls`](../graph/schema.graphqls)

```bash
make serve     # → http://localhost:8080/graphql
make types     # regenerate frontend/src/lib/api-types.ts from the schema
make generate  # regenerate Go resolvers/types with gqlgen
```

## Generated types, both directions

| Direction | Generator | Output |
|---|---|---|
| schema → Go | gqlgen | `graph/generated/`, `graph/model/` |
| schema → TypeScript | `cmd/gentypes` | `frontend/src/lib/api-types.ts` |

Neither side hand-maintains a copy of the schema. `make check` fails if either
is stale, so a schema change without regeneration breaks the build instead of
silently shipping a lying client.

## Commands vs. the clock

**A move does not move the car.** `moveCar` queues exactly one step for one car;
the tick loop applies it. This is what makes driving several cars by hand
unambiguous — each car has its own input slot, and nothing moves unless you say
so.

**Every session starts paused, including a restored one.** This is a
manual-driving game; a live clock would expire orders while the player was
reading the screen. Unpause with `setSpeed`, or step deterministically with
`tick`.

**CHANGED (was an asymmetry, now uniform):** loading a save used to restore the
`speed` stored in the file and start ticking immediately. It now always comes
back **paused**, and the client opts in to the clock explicitly. Verified against
a live server:

```
createSession          -> { tick: 0, paused: true,  speed: 0 }
setSpeed(2) + save     -> { tick: 0, paused: false, speed: 2 }
evictSession (unload)
session(id) reload     -> { tick: 0, paused: true,  speed: 0 }   # paused
```

Two reasons it is not negotiable, both observable:

1. A client that opens a save while the player is still reading would otherwise
   advance the world — and expire their orders — unattended.
2. With fleet upkeep on (see README §"Fleet running cost"), a resumed-at-2x save
   drains money from a session nobody is playing.

The corrupted-speed fallback therefore no longer applies on load: a save whose
stored speed is `99` also comes back paused, because the stored speed is no
longer used to start the clock at all. It is still *reported* by `Session`
(`speed` in the list view) so a save picker can show the pace a save was left at.

`tickMs` is the wall-clock length of one tick at 1x, from the city config. The
server recomputes the interval each iteration instead of using a fixed ticker, so
a speed change takes effect on the next tick and a paused session never
accumulates ticks to fire in a burst on resume. The cost is that each interval
includes the tick's own work time, so a long run drifts slightly slower than
`tickMs / speed`. Do not treat `tick` as a wall clock; use the `tick` mutation
when you need exact steps.

**Saving is implicit in more places than the name suggests.** `saveSession` is
the explicit one, but three others write to disk:

| action | writes a save? |
|---|---|
| `saveSession` | yes, obviously |
| `evictSession` | **yes** — it saves, then unloads so the next read rehydrates |
| server SIGINT/SIGTERM | **yes** — `Manager.Close` → `SaveAll` before exit |
| `deleteSession` | no; it removes the file |
| ordinary ticking | no |

Verified: created a session, ticked it to 5 without ever calling `saveSession`,
called `evictSession`, and a reload came back at tick 5 with `s2.json` on disk.
So "evict to throw away my scratch game" does not work — use `deleteSession` for
that. Conversely a hard kill (SIGKILL, crash) loses everything since the last
save, because there is no periodic autosave.

## Query

```graphql
query {
  health { status }

  "Static city, every district revealed."
  city { name gridSize rows hiddenTiles kitchens { id name x y } }

  "Every save on disk, newest first."
  sessions { id createdAt tick money reputation deliveries loaded }

  "One session's full state. Rehydrates from disk when not in memory."
  session(id: "demo") {
    tick paused tickMs
    city {
      name gridSize rows hiddenTiles
      kitchens { id name x y } customers { id name x y }
      depots { id name x y } chargers { id name x y }
    }
    cars { id className x y battery maxBattery batteryPct capacity load freeBoxes state cargo queuedMove lastReject }
    orders { id itemName quantity kitchenId kitchenName customerId customerName state reward ticksLeft paid late assignedCar createdTick deadlineTick }
    ledger { money moneyEarned reputation deliveries lateDeliveries expiredOrders ordersSpawned onTimeStreak tows buildsCompleted deliveriesByItem { item count } upkeepPaid towFees upkeepDue }
    carClasses { className price capacity maxBattery stepCost unlocked affordable }
    achievements { id title description done progress target }
    unlocks { id title description requires earned progress target builds }
    builds { id title description cost unlocked completed affordable applicable problem }
    closures { id reason startTick endTick ticksLeft minX minY maxX maxY tiles { x y } }
    events { tick type message carId x y }
  }
}
```

`closures` is the street works live right now — empty in the common case. A
closed tile renders `X` and rejects entry with `CLOSED`, but a car already
standing on it may drive off.

`unlocks[].requires` is the gate in words (`5 deliveries`, `earn $2000`) and
`progress`/`target` measure the strictest condition in its own units, which is
what the City growth panel draws its bar from. A gate with two conditions names
both in `requires`, while the bar tracks only the one furthest from satisfied.

`capacity`, `load` and `freeBoxes` are measured in **boxes**, not orders. A
customer can ask for forty pizzas; whether that fits is a property of the
vehicle, so `assignOrder` rejects an order larger than the car's free hold with
an error naming both numbers. `quantity` on an order is that box count.

### Reading order state

`assignedCar` is **not** cleared when a job finishes: a delivered order keeps the
id of the car that delivered it. Use `state` to decide what is in flight —
`PENDING` `ASSIGNED` `PICKED_UP` are live, `DONE` `EXPIRED` are settled — and read
`assignedCar` as history. `paid` and `late` only become meaningful once `state`
is `DONE`.

`ticksLeft` is signed. An order that is past its deadline but has not been swept
by the tick loop yet reports a negative value instead of clamping to zero, so
"due this tick" and "already missed" stay distinguishable.

### `lastReject` and `queuedMove` timing

`moveCar` only queues; it does not touch `lastReject`. So immediately after
`moveCar` you still see the *previous* rejection, and only a `tick` replaces it.
Verified on the shipped map:

```
car at (3,2), lastReject null
moveCar WEST   -> lastReject null, queuedMove "WEST"   (unchanged so far)
tick           -> lastReject "BLOCKED", queuedMove null
moveCar SOUTH  -> lastReject "BLOCKED"                 (stale by one tick)
tick           -> (3,3), lastReject null               (cleared on success)
```

A client that wants to explain *this* refusal must read the snapshot returned by
`tick`, not the one `moveCar` returned. `lastReject` is also sticky in a second
sense: it stays set until a later tick either rejects again with the same reason
or lets a step succeed, so it is "the most recent failure", not "the failure of
the last command".

### Ledger counters

| field | counts |
|---|---|
| `money` | spendable balance (goes down when you buy) |
| `moneyEarned` | lifetime earnings, never decreases — achievements read this |
| `deliveries` | on-time deliveries only |
| `lateDeliveries` | delivered past the deadline, at reduced pay |
| `expiredOrders` | never delivered |
| `ordersSpawned` | every order ever raised, including expired ones |
| `onTimeStreak` | consecutive on-time deliveries; late and expired both reset it |
| `buildsCompleted` | the same number `Chapter()` derives from, capped at 4 |
| `deliveriesByItem` | per-item **order** counts, not boxes — a two-box order adds 1 |
| `upkeepPaid` | lifetime fleet running cost paid; cumulative, **not** subtracted from `moneyEarned` |
| `towFees` | lifetime spend on tows; cumulative |
| `upkeepDue` | what the next upkeep interval would charge at the cars' current positions |

`deliveries + lateDeliveries + expiredOrders` equals `ordersSpawned` minus the
orders still live, so a client that wants "jobs that failed" must not read
`deliveries` alone.

### The static `city` is fully revealed

`Query.city` is a preview for a client with no session: it calls `RevealAll`
before rendering, so `hiddenTiles` is always `0` there and `rows` never contains
`?`. Per-session fog lives on `session.city`, which is the one to poll.

It also draws **no cars and no closures**, because it renders with an empty car
list and an empty closed-set. Verified side by side on the same seed: the static
query returned `hiddenTiles: 0` with zero `X` glyphs and zero digit glyphs,
while `session.city` for a running session returned `hiddenTiles: 182` with one
car digit. So the static map is a terrain blueprint, not a snapshot — do not use
it to show the player their fleet or current street works.

The resolver reloads the city from disk on every call rather than reusing a live
session's, so `Query.city` cannot cost a running game its fog. That also means it
reflects `maps/city.json` as it is on disk right now, even if a session was
created from an older version of the file.

### Glyphs

`?` unexplored · `#` water · `B` building · `.` road · `,` ground (costs 2
steps to enter — slower to drive on, but does **not** charge) ·
`D` depot (charges 6/tick) · `C` charger (charges 2/tick) · `K` kitchen ·
`H` customer · `X` closed for works

Only `D` and `C` recharge. `CellType.Charges()` is `depot || charger`, so a
car parked on ground — however it looks — never recovers battery.

`1`–`9` car by index. Closures are drawn under cars: a car parked on a closed
street still renders as a car.

Three limits matter when you draw the map yourself:

- **Only the first nine cars get a glyph.** The renderer stops at `i >= 9`, so a
  tenth car is simply absent from `rows` while still present in `cars`.
- **Co-located cars collapse.** A depot may hold several cars (it authors a
  capacity, 4 on the shipped map), so they share one tile and the last one's
  digit wins. Measured with four cars parked at the depot: four cars, one glyph
  (`4`). The UI compensates with a `×N` badge and cycles selection on click;
  a client drawing its own map should read `cars` for the full list.
- **Entity tiles beat terrain, and fog beats both.** A hidden tile renders `?`
  even if a kitchen sits on it, so fog cannot leak a building's position.

The car *data* is authoritative; `rows` is a convenience view. If you need to
show more than nine cars, or a tile holding a fleet, render from `cars`.

### `carView` — a window instead of the whole map

For an agent or a client watching a large map, fetching the whole snapshot every
step is wasteful. `carView` returns a window around one car:

Note this is a **query**, not a live feed: it is one read per call. For
push-based updates use the `Subscription` below — it always carries the full
snapshot, so `carView` remains the right choice only when the payload size
matters more than freshness.

```graphql
query {
  carView(id: "demo", carId: "car_1", radius: 2) {
    rowSpan colSpan rows originX originY
    car { id x y battery state }
    visibleCars { id x y state }
    nearbyOrders { id itemName state ticksLeft reward }
  }
}
```

`rows` are the glyph lines for that window only, `originX`/`originY` say where
`rows[0][0]` sits in city coordinates. `radius` is clamped **down** to 10, but a
negative radius is a `bad_request` error rather than being clamped — verified:
`radius: 99` returns a 14×14 span for a car near the corner (`rowSpan`/`colSpan`
shrink at the map edge, so do not assume they are `2*radius+1`), while
`radius: -1` fails with `radius must be >= 0`. `radius: 0` is legal and returns
just the car's own tile.

#### `nearbyOrders` in a `carView`

`nearbyOrders` is **not** "orders for this car" and not "orders within radius of
the car". It is every live order whose kitchen *or* customer sits inside the
window rectangle — a spatial filter only. It ignores which car an order is
assigned to, so it will list orders belonging to other cars, and it never
includes `DONE` or `EXPIRED`. To find *this* car's work, read `session.orders`
and match `assignedCar`.

Proof by contradiction, run live: with `ord_1` assigned to `car_1` and `car_1`
at (3,3), `carView(car_1, radius: 0)` returns `nearbyOrders: []` while
`radius: 10` returns `ord_1`. Nothing about the order's ownership changed — only
whether the kitchen/customer fell inside the window.


### `events` — incremental deltas

```graphql
query { events(id: "demo", sinceTick: 42) { tick type message } }
```

`sinceTick` is **inclusive**: `sinceTick: 9` returns every event stamped tick 9,
so a client that stores `lastTick` and re-polls with it will see the last tick's
events twice. Poll with `lastTick + 1`, or dedupe on `(tick, type, message)`.

The feed is a ring, not a log: it retains the most recent 500 events
(`engine.MaxEvents`) and drops the oldest silently. A client that falls further
behind than that loses deltas with no error — resync from `session` instead of
trusting an empty delta as "nothing happened".

Event `type` values, all of which the engine emits:

| type | when |
|---|---|
| `car_moved` | a queued step executed |
| `car_blocked` | a step was refused; `lastReject` on the car says why |
| `car_charged` | a tick of charging landed (also used for a completed tow) |
| `car_stranded` | battery hit zero off a charging tile |
| `car_bought` | a purchase succeeded |
| `order_spawned` | a customer raised an order. A `order_starved: …` message shares this type but the branch that emits it is unreachable today — an unsupplied customer goes silent instead (see `docs/known-issues.md` #4) |
| `order_picked_up` | a car collected from a kitchen |
| `order_delivered` | a dropoff settled payment |
| `order_expired` | the deadline passed undelivered, and the penalty was charged |
| `build_completed` | a build applied |
| `unlock_earned` | an achievement or unlock fired |
| `closure_started` / `closure_ended` | street works began or reopened |

Every event carries `tick` and a human-readable `message`; `carId` is null for
world events, and `x`/`y` are `0,0` when the event has no position — they are
plain ints, so a client cannot distinguish "at the origin" from "no position".

## Mutation

```graphql
mutation {
  createSession(id: "demo", seed: 7) { id tick paused }

  "Queue one step. Does NOT advance the clock."
  moveCar(id: "demo", carId: "car_1", direction: SOUTH) { cars { x y queuedMove } }

  "Advance manually. count is capped at 1000."
  tick(id: "demo", count: 1) { tick orders { id state } }

  setSpeed(id: "demo", speed: 0) { paused }       # 0 pauses; 1/2/4 run

  assignOrder(id: "demo", orderId: "ord_1", carId: "car_1") { orders { state } }
  unassignOrder(id: "demo", orderId: "ord_1") { orders { state } }

  buyCar(id: "demo", className: "van", depotId: "d_main") { cars { id className } }
  "Costs tow_fee_per_tile x route distance (min tow_fee_min), clamped to the balance."
  towCar(id: "demo", carId: "car_1") { ledger { money towFees } cars { state } }
  build(id: "demo", buildId: "b02_east_bridge") { city { rows } }

  saveSession(id: "demo") { tick }
  evictSession(id: "demo")   # save, then drop from memory
  deleteSession(id: "demo")  # remove the save file
}
```

Every state-changing mutation returns the **full snapshot**, so a client never
needs a follow-up read to know the new state. The two exceptions are
`evictSession` and `deleteSession`, which return a bare `Boolean` because the
session they would have described no longer exists — verified live (`true` for
each, with no snapshot in the payload).

## Subscription — live updates instead of polling

```graphql
subscription { session(id: "demo") { tick paused cars { id x y } } }
```

The UI opens this instead of polling. The server pushes one frame per tick while
the clock runs, **and** one frame per mutation otherwise (a manual step, a queued
move, a speed change, a purchase). That second half is not a nicety: sessions
start paused, so a stream that only fired on ticks would never move a
hand-played game.

Coalesced, not queued. If the client cannot keep up it misses intermediate
frames and gets the newest state once, rather than a backlog: a paused tab must
not bank thousands of 1 MB snapshots. Measured against a live server — a paused
session idle for 3 s makes **zero** requests, and 1.5 s at 4x delivers every tick
with zero requests:

| situation | requests from the page |
|---|---|
| 3 s idle, paused | 0 |
| 5 external ticks while paused | 0 (5 frames pushed) |
| 1.5 s at 4x (19 ticks) | 0 (19 frames pushed) |

Transports: **SSE** (`Accept: text/event-stream`, a POST whose response never
ends) and **WebSocket** for agents and the playground. SSE is what the browser
uses, because `fetch` + `ReadableStream` reads it without adding a GraphQL
client library to a dependency-free bundle.

A `: ping` comment every 15 s keeps an idle stream alive — a paused city can
legitimately go minutes without a change.

**The payload is not the same as the query's.** `subscription.session` trims
finished orders to the most recent 20 (`streamHistory` in
`game/session/watch.go`); `query.session` keeps the full history. Order history
is ~96% of a late-game snapshot and never shrinks — a 42k-tick session carries
~4,500 orders, of which most are DONE or EXPIRED — so streaming all of it every
tick would cost more than polling did. Measured on that session: 1.4 MB on the
query path versus 63 KB per stream frame.

That asymmetry is deliberate and worth knowing before substituting one for the
other: agents and the CLI treat `session(id:)` as the complete record, and a
client that needs full order history must use the query, not the stream.

Streams are **not** gzipped. `graph/gzip.go` buffers the whole response so it can
encode once, which a response that never ends can never satisfy; `isStreaming`
routes SSE and WebSocket upgrades around the wrapper.

## Errors

Errors are GraphQL errors whose message begins with a machine-readable code:

```
"not_found: session \"nope\": session not found"
"bad_request: count must be <= 1000"
"conflict: van costs $400, you have $0"
```

| code | meaning |
|---|---|
| `not_found` | no such session, car or order |
| `bad_request` | malformed or out-of-range argument |
| `conflict` | legal request, wrong state (unaffordable, locked, already done) |

## Enums

Go models these as strings and gqlgen renders them as GraphQL enums, so the
client gets exhaustiveness checking:

- `Direction` — `NORTH` `SOUTH` `EAST` `WEST`
- `CarState` — `IDLE` `CARRYING` `CHARGING` `STRANDED`
- `OrderState` — `PENDING` `ASSIGNED` `PICKED_UP` `DONE` `EXPIRED`
- `RejectReason` — `BAD_DIRECTION` `OFF_GRID` `BLOCKED` `NO_BATTERY` `CLOSED`
  `OCCUPIED`

  `OCCUPIED` means the destination tile is at capacity for cars: another car
  stands on it, or it is a depot/charger whose slots are taken. It is distinct
  from `BLOCKED` (scenery — reroute) and `CLOSED` (street works — wait) because
  the fix is different again: the tile may clear next tick, so waiting or
  reversing is the answer, not a new route.

  A `null` `lastReject` means "no refusal recorded", and it is ambiguous: a car
  that did not move may simply have had nothing queued. Before `OCCUPIED`
  existed, a car blocked by another car also reported null — that is no longer
  the case.

Adding a value makes the UI's `Record<OrderState, …>` maps fail to compile until
the new case is handled.

## Snapshot glyphs

`city.rows` uses one character per tile:

| glyph | meaning | passable |
|---|---|---|
| `#` | water | no |
| `B` | building | no |
| `.` | road | yes, 1 battery |
| `,` | ground | yes, 2 battery |
| `D` | depot | yes, recharges fast |
| `C` | charger | yes, recharges |
| `K` | kitchen | pickup |
| `H` | customer | dropoff |
| `?` | unexplored | **no** |
| `X` | closed for street works | no (may drive off) |
| `1`–`9` | car by index (first nine only; a depot holding several cars draws one digit plus a `×N` badge) | — |

## A complete delivery

```bash
G=http://localhost:8080/graphql
q() { curl -s -X POST $G -H 'Content-Type: application/json' -d "$1"; }

q '{"query":"mutation { createSession(id:\"demo\", seed:7) { id paused } }"}'
q '{"query":"mutation { tick(id:\"demo\", count:1) { orders { id state } } }"}'
q '{"query":"mutation { assignOrder(id:\"demo\", orderId:\"ord_1\", carId:\"car_1\") { orders { state } } }"}'

# East three times to Pizza Palace (x=6), then west three times and south
# three times to Anna's Flat (3,6). Verified against the shipped 22x22 map.
for d in EAST EAST EAST WEST WEST WEST SOUTH SOUTH SOUTH; do
  q "{\"query\":\"mutation { moveCar(id:\\\"demo\\\", carId:\\\"car_1\\\", direction: $d) { tick } }\"}"
  q '{"query":"mutation { tick(id:\"demo\", count:1) { tick } }"}'
done

q '{"query":"query { session(id:\"demo\") { ledger { money deliveries } orders { state paid } } }"}'
```

Note the quantities: the first order is not necessarily one box, so `reward`
and the payload differ per seed. Adding `quantity` to the order selection above
shows what the job actually pays for.
