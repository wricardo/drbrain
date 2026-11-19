---
name: drbrain-docs-audit
description: "Use when reviewing or updating documentation in drbrain-motor-programming (README, docs/DEVELOPMENT.md, llms.txt.tmpl, CLAUDE.md, Go doc comments) — verifies each claim by execution rather than re-reading prose."
---

# Auditing drbrain-motor-programming docs

Docs-only scope: README.md, docs/DEVELOPMENT.md, llms.txt.tmpl, CLAUDE.md (AGENTS.md symlinks it), .env.example, justfile comments, Go doc comments. Do NOT touch behaviour, tests, static/, frontend logic, or graph/generated.

Re-reading prose misses drift. Every claim must be reproduced.

## 1. Find what changed

```sh
cd /Users/wricardo/go/src/github.com/wricardo/drbrain-motor-programming
git diff --stat
git diff docs/ graph/schema.graphqls api/server.go auth/auth.go game/service/ | cat
```

Peers often edit the same docs concurrently: re-run `git diff docs/DEVELOPMENT.md` immediately before each edit and re-read the exact line you are about to change.

## 2. Run the GraphQL examples

```sh
cd /Users/wricardo/go/src/github.com/wricardo/drbrain-motor-programming
go build -o /tmp/probe/srv . && rm -rf /tmp/probe/s /tmp/probe/m /tmp/probe/a
nohup /tmp/probe/srv -port 9311 -host 127.0.0.1 \
  -sessions-dir /tmp/probe/s -user-maps-dir /tmp/probe/m \
  -accounts-dir /tmp/probe/a >/tmp/probe/log 2>&1 &
sleep 2 && curl -sf http://127.0.0.1:9311/healthz
```

Helper, then paste each documented example in verbatim:

```sh
U=http://127.0.0.1:9311/graphql
q(){ curl -s -H 'Content-Type: application/json' -d "$(jq -nc --arg q "$1" '{query:$q}')" "$U"; }
```

Real find: README's four-call example ended `run(...) { id status }`, but `status` lives under `vm` — GRAPHQL_VALIDATION_FAILED.

**llms.txt.tmpl is embedded at build time.** Editing the template does not change a running binary; rebuild and restart before grepping `/llms.txt`, or you "confirm" text that is absent.

Also check the creator-only play model: `setProgram`/`run`/`step`/`pause`/`reset` need `ownerToken`; `canPlaySession(id, ownerToken)` reports it. Without a token the answer is `FORBIDDEN`, so every documented agent example needs the token threaded through.

## 3. Measure numbers, never copy them

Forcing numbers come from helpers in `validate/validate_test.go` (`minActions`, `maxLeafActions`, `maxForwardMoves`, `requiredMoves`). Add a throwaway probe, run it, delete it, prove the tree is clean:

```sh
# validate/zz_probe_test.go calling the helpers, then:
go test -run TestZZDocProbe ./validate -v 2>&1 | grep PROBE
rm validate/zz_probe_test.go && go vet ./validate
```

A bound only proves forcing when the ceiling is BELOW the requirement. `maxLeafActions` at 2 levels can exceed the requirement where 1 level does not (`switchback`: 22 then 132 vs 36 needed; `mountain_range`: 27 vs 12 needed). For those, say recursion is proven only by the reference solution losing without it. `solutions/` is gitignored and usually missing, so `just validate` and solution tests skip — do not invent bounds you cannot run.

## 4. Count inventories

```sh
ls maps/*.json | wc -l
grep -n 'golden := ' -A 4 validate/validate_test.go
```

DEVELOPMENT.md claimed "Shipped maps (11)" while 17 shipped; six maps were missing from the forcing table entirely.

## 5. Error codes and schema surface

- Codes: `game/service/errors.go` plus `CodeUnauthenticated` in `game/service/accounts.go` — lists routinely omit UNAUTHENTICATED.
- Diff the Queries/Mutations bullets against `graph/schema.graphqls` field by field; new fields (`canPlaySession`) get missed.
- Route tables: read the Svelte page to see conditional UI (`Take over` needs an owner token; `/play` shows an amber read-only notice and locks controls when `canPlay` is false).
- Password sign-in is two mutations (`registerWithPassword`, `logInWithPassword`), not `/auth/password/*` routes; only redirect providers keep `/auth/<provider>/login|callback`. The mutations set the cookie on their own response, so a websocket subscription is refused (`FORBIDDEN`).

## 6. Doc comments that contradict their own file

```sh
rg -n "not gated|stays open|deliberately|anyone who knows" --glob '*.go' .
```

Found `accounts.go` claiming playing "is deliberately not gated" while the same file defined `AuthorizeSessionPlay`. Comments are docs: fix them, leave code alone.

## 7. Finish

```sh
go build ./... && gofmt -l $(find . -name '*.go' -not -path './frontend/*' -not -path './graph/generated/*')
for f in README.md docs/DEVELOPMENT.md CLAUDE.md llms.txt.tmpl; do echo "$f: $(grep -c '^```' $f)"; done
grep -o '(#[a-z0-9-]*)' docs/DEVELOPMENT.md | sort -u   # every anchor must exist as a heading
./scripts/smoke.sh 9291
```

Report each fix as file:line → old claim → how you reproduced the failure. Explicitly flag anything unverified (missing `solutions/`, unmeasurable claims) instead of quietly documenting a guess.
