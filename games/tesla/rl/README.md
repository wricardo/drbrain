# Tesla RL: teach a neural net to drive

Reinforcement learning (RL) with MaskablePPO. The Go engine is the game; Python
only does the learning.

```
games/tesla/cmd/rlenv/   Go: wraps the real engine, rewards, random maps, observations
games/tesla/rl/          Python: Gymnasium env, training, evaluation
```

## Run

```bash
just -f games/tesla/rl/justfile venv build    # once: Python env + Go helper binary
cd games/tesla/rl
.venv/bin/python train.py --steps 6_000_000   # ~1h on an M-series Mac (uses the GPU via mps)
.venv/bin/tensorboard --logdir runs           # live curves
.venv/bin/python evaluate.py runs/ppo/final.zip
```

## Play the real game over GraphQL

```bash
# 1. run a server (any port; API only is fine); PostgreSQL is required
DRBRAIN_ADDR=127.0.0.1:8099 DRBRAIN_GAMES=tesla TESLA_CONFIG_DIR=games/tesla/maps \
  DRBRAIN_DATABASE_URL=postgres://drbrain:drbrain@127.0.0.1:55432/drbrain?sslmode=disable \
  ./drbrain serve
# 2. let the policy drive: creates a session if --session is omitted
.venv/bin/python play.py --map one_way_city --url http://127.0.0.1:8099/graphql --draw
```

`play.py` loops `gameState -> rlenv observe -> policy -> move`. The observation is built by the
same Go code as training (`rlenv observe`), so the rules and planes cannot drift.

`bench.py` measures the raw Go environment speed (~12k steps/s per process).

## Vocabulary

| term | meaning here |
|---|---|
| environment | the game, seen as `reset()` / `step(action)` |
| observation | 18 stacked 20x20 grids ("planes"): walls, home, chargers, unvisited parks, car, one-way arrows, cells that are safe to visit (a charger is still reachable afterwards), cells already driven over, road distance from car, distance to nearest charger, best battery on arrival, then four constant planes carrying battery level, moves left before the cap and moves since the last park |
| action | up / down / left / right; illegal ones are masked out |
| reward | +1 park, +5 win, -2 death, -0.01 per move, +0.05 per cell closer to the nearest park |
| policy | the neural net: observation in, move probabilities out |
| episode | one game, from start until win/death/move cap |
| curriculum | maps start tiny and generous (1 park, spare battery) and get harder as the win rate passes 80% |

## Fog of war

The observation never assumes the whole map is visible. `TESLA_FOG=3` starts the
Go env with a 3-cell visibility radius: cells the car has not driven past are
masked out and reported on an extra "unknown" plane, the derived distance planes
are computed from known cells only, and unexplored cells are offered as legal
but risky moves (a hidden wall still ends the run). Without fog the unknown
plane is omitted, so existing full-map checkpoints keep working.

Fog needs memory, which is what the recurrent trainer is for:

```bash
just build
TESLA_LEGAL=reject TESLA_FOG=3 .venv/bin/python train_recurrent.py --steps 4_000_000
```

`TESLA_LEGAL=reject` makes illegal moves cost a wasted step instead of crashing,
because RecurrentPPO has no action masking; the agent still cannot cheat, it just
pays for the mistake.

## Things to try when it stalls

- Watch `fixedmaps/win_rate` in TensorBoard: it is the honest score on maps never
  used for training (training win rate only reflects the current curriculum level).
- `end_reason/*` shows why runs end (stranded, max_moves, crash...), which tells
  you which failure to attack next.
- Reward numbers: `defaultRewards` in `cmd/rlenv/env.go` (rebuild with `just build`).
- Map difficulty ramp: `difficulty_to_gen` in `tesla_env.py`; promotion rule: `Curriculum` in `train.py`.
- Remove the two distance planes in `env.go` to see how much the net can figure out by itself.
- Fog mode needs memory (a recurrent policy); not implemented here.

## Results so far

All runs: MaskablePPO, 8M steps, 16 parallel game processes, scored deterministically on the
24 shipped maps with grid_size <= 20 (the fixed 20x20 observation cannot hold the 50x50 and
100x100 maps).

| run | what changed | maps solved | end reasons (losses) |
|---|---|---|---|
| v2 | battery-reachability grids | 3/24 | mostly stranded |
| v3 | + binary visited grid, moves-left budget | 6/24 | 15 stranded, 3 max_moves |
| v4 | + fading visit grid, `--mix`, safety shaping 0.05 | **10/24** | 12 stranded, 2 max_moves |
| v5 | v4 with safety shaping 0.50 | killed at 2.9M | stalled at level 0.15, max_moves rose |
| v6 | v4 with safety shaping 0.15 | killed at 3.3M | slower than v4 (level 0.15 at 2.5M vs 1.9M) |
| v7 | resume v4, 8M more steps from level 0.6 | **17/24** | 6 stranded, 1 max_moves |

Lessons worth keeping:

- **Memoryless policies circle.** A per-cell "have I been here" plane was the single biggest
  win; a *fading* age plane (255 now -> 0 after 24 moves) beat the binary version.
- **Potential-based shaping weight is not free.** 0.05 of a battery-deficit potential was too
  weak to matter (it fires on 2% of steps); 0.50 made the agent refuse to cross deficit
  regions to reach the last park, so it circled instead (curriculum stalled, `max_moves` up).
- **The curriculum causes forgetting.** Scoring on shipped maps mid-run is noisy because the
  training distribution moves; `--mix` (sample difficulty from [0, level]) keeps old map sizes
  in rotation.
- **Log the honest metric.** `fixedmaps/win_rate` on maps never trained on is the number that
  tracks real progress; the training win rate only measures the current curriculum level.
