// Package sessionstore is the shared durable store for game sessions. It
// stores a game-defined payload (JSON) plus a platform Meta, so games keep
// their own session types while sharing limits, listing and both backends.
package sessionstore

import (
	"errors"
	"time"
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
