"""Train a Tesla-game driver with MaskablePPO.

PPO ("proximal policy optimisation") is a reinforcement-learning algorithm:
  1. play lots of games with the current network, recording what happened;
  2. nudge the network's weights so actions that led to more reward become likelier;
  3. repeat, never changing the network too much in one go (that's the "proximal").
"Maskable" = illegal moves (walls, wrong-way roads) are hidden from the network.

Run:  .venv/bin/python train.py --steps 5_000_000
Watch: .venv/bin/tensorboard --logdir runs
"""
import argparse
from pathlib import Path

import numpy as np
import torch
import torch.nn as nn
from sb3_contrib import MaskablePPO
from sb3_contrib.common.wrappers import ActionMasker
from stable_baselines3.common.callbacks import BaseCallback, CheckpointCallback
from stable_baselines3.common.torch_layers import BaseFeaturesExtractor
from stable_baselines3.common.vec_env import SubprocVecEnv

from tesla_env import GRID, PLANES, TeslaEnv


class GridCNN(BaseFeaturesExtractor):
    """The "eyes" of the network: a small convolutional net over the 15x20x20 map planes.

    A convolution slides a tiny 3x3 filter over the grid, so each layer sees a bit
    further. Stacking them lets the net notice patterns like "dead end" or
    "charger two streets away". The result is flattened to one 256-number summary.
    """

    def __init__(self, observation_space, features_dim=256):
        super().__init__(observation_space, features_dim)
        self.cnn = nn.Sequential(
            nn.Conv2d(PLANES, 32, 3, padding=1), nn.ReLU(),
            nn.Conv2d(32, 64, 3, padding=1), nn.ReLU(),
            nn.Conv2d(64, 64, 3, padding=1), nn.ReLU(),
            nn.Conv2d(64, 32, 3, padding=1), nn.ReLU(),
            nn.Flatten(),
        )
        self.fc = nn.Sequential(nn.Linear(32 * GRID * GRID, features_dim), nn.ReLU())

    def forward(self, obs):
        return self.fc(self.cnn(obs))


class Metrics(BaseCallback):
    """Log why games end, and how the policy does on fixed maps it never trains on.

    Training win rate only measures the current curriculum level, so it can look
    great while the agent still fails every real map. The fixed-map score is the
    honest number.
    """

    def __init__(self, maps, every=250_000, episodes=1):
        super().__init__()
        self.maps, self.every, self.episodes = maps, every, episodes
        self.next_eval = every
        self.eval_env = None
        self.reasons = {}

    def _on_step(self):
        for info, done in zip(self.locals["infos"], self.locals["dones"]):
            if done:
                r = info.get("reason") or "ended"
                self.reasons[r] = self.reasons.get(r, 0) + 1
        if self.num_timesteps >= self.next_eval:
            self.next_eval += self.every
            if self.eval_env is None:
                self.eval_env = TeslaEnv()
            wins, left = 0, 0
            for name in self.maps:
                for _ in range(self.episodes):
                    obs, _ = self.eval_env.reset(options={"map": name})
                    done = False
                    while not done:
                        a, _ = self.model.predict(obs, action_masks=self.eval_env.action_masks(), deterministic=True)
                        obs, _, te, tr, info = self.eval_env.step(a)
                        done = te or tr
                    wins += bool(info.get("is_success"))
                    left += info.get("left", 0)
            n = len(self.maps) * self.episodes
            self.logger.record("fixedmaps/win_rate", wins / n)
            self.logger.record("fixedmaps/parks_left", left / n)
            total = sum(self.reasons.values())
            for r, c in sorted(self.reasons.items()):
                self.logger.record(f"end_reason/{r}", c / max(total, 1))
            print(f"[fixed maps] {wins}/{n} solved, {left / n:.1f} parks left; ends: "
                  + ", ".join(f"{r} {c}" for r, c in sorted(self.reasons.items())), flush=True)
            self.reasons = {}
        return True


class Curriculum(BaseCallback):
    """Start easy, get harder once the agent is winning most games.

    Every `window` finished episodes, if the win rate is above `promote_at`, the
    difficulty level goes up. Without this the agent mostly sees games it can
    not win yet and learns nothing ("sparse reward" problem).
    """

    def __init__(self, window=500, promote_at=0.8, step=0.05, start=0.0):
        super().__init__()
        self.window, self.promote_at, self.step = window, promote_at, step
        self.level, self.wins, self.eps = start, 0, 0

    def _on_training_start(self):
        self.training_env.env_method("set_difficulty", self.level)

    def _on_step(self):
        for info, done in zip(self.locals["infos"], self.locals["dones"]):
            if done:
                self.eps += 1
                self.wins += bool(info.get("is_success"))
        if self.eps >= self.window:
            rate = self.wins / self.eps
            self.logger.record("curriculum/win_rate", rate)
            self.logger.record("curriculum/level", self.level)
            if rate >= self.promote_at and self.level < 1.0:
                self.level = round(min(1.0, self.level + self.step), 3)
                self.training_env.env_method("set_difficulty", self.level)
                print(f"[curriculum] win rate {rate:.2f} -> level {self.level}", flush=True)
            self.wins = self.eps = 0
        return True


def make_env(seed, mix=False):
    def fn():
        return ActionMasker(TeslaEnv(seed=seed, mix=mix), lambda env: env.unwrapped.action_masks())
    return fn


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--start-level", type=float, default=0.0,
                    help="curriculum level to start from; use ~the level the checkpoint ended on")
    ap.add_argument("--mix", action="store_true",
                    help="sample map difficulty from [0, level] so old map sizes stay in rotation")
    ap.add_argument("--steps", type=lambda s: int(s.replace("_", "")), default=5_000_000)
    ap.add_argument("--envs", type=int, default=16, help="parallel game processes")
    ap.add_argument("--out", default="runs/ppo")
    ap.add_argument("--device", default="mps", help="mps (Apple GPU), cuda or cpu; the CNN is ~20x faster on a GPU")
    ap.add_argument("--resume", help="path to a saved .zip to continue from")
    args = ap.parse_args()
    torch.set_num_threads(4)

    venv = SubprocVecEnv([make_env(i, args.mix) for i in range(args.envs)])
    kwargs = dict(
        policy_kwargs=dict(features_extractor_class=GridCNN, net_arch=[128]),
        n_steps=256, batch_size=2048, n_epochs=4,
        learning_rate=3e-4, gamma=0.995, gae_lambda=0.95, ent_coef=0.01,
        tensorboard_log=args.out, verbose=1, device=args.device,
    )
    model = MaskablePPO.load(args.resume, env=venv, **{k: v for k, v in kwargs.items() if k not in ("policy_kwargs",)}) \
        if args.resume else MaskablePPO("CnnPolicy", venv, **kwargs)
    metric_maps = ["easy", "classic", "medium_maze", "medium_island", "one_way_city",
                   "keys_run", "easy_circuit", "hub_maze"]
    ckpt = CheckpointCallback(save_freq=max(200_000 // args.envs, 1), save_path=args.out, name_prefix="ckpt")
    model.learn(args.steps, callback=[Curriculum(start=args.start_level), Metrics(metric_maps), ckpt], progress_bar=False)
    Path(args.out).mkdir(parents=True, exist_ok=True)
    model.save(f"{args.out}/final")


if __name__ == "__main__":
    main()
