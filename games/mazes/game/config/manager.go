// Package config loads, lists and saves map files through platform/mapstore.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/platform/mapstore"
)

// Manager is a mapstore-backed service.MapStore. Parsed maps are revalidated on
// every read; the store caches the decoded configs.
type Manager struct {
	dir   string
	store *mapstore.Store[engine.MapConfig]
}

var _ service.MapStore = (*Manager)(nil)

// NewManager opens dir as a single-directory catalogue. Every existing file is
// validated at startup (an invalid map is a fatal error naming the file).
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create maps dir: %w", err)
	}
	store, err := mapstore.Open(mapstore.Options[engine.MapConfig]{
		BuiltinDir: dir,
		CustomDir:  dir,
		Decode: func(raw []byte) (engine.MapConfig, error) {
			var cfg engine.MapConfig
			err := json.Unmarshal(raw, &cfg)
			return cfg, err
		},
		Encode: func(cfg engine.MapConfig) ([]byte, error) {
			raw, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return nil, err
			}
			return append(raw, '\n'), nil
		},
		Validate: func(cfg engine.MapConfig) error {
			_, err := engine.NewMap(cfg)
			return err
		},
		// Mazes has one admin-managed directory; it never issues edit tokens.
		NoEditTokens: true,
	})
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read maps dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".json")
		entry, err := store.Get(base)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if entry.Config.ID != base {
			return nil, fmt.Errorf("%s: map id %q does not match file name %q", e.Name(), entry.Config.ID, base)
		}
	}
	return &Manager{dir: dir, store: store}, nil
}

// List returns copies of all maps sorted by id.
func (m *Manager) List() []*engine.Map {
	entries, err := m.store.List()
	if err != nil {
		return nil
	}
	out := make([]*engine.Map, 0, len(entries))
	for _, e := range entries {
		mp, err := engine.NewMap(e.Config)
		if err != nil {
			continue
		}
		out = append(out, mp.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a copy of the map or a NOT_FOUND *service.Error.
func (m *Manager) Get(id string) (*engine.Map, error) {
	entry, err := m.store.Get(id)
	if err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
		}
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q: %v", id, err)
	}
	mp, err := engine.NewMap(entry.Config)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	return mp.Clone(), nil
}

// Save validates cfg, writes it atomically and stores it, replacing any
// existing map with the same id. The transport has already checked admin:
// mazes has one directory, so writes use the builtin path.
func (m *Manager) Save(cfg engine.MapConfig) (*engine.Map, error) {
	return m.save(cfg, false)
}

// Create is Save that fails with INVALID_ARGUMENT if the id already exists.
func (m *Manager) Create(cfg engine.MapConfig) (*engine.Map, error) {
	return m.save(cfg, true)
}

func (m *Manager) save(cfg engine.MapConfig, createOnly bool) (*engine.Map, error) {
	if !engine.ValidID(cfg.ID) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q: must match ^[a-z0-9_-]{1,64}$", cfg.ID)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	exists := m.store.IsBuiltin(parsed.ID)
	if exists {
		if createOnly {
			return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", parsed.ID)
		}
		if err := m.store.SaveBuiltin(parsed.ID, parsed.Config(), true); err != nil {
			return nil, fmt.Errorf("save map: %w", err)
		}
		return parsed.Clone(), nil
	}
	if entry, err := m.store.Get(parsed.ID); err == nil && !entry.Builtin {
		if createOnly {
			return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", parsed.ID)
		}
		if err := m.store.Save(parsed.ID, parsed.Config(), false); err != nil {
			return nil, fmt.Errorf("save map: %w", err)
		}
		return parsed.Clone(), nil
	}
	if _, err := m.store.Create(parsed.ID, parsed.Config()); err != nil {
		if errors.Is(err, mapstore.ErrExists) {
			return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", parsed.ID)
		}
		return nil, fmt.Errorf("save map: %w", err)
	}
	return parsed.Clone(), nil
}

// Delete removes the map. The transport has already checked admin.
func (m *Manager) Delete(id string) error {
	if !engine.ValidID(id) {
		return service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	if m.store.IsBuiltin(id) {
		if err := m.store.DeleteBuiltin(id, true); err != nil {
			if errors.Is(err, mapstore.ErrNotFound) {
				return service.Errorf(service.CodeNotFound, "map %q not found", id)
			}
			return fmt.Errorf("delete map: %w", err)
		}
		return nil
	}
	if err := m.store.Delete(id, true); err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return service.Errorf(service.CodeNotFound, "map %q not found", id)
		}
		return fmt.Errorf("delete map: %w", err)
	}
	return nil
}
