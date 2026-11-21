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
	ID           string    `json:"id"`
	DisplayName  string    `json:"display_name,omitempty"`
	MapID        string    `json:"map_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastActionAt time.Time `json:"last_action_at"`
	Seq          uint64    `json:"seq"`
	// Loaded is true when the entry is in the store's loaded set (persisted or
	// Put this process) and false for a save discovered on disk/database.
	Loaded bool `json:"loaded"`
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
	// MaxSessions bounds stored sessions; <= 0 means unlimited.
	MaxSessions int
	// Evict replaces the oldest LastActionAt entry instead of returning
	// ErrLimit. Games whose manager evicts (delivery) set it; games that reject
	// (mazes, motor) leave it false.
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
	s := &store{pool: opts.Pool, max: opts.MaxSessions, evict: opts.Evict, mem: make(map[string]entry)}
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
