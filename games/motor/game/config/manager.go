// Package config loads, lists and saves maps for the Motor game. Shipped maps
// stay files in Options.Dir; player-created maps live in platform_maps through
// platform/mapstore, which owns the ownership, draft and quota rules. Without a
// PostgreSQL pool the manager falls back to mapstore's file mode for builtins
// and CLI/tests.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/validate"
	"github.com/wricardo/drbrain/platform/mapstore"
)

// game is the platform_maps partition for Motor.
const game = "motor"

// Options configures a Manager.
type Options struct {
	// Dir holds the shipped maps. They are files and are read-only through the
	// API unless the caller is an admin.
	Dir string
	// UserDir is the legacy file directory for community maps. It is used only
	// when Pool is nil (CLI and file-only tests). Production must use Pool.
	//
	// Deprecated: player maps belong in platform_maps.
	UserDir string
	// UserStore was the legacy PostgreSQL map store. NewManager rejects it;
	// pass Pool instead. The type remains for the deprecated
	// PostgresUserMaps import path and its tests.
	//
	// Deprecated: use Pool.
	UserStore UserStore
	// MaxUserMaps caps how many custom maps Motor may have (0 = no cap).
	MaxUserMaps int
	// Pool enables platform_maps for player maps. Nil keeps the legacy
	// file-backed mode for builtins-only use and tests.
	Pool *pgxpool.Pool
}

// UserStore persists player-created maps for the legacy maps table.
//
// Deprecated: Manager reads and writes platform_maps through Pool.
type UserStore interface {
	List() ([]engine.MapConfig, error)
	Put(cfg engine.MapConfig) error
	Delete(id string) error
}

// Manager is a service.MapStore backed by platform/mapstore.
type Manager struct {
	opts    Options
	store   *mapstore.Store[engine.MapConfig]
	pg      bool
	builtin map[string]bool // ids shipped at startup; runtime files in Dir are not shipped
}

var _ service.MapStore = (*Manager)(nil)

// NewManager opens the map catalogue. Shipped maps are validated eagerly so a
// bad file fails startup; corrupt player rows are skipped by the store.
//
// Without Pool it uses mapstore's file mode (UserDir when set, otherwise Dir
// itself), which is useful for `go test` and CLI tools but carries no ownership
// metadata.
func NewManager(opts Options) (*Manager, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("create maps manager: Dir is required")
	}
	if opts.Pool == nil && opts.UserStore != nil {
		return nil, fmt.Errorf("create maps manager: UserStore is deprecated; pass Pool")
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create maps dir: %w", err)
	}
	builtin, err := loadBuiltins(opts.Dir)
	if err != nil {
		return nil, err
	}

	storeOpts := mapstore.Options[engine.MapConfig]{
		BuiltinDir:      opts.Dir,
		Game:            game,
		Pool:            opts.Pool,
		Decode:          decodeMapConfig,
		Encode:          encodeMapConfig,
		Validate:        validateStructure,
		PublishValidate: validatePublish,
		MaxCustomMaps:   opts.MaxUserMaps,
		IssueEditTokens: false,
	}
	if opts.Pool != nil {
		if opts.UserDir != "" {
			log.Printf("config: MOTOR user dir %q is ignored; player maps live in platform_maps", opts.UserDir)
		}
	} else {
		custom := opts.UserDir
		if custom == "" {
			custom = opts.Dir
		}
		storeOpts.CustomDir = custom
		storeOpts.NoEditTokens = true
		if err := os.MkdirAll(custom, 0o755); err != nil {
			return nil, fmt.Errorf("create maps dir: %w", err)
		}
		logCorruptUserMaps(custom)
	}

	store, err := mapstore.Open(storeOpts)
	if err != nil {
		return nil, fmt.Errorf("open maps: %w", err)
	}
	return &Manager{opts: opts, store: store, pg: opts.Pool != nil, builtin: builtin}, nil
}

// List returns copies of all maps sorted by id.
func (m *Manager) List() []*engine.Map {
	entries := m.ListEntries()
	out := make([]*engine.Map, len(entries))
	for i, e := range entries {
		out[i] = e.Map
	}
	return out
}

// ListEntries returns every map with its ownership metadata. Visibility is the
// caller's (service) job: ListEntries includes other players' drafts.
func (m *Manager) ListEntries() []service.MapEntry {
	rows, err := m.store.List()
	if err != nil {
		log.Printf("config: list maps: %v", err)
		return nil
	}
	out := make([]service.MapEntry, 0, len(rows))
	for _, row := range rows {
		e, err := entryOf(row)
		if err != nil {
			log.Printf("config: skipping map %q: %v", row.Name, err)
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Map.ID < out[j].Map.ID })
	return out
}

// Get returns a copy of the map or a NOT_FOUND *service.Error.
func (m *Manager) Get(id string) (*engine.Map, error) {
	e, err := m.Entry(id)
	if err != nil {
		return nil, err
	}
	return e.Map, nil
}

// Entry returns a copy of the map with its metadata, or NOT_FOUND.
func (m *Manager) Entry(id string) (service.MapEntry, error) {
	row, err := m.store.Get(id)
	if err != nil {
		return service.MapEntry{}, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	e, err := entryOf(row)
	if err != nil {
		return service.MapEntry{}, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	return e, nil
}

// Builtin reports whether id is a shipped map loaded at startup.
func (m *Manager) Builtin(id string) bool { return m.builtin[id] }

// Save validates cfg, writes it and replaces any existing map with the same id.
// Editing a community map needs its owner and puts it back into DRAFT; shipped
// and unowned legacy maps need an admin.
func (m *Manager) Save(cfg engine.MapConfig, actor service.MapActor) (*engine.Map, error) {
	parsed, err := parsedMap(cfg)
	if err != nil {
		return nil, err
	}
	existing, err := m.store.Get(parsed.ID)
	switch {
	case err == nil:
		return m.saveExisting(existing, parsed.Config(), actor)
	case errors.Is(err, mapstore.ErrNotFound):
		return m.saveNew(parsed, actor)
	default:
		return nil, m.storeError(parsed.ID, err, "save map")
	}
}

// Create stores a new DRAFT map owned by ownerID. In file-only mode the map is
// a legacy published file, matching mapstore's file semantics.
func (m *Manager) Create(cfg engine.MapConfig, ownerID string) (*engine.Map, error) {
	if ownerID == "" {
		return nil, service.Errorf(service.CodeForbidden, "creating a map needs a signed-in player")
	}
	parsed, err := parsedMap(cfg)
	if err != nil {
		return nil, err
	}
	if m.pg {
		if _, err := m.store.CreateDraft(parsed.ID, parsed.Config(), ownerID); err != nil {
			return nil, m.storeError(parsed.ID, err, "create map")
		}
	} else {
		if m.fileCapReached() {
			return nil, m.limitError()
		}
		if _, err := m.store.Create(parsed.ID, parsed.Config()); err != nil {
			return nil, m.storeError(parsed.ID, err, "create map")
		}
	}
	return parsed.Clone(), nil
}

// Publish validates a draft and makes it public. Publishing an already
// published, shipped or ownerless map is a no-op; only the owner (or an admin)
// may publish a draft.
func (m *Manager) Publish(id string, actor service.MapActor) (*engine.Map, error) {
	if !engine.ValidID(id) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	existing, err := m.store.Get(id)
	if err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
		}
		return nil, m.storeError(id, err, "publish map")
	}
	e, err := entryOf(existing)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	if m.builtin[id] || e.OwnerID == "" {
		return e.Map, nil
	}
	if e.OwnerID != actor.AccountID {
		return nil, service.Errorf(service.CodeForbidden, "only the map's owner may publish it")
	}
	if e.Status == engine.MapStatusPublished {
		return e.Map, nil
	}
	if !m.pg {
		return e.Map, nil
	}
	if res := validate.Check(existing.Config); !res.Valid {
		return nil, service.Errorf(service.CodeInvalidArgument, "map is not valid: %s", summarizeIssues(res.Issues))
	}
	row, err := m.store.Publish(id, mapActor(actor))
	if err != nil {
		return nil, m.storeError(id, err, "publish map")
	}
	e, err = entryOf(row)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	return e.Map, nil
}

// Delete removes the map. Community maps need their owner; shipped and unowned
// legacy maps need an admin. NOT_FOUND if absent.
func (m *Manager) Delete(id string, actor service.MapActor) error {
	if !engine.ValidID(id) {
		return service.Errorf(service.CodeInvalidArgument, "invalid map id %q", id)
	}
	existing, err := m.store.Get(id)
	if err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return service.Errorf(service.CodeNotFound, "map %q not found", id)
		}
		return m.storeError(id, err, "delete map")
	}
	e, err := entryOf(existing)
	if err != nil {
		return service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	switch {
	case m.builtin[id] || (m.pg && e.OwnerID == ""):
		if !actor.Admin {
			return service.Errorf(service.CodeForbidden, "deleting this map needs X-Admin-Key")
		}
	case m.pg && actor.AccountID != e.OwnerID:
		return service.Errorf(service.CodeForbidden, "only the map's owner may delete it")
	}
	if m.pg {
		err = m.store.DeleteOwned(id, mapActor(actor))
	} else {
		err = m.store.Delete(id, true)
	}
	if err != nil {
		if errors.Is(err, mapstore.ErrForbidden) {
			return service.Errorf(service.CodeForbidden, "deleting this map needs X-Admin-Key")
		}
		return m.storeError(id, err, "delete map")
	}
	delete(m.builtin, id)
	return nil
}

// fileCapReached reports whether another custom map would exceed MaxUserMaps in
// file-only mode. PostgreSQL mode enforces the cap atomically in mapstore.
func (m *Manager) fileCapReached() bool {
	if m.opts.MaxUserMaps <= 0 {
		return false
	}
	rows, err := m.store.List()
	if err != nil {
		return false
	}
	n := 0
	for _, row := range rows {
		if !m.builtin[row.Name] {
			n++
		}
	}
	return n >= m.opts.MaxUserMaps
}

// DetachAccount removes an account's drafts and orphans its published maps.
// The account service runs it before deleting the account row.
func (m *Manager) DetachAccount(ownerID string) error {
	return errors.Join(
		m.store.DeleteDraftsByOwner(ownerID),
		m.store.OrphanPublishedByOwner(ownerID),
	)
}

// ---- helpers --------------------------------------------------------------

func (m *Manager) saveExisting(existing mapstore.Map[engine.MapConfig], cfg engine.MapConfig, actor service.MapActor) (*engine.Map, error) {
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	switch {
	case m.builtin[existing.Name]:
		if !actor.Admin {
			return nil, service.Errorf(service.CodeForbidden, "changing a shipped map needs X-Admin-Key")
		}
	case m.pg && existing.OwnerID == "":
		if !actor.Admin {
			return nil, service.Errorf(service.CodeForbidden, "this map has no owner; changing it needs X-Admin-Key")
		}
	case m.pg && !actor.Admin && actor.AccountID != existing.OwnerID:
		return nil, service.Errorf(service.CodeForbidden, "only the map's owner may change it")
	}

	var row mapstore.Map[engine.MapConfig]
	if m.pg {
		row, err = m.store.SaveOwned(parsed.ID, cfg, mapActor(actor))
	} else {
		err = m.store.Save(parsed.ID, cfg, true)
	}
	if err != nil {
		return nil, m.storeError(parsed.ID, err, "save map")
	}
	if !m.pg {
		return parsed.Clone(), nil
	}
	e, err := entryOf(row)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	return e.Map, nil
}

func (m *Manager) saveNew(parsed *engine.Map, actor service.MapActor) (*engine.Map, error) {
	if actor.AccountID == "" && !actor.Admin {
		return nil, service.Errorf(service.CodeForbidden, "saving a map needs a signed-in player")
	}
	if m.pg && actor.AccountID != "" {
		if _, err := m.store.CreateDraft(parsed.ID, parsed.Config(), actor.AccountID); err == nil {
			return parsed.Clone(), nil
		} else if !errors.Is(err, mapstore.ErrExists) {
			return nil, m.storeError(parsed.ID, err, "save map")
		}
		// Another request created it first: fall through to the update path.
		if existing, getErr := m.store.Get(parsed.ID); getErr == nil {
			return m.saveExisting(existing, parsed.Config(), actor)
		}
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", parsed.ID)
	}
	if !m.pg && actor.AccountID != "" && m.fileCapReached() {
		return nil, m.limitError()
	}
	if _, err := m.store.Create(parsed.ID, parsed.Config()); err != nil {
		return nil, m.storeError(parsed.ID, err, "save map")
	}
	return parsed.Clone(), nil
}

func (m *Manager) storeError(id string, err error, op string) error {
	switch {
	case errors.Is(err, mapstore.ErrNotFound):
		return service.Errorf(service.CodeNotFound, "map %q not found", id)
	case errors.Is(err, mapstore.ErrExists):
		return service.Errorf(service.CodeInvalidArgument, "map %q already exists", id)
	case errors.Is(err, mapstore.ErrLimit):
		return m.limitError()
	case errors.Is(err, mapstore.ErrForbidden):
		return service.Errorf(service.CodeForbidden, "only the map's owner may change it")
	case errors.Is(err, mapstore.ErrReadOnly):
		return service.Errorf(service.CodeForbidden, "changing a shipped map needs X-Admin-Key")
	case errors.Is(err, mapstore.ErrInvalid):
		return service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

func (m *Manager) limitError() error {
	return service.Errorf(service.CodeLimit, "%d player maps already exist; delete one before creating another", m.opts.MaxUserMaps)
}

func mapActor(actor service.MapActor) mapstore.Actor {
	return mapstore.Actor{AccountID: actor.AccountID, Admin: actor.Admin}
}

func entryOf(row mapstore.Map[engine.MapConfig]) (service.MapEntry, error) {
	parsed, err := engine.NewMap(row.Config)
	if err != nil {
		return service.MapEntry{}, err
	}
	return service.MapEntry{
		Map:     parsed,
		OwnerID: row.OwnerID,
		Status:  engine.MapStatus(row.Status),
	}, nil
}

// decodeMapConfig drops the legacy owner/status fields: platform_maps columns
// are authoritative, and the playable config must never carry them.
func decodeMapConfig(raw []byte) (engine.MapConfig, error) {
	var cfg engine.MapConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	cfg.OwnerID, cfg.Status = "", ""
	return cfg, nil
}

func encodeMapConfig(cfg engine.MapConfig) ([]byte, error) {
	cfg.OwnerID, cfg.Status = "", ""
	return json.Marshal(cfg)
}

func validateStructure(cfg engine.MapConfig) error {
	_, err := engine.NewMap(cfg)
	return err
}

func validatePublish(cfg engine.MapConfig) error {
	if res := validate.Check(cfg); !res.Valid {
		return errors.New(summarizeIssues(res.Issues))
	}
	return nil
}

// parsedMap validates the id and structure of a map. Reachability is Publish's
// job, so a draft only needs to be structurally playable.
func parsedMap(cfg engine.MapConfig) (*engine.Map, error) {
	if !engine.ValidID(cfg.ID) {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map id %q: must match ^[a-z0-9_-]{1,64}$", cfg.ID)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	return parsed, nil
}

// summarizeIssues keeps a validation failure readable in one GraphQL error.
func summarizeIssues(issues []string) string {
	const max = 5
	if len(issues) <= max {
		return strings.Join(issues, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(issues[:max], "; "), len(issues)-max)
}

// loadBuiltins validates every shipped map and returns its ids. A bad file
// fails startup naming the file.
func loadBuiltins(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read maps dir: %w", err)
	}
	out := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		cfg, _, err := readMap(path)
		if err != nil {
			return nil, err
		}
		out[cfg.ID] = true
	}
	return out, nil
}

// logCorruptUserMaps reports legacy user files that will be skipped; startup
// continues, like the old file manager.
func logCorruptUserMaps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if _, _, err := readMap(path); err != nil {
			log.Printf("config: skipping user map %s: %v", path, err)
		}
	}
}

// readMap parses and validates one map file.
func readMap(path string) (engine.MapConfig, *engine.Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return engine.MapConfig{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	var cfg engine.MapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return engine.MapConfig{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.OwnerID, cfg.Status = "", ""
	if want := strings.TrimSuffix(filepath.Base(path), ".json"); cfg.ID != want {
		return engine.MapConfig{}, nil, fmt.Errorf("%s: map id %q does not match file name %q", path, cfg.ID, want)
	}
	parsed, err := engine.NewMap(cfg)
	if err != nil {
		return engine.MapConfig{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, parsed, nil
}
