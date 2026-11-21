"""Score a trained model on the maps shipped with the game (never seen in training).

    .venv/bin/python evaluate.py runs/ppo/final.zip
"""
import sys

import numpy as np
from sb3_contrib import MaskablePPO

import tesla_env


def play(model, env, map_name):
    obs, _ = env.reset(options={"map": map_name})
    done, steps, info = False, 0, {}
    while not done:
        a, _ = model.predict(obs, action_masks=env.action_masks(), deterministic=True)
        obs, _, te, tr, info = env.step(a)
        done, steps = te or tr, steps + 1
    return info.get("is_success", False), info.get("reason", ""), info.get("left", 0), steps


def main(path):
    model = MaskablePPO.load(path, device="cpu")
    env = tesla_env.TeslaEnv()
    wins = 0
    names = tesla_env.shipped_maps()
    for name in names:
        ok, reason, left, steps = play(model, env, name)
        wins += ok
        print(f"{name:24s} {'WIN ' if ok else 'lose'} {reason:15s} parks left {left}  moves {steps}")
    print(f"\n{wins}/{len(names)} shipped maps solved")


if __name__ == "__main__":
    main(sys.argv[1])
