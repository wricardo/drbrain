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

	"github.com/jackc/pgx/v5/pgxpool"

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
	// ErrMapForbidden means the actor does not own the map and is not an admin.
	ErrMapForbidden = errors.New("map belongs to another player")
	// ErrMapLimit means the player-map quota is exhausted.
	ErrMapLimit = errors.New("map limit reached")
)

// Manager handles game configuration loading and caching.
//
// Shipped maps live in ConfigDir and are read-only through the API. Player
// maps live in PostgreSQL (owner, draft/published state, edit tokens); the file
// rules, ownership, quotas and tokens live in platform/mapstore. Without a pool
// the store falls back to the file catalogue (ConfigDir plus an optional custom
// directory), which tests and file-only tools use.
type Manager struct {
	configDir     string
	store         *mapstore.Store[*engine.GameConfig]
	defaultConfig *engine.GameConfig
	mu            sync.RWMutex
}

// Options configures a Manager.
type Options struct {
	// ConfigDir holds the shipped maps. It is required.
	ConfigDir string
	// Pool stores player maps in PostgreSQL. Production always sets it; nil
	// selects the file catalogue.
	Pool *pgxpool.Pool
	// MaxUserMaps caps how many player maps may exist across all accounts
	// (0 = no cap).
	MaxUserMaps int
	// CustomDir is the file-mode custom directory. Empty means
	// "<ConfigDir>/custom". It is ignored when Pool is set and never comes
	// from TESLA_CUSTOM_MAPS_DIR at runtime.
	CustomDir string
}

// NewManager creates a manager for the shipped and player maps.
func NewManager(opts Options) (*Manager, error) {
	if _, err := os.Stat(opts.ConfigDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("config directory does not exist: %s", opts.ConfigDir)
	}
	store, err := mapstore.Open(mapstore.Options[*engine.GameConfig]{
		BuiltinDir: opts.ConfigDir,
		CustomDir:  strings.TrimSpace(opts.CustomDir),
		Game:       "tesla",
		Pool:       opts.Pool,
		// Structural checks only on read and draft save; winnability is the
		// publication gate because the search is expensive.
		Validate:        engine.ValidateGameConfigStructure,
		PublishValidate: engine.ValidateGameConfig,
		MaxCustomMaps:   opts.MaxUserMaps,
		IssueEditTokens: true,
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
	})
	if err != nil {
		return nil, err
	}
	m := &Manager{configDir: opts.ConfigDir, store: store}
	if err := m.loadDefaultConfig(); err != nil {
		return nil, fmt.Errorf("failed to load default config: %w", err)
	}
	return m, nil
}

// CustomDir returns the file-mode custom directory; empty in PostgreSQL mode.
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

// ListConfigs returns information about all playable maps. Other players'
// drafts stay hidden; ListEntries exposes the viewer's own drafts too.
func (m *Manager) ListConfigs() ([]*service.MapInfo, error) {
	entries, err := m.store.ListFor("")
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
//
// This is the pre-draft API: it only stores published maps, so it keeps the
// legacy winnability gate (new code uses CreateDraft plus Publish).
func (m *Manager) CreateCustom(name string, config *engine.GameConfig) (string, error) {
	if err := validateConfigName(name); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := validatePublishable(config); err != nil {
		return "", err
	}
	token, err := m.store.Create(name, config)
	if err != nil {
		return "", mapError(err)
	}
	return token, nil
}

// SaveConfig overwrites an existing custom map. Built-in maps are refused;
// admin writes go through SaveBuiltin. Like CreateCustom it keeps the legacy
// winnability gate.
func (m *Manager) SaveConfig(name string, config *engine.GameConfig) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := validatePublishable(config); err != nil {
		return err
	}
	if err := m.store.Save(name, config, false); err != nil {
		return mapError(err)
	}
	return nil
}

// SaveBuiltin overwrites a built-in map. The transport authenticates the admin
// key before calling it. Shipped maps are always published, so they keep the
// winnability gate.
func (m *Manager) SaveBuiltin(name string, config *engine.GameConfig) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := validatePublishable(config); err != nil {
		return err
	}
	if err := m.store.SaveBuiltin(name, config, true); err != nil {
		return mapError(err)
	}
	return nil
}

// validatePublishable is the legacy write gate: a map the pre-draft API stored
// was immediately playable, so it had to pass the winnability proof.
func validatePublishable(config *engine.GameConfig) error {
	if err := engine.ValidateGameConfig(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
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

// ListEntries returns the full catalogue for a viewer: shipped maps, published
// player maps and the viewer's own drafts. Other players' drafts stay hidden.
func (m *Manager) ListEntries(viewerID string) ([]*service.MapEntry, error) {
	rows, err := m.store.ListFor(viewerID)
	if err != nil {
		return nil, fmt.Errorf("failed to read map catalogue: %w", err)
	}
	out := make([]*service.MapEntry, len(rows))
	for i, row := range rows {
		out[i] = toMapEntry(row)
	}
	return out, nil
}

// GetEntry returns one catalogue entry with its owner and lifecycle state.
func (m *Manager) GetEntry(name string) (*service.MapEntry, error) {
	if err := validateConfigName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	entry, err := m.store.Get(name)
	if err != nil {
		return nil, lifecycleError(err)
	}
	return toMapEntry(entry), nil
}

// CreateDraft stores a new DRAFT map owned by ownerID and returns its edit
// token. The draft only needs to be structurally valid; publication runs the
// winnability check.
func (m *Manager) CreateDraft(name string, config *engine.GameConfig, ownerID string) (string, error) {
	if err := validateConfigName(name); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	token, err := m.store.CreateDraft(name, config, ownerID)
	if err != nil {
		return "", lifecycleError(err)
	}
	return token, nil
}

// SaveOwned overwrites a map on behalf of actor. Owners edit their own maps (a
// published map returns to DRAFT); admins may edit shipped maps.
func (m *Manager) SaveOwned(name string, config *engine.GameConfig, actor service.MapActor) (*service.MapEntry, error) {
	if err := validateConfigName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	entry, err := m.store.SaveOwned(name, config, mapActor(actor))
	if err != nil {
		return nil, lifecycleError(err)
	}
	return toMapEntry(entry), nil
}

// Publish promotes the actor's draft after running the winnability check.
func (m *Manager) Publish(name string, actor service.MapActor) (*service.MapEntry, error) {
	if err := validateConfigName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	entry, err := m.store.Publish(name, mapActor(actor))
	if err != nil {
		return nil, lifecycleError(err)
	}
	return toMapEntry(entry), nil
}

// DeleteOwned removes a map on behalf of actor: owners delete their own maps,
// admins may delete shipped maps.
func (m *Manager) DeleteOwned(name string, actor service.MapActor) error {
	if err := validateConfigName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := m.store.DeleteOwned(name, mapActor(actor)); err != nil {
		return lifecycleError(err)
	}
	return nil
}

// Claim attaches an ownerless custom row (an imported map) to ownerID after
// checking its edit token. Claiming a row you already own is a no-op.
func (m *Manager) Claim(name, ownerID, token string) (*service.MapEntry, error) {
	if err := validateConfigName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	entry, err := m.store.Claim(name, ownerID, token)
	if err != nil {
		return nil, lifecycleError(err)
	}
	return toMapEntry(entry), nil
}

// toMapEntry converts a storage row into the transport-facing entry.
func toMapEntry(row mapstore.Map[*engine.GameConfig]) *service.MapEntry {
	status := service.MapStatusPublished
	if row.Status == mapstore.StatusDraft {
		status = service.MapStatusDraft
	}
	return &service.MapEntry{
		Name:    row.Name,
		Config:  row.Config,
		Builtin: row.Builtin,
		OwnerID: row.OwnerID,
		Status:  status,
	}
}

func mapActor(actor service.MapActor) mapstore.Actor {
	return mapstore.Actor{AccountID: actor.AccountID, Admin: actor.Admin}
}

// lifecycleError translates storage errors into the service's public
// vocabulary, keeping platform/mapstore out of the transport.
func lifecycleError(err error) error {
	switch {
	case errors.Is(err, mapstore.ErrNotFound):
		return service.ErrMapNotFound
	case errors.Is(err, mapstore.ErrForbidden):
		return service.ErrMapForbidden
	case errors.Is(err, mapstore.ErrExists):
		return service.ErrMapExists
	case errors.Is(err, mapstore.ErrReadOnly):
		return service.ErrMapReadOnly
	case errors.Is(err, mapstore.ErrLimit):
		return service.ErrMapLimit
	case errors.Is(err, mapstore.ErrEditTokenRequired):
		return service.ErrEditTokenRequired
	case errors.Is(err, mapstore.ErrInvalidToken):
		return service.ErrInvalidEditToken
	case errors.Is(err, mapstore.ErrInvalid):
		return fmt.Errorf("%w: %v", service.ErrMapInvalid, err)
	}
	return err
}

// DeleteDraftsByOwner removes every draft owned by ownerID. Account deletion
// calls it before published maps are orphaned.
func (m *Manager) DeleteDraftsByOwner(ownerID string) error {
	if err := m.store.DeleteDraftsByOwner(ownerID); err != nil {
		return mapError(err)
	}
	return nil
}

// OrphanPublishedByOwner detaches a deleted account's published maps so they
// stay playable.
func (m *Manager) OrphanPublishedByOwner(ownerID string) error {
	if err := m.store.OrphanPublishedByOwner(ownerID); err != nil {
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
	case errors.Is(err, mapstore.ErrForbidden):
		return ErrMapForbidden
	case errors.Is(err, mapstore.ErrLimit):
		return ErrMapLimit
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
