import time, tesla_env, numpy as np
e = tesla_env.TeslaEnv(level=0.5, seed=1)
t = time.time(); n = 0; eps = 0
obs, _ = e.reset()
while time.time() - t < 5:
    a = np.random.choice(np.flatnonzero(e.action_masks()))
    obs, r, te, tr, info = e.step(a); n += 1
    if te or tr:
        obs, _ = e.reset(); eps += 1
print(f"{n/5:.0f} steps/s, {eps} episodes, obs {obs.shape} max {obs.max():.2f}")
