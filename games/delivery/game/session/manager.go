package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// ErrNotFound is returned when a session id has no save and is not in memory.
// It is a sentinel so transports can map it to a 404 / not_found without
// matching on error text.
var ErrNotFound = errors.New("session not found")

// Manager owns the live sessions and their persistence directory.
type Manager struct {
	cityFile string
	dir      string // sessions directory, for callers that inspect it
	store    sessionstore.Store

	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewManager creates a manager that reads the city from cityFile and stores
// saves in dir.
func NewManager(cityFile, dir string) (*Manager, error) {
	if _, err := os.Stat(cityFile); err != nil {
		return nil, fmt.Errorf("city file %s: %w", cityFile, err)
	}
	store, err := sessionstore.Open(sessionstore.Options{Dir: dir, Summary: summarizePayload})
	if err != nil {
		return nil, err
	}
	return &Manager{
		cityFile: cityFile,
		dir:      dir,
		store:    store,
		sessions: map[string]*Session{},
	}, nil
}

// Create starts a new session with the given id (or a generated one when
// empty) and an optional seed.
func (m *Manager) Create(id string, seed uint64) (*Session, error) {
	city, cfg, err := engine.CityFromFile(m.cityFile)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = fmt.Sprintf("s%d", time.Now().UnixNano())
	}
	if _, exists := m.Get(id); exists {
		return nil, fmt.Errorf("session %q already exists", id)
	}

	s, err := newWithStore(id, city, cfg, seed, m.store)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	s.Start()
	return s, nil
}

// Get returns a live session, loading it from disk if it is not in memory.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok {
		return s, true
	}
	return nil, false
}

// GetOrLoad returns a session, rehydrating it from disk when necessary. This is
// what the HTTP layer uses, so a server restart does not lose saves.
func (m *Manager) GetOrLoad(id string) (*Session, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	if s, ok := m.Get(id); ok {
		return s, nil
	}

	s, err := m.load(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	// Another goroutine may have loaded it concurrently; prefer theirs.
	if existing, ok := m.sessions[id]; ok {
		m.mu.Unlock()
		s.Close()
		return existing, nil
	}
	m.sessions[id] = s
	m.mu.Unlock()

	s.Start()
	return s, nil
}

// load rebuilds a session from its save file.
func (m *Manager) load(id string) (*Session, error) {
	_, blob, err := m.store.Load(id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, fmt.Errorf("session %q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("read save: %w", err)
	}
	var p persisted
	if err := json.Unmarshal(blob, &p); err != nil {
		return nil, fmt.Errorf("parse save %s: %w", id, err)
	}

	city, cfg, err := engine.CityFromFile(m.cityFile)
	if err != nil {
		return nil, err
	}
	s, err := newWithStore(id, city, cfg, p.World.Seed, m.store)
	if err != nil {
		return nil, err
	}
	s.CreatedAt = p.CreatedAt
	if err := s.world.ImportState(p.World); err != nil {
		return nil, fmt.Errorf("restore session %s: %w", id, err)
	}

	// A load ALWAYS starts paused, whatever speed the save recorded.
	//
	// This used to restore the saved speed and start ticking immediately, which
	// meant opening a save at 2x advanced the world while the player was still
	// reading it. That was merely surprising before; with fleet upkeep it is a
	// money drain on a session nobody is playing. Loading is an act of opening a
	// session, not an act of resuming one, and the client opts in to the clock
	// explicitly with SetSpeed.
	//
	// p.Speed is still READ rather than discarded: Summary for an on-disk row
	// reports it, so a save picker can show the pace the save was left at even
	// though opening it will pause.
	if err := s.SetSpeed(SpeedPaused); err != nil {
		return nil, err
	}
	return s, nil
}

// List returns every session known on disk plus any live ones, newest first.
//
// It never returns nil: an empty list must marshal as [] rather than null, or
// every client has to special-case "no sessions" as a null check.
func (m *Manager) List() []Summary {
	m.mu.RLock()
	live := make(map[string]*Session, len(m.sessions))
	for id, s := range m.sessions {
		live[id] = s
	}
	m.mu.RUnlock()

	seen := map[string]bool{}
	out := []Summary{}

	for id, s := range live {
		seen[id] = true
		out = append(out, s.Summary())
	}

	metas, err := m.store.List()
	if err == nil {
		for _, meta := range metas {
			if seen[meta.ID] || !ValidID(meta.ID) {
				continue
			}
			if sum, err := m.summaryFromDisk(meta.ID); err == nil {
				out = append(out, sum)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// summaryFromDisk reads just enough of a save to describe it without loading
// the whole world into memory.
func (m *Manager) summaryFromDisk(id string) (Summary, error) {
	_, blob, err := m.store.Load(id)
	if err != nil {
		return Summary{}, err
	}
	var p persisted
	if err := json.Unmarshal(blob, &p); err != nil {
		return Summary{}, err
	}
	sum := Summary{ID: id, CreatedAt: p.CreatedAt, Speed: p.Speed, Loaded: false}
	if p.World.Ledger != nil {
		sum.Money = p.World.Ledger.Money
		sum.Reputation = p.World.Ledger.Reputation
		sum.Deliveries = p.World.Ledger.Deliveries
	}
	sum.Tick = p.World.Tick
	return sum, nil
}

// CityFileName returns the basename of the configured city file, which is what
// a save records so content fixes apply to existing saves.
func (m *Manager) CityFileName() string { return filepath.Base(m.cityFile) }

// CityFile returns the full path of the configured city file.
func (m *Manager) CityFile() string { return m.cityFile }

// Evict stops a session and drops it from memory, leaving its save on disk.
// A later GetOrLoad rehydrates it.
func (m *Manager) Evict(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok {
		s.Close()
	}
}

// Delete stops a session, removes it from memory and deletes its save.
//
// The id is validated first: filepath.Join cleans ".." segments, so an
// unvalidated id like "../other" would delete a file outside the sessions
// directory. Every other entry point validates; this one used to be the gap.
func (m *Manager) Delete(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid session id %q", id)
	}
	m.Evict(id)
	if err := m.store.Delete(id); err != nil && !errors.Is(err, sessionstore.ErrNotFound) {
		return fmt.Errorf("delete save: %w", err)
	}
	return nil
}

// SaveAll persists every live session.
func (m *Manager) SaveAll() error {
	m.mu.RLock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.RUnlock()

	var firstErr error
	for _, s := range list {
		if err := s.Save(filepath.Base(m.cityFile)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("save %s: %w", s.ID, err)
		}
	}
	return firstErr
}

// Close stops every session and flushes saves.
func (m *Manager) Close() error {
	err := m.SaveAll()
	m.mu.Lock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.sessions = map[string]*Session{}
	m.mu.Unlock()

	for _, s := range list {
		s.Close()
	}
	return err
}

// Summary is the list-view of a session: what a save picker shows.
//
// Loaded is true only for a session currently in memory. An on-disk row is a
// save that has not been opened this process, so its Speed is the value it was
// saved with, not a running clock.
type Summary struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Tick       int64     `json:"tick"`
	Speed      int       `json:"speed"`
	Money      int       `json:"money"`
	Reputation float64   `json:"reputation"`
	Deliveries int       `json:"deliveries"`
	Loaded     bool      `json:"loaded"`
}
