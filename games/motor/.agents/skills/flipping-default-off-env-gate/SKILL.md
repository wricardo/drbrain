---
name: flipping-default-off-env-gate
description: "How to flip an environment feature gate from default-off to default-on without breaking startup, CSRF/origin checks, tests, docs, local tooling or the deploy smoke test (drbrain-motor-programming convention)."
---

# Flipping a default-off env gate to default-on

Applies to `drbrain-motor-programming` (Go server + committed SvelteKit `static/`), and to similar repos where a feature gate is read from the environment at startup.

## The risk to check first

A previously-optional gate going default-on **silently promotes every startup validation that gate triggers into a boot requirement**. Before touching the default, grep for what the gate unlocks:

```sh
rg -n 'AuthPassword|AUTH_PASSWORD' internal/appconfig   # the gate, parsed once
rg -n 'ProvidersConfig|auth.Providers'                  # what it unlocks
rg -n 'public-url|PublicURL' --glob '*.go'      # hard requirements it now pulls in
```

In this repo, `AUTH_PASSWORD` being on once made the provider setup demand `-public-url`, which `go run .`, the `justfile` recipes and `scripts/smoke.sh` did not pass — every local run would fail to boot. Gates now live in `internal/appconfig`; `auth.Providers` still validates `-public-url` for redirect providers.

## Separate the requirements

Split "this feature needs X" from "sign-in needs X". A redirect provider needs an exact registered callback URL; a credential-based one does not. Make the base URL nilable (`a.base == nil`) and nil-check it in the cookie `secure()` flag and the same-origin `ownOrigins()`.

## Do not loosen a CSRF/origin check to make it boot

Falling back to the request's own `Host` is only safe on local hosts. On a public host a DNS-rebinding page's origin *is* the hostname it made resolve here, so it matches its own `Origin`. Bound the fallback with `IsLocalHost(r.Host)` and refuse (plus log a warning) when the origin cannot be established.

## Checklist

1. Invert the parser: default-on means the switch returns true and only explicit falsy values (`false`/`0`/`no`) disable it.
2. Update the test that asserts the old default, and add coverage for the new path — including the *negative* security case (e.g. `TestPasswordSignInWithoutPublicURLRefusesPublicHosts`).
3. Docs: `docs/DEVELOPMENT.md` config table, the feature section and Owner setup; `.env.example`; `llms.txt.tmpl`.
4. Local tooling: pass the now-needed flag (`justfile` → `-public-url http://localhost:<port>`, overridable), and give smoke scripts isolated state dirs (`-accounts-dir`) so they stop creating tracked-looking dirs in the repo.
5. Deploy smoke test: assert the feature is actually offered (`authProviders { id }` contains `password`), not just that the server answers.
6. Verify by hand both ways — no env at all (feature on) and the explicit off value (routes 404, provider list empty) — then `go vet ./...`, `go test -race ./...`, `scripts/smoke.sh`.
7. State the blast radius to the user: merging deploys it, and any new runtime directory (`accounts/`) must be backed up and preserved across deploys.
