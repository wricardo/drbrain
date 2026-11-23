"""Uniform way to drive a trained model, whether it has memory or not.

MaskablePPO takes the observation only and can be told which moves are legal.
RecurrentPPO carries an LSTM state between steps and does not support masks, so
it must pass state and episode_start through, and the environment has to refuse
illegal moves itself (TESLA_LEGAL=reject).
"""
import numpy as np
from sb3_contrib import MaskablePPO, RecurrentPPO


class Player:
    def __init__(self, path, deterministic=True):
        if path.endswith(".zip"):
            self.model = _load(path)
        else:
            self.model = path
        self.deterministic = deterministic
        self.recurrent = isinstance(self.model, RecurrentPPO)
        self.maskable = isinstance(self.model, MaskablePPO)
        self.state = None

    def reset(self):
        self.state = None

    def act(self, obs, env, episode_start=False):
        kw = {"deterministic": self.deterministic}
        if self.maskable:
            kw["action_masks"] = env.action_masks()
        if self.recurrent:
            self.state, action = self.model.predict(
                obs, state=self.state,
                episode_start=np.array([episode_start], dtype=bool), **kw)
        else:
            action, _ = self.model.predict(obs, **kw)
        return int(action)


def _load(path):
    try:
        return MaskablePPO.load(path, device="cpu")
    except Exception:
        return RecurrentPPO.load(path, device="cpu")


def play(player, env, map_name=None, options=None):
    """One deterministic episode. Returns (won, reason, parks_left, moves)."""
    player.reset()
    opts = options or ({"map": map_name} if map_name else None)
    obs, info = env.reset(options=opts)
    done, moves, start = False, 0, True
    while not done:
        a = player.act(obs, env, episode_start=start)
        start = False
        obs, _, te, tr, info = env.step(a)
        done, moves = te or tr, moves + 1
    return bool(info.get("is_success")), info.get("reason") or "ended", info.get("left", 0), moves
