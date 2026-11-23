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

// ErrExists reports a create colliding with an existing id (live or stored).
// It is a client conflict, not an internal failure.
var ErrExists = errors.New("session exists")

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
	history *historyWriter

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
	store, err := sessionstore.Open(sessionstore.Options{Pool: pool, Game: "delivery"})
	if err != nil {
		return nil, err
	}
	return &Manager{
		catalog:  catalog,
		store:    store,
		history:  newHistoryWriter(pool),
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
		return nil, "", fmt.Errorf("session %q already exists: %w", id, ErrExists)
	}
	// A save already stored under this id must not be adopted by a new creator:
	// its owner credentials live in that payload. A corrupt payload also blocks
	// the id rather than being silently overwritten.
	if _, _, err := m.store.Load(id); err == nil {
		return nil, "", fmt.Errorf("session %q already exists: %w", id, ErrExists)
	} else if !errors.Is(err, sessionstore.ErrNotFound) {
		return nil, "", fmt.Errorf("session %q already exists: %w", id, ErrExists)
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
	s.history = m.history
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
	s.history = m.history
	s.CreatedAt = p.CreatedAt
	s.displayName = p.DisplayName
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

// LiveSummaries describes only the sessions held in memory and never touches
// the store. It exists for metrics scrapes: List is right for a save picker but
// would read every stored payload, turning one scrape into a PostgreSQL scan.
func (m *Manager) LiveSummaries() []Summary {
	m.mu.RLock()
	live := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		live = append(live, s)
	}
	m.mu.RUnlock()

	out := make([]Summary, 0, len(live))
	for _, s := range live {
		out = append(out, s.Summary())
	}
	return out
}

// SessionSort selects the order List returns rows in.
type SessionSort int

const (
	// SortCreatedAt is the default: newest save first.
	SortCreatedAt SessionSort = iota
	// SortTick orders by world tick, highest first.
	SortTick
	// SortMoney orders by spendable balance, highest first.
	SortMoney
)

// ListOptions narrows List. The zero value returns every row, newest first —
// which is exactly the pre-options behaviour, so callers that want the whole
// catalogue keep it by passing the zero value.
type ListOptions struct {
	Sort SessionSort
	// Limit caps the rows returned. 0 means no cap.
	Limit int
	// Before keeps saves created strictly before this time. Zero means no
	// cursor.
	Before time.Time
	// AccountID keeps the saves owned by one account. "" means every owner,
	// including guests.
	AccountID string
	// Visible, when set, drops rows the caller may not read (another player's
	// draft city). It runs before Limit truncates, so the limit counts rows the
	// caller can actually see; a false keeps the row hidden. It must not do
	// I/O under a lock.
	Visible func(Summary) bool
	// Cursor pages strictly after an encoded position in opts.Sort's order,
	// with the id as the tiebreak, so rows that share the sort value are not
	// skipped or repeated. The transport decodes its opaque cursor into this.
	Cursor *ListCursor
}

// ListCursor is one row's position in the requested sort order. Value is the
// sort key (created-at unix nanos, tick, or money); ID breaks ties, matching
// sortSummaries' ascending-id order.
type ListCursor struct {
	Value int64
	ID    string
}

// List returns the sessions in the store plus any live ones, ordered by
// opts.Sort.
//
// A stored row is described from its sessionstore.Meta when the game published
// counters there, which is what every save written since counters existed does.
// Saves from before that have no counters and are decoded from their payload —
// but only when the row survives filtering and truncation, so a bounded list
// does bounded work. Ordering by tick or money needs counters for every
// candidate, so that sort decodes legacy rows before sorting.
//
// It never returns nil: an empty list must marshal as [] rather than null, or
// every client has to special-case "no sessions" as a null check.
func (m *Manager) List(opts ListOptions) []Summary {
	m.mu.RLock()
	live := make(map[string]*Session, len(m.sessions))
	for id, s := range m.sessions {
		live[id] = s
	}
	m.mu.RUnlock()

	seen := map[string]bool{}
	out := make([]Summary, 0, len(live))
	for id, s := range live {
		seen[id] = true
		sum := s.Summary()
		if opts.AccountID != "" && sum.AccountID != opts.AccountID {
			continue
		}
		if !opts.Before.IsZero() && !sum.CreatedAt.Before(opts.Before) {
			continue
		}
		if opts.Visible != nil && !opts.Visible(sum) {
			continue
		}
		out = append(out, sum)
	}

	// legacy holds the ids of stored rows whose counters are not published, so
	// their payload still has to be decoded. It is keyed by id, not row index:
	// sorting moves rows, and a stale index would overwrite another session.
	legacy := map[string]bool{}
	if metas, err := m.store.List(); err == nil {
		for _, meta := range metas {
			if seen[meta.ID] || !ValidID(meta.ID) {
				continue
			}
			if opts.AccountID != "" && meta.AccountID != opts.AccountID {
				continue
			}
			if !opts.Before.IsZero() && !meta.CreatedAt.Before(opts.Before) {
				continue
			}
			sum := m.summaryFromMeta(meta, false)
			if opts.Visible != nil && !opts.Visible(sum) {
				continue
			}
			out = append(out, sum)
			if meta.Counters == nil {
				legacy[meta.ID] = true
			}
		}
	}

	// Tick and money ordering needs every candidate's numbers before it can
	// sort, so legacy payloads are decoded up front. CreatedAt ordering needs
	// none, so truncation happens first and only the surviving rows are
	// resolved: a bounded list with the default sort decodes nothing.
	if opts.Sort != SortCreatedAt {
		m.resolveLegacy(out, legacy)
	}
	m.sortSummaries(out, opts.Sort)
	if opts.Cursor != nil {
		out = rowsAfterCursor(out, opts.Sort, *opts.Cursor)
	}
	out = truncateSummaries(out, opts.Limit)
	if opts.Sort == SortCreatedAt {
		m.resolveLegacy(out, legacy)
	}
	return out
}

// rowsAfterCursor keeps the rows strictly after cur in the requested order.
// Ordering is key descending, id ascending, so the next page is a smaller key
// or an equal key with a larger id.
func rowsAfterCursor(rows []Summary, sort SessionSort, cur ListCursor) []Summary {
	key := func(s Summary) int64 {
		switch sort {
		case SortTick:
			return s.Tick
		case SortMoney:
			return int64(s.Money)
		default:
			return s.CreatedAt.UnixNano()
		}
	}
	out := rows[:0]
	for _, s := range rows {
		k := key(s)
		if k < cur.Value || (k == cur.Value && s.ID > cur.ID) {
			out = append(out, s)
		}
	}
	return out
}

// resolveLegacy decodes the payload of rows without published counters, in
// place. Rows are matched by id because sorting may have moved them since the
// set was built. Rows that fail to decode keep the meta-only description (zero
// counters) rather than vanishing from the list.
func (m *Manager) resolveLegacy(rows []Summary, legacy map[string]bool) {
	for i := range rows {
		id := rows[i].ID
		if !legacy[id] {
			continue
		}
		if sum, err := m.summaryFromDisk(id); err == nil {
			sum.DisplayName = rows[i].DisplayName
			rows[i] = sum
		}
	}
}

// sortSummaries orders rows by opts.Sort, newest/highest first, with the id as
// a stable tiebreak so two equal rows never swap between calls.
func (m *Manager) sortSummaries(rows []Summary, key SessionSort) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var less, equal bool
		switch key {
		case SortTick:
			less, equal = a.Tick > b.Tick, a.Tick == b.Tick
		case SortMoney:
			less, equal = a.Money > b.Money, a.Money == b.Money
		default:
			less = a.CreatedAt.After(b.CreatedAt)
			equal = a.CreatedAt.Equal(b.CreatedAt)
		}
		if equal {
			return a.ID < b.ID
		}
		return less
	})
}

// truncateSummaries applies a limit, preserving the no-cap behaviour for 0.
func truncateSummaries(rows []Summary, limit int) []Summary {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

// summaryFromMeta describes a stored save without decoding its payload when the
// game published counters. Speed is unknown for a row that is not loaded, which
// is why it reads as paused: a stored save has no running clock.
func (m *Manager) summaryFromMeta(meta sessionstore.Meta, loaded bool) Summary {
	sum := Summary{
		ID:          meta.ID,
		DisplayName: meta.DisplayName,
		CreatedAt:   meta.CreatedAt,
		Loaded:      loaded,
		CityID:      meta.MapID,
		AccountID:   sanitizeAccountID(meta.AccountID),
		Speed:       SpeedPaused,
	}
	if meta.Counters != nil {
		sum.Tick = meta.Counters.Tick
		sum.Money = meta.Counters.Money
		sum.Reputation = meta.Counters.Reputation
		sum.Deliveries = meta.Counters.Deliveries
	}
	return sum
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
		AccountID: sanitizeAccountID(p.AccountID), CityID: m.resolvedCityID(p),
		DisplayName: p.DisplayName}
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

// DeleteByCity destroys every delivery session created on cityID, live or
// stored, and returns how many it removed.
//
// Deleting a city must not leave sessions behind: their city gate cannot be
// evaluated once the catalogue row is gone, and a private draft's sessions
// would silently become world-readable. The caller runs this before or after
// the city delete; a missing session is not an error.
func (m *Manager) DeleteByCity(cityID string) (int, error) {
	if cityID == "" {
		return 0, nil
	}
	ids := make(map[string]bool)
	m.mu.RLock()
	for id, s := range m.sessions {
		if s.CityID() == cityID {
			ids[id] = true
		}
	}
	m.mu.RUnlock()
	if metas, err := m.store.List(); err == nil {
		for _, meta := range metas {
			if meta.MapID == cityID {
				ids[meta.ID] = true
			}
		}
	} else {
		return 0, fmt.Errorf("list sessions for city %s: %w", cityID, err)
	}

	deleted := 0
	var errs []error
	for id := range ids {
		if err := m.Delete(id); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, fmt.Errorf("delete session %s: %w", id, err))
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
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
	// DisplayName is the player's name for the save; "" means unnamed and the
	// client shows the city name.
	DisplayName string `json:"display_name,omitempty"`
	// CityID is the catalogue id the session snapshotted at creation. It is
	// informational here; the session already owns its city.
	CityID string `json:"city_id,omitempty"`
	// AccountID is the owning account id ("" = guest). It never leaves the
	// server: the GraphQL layer turns it into a display name and a `mine` flag.
	AccountID string `json:"-"`
}
