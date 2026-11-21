"""Watch a model drive in the terminal.

    .venv/bin/python watch.py                       # newest checkpoint, map "easy"
    .venv/bin/python watch.py --map classic --delay 0.1
    .venv/bin/python watch.py --level 0.5           # a random generated map
    .venv/bin/python watch.py --frames              # print frames instead of animating

Legend: @ car, P park (still to collect), H home, S charger, # wall, . road, o already driven.
"""
import argparse
import glob
import os
import re
import sys
import time

import numpy as np
from sb3_contrib import MaskablePPO

import tesla_env as T


def latest_checkpoint():
    files = glob.glob("runs/ppo/ckpt_*_steps.zip") + glob.glob("runs/ppo/final.zip")
    if not files:
        sys.exit("no checkpoints in runs/ppo yet")
    def steps(p):
        m = re.search(r"ckpt_(\d+)_", p)
        return int(m.group(1)) if m else 10**12
    return max(files, key=steps)


def render(obs, trail, n, header):
    p = (obs[:9] > 0.5)
    wall, home, chg, park, car = p[0], p[1], p[2], p[3], p[4]
    rows = []
    for y in range(n):
        row = ""
        for x in range(n):
            if car[y, x]: ch = "@"
            elif wall[y, x]: ch = "#"
            elif park[y, x]: ch = "P"
            elif home[y, x]: ch = "H"
            elif chg[y, x]: ch = "S"
            elif (x, y) in trail: ch = "o"
            else: ch = "."
            row += ch + " "
        rows.append(row)
    return header + "\n" + "\n".join(rows)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("model", nargs="?")
    ap.add_argument("--map", default=None)
    ap.add_argument("--level", type=float, default=None, help="random map at this curriculum level")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--delay", type=float, default=0.15)
    ap.add_argument("--sample", action="store_true", help="sample moves instead of always taking the most likely one")
    ap.add_argument("--frames", action="store_true", help="print every 10th frame, no animation")
    a = ap.parse_args()

    path = a.model or latest_checkpoint()
    model = MaskablePPO.load(path, device="cpu")
    env = T.TeslaEnv(level=a.level or 0.0, seed=a.seed)
    opts = {"map": a.map or "easy"} if a.level is None else None
    obs, info = env.reset(options=opts)
    # the grid side is the number of non-wall columns/rows' extent: take it from the walls plane
    n = int(max(np.flatnonzero((obs[0] < 0.5).any(axis=0)).max(), np.flatnonzero((obs[0] < 0.5).any(axis=1)).max())) + 2
    n = min(n, T.GRID)
    trail, step, ret, done = set(), 0, 0.0, False
    title = f"{os.path.basename(path)}  map={info['map']}  parks={info['parks']}  battery={info['max_battery']}"
    while True:
        ys, xs = np.nonzero(obs[4]); trail.add((int(xs[0]), int(ys[0])))
        left = int(obs[3].sum())
        batt = obs[T.IDX_BATTERY_ABS, 0, 0] * 50
        header = f"{title}\nstep {step:3d}  battery {batt:4.0f}  parks left {left}  return {ret:6.2f}"
        if not a.frames or step % 10 == 0 or done:
            if not a.frames: print("\033[2J\033[H", end="")
            print(render(obs, trail, n, header), flush=True)
            if not a.frames: time.sleep(a.delay)
        if done: break
        act, _ = model.predict(obs, action_masks=env.action_masks(), deterministic=not a.sample)
        obs, r, te, tr, inf = env.step(act)
        ret += r; step += 1; done = te or tr
    print(f"\nresult: {inf.get('reason') or 'ended'}   parks left: {inf.get('left')}   moves: {step}")


if __name__ == "__main__":
    main()
