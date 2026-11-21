// Package config holds the process-wide settings of cmd/drbrain. Per-game
// settings stay in each game's own prefixed env section, parsed by that game.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config is the platform configuration, read from DRBRAIN_* environment
// variables.
type Config struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string `envconfig:"DRBRAIN_ADDR" default:":8080"`
	// Games is a comma-separated list of games to serve. Empty means every
	// compiled-in game; --games overrides it.
	Games string `envconfig:"DRBRAIN_GAMES"`
	// PublicURL is the external base URL used in generated links and llms.txt.
	PublicURL string `envconfig:"DRBRAIN_PUBLIC_URL"`
	// StaticDir is the root of the built SvelteKit app.
	StaticDir string `envconfig:"DRBRAIN_STATIC_DIR" default:"static"`
	// ShutdownTimeout bounds HTTP drain and each game's save.
	ShutdownTimeout time.Duration `envconfig:"DRBRAIN_SHUTDOWN_TIMEOUT" default:"10s"`
	// Debug raises the log level.
	Debug bool `envconfig:"DRBRAIN_DEBUG"`
	// AllowedOrigins is the comma-separated origin allowlist for CORS and
	// websocket upgrades. Empty or "*" allows every origin.
	AllowedOrigins string `envconfig:"DRBRAIN_ALLOWED_ORIGINS"`
	// DatabaseURL is required: every process stores accounts and sessions in
	// PostgreSQL. Only maps (and the built web app) are read from disk.
	DatabaseURL string `envconfig:"DRBRAIN_DATABASE_URL"`
	// AuthPassword enables username/password sign-up and sign-in.
	AuthPassword bool `envconfig:"DRBRAIN_AUTH_PASSWORD" default:"true"`
	// AuthJWTSecret signs access tokens (HS256). Empty means a random
	// per-process key: fine for development, wrong for production (tokens stop
	// working on restart and differ between instances).
	AuthJWTSecret string `envconfig:"DRBRAIN_AUTH_JWT_SECRET"`
	// OAuth providers are enabled when both their id and secret are set.
	GoogleClientID     string `envconfig:"DRBRAIN_AUTH_GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `envconfig:"DRBRAIN_AUTH_GOOGLE_CLIENT_SECRET"`
	GitHubClientID     string `envconfig:"DRBRAIN_AUTH_GITHUB_CLIENT_ID"`
	GitHubClientSecret string `envconfig:"DRBRAIN_AUTH_GITHUB_CLIENT_SECRET"`
	// AuthDevFake adds a local-only fake provider.
	AuthDevFake bool `envconfig:"DRBRAIN_AUTH_DEV_FAKE"`
}

// Load reads Config from the environment and validates it.
func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return Config{}, err
	}
	// Older deployments set the per-game ALLOWED_ORIGINS. Keep honoring it
	// until the cutover is complete.
	if c.AllowedOrigins == "" {
		c.AllowedOrigins = os.Getenv("ALLOWED_ORIGINS")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate rejects settings that would fail confusingly later.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("DRBRAIN_ADDR is empty")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("DRBRAIN_SHUTDOWN_TIMEOUT must be positive, got %s", c.ShutdownTimeout)
	}
	if strings.TrimSpace(c.StaticDir) == "" {
		return errors.New("DRBRAIN_STATIC_DIR is empty")
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("DRBRAIN_DATABASE_URL is required: accounts and sessions live in PostgreSQL")
	}
	for _, name := range c.EnabledGames() {
		if name == "" {
			return errors.New("DRBRAIN_GAMES contains an empty name")
		}
	}
	return nil
}

// EnabledGames splits Games into names, dropping whitespace and empties. A nil
// result means "all compiled-in games".
func (c Config) EnabledGames() []string {
	if strings.TrimSpace(c.Games) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(c.Games, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}
