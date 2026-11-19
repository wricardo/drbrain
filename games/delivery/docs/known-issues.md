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

## 5. The sim table's `tgt` column is not the governor's target

**Ticket:** `drbrain-delivery-swd` (cleanup, part c) · **Severity:** cosmetic,
but it misleads tuning.

`cmd/drbrain/sim.go:targetOf` hardcodes ratio `1.0` and cap `8`; the engine uses
`target_orders_per_car` (`0.75` in `city.json`). With four driving cars the
harness prints `tgt=4` while `World.targetActive()` returns `3`.

Observed: `cars=4 driving=4 ratio=0.75 max=8 → engine 3, harness 4`.

The `tgt` column is therefore an approximation that runs high, and the tuning
table in the README has been read as if it were governor output. Exposing the
engine's own number (a small accessor) is the honest fix.
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

Now: a tile holds one car, a depot holds `DepotConfig.Capacity` and a charger its
own capacity, so parking and fast charging still work. The limit is on entry, and
occupancy is evaluated last so a flat car in traffic is told it is flat.

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

- **Seed spread is larger than the rule's cost.** Same driver both sides, 3000
  ticks: 247/245/240 with collision on against 269/240/253 with it off, at seeds
  0x5eed5eed/7/99. Reputation 4.83/5.00/4.90. A single-seed before/after is not
  evidence here; the per-seed gap between two runs of the *same* build is wider
  than the rule's effect.
