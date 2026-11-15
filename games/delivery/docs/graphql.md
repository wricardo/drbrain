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

**Sessions start paused.** This is a manual-driving game; a live clock would
expire orders while the player was reading the screen. Unpause with `setSpeed`,
or step deterministically with `tick`.

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
    tick paused
    city { rows hiddenTiles }
    cars { id className x y battery maxBattery batteryPct state cargo queuedMove }
    orders { id itemName kitchenName customerName state reward ticksLeft }
    ledger { money reputation deliveries onTimeStreak }
    carClasses { className price capacity maxBattery unlocked affordable }
    achievements { id title done progress target }
    unlocks { id title earned builds }
    builds { id title cost unlocked completed affordable applicable problem }
    events { tick type message }
  }
}
```

### `carView` — the cheap poll

For an agent or a client watching a large map, fetching the whole snapshot every
step is wasteful. `carView` returns a window around one car:

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
`rows[0][0]` sits in city coordinates, and `radius` is clamped to 10.

### `events` — incremental deltas

```graphql
query { events(id: "demo", sinceTick: 42) { tick type message } }
```

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
  towCar(id: "demo", carId: "car_1") { cars { state } }
  build(id: "demo", buildId: "b02_east_bridge") { city { rows } }

  saveSession(id: "demo") { tick }
  evictSession(id: "demo")   # save, then drop from memory
  deleteSession(id: "demo")  # remove the save file
}
```

Every mutation returns the **full snapshot**, so a client never needs a follow-up
read to know the new state.

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
- `RejectReason` — `BAD_DIRECTION` `OFF_GRID` `BLOCKED` `NO_BATTERY` `NOT_DRIVABLE` `PAUSED`

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
| `1`–`9` | car by index | — |

## A complete delivery

```bash
G=http://localhost:8080/graphql
q() { curl -s -X POST $G -H 'Content-Type: application/json' -d "$1"; }

q '{"query":"mutation { createSession(id:\"demo\", seed:7) { id paused } }"}'
q '{"query":"mutation { tick(id:\"demo\", count:1) { orders { id state } } }"}'
q '{"query":"mutation { assignOrder(id:\"demo\", orderId:\"ord_1\", carId:\"car_1\") { orders { state } } }"}'

# East twice to the kitchen, then west twice and south three times to the customer.
for d in EAST EAST WEST WEST SOUTH SOUTH SOUTH; do
  q "{\"query\":\"mutation { moveCar(id:\\\"demo\\\", carId:\\\"car_1\\\", direction: $d) { tick } }\"}"
  q '{"query":"mutation { tick(id:\"demo\", count:1) { tick } }"}'
done

q '{"query":"query { session(id:\"demo\") { ledger { money deliveries } orders { state paid } } }"}'
```
