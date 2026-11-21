package service

import (
	"sync"
	"time"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
)

// Session is one run of one map. The Map is a snapshot taken at creation so
// editing/deleting the source map never invalidates a session.
//
// Live sessions are shared pointers guarded by Lock/Unlock. Anything handed
// outside the service (hub, resolvers) MUST be a Clone.
type Session struct {
	mu sync.Mutex

	ID           string
	DisplayName  string
	MapID        string
	Map          *engine.Map
	State        engine.State
	Seq          uint64
	CreatedAt    time.Time
	LastActionAt time.Time
	Attempts     int
	// BestMoves is the fewest moves in a won game of this session; nil until
	// the first win.
	BestMoves *int
	// OwnerAccountID is the account that created the session ("" = guest).
	OwnerAccountID string
	// OwnerTokenHash is the SHA-256 hex of the secret owner token returned by
	// CreateSession ("" = legacy session, where anyone may play).
	OwnerTokenHash string
	// LastEvent is the most recent move attempt; transient (not persisted).
	LastEvent *engine.MoveEvent
}

// Lock acquires the session mutex.
func (s *Session) Lock() { s.mu.Lock() }

// Unlock releases the session mutex.
func (s *Session) Unlock() { s.mu.Unlock() }

// Clone returns an immutable-by-convention deep copy (Map is shared: it is
// never mutated after creation). Caller must hold the lock if s is live.
func (s *Session) Clone() *Session {
	c := &Session{
		ID: s.ID, DisplayName: s.DisplayName, MapID: s.MapID, Map: s.Map,
		State: s.State.Clone(), Seq: s.Seq, CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt,
		Attempts: s.Attempts, OwnerAccountID: s.OwnerAccountID, OwnerTokenHash: s.OwnerTokenHash,
	}
	if s.BestMoves != nil {
		b := *s.BestMoves
		c.BestMoves = &b
	}
	if s.LastEvent != nil {
		ev := *s.LastEvent
		c.LastEvent = &ev
	}
	return c
}

// SessionFilter narrows ListSessions.
type SessionFilter struct {
	MapID string
	Sort  string // "recent" (default, LastActionAt desc) | "created"
	Limit int    // 0 = default 50, max 500
}

// GameService is the single facade used by every transport (GraphQL today).
type GameService interface {
	// CreateSession starts a session; accountID ("" = guest) becomes its
	// owner together with the returned secret owner token.
	CreateSession(mapID, displayName, accountID string) (*Session, string, error)
	GetSession(id string) (*Session, error)
	ListSessions(f SessionFilter) ([]*Session, error)
	DeleteSession(id string) error
	RenameSession(id, name string) (*Session, error)

	// Move attempts one move. A blocked attempt is not an error: it changes
	// nothing in the game state but still bumps Seq, sets LastEvent (Blocked)
	// and is broadcast so UIs can animate the bump. Errors: SESSION_TERMINAL.
	Move(id string, d engine.Direction) (*Session, error)
	// BulkMove applies dirs in order and stops early once the game ends and,
	// if stopOnBlocked, at the first blocked attempt (which is processed:
	// LastEvent.Blocked is set); otherwise blocked moves are ignored and the
	// rest still run. LastEvent is the last attempt made. Errors:
	// SESSION_TERMINAL (already finished), INVALID_ARGUMENT (too many moves).
	BulkMove(id string, dirs []engine.Direction, stopOnBlocked bool) (*Session, error)
	// Reset puts the game back to the start, keeping Attempts and BestMoves.
	Reset(id string) (*Session, error)

	ListMaps() []*engine.Map
	GetMap(id string) (*engine.Map, error)
	// ListMapsFor returns published maps plus viewerID's own drafts.
	ListMapsFor(viewerID string) []*engine.Map
	// GetMapFor returns a map only when viewerID may see it: published maps are
	// public, another player's draft is NOT_FOUND.
	GetMapFor(id, viewerID string) (*engine.Map, error)
	// ListMapEntries returns the catalogue for viewerID (published + own drafts).
	ListMapEntries(viewerID string) ([]*MapEntry, error)
	// GetMapEntry returns any catalogue entry, drafts included.
	GetMapEntry(id string) (*MapEntry, error)
	// CreateMapDraft stores a DRAFT owned by ownerID; it may be invalid until
	// published.
	CreateMapDraft(cfg engine.MapConfig, ownerID string) (*MapEntry, error)
	// UpdateMapOwned overwrites a map on behalf of actor and resets it to DRAFT.
	UpdateMapOwned(cfg engine.MapConfig, actor MapActor) (*MapEntry, error)
	// PublishMap promotes actor's draft once the finish is reachable.
	PublishMap(id string, actor MapActor) (*MapEntry, error)
	// DeleteMapOwned removes a map on behalf of actor.
	DeleteMapOwned(id string, actor MapActor) error

	// Admin-gated by the transport; the service itself does not authenticate.
	SaveMap(cfg engine.MapConfig) (*engine.Map, error)
	// CreateMap is SaveMap that fails atomically (INVALID_ARGUMENT) if the id exists.
	CreateMap(cfg engine.MapConfig) (*engine.Map, error)
	DeleteMap(id string) error

	// Shutdown persists all sessions. It is idempotent.
	Shutdown()
}

// MapStatus is a catalogue entry's lifecycle state.
type MapStatus string

const (
	// MapStatusDraft is visible only to its owner.
	MapStatusDraft MapStatus = "DRAFT"
	// MapStatusPublished is visible to everyone.
	MapStatusPublished MapStatus = "PUBLISHED"
)

// MapEntry is one catalogue entry: a shipped map, a published player map or
// (for its owner) a draft.
type MapEntry struct {
	ID      string
	Config  engine.MapConfig
	Builtin bool
	// OwnerID is the account that created a player map; empty for shipped maps
	// and legacy unowned rows.
	OwnerID string
	Status  MapStatus
}

// MapActor is who asks to change a map.
type MapActor struct {
	AccountID string
	Admin     bool
}

// MapStore provides maps (implemented by game/config.Manager).
type MapStore interface {
	List() []*engine.Map
	Get(id string) (*engine.Map, error) // NOT_FOUND error if absent
	Save(cfg engine.MapConfig) (*engine.Map, error)
	Create(cfg engine.MapConfig) (*engine.Map, error) // INVALID_ARGUMENT if id exists
	Delete(id string) error

	// Lifecycle API: drafts, ownership and publication.
	ListEntries(viewerID string) ([]*MapEntry, error)
	GetEntry(id string) (*MapEntry, error)
	CreateDraft(cfg engine.MapConfig, ownerID string) (string, error)
	SaveOwned(cfg engine.MapConfig, actor MapActor) (*MapEntry, error)
	Publish(id string, actor MapActor) (*MapEntry, error)
	DeleteOwned(id string, actor MapActor) error
}

// SessionStore holds live sessions (implemented by game/session.Manager).
// Get returns the live pointer. Persist writes the session durably (no-op for
// memory-only stores); the implementation takes the session lock and clones
// internally, so callers MUST NOT hold the lock when calling Persist.
type SessionStore interface {
	Put(s *Session) error // register a new live session (enforces MAX_SESSIONS)
	Get(id string) (*Session, bool)
	Delete(id string) error
	List() []*Session
	Persist(s *Session) error
}

// Broadcaster publishes immutable snapshots to subscribers
// (implemented by transport/websocket.Hub).
type Broadcaster interface {
	Broadcast(snapshot *Session)
}
