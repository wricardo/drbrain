// Package config loads, lists and saves map files (maps/*.json).
package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/wricardo/drbrain-motor-programming/game/engine"
	"github.com/wricardo/drbrain-motor-programming/game/service"
)

// Options configures a Manager.
type Options struct {
	// Dir holds the shipped maps. When UserDir is set these are read-only
	// through the API (only an admin may change them).
	Dir string
	// UserDir holds maps created through the API. Empty means Dir is
	// writable and nothing is read-only.
	UserDir string
	// MaxUserMaps caps how many maps UserDir may hold (0 = no cap).
	MaxUserMaps int
}

// Manager is a file-backed service.MapStore. All maps are held in memory and
// guarded by an RWMutex; callers always receive deep copies.
type Manager struct {
	opts    Options
	mu      sync.RWMutex
	maps    map[string]*engine.Map
	builtin map[string]bool // ids loaded from opts.Dir
}

var _ service.MapStore = (*Manager)(nil)

// NewManager loads every *.json file in opts.Dir (and opts.UserDir, creating
// it if missing). An invalid shipped map, or one whose id differs from its
// file name, is a fatal error naming the file; an invalid user map is logged
// and skipped, so one bad file cannot take the server down.
//
// A user map whose id matches a shipped map shadows it.
func NewManager(opts Options) (*Manager, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("create maps manager: Dir is required")
	}
	m := &Manager{opts: opts, maps: make(map[string]*engine.Map), builtin: make(map[string]bool)}
	if err := m.load(opts.Dir, true); err != nil {
		return nil, err
	}
	if opts.UserDir != "" {
		if err := m.load(opts.UserDir, false); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// load reads every *.json in dir into m. strict reports the first bad file as
// an error and marks the maps read-only (shipped maps); otherwise a bad file is
// logged and skipped, so one corrupt player file cannot take the server down.
func (m *Manager) load(dir string, strict bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create maps dir: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read maps dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		parsed, err := readMap(path)
		if err != nil {
			if strict {
				return err
			}
			log.Printf("config: skipping user map %s: %v", path, err)
			continue
		}
		if _, clash := m.maps[parsed.ID]; clash && !strict {
			log.Printf("config: skipping user map %s: id %q is taken by a shipped map", path, parsed.ID)
			continue
		}
		m.maps[parsed.ID] = parsed
		// Shipped maps are read-only through the API (admin key only).
		if strict {
			m.builtin[parsed.ID] = true
		}
	}
	return nil
}

// readMap parses and validates one map file.
func readMap(path string) (*engine.Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var cfg engine.MapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if want := strings.TrimSuffix(filepath.Base(path), ".json"); cfg.ID != want {
		return nil, fmt.Errorf("%s: map id %q does not match file name %q", path, cfg.ID, want)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return parsed, nil
}

// List returns copies of all maps sorted by id.
func (m *Manager) List() []*engine.Map {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*engine.Map, 0, len(m.maps))
	for _, mp := range m.maps {
		out = append(out, mp.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a copy of the map or a NOT_FOUND *service.Error.
func (m *Manager) Get(id string) (*engine.Map, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mp, ok := m.maps[id]
	if !ok {
		return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	return mp.Clone(), nil
}

// Builtin reports whether id is a shipped map. The transport gates writes to
// these on the admin key; user-created maps are open to everyone.
func (m *Manager) Builtin(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.builtin[id]
}

// Save validates cfg, writes it atomically and stores it, replacing any
// existing map with the same id.
func (m *Manager) Save(cfg engine.MapConfig) (*engine.Map, error) {
	return m.store(cfg, false)
}

// Create is Save that fails with INVALID_ARGUMENT if the id already exists.
// The existence check, the cap check and the write happen under one lock.
func (m *Manager) Create(cfg engine.MapConfig) (*engine.Map, error) {
	return m.store(cfg, true)
}

func (m *Manager) store(cfg engine.MapConfig, createOnly bool) (*engine.Map, error) {
	if !engine.ValidID(cfg.ID) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q: must match ^[a-z0-9_-]{1,64}$", cfg.ID)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	data, err := json.MarshalIndent(parsed.Config(), "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.maps[parsed.ID]; createOnly && exists {
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", parsed.ID)
	}
	// Editing a shipped map keeps it shipped; everything new goes to UserDir.
	dir := m.opts.UserDir
	if dir == "" || m.builtin[parsed.ID] {
		dir = m.opts.Dir
	}
	if createOnly && dir == m.opts.UserDir && m.opts.MaxUserMaps > 0 && m.userCount() >= m.opts.MaxUserMaps {
		return nil, service.Errorf(service.CodeLimit, "%d player maps already exist; delete one before creating another", m.opts.MaxUserMaps)
	}
	if err := writeAtomic(dir, parsed.ID+".json", data); err != nil {
		return nil, fmt.Errorf("save map: %w", err)
	}
	m.maps[parsed.ID] = parsed
	if dir == m.opts.Dir && m.opts.UserDir != "" {
		m.builtin[parsed.ID] = true
	}
	return parsed.Clone(), nil
}

// userCount reports how many maps live in UserDir. The lock must be held.
func (m *Manager) userCount() int {
	return len(m.maps) - len(m.builtin)
}

// Delete removes the map and its file. NOT_FOUND if absent.
func (m *Manager) Delete(id string) error {
	if !engine.ValidID(id) {
		return service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.maps[id]; !ok {
		return service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	dir := m.opts.Dir
	if m.opts.UserDir != "" && !m.builtin[id] {
		dir = m.opts.UserDir
	}
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete map: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("delete map: %w", err)
	}
	delete(m.maps, id)
	delete(m.builtin, id)
	return nil
}

// writeAtomic writes data to dir/name via temp file (fsynced) + rename, then
// fsyncs dir so the rename is durable.
func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

// syncDir fsyncs a directory so renames/removals in it are durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
