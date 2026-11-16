---
name: drbrain-schema-change
description: "Change the GraphQL schema, resolvers or mutations in drbrain-motor-programming (gqlgen + SvelteKit): pinned-toolchain codegen, service-layer rules, cookie-setting resolvers, and every ripple surface that must move with the schema."
---

# Changing the GraphQL API in drbrain-motor-programming

Go + gqlgen (v0.17.73) + a committed SvelteKit bundle served by the Go binary. No database: flat JSON under `sessions/`, `accounts/`, `maps-user/`.

## 1. Edit the SDL

`graph/schema.graphqls` is the source of truth. Descriptions (`"""…"""`) are user-facing: they are rendered into `/llms.txt`. Types land in `graph/model` as plain data carriers; engine/service conversion is hand-written in `graph/convert.go` — never hand-edit `graph/generated` or `graph/model`.

## 2. Regenerate with the pinned toolchain

```sh
GOTOOLCHAIN=go1.25.0 go run github.com/99designs/gqlgen generate
```

`just generate` runs the bare `go run …` form and FAILS on the default Go 1.27 toolchain: `go/packages` cannot load `github.com/vektah/gqlparser/v2/ast` through `gqlgen/graphql/introspection`, and the CLI usually exits 1 with empty stderr — after **deleting** `graph/generated/generated.go` and `graph/model/models_gen.go`. If that happens, `git checkout -- graph/generated graph/model` and rerun with the pinned toolchain.

To see the swallowed error, drive the library directly in a throwaway `main` in this module:

```go
cfg, err := config.LoadConfigFromDefaultLocations()   // github.com/99designs/gqlgen/codegen/config
if err != nil { fmt.Printf("LOAD ERR: %v\n", err); return }
if err := api.Generate(cfg); err != nil { fmt.Printf("GEN ERR: [%v]\n%+v\n", err, err) }
```

Verify idempotency: generate a second time and confirm `git diff --stat graph/schema.resolvers.go` shows only the new stubs.

## 3. Keep helpers out of `schema.resolvers.go`

gqlgen preserves only schema-declared resolvers there; any unexported helper is moved into a commented-out `// !!! WARNING !!!` block at the end of the file, which then breaks the build and every later generate. Shared logic belongs in its own file in `package graph` (e.g. `graph/password.go`) as a method on the resolver type, or in `graph/resolver.go` / `graph/convert.go`.

## 4. Put the rule in `game/service`, not the resolver

- Add the method to the `GameService` interface (`game/service/types.go`) and implement it in `game/service/impl.go` / `accounts.go`; the resolver only calls it. Authorization enforced in a resolver alone can be bypassed by a second transport.
- Errors: `service.Errorf(service.CodeForbidden, …)`; `toGQLError` maps codes into `extensions.code`, and the UI picks wording from `frontend/src/lib/errors.ts`. Codes live in `game/service/errors.go` plus `CodeUnauthenticated` in `game/service/accounts.go`.
- Optional credentials are `*string` args (`ownerToken`, `displayName`) via small actor builders (`sessionActor(ctx, ownerToken)`).
- A session with an empty `OwnerHash` is "open" (legacy rows) — preserve that when adding checks.
- A "can I …?" query (`canPlaySession`) beats letting the UI discover it from a failed mutation.

## 5. Setting cookies from a resolver

gqlgen's POST transport sets headers (including Content-Type) before running resolvers but does not flush, so `http.SetCookie` from a resolver reaches the client — load-bearing for the password sign-in mutations (`registerWithPassword`, `logInWithPassword`).

Pattern (`api/server.go` → `withViewer`): middleware injects `graph.SignIn{OverHTTP: !isWebSocketUpgrade(r), SetSession: func(token string, expires time.Time){ a.SetSession(w, r, token, expires) }}`; the resolver reads it with `requireSignIn(ctx)`, which returns FORBIDDEN when `!OverHTTP`. That stops a websocket subscription from minting a login the browser could never send back. Keep same-origin / rate-limit / lockout checks inside the auth package method (e.g. `Auth.PasswordSignIn`) and pass `*http.Request` plus the callback.

## 6. Ripple checklist — a field is not done until every line is checked

- `graph/convert.go` — fill the new field in the right converter (`toVMState`, `toStepEvent`, `toMap`, …).
- `graph/graph_test.go` — `fakeService` embeds `service.GameService`, so a new interface method needs a stub or the test binary panics on the nil embed.
- `api/hardening_test.go` — every frontend document must stay under `api.ComplexityLimit`; add the new field to the selector copies (`mapSel`, `eventSel`, `sessionSel`) and the per-operation cases. A new fan-out field also needs `complexityRoot()` in `api/server.go`.
- `frontend/src/lib/queries.ts` + `types.ts` — one document per mutation (two root fields in one document both execute); update each call site that passes variables.
- `frontend/src/lib/prompt.ts` — the "Play with an AI" prompt must carry any new credential or the agent flow breaks.
- `llms.txt.tmpl` — embedded at build time and executed verbatim by agents: editing it changes nothing until the binary is rebuilt.
- `README.md` — the four-call GraphQL snippet.
- `scripts/smoke.sh` — its `gql` helper needs `-c/-b "$JAR"` to persist cookies or `me` is null after sign-in.
- `docs/DEVELOPMENT.md` — rewrite the prose that the change contradicts; do not append.

## 7. Rebuild the committed bundle

`static/` is committed and served by the Go server; CI does not build it.

```sh
just build-ui     # builds into static/
just build-go
```

`api/server.go` prefers `frontend/build` when that directory exists, so `rm -rf frontend/build` before testing or you will serve the stale copy.

## 8. Gates

```sh
just verify                    # gofmt, vet, golangci-lint, go test -race ./..., UI check
just validate                  # maps + reference solutions
scripts/smoke.sh 9191          # real HTTP; starts its own server, port must be free
cd frontend && npm run check && npm test
```

`golangci-lint` must be >= 2.15 built with this Go toolchain. macOS has no `setsid`, and background servers get reaped by the sandbox — prefer `scripts/smoke.sh` over a long-lived `&` process.

## Gotchas

- Check `git diff -U0 <file>` before "fixing" a lint error: this tree often carries unrelated uncommitted work, sometimes from a second agent.
- Tests that call play mutations need the session's `ownerToken`; `browser.play()` in `api/accounts_test.go` already threads it.
- Do not hand-edit `sessions/*.json` on a live server: shutdown persists in-memory state over it. `just stop`, edit, `jq`-diff the file, `just restart`.
