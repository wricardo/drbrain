// Package config holds the process-wide settings of cmd/drbrain. Per-game
// settings stay in each game's own prefixed env section, parsed by that game.
package config

import (
	"errors"
	"fmt"
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
}

// Load reads Config from the environment and validates it.
func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return Config{}, err
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
