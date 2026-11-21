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
  music/scores-custom/   admin-authored pieces (+ per-piece .edit-token sidecars)
```

PostgreSQL is the durable store for accounts, sessions and custom maps/cities
(`platform_maps`); give its data directory its own volume outside the release.
Only builtin maps/cities (inside `releases/<sha>/games/...`), the music score
library and the built web app are files. `music/scores-custom` is the one
writable file tree a fresh install creates; old `.../sessions`, `.../maps-user`
and `.../maps-custom` directories may still exist from pre-cutover releases but
are no longer read or written. **Never `rm -rf` `/var/lib/drbrain`**; keep those
legacy directories as rollback backups until a later cleanup.

- Back up custom maps/cities with `pg_dump` (they share the database with
  accounts and sessions). Back up the music score library as files. Builtin
  city files under `DELIVERY_CITIES_DIR` (and every other builtin map) are
  release-managed: edit them in the repo/release, never on the host.
- Map/city settings are documented in `deploy/drbrain.env.example`:
  `MAZES_MAPS_DIR`/`MAZES_MAX_USER_MAPS`, `MOTOR_MAPS_DIR` (`MAX_USER_MAPS`,
  motor's unprefixed cap), `TESLA_CONFIG_DIR`/`TESLA_MAX_USER_MAPS`,
  `DELIVERY_CITIES_DIR`/`DELIVERY_MAX_USER_CITIES`, and `DELIVERY_CITY` (the
  legacy default city file; it must live inside `DELIVERY_CITIES_DIR`).
  `MUSIC_SCORES_DIR`/`MUSIC_CUSTOM_SCORES_DIR` plus `MUSIC_MAX_SESSIONS` and
  `MUSIC_SESSION_TTL` cover the score library.

## Admin keys (shipped/legacy writes)

Changing or deleting a shipped map/city, or a legacy ownerless custom row, needs
the operator key sent as `X-Admin-Key`. The key is configured per game:

| Game | Key env | Dev opt-out env |
| --- | --- | --- |
| Tesla | `TESLA_ADMIN_API_KEY` | `TESLA_ALLOW_UNAUTHENTICATED_ADMIN` |
| Mazes | `ADMIN_API_KEY` (unprefixed) | `ALLOW_UNAUTHENTICATED_ADMIN` (unprefixed) |
| Delivery | `DELIVERY_ADMIN_API_KEY` | `DELIVERY_ALLOW_UNAUTHENTICATED_ADMIN` |

An empty/comment-out key disables those admin writes by default; the local-only
`*ALLOW_UNAUTHENTICATED_ADMIN` opt-out is the exception, and it is ignored once
its key is set (Tesla accepts only the exact string `true`).
`deploy/drbrain.env.example` has commented examples for all three.

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

   It creates the `drbrain` user, the music score-library directory, unit, env
   template and nginx config, and removes the four old per-game vhosts.
2. Edit `/etc/drbrain/drbrain.env` (paths, `DRBRAIN_PUBLIC_URL`,
   `DRBRAIN_DATABASE_URL` is required, `MOTOR_PUBLIC_URL`, `MUSIC_*` paths and
   limits, origins).
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
`.edit-token` sidecar per piece, so a deploy never overwrites them. Sessions are rows
in PostgreSQL; `MUSIC_MAX_SESSIONS` and `MUSIC_SESSION_TTL` bound their count and
lifetime. `MUSIC_PUBLIC_URL` is a bare origin — the platform appends `/music` when
rendering `/music/llms.txt`.

## Legacy Tesla custom maps (one-time import)

Custom maps used to live in `TESLA_CUSTOM_MAPS_DIR`
(`/var/lib/drbrain/tesla/maps-custom`). That variable is no longer a runtime
setting: it is only the source directory for the one-time import into
`platform_maps`. Per host:

1. Stop the serving process while importing.
2. Dry-run and read the counts/errors:
   `sudo -u drbrain drbrain import-maps --game=tesla --dir=/var/lib/drbrain/tesla/maps-custom --dry-run`
3. Run it again without `--dry-run` (add `--replace` only when intentionally
   overwriting rows that already exist).
4. Start the new build and verify the maps in the UI.
5. Remove `TESLA_CUSTOM_MAPS_DIR` from `/etc/drbrain/drbrain.env`. Keep the
   directory itself as a backup until the rollback window closes; the importer
   never deletes source files.

If a host also has legacy Delivery custom cities, import them the same way with
`--game=delivery --dir=<legacy custom city dir>`.

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
