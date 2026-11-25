# DrBrain Delivery — GraphQL API

GraphQL is the **only** transport. There is no REST surface. The canonical schema
is `graph/schema.graphqls` at the repository root; both the Go server types and
the TypeScript client types are generated from it.

- Endpoint: `POST /graphql`
- Agent guide: `GET /delivery/llms.txt` (rendered by the platform from `llms.txt.tmpl`)
- Playground: `GET /graphql/playground` (interactive query editor)
- Schema: [`graph/schema.graphqls`](../../../graph/schema.graphqls) (canonical);
  `games/delivery/graph/schema.graphqls` is generated

```bash
just restart       # regenerate schema, build web + binary, serve on :8181
just regen         # after any schema edit
go run ./cmd/gentypes -check   # TypeScript client types are current
```

## Generated types, both directions

| Direction | Generator | Output |
|---|---|---|
| schema → Go | gqlgen | `graph/generated/`, `graph/model/` |
| schema → TypeScript | `cmd/gentypes` | `web/src/lib/graphql-types.ts` |

Neither side hand-maintains a copy of the schema. `go run ./cmd/gentypes -check`
and `go run ./cmd/split-schema -check` fail when generated output is stale, so a
schema change without regeneration breaks the build instead of silently shipping
a lying client.

**Queries go through the `delivery` namespace; mutations are flat root fields.**
Wrap a query in `delivery { … }` (`query { delivery { … } }`). Every mutation is
a top-level `Mutation` field named `delivery` + the capitalized operation
(`mutation { deliveryMoveCar(…) { … } }`); root mutation fields run serially, so
aliased mutations in one document keep their order. The mutation names in the
prose below (`moveCar`, `setSpeed`, …) are the operation names without the
`delivery` prefix. Subscriptions stay
flat at the operation root: `subscription { deliverySession(id: "…") { … } }`.

## Commands vs. the clock

**A move does not move the car.** `moveCar` appends one step to a car's movement
queue; the tick loop applies at most one queued step per car per tick. Each car
has its own queue, so driving several cars by hand is unambiguous, and an agent
can preload a route for the running clock. A car may hold up to 64 queued
steps (`LIMIT_REACHED` beyond that); `clearMoves` empties the queue when traffic
or a closure invalidates the plan. A rejected step is spent, not retried.

**Every session starts paused, including a restored one.** This is a
manual-driving game; a live clock would expire orders while the player was
reading the screen. Unpause with `setSpeed`: the clock is the only thing that
advances the world.

**CHANGED (was an asymmetry, now uniform):** loading a save used to restore the
`speed` stored in the save and start ticking immediately. It now always comes
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
`tickMs / speed`. Do not treat `tick` as a wall clock; use `waitUntil` when you
need exact tick boundaries.

**Saving is implicit in more places than the name suggests.** `saveSession` is
the explicit one, autosave runs every `autosave_ticks` world ticks (60 on the
shipped map), and two lifecycle actions persist too. Saves are
`platform_sessions` rows in PostgreSQL; there is no file on disk:

| action | writes a save? |
|---|---|
| `saveSession` | yes, obviously |
| autosave | **yes** — every `autosave_ticks` while the clock runs; also on pause and close |
| `evictSession` | **yes** — it closes (and saves) the session, then unloads it; the next read rehydrates the row |
| server SIGINT/SIGTERM | **yes** — `Manager.Close` → `SaveAll` before exit |
| `deleteSession` | no; it deletes the row |
| ordinary ticking | not on every tick: the autosave interval decides |

Verified: created a session, ticked it past the autosave interval without calling
`saveSession`, called `evictSession`, and a reload came back at the same tick.
So "evict to throw away my scratch game" does not work — use `deleteSession` for
that. A crash still loses everything since the last autosave or explicit save.

## Query

```graphql
query {
  health { status }

  # Published cities plus your own drafts; another player's draft is never listed.
  cities { id name gridSize status ownedByMe builtin ownerless }

  # Static city with every district revealed; omit id for the default city.
  city(id: "city") { id name gridSize status ownedByMe builtin ownerless rows hiddenTiles kitchens { id name x y } }

  # Exact authored city JSON (decoded/defaulted); null for missing or another player's draft.
  cityConfig(id: "city")

  # Saves plus live sessions. sort: CREATED_AT|TICK|MONEY; limit <= 500; before is a createdAt cursor; mine keeps the viewer's saves (UNAUTHENTICATED when signed out).
  sessions(sort: CREATED_AT, limit: 50) { id createdAt displayName tick money reputation deliveries loaded ownerDisplayName mine }

  # One analytics series per field, read from the indexed delivery history projection.
  # Metrics: MONEY, BOXES_DELIVERED, REPUTATION, DELIVERIES, LATE_DELIVERIES,
  # EXPIRED_ORDERS, ORDERS_CREATED, DELIVERY_INCOME, ORDER_FINES, UPKEEP_SPEND,
  # CHARGING_SPEND, TOW_SPEND. Up to 20 sessions; maxPoints downsamples each series.
  money: metricHistory(metric: MONEY, ids: ["demo"], fromTick: 0, toTick: 5000, maxPoints: 500) {
    sessionId cityId points { tick value }
  }
  boxes: metricHistory(metric: BOXES_DELIVERED, ids: ["demo"], maxPoints: 500) {
    sessionId cityId points { tick value }
  }
  reputation: metricHistory(metric: REPUTATION, ids: ["demo"], maxPoints: 500) {
    sessionId cityId points { tick value }
  }

  # Per-tick world frames recorded for one session (the replayable tick log).
  # Ordered by tick ascending; fromTick/toTick are inclusive and afterTick is an
  # exclusive keyset cursor. limit defaults to 100 and is capped at 500.
  # A missing or hidden session is NOT_FOUND; a disabled log returns [].
  tickSnapshots(sessionId: "demo", fromTick: 100, toTick: 400, limit: 50) {
    sessionId tick recordedAt money moneyEarned reputation
    deliveries lateDeliveries expiredOrders ordersSpawned onTimeStreak tows upkeepPaid towFees
    deliveriesByItem { item count }
    cars { id name className x y battery cargo chargeDue queuedMove lastReject stepsTaken }
    orders { id item quantity kitchenId customerId assignedCar state createdTick deadlineTick deliveredTick reward penalty paid late }
  }

  # One session's full state. Loads from PostgreSQL when not in memory.
  session(id: "demo") {
    displayName tick paused tickMs eventCapacity
    city {
      name gridSize rows hiddenTiles
      kitchens { id name x y } customers { id name x y }
      depots { id name x y } chargers { id name x y }
    }
    cars { id className x y battery maxBattery batteryPct capacity load freeBoxes state cargo queuedMove lastReject chargeDue }
    # openOrders = PENDING|ASSIGNED|PICKED_UP. orders() filters the history, newest first.
    openOrders { id itemName quantity kitchenId kitchenName customerId customerName state reward ticksLeft paid late assignedCar createdTick deadlineTick }
    orders(state: [DONE], sinceTick: 0, limit: 50) { id state }
    ledger { money moneyEarned reputation payMultiplier deliveries lateDeliveries expiredOrders ordersSpawned onTimeStreak tows deliveriesByItem { item count } upkeepPaid towFees upkeepDue ticksPerMonth cashEntries { seq tick kind category amount balance carId carName orderId memo } months { index startTick endTick income expenses net categories { category income expenses } } }
    carClasses { className price capacity maxBattery stepCost unlocked affordable }
    growth { id tick title description done }
    closures { id reason startTick endTick ticksLeft minX minY maxX maxY tiles { x y } }
    events { tick type message carId x y }
  }
}
```

`closures` is the street works live right now — empty in the common case. A
closed tile renders `X` and rejects entry with `CLOSED`, but a car already
standing on it may drive off.

`growth` is the city's expansion timeline: stages in tick order, each with the
tick it fires at, a title/description, and `done` once it has applied. Nothing
in it is purchasable — the city opens districts, adds customers, kitchens and
chargers, and adds depot charging bays on its own clock. Each stage that applies
raises `Chapter()`, which drives reward and deadline tables; quantity bands and
customer cooldowns scale by session tick as a percentage of `ramp_ticks`. The
scooter and van bands also carry a small early share from tick 0, so 1–3 and
5–24 box work exists at every stage; a band's `from_progress` is where its
share jumps. The 30–40 box truck band has no early share and starts at its
gate, so it cannot appear before a truck is affordable.

`capacity`, `load` and `freeBoxes` are measured in **boxes**, not orders. A
customer can ask for forty pizzas; whether that fits is a property of the
vehicle. `assignOrder` counts both the boxes the car carries and the boxes
promised to its other `ASSIGNED` orders, so several orders fit on one car only
while their total does; the error names the carried, assigned and capacity
figures. `quantity` on an order is that box count.

`chargeDue` is the unpaid balance of a charging session in progress, in
dollars. It is zero when the car is not on a paid charger and after the session
settles; the fleet panel uses it to show the running bill before the single
`CHARGING` statement row lands.

### Reading order state

`assignedCar` is **not** cleared when a job finishes: a delivered order keeps the
id of the car that delivered it. Use `state` to decide what is in flight —
`PENDING` `ASSIGNED` `PICKED_UP` are live, `DONE` `EXPIRED` are settled — and read
`assignedCar` as history. `paid` and `late` only become meaningful once `state`
is `DONE`.

`ticksLeft` is signed. An order that is past its deadline but has not been swept
by the tick loop yet reports a negative value instead of clamping to zero, so
"due this tick" and "already missed" stay distinguishable.

`openOrders` is the actionable list: every `PENDING`/`ASSIGNED`/`PICKED_UP` job,
newest created first. `orders` takes filters and defaults to the **full**
history, which is what a replay or audit wants:

```graphql
session(id: "demo") {
  openOrders { id state ticksLeft }
  orders(state: [DONE, EXPIRED], carId: "car_1", sinceTick: 4000, includeFinished: false, limit: 50) { id state paid late }
}
```

`limit` defaults to 2000 and is capped there; `sinceTick` compares against
`createdTick`; `carId` matches `assignedCar` (a delivered order keeps its car).
Finished history is roughly 96% of a long save and never shrinks, so selecting
it per frame is the difference between a small and a multi-megabyte payload.

### `lastReject`, `queuedMove` and `queuedMoves` timing

`moveCar` only appends to the queue; it does not touch `lastReject`, and it
returns just that car (`CarView`). So immediately after `moveCar` you still see
the *previous* rejection, and only a world tick replaces it. `queuedMove` is the head — the step for the next tick —
while `queuedMoves` is the whole plan in execution order. Verified on the
shipped map:

```
car at (3,2), lastReject null
moveCar WEST   -> lastReject null, queuedMove "WEST", queuedMoves ["WEST"]
moveCar SOUTH  -> lastReject null, queuedMove "WEST", queuedMoves ["WEST","SOUTH"]
world tick     -> lastReject "BLOCKED", queuedMove "SOUTH", queuedMoves ["SOUTH"]
world tick     -> (3,3), lastReject null, queuedMove null, queuedMoves []
```

A client that wants to explain *this* refusal must read the snapshot returned by
`waitUntil` (or a `session` query), not the car `moveCar` returned. `lastReject` is also sticky in a second
sense: it stays set until a later tick either rejects again with the same reason
or lets a step succeed, so it is "the most recent failure", not "the failure of
the last command". A rejected step is consumed: the next tick tries the next
queued step, so replan with `clearMoves` rather than assuming the head retries.

### Ledger counters

| field | counts |
|---|---|
| `money` | spendable balance (goes down when you buy) |
| `moneyEarned` | lifetime earnings, never decreases — the economy reports on this |
| `deliveries` | on-time deliveries only |
| `lateDeliveries` | delivered past the deadline, at reduced pay |
| `expiredOrders` | never delivered |
| `ordersSpawned` | every order ever raised, including expired ones |
| `onTimeStreak` | consecutive on-time deliveries; late and expired both reset it |
| `deliveriesByItem` | per-item **order** counts, not boxes — a two-box order adds 1 |
| `payMultiplier` | share of a quoted reward the fleet collects, from its reputation (see below) |
| `upkeepPaid` | lifetime fleet running cost paid; cumulative, **not** subtracted from `moneyEarned` |
| `towFees` | lifetime spend on tows; cumulative |
| `upkeepDue` | what the next upkeep interval would charge at the cars' current positions |
| `ticksPerMonth` | length of one accounting month, currently 600 ticks |
| `months` | monthly income/expense report, newest first; default window 3 months, filterable |
| `cars` | per-car income/expenses/net/rows for a window; world costs excluded |

`deliveries + lateDeliveries + expiredOrders` equals `ordersSpawned` minus the
orders still live, so a client that wants "jobs that failed" must not read
`deliveries` alone.

### Reputation is the pay rate

Reputation is clamped to `0..max_reputation` (5 on the shipped maps). It moves
`+0.02` per on-time delivery, `-0.05` per late delivery, and up to `-0.10` per
expired order — the expiry penalty scales with standing, so it is the full 0.10
at 1.0 and above, half at 0.5, a fifth at 0.2, and nothing at 0. A collapse is
therefore recoverable: at 0.2 a miss costs exactly one delivery's worth of
standing, so any delivery advantage climbs. A growth stage can also grant it. What it buys is the multiplier
customers pay:

| reputation | payMultiplier |
|---|---|
| 4.90 – 5.00 | 1.0 |
| 4.50 – 4.89 | 0.9 |
| 4.00 – 4.49 | 0.8 |
| 3.50 – 3.99 | 0.7 |
| 3.00 – 3.49 | 0.6 |
| 2.50 – 2.99 | 0.5 |
| 2.00 – 2.49 | 0.4 |
| 1.50 – 1.99 | 0.3 |
| 1.00 – 1.49 | 0.2 |
| 0.50 – 0.99 | 0.1 |
| 0.01 – 0.49 | 0.1 (floor) |
| exactly 0.00 | 0.0 |

A new session opens at the top of the scale (`max_reputation`, 5.00), so its
first deliveries pay in full; the ladder is what expiries and late dropoffs
spend.

The bottom of the ladder is a floor, not a cliff: any standing above zero still
earns a tenth, so a fleet that let its reputation collapse can keep working,
buy capacity and climb back. Only an exactly-zero standing pays nothing — and a
single delivery lifts it off zero. (An earlier version paid 0% across the whole
bottom band; on a 6k-tick run that deadlocked the economy, because zero income
meant no new trucks, which meant the expiries that held the standing at zero.)

An on-time delivery pays `round(reward × payMultiplier)`; a late one pays
`round(reward × late_pay_multiplier × payMultiplier)`. A zero payout records no
statement row (a movement of nothing is not a line), but the order still counts
as a delivery and still adds `+0.02` reputation — free runs are the way back into
a paid band. Holding a band costs five deliveries for every order let rot.

### Cash statement

`cashEntries` is the checking account behind `money`, newest first. Every
movement of money has one row:

| field | meaning |
|---|---|
| `seq` | stable id within the session; use it as the list key |
| `tick` | tick the movement landed |
| `kind` | `OPENING` `DELIVERY` `LATE_DELIVERY` `ORDER_FINE` `UPKEEP` `CHARGING` `TOW` `CAR_PURCHASE` `BUILD` `GRANT` |
| `category` | monthly bucket: `DELIVERY_INCOME` `REWARDS` `ORDER_FINES` `UPKEEP` `CHARGING` `TOWS` `CAR_PURCHASES` `BUILDS` `OTHER` `OPENING` |
| `amount` | signed dollars: positive in, negative out |
| `balance` | running balance **after** the movement |
| `carId` / `carName` | the vehicle the movement belongs to, when it belongs to one |
| `orderId` | the order behind a delivery or fine, when there is one |
| `memo` | short human-readable detail, e.g. `pizza to Rosa's` |

Car tags are set for deliveries (the delivering car), upkeep (the car billed),
charging sessions (the car charged), tows (the towed car) and car purchases
(the car bought). Growth grants and opening balances are fleet-wide and leave
`carId` null. `carName` is a snapshot, so a statement row stays readable even if
the fleet changes.

`amount` is **what was actually charged**, not what was billed. A `$25` tow
against a `$5` balance records `-5`, and a charge against an empty balance
records no row at all — the balance never goes negative. That keeps the
statement chain self-consistent: each `balance` is the previous one plus
`amount`.

The engine keeps the full statement in the save. A snapshot carries the rows of
the newest three accounting months, capped at 200, and the live `deliverySession`
stream carries **the same window** — a subscriber never sees a narrower account
than a reader. Old rows stay in the save and are reachable through the filters:

```graphql
ledger {
  cashEntries(month: 2, fromTick: 600, toTick: 1199, category: TOWS, carId: "car_1", limit: 200) { seq tick amount balance memo }
  months(month: 2) { index income expenses net categories { category income expenses } }
  cars(month: 2) { carId carName income expenses net rows }
}
```

`month` is 1-based and overrides the tick window; `fromTick`/`toTick` are
inclusive and intersect `month`; `category` keeps one bucket; `carId` applies to
statement rows only; `limit` caps the newest rows (1000 max). A month that has
not started is an error (`bad_request: month 9 has not started (current month
8)`), not an empty page. `cars` aggregates the same window per car and leaves
out rows with no `carId` (upkeep, world costs), so its totals do not sum to the
month's `net`.

### Monthly report

`months` aggregates the statement by `ticksPerMonth` (600 ticks). Each month
has positive `income` and `expenses` magnitudes, `net`, and category totals:

```graphql
months {
  index startTick endTick
  income expenses net
  categories { category income expenses }
}
```

Months are newest first and a snapshot carries the newest three by default; the
`month`, `fromTick`/`toTick` and `category` arguments read further back or narrow
to one bucket (a category-filtered month recomputes its totals from that bucket).
Quiet months are
included with zero totals so month numbers do not skip. The first month starts
at tick 0, so month 1 is `startTick: 0, endTick: 599`. Opening capital is
excluded from monthly income; the statement row remains visible in
`cashEntries`. The engine derives the report from `cashEntries` and rebuilds it
on load, so old saves need no migration.

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

It has **no customers either**: `customers` is empty and every house renders
`h`. Customers are live state (a site raises an order, appears as `H`, and
leaves when that order is delivered or expires), so there is nothing to show
without a session. Read authored dropoff addresses from `city.json` if you need
content, and from `session.city.customers` if you need who is waiting right now.

The resolver loads the selected city config on every call (a builtin file or a
`platform_maps` row) rather than reusing a live session's, so `Query.city` cannot cost a
running game its fog. A builtin city therefore reflects the release file as it is on disk
right now, even if a session was created from an older version of it; a custom city reflects
its current database row.

### Glyphs

`?` unexplored · `#` water · `B` building · `.` road · `,` ground (costs 2
steps to enter — slower to drive on, but does **not** charge) ·
`D` depot (charges 6/tick, free) · `C` charger (charges 2/tick, paid per the
map's `charging.charger_price_per_tick`) · `K` kitchen ·
`H` customer waiting · `h` house, nobody home · `X` closed for works

Only `D` and `C` recharge. `CellType.Charges()` is `depot || charger`, so a
car parked on ground — however it looks — never recovers battery.

Customers are not permanently on the map. A site raises an order, at which
point it appears as `H` and is listed in `city.customers`; when the order is
delivered or expires the customer leaves and the tile drops back to `h`. Spawns
are drawn at random among the eligible sites (cooldown elapsed, no live order,
not hidden, reachable kitchen), so a reveal build is what lets the customers of
a locked district start appearing.

`1`–`9` car by index. Closures are drawn under cars: a car parked on a closed
street still renders as a car.

Three limits matter when you draw the map yourself:

- **Only the first nine cars get a glyph.** The renderer stops at `i >= 9`, so a
  tenth car is simply absent from `rows` while still present in `cars`.
- **Co-located cars collapse.** A depot holds any number of cars (fleet size is
  money-limited), so they share one tile and the last one's digit wins. Measured
  with four cars parked at the depot: four cars, one glyph (`4`). The UI
  compensates with a `×N` badge and cycles selection on click; a client drawing
  its own map should read `cars` for the full list.
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
includes `DONE` or `EXPIRED`. To find *this* car's work, read
`session.orders(carId: "car_1")` (or `openOrders` and match `assignedCar`).

Proof by contradiction, run live: with `ord_1` assigned to `car_1` and `car_1`
at (3,3), `carView(car_1, radius: 0)` returns `nearbyOrders: []` while
`radius: 10` returns `ord_1`. Nothing about the order's ownership changed — only
whether the kitchen/customer fell inside the window.


### `events` — incremental deltas

```graphql
query {
  events(id: "demo", sinceTick: 42, limit: 200) {
    oldestTick truncated capacity
    events { tick type message carId x y }
  }
}
```

`sinceTick` is **inclusive**: `sinceTick: 9` returns every event stamped tick 9,
so a client that stores `lastTick` and re-polls with it will see the last tick's
events twice. Poll with `lastTick + 1`, or dedupe on `(tick, type, message)`.

The feed is a ring, not a log: it retains the most recent `max_events` events
(500 on the shipped map, `session.eventCapacity`) and drops the oldest. The
response now says when that happened instead of leaving a client to guess:

| field | meaning |
|---|---|
| `oldestTick` | tick of the oldest retained event, or the current tick when the feed is empty |
| `truncated` | `true` when the request reached past the retained ring, so events before `oldestTick` are gone rather than absent |
| `capacity` | how many events the ring retains (`max_events`) |
| `events` | matching events, oldest first; `limit` (default 200) keeps the newest when more match |

A `truncated: true` means "resync from `session`"; `truncated: false` with an
empty list honestly means nothing happened in that window.

Event `type` values, all of which the engine emits:

| type | when |
|---|---|
| `car_blocked` | a step was refused; `lastReject` on the car says why |
| `car_charged` | a tick of charging landed (also used for a completed tow) |
| `car_stranded` | battery hit zero off a charging tile |
| `car_bought` | a purchase succeeded |
| `order_spawned` | a customer raised an order. A `order_starved: …` message shares this type but the branch that emits it is unreachable today — an unsupplied customer goes silent instead (see `docs/known-issues.md` #4) |
| `order_picked_up` | a car collected from a kitchen |
| `order_delivered` | a dropoff settled payment |
| `order_expired` | the deadline passed undelivered, and the penalty was charged |
| `city_growth` | a scheduled growth stage applied (or was rejected) |
| `closure_started` / `closure_ended` | street works began or reopened |

Every event carries `tick` and a human-readable `message`; `carId` is null for
world events, and `x`/`y` are `0,0` when the event has no position — they are
plain ints, so a client cannot distinguish "at the origin" from "no position".

A successful step is **not** an event. Movement is the most frequent thing a
session does and is visible on the map, so the feed stores outcomes and
blockers only; read car coordinates from `session.cars`. Saves written before
this rule are cleaned of their `car_moved` rows on load.

## Mutation

Every state-changing mutation needs the session's **owner token** — the secret
`createSession` returns. Reading is public; changing the world is not. A
signed-in creator may use their login instead, and a save written before owner
tokens stays open. A caller with neither gets `forbidden: ...`; an absent
session is `not_found:` regardless of the credential. Session and statement
errors keep those message prefixes **and** now carry `extensions.code`
(`FORBIDDEN`, `NOT_FOUND`, `INVALID_ARGUMENT`, `CONFLICT`), so a client can
branch on the code instead of matching text.

```graphql
mutation {
  # Returns the session AND the secret that proves you created it. Keep it. cityId picks the city snapshot; omit it for the default.
  deliveryCreateSession(id: "demo", seed: 7, cityId: "city") {
    session { id tick paused }
    ownerToken
  }

  # Append one step to the car's queue. Does NOT advance the clock.
  deliveryMoveCar(id: "demo", carId: "car_1", direction: SOUTH, ownerToken: "…") { x y queuedMove queuedMoves }

  # Drop the whole queue, including the next step.
  deliveryClearMoves(id: "demo", carId: "car_1", ownerToken: "…") { queuedMove queuedMoves }

  deliverySetSpeed(id: "demo", speed: 0, ownerToken: "…") { paused }  # 0 pauses; 1/2/4 run

  # Block until the running clock reaches a target. Exactly one of tick/ticks;
  # ticks is capped at 1000. Paused -> INVALID_ARGUMENT, including mid-wait.
  deliveryWaitUntil(id: "demo", ticks: 5, ownerToken: "…") { tick paused }

  deliveryAssignOrder(id: "demo", orderId: "ord_1", carId: "car_1", ownerToken: "…") { orders { state } }
  deliveryUnassignOrder(id: "demo", orderId: "ord_1", ownerToken: "…") { orders { state } }

  deliveryBuyCar(id: "demo", className: "van", depotId: "d_main", ownerToken: "…") { cars { id className } }
  # Buy and build a charger on tile (x, y): revealed open road/ground, no building or car on
  # it, reachable from a depot. Price and cap come from the city file and are
  # readable as city.charging.chargerPurchasePrice / maxPurchasedChargers
  # (cap 0 disables).
  deliveryBuyCharger(id: "demo", x: 3, y: 4, ownerToken: "…") { city { chargers { id x y } } ledger { money } }
  # Sell and bank 70% of the original purchase price; keeps the last car and
  # refuses a car that is carrying cargo or has an order assigned.
  deliverySellCar(id: "demo", carId: "car_1", ownerToken: "…") { cars { id } ledger { money } }
  # Costs tow_fee_per_tile x route distance (min tow_fee_min), clamped to the balance.
  deliveryTowCar(id: "demo", carId: "car_1", ownerToken: "…") { ledger { money towFees } cars { state } }

  # The city grows on its own schedule: there is no build mutation.

  deliverySaveSession(id: "demo", ownerToken: "…") { tick }
  # Name this save. Blank clears it; every view then falls back to the city name. Persisted immediately.
  deliveryRenameSession(id: "demo", displayName: "Fleet run", ownerToken: "…") { displayName }
  deliveryEvictSession(id: "demo", ownerToken: "…")   # save, then drop from memory
  deliveryDeleteSession(id: "demo", ownerToken: "…")  # remove the save row
}
```

Every state-changing mutation returns the **full snapshot**, so a client never
needs a follow-up read to know the new state. The two exceptions are
`deliveryEvictSession` and `deliveryDeleteSession`, which return a bare `Boolean` because the
session they would have described no longer exists — verified live (`true` for
each, with no snapshot in the payload).

An omitted `deliveryCreateSession` id is generated (16 random hex characters); a
client-supplied id is accepted when it is 1-64 characters of `[A-Za-z0-9_-]`.
A signed-in creator's session belongs to their account automatically.

### Admin

`Mutation.deliveryAdmin(key)` is the operator path for saves that no longer belong to a
caller. It validates `key` against `DELIVERY_ADMIN_API_KEY` (the same secret
`X-Admin-Key` carries for city writes), so a wrong or missing key is `FORBIDDEN`
before any id is looked at. When no key is configured, only
`DELIVERY_ALLOW_UNAUTHENTICATED_ADMIN=true` (local development) accepts it.

```graphql
mutation AdminDelete {
  deliveryAdmin(key: "…") {
    # Permanently removes the save, its live session and its tick log.
    # Bypasses ownerToken/account checks; idempotent (unknown id returns true).
    deleteSession(id: "demo")
  }
}
```

The same object carries operational verbs, all authorized by the key validated
at `deliveryAdmin(key)`:

| field | effect |
|---|---|
| `pauseSession(id)` | `SetSpeed(0)` without an owner token; returns the paused snapshot and flushes the pause to the store |
| `evictSession(id)` | save a live session, then drop it from memory (the save stays readable) |
| `saveSession(id)` | persist one live session; a stored-only save is a no-op success |
| `saveAll` | persist every live session, returning how many were flushed |
| `deleteSessions(ids: [ID!]!)` | bulk delete; returns `{ deleted, missing }`, deduplicates ids, caps at 500, and rejects the whole batch on an invalid id |

`deleteSessions` classifies `deleted` vs `missing` from a list of live plus
stored sessions taken before any delete, so it is best-effort under concurrent
writes.

Prefer the `X-Admin-Key` header where the client can set headers: the argument
travels in the GraphQL document, so it may end up in request logs.

### City catalogue and authoring

`Query.cities` lists every PUBLISHED city plus the signed-in caller's own DRAFTs; another
player's draft is never listed. `Query.city(id)` selects one (an omitted id is the default
city from `DELIVERY_CITY`) and returns `null` for another player's draft, exactly like an
unknown id. Every entry carries `status` (`DRAFT`/`PUBLISHED`), `ownedByMe`, `builtin`
(shipped file) and `ownerless` (community row with no owner, imported/legacy). Builtins and
ownerless cities are admin-managed; another player's owned city is never an admin path.

A draft is private to its owner: the owner may play and publish it, while another player
cannot see or select it. A session created on a draft city inherits that visibility — guests
and other players get NOT_FOUND from `session`, `carView`, `events`, `canPlaySession` and the
`deliverySession` subscription, and the save is omitted from `sessions`. Published and
builtin-city sessions remain public. Deleting a city deletes the sessions created on it
(live and saved), so a draft's private saves can never become public through deletion;
deleting an account deletes the sessions on that account's drafts before the drafts go.

`Query.cityConfig(id)` returns the exact authored city JSON for a published city, a builtin,
or the caller's own draft, and `null` for a missing city or another player's draft. It is the
editor's read: the server returns its decoded, defaulted document, so the keys match the ones
players write and unknown fields cannot survive a round trip. Guests can read published and
builtin cities; the authoring mutations still require sign-in.

`deliveryCreateSession(cityId: "…")` snapshots that city; an omitted `cityId` uses the default. The
session keeps the snapshot, so later city edits never change a running game. A DRAFT city
cannot be started, played or published by anyone but its owner.

```graphql
mutation {
  # Signed-in only: stores a DRAFT owned by the caller. config is raw city JSON.
  deliveryCreateCity(id: "harbour", config: "{ ...city JSON... }") { id name status ownedByMe }

  # Owner-only; replaces the config and puts the city back into DRAFT.
  deliveryUpdateCity(id: "harbour", config: "{ ...city JSON... }") { id status }

  # Owner-only; runs the full startup validation (grid, districts, orders, reachability).
  deliveryPublishCity(id: "harbour") { id status }

  # Stores nothing; signed-in only.
  deliveryValidateCity(config: "{ ...city JSON... }") { valid publishable issues }

  # Owner-only for community cities; shipped/legacy ownerless cities need X-Admin-Key.
  deliveryDeleteCity(id: "harbour")
}
```

`deliveryCreateCity`, `deliveryUpdateCity`, `deliveryPublishCity`, `deliveryDeleteCity` and `deliveryValidateCity` need a signed-in
viewer (`UNAUTHENTICATED` otherwise). `deliveryCreateCity` never overwrites a builtin city. Shipped
cities are builtin files and always PUBLISHED; changing or deleting one, or editing a legacy
ownerless custom city, needs the operator `X-Admin-Key`. `valid` means the JSON parses into a
city; `publishable` means `publishCity` would accept it. An invalid publish is
`INVALID_ARGUMENT`.

Custom cities live in PostgreSQL (`platform_maps`), capped by `DELIVERY_MAX_USER_CITIES`
(default 200). `DELIVERY_CITIES_DIR` points at the builtin city files (release-managed), and
`DELIVERY_CITY` is the legacy default file and must live inside that directory. `pg_dump` is
the only backup for custom cities. Legacy custom city files, if any, are imported once with
`drbrain import-maps --game=delivery --dir=<legacy dir>`; imported rows are PUBLISHED and
ownerless, so they remain admin-managed until the schema grows an ownership claim.

### Ownership, claiming and the save list

```graphql
query Saves {
  delivery {
    # Reads are public: anyone with the id may watch. canPlaySession says whether
    # this caller may also change it.
    canPlaySession(id: "demo", ownerToken: "…")
    session(id: "demo") { tick cars { id x y } }
    sessions(sort: TICK, limit: 20, mine: true) {
      id displayName ownerDisplayName ownerHandle mine loaded tick money
      cursor   # opaque page position; pass to the next call
    }
  }
}

mutation Claim {
  delivery {
    # Attach guest saves to the signed-in account. Each needs its own token.
    claimSessions(claims: [{ sessionID: "demo", ownerToken: "…" }]) {
      sessionID
      status   # ATTACHED | ALREADY_YOURS | REJECTED
    }
  }
}
```

`sessions` takes `sort` (`CREATED_AT` default, `TICK`, `MONEY`), `limit`
(default 50, capped at 500), `cursor` and `mine` (`true` keeps the signed-in
viewer's saves; signing out makes it `UNAUTHENTICATED`). Page with the `cursor`
of the last row you saw:

```graphql
query NextPage($cursor: String) {
  delivery { sessions(sort: TICK, limit: 20, cursor: $cursor) { id tick cursor } }
}
```

A `cursor` encodes the sort and the row id, so it is exact for every sort and
never skips or repeats rows that share a tick, money value or creation time;
using it with a different `sort` is `INVALID_ARGUMENT`. The older `before: Time`
argument still filters by creation time but is only valid with the default
`CREATED_AT` sort (it cannot break ties), and cannot be combined with `cursor`.
Each row comes from the save's stored counters, so a bounded list does not
decode save payloads.

`ownerDisplayName` is the owning account's display name, `Private player` when
its profile is private, or `Guest` for a session with no account. `displayName`
is the player's own name for the save, empty when unnamed — clients fall back to
the city name. `ownerHandle`
is null for guests and private profiles, and `mine` is true when the row belongs
to the signed-in viewer. A save made before owner tokens can never be claimed
and has no display name beyond `Guest`; it stays open for anyone with its id.

`metricHistory` is the analytics read. It never rehydrates a session: it reads
the indexed `delivery_sessions` and history projections, which the save and
autosave paths maintain. Omitted `ids` selects the newest saves; `cityId`
narrows the set; `fromTick`/`toTick` are inclusive. `maxPoints` (default 500,
capped at 2000) buckets each series by tick. Balances (`MONEY`, `REPUTATION`)
keep the last value in each bucket; count and money-flow metrics accumulate a
running total. The session's current value is appended when the window reaches
its current tick.

The metrics:

- `MONEY` — cash balance from every checking-account movement.
- `BOXES_DELIVERED` — cumulative boxes from `done` orders in
  `delivery_order_history`; expired and open orders do not count.
- `REPUTATION` — recorded standing from `delivery_reputation_entries`.
  Sessions saved before that ledger existed return one current point until
  their next reputation change, because the old history cannot be reconstructed
  from order outcomes alone.
- `DELIVERIES`, `LATE_DELIVERIES`, `EXPIRED_ORDERS`, `ORDERS_CREATED` —
  cumulative order counts; expiry is counted just after the deadline.
- `DELIVERY_INCOME`, `ORDER_FINES`, `UPKEEP_SPEND`, `CHARGING_SPEND`,
  `TOW_SPEND` — cumulative cash by category. Spend is a positive magnitude.
  `ORDER_FINES` reads the cash ledger, so it reports the fine actually charged
  after the balance clamp, not the nominal penalty.


`/delivery/analytics` overlays up to six sessions on a shared tick axis.

`tickSnapshots` is the replay read: one frame per completed tick, written from
the engine tick hook into `delivery_tick_snapshots`. Unlike `metricHistory` it
does rehydrate the session first, so city visibility and ownership rules apply
and a missing or hidden session is `NOT_FOUND`; the tick log itself is optional
infrastructure, and a deployment with `DELIVERY_TICK_SNAPSHOTS=0` returns an
empty list. Frames carry the mutable state (cars, orders, balances, counters)
and deliberately omit the append-only cash/reputation statements and event
feed. Filtering and paging happen in SQL: `fromTick`/`toTick` are inclusive,
`afterTick` is exclusive and wins over `fromTick` when higher, and pass the last
returned tick as `afterTick` for the next page. Retention keeps the newest
`DELIVERY_TICK_SNAPSHOTS` ticks (default 1800) per session, so an old tick that
was pruned is absent rather than an error.

## Subscription — live updates instead of polling

```graphql
subscription { deliverySession(id: "demo") { tick paused cars { id x y } } }
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

**The order payload is not the same as the query's.** `deliverySession`
trims finished orders to the most recent 20 (`streamHistory` in
`game/session/watch.go`); the query path keeps the history (up to the orders
default cap of 2000 rows). Order history
is ~96% of a late-game snapshot and never shrinks — a 42k-tick session carries
~4,200 orders, of which most are DONE or EXPIRED — so streaming all of it every
tick would cost more than polling did. Measured on that session: 1.4 MB on the
query path versus 63 KB per stream frame.

That asymmetry is deliberate and worth knowing before substituting one for the
other: agents and the CLI treat `session(id:)` as the complete record, and a
client that needs full order history must use the query, not the stream. The
statement is **not** asymmetric: both paths carry the same newest-three-months
window.

Streams are **not** gzipped. `platform/httpserve/gzip.go` buffers the whole
response so it can encode once, which a response that never ends can never
satisfy; `isStreaming` routes SSE and WebSocket upgrades around the wrapper.

## Errors

Errors are GraphQL errors whose message begins with a machine-readable code:

```
"not_found: session \"nope\": session not found"
"bad_request: count must be <= 1000"
"conflict: van costs $600, you have $0"
```

| message prefix | `extensions.code` | meaning |
|---|---|---|
| `not_found` | `NOT_FOUND` | no such session, car or order |
| `bad_request` | `INVALID_ARGUMENT` | malformed or out-of-range argument (including a month that has not started) |
| `conflict` | `CONFLICT` | legal request, wrong state (unaffordable, locked, already done) |
| `limit_reached` | `LIMIT_REACHED` | a contract limit: more than 64 queued steps per car |
| `forbidden` | `FORBIDDEN` | owner token missing or wrong, and not the owning account |

Both forms are present on every session/statement error, so a client may keep
matching the prefix it already matches or switch to `extensions.code`. City
mutations have always carried `extensions.code` only (`UNAUTHENTICATED`,
`LIMIT_REACHED`, `INTERNAL` included).

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
| `H` | customer waiting for a delivery | dropoff |
| `h` | house, no customer there right now | yes |
| `?` | unexplored | **no** |
| `X` | closed for street works | no (may drive off) |
| `1`–`9` | car by index (first nine only; a depot holding several cars draws one digit plus a `×N` badge) | — |

## A complete delivery

```bash
G=http://127.0.0.1:8181/graphql
q() { curl -s -X POST "$G" -H 'Content-Type: application/json' -d "$1"; }

# createSession returns the session and the ownerToken every later mutation needs.
q '{"query":"mutation { deliveryCreateSession(id:\"demo\", seed:7) { session { id tick paused } ownerToken } }"}'

TOKEN=...  # ownerToken from the response above

# Start the clock and wait one tick so the first orders spawn.
q "{\"query\":\"mutation { deliverySetSpeed(id:\\\"demo\\\", speed:4, ownerToken:\\\"$TOKEN\\\") { paused } }\"}"
q "{\"query\":\"mutation { deliveryWaitUntil(id:\\\"demo\\\", ticks:1, ownerToken:\\\"$TOKEN\\\") { orders { id state } } }\"}"
q "{\"query\":\"mutation { deliveryAssignOrder(id:\\\"demo\\\", orderId:\\\"ord_1\\\", carId:\\\"car_1\\\", ownerToken:\\\"$TOKEN\\\") { orders { state } } }\"}"

# East three times to Pizza Palace (x=6), then west three times and south
# three times to Anna's Flat (3,6). Verified against the shipped 22x22 map.
for d in EAST EAST EAST WEST WEST WEST SOUTH SOUTH SOUTH; do
  q "{\"query\":\"mutation { deliveryMoveCar(id:\\\"demo\\\", carId:\\\"car_1\\\", direction: $d, ownerToken:\\\"$TOKEN\\\") { id } }\"}"
  q "{\"query\":\"mutation { deliveryWaitUntil(id:\\\"demo\\\", ticks:1, ownerToken:\\\"$TOKEN\\\") { tick } }\"}"
done

q '{"query":"query { delivery { session(id:\"demo\") { ledger { money deliveries } orders { state paid } } } }"}'
```

Note the quantities: the first order is not necessarily one box, so `reward`
and the payload differ per seed. Adding `quantity` to the order selection above
shows what the job actually pays for.
