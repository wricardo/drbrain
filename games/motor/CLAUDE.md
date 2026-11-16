# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository. `AGENTS.md` is a symlink to this file, so other coding agents read the same guide.

## Dr Brain - Motor Programming

Robot-programming puzzle game for humans and AI agents. Go server (pure rules engine → `GameService` → gqlgen GraphQL with subscriptions) serving a SvelteKit SPA. Repo, Go module (`github.com/wricardo/drbrain-motor-programming`) and binary share the name `drbrain-motor-programming`.

**Full reference: [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)** — architecture, rules and engine semantics, map format and design, API, frontend, UI look and feel, configuration, deployment, maintenance workflows. Read the relevant section before non-trivial changes and keep it up to date when behavior changes. `README.md` is player-facing only.

## Commands

```sh
just --list                                # every recipe
just restart                               # rebuild UI + binary, restart on :8000 (the usual loop)
just dev                                   # stop the port holder, then go run . -port 8000 -host 0.0.0.0 -public-url http://localhost:8000 -debug
just test                                  # go test -race ./...
go test -race -run TestName ./game/engine  # single Go test
just validate                              # maps + reference solutions (needs local solutions/)
just verify                                # gofmt, vet, lint (golangci-lint 2.15+), Go tests, UI check (skips without frontend/node_modules)
just generate                              # gqlgen after editing graph/schema.graphqls
just build-ui                              # build UI into static/ (committed)
cd frontend && npm run check && npm test
cd frontend && npx vitest run src/lib/grid.test.ts   # single frontend test
cd frontend && npm run dev:local           # Vite UI against a local server on :8000
scripts/smoke.sh 9191                      # end-to-end HTTP smoke (starts its own server)
```

## Invariants — do not break

- **Engine** (`game/engine`, no I/O): call `vm.Settle(&program)` after `NewVMState` and any program change. `Step` mutates in place; `Clone()` anything leaving a lock. `Run`/`step(…, nil)` allocate no events.
- **Dependencies**: `game/service` defines `MapStore`, `SessionStore`, `Broadcaster`; implementations import `service`, never the reverse.
- **Locking**: mutate pattern is lock → mutate → `touch` (seq++) → `Clone` → unlock → persist → broadcast. Never broadcast, do I/O, `Persist` or wait on a runner while holding a session lock. Lock order session → runner mutex.
- **Runner lifecycle**: `Playing` ⇔ runner registered; every stop path goes through one `detach`.
- **Delivery**: full snapshot + monotonic `Seq` on every broadcast; hub drops the *oldest* queued update and ignores `Seq` ≤ last published; subscription resolver emits the current snapshot first.
- **Map snapshot**: sessions embed their own map copy; map edits/deletes never touch sessions. Map creation is atomic via `MapStore.Create`, never check-then-save.
- **Security**: map ids `^[a-z0-9_-]{1,64}$` (filenames); session ids 16 lowercase hex, validated before disk; admin mutations need `X-Admin-Key` (constant-time) unless `ALLOW_UNAUTHENTICATED_ADMIN=true` with no key set; `max_steps ≤ 10000`, `max_call_depth ≤ 64`.
- **Session ownership**: `AuthorizeSessionPlay` gates `setProgram`/`run`/`step`/`pause`/`reset` (owner token or owning account's login; a session with no `OwnerHash` stays open) and `AuthorizeSessionChange` gates rename/delete. Both live in `game/service` and are called by the resolvers, so no transport can bypass them. `canPlaySession` answers the same question for the UI.
- **Recursion is per map**: with `allow_recursion: false` (the default) a call to a sub already anywhere on the call stack is a no-op (still a step, reported as `callBlocked`); with `true` it recurses, bounded by `max_call_depth` (`depth > max_call_depth` → LOST/CALL_DEPTH).
- **Tiles**: an arrival tile fires at most one effect per step (portal, then mirror, then conveyor push), never chains, and never costs an extra step; a closed door blocks like a rock; the only tile state is `VMState.DoorsOpen` and it must be cloned, persisted and broadcast.
- **Zero subs**: `sub_tape_lengths: []` ≠ omitted. No `omitempty`; never turn `[]` into `nil` (incl. `graph/convert.go`).

## Repo rules

- **Public repo.** Never mention other local/private repos in tracked files. README stays player-facing.
- **Solutions are private.** `solutions/` is gitignored and local only. Never commit it or put solution content in the schema, API, prompts, `/llms.txt` or `static/`. Solution-dependent tests skip via `requireSolutions` when it is absent.
- **Push to `main` deploys** to https://motor-programming.wricardo.net (`.github/workflows/deploy.yml`). CI does not build the UI: rebuild and commit `static/` with every UI change.
- `graph/generated`, `graph/model` are gqlgen output: don't hand-edit. Don't run `go mod tidy` casually (gqlgen rewrites `go.mod`; re-`go get` dropped deps).

## Change checklists

- **Schema**: `graph/schema.graphqls` → `just generate` → `graph/convert.go` + resolvers → `frontend/src/lib/{queries,types}.ts` → `llms.txt.tmpl` / `lib/prompt.ts` if agents are affected → query copies in `api/hardening_test.go`.
- **Engine**: update golden step counts in `validate/validate_test.go` (7, 17, 19, 46, 23, 30, 44, 80, 32, 48, 83, 65, 77, 57, 40, 30, 42, 257, 199, 140, 151, 103, 6, 14, 9, 8, 22, 27) and the five inline fixtures in `game/engine/engine_test.go` (7, 46, 23, 30, 44) deliberately, never to hide a failure.
- **New map**: `maps/<id>.json` + local `solutions/<id>.json` → `just validate` → golden step count in `validate/validate_test.go`.
- **New page**: route under `frontend/src/routes/` + `spaRoutes` in `api/server.go` (+ nav in `+layout.svelte`).
- **UI**: Svelte 5 runes only; follow "UI look and feel" in `docs/DEVELOPMENT.md` (instruction colors/glyphs/keys live in `lib/instructions.ts`, brand names in `lib/brand.ts`). Gotcha: explicit `onchange`/`oninput` run before `bind:value` updates — read `event.currentTarget.value`.

## Testing

Go: table tests + `t.Run`, no `t.Parallel()` (uses `t.Setenv`), always `-race`; service tests use `newEnv` (`game/service/helpers_test.go`) and `waitFor`, never fixed sleeps. Frontend: Vitest + jsdom + Testing Library; logic lives in tested `lib/*.ts` modules.

**Before pushing**: `just verify && just validate`, `scripts/smoke.sh 9191` (starts its own server; port must be free); for UI changes open `/play/<id>` and `/watch/<id>` and run a program to the end.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:1105d646 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->
