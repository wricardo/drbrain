# Deploying Dr Brain

One binary (`drbrain`), one systemd unit, one nginx vhost, one port (`127.0.0.1:8080`).
Games are path-routed (`/mazes`, `/motor`, `/tesla`, `/delivery`, `/music`) and the SvelteKit app is
served from the same process.

## Layout on the host

```
/opt/drbrain/
  releases/<sha>/        drbrain, web/, games/*/maps/, deploy/smoke.sh
  current -> releases/<sha>
  releases/<old>/        kept for rollback
/etc/drbrain/drbrain.env process + per-game env (never replaced by a deploy)
/etc/systemd/system/drbrain.service
/var/lib/drbrain/
  mazes/sessions/
  motor/{sessions,maps-user,accounts}/
  tesla/{sessions,maps-custom}/
  delivery/sessions/
  music/{sessions,scores-custom}/
```

Runtime state lives in `/var/lib/drbrain`, outside the release directory, so a deploy
only replaces the binary, the web bundle and the built-in maps. **Never `rm -rf` the
state dirs**; they are the only copy of player sessions, accounts and user maps.
PostgreSQL (when `DRBRAIN_DATABASE_URL` is set; it stores accounts, sessions,
logins and player maps) keeps its own volume outside the release.

## First-time provisioning

1. Copy the repo (or a checkout) to the host and run:

   ```sh
   sudo CANONICAL_HOST=drbrain.wricardo.net \
        MAZES_HOST=mazes.wricardo.net \
        MOTOR_HOST=motor-programming.wricardo.net \
        TESLA_HOST=tesla.wricardo.net \
        DELIVERY_HOST=delivery.wricardo.net \
        ./deploy/install-host.sh
   ```

   It creates the `drbrain` user, state dirs, unit, env template and nginx config, and
   removes the four old per-game vhosts.
2. Edit `/etc/drbrain/drbrain.env` (paths, `DRBRAIN_PUBLIC_URL`, `MOTOR_PUBLIC_URL`,
   `MUSIC_*` paths and limits, origins, optional database URL).
3. DNS the canonical name and the four legacy names at the host; issue certificates
   with certbot (paths in the nginx template are `/etc/letsencrypt/live/<host>/`).
4. Stop the old units and start the new one:

   ```sh
   sudo systemctl disable --now drbrain-mazes drbrain-motor-programming \
        tesla-road-trip-game drbrain-delivery
   sudo systemctl enable --now drbrain.service
   ```
5. Push to `main` once to install a release through the workflow (or extract a package
   into `/opt/drbrain/releases/<sha>` and point `current` at it).

## Every deploy

`.github/workflows/deploy.yml`: vet + tests, build the web app, build `linux/arm64`
`CGO_ENABLED=0`, package, one SSH upload, one release swap + restart, remote smoke test
(`/healthz` with five `ok` entries, one namespaced GraphQL query per game against
`/graphql`, SPA shell).

### Music scores

The shipped score library is read from `MUSIC_SCORES_DIR` (default
`/opt/drbrain/current/games/music/scores`, inside the release). Admin-authored pieces
live in `MUSIC_CUSTOM_SCORES_DIR` (`/var/lib/drbrain/music/scores-custom`) with an
`.edit-token` sidecar per piece, so a deploy never overwrites them. Sessions are stored
as one JSON payload plus metadata per session in `MUSIC_SESSIONS_DIR`
(`/var/lib/drbrain/music/sessions`); `MUSIC_MAX_SESSIONS` and `MUSIC_SESSION_TTL`
bound their count and lifetime. `MUSIC_PUBLIC_URL` is a bare origin — the platform
appends `/music` when rendering `/music/llms.txt`.

## Legacy domains

`deploy/nginx/drbrain.conf.tmpl` redirects each old host to the canonical path:

| Legacy host | Redirect |
| --- | --- |
| `mazes.*` | `https://<canonical>/mazes$request_uri` |
| `motor-programming.*` | `https://<canonical>/motor$request_uri` |
| `tesla.*` | `https://<canonical>/tesla$request_uri` |
| `delivery.*` | `https://<canonical>/delivery$request_uri` |

`$request_uri` starts with `/` and includes the query string, so
`https://mazes.example/a/b?x=1` becomes `https://<canonical>/mazes/a/b?x=1` with no
double slash.

## Mitigation and rollback

There is no per-game restart: one process serves all games. To drop a regressed game
without a rebuild, edit `DRBRAIN_GAMES` in `/etc/drbrain/drbrain.env` and
`systemctl restart drbrain.service`:

```sh
# e.g. keep everything except motor
DRBRAIN_GAMES=mazes,tesla,delivery
```

That game's prefix returns 404 while the others stay up (the SPA still loads; its API
calls fail). Re-add the name and restart to restore it — no rebuild needed.

Full rollback (previous binary + previous env):

```sh
ls -1 /opt/drbrain/releases                  # pick the previous release
sudo ./deploy/rollback.sh <previous-release> # swaps current, restarts, smoke tests
# env is /etc/drbrain/drbrain.env and is not versioned by deploys; restore your
# previous copy (or the pre-cutover env backup) before restarting if it changed.
```

The drill is: `systemctl is-active drbrain`, `/healthz` shows five `ok` entries, and one
namespaced GraphQL query per game answers on `/graphql`.

## Client repointing

External clients that used a per-game GraphQL URL now use the one endpoint,
`https://drbrain.wricardo.net/graphql`, selecting their game's namespace in every
operation (`{ mazes { maps { id } } }`). There is no `/<game>/graphql` route; do not
resurrect one. Update `.gqlcli.json`, `.mcp.json`, `MCP_TOOLS.md` examples and any
scripts; the legacy hosts only redirect HTTP requests, not embedded API paths for tools
that ignore redirects (GraphQL POST redirects are followed by most clients, but pin the
canonical URL).
