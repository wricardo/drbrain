// Package appconfig centralizes the server's environment configuration.
//
// Every environment variable the server reads is declared here exactly once and
// loaded with kelseyhightower/envconfig, so defaults, parsing rules, and
// validation live in one place instead of scattered os.Getenv calls.
//
// Parsing rules intentionally match the hand-rolled helpers this replaced:
//   - feature flags accept "true", "1", and "yes" as true, anything else false
//   - an empty value means "keep the default"
//   - AllowUnauthenticatedAdmin is strict: only the exact string "true" enables it
package appconfig

import (
	"net/http"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

// Flag is a forgiving boolean: "true", "1", and "yes" are true; any other value
// is false. An empty value keeps the seeded default.
type Flag bool

// Decode implements envconfig.Decoder.
func (f *Flag) Decode(value string) error {
	switch value {
	case "":
		// Unset or empty keeps the default (mirrors the old envBool helper).
	case "true", "1", "yes":
		*f = true
	default:
		*f = false
	}
	return nil
}

// StrictFlag is enabled only by the exact string "true".
type StrictFlag bool

// Decode implements envconfig.Decoder.
func (f *StrictFlag) Decode(value string) error {
	*f = value == "true"
	return nil
}

// Origins is a comma-separated origin allowlist. Empty or "*" means "allow all",
// represented as a nil slice.
type Origins []string

// Decode implements envconfig.Decoder.
func (o *Origins) Decode(value string) error {
	if value = strings.TrimSpace(value); value == "" || value == "*" {
		*o = nil
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	*o = out
	return nil
}

// DefaultMaxUserMaps caps player-created maps (TESLA_MAX_USER_MAPS).
const DefaultMaxUserMaps = 200

// Config is the complete environment-driven configuration of the server.
type Config struct {
	// Directories. Sessions and player maps live in PostgreSQL; only the
	// shipped maps are read from disk.
	ConfigDir string `envconfig:"CONFIG_DIR"`
	// CustomMapsDir is deprecated: custom maps live in PostgreSQL. The
	// variable is still parsed so deployments that set it do not fail, but it
	// is never used as storage.
	CustomMapsDir string `envconfig:"CUSTOM_MAPS_DIR"`

	// MaxUserMaps caps how many player-created maps tesla keeps (0 = no cap).
	MaxUserMaps int `envconfig:"MAX_USER_MAPS"`

	// Feature gates. All default to enabled for backward compatibility.
	GraphQLIntrospection Flag `envconfig:"GRAPHQL_INTROSPECTION"`
	GraphQLPlayground    Flag `envconfig:"GRAPHQL_PLAYGROUND"`
	MCPEnabled           Flag `envconfig:"MCP_ENABLED"`

	// Admin gates map mutations on built-in maps.
	AdminAPIKey               string     `envconfig:"ADMIN_API_KEY"`
	AllowUnauthenticatedAdmin StrictFlag `envconfig:"ALLOW_UNAUTHENTICATED_ADMIN"`

	// AllowedOrigins restricts browser WebSocket/GraphQL-ws origins.
	AllowedOrigins Origins `envconfig:"ALLOWED_ORIGINS"`
}

// Default returns the configuration used when nothing is set in the environment.
func Default() Config {
	return Config{
		ConfigDir:            "maps",
		MaxUserMaps:          DefaultMaxUserMaps,
		GraphQLIntrospection: true,
		GraphQLPlayground:    true,
		MCPEnabled:           true,
	}
}

// Load returns the defaults overlaid with the process environment. Every
// variable is prefixed TESLA_ (TESLA_CONFIG_DIR, TESLA_MCP_ENABLED, ...), so
// the game cannot read or be configured by another game's section.
func Load() (Config, error) {
	cfg := Default()
	if err := envconfig.Process("TESLA", &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// AdminPolicy decides whether a request may act on built-in maps.
//
// With no API key configured, only the local-development bypass grants access.
type AdminPolicy struct {
	APIKey               string
	AllowUnauthenticated bool
}

// Admin returns the policy described by the configuration.
func (c Config) Admin() AdminPolicy {
	return AdminPolicy{
		APIKey:               c.AdminAPIKey,
		AllowUnauthenticated: bool(c.AllowUnauthenticatedAdmin),
	}
}

// Authorized reports whether the request may perform admin-only operations.
// A nil request (no HTTP context) is never authorized.
func (p AdminPolicy) Authorized(r *http.Request) bool {
	if p.APIKey == "" {
		return p.AllowUnauthenticated
	}
	return r != nil && r.Header.Get("X-Admin-Key") == p.APIKey
}
