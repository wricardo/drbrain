"""Train a Tesla-game driver with a recurrent policy (RecurrentPPO + LSTM).

Why a recurrent policy: at level 1.0 of the curriculum the agent plateaus at
17/24 shipped maps, and every remaining loss is "stranded" on a map with many
parks. Those need a route plan (which park next so the battery ends at a
charger), which a memoryless policy cannot hold across steps: it re-decides from
a single snapshot every move. An LSTM carries a summary of the past, so the
agent can commit to a plan and follow it.

RecurrentPPO has no action masking. Instead the environment refuses illegal
moves and charges a small penalty (TESLA_LEGAL=reject), so the agent still
cannot crash into walls to shortcut games.

    TESLA_LEGAL=reject .venv/bin/python train_recurrent.py --steps 4_000_000
    TESLA_LEGAL=reject TESLA_FOG=3 .venv/bin/python train_recurrent.py   # fog run

Warm start (recommended): --init-from runs_v8/ppo/final.zip copies the CNN
"eyes" from a maskable run, so the LSTM starts with competent perception and
only has to learn planning.
"""
import argparse
from pathlib import Path

import numpy as np
import torch
from sb3_contrib import RecurrentPPO
from sb3_contrib.common.recurrent.policies import RecurrentActorCriticCnnPolicy
from stable_baselines3.common.callbacks import BaseCallback, CheckpointCallback
from stable_baselines3.common.vec_env import SubprocVecEnv

from tesla_env import TeslaEnv
from train import Curriculum, GridCNN, Metrics

METRIC_MAPS = ["easy", "classic", "medium_maze", "medium_island", "one_way_city",
               "keys_run", "easy_circuit", "hub_maze"]


def make_env(seed, mix=True):
    def fn():
        return TeslaEnv(seed=seed, mix=mix)
    return fn


class RecurrentMetrics(BaseCallback):
    """Recurrent-aware version of the fixed-map score in train.Metrics."""

    def __init__(self, maps, every=250_000):
        super().__init__()
        self.maps, self.every = maps, every
        self.next_eval = every
        self.env = None
        self.reasons = {}

    def _on_step(self):
        for info, done in zip(self.locals["infos"], self.locals["dones"]):
            if done:
                r = info.get("reason") or "ended"
                self.reasons[r] = self.reasons.get(r, 0) + 1
        if self.num_timesteps >= self.next_eval:
            self.next_eval += self.every
            self.env = self.env or TeslaEnv()
            wins = left = 0
            for name in self.maps:
                obs, _ = self.env.reset(options={"map": name})
                state, start, done, info = None, True, False, {}
                while not done:
                    action, state = self.model.predict(
                        obs, state=state, episode_start=np.array([start], dtype=bool),
                        deterministic=True)
                    start = False
                    obs, _, te, tr, info = self.env.step(int(action))
                    done = te or tr
                wins += bool(info.get("is_success"))
                left += info.get("left", 0)
            self.logger.record("fixedmaps/win_rate", wins / len(self.maps))
            self.logger.record("fixedmaps/parks_left", left / len(self.maps))
            total = sum(self.reasons.values())
            for r, c in sorted(self.reasons.items()):
                self.logger.record(f"end_reason/{r}", c / max(total, 1))
            print(f"[fixed maps] {wins}/{len(self.maps)} solved, {left / len(self.maps):.1f} parks left; ends: "
                  + ", ".join(f"{r} {c}" for r, c in sorted(self.reasons.items())), flush=True)
            self.reasons = {}
        return True


def warm_start(model, path):
    """Copy the CNN feature extractor from a MaskablePPO run (same input layout)."""
    try:
        from sb3_contrib import MaskablePPO
        old = MaskablePPO.load(path, device="cpu")
        src = old.policy.features_extractor.state_dict()
    except Exception:
        from sb3_contrib import RecurrentPPO as RP
        old = RP.load(path, device="cpu")
        src = old.policy.features_extractor.state_dict()
    dst = model.policy.features_extractor
    fixed = {}
    for key, tensor in src.items():
        mine = dst.state_dict().get(key)
        if mine is None:
            continue
        if tensor.shape == mine.shape:
            fixed[key] = tensor
        elif (tensor.dim() >= 2 and tensor.shape[1] == mine.shape[1] - 1
              and tensor.shape[0] == mine.shape[0]):
            # one extra input plane (the fog "unknown" plane): start it at zero
            # weight so the copied network behaves exactly as it did before
            pad = torch.zeros_like(mine)
            pad[:, : tensor.shape[1]] = tensor
            fixed[key] = pad
    dst.load_state_dict(fixed, strict=False)
    print(f"warm start: copied {len(fixed)}/{len(src)} tensors from {path}", flush=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--steps", type=lambda s: int(s.replace("_", "")), default=4_000_000)
    ap.add_argument("--envs", type=int, default=16)
    ap.add_argument("--out", default="runs_lstm/ppo")
    ap.add_argument("--init-from", help="checkpoint whose CNN weights to copy")
    ap.add_argument("--resume", help="continue a recurrent checkpoint")
    ap.add_argument("--start-level", type=float, default=0.0)
    ap.add_argument("--no-mix", action="store_true")
    ap.add_argument("--device", default="mps")
    args = ap.parse_args()
    torch.set_num_threads(4)

    venv = SubprocVecEnv([make_env(i, not args.no_mix) for i in range(args.envs)])
    kwargs = dict(
        policy_kwargs=dict(features_extractor_class=GridCNN,
                           features_extractor_kwargs={"features_dim": 256},
                           lstm_hidden_size=256, n_lstm_layers=1,
                           net_arch=[128]),
        n_steps=256, batch_size=256, n_epochs=5,
        learning_rate=3e-4, gamma=0.995, gae_lambda=0.95, ent_coef=0.01,
        tensorboard_log=args.out, verbose=1, device=args.device,
    )
    if args.resume:
        model = RecurrentPPO.load(args.resume, env=venv, device=args.device)
    else:
        model = RecurrentPPO("CnnLstmPolicy", venv, **kwargs)
        if args.init_from:
            warm_start(model, args.init_from)
    ckpt = CheckpointCallback(save_freq=max(200_000 // args.envs, 1), save_path=args.out,
                              name_prefix="ckpt")
    model.learn(args.steps, callback=[Curriculum(start=args.start_level), RecurrentMetrics(METRIC_MAPS), ckpt])
    Path(args.out).mkdir(parents=True, exist_ok=True)
    model.save(f"{args.out}/final")


if __name__ == "__main__":
    main()
