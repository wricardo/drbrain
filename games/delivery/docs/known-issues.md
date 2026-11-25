# Known issues

Defects found while documenting, each reproduced before it was written down.
Every item here was observed on the shipped map with `maps/city.json`, either by
a throwaway probe test or against a live server. An item is listed only if it
was **observed**, not inferred from reading the code.

This file records the evidence for problems that now live as tickets in the
`drbrain-delivery` beads workspace (`bd list` from the repo root). The tickets
carry owner and status; this file carries the measurement, including the ones too
small for a ticket. Where a ticket exists the heading names it, so the two can be
kept in step without duplicating prose.

---

## 1. ~~A save taken during street works reloaded with the closure gone~~ — FIXED

**Ticket:** `drbrain-delivery-cty` (closed) · **Severity:** was medium.

Was: `World.closures` is an in-memory cache that `ImportState` did not refill, so
a save taken mid-works reloaded with the street open and the two worlds diverged
on the next tick:

```
active at tick 802: cl_801 tiles=[(15,2) (15,3) (15,4)]
reloaded tick=802 closures=0 active=0
after one tick: original active=1 reloaded active=0
```

Now fixed by `rebuildClosures`, which re-derives the cache for the restored tick
by walking the closure window (`(Tick-duration, Tick]`) instead of replaying from
0, so no new save state was needed.

Verified at tick 802: both worlds report
`cl_801` on tiles `[(15,2) (15,3) (15,4)]` and stay in step on the next tick.

One measurement trap worth recording, because it readily produces a false alarm:
comparing tiles requires **equal fog on both sides**. A check that called
`RevealAll` on the original but rebuilt the reloaded world from a fresh city
saw the same ID on different tiles (`(15,2)` vs `(12,12)`) and looked like a
second bug. It was not — `closureIsSafe` consults reachability, which depends on
what is revealed, so fog is an input to the derivation. Fog itself is correctly
not saved (it is replayed from `Built`); a comparison just has to match it before
comparing.

## 2. ~~A car standing on a tile that closes under it got a *shorter* deadline~~ — FIXED

**Ticket:** `drbrain-delivery-bic` (closed) · **Severity:** was medium — it
penalised the player for a scheduled event.

Was: three rules disagreed about the same tile.

| query | before (own tile closed) | now |
|---|---|---|
| movement (`stepCar`) | may leave — the block is on entry | unchanged |
| `RouteWalkable(tile)` | `false` | `false` (still an entry test) |
| `RouteDistance(tile, goal)` | `-1` | `3` |
| `StepToward(tile, goal)` | a real direction | unchanged |

Original observation, with the depot tile closed:

```
car at (3,3), tile closed=true
RouteDistance(home->customer) = -1
StepToward(home->customer) = "south"
drove off: (3,3) -> (3,4) (moved=true)
estimateFrom: open=16 closed-under-car=11    <-- deadline shrank by a third
```

The player-visible consequence was that `estimateFrom` skipped a negative leg, so
a deadline quoted from a closed tile came out *shorter* while the car could drive
away freely.

Now fixed in `City.DistanceAvoiding`, which treats the origin as passable and
returns `-1` only for unwalkable terrain. Verified against the current tree:
`estimateFrom` reports 16 on both sides (no shrinkage) and `RouteDistance` from
the closed tile returns `3`.

`RouteWalkable` still reports `false` for a closed tile, and that is correct, not
a leftover: it answers "may a car *enter* here", which is still no. The two
queries now disagree only where they should.

## 3. `max_concurrent` above 1 can jointly disconnect the map

**Ticket:** `drbrain-delivery-swd` (cleanup) — the guard's unsoundness is a
property of the code, not yet an observable gameplay bug. **Severity:** latent —
unreachable with the shipped pacing, unsound once the pacing is retuned.

`closureIsSafe` validates one candidate against the grid as it stands and never
against a closure that is already live. So the "a closure must never disconnect
anything" guarantee is only complete while one closure is live at a time.

Observed — every candidate passes on its own, and pairs still cut the map:

```
candidates=182 individually-safe=182
SAFE PAIR DISCONNECTS: [(0,0) (1,0) (2,0)] + [(0,3) (1,3) (2,3)] loses 2 reachable tiles
SAFE PAIR DISCONNECTS: [(1,0) (2,0) (3,0)] + [(0,3) (1,3) (2,3)] loses 3 reachable tiles
SAFE PAIR DISCONNECTS: [(2,0) (3,0) (4,0)] + [(0,3) (1,3) (2,3)] loses 4 reachable tiles
SAFE PAIR DISCONNECTS: [(3,0) (4,0) (5,0)] + [(0,3) (1,3) (2,3)] loses 5 reachable tiles
```

Worst observed is 5 tiles, not 2 — the damage grows as the pair straddles the
corner near the depot, so the first example understates it. Re-measured after the
`reachFromSeeds` fix that now seeds from depots only (`drbrain-delivery-cfo`); the
per-candidate guarantee improved but the joint one is unchanged, because nothing
in the guard ever looks at two candidates at once.

Reproducible only when the pacing also allows overlap. With the shipped table
(`every_ticks: 900`, `duration_ticks: 260`, `max_concurrent: 1`) a second closure
can never start while the first is live, so the defect is **latent**: raising
`max_concurrent` alone changes nothing. Verification over 6000 ticks with
`max_concurrent` forced to 2: zero ticks had two closures live, and zero tiles
were lost. The pairing above is therefore a property of the guard — proved by
calling it directly — not a behaviour the shipped game can currently reach.

Two conditions are needed together: `duration_ticks >= every_ticks` so runs can
overlap, and `max_concurrent > 1` so the guard is asked about a second candidate.
Once both hold, validate the union of a live closure plus the candidate before
starting it, or reject the combination in the loader instead of accepting a knob
whose value is unsound.

## 4. The `order_starved` diagnostic is unreachable, so a broken menu is silent

**Ticket:** `drbrain-delivery-swd` (cleanup, part a) · **Severity:** low —
content bugs are caught by a test, not at runtime.

`spawnOneFor` has a branch that emits `order_starved: …` when no kitchen can
supply the chosen item, with a comment saying the harness should surface it.
It cannot be reached. The mechanism is subtler than "everything is filtered":

- `customerIsServable` returns `false` unless some demand has a reachable
  kitchen, and `spawnOneFor` returns before the branch in that case.
- `pickDemand` treats the two cases differently. A **single**-demand customer
  gets that demand back unconditionally, with no supply check at all; a
  **multi**-demand customer is filtered down to the viable items and returns
  `""` if none are viable.

Measured with every kitchen's menu cleared:

```
single-demand [pizza]:        servable=false pickDemand="pizza"
multi-demand  [pizza sushi]:  servable=false pickDemand=""
```

So the branch needs `customerIsServable == true` (some demand reachable) together
with `nearestKitchenFor` failing for the picked item. For a single-demand
customer those are the same question about the same item, so it cannot happen.
For a multi-demand customer `pickDemand` has already removed every unservable
item, so it cannot happen there either. Both paths agree; the branch is dead.

The consequence is what matters: an unsupplied customer goes silent. It raises no
order and emits nothing to say why, so a player sees a customer that simply never
places work. `TestEveryCustomerHasASupplyingKitchen` catches a permanently
unsupplied item at authoring time, which is why this stays low severity.

## 5. ~~The sim table's `tgt` column is not the governor's target~~ — FIXED

**Ticket:** `drbrain-delivery-swd` (closed) · **Severity:** was cosmetic but
misled tuning.

Was: `cmd/drbrain/sim.go:targetOf` hardcoded ratio `1.0` and cap `8`; the engine
used a fleet ratio (`0.75` in `city.json`). With four driving cars the harness
printed `tgt=4` while the governor returned `3`.

Fixed by exposing `World.TargetActive()` and having the harness print that exact
number. The target now comes from visible customers, not fleet size, so the
column also reflects the demand ramp rather than a re-derivation.

## 6. ~~The `sim` harness panicked on two inputs and silently lied on a third~~ — FIXED

**Ticket:** `drbrain-delivery-tzn` (closed) · **Severity:** was low for the game,
medium for anyone reading the pacing table as evidence.

Was: `cmdSim` parsed flags by hand, each `case` incrementing `i` then indexing
`args[i]` with no length check, and the tick loop divided by `every`.

```
drbrain sim -ticks                  panic: index out of range [1] with length 1
drbrain sim -every 0 -ticks 5       panic: integer divide by zero
drbrain sim -ticks abc              runs 0 ticks, exits 0, prints a table
```

The third was the one that mattered: a discarded `strconv.Atoi` error turned a
typo into a zero-tick run that still printed a header and a row. The README's
pacing table is cited as evidence, so that was a documentation hazard, not only
a rough edge.

Now parsed with a `flag.FlagSet` plus explicit validation:

```
drbrain sim -ticks      -> flag needs an argument: -ticks
drbrain sim -every 0    -> sim: -every must be > 0, got 0
drbrain sim -ticks abc  -> invalid value "abc" for flag -ticks: parse error
```

All three exit 2 with a usage message and no panic. `-ticks <= 0`, negative
`-every`, unparseable `-seed` and stray positional args are rejected too.

## 7. ~~A tile had no car capacity: cars stacked and passed through each other~~ — FIXED

**Ticket:** `drbrain-delivery-9by` (closed) · **Severity:** was medium — it
silently made one car per tile unselectable and let a head-on pair swap.

Was: `World.stepCar` validated direction, street works, walkability and battery,
and never asked whether another car was on the destination. There was no
occupancy index anywhere in `game/engine`.

```
probe, cars at (2,3) and (3,3), A queues East, B queues West, one tick:
after head-on: A=(3,3) reject=""  B=(2,3) reject=""
after stack:   A=(3,3) B=(3,3) sameTile=true
```

Knock-on effects, both observed: `City.renderRows` writes one glyph per tile, so
of two stacked cars only the higher index was drawn (two cars at (2,3) rendered
as `#.2...#`), and `Map.svelte` keyed cars by `x,y` with `map.set`, so the
invisible car was also unclickable — the player could not select it to drive it
away.

Now: a road tile holds one car; a depot accepts any number of cars (fleet size is
money-gated), and a charger holds its own capacity, so parking and fast charging
still work. The limit is on entry, and occupancy is evaluated last so a flat car
in traffic is told it is flat.

Two behaviours are load-bearing and are pinned by
`game/engine/occupancy_test.go`:

- **Following works.** A car may enter a tile a car ahead vacated on the same
  tick.
- **A head-on pair cannot swap.** Neither destination frees, so both are refused.
  Refusing only the higher id would make the rule "lower id wins" instead of
  "there is no room".

That needed a rewrite of the move phase rather than one extra `if`: planning is
separated from resolution and blocked moves are retried to a fixpoint against the
positions a committed tick would leave. A single pass reading live positions gets
following right but also lets a swap through, and a single pass ignoring other
movers gets the swap right but deadlocks every queue.

Two measurement traps, both of which produced false alarms in tickets filed
against a mid-edit tree:

- **The driver matters as much as the rule.** A car-blind planner that detours
  around traffic stalls: 0 of 8 cars moved for the last 500 ticks of a 3000-tick
  run, with 9993 blocked moves. Waiting is free (a refused move keeps the queued
  direction), detouring is not. Same map, 3000 ticks, three driver models:

  ```
  wait behind traffic                   250 delivered  /  49 expired /  0 tows
  detour, budget on the detoured route   79 delivered  / 177 expired / 34 tows
  detour, budget on the straight route  198 delivered  /  89 expired / 84 tows
  ```

  These three were measured on an older engine and bot and have not been re-run.
  The wait-behind-traffic driver as it stands now (3000 ticks), delivered /
  expired / tows / reputation:

  ```
  seed 0x5eed5eed   325 / 14 / 17 / 5.00     (154 / 111 / 36 / 0.61 before #8)
  seed 1            316 / 25 /  5 / 5.00     (45 / 184 / 1 / 0.03 before #8)
  seed 2            286 / 28 / 20 / 4.96
  seed 3            329 / 15 / 10 / 5.00
  seed 99           324 / 18 / 11 / 5.00
  ```

- **Seed spread is larger than the rule's cost.** The collision on/off comparison
  (247/245/240 against 269/240/253, seeds 0x5eed5eed/7/99, reputation
  4.83/5.00/4.90) is from the older engine and has not been re-measured. A
  single-seed before/after is not evidence here; the per-seed gap between two
  runs of the *same* build is wider than the rule's effect (286 to 329 delivered
  across the five seeds above).

## 8. The `sim` bot deadlocked head-on, abandoned chargers and over-assigned — FIXED

**Severity:** low for the game, high for anyone reading the pacing table as
evidence. Found by reviewing `cmd/drbrain/sim.go`; the bot, not the engine, was
at fault.

Five defects in the harness driver, each measured before it was fixed:

- **Head-on lock in a one-lane street.** Two cars wanting each other's tile can
  never resolve by waiting, because the engine refuses a swap and the bot
  re-queued the same step. On seed 1 a scooter and a van sat locked for 2000+
  ticks and the fleet never grew past two cars (45 delivered, 155 unclaimed
  expiries). One car of the pair now steps aside (`headOnSidestep`).
- **Idle cars left the charger they stood on.** 1136 of 1136 idle-on-charger
  car-ticks (default seed) pointed the car at a different tile.
- **Orders were assigned without checking battery.** Half-charged cars took
  orders they could not finish. `canServe` now requires car to kitchen to
  customer to a charger within the present charge, or a full battery.
- **Idle cars picked a free charger without checking they could reach it** and
  ran dry on the way, which was the main source of tows (61 to 86 per run once
  the deadlock was gone, 8 to 20 after).
- **Cars parked on a kitchen never collected their order.** Pickup fires when a
  car steps onto the kitchen tile, not when an order is assigned to a car
  already standing there (the engine's rule: assignment alone must not complete
  work). The nearest free car was often the one parked on the kitchen, so it sat
  still until the order expired. The bot now steps off and back on. Together
  with the other fixes this took expiries on the five seeds from 121 to 100.

The deadline is not checked at assignment: `AssignOrder` only ever extends it.
A head-on pair with no free neighbour tile on either side still stays locked;
the bot does not tow for that.

Tried and dropped, each measured over the same five seeds against the build as it
stood at the time:

- Heading for a reachable charger even when it has no room: 1504 delivered, 49
  tows, against 1549 and 56 without it (measured before the kitchen fix).
- Hopping charger to charger when one battery cannot cover a trip: 1551
  delivered, 84 tows, against 1580 and 63 without it.
- Refusing orders a full battery cannot cover: starved the default seed to one
  scooter, 30 delivered and 215 expired. The full-battery exemption in
  `canServe` is load-bearing.
- Not sending a full car off a depot, and treating a battery within two units
  of full as topped up: 1533 and 1182 delivered respectively, against 1580.
  A flat 60% top-up bar starved every seed down to one delivery: a car parked
  at 62% could not serve any order and never went to charge.

Still open: a two-car livelock. A car at a 1-slot charger steps off when full,
reads 97% (the step cost a unit) and steps back, so the charger's room flips
every tick. A second car choosing between that charger and another flips its
goal with it, and a third car head-on with it yields back and forth in a
one-lane street, burning battery until it strands or the order expires
(default seed, ticks 2440 to 2466, a 40-box order). The bot has no memory
between ticks, so it cannot commit to a goal; the straightforward cures above
each cost more throughput than they saved.

A zero-expiry run is not expected from this bot. Early orders (5 to 9 boxes
with a 25-tick life) arrive while the fleet is one scooter carrying 4 boxes,
demand is meant to outrun the fleet, and the bot keeps one order per car.
