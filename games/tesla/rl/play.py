"""Drive a live game over GraphQL with a trained policy.

    .venv/bin/python play.py --session <id> [--model runs/ppo/final.zip] [--url http://127.0.0.1:8080/graphql]

The loop is the point of this file:

    GraphQL gameState  ->  rlenv "observe"  ->  policy picks a move  ->  GraphQL move

`rlenv observe` builds the observation with the very same Go code the training
environment uses, so there is no second implementation of the rules or of the
observation to drift out of sync.
"""
import argparse
import json
import subprocess
import time
import urllib.request

import numpy as np
from sb3_contrib import MaskablePPO

import tesla_env as T

STATE_QUERY = """
query($id: ID!) {
  tesla {
    gameState(sessionID: $id) {
      grid { x y type visited id allowedDirections }
      playerPos { x y }
      battery maxBattery score totalParks gameOver victory mapName
    }
  }
}"""

MOVE_MUTATION = """
mutation($id: ID!, $dir: Tesla_Direction!) {
  tesla { move(sessionID: $id, direction: $dir) { success message
    gameState { battery score gameOver victory } } }
}"""

CREATE_SESSION = """
mutation($map: String!) {
  tesla { createSession(mapName: $map) { id mapName } }
}"""

DIRECTIONS = ["UP", "DOWN", "LEFT", "RIGHT"]


class GraphQL:
    def __init__(self, url, token=None):
        self.url, self.token = url, token

    def __call__(self, query, variables=None):
        body = json.dumps({"query": query, "variables": variables or {}}).encode()
        req = urllib.request.Request(self.url, body, {"Content-Type": "application/json"})
        if self.token:
            req.add_header("Authorization", f"Bearer {self.token}")
        with urllib.request.urlopen(req, timeout=30) as resp:
            payload = json.load(resp)
        if payload.get("errors"):
            raise RuntimeError(payload["errors"][0].get("message", payload["errors"]))
        data = payload["data"]["tesla"]
        return next(iter(data.values()))


class Observer:
    """Talks to the rlenv helper: live game state in, observation planes out."""

    def __init__(self):
        self.proc = subprocess.Popen(
            [str(T.RLENV_BIN), "-maps", str(T.MAPS_DIR), "-safety", str(T.SAFETY)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0,
        )

    def observe(self, state, visited):
        self.proc.stdin.write((json.dumps(
            {"cmd": "observe", "state": state, "visited": [list(v) for v in visited]}) + "\n").encode())
        header = json.loads(self.proc.stdout.readline())
        if header.get("error"):
            raise RuntimeError(header["error"])
        raw = b""
        while len(raw) < header["size"]:
            raw += self.proc.stdout.read(header["size"] - len(raw))
        planes = np.frombuffer(raw, dtype=np.uint8).reshape(T.GO_PLANES, T.GRID, T.GRID).astype(np.float32)
        planes[T.BINARY_PLANES:] /= 255.0
        sc = np.zeros((4, T.GRID, T.GRID), np.float32)
        sc[0] = header["battery"] / max(header["max_battery"], 1)
        sc[1] = min(header["battery"] / 50.0, 1.0)
        cap = header.get("move_cap") or 2 * max(header["grid"], 1) ** 2
        sc[2] = np.clip(header.get("move_left", cap) / cap, 0.0, 1.0)
        sc[3] = min(header.get("since_park", 0) / 40.0, 1.0)
        return np.concatenate([planes, sc]), np.array(header["mask"], dtype=bool)

    def close(self):
        self.proc.kill()


def show(state):
    g = state["grid"]
    p = state["playerPos"]
    chars = {"road": ".", "building": "#", "water": "#"}
    for y, row in enumerate(g):
        line = ""
        for x, c in enumerate(row):
            t = c["type"]
            if (x, y) == (p["x"], p["y"]):
                line += "@"
            elif t == "park":
                line += "x" if c["visited"] else "P"
            elif t == "home":
                line += "H"
            elif t == "supercharger":
                line += "S"
            else:
                line += chars.get(t, "?")
        print(line)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--session", help="existing session id; omit to create one")
    ap.add_argument("--map", default="easy", help="map to create when --session is omitted")
    ap.add_argument("--model", default="runs/ppo/final.zip")
    ap.add_argument("--url", default="http://127.0.0.1:8080/graphql")
    ap.add_argument("--token", default=None, help="Bearer JWT, only if the server requires sign-in")
    ap.add_argument("--delay", type=float, default=0.0, help="seconds between moves (watching in the browser)")
    ap.add_argument("--draw", action="store_true", help="print the board each move")
    args = ap.parse_args()

    gql = GraphQL(args.url, args.token)
    sid = args.session or gql(CREATE_SESSION, {"map": args.map})["id"]
    model = MaskablePPO.load(args.model, device="cpu")
    obs_builder = Observer()
    visited, moves = set(), 0
    try:
        while True:
            st = gql(STATE_QUERY, {"id": sid})
            if st["gameOver"]:
                print("victory" if st["victory"] else "game over", "-", st["score"], "of", st["totalParks"], "parks")
                return
            x, y = st["playerPos"]["x"], st["playerPos"]["y"]
            visited.add((x, y))
            obs, mask = obs_builder.observe(st, sorted(visited))
            if not mask.any():
                print("no legal moves left")
                return
            action, _ = model.predict(obs, action_masks=mask, deterministic=True)
            res = gql(MOVE_MUTATION, {"id": sid, "dir": DIRECTIONS[int(action)]})
            moves += 1
            if args.draw:
                show(st)
                print(f"move {moves}: {DIRECTIONS[int(action)]} {res['message']}  "
                      f"battery {res['gameState']['battery']}  score {res['gameState']['score']}")
            if args.delay:
                time.sleep(args.delay)
    finally:
        obs_builder.close()


if __name__ == "__main__":
    main()
