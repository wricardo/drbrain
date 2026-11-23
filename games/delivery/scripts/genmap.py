#!/usr/bin/env python3
"""Generate maps/city.json: a Manhattan-style street grid.

The layout is derived, not hand-typed: streets fall on every third row and
column, which leaves 2x2 building blocks between them and a one-tile street at
both grid edges (the ring road). Hand-typing 22 rows of 22 characters is how the
old map ended up with no buildings at all and 62% of its walkable tiles having
four open neighbours -- there was nothing to route around.

Keeping the generator in the repo means the street period is a number you can
change and re-validate, instead of a wall of characters nobody dares edit.

Usage:
    python3 scripts/genmap.py            # rewrite maps/city.json
    python3 scripts/genmap.py --stats    # print geometry stats only
"""

import argparse
import json
import os
from collections import deque

GRID = 22
STREET_EVERY = 3

# The canal separates downtown from the harbour; the East Bridge build paves
# two of its tiles, which is why the canal is two columns wide.
CANAL_X = (13, 14)
CANAL_Y_MAX = 12

# Everything south-east of the canal and south of downtown is open water. The
# point is not decoration: it is what lets the three districts tile the grid
# without leaving a permanently-visible corner the player can drive into for
# free before any build.
SEA_X_MIN = 13
SEA_Y_MIN = 13

# The city's scheduled expansion. This is content, not geometry: the generator
# copies the campaign schedule so a regenerated city.json keeps the same stages
# the engine and UI were tuned against.
GROWTH = [
    {"id": "g01_corner_charger", "tick": 180, "title": "Corner Charger",
     "description": "The city installs a charger a block north of the depot, and the corner cafe opens.",
     "effects": [
         {"type": "add_charger", "entities": [
             {"id": "ch_corner", "pos": {"x": 3, "y": 0}, "capacity": 1}]},
         {"type": "add_customer", "entities": [
             {"id": "c_corner", "name": "Corner Cafe", "pos": {"x": 9, "y": 6},
              "demands": ["pizza"], "order_interval_ticks": 50, "jitter_ticks": 10}]},
     ]},
    {"id": "g02_harbour_gate", "tick": 420, "title": "Harbour Gate",
     "description": "The canal is bridged and the harbour opens with its two regulars and a charger.",
     "effects": [
         {"type": "build_bridge", "tiles": [
             {"x": 13, "y": 3, "terrain": "road"},
             {"x": 14, "y": 3, "terrain": "road"}]},
         {"type": "reveal_district", "district": "harbour"},
         {"type": "add_charger", "entities": [
             {"id": "ch_bridgehead", "pos": {"x": 12, "y": 3}, "capacity": 1}]},
     ]},
    {"id": "g03_market_flats", "tick": 720, "title": "Market Flats",
     "description": "The dirt track is paved, and a new harbour block opens with the Dock Diner.",
     "effects": [
         {"type": "pave_road", "tiles": [
             {"x": 3, "y": 13, "terrain": "road"},
             {"x": 3, "y": 14, "terrain": "road"},
             {"x": 6, "y": 13, "terrain": "road"},
             {"x": 6, "y": 14, "terrain": "road"},
             {"x": 15, "y": 3, "terrain": "house"},
             {"x": 15, "y": 6, "terrain": "kitchen"}]},
         {"type": "add_kitchen", "entities": [
             {"id": "k_dock", "name": "Dock Diner", "pos": {"x": 15, "y": 6},
              "menu": ["sushi"]}]},
         {"type": "add_customer", "entities": [
             {"id": "c_market", "name": "Market Flats", "pos": {"x": 15, "y": 3},
              "demands": ["sushi"], "order_interval_ticks": 55, "jitter_ticks": 10}]},
     ]},
    {"id": "g04_hills_road", "tick": 1200, "title": "Hills Road",
     "description": "The hills open: pharmacy, villa, observatory, and the Office Block.",
     "effects": [
         {"type": "reveal_district", "district": "hills"},
         {"type": "add_charger", "entities": [
             {"id": "ch_hilltop", "pos": {"x": 12, "y": 18}, "capacity": 1}]},
         {"type": "pave_road", "tiles": [
             {"x": 0, "y": 18, "terrain": "house"}]},
         {"type": "add_customer", "entities": [
             {"id": "c_office", "name": "Office Block", "pos": {"x": 0, "y": 18},
              "demands": ["meds"], "order_interval_ticks": 60, "jitter_ticks": 15}]},
     ]},
    {"id": "g05_hilltop_kitchen", "tick": 1680, "title": "Hilltop Kitchen",
     "description": "The depot adds charging bays; a new kitchen and home open on the ridge.",
     "effects": [
         {"type": "upgrade_depot", "depot": "d_main", "charge_slots": 8},
         {"type": "pave_road", "tiles": [
             {"x": 12, "y": 15, "terrain": "kitchen"},
             {"x": 12, "y": 21, "terrain": "house"}]},
         {"type": "add_kitchen", "entities": [
             {"id": "k_ridge", "name": "Ridge Kitchen", "pos": {"x": 12, "y": 15},
              "menu": ["meds", "pizza"]}]},
         {"type": "add_customer", "entities": [
             {"id": "c_ridge", "name": "Ridge House", "pos": {"x": 12, "y": 21},
              "demands": ["meds"], "order_interval_ticks": 65, "jitter_ticks": 15}]},
     ]},
    {"id": "g06_night_market", "tick": 2400, "title": "Night Market",
     "description": "A second harbour kitchen opens for the late crowd.",
     "effects": [
         {"type": "pave_road", "tiles": [
             {"x": 21, "y": 9, "terrain": "kitchen"},
             {"x": 21, "y": 10, "terrain": "house"}]},
         {"type": "add_kitchen", "entities": [
             {"id": "k_night", "name": "Night Market", "pos": {"x": 21, "y": 9},
              "menu": ["sushi"]}]},
         {"type": "add_customer", "entities": [
             {"id": "c_night", "name": "Night Flat", "pos": {"x": 21, "y": 10},
              "demands": ["sushi"], "order_interval_ticks": 70, "jitter_ticks": 20}]},
     ]},
]


def base_terrain(x, y):
    if x >= SEA_X_MIN and y >= SEA_Y_MIN:
        return "W"
    if x in CANAL_X and y <= CANAL_Y_MAX:
        return "W"
    if x % STREET_EVERY == 0 or y % STREET_EVERY == 0:
        return "R"
    return "B"


def build_layout():
    grid = [[base_terrain(x, y) for x in range(GRID)] for y in range(GRID)]

    # The southern approaches of two avenues are dirt: passable, but a step
    # costs an extra battery. This is what makes Alley Pavement an upgrade
    # rather than decoration.
    for x in (3, 6):
        for y in (13, 14):
            grid[y][x] = "G"

    # Entities. Every one of these sits on a street tile, so placing an entity
    # glyph replaces a road with something still passable. A charger dropped on
    # a building tile would render as a charger and refuse to be entered.
    entities = {
        (3, 3): "D",    # depot: downtown intersection
        (6, 3): "K",    # Pizza Palace
        (9, 9): "C",    # downtown charger
        (3, 6): "H",    # Anna's Flat
        (12, 6): "H",   # Ben's Workshop
        (6, 6): "H",    # Market Flats (growth)
        (9, 6): "H",    # Corner Cafe (growth)
        (9, 3): "H",    # Office Block (growth)
        (18, 6): "K",   # Harbour Fish Market
        (21, 3): "H",   # Dockside Diner
        (18, 0): "H",   # Lighthouse Keep
        (21, 6): "C",   # harbour charger
        (6, 18): "K",   # Hilltop Pharmacy
        (3, 18): "H",   # Hill Villa
        (9, 18): "H",   # The Observatory
    }
    for (x, y), ch in entities.items():
        grid[y][x] = ch
    return ["".join(row) for row in grid]


def walkable(ch):
    return ch not in ("W", "B")


def bfs(rows, start, blocked=frozenset()):
    dist = {start: 0}
    q = deque([start])
    while q:
        x, y = q.popleft()
        for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
            n = (x + dx, y + dy)
            if not (0 <= n[0] < GRID and 0 <= n[1] < GRID):
                continue
            if n in dist or n in blocked or not walkable(rows[n[1]][n[0]]):
                continue
            dist[n] = dist[(x, y)] + 1
            q.append(n)
    return dist


def city_config(rows):
    return {
        "name": "DrBrain City",
        "grid_size": GRID,
        "tick_ms": 300,
        "ground_extra_step_cost": 1,
        "ticks_per_month": 600,
        "max_events": 500,
        "max_reputation": 5.0,
        "max_chapter": 5,
        "autosave_ticks": 60,
        "starting_money": 0,
        "legend": {
            "W": "water", "B": "building", "R": "road", "G": "ground",
            "D": "depot", "C": "charger", "K": "kitchen", "H": "house",
        },
        "layout": rows,
        "items": {"pizza": "Pizza", "sushi": "Sushi", "meds": "Medicine"},
        "kitchens": [
            {"id": "k_pizza", "name": "Pizza Palace", "pos": {"x": 6, "y": 3}, "menu": ["pizza"]},
            {"id": "k_sushi", "name": "Harbour Fish Market", "pos": {"x": 18, "y": 6}, "menu": ["sushi"]},
            {"id": "k_pharmacy", "name": "Hilltop Pharmacy", "pos": {"x": 6, "y": 18}, "menu": ["meds"]},
        ],
        "customers": [
            {"id": "c_anna", "name": "Anna's Flat", "pos": {"x": 3, "y": 6},
             "demands": ["pizza"], "order_interval_ticks": 40, "jitter_ticks": 10},
            {"id": "c_ben", "name": "Ben's Workshop", "pos": {"x": 12, "y": 6},
             "demands": ["pizza"], "order_interval_ticks": 55, "jitter_ticks": 15},
            {"id": "c_docks", "name": "Dockside Diner", "pos": {"x": 21, "y": 3},
             "demands": ["sushi"], "order_interval_ticks": 60, "jitter_ticks": 15},
            {"id": "c_lighthouse", "name": "Lighthouse Keep", "pos": {"x": 18, "y": 0},
             "demands": ["pizza", "sushi"], "order_interval_ticks": 70, "jitter_ticks": 20},
            {"id": "c_villa", "name": "Hill Villa", "pos": {"x": 3, "y": 18},
             "demands": ["meds"], "order_interval_ticks": 65, "jitter_ticks": 15},
            {"id": "c_observatory", "name": "The Observatory", "pos": {"x": 9, "y": 18},
             "demands": ["meds", "pizza"], "order_interval_ticks": 75, "jitter_ticks": 20},
        ],
        "depots": [
            {"id": "d_main", "pos": {"x": 3, "y": 3}, "charge_slots": 4},
        ],
        "chargers": [
            {"id": "ch_downtown", "pos": {"x": 9, "y": 9}, "capacity": 1},
            {"id": "ch_harbour", "pos": {"x": 21, "y": 6}, "capacity": 1},
        ],
        "initial_cars": [
            {"class": "scooter", "name": "Scooter One", "pos": {"x": 3, "y": 3}},
        ],
        # Capacity is measured in BOXES, not orders: whether a job fits is a
        # property of the vehicle. The truck's hold is what makes a forty-box
        # order possible at all.
        "car_classes": {
            "scooter": {"capacity": 4, "max_battery": 24, "step_cost": 1, "price": 0},
            "van": {"capacity": 24, "max_battery": 36, "step_cost": 1, "price": 1000},
            "truck": {"capacity": 40, "max_battery": 48, "step_cost": 1, "price": 3500},
        },
        "districts": {
            "downtown": {
                "description": "Where you start: depot, Pizza Palace and two regulars.",
                "hidden": False,
                "rects": [[0, 0, 12, 14]],
            },
            "harbour": {
                "description": "Across the canal. Closed until the East Bridge is built.",
                "hidden": True,
                "rects": [[15, 0, 21, 12]],
            },
            "hills": {
                "description": "The southern rise. Long routes, richer customers.",
                "hidden": True,
                "rects": [[0, 15, 12, 21]],
            },
        },
        "orders": {
            "max_active_orders": 10,
            # Demand follows the visible customer population, not the fleet.
            # 0.6 leaves some headroom while the queue is being served.
            "target_orders_per_customer": 0.6,
            "ramp_ticks": 2400,
            "customer_cooldown_min_scale": 0.75,
            "min_spawn_gap_ticks": 12,
            "deadline_slack": 3.0,
            "deadline_slack_by_chapter": [3.0, 2.9, 2.8, 2.7, 2.6],
            "reward_base": {"pizza": 40, "sushi": 55, "meds": 70},
            "reward_bulk_base": 0.6,
            "reward_bulk_per_box": 0.4,
            "expire_fine_divisor": 4,
            "min_route_ticks": 5,
            "min_deadline_ticks": 10,
            "unreachable_leg_ticks": 20,
            "chapter_reward_multiplier": [1.0, 1.1, 1.25, 1.5, 1.8],
            "distance_bonus_per_tile": 3,
            "expire_penalty_reputation": 0.05,
            "late_pay_multiplier": 0.6,
            "force_first_order_at_tick": 0,
            "reputation_per_delivery": 0.02,
            "reputation_per_late": 0.05,
            "quantity_bands": [
                {"min_size": 1, "max_size": 3, "from_progress": 0.0, "weight": 40},
                {"min_size": 5, "max_size": 10, "from_progress": 0.175, "weight": 35},
                {"min_size": 13, "max_size": 24, "from_progress": 0.30, "weight": 25},
                {"min_size": 30, "max_size": 40, "from_progress": 0.70, "weight": 30},
            ],
        },
        "charging": {"depot_rate": 6, "charger_rate": 2, "charger_price_per_tick": 1},
        "fleet": {
            "upkeep_interval_ticks": 120,
            "upkeep_per_box_per_interval": 1,
            "upkeep_box_divisor": 4,
            "default_upkeep_per_interval": 1,
            "tow_fee_per_tile": 2,
            "tow_fee_min": 25,
        },
        # Street works. Off for the first chapter so a player with one scooter
        # has time to learn the map; a detour is only interesting once there is
        # more than one route to choose between.
        "closures": {
            "enabled": True,
            "from_chapter": 1,
            "every_ticks": 900,
            "duration_ticks": 260,
            "max_concurrent": 1,
            "segment_tiles": 3,
        },
        "growth": GROWTH,
    }


def stats(rows):
    roads = sum(1 for r in rows for ch in r if walkable(ch))
    buildings = sum(1 for r in rows for ch in r if ch == "B")
    water = sum(1 for r in rows for ch in r if ch == "W")
    depot = next((x, y) for y, r in enumerate(rows) for x, ch in enumerate(r) if ch == "D")
    dist = bfs(rows, depot)
    hidden = sum(1 for y in range(GRID) for x in range(GRID)
                 if (15 <= x <= 21 and y <= 12) or (y >= 15 and x <= 12))
    print(f"grid {GRID}x{GRID}: roads/entities {roads}, buildings {buildings}, water {water}")
    print(f"reachable from depot {len(dist)} of {roads}, unreachable {roads - len(dist)}")
    print(f"max distance {max(dist.values())}, mean {sum(dist.values())/len(dist):.1f}")
    print(f"hidden at start {hidden} = {100*hidden/(GRID*GRID):.1f}%")

    # Single 3-tile street-segment closures: how much do they cost, and do any
    # of them disconnect a tile? These are the numbers that make closures a
    # mechanic instead of a decoration.
    segs = set()
    for y in range(GRID):
        for x in range(GRID):
            if not walkable(rows[y][x]):
                continue
            if x % STREET_EVERY == 0 and y > 0 and y + 1 < GRID \
                    and walkable(rows[y - 1][x]) and walkable(rows[y + 1][x]):
                segs.add(frozenset([(x, y - 1), (x, y), (x, y + 1)]))
            if y % STREET_EVERY == 0 and x > 0 and x + 1 < GRID \
                    and walkable(rows[y][x - 1]) and walkable(rows[y][x + 1]):
                segs.add(frozenset([(x - 1, y), (x, y), (x + 1, y)]))
    segs.discard(frozenset())  # never close the depot itself
    segs = [s for s in segs if depot not in s]
    detours, cut = [], 0
    for s in segs:
        d = bfs(rows, depot, s)
        lost = [p for p in dist if p not in d and p not in s]
        if lost:
            cut += 1
        if d:
            detours.append(max(d[p] - dist[p] for p in d))
    detours.sort()
    if detours:
        print(f"{len(segs)} street-segment closures: detour p50 {detours[len(detours)//2]}, "
              f"p90 {detours[int(0.9*len(detours))]}, max {detours[-1]}; "
              f"closures that cut a tile off: {cut}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stats", action="store_true", help="print geometry stats and exit")
    ap.add_argument("-o", "--out", default=None, help="output path")
    args = ap.parse_args()

    rows = build_layout()
    stats(rows)
    if args.stats:
        return

    out = args.out or os.path.join(os.path.dirname(__file__), "..", "maps", "city.json")
    cfg = city_config(rows)
    with open(out, "w") as fh:
        json.dump(cfg, fh, indent=2)
        fh.write("\n")
    print(f"wrote {os.path.relpath(out)}")


if __name__ == "__main__":
    main()
