// Package delivery wires DrBrain Delivery into the platform: it loads
// DELIVERY_* configuration, opens the builtin/community city catalogue, builds
// the session manager, and hands the GraphQL resolver to the host's unified
// executor.
//
// The game is layered: engine is the pure world simulation, session owns the
// clock and durable saves, and graph is the GraphQL transport. Delivery
// publishes no per-game HTTP handlers. The host serves the SPA under /delivery
// and every operation at /graphql under the delivery namespace; Mount only
// wires dependencies and validates startup state.
package delivery
