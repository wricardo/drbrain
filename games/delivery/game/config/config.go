// Package config owns Delivery's city catalogue: the builtin city JSON files
// on disk and the community cities in PostgreSQL.
//
// The catalogue deliberately has no custom city directory. Builtins are
// shipped files; everything a player creates lives in platform_maps with
// game = "delivery". With no PostgreSQL pool the manager is builtin-only, which
// is what the engine and game tests use; production always mounts with the
// shared pool.
//
// A city's identity is its file base (or platform_maps.id): lowercase
// [a-z0-9_-]{1,64}. CityConfig.Name remains display text only.
//
// Builtins are normally deploy assets, but an admin may rewrite or delete one
// through SaveOwned/DeleteOwned. Those paths refresh the in-memory copy from
// disk so Get/ListFor see the change immediately; the configured default city
// cannot be deleted.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/mapstore"
)

// Errors are the platform mapstore vocabulary plus a catalogue-specific
// not-found so callers can hide a draft behind the same error a missing city
// produces. errors.Is works with both this package's aliases and mapstore's.
var (
	// ErrNotFound means the city does not exist, or is another player's draft.
	ErrNotFound = errors.New("city catalog: city not found")
	// ErrExists means the id is already a builtin or custom city.
	ErrExists = mapstore.ErrExists
	// ErrInvalid means the city failed structural or publish validation.
	ErrInvalid = mapstore.ErrInvalid
	// ErrReadOnly means a builtin city write was attempted without admin.
	ErrReadOnly = mapstore.ErrReadOnly
	// ErrDefaultCity means the operation would remove the configured default
	// city, which would leave the catalogue unable to serve an implicit
	// city selection.
	ErrDefaultCity = errors.New("city catalog: default city")
	// ErrForbidden means the actor does not own the city.
	ErrForbidden = mapstore.ErrForbidden
	// ErrLimit means the game already has the maximum number of custom cities.
	ErrLimit = mapstore.ErrLimit
	// ErrNoDatabase means a custom-city operation was attempted without a pool.
	ErrNoDatabase = mapstore.ErrNoDatabase
)

// ValidID is the city id shape: a store key and a URL segment.
var validID = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// ValidID reports whether id may name a city: 1-64 characters from [a-z0-9_-].
// Builtin file names, catalogue ids and platform_maps ids share this lowercase
// shape; uppercase is rejected. Session ids are a separate, wider shape.
func ValidID(id string) bool { return validID.MatchString(id) }

// Options configures a catalogue.
type Options struct {
	// BuiltinDir holds the shipped <id>.json city files. Required.
	BuiltinDir string
	// DefaultCityID is the id a request that names no city gets. It must be a
	// builtin. Required.
	DefaultCityID string
	// Pool enables community cities in platform_maps. Nil makes the catalogue
	// builtin-only and makes every custom-city operation return ErrNoDatabase.
	Pool *pgxpool.Pool
	// MaxUserCities caps custom rows for Delivery (all owners). Zero means no
	// cap.
	MaxUserCities int
}

// Entry is one catalogue row.
type Entry struct {
	// ID is the catalogue key: the builtin file base or the platform_maps id.
	ID string
	// Name is display text.
	Name string
	// Config is the decoded city content. Each builtin read decodes fresh
	// bytes, so a caller may mutate its copy without touching another session.
	Config *engine.CityConfig
	// Builtin is true for a shipped file and false for a community row.
	Builtin bool
	// OwnerID is the custom city's owner ("" for builtins and legacy rows).
	OwnerID string
	// Status is DRAFT or PUBLISHED; builtins are always PUBLISHED.
	Status mapstore.Status
}

// Actor is who is changing a city. Admin bypasses community-city ownership;
// shipped builtins stay read-only regardless.
type Actor struct {
	AccountID string
	Admin     bool
}

// Manager is the catalogue. It is safe for concurrent use: builtin bytes are
// decoded per request (so no session can mutate another's config), and an
// admin builtin write refreshes the cache under mu. mapstore owns the custom
// row locks.
type Manager struct {
	mu         sync.RWMutex
	builtinDir string
	defaultID  string
	builtins   map[string][]byte
	store      *mapstore.Store[*engine.CityConfig]
}

// Open validates every builtin city and, with a pool, opens the PostgreSQL
// custom-city partition.
//
// Builtin validation is the full startup check (NewCity + City.Validate): a
// broken shipped map is fatal and names its file. Custom rows are not fatal:
// Open lists them once so a corrupt row is logged and skipped by mapstore, then
// continues.
func Open(opts Options) (*Manager, error) {
	dir := strings.TrimSpace(opts.BuiltinDir)
	if dir == "" {
		return nil, fmt.Errorf("city catalog: builtin dir is empty")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("city catalog: builtin dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("city catalog: builtin dir %s is not a directory", dir)
	}
	if opts.MaxUserCities < 0 {
		return nil, fmt.Errorf("city catalog: max user cities must not be negative")
	}

	m := &Manager{
		builtinDir: dir,
		defaultID:  strings.TrimSpace(opts.DefaultCityID),
		builtins:   map[string][]byte{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("city catalog: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		id := strings.TrimSuffix(e.Name(), ".json")
		if !ValidID(id) {
			return nil, fmt.Errorf("city %s: id %q must match %s", path, id, validID)
		}
		if _, dup := m.builtins[id]; dup {
			return nil, fmt.Errorf("city %s: duplicate id %q", path, id)
		}
		raw, _, err := loadBuiltin(path)
		if err != nil {
			return nil, fmt.Errorf("city %s: %w", path, err)
		}
		m.builtins[id] = raw
	}
	if m.defaultID == "" {
		return nil, fmt.Errorf("city catalog: default city id is empty")
	}
	if _, ok := m.builtins[m.defaultID]; !ok {
		return nil, fmt.Errorf("city catalog: default city %q not found in %s", m.defaultID, dir)
	}

	if opts.Pool != nil {
		store, err := mapstore.Open(mapstore.Options[*engine.CityConfig]{
			BuiltinDir:      dir,
			Game:            "delivery",
			Pool:            opts.Pool,
			Decode:          engine.DecodeCityConfig,
			Encode:          encodeCityConfig,
			Validate:        engine.ValidateCityConfigStructural,
			PublishValidate: engine.ValidateCityConfig,
			MaxCustomMaps:   opts.MaxUserCities,
			IssueEditTokens: false,
		})
		if err != nil {
			return nil, err
		}
		// A corrupt custom row must be logged and skipped, never a startup
		// failure. mapstore does the logging; List (not ListFor) walks drafts
		// too, so the check covers every row, not only published ones.
		if _, err := store.List(); err != nil {
			return nil, fmt.Errorf("city catalog: list custom cities: %w", err)
		}
		m.store = store
	}
	return m, nil
}

// loadBuiltin reads, decodes and fully validates one shipped city. It is the
// startup check and the post-admin-write refresh, so both paths enforce the
// same invariant: a builtin on disk is always simulatable.
func loadBuiltin(path string) ([]byte, *engine.CityConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := engine.DecodeCityConfig(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := engine.ValidateCityConfig(cfg); err != nil {
		return nil, nil, err
	}
	return raw, cfg, nil
}

func encodeCityConfig(cfg *engine.CityConfig) ([]byte, error) {
	return json.Marshal(cfg)
}

// DefaultID is the city a request that names none selects.
func (m *Manager) DefaultID() string { return m.defaultID }

// Get returns one city. A draft owned by someone other than viewerID is hidden:
// it reports ErrNotFound exactly like a city that does not exist.
func (m *Manager) Get(id, viewerID string) (Entry, error) {
	entry, err := m.Lookup(id)
	if err != nil {
		return Entry{}, err
	}
	if entry.Builtin {
		return entry, nil
	}
	if entry.Status == mapstore.StatusDraft && (entry.OwnerID == "" || entry.OwnerID != viewerID) {
		return Entry{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return entry, nil
}

// Lookup returns one city without applying draft visibility. It is for policy
// checks that must distinguish "hidden draft" from "deleted city", such as
// deciding whether a session's city may be read by this viewer. Public callers
// should use Get.
func (m *Manager) Lookup(id string) (Entry, error) {
	base := strings.TrimSpace(id)
	if !ValidID(base) {
		return Entry{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	m.mu.RLock()
	raw, isBuiltin := m.builtins[base]
	m.mu.RUnlock()
	if isBuiltin {
		cfg, err := engine.DecodeCityConfig(raw)
		if err != nil {
			return Entry{}, fmt.Errorf("builtin city %q: %w", base, err)
		}
		return Entry{
			ID: base, Name: cfg.Name, Config: cfg, Builtin: true,
			Status: mapstore.StatusPublished,
		}, nil
	}
	if m.store == nil {
		return Entry{}, fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	row, err := m.store.Get(base)
	if err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return Entry{}, fmt.Errorf("%w: %q", ErrNotFound, base)
		}
		return Entry{}, err
	}
	if row.Builtin {
		// The file appeared after Open. mapstore has decoded it structurally;
		// serve it with the same shape as a cached builtin.
		return entryFrom(row), nil
	}
	return entryFrom(row), nil
}

// Config is Get without the catalogue metadata. Session creation and load use
// it; a hidden or missing city is ErrNotFound.
func (m *Manager) Config(id, viewerID string) (*engine.CityConfig, error) {
	e, err := m.Get(id, viewerID)
	if err != nil {
		return nil, err
	}
	return e.Config, nil
}

// ListFor returns builtins and the custom cities viewerID may see: published
// rows plus the viewer's own drafts. Custom rows never shadow a builtin id.
func (m *Manager) ListFor(viewerID string) ([]Entry, error) {
	m.mu.RLock()
	snapshot := make(map[string][]byte, len(m.builtins))
	for id, raw := range m.builtins {
		snapshot[id] = raw
	}
	m.mu.RUnlock()

	out := make([]Entry, 0, len(snapshot))
	for id, raw := range snapshot {
		cfg, err := engine.DecodeCityConfig(raw)
		if err != nil {
			return nil, fmt.Errorf("builtin city %q: %w", id, err)
		}
		out = append(out, Entry{
			ID: id, Name: cfg.Name, Config: cfg, Builtin: true,
			Status: mapstore.StatusPublished,
		})
	}
	if m.store != nil {
		rows, err := m.store.ListFor(viewerID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Builtin {
				// Builtins are decoded from the catalogue cache above; the
				// store read them again only to merge directories.
				continue
			}
			if !ValidID(row.Name) {
				// A row written outside this API (or by a future/mistaken
				// writer) with an unusable id cannot be addressed by Get, so it
				// must not appear in the catalogue either.
				continue
			}
			out = append(out, entryFrom(row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreateDraft stores a new owned DRAFT. Delivery issues no edit tokens; the
// owner is always the signed-in account. The config only has to be
// structurally valid, so an unwinnable city can be saved and worked on.
func (m *Manager) CreateDraft(id string, cfg *engine.CityConfig, ownerID string) error {
	base, err := lifecycleID(id)
	if err != nil {
		return err
	}
	if m.store == nil {
		return fmt.Errorf("create draft: %w", ErrNoDatabase)
	}
	if _, err := m.store.CreateDraft(base, cfg, ownerID); err != nil {
		return err
	}
	return nil
}

// SaveOwned overwrites a city for actor. Saving a published community city as
// its owner puts it back into draft; an admin edit keeps owner and status.
//
// Community saves use the structural validator, so an unwinnable draft can be
// stored. A builtin is a shipped asset and must stay startup-valid: its admin
// write uses the full validator, then re-reads the file so Get/ListFor and the
// default id see the new content immediately. Non-admins get ErrReadOnly.
func (m *Manager) SaveOwned(id string, cfg *engine.CityConfig, actor Actor) (Entry, error) {
	base, err := lifecycleID(id)
	if err != nil {
		return Entry{}, err
	}

	m.mu.Lock()
	if _, ok := m.builtins[base]; ok {
		entry, err := m.saveBuiltinLocked(base, cfg, actor)
		m.mu.Unlock()
		return entry, err
	}
	m.mu.Unlock()

	if m.store == nil {
		return Entry{}, fmt.Errorf("save city: %w", ErrNoDatabase)
	}
	row, err := m.store.SaveOwned(base, cfg, mapstore.Actor{AccountID: actor.AccountID, Admin: actor.Admin})
	if err != nil {
		return Entry{}, err
	}
	return entryFrom(row), nil
}

// saveBuiltinLocked rewrites one shipped city. Caller holds m.mu.
func (m *Manager) saveBuiltinLocked(base string, cfg *engine.CityConfig, actor Actor) (Entry, error) {
	if !actor.Admin {
		return Entry{}, fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if m.store == nil {
		return Entry{}, fmt.Errorf("save city: %w", ErrNoDatabase)
	}
	// Builtins must remain fully simulatable; the community structural gate is
	// deliberately not enough here.
	if err := engine.ValidateCityConfig(cfg); err != nil {
		return Entry{}, fmt.Errorf("%w: builtin %q: %v", ErrInvalid, base, err)
	}
	if _, err := m.store.SaveOwned(base, cfg, mapstore.Actor{AccountID: actor.AccountID, Admin: true}); err != nil {
		return Entry{}, err
	}
	path := filepath.Join(m.builtinDir, base+".json")
	raw, refreshed, err := loadBuiltin(path)
	if err != nil {
		return Entry{}, fmt.Errorf("city %s: %w", path, err)
	}
	m.builtins[base] = raw
	return Entry{
		ID: base, Name: refreshed.Name, Config: refreshed, Builtin: true,
		Status: mapstore.StatusPublished,
	}, nil
}

// Publish runs the full city validation (the same check startup uses) and
// moves an owned draft to PUBLISHED.
func (m *Manager) Publish(id string, actor Actor) (Entry, error) {
	base, err := lifecycleID(id)
	if err != nil {
		return Entry{}, err
	}
	if m.isBuiltin(base) {
		return Entry{}, fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if m.store == nil {
		return Entry{}, fmt.Errorf("publish city: %w", ErrNoDatabase)
	}
	row, err := m.store.Publish(base, mapstore.Actor{AccountID: actor.AccountID, Admin: actor.Admin})
	if err != nil {
		return Entry{}, err
	}
	return entryFrom(row), nil
}

// DeleteOwned removes a city for actor: a community row needs its owner or an
// admin; a builtin needs an admin. Deleting the configured default city is
// refused: every implicit city selection would break.
func (m *Manager) DeleteOwned(id string, actor Actor) error {
	base, err := lifecycleID(id)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if _, ok := m.builtins[base]; ok {
		defer m.mu.Unlock()
		if !actor.Admin {
			return fmt.Errorf("%w: %q", ErrReadOnly, base)
		}
		if base == m.defaultID {
			return fmt.Errorf("%w: %q cannot be deleted while it is the default", ErrDefaultCity, base)
		}
		if m.store == nil {
			return fmt.Errorf("delete city: %w", ErrNoDatabase)
		}
		if err := m.store.DeleteOwned(base, mapstore.Actor{AccountID: actor.AccountID, Admin: true}); err != nil {
			return err
		}
		delete(m.builtins, base)
		return nil
	}
	m.mu.Unlock()

	if m.store == nil {
		return fmt.Errorf("delete city: %w", ErrNoDatabase)
	}
	return m.store.DeleteOwned(base, mapstore.Actor{AccountID: actor.AccountID, Admin: actor.Admin})
}

func (m *Manager) isBuiltin(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.builtins[id]
	return ok
}

// OnAccountDelete is the account-deletion hook: the player's drafts go away,
// and their published cities are orphaned (kept, owner cleared) so other
// players' sessions that reference them can still load.
func (m *Manager) OnAccountDelete(ownerID string) error {
	if m.store == nil || strings.TrimSpace(ownerID) == "" {
		return nil
	}
	// Run both steps even if the first fails: a draft row is private, but
	// leaving published rows attached to a deleted account is worse.
	return errors.Join(
		m.store.DeleteDraftsByOwner(ownerID),
		m.store.OrphanPublishedByOwner(ownerID),
	)
}

func lifecycleID(id string) (string, error) {
	base := strings.TrimSpace(id)
	if !ValidID(base) {
		return "", fmt.Errorf("%w: city id %q must match %s", ErrInvalid, id, validID)
	}
	return base, nil
}

func entryFrom(row mapstore.Map[*engine.CityConfig]) Entry {
	return Entry{
		ID:      row.Name,
		Name:    row.Config.Name,
		Config:  row.Config,
		Builtin: row.Builtin,
		OwnerID: row.OwnerID,
		Status:  row.Status,
	}
}
