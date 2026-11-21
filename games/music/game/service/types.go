// Package service is the single facade (GameService) every transport uses.
//
// It owns the session lifecycle on top of three collaborators: a PieceStore
// (the score catalogue), a SessionStore (live sessions + persistence) and a
// Broadcaster (subscriber fan-out). New wires them together.
//
// Every session embeds a private snapshot of its piece, so editing or deleting
// a score never invalidates stored game state. Every mutation follows the same
// pattern: lock -> mutate -> recompute Solved -> Seq++ -> Clone -> unlock ->
// persist -> broadcast. Nothing persists or broadcasts while holding a session
// lock.
package service

import (
	"sync"
	"time"

	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
)

// LastChange kinds. They are the wire strings the client switches on.
const (
	KindSwitch    = "SWITCH"
	KindTransform = "TRANSFORM"
	KindArrange   = "ARRANGE"
	KindReset     = "RESET"
	KindSolve     = "SOLVE"
)

// Change summarises the mutation that produced a snapshot. A and B are the
// operation's indices (SWITCH: both slots; TRANSFORM: the slot; others 0).
type Change struct {
	Kind string
	A    int
	B    int
}

// Session is one Music Region game. Piece is a frozen snapshot shared read-only
// by every clone; Slots are the player's current arrangement.
//
// Live sessions are shared pointers guarded by Lock/Unlock. Anything handed
// outside the service (hub, resolvers) MUST be a Clone.
type Session struct {
	mu sync.Mutex

	ID           string
	DisplayName  string
	Difficulty   engine.Difficulty
	Seed         int64
	Piece        *score.Piece
	Slots        []engine.Placement
	Current      int
	Seq          uint64
	CreatedAt    time.Time
	LastActionAt time.Time
	Wins         int
	Solved       bool
	// LastChange is the most recent mutation; transient (not persisted).
	LastChange *Change
}

// Lock acquires the session mutex.
func (s *Session) Lock() { s.mu.Lock() }

// Unlock releases the session mutex.
func (s *Session) Unlock() { s.mu.Unlock() }

// Clone returns an immutable-by-convention deep copy (Piece is shared: it is
// never mutated after creation). The caller must hold the lock if s is live.
func (s *Session) Clone() *Session {
	c := &Session{
		ID: s.ID, DisplayName: s.DisplayName, Difficulty: s.Difficulty, Seed: s.Seed,
		Piece: s.Piece, Slots: append([]engine.Placement(nil), s.Slots...), Current: s.Current,
		Seq: s.Seq, CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt,
		Wins: s.Wins, Solved: s.Solved,
	}
	if s.LastChange != nil {
		ch := *s.LastChange
		c.LastChange = &ch
	}
	return c
}

// SessionFilter narrows ListSessions.
type SessionFilter struct {
	PieceID string
	Sort    string // "recent" (default, LastActionAt desc) | "created"
	Limit   int    // 0 = default 50, max 500
}

// GameService is the single facade used by every transport (GraphQL today).
type GameService interface {
	// CreateSession starts a challenge on a private snapshot of pieceID. A
	// seed <= 0 draws a random one; the chosen seed is returned in the session.
	CreateSession(pieceID string, d engine.Difficulty, displayName string, seed int64) (*Session, error)
	GetSession(id string) (*Session, error)
	ListSessions(f SessionFilter) ([]*Session, error)
	DeleteSession(id string) error
	RenameSession(id, name string) (*Session, error)

	// SwitchMeasures swaps two slots of the arrangement. No-op swaps still bump
	// Seq and are broadcast.
	SwitchMeasures(id string, a, b int) (*Session, error)
	// TransformMeasure replaces one slot's flips.
	TransformMeasure(id string, index int, t engine.Transform) (*Session, error)
	// SetArrangement replaces the whole arrangement; slots must be a
	// permutation of the session's measure ids.
	SetArrangement(id string, slots []engine.Placement) (*Session, error)
	// ResetArrangement restores the scramble the session was generated with.
	ResetArrangement(id string) (*Session, error)

	ListExcerpts() []*score.Piece
	GetExcerpt(id string) (*score.Piece, error)

	// Shutdown persists all sessions. It is idempotent.
	Shutdown()
}

// PieceStore provides the score catalogue (implemented by score.Catalogue).
// Get returns a deep copy; a missing piece wraps mapstore.ErrNotFound.
type PieceStore interface {
	List() ([]score.Entry, error)
	Get(id string) (*score.Piece, error)
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
