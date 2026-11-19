# Routing

One listener, one port, path-based routing. The platform mounts each game's
declared API surface under its prefix; everything else falls through to the
single SvelteKit app.

| path | owner |
|---|---|
| `/<game>/graphql` | that game's GraphQL handler |
| `/<game>/mcp` | that game's MCP transport (where it has one) |
| `/<game>/llms.txt` | platform, rendered from the game's template |
| `/<game>/...` | the SvelteKit app |

Games see paths with their prefix stripped, so their own mux keeps absolute
paths such as `/graphql`.

## Removed legacy routes

Tesla used to serve a REST surface (`/api/sessions`, `/api/sessions/{id}/...`)
and a raw `/ws` endpoint. Both existed for the terminal client and older
scripts. They were deleted with `games/tesla/cmd/tui` in the Tesla migration:

- GraphQL at `/tesla/graphql` is the only game API.
- MCP is Streamable HTTP at `/tesla/mcp`, opt-in via `TESLA_MCP_ENABLED=true`.
- There is no stdio MCP mode (`drbrain mcp` fails with an explicit message).

No unprefixed route survives; the old domains only redirect (deploy cutover).
