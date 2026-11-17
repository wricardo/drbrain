package service

import (
	"sync"
	"time"

	"github.com/wricardo/drbrain-motor-programming/game/engine"
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
	Program      engine.Program
	VM           engine.VMState
	Playing      bool
	SpeedMs      int
	Seq          uint64
	CreatedAt    time.Time
	LastActionAt time.Time
	Attempts     int
	BestSteps    int // 0 = never won
	// FewestSlots and FewestSteps are the session's best wins under the two
	// leaderboard orderings (nil = never won). They are recorded by the server
	// when a run is won and survive later edits and resets of the program.
	FewestSlots *Win
	FewestSteps *Win
	// AccountID is the account this session belongs to ("" = guest). It is only
	// ever set from the signed-in creator or by ClaimSessions with the owner
	// token, never from a session id alone.
	AccountID string
	// OwnerHash is the SHA-256 (hex) of the secret owner token returned once by
	// CreateOwnedSession ("" for sessions created before owner tokens). Never
	// exposed by the API.
	OwnerHash string
	// LastEvent is the most recent step event; transient (not persisted).
	LastEvent *engine.StepEvent
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
		Program: s.Program.Clone(), VM: s.VM.Clone(), Playing: s.Playing, SpeedMs: s.SpeedMs,
		Seq: s.Seq, CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt,
		Attempts: s.Attempts, BestSteps: s.BestSteps,
		AccountID: s.AccountID, OwnerHash: s.OwnerHash,
	}
	if s.FewestSlots != nil {
		w := *s.FewestSlots
		c.FewestSlots = &w
	}
	if s.FewestSteps != nil {
		w := *s.FewestSteps
		c.FewestSteps = &w
	}
	if s.LastEvent != nil {
		ev := *s.LastEvent
		ev.CallStack = append([]engine.Frame(nil), ev.CallStack...)
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
	CreateSession(mapID, displayName string) (*Session, error)
	// CreateOwnedSession is CreateSession for a caller that may be signed in:
	// the session is attached to accountID (when not empty) and a secret owner
	// token is returned (only its hash is kept).
	CreateOwnedSession(mapID, displayName, accountID string) (*Session, string, error)
	GetSession(id string) (*Session, error)
	ListSessions(f SessionFilter) ([]*Session, error)
	DeleteSession(id string) error
	RenameSession(id, name string) (*Session, error)
	// AuthorizeSessionChange checks that actor may delete or rename the session:
	// always for guest sessions, only the owning account (or the owner token) for
	// a signed-in player's. Transports call it before DeleteSession/RenameSession.
	AuthorizeSessionChange(id string, actor SessionActor) error
	// AuthorizeSessionEdit checks that actor may write the session's program
	// (setProgram) and therefore read it back: the creator proves it with the
	// owner token, or with the login of the account that owns it. Sessions that
	// predate owner tokens have no hash to compare and stay open. Transports call
	// it before SetProgram and before a response carries Session.program.
	// Execution control (run, step, pause, reset) is NOT gated: the watch link is
	// a shared playback view and those calls never reveal the program.
	AuthorizeSessionEdit(id string, actor SessionActor) error

	// SetProgram validates and stores the program and resets the VM to start.
	// Errors: INVALID_PROGRAM, SESSION_PLAYING.
	SetProgram(id string, p engine.Program) (*Session, error)
	// Run starts the server-side ticker (speedMs 50..5000).
	// Errors: SESSION_PLAYING (already), SESSION_TERMINAL, INVALID_ARGUMENT.
	Run(id string, speedMs int) (*Session, error)
	Pause(id string) (*Session, error)
	// Step executes one manual step. Errors: SESSION_PLAYING, SESSION_TERMINAL.
	Step(id string) (*Session, error)
	// Reset puts the VM back to start, keeping the program; stops playback.
	Reset(id string) (*Session, error)

	// GetLeaderboard ranks the winning sessions of a map (see LeaderboardQuery).
	// Errors: NOT_FOUND (unknown map), INVALID_ARGUMENT (bad order or limit).
	GetLeaderboard(q LeaderboardQuery) (*Leaderboard, error)
	// GetPlayerRuns returns the best run per won map for a set of session ids
	// or a display name (see RunsQuery). Errors: INVALID_ARGUMENT.
	GetPlayerRuns(q RunsQuery) ([]PlayerRun, error)

	// Accounts. All of these fail with FORBIDDEN when AccountsEnabled is false.
	AccountsEnabled() bool
	// SignIn finds or creates the account of a provider identity.
	SignIn(provider, providerUserID string) (acct *Account, created bool, err error)
	// StartLogin makes a new login for the account and returns the cookie token.
	StartLogin(accountID string) (token string, expires time.Time, err error)
	Authenticate(token string) (*Account, bool)
	EndLogin(token string)
	GetAccount(id string) (*Account, error)
	UpdateAccount(id string, u AccountUpdate) (*Account, error)
	// HandleChangeableAt is when the account may change its handle (zero = now).
	HandleChangeableAt(a *Account) time.Time
	// DeleteAccount removes the account, its logins and its links to runs.
	DeleteAccount(id string) error
	// GetProfile is NOT_FOUND for unknown handles and for private profiles
	// unless viewerAccountID is the owner.
	GetProfile(handle, viewerAccountID string) (*Profile, error)
	// RegisterPassword creates a username/password account (username = handle).
	// VerifyPassword checks a login: every failure is ErrBadCredentials.
	// ChangePassword needs the current password and ends the account's other logins.
	// All three need AUTH_PASSWORD and fail with FORBIDDEN otherwise.
	RegisterPassword(handle, password string) (*Account, error)
	VerifyPassword(handle, password string) (*Account, error)
	ChangePassword(accountID, current, next, keepToken string) error
	// ClaimSessions attaches sessions to an account using their owner tokens.
	ClaimSessions(accountID string, claims []SessionClaim) ([]ClaimResult, error)

	ListMaps() []*engine.Map
	GetMap(id string) (*engine.Map, error)
	// IsBuiltinMap reports whether id is a shipped map. Shipped maps are
	// read-only for players; the transport gates writes on the admin key.
	IsBuiltinMap(id string) bool
	// SaveMap replaces a map. Gated by the transport, not the service.
	SaveMap(cfg engine.MapConfig) (*engine.Map, error)
	// CreateMap is SaveMap that fails atomically (INVALID_ARGUMENT) if the id exists.
	CreateMap(cfg engine.MapConfig) (*engine.Map, error)
	DeleteMap(id string) error

	// Shutdown stops all runners and persists sessions.
	Shutdown()
}

// MapStore provides maps (implemented by game/config.Manager).
type MapStore interface {
	List() []*engine.Map
	Get(id string) (*engine.Map, error) // NOT_FOUND error if absent
	// Builtin reports whether id is shipped (read-only for players).
	Builtin(id string) bool
	Save(cfg engine.MapConfig) (*engine.Map, error)
	Create(cfg engine.MapConfig) (*engine.Map, error) // INVALID_ARGUMENT if id exists
	Delete(id string) error
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
