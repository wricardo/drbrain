// Package config loads, lists and saves map files through platform/mapstore.
//
// Shipped maps live in the maps directory. Player maps live in PostgreSQL with
// ownership, drafts, publication and a quota; in file mode (no pool) the
// directory is the single admin-managed catalogue, as it always was.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/games/mazes/validate"
	"github.com/wricardo/drbrain/platform/mapstore"
)

// Options configures a Manager.
type Options struct {
	// Dir holds the shipped maps. It is required.
	Dir string
	// Pool stores player maps in PostgreSQL. Production always sets it; nil
	// selects the file catalogue.
	Pool *pgxpool.Pool
	// MaxUserMaps caps how many player maps may exist across all accounts
	// (0 = no cap).
	MaxUserMaps int
}

// Manager is a mapstore-backed service.MapStore. Parsed maps are revalidated on
// every read; the store caches the decoded configs.
type Manager struct {
	dir   string
	store *mapstore.Store[engine.MapConfig]
}

var _ service.MapStore = (*Manager)(nil)

// NewManager opens the shipped maps directory as a catalogue. Every existing
// file is validated at startup (an invalid map is a fatal error naming the
// file) and its id must match the file name.
func NewManager(opts Options) (*Manager, error) {
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create maps dir: %w", err)
	}
	customDir := ""
	if opts.Pool == nil {
		// File mode is the legacy single directory: Create must write next to
		// the shipped maps, not into a "<dir>/custom" sibling.
		customDir = opts.Dir
	}
	store, err := mapstore.Open(mapstore.Options[engine.MapConfig]{
		BuiltinDir: opts.Dir,
		CustomDir:  customDir,
		Game:       "mazes",
		Pool:       opts.Pool,
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
		// Structural checks on read and draft save; the finish must be
		// reachable before a map is published.
		Validate: func(cfg engine.MapConfig) error {
			_, err := engine.NewMap(cfg)
			return err
		},
		PublishValidate: publishValidate,
		MaxCustomMaps:   opts.MaxUserMaps,
		// Mazes never issues edit tokens: ownership is the account id.
		NoEditTokens: true,
	})
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(opts.Dir)
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
	return &Manager{dir: opts.Dir, store: store}, nil
}

// publishValidate is the publication gate: the map must be structurally valid
// and its finish must be reachable from the start. Check runs both (and the
// par/stairs search); an unreachable finish is reported as an issue.
func publishValidate(cfg engine.MapConfig) error {
	res := validate.Check(cfg)
	if res.Valid && res.FinishReachable {
		return nil
	}
	if len(res.Issues) > 0 {
		return errors.New(strings.Join(res.Issues, "; "))
	}
	return errors.New("finish is unreachable from the start")
}

// List returns copies of all playable maps sorted by id. Other players'
// drafts are hidden; ListEntries exposes the viewer's own drafts in full.
func (m *Manager) List() []*engine.Map {
	entries, err := m.store.ListFor("")
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

// IsBuiltin reports whether id is a shipped map (read-only for players).
func (m *Manager) IsBuiltin(id string) bool { return m.store.IsBuiltin(id) }

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

// ListEntries returns the catalogue for a viewer: shipped maps, published
// player maps and the viewer's own drafts. Other players' drafts stay hidden.
func (m *Manager) ListEntries(viewerID string) ([]*service.MapEntry, error) {
	rows, err := m.store.ListFor(viewerID)
	if err != nil {
		return nil, fmt.Errorf("list maps: %w", err)
	}
	out := make([]*service.MapEntry, len(rows))
	for i, row := range rows {
		out[i] = toMapEntry(row)
	}
	return out, nil
}

// GetEntry returns one catalogue entry with its owner and lifecycle state.
func (m *Manager) GetEntry(id string) (*service.MapEntry, error) {
	if !engine.ValidID(id) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	entry, err := m.store.Get(id)
	if err != nil {
		return nil, mapLifecycleError(id, err)
	}
	return toMapEntry(entry), nil
}

// CreateDraft stores a new DRAFT map owned by ownerID. It only needs to be
// structurally valid; publication runs the finish-reachability check. Mazes
// issues no edit tokens, so the returned token is always empty.
func (m *Manager) CreateDraft(cfg engine.MapConfig, ownerID string) (string, error) {
	if !engine.ValidID(cfg.ID) {
		return "", service.Errorf(service.CodeInvalidArgument, "invalid map id %q: must match ^[a-z0-9_-]{1,64}$", cfg.ID)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return "", service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	token, err := m.store.CreateDraft(parsed.ID, parsed.Config(), ownerID)
	if err != nil {
		return "", mapLifecycleError(parsed.ID, err)
	}
	return token, nil
}

// SaveOwned overwrites a map on behalf of actor. Owners edit their own maps (a
// published map returns to DRAFT); admins may edit shipped maps.
func (m *Manager) SaveOwned(cfg engine.MapConfig, actor service.MapActor) (*service.MapEntry, error) {
	if !engine.ValidID(cfg.ID) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q: must match ^[a-z0-9_-]{1,64}$", cfg.ID)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	entry, err := m.store.SaveOwned(parsed.ID, parsed.Config(), mapActor(actor))
	if err != nil {
		return nil, mapLifecycleError(parsed.ID, err)
	}
	return toMapEntry(entry), nil
}

// Publish promotes the actor's draft once the finish is reachable.
func (m *Manager) Publish(id string, actor service.MapActor) (*service.MapEntry, error) {
	if !engine.ValidID(id) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	entry, err := m.store.Publish(id, mapActor(actor))
	if err != nil {
		return nil, mapLifecycleError(id, err)
	}
	return toMapEntry(entry), nil
}

// DeleteOwned removes a map on behalf of actor: owners delete their own maps,
// admins may delete shipped maps.
func (m *Manager) DeleteOwned(id string, actor service.MapActor) error {
	if !engine.ValidID(id) {
		return service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	if err := m.store.DeleteOwned(id, mapActor(actor)); err != nil {
		return mapLifecycleError(id, err)
	}
	return nil
}

// toMapEntry converts a storage row into the transport-facing entry.
func toMapEntry(row mapstore.Map[engine.MapConfig]) *service.MapEntry {
	status := service.MapStatusPublished
	if row.Status == mapstore.StatusDraft {
		status = service.MapStatusDraft
	}
	return &service.MapEntry{
		ID:      row.Name,
		Config:  row.Config,
		Builtin: row.Builtin,
		OwnerID: row.OwnerID,
		Status:  status,
	}
}

func mapActor(actor service.MapActor) mapstore.Actor {
	return mapstore.Actor{AccountID: actor.AccountID, Admin: actor.Admin}
}

// DeleteDraftsByOwner removes every draft owned by ownerID. Account deletion
// calls it before published maps are orphaned.
func (m *Manager) DeleteDraftsByOwner(ownerID string) error {
	if err := m.store.DeleteDraftsByOwner(ownerID); err != nil {
		return mapLifecycleError("", err)
	}
	return nil
}

// OrphanPublishedByOwner detaches a deleted account's published maps so they
// stay playable.
func (m *Manager) OrphanPublishedByOwner(ownerID string) error {
	if err := m.store.OrphanPublishedByOwner(ownerID); err != nil {
		return mapLifecycleError("", err)
	}
	return nil
}

// mapLifecycleError maps mapstore failures onto the service's public codes so
// the API layer returns FORBIDDEN/NOT_FOUND/LIMIT_REACHED instead of internals.
func mapLifecycleError(id string, err error) error {
	switch {
	case errors.Is(err, mapstore.ErrNotFound):
		return service.Errorf(service.CodeNotFound, "map %q not found", id)
	case errors.Is(err, mapstore.ErrForbidden):
		return service.Errorf(service.CodeForbidden, "map %q belongs to another player", id)
	case errors.Is(err, mapstore.ErrLimit):
		return service.Errorf(service.CodeLimit, "%v", err)
	case errors.Is(err, mapstore.ErrExists):
		return service.Errorf(service.CodeInvalidArgument, "map %q already exists", id)
	case errors.Is(err, mapstore.ErrReadOnly):
		return service.Errorf(service.CodeInvalidArgument, "map %q is read-only", id)
	case errors.Is(err, mapstore.ErrInvalid):
		return service.Errorf(service.CodeInvalidArgument, "invalid map %q: %v", id, err)
	}
	if id == "" {
		return fmt.Errorf("mapstore: %w", err)
	}
	return fmt.Errorf("map %q: %w", id, err)
}
