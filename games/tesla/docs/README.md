# Tesla Road Trip — Documentation

Tesla is one game in the Dr Brain monorepo. The current developer entry points
are:

- [Game README](../README.md) — rules, environment, routes, quick start
- [MCP tools](../MCP_TOOLS.md) — tools exposed at `POST /tesla/mcp`
- [Repository AGENTS guide](../../../AGENTS.md) — architecture, schema pipeline,
  test commands
- [Routing](../../../docs/routing.md) — one listener, one `/graphql`, per-game
  prefixes

## Current reference

- **[Configuration Schema](config-schema.md)** — builtin map JSON format and
  validation
- **[AI Strategy Guide](ai-strategy.md)** — gameplay heuristics for agents
  (endpoint examples predate the unified namespace; see the banner in the file)

## Historical (standalone repository era)

These documents describe the game as it existed before it was merged into the
monorepo. Paths, commands, transports and storage in them are not current.
They are kept for design history:

- [GraphQL API reference](graphql.md)
- [UI pages](ui-pages-description.md)
- [Refactoring epic](epic-architectural-refactoring.md)
- [Architecture refactoring diagram](architecture-refactoring-diagram.md)
- [Stories](stories/)
- [Archive](archive/)

For the current API, use the canonical schema at `graph/schema.graphqls` and the
generated `games/tesla/graph/schema.graphqls`; every operation is under the
`tesla` namespace, and the subscription root is `teslaSessionUpdated`.
