// Package config loads, lists and saves game configurations, and owns the
// builtin/custom split and edit-token rules through platform/mapstore.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/platform/mapstore"
)

// editTokenSuffix is the custom-map sidecar used by platform/mapstore; kept
// here for tests that inspect the sidecar path.
const editTokenSuffix = ".edit-token"

var (
	ErrConfigNotFound = errors.New("configuration not found")
	ErrInvalidConfig  = errors.New("invalid configuration")
	// ErrMapExists is returned by CreateCustom when the name is already taken by
	// a built-in or another user map; creation never overwrites.
	ErrMapExists         = errors.New("map already exists")
	ErrReadOnlyMap       = errors.New("built-in map is read-only")
	ErrEditTokenRequired = errors.New("edit token required to modify this map")
	ErrInvalidEditToken  = errors.New("invalid edit token")
)

// Manager handles game configuration loading and caching.
//
// Maps live in two places: configDir (shipped, read-only for API users) and
// customDir (created through the API, each with an edit-token sidecar). The
// file rules and tokens live in platform/mapstore; this type keeps tesla's
// public API, default config and error vocabulary.
type Manager struct {
	configDir     string
	store         *mapstore.Store[*engine.GameConfig]
	defaultConfig *engine.GameConfig
	mu            sync.RWMutex
}

// NewManager creates a manager. User-created maps go to customDir when
// non-empty, otherwise "<configDir>/custom".
func NewManager(configDir, customDir string) (*Manager, error) {
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("config directory does not exist: %s", configDir)
	}
	store, err := mapstore.Open(mapstore.Options[*engine.GameConfig]{
		BuiltinDir: configDir,
		CustomDir:  strings.TrimSpace(customDir),
		Decode: func(raw []byte) (*engine.GameConfig, error) {
			var cfg engine.GameConfig
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
			return &cfg, nil
		},
		Encode: func(cfg *engine.GameConfig) ([]byte, error) {
			return json.MarshalIndent(cfg, "", "  ")
		},
		Validate: engine.ValidateGameConfig,
	})
	if err != nil {
		return nil, err
	}
	m := &Manager{configDir: configDir, store: store}
	if err := m.loadDefaultConfig(); err != nil {
		return nil, fmt.Errorf("failed to load default config: %w", err)
	}
	return m, nil
}

// CustomDir returns the directory holding user-created maps.
func (m *Manager) CustomDir() string { return m.store.CustomDir() }

// IsBuiltin reports whether name is a shipped map.
func (m *Manager) IsBuiltin(name string) bool { return m.store.IsBuiltin(name) }

// LoadConfig loads a configuration by name.
func (m *Manager) LoadConfig(name string) (*engine.GameConfig, error) {
	if err := validateConfigName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	entry, err := m.store.Get(name)
	if err != nil {
		return nil, mapError(err)
	}
	return entry.Config, nil
}

// ListConfigs returns information about all available maps.
func (m *Manager) ListConfigs() ([]*service.MapInfo, error) {
	entries, err := m.store.List()
	if err != nil {
		return nil, fmt.Errorf("failed to read config directory: %w", err)
	}
	maps := make([]*service.MapInfo, 0, len(entries))
	for _, e := range entries {
		maps = append(maps, &service.MapInfo{
			Filename:    e.Name + ".json",
			MapID:       e.Name,
			Name:        e.Config.Name,
			Description: e.Config.Description,
			GridSize:    e.Config.GridSize,
			MaxBattery:  e.Config.MaxBattery,
			Builtin:     e.Builtin,
		})
	}
	return maps, nil
}

// GetDefault returns the default configuration.
func (m *Manager) GetDefault() *engine.GameConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultConfig
}

// SetDefault sets the default configuration by name.
func (m *Manager) SetDefault(name string) error {
	config, err := m.LoadConfig(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultConfig = config
	return nil
}

// RefreshCache drops parsed configs and reloads the default.
func (m *Manager) RefreshCache() error {
	m.store.Refresh()
	return m.loadDefaultConfig()
}

// loadDefaultConfig loads classic.json, else the first available map, else a
// minimal built-in default.
func (m *Manager) loadDefaultConfig() error {
	config, err := m.LoadConfig("classic")
	if err != nil {
		configs, listErr := m.ListConfigs()
		if listErr != nil || len(configs) == 0 {
			m.setDefault(m.createMinimalConfig())
			return nil
		}
		config, err = m.LoadConfig(strings.TrimSuffix(configs[0].Filename, ".json"))
		if err != nil {
			m.setDefault(m.createMinimalConfig())
			return nil
		}
	}
	m.setDefault(config)
	return nil
}

func (m *Manager) setDefault(cfg *engine.GameConfig) {
	m.mu.Lock()
	m.defaultConfig = cfg
	m.mu.Unlock()
}

// CreateCustom stores a new user-created map and returns its edit token.
func (m *Manager) CreateCustom(name string, config *engine.GameConfig) (string, error) {
	if err := validateConfigName(name); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	token, err := m.store.Create(name, config)
	if err != nil {
		return "", mapError(err)
	}
	return token, nil
}

// SaveConfig overwrites an existing custom map. Built-in maps are refused;
// admin writes go through SaveBuiltin.
func (m *Manager) SaveConfig(name string, config *engine.GameConfig) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := m.store.Save(name, config, false); err != nil {
		return mapError(err)
	}
	return nil
}

// SaveBuiltin overwrites a built-in map. The transport authenticates the admin
// key before calling it.
func (m *Manager) SaveBuiltin(name string, config *engine.GameConfig) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := m.store.SaveBuiltin(name, config, true); err != nil {
		return mapError(err)
	}
	return nil
}

// DeleteBuiltin removes a built-in map. The transport authenticates first.
func (m *Manager) DeleteBuiltin(name string) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := m.store.DeleteBuiltin(name, true); err != nil {
		return mapError(err)
	}
	return nil
}

// DeleteConfig removes a user-created map and its token. Built-ins are refused.
func (m *Manager) DeleteConfig(name string) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := m.store.Delete(name, false); err != nil {
		return mapError(err)
	}
	return nil
}

// VerifyEditToken reports whether token authorizes modifying a custom map.
func (m *Manager) VerifyEditToken(name, token string) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := m.store.VerifyEditToken(name, token); err != nil {
		return mapError(err)
	}
	return nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, mapstore.ErrNotFound):
		return ErrConfigNotFound
	case errors.Is(err, mapstore.ErrExists):
		return ErrMapExists
	case errors.Is(err, mapstore.ErrReadOnly):
		return ErrReadOnlyMap
	case errors.Is(err, mapstore.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	case errors.Is(err, mapstore.ErrEditTokenRequired):
		return ErrEditTokenRequired
	case errors.Is(err, mapstore.ErrInvalidToken):
		return ErrInvalidEditToken
	}
	return err
}

func validateConfigName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	base := strings.TrimSuffix(name, ".json")
	if base == "" || base == "." || base == ".." || filepath.IsAbs(name) || strings.ContainsAny(name, `/\\`) || strings.Contains(base, "..") {
		return fmt.Errorf("invalid map name %q", name)
	}
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid map name %q: use letters, numbers, hyphen, or underscore", name)
	}
	return nil
}

func (m *Manager) createMinimalConfig() *engine.GameConfig {
	return &engine.GameConfig{
		Name:            "default",
		Description:     "Default minimal configuration",
		GridSize:        5,
		MaxBattery:      10,
		StartingBattery: 10,
		Legend:          map[string]string{"R": "road", "H": "home", "P": "park", "S": "supercharger", "W": "water", "B": "building"},
		Layout: []string{
			"RRPRR",
			"RRRHR",
			"RRSRR",
			"RRRHR",
			"RRPRR",
		},
	}
}
