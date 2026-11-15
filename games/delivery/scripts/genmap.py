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
            {"id": "d_main", "pos": {"x": 3, "y": 3}, "capacity": 4, "charge_slots": 4},
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
            "scooter": {"speed": 1, "capacity": 4, "max_battery": 24, "step_cost": 1, "price": 0},
            "van": {"speed": 1, "capacity": 12, "max_battery": 36, "step_cost": 1, "price": 400},
            "truck": {"speed": 1, "capacity": 40, "max_battery": 48, "step_cost": 1, "price": 900,
                      "requires_grant": True},
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
            "max_active_orders": 8,
            "target_orders_per_car": 1.0,
            "min_spawn_gap_ticks": 12,
            "deadline_slack": 1.6,
            "deadline_slack_by_chapter": [1.6, 1.5, 1.4, 1.3, 1.2],
            "reward_base": {"pizza": 40, "sushi": 55, "meds": 70},
            "chapter_reward_multiplier": [1.0, 1.1, 1.25, 1.5, 1.8],
            "distance_bonus_per_tile": 3,
            "expire_penalty_reputation": 0.05,
            "late_pay_multiplier": 0.6,
            "force_first_order_at_tick": 0,
            "reputation_per_delivery": 0.02,
            "reputation_per_late": 0.05,
            "quantity_bands": [
                {"min_size": 1, "max_size": 3, "from_chapter": 0, "weight": 70},
                {"min_size": 4, "max_size": 10, "from_chapter": 1, "weight": 25},
                {"min_size": 12, "max_size": 24, "from_chapter": 2, "weight": 6},
                {"min_size": 30, "max_size": 40, "from_chapter": 3, "weight": 8},
            ],
        },
        "charging": {"depot_rate": 6, "charger_rate": 2},
        "achievements": [
            {"id": "a01_first_drop", "title": "First Drop",
             "description": "Deliver your first order.", "requires": {"deliveries": 1}},
            {"id": "a02_getting_known", "title": "Getting Known",
             "description": "Complete 5 deliveries.", "requires": {"deliveries": 5}},
            {"id": "a03_first_500", "title": "First $500",
             "description": "Earn $500 in total.", "requires": {"money_earned": 500}},
            {"id": "a50_first_tow", "title": "First Tow",
             "description": "Get a car towed. It happens to everyone.",
             "requires": {"tows": 1}, "grant_money": 50},
            {"id": "a04_harbour_trade", "title": "Harbour Trade",
             "description": "Deliver 5 dishes from the harbour.",
             "requires": {"item_delivered": {"item": "sushi", "delivered": 5}},
             "grant_money": 100},
            {"id": "a05_house_call", "title": "House Call",
             "description": "Deliver 5 medicines to the hills.",
             "requires": {"item_delivered": {"item": "meds", "delivered": 5}},
             "grant_money": 150},
        ],
        "unlocks": [
            {"id": "u01_corner_charger", "title": "Corner Charger",
             "description": "A first upgrade: a charger a block from the depot.",
             "requires": {"deliveries": 1}, "grants_build": ["b01_corner_charger"]},
            {"id": "u02_east_bridge", "title": "East Bridge",
             "description": "Bridge the canal and open the harbour road.",
             "requires": {"deliveries": 5}, "grants_build": ["b02_east_bridge"]},
            {"id": "u03_alley_pavement", "title": "Alley Pavement",
             "description": "Pave the southern dirt track: cheaper than driving dirt.",
             "requires": {"money_earned": 500}, "grants_build": ["b03_alley_pavement"]},
            {"id": "u04_harbour_gate", "title": "Harbour Gate",
             "description": "Bridge is done and the city trusts you: open the harbour.",
             "requires": {"deliveries": 15}, "grants_build": ["b04_harbour_gate"]},
            {"id": "u05_fleet_yard", "title": "Fleet Yard",
             "description": "Earn enough to expand the depot and license a truck.",
             "requires": {"money_earned": 2000}, "grants_build": ["b06_depot_yard"]},
            {"id": "u06_hills_road", "title": "Hills Road",
             "description": "A serious investment: the southern hills open up.",
             "requires": {"money_earned": 4000}, "grants_build": ["b05_hills_road"]},
        ],
        "builds": [
            {"id": "b01_corner_charger", "title": "Corner Charger",
             "description": "Add a charger beside the depot approach.", "cost": 150,
             "effects": [{"type": "add_charger", "entities": [
                 {"id": "ch_corner", "pos": {"x": 3, "y": 0}, "capacity": 1}]}]},
            {"id": "b02_east_bridge", "title": "East Bridge",
             "description": "Turn the canal crossing into road.", "cost": 300,
             "effects": [
                 {"type": "build_bridge", "tiles": [
                     {"x": 13, "y": 3, "terrain": "road"},
                     {"x": 14, "y": 3, "terrain": "road"}]},
                 {"type": "add_charger", "entities": [
                     {"id": "ch_bridgehead", "pos": {"x": 12, "y": 3}, "capacity": 1}]},
             ]},
            {"id": "b03_alley_pavement", "title": "Alley Pavement",
             "description": "Pave the dirt track south of the depot.", "cost": 250,
             "effects": [{"type": "pave_road", "tiles": [
                 {"x": 3, "y": 13, "terrain": "road"},
                 {"x": 3, "y": 14, "terrain": "road"},
                 {"x": 6, "y": 13, "terrain": "road"},
                 {"x": 6, "y": 14, "terrain": "road"}]}]},
            {"id": "b04_harbour_gate", "title": "Harbour Gate",
             "description": "Open the harbour district: fish market, dock diner and a charger.",
             "cost": 800,
             "effects": [
                 {"type": "reveal_district", "district": "harbour"},
                 {"type": "add_charger", "entities": [
                     {"id": "ch_harbour_gate", "pos": {"x": 15, "y": 6}, "capacity": 1}]},
             ]},
            {"id": "b05_hills_road", "title": "Hills Road",
             "description": "Grade a road into the hills: pharmacy, villa and observatory.",
             "cost": 1200,
             "effects": [
                 {"type": "reveal_district", "district": "hills"},
                 {"type": "add_charger", "entities": [
                     {"id": "ch_hilltop", "pos": {"x": 12, "y": 18}, "capacity": 1}]},
             ]},
            {"id": "b06_depot_yard", "title": "Depot Yard",
             "description": "Extend the depot so the fleet has somewhere to park, and license the truck.",
             "cost": 700,
             "effects": [
                 {"type": "upgrade_depot", "depot": "d_main", "capacity": 8, "charge_slots": 8},
                 {"type": "grant_car_class", "class": "truck"},
             ]},
        ],
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
