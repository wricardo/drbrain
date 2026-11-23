// Package sessionstore is the shared durable store for game sessions. It
// stores a game-defined payload (JSON) plus a platform Meta, so games keep
// their own session types while sharing limits, listing and the PostgreSQL
// backend.
package sessionstore

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors returned by every backend.
var (
	// ErrNotFound means no entry exists for the id.
	ErrNotFound = errors.New("sessionstore: not found")
	// ErrLimit means the store is full and eviction is disabled.
	ErrLimit = errors.New("sessionstore: session limit reached")
	// ErrInvalid means the id or payload is unusable.
	ErrInvalid = errors.New("sessionstore: invalid entry")
)

// Meta is the part of a session the platform understands. Everything
// game-specific stays in the payload.
type Meta struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	// Game is the owning game's registration name ("delivery", "mazes", …).
	// The shared table holds every game's rows, so a store opened with
	// Options.Game only lists, loads and deletes its own.
	Game         string    `json:"game,omitempty"`
	MapID        string    `json:"map_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastActionAt time.Time `json:"last_action_at"`
	Seq          uint64    `json:"seq"`
	// AccountID links the save to a signed-in player ("" = guest). Games that
	// track ownership publish it here so a list can filter by owner without
	// decoding the payload.
	AccountID string `json:"account_id,omitempty"`
	// Counters is the list-row summary a game optionally publishes alongside
	// its payload. Nil means "not published": the game must decode the payload
	// to describe the row, which is what every save written before this field
	// did. Present counters make List O(rows) instead of O(bytes).
	Counters *Counters `json:"counters,omitempty"`
	// Loaded is true when the entry is in the store's loaded set (persisted or
	// Put this process) and false for a save discovered on disk/database.
	Loaded bool `json:"loaded"`
}

// Counters is the cheap description of a save's progress. It is a fixed
// platform shape so a session picker can render a row without decoding the
// payload. Delivery publishes tick, money, reputation and deliveries today;
// every other game leaves Meta.Counters nil.
type Counters struct {
	Tick       int64   `json:"tick"`
	Money      int     `json:"money"`
	Reputation float64 `json:"reputation,omitempty"`
	Deliveries int     `json:"deliveries,omitempty"`
}

// Store is the durable session interface. Games keep their live session
// objects; the store only ever sees their encoded payload.
type Store interface {
	// Put registers a live entry without writing it to durable storage.
	Put(meta Meta, payload []byte) error
	// Get returns a loaded entry.
	Get(id string) (Meta, []byte, bool)
	// Delete removes the entry from memory and durable storage.
	Delete(id string) error
	// List returns every entry, including saves not loaded in this process.
	List() ([]Meta, error)
	// Persist writes the payload and marks the entry loaded.
	Persist(meta Meta, payload []byte) error
	// Load reads one entry, loading it from durable storage when needed.
	Load(id string) (Meta, []byte, error)
	// Count is the number of loaded entries.
	Count() int
}

// Options configures a store.
type Options struct {
	// Pool is the PostgreSQL pool every server uses; it is required.
	Pool *pgxpool.Pool
	// Game scopes the store to one game's rows in the shared table. Empty
	// means unscoped (tests and migration tooling); every server-opened store
	// must set it, or it will list and overwrite other games' saves.
	Game string
	// MaxSessions bounds stored sessions; <= 0 means unlimited.
	MaxSessions int
	// Evict replaces the oldest LastActionAt entry instead of returning
	// ErrLimit. No current game sets it: delivery's Manager.Evict unloads a
	// live session from memory but does not evict stored saves. The option is
	// exercised only by the memory-store tests.
	Evict bool
}

type entry struct {
	meta    Meta
	payload []byte
}

// Open returns the PostgreSQL-backed store. The pool must already be migrated
// (pgdb.Open): the platform_sessions table is created by
// platform/pgdb/migrations.
func Open(opts Options) (Store, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("sessionstore: PostgreSQL pool is required")
	}
	s := &store{pool: opts.Pool, game: opts.Game, max: opts.MaxSessions, evict: opts.Evict, mem: make(map[string]entry)}
	return s, nil
}

// validID keeps ids safe as primary keys: no separators, no traversal, bounded.
func validID(id string) error {
	if id == "" || len(id) > 128 || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return fmt.Errorf("%w: id %q", ErrInvalid, id)
	}
	return nil
}

// oldestLocked returns the least recently active loaded id; "" when empty.
func oldestLocked(mem map[string]entry) string {
	oldest := ""
	var oldestAt int64
	for id, e := range mem {
		t := e.meta.LastActionAt.UnixNano()
		if oldest == "" || t < oldestAt {
			oldest, oldestAt = id, t
		}
	}
	return oldest
}
