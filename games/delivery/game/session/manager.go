package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// ErrNotFound is returned when a session id has no save and is not in memory.
// It is a sentinel so transports can map it to a 404 / not_found without
// matching on error text.
var ErrNotFound = errors.New("session not found")

// ErrCityNotFound is returned when a save names a city that no longer exists,
// or one the save's owner may no longer see. The save itself may still exist;
// it simply cannot be simulated without its city.
var ErrCityNotFound = errors.New("city not found")

// CityCatalog is the city source a manager reads. At product time it is the
// delivery config.Manager (builtin files plus PostgreSQL rows); tests can pass
// a small in-memory implementation.
type CityCatalog interface {
	// DefaultID is the city a caller that names none gets.
	DefaultID() string
	// Config returns a city, hiding another player's draft as not-found.
	// viewerID is the account the city is being read for ("" = anonymous).
	Config(id, viewerID string) (*engine.CityConfig, error)
}

// Manager owns the live sessions and their durable store.
type Manager struct {
	catalog CityCatalog
	store   sessionstore.Store

	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewManager creates a manager backed by catalog and the shared PostgreSQL
// session store. Each session snapshots the city it selects; the catalogue is
// only consulted at creation and when rehydrating a save.
func NewManager(catalog CityCatalog, pool *pgxpool.Pool) (*Manager, error) {
	if catalog == nil {
		return nil, fmt.Errorf("city catalog is required")
	}
	store, err := sessionstore.Open(sessionstore.Options{Pool: pool})
	if err != nil {
		return nil, err
	}
	return &Manager{
		catalog:  catalog,
		store:    store,
		sessions: map[string]*Session{},
	}, nil
}

// Create starts a new session with the given id (or a random one when empty),
// an optional seed, the signed-in creator ("" = guest), and a city id ("" =
// catalogue default). It returns the session plus the secret owner token that
// proves the caller created it. Only the token's hash is stored.
//
// A client-supplied session id is accepted when ValidID; otherwise one is
// generated. The selected city is snapshotted into the session: editing or
// deleting it afterwards cannot change a session that already exists.
func (m *Manager) Create(id string, seed uint64, accountID, cityID string) (*Session, string, error) {
	if id == "" {
		var err error
		id, err = newSessionID()
		if err != nil {
			return nil, "", err
		}
	}
	if !ValidID(id) {
		return nil, "", fmt.Errorf("invalid session id %q", id)
	}
	if _, exists := m.Get(id); exists {
		return nil, "", fmt.Errorf("session %q already exists", id)
	}
	// A save already stored under this id must not be adopted by a new creator:
	// its owner credentials live in that payload. A corrupt payload also blocks
	// the id rather than being silently overwritten.
	if _, _, err := m.store.Load(id); err == nil {
		return nil, "", fmt.Errorf("session %q already exists", id)
	} else if !errors.Is(err, sessionstore.ErrNotFound) {
		return nil, "", fmt.Errorf("session %q already exists", id)
	}

	accountID = sanitizeAccountID(accountID)
	if cityID == "" {
		cityID = m.catalog.DefaultID()
	}
	cfg, city, err := m.cityForCreate(cityID, accountID)
	if err != nil {
		return nil, "", err
	}

	token, hash, err := newOwnerToken()
	if err != nil {
		return nil, "", err
	}
	s, err := newWithStore(id, city, cfg, cityID, seed, m.store, accountID, hash)
	if err != nil {
		return nil, "", err
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	s.Start()
	return s, token, nil
}

// cityForCreate resolves and builds a city for a new session. The creator is
// the viewer, so their own drafts are playable; another player's draft looks
// exactly like a missing city.
func (m *Manager) cityForCreate(cityID, accountID string) (*engine.CityConfig, *engine.City, error) {
	cfg, err := m.catalog.Config(cityID, accountID)
	if err != nil {
		if errors.Is(err, cityconfig.ErrNotFound) {
			return nil, nil, fmt.Errorf("city %q: %w", cityID, ErrCityNotFound)
		}
		return nil, nil, fmt.Errorf("city %q: %w", cityID, err)
	}
	city, err := engine.CityFromConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("city %q: %w", cityID, err)
	}
	return cfg, city, nil
}

// Claim attaches a session to accountID when token proves ownership. A session
// already owned by this account reports ALREADY_YOURS; one owned by another
// account, unknown, or with a wrong token reports REJECTED. Legacy sessions
// (no stored hash) can never be claimed.
func (m *Manager) Claim(accountID, id, token string) (string, error) {
	if accountID == "" {
		return ClaimRejected, fmt.Errorf("claim needs a signed-in account")
	}
	s, err := m.GetOrLoad(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) || !ValidID(id) {
			return ClaimRejected, nil
		}
		return ClaimRejected, err
	}
	status := s.claim(accountID, token)
	if status == ClaimAttached {
		if err := s.Save(); err != nil {
			return status, err
		}
	}
	return status, nil
}

// Get returns only a session already resident in memory. It does not touch the
// store, so a false result means "not loaded", not "does not exist": request
// paths must use GetOrLoad, and list views get the same distinction from
// Summary.Loaded.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok {
		return s, true
	}
	return nil, false
}

// GetOrLoad returns a session, rehydrating it from the store when necessary.
// This is what the HTTP layer uses, so a server restart does not lose saves.
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

// load rebuilds a session from its stored payload.
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

	cityID, cfg, err := m.cityForLoad(p)
	if err != nil {
		return nil, err
	}
	city, err := engine.CityFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("session %s city %q: %w", id, cityID, err)
	}
	s, err := newWithStore(id, city, cfg, cityID, p.World.Seed, m.store,
		sanitizeAccountID(p.AccountID), sanitizeHash(p.OwnerHash))
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

// cityForLoad resolves which city a save was created on and returns its
// current config.
//
// A current payload stores city_id and a missing/hidden city is a hard error:
// silently swapping in the default would replay the save's deltas against
// different geography. A legacy payload stores only city_file (the old basename
// such as "city.json"); its base is tried as an id first, then the default,
// which is the documented compatibility path. The save's owner is the viewer,
// so their own draft remains loadable after an edit puts it back in review.
func (m *Manager) cityForLoad(p persisted) (string, *engine.CityConfig, error) {
	viewer := sanitizeAccountID(p.AccountID)
	if p.CityID != "" {
		cfg, err := m.catalog.Config(p.CityID, viewer)
		if err != nil {
			return "", nil, cityLoadError(p.CityID, err)
		}
		return p.CityID, cfg, nil
	}
	if base := legacyCityFileBase(p.CityFile); base != "" && cityconfig.ValidID(base) {
		cfg, err := m.catalog.Config(base, viewer)
		if err == nil {
			return base, cfg, nil
		}
		if !errors.Is(err, cityconfig.ErrNotFound) {
			return "", nil, cityLoadError(base, err)
		}
	}
	id := m.catalog.DefaultID()
	cfg, err := m.catalog.Config(id, viewer)
	if err != nil {
		return "", nil, cityLoadError(id, err)
	}
	return id, cfg, nil
}

func cityLoadError(id string, err error) error {
	if errors.Is(err, cityconfig.ErrNotFound) {
		return fmt.Errorf("city %q: %w", id, ErrCityNotFound)
	}
	return fmt.Errorf("city %q: %w", id, err)
}

// legacyCityFileBase turns a pre-catalogue payload's city_file ("maps/city.json")
// into the id it maps to ("city"). Empty means the payload did not record a
// usable file and the default applies.
func legacyCityFileBase(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(file), ".json")
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// resolvedCityID is the id a save reports in summaries. It never consults the
// catalogue: listing must keep working when a referenced city was deleted.
func (m *Manager) resolvedCityID(p persisted) string {
	if p.CityID != "" {
		return p.CityID
	}
	if base := legacyCityFileBase(p.CityFile); base != "" && cityconfig.ValidID(base) {
		return base
	}
	return m.catalog.DefaultID()
}

// List returns every session in the store plus any live ones, newest first.
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

// summaryFromDisk reads just enough of a stored payload to describe it without
// loading the whole world into memory.
func (m *Manager) summaryFromDisk(id string) (Summary, error) {
	_, blob, err := m.store.Load(id)
	if err != nil {
		return Summary{}, err
	}
	var p persisted
	if err := json.Unmarshal(blob, &p); err != nil {
		return Summary{}, err
	}
	sum := Summary{ID: id, CreatedAt: p.CreatedAt, Speed: p.Speed, Loaded: false,
		AccountID: sanitizeAccountID(p.AccountID), CityID: m.resolvedCityID(p)}
	if p.World.Ledger != nil {
		sum.Money = p.World.Ledger.Money
		sum.Reputation = p.World.Ledger.Reputation
		sum.Deliveries = p.World.Ledger.Deliveries
	}
	sum.Tick = p.World.Tick
	return sum, nil
}

// DefaultCityID is the catalogue's default city id.
func (m *Manager) DefaultCityID() string { return m.catalog.DefaultID() }

// DefaultCity returns a fresh City for the default id. The catalogue decodes a
// new config per call, so the caller owns the returned city and may reveal its
// districts for a preview without touching any session.
func (m *Manager) DefaultCity() (*engine.City, error) {
	cfg, err := m.catalog.Config(m.catalog.DefaultID(), "")
	if err != nil {
		return nil, err
	}
	return engine.CityFromConfig(cfg)
}

// Evict stops a session and drops it from memory, leaving its stored payload.
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

// Delete stops a session, removes it from memory and deletes its stored
// payload.
//
// The id is validated first: ids double as primary keys, so an unvalidated
// id like "../other" would be rejected by the store anyway, but every entry
// point validates rather than relying on that.
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

// SaveAll persists every session currently in memory, returning the first
// error. Sessions that exist only in the store are not re-saved.
func (m *Manager) SaveAll() error {
	m.mu.RLock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.RUnlock()

	var firstErr error
	for _, s := range list {
		if err := s.Save(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("save %s: %w", s.ID, err)
		}
	}
	return firstErr
}

// Close flushes every in-memory session to the store, then stops its ticker and
// drops it from memory. It still closes the sessions when a save fails, and
// returns the first save error afterwards so a failed flush cannot leave
// goroutines running.
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
// Loaded is true only for a session currently in memory. A stored row is a
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
	// CityID is the catalogue id the session snapshotted at creation. It is
	// informational here; the session already owns its city.
	CityID string `json:"city_id,omitempty"`
	// AccountID is the owning account id ("" = guest). It never leaves the
	// server: the GraphQL layer turns it into a display name and a `mine` flag.
	AccountID string `json:"-"`
}
