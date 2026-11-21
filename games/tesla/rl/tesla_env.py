"""Gymnasium wrapper around the Go game engine (games/tesla/cmd/rlenv).

A Gymnasium "environment" is the standard Python interface for RL:
    obs, info = env.reset()
    obs, reward, terminated, truncated, info = env.step(action)
Everything game-related (rules, rewards, map generation) lives in Go; this file
only moves bytes between the Go process and the learner.
"""
import json
import os
import random
import subprocess
from pathlib import Path

import gymnasium as gym
import numpy as np

HERE = Path(__file__).resolve().parent
MAPS_DIR = HERE.parent / "maps"
RLENV_BIN = HERE / "rlenv"
SAFETY = float(os.environ.get("TESLA_SAFETY", "0.05"))  # battery-safety shaping weight; 0 disables it
GRID = 20
GO_PLANES = 14
BINARY_PLANES = 10  # planes below this are 0/1; the rest are 0..255 bytes
# Broadcast planes built here from the scalars Go sends: cheap way to hand a
# network single numbers (a CNN only understands grids).
IDX_BATTERY_FRAC = GO_PLANES
IDX_BATTERY_ABS = GO_PLANES + 1
IDX_MOVES_LEFT = GO_PLANES + 2
IDX_SINCE_PARK = GO_PLANES + 3
PLANES = GO_PLANES + 4  # 18 total


def shipped_maps(max_grid=GRID):
    """Names of the shipped maps small enough for the fixed 20x20 observation."""
    names = []
    for p in sorted(MAPS_DIR.glob("*.json")):
        if json.loads(p.read_text())["grid_size"] <= max_grid:
            names.append(p.stem)
    return names


def difficulty_to_gen(level, rng):
    """Curriculum: level 0 = tiny maps, 1 park, lots of spare battery;
    level 1 = shipped-map-sized mazes, up to 5 parks, little spare battery."""
    level = min(max(level, 0.0), 1.0)
    return {
        "size_min": 7 + round(level * 5),
        "size_max": min(GRID, 9 + round(level * 11)),
        "parks_min": 1 + round(level * 1),
        "parks_max": 1 + round(level * 4),
        "slack": 2 + round(8 * (1 - level)),
        "open": rng.uniform(0.0, 0.25),
    }


class TeslaEnv(gym.Env):
    metadata = {"render_modes": []}

    def __init__(self, level=0.0, seed=None, mix=False):
        if not RLENV_BIN.exists():
            raise FileNotFoundError(f"{RLENV_BIN} missing: run `make -C games/tesla/rl build`")
        self.level = level
        self.mix = mix  # sample the map difficulty from [0, level] instead of using `level`
        self.rng = random.Random(seed)
        self.observation_space = gym.spaces.Box(0.0, 1.0, (PLANES, GRID, GRID), np.float32)
        self.action_space = gym.spaces.Discrete(4)  # up, down, left, right
        self._proc = subprocess.Popen(
            [str(RLENV_BIN), "-maps", str(MAPS_DIR), "-safety", str(SAFETY)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0,
        )
        self._out = self._proc.stdout
        self._mask = np.ones(4, dtype=bool)

    # -- plumbing -----------------------------------------------------------
    def _call(self, req):
        self._proc.stdin.write((json.dumps(req) + "\n").encode())
        header = json.loads(self._out.readline())
        if header.get("error"):
            raise RuntimeError(header["error"])
        raw = b""
        while len(raw) < header["size"]:
            chunk = self._out.read(header["size"] - len(raw))
            if not chunk:
                raise RuntimeError("rlenv died")
            raw += chunk
        n = header["planes"]
        if n != GO_PLANES:
            raise RuntimeError(f"rlenv sent {n} planes, this wrapper expects {GO_PLANES}: rebuild both")
        planes = np.frombuffer(raw, dtype=np.uint8).reshape(n, GRID, GRID).astype(np.float32)
        # Go tells us where the 0/1 planes stop; never guess the boundary.
        planes[header.get("binary_planes", BINARY_PLANES):] /= 255.0
        scalars = np.zeros((4, GRID, GRID), np.float32)
        scalars[0] = header["battery"] / max(header["max_battery"], 1)      # battery fraction
        scalars[1] = min(header["battery"] / 50.0, 1.0)                     # battery amount
        cap = header.get("move_cap") or 2 * max(header["grid"], 1) ** 2
        scalars[2] = np.clip((header.get("move_left", cap)) / cap, 0.0, 1.0)  # moves before the cap
        scalars[3] = min(header.get("since_park", 0) / 40.0, 1.0)            # moves since last park
        obs = np.concatenate([planes, scalars])
        self._mask = np.array(header["mask"], dtype=bool)
        return obs, header

    # -- gymnasium API ------------------------------------------------------
    def set_difficulty(self, level):
        self.level = level

    def set_mix(self, mix):
        self.mix = mix

    def action_masks(self):
        """Which of the four moves are legal right now (used by MaskablePPO)."""
        return self._mask

    def reset(self, seed=None, options=None):
        super().reset(seed=seed)
        options = options or {}
        if "map" in options:
            req = {"cmd": "reset", "map": options["map"]}
        else:
            # Mixing keeps small maps in rotation after a promotion, which stops the
            # policy forgetting what it already knew (see runs notes: one_way_city).
            level = self.rng.uniform(0.0, self.level) if self.mix else self.level
            req = {"cmd": "reset", "gen": difficulty_to_gen(level, self.rng)}
        obs, h = self._call(req)
        return obs, {"map": h["map"], "parks": h["parks"], "max_battery": h["max_battery"]}

    def step(self, action):
        obs, h = self._call({"cmd": "step", "action": int(action)})
        info = {"reason": h.get("reason", ""), "left": h["left"], "map": h["map"],
                "is_success": h.get("reason", "") == "victory"}
        return obs, h["reward"], h["terminal"], h["truncated"], info

    def close(self):
        if self._proc.poll() is None:
            self._proc.stdin.close()
            self._proc.wait(timeout=5)
