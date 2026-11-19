// Package appconfig loads the process configuration from environment variables
// (and the .env file the commands load first) into one struct, so parsing rules
// live in a single tested place.
//
// Every field is seeded by Default and then filled by envconfig, so an unset
// variable keeps its default. A variable set to an empty value also keeps it:
// os.LookupEnv cannot tell "set to nothing" from "not set", and that is what
// the documented defaults promise.
package appconfig

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/wricardo/drbrain/games/motor/game/service"
)

const (
	// DefaultMaxSessions caps live sessions (MAX_SESSIONS).
	DefaultMaxSessions = 1000
	// DefaultMaxUserMaps caps player-created maps (MAX_USER_MAPS).
	DefaultMaxUserMaps = 200
	// DefaultSessionTTL is the idle lifetime of a live session (SESSION_TTL).
	DefaultSessionTTL = 7 * 24 * time.Hour
	// DefaultSweepInterval is how often idle sessions are swept while the
	// server runs (SESSION_SWEEP_INTERVAL).
	DefaultSweepInterval = time.Hour
)

// Config is the server's environment configuration. See .env.example and the
// Configuration section of docs/DEVELOPMENT.md for what each variable does.
type Config struct {
	GraphQLIntrospection      Flag       `envconfig:"GRAPHQL_INTROSPECTION"`
	GraphQLPlayground         Flag       `envconfig:"GRAPHQL_PLAYGROUND"`
	AdminKey                  string     `envconfig:"ADMIN_API_KEY"`
	AllowUnauthenticatedAdmin StrictFlag `envconfig:"ALLOW_UNAUTHENTICATED_ADMIN"`

	// DatabaseURL is the PostgreSQL connection string; empty keeps sessions,
	// accounts, logins and player maps in JSON files.
	DatabaseURL string `envconfig:"DATABASE_URL"`

	MaxUserMaps          PositiveInt `envconfig:"MAX_USER_MAPS"`
	MaxSessions          PositiveInt `envconfig:"MAX_SESSIONS"`
	MaxAccounts          PositiveInt `envconfig:"MAX_ACCOUNTS"`
	SessionTTL           Duration    `envconfig:"SESSION_TTL"`
	SessionSweepInterval Duration    `envconfig:"SESSION_SWEEP_INTERVAL"`

	// AllowedOrigins is the comma-separated origin allowlist for WebSocket
	// upgrades and CORS; empty or "*" allows all.
	AllowedOrigins string `envconfig:"ALLOWED_ORIGINS"`

	AuthGoogleClientID     string `envconfig:"AUTH_GOOGLE_CLIENT_ID"`
	AuthGoogleClientSecret string `envconfig:"AUTH_GOOGLE_CLIENT_SECRET"`
	AuthGitHubClientID     string `envconfig:"AUTH_GITHUB_CLIENT_ID"`
	AuthGitHubClientSecret string `envconfig:"AUTH_GITHUB_CLIENT_SECRET"`
	AuthPassword           Switch `envconfig:"AUTH_PASSWORD"`
	AuthDevFake            Flag   `envconfig:"AUTH_DEV_FAKE"`
}

// Default returns the configuration with every documented fallback applied.
func Default() Config {
	return Config{
		GraphQLIntrospection: true,
		GraphQLPlayground:    true,
		MaxUserMaps:          newPositiveInt("MAX_USER_MAPS", DefaultMaxUserMaps),
		MaxSessions:          newPositiveInt("MAX_SESSIONS", DefaultMaxSessions),
		MaxAccounts:          newPositiveInt("MAX_ACCOUNTS", service.DefaultMaxAccounts),
		SessionTTL:           newDuration("SESSION_TTL", DefaultSessionTTL),
		SessionSweepInterval: newDuration("SESSION_SWEEP_INTERVAL", DefaultSweepInterval),
		AuthPassword:         true,
	}
}

// Load reads the configuration from the environment on top of Default.
func Load() (Config, error) {
	cfg := Default()
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, err
	}
	// Credentials and the database URL are pasted by hand, so surrounding
	// spaces are noise rather than a secret.
	cfg.DatabaseURL = strings.TrimSpace(cfg.DatabaseURL)
	cfg.AuthGoogleClientID = strings.TrimSpace(cfg.AuthGoogleClientID)
	cfg.AuthGoogleClientSecret = strings.TrimSpace(cfg.AuthGoogleClientSecret)
	cfg.AuthGitHubClientID = strings.TrimSpace(cfg.AuthGitHubClientID)
	cfg.AuthGitHubClientSecret = strings.TrimSpace(cfg.AuthGitHubClientSecret)
	return cfg, nil
}

// Flag is a boolean env var read forgivingly: true, 1, yes and on (in any case,
// surrounding spaces ignored) turn it on and any other value turns it off, so a
// capitalised value cannot silently mean the opposite of what the operator
// asked for. An explicitly empty value keeps the default.
type Flag bool

// Decode implements envconfig.Decoder.
func (f *Flag) Decode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		// Set but empty means unset: keep the seeded default.
	case "true", "1", "yes", "on":
		*f = true
	default:
		*f = false
	}
	return nil
}

// Bool reports the flag.
func (f Flag) Bool() bool { return bool(f) }

// StrictFlag is a boolean env var that only the exact value "true" turns on;
// anything else (including a missing value) keeps it off. It gates
// ALLOW_UNAUTHENTICATED_ADMIN, where a typo must not open the gate.
type StrictFlag bool

// Decode implements envconfig.Decoder.
func (f *StrictFlag) Decode(value string) error {
	*f = value == "true"
	return nil
}

// Bool reports the flag.
func (f StrictFlag) Bool() bool { return bool(f) }

// Switch is a boolean env var that is on unless set to false, 0 or no
// (AUTH_PASSWORD: password sign-in needs nothing registered anywhere, so the
// absence of configuration means on). An explicitly empty value keeps the
// default.
type Switch bool

// Decode implements envconfig.Decoder.
func (s *Switch) Decode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
	case "false", "0", "no":
		*s = false
	default:
		*s = true
	}
	return nil
}

// Bool reports the switch.
func (s Switch) Bool() bool { return bool(s) }

// PositiveInt is an integer env var whose invalid values are logged and
// ignored: a cap of zero or less would disable the limit, so the default must
// survive. An explicitly empty value keeps the default silently.
type PositiveInt struct {
	value int
	name  string
}

func newPositiveInt(name string, def int) PositiveInt {
	return PositiveInt{value: def, name: name}
}

// Decode implements envconfig.Decoder.
func (p *PositiveInt) Decode(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		log.Printf("ignoring invalid %s=%q, using %d", p.name, value, p.value)
		return nil
	}
	p.value = n
	return nil
}

// Int reports the value.
func (p PositiveInt) Int() int { return p.value }

// Duration is a Go duration env var (e.g. "168h") whose invalid or negative
// values are logged and ignored, keeping the default.
type Duration struct {
	value time.Duration
	name  string
}

func newDuration(name string, def time.Duration) Duration {
	return Duration{value: def, name: name}
}

// Decode implements envconfig.Decoder.
func (d *Duration) Decode(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		log.Printf("ignoring invalid %s=%q, using %s", d.name, value, d.value)
		return nil
	}
	d.value = parsed
	return nil
}

// Duration reports the value.
func (d Duration) Duration() time.Duration { return d.value }
