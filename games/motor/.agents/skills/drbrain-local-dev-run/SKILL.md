---
name: drbrain-local-dev-run
description: "Run, restart and debug drbrain-motor-programming locally (Go server + committed SvelteKit UI on :8000), including stale-bundle and stale-schema diagnosis."
---

# Run drbrain-motor-programming locally

Go server serving GraphQL + the built SvelteKit UI. Repo root: `/Users/wricardo/go/src/github.com/wricardo/drbrain-motor-programming`.

## Start

```sh
just restart      # rebuild UI + binary, stop whatever holds :8000, start again (the usual loop)
just dev          # go run . -port 8000 -debug (foreground)
just start        # built binary in the background; just logs, just status, just stop
```

Plain `go build -o /tmp/x . && /tmp/x -port 8000 -debug` works too. A bare `cmd &` inside a tool call gets reaped when the call ends, so use `just start` or `nohup … & disown`.

Endpoints on :8000 — UI `/`, `/maps`, `/editor`, `/learn`, `/multi`, `/runs`, `/settings`, `/leaderboard`; a game is `/play/<session-id>` and a spectator view `/watch/<session-id>`; GraphQL `/graphql` (introspection + playground under `-debug`); agent guide `/llms.txt`; `GET /healthz` for readiness.

Verify: `curl -s -o /dev/null -w '%{http_code}' http://localhost:8000/`, then POST `{ maps { id name } }` to `/graphql`.

Stop: `just stop` (handles the port listener and the recorded pid).

## Getting a playable session

`/play/<map-id>` is NOT a map route — the segment is a session id, and an unknown one silently falls back to another map. Create a session first:

```sh
curl -s http://localhost:8000/graphql -H 'Content-Type: application/json' \
  -d '{"query":"mutation{ createSession(mapID:\"grand_star\"){ id ownerToken } }"}'
```

Keep `ownerToken`: `setProgram`, `run`, `step`, `pause`, `reset` are creator-only and need it. `vm.status` values: READY, RUNNING, WON, LOST; the VM exposes `treatsRemaining` (there is no `treatsLeft`).

## Stale UI ("this looks like an old version")

`api/server.go` → `spaHandler` picks the first existing dir of `["./frontend/build", "./static"]`. `frontend/build` is git-ignored output of `npm run build`, so a leftover one silently shadows the committed `static/`.

```sh
curl -s http://localhost:8000/ | grep -o 'entry/[a-zA-Z0-9._-]*\.js' | sort -u
grep -o 'entry/[a-zA-Z0-9._-]*\.js' static/index.html | sort -u
grep -o 'entry/[a-zA-Z0-9._-]*\.js' frontend/build/index.html | sort -u
```

Served chunks matching `frontend/build` but not `static/` = stale. Fix: `rm -rf frontend/build`, restart. To rebuild for real: `just build-ui` (builds into `static/`), then `just build-go`.

## Stale GraphQL server ("field does not exist" for a field that does)

The server holds the schema it was built with; `graph/generated/` is codegen output. After a schema edit: `just generate` (see `drbrain-schema-change`), then `just restart`. A running `go run` process does not pick up new code.

## Sanity checks

```sh
just validate     # maps + reference solutions (needs local solutions/)
just test         # go test -race ./...
just verify       # gofmt + vet + lint + tests + UI check
just smoke 9191   # end-to-end HTTP; starts its own server, port must be free
```
