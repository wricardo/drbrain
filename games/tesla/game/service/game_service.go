package service

import (
	"context"
	"time"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
)

// CreateSessionOptions controls optional per-session behavior at creation time.
type CreateSessionOptions struct {
	// ViewerID is the signed-in caller's account id ("" = guest). It decides
	// whether the caller may start a session on a draft map.
	ViewerID     string
	FogEnabled   bool
	FogRadius    int
	GridPassword string
	// MoveDelayMs sets the per-step delay (and websocket broadcast) for bulkMove. Single moves are not delayed.
	// nil leaves the session default unchanged.
	MoveDelayMs *int
}

const DefaultMoveDelayMs = 300

// BulkMoveOptions paces a bulk move for spectators.
type BulkMoveOptions struct {
	// StepDelay pauses between moves. The service lock is released while waiting.
	StepDelay time.Duration
	// OnStep, when set, receives a snapshot after every attempted move. It runs
	// without the service lock held.
	OnStep func(*engine.GameState)
}

// GameService defines all game-related operations
type GameService interface {
	// Session Management
	CreateSession(ctx context.Context, configName string, opts ...CreateSessionOptions) (*SessionInfo, error)
	GetSession(ctx context.Context, sessionID string) (*SessionInfo, error)
	ListSessions(ctx context.Context) ([]*SessionInfo, error)
	DeleteSession(ctx context.Context, sessionID string) error
	UpdateSessionDisplayName(ctx context.Context, sessionID, displayName string) (*SessionInfo, error)

	// Game Operations
	Move(ctx context.Context, sessionID, direction string, reset bool) (*MoveResult, error)
	BulkMove(ctx context.Context, sessionID string, moves []string, reset bool, opts BulkMoveOptions) (*BulkMoveResult, error)
	Reset(ctx context.Context, sessionID string) (*engine.GameState, error)

	// Game State
	GetGameState(ctx context.Context, sessionID string) (*engine.GameState, error)
	GetMoveHistory(ctx context.Context, sessionID string, opts HistoryOptions) (*HistoryResponse, error)

	// Map management
	ListMaps(ctx context.Context) ([]*MapInfo, error)
	LoadMap(ctx context.Context, mapName string) (*engine.GameConfig, error)
	// ListMapEntries returns published maps plus viewerID's own drafts.
	ListMapEntries(ctx context.Context, viewerID string) ([]*MapEntry, error)
	// GetMapEntry returns one catalogue entry, drafts included; the caller
	// decides visibility.
	GetMapEntry(ctx context.Context, mapName string) (*MapEntry, error)
	// CreateMapDraft stores a DRAFT owned by ownerID and returns its edit token.
	CreateMapDraft(ctx context.Context, mapName string, config *engine.GameConfig, ownerID string) (*MapEntry, string, error)
	// UpdateMapOwned overwrites a map on behalf of actor (owner or admin).
	UpdateMapOwned(ctx context.Context, mapName string, config *engine.GameConfig, actor MapActor) (*MapEntry, error)
	// PublishMap promotes actor's draft after the winnability proof.
	PublishMap(ctx context.Context, mapName string, actor MapActor) (*MapEntry, error)
	// DeleteMapOwned removes a map on behalf of actor (owner or admin).
	DeleteMapOwned(ctx context.Context, mapName string, actor MapActor) error
	// ClaimMap attaches a legacy unowned map to ownerID after checking editKey.
	ClaimMap(ctx context.Context, mapName, ownerID, editKey string) (*MapEntry, error)

	// CreateMap stores a brand-new map and returns its edit token. It fails with
	// config.ErrMapExists if the name is taken; it never overwrites.
	CreateMap(ctx context.Context, mapName string, config *engine.GameConfig) (*MapCreateResult, error)
	// UpdateMap overwrites an existing map. allowBuiltin must be set only after
	// the transport has verified admin credentials.
	UpdateMap(ctx context.Context, mapName string, config *engine.GameConfig, allowBuiltin bool) error
	// MapAuthorization describes how mapName may be mutated.
	MapAuthorization(ctx context.Context, mapName string) (builtin bool, exists bool, err error)
	// VerifyMapEditToken reports whether editKey authorizes mutating mapName.
	VerifyMapEditToken(ctx context.Context, mapName, editKey string) error
	// DeleteMap removes a map. allowBuiltin must be set only after the transport
	// has verified admin credentials.
	DeleteMap(ctx context.Context, mapName string, allowBuiltin bool) error
}

// SessionManager defines session storage operations
type SessionManager interface {
	Create(id string, config *engine.GameConfig) (*Session, error)
	Get(id string) (*Session, error)
	GetOrCreate(id string, config *engine.GameConfig) (*Session, error)
	List() []*Session
	Delete(id string) error
	UpdateLastAction(id string) error
	UpdateDisplayName(id, displayName string) error
	Save(id string) error
}

// ConfigManager handles game configuration loading
type ConfigManager interface {
	LoadConfig(name string) (*engine.GameConfig, error)
	ListConfigs() ([]*MapInfo, error)
	GetDefault() *engine.GameConfig
	// CreateCustom stores a new user-created map and returns its edit token.
	CreateCustom(name string, config *engine.GameConfig) (string, error)
	// SaveConfig overwrites an existing custom map; built-in maps are rejected.
	SaveConfig(name string, config *engine.GameConfig) error
	// SaveBuiltin writes a built-in map. Only reachable through an explicit
	// admin path (see GameService.UpdateMap with allowBuiltin).
	SaveBuiltin(name string, config *engine.GameConfig) error
	// DeleteConfig removes a custom map; built-in maps are rejected.
	DeleteConfig(name string) error
	// DeleteBuiltin removes a built-in map. Admin-only, like SaveBuiltin.
	DeleteBuiltin(name string) error
	// IsBuiltin reports whether name is a map shipped with the server.
	IsBuiltin(name string) bool
	// VerifyEditToken reports whether token authorizes modifying a custom map.
	VerifyEditToken(name, token string) error
}

// MapLifecycle is the draft/ownership map API the manager exposes in addition
// to ConfigManager. It is separate so the legacy ConfigManager (and its test
// mocks) stay small; production always provides it.
type MapLifecycle interface {
	ListEntries(viewerID string) ([]*MapEntry, error)
	// GetEntry returns any catalogue entry, drafts included.
	GetEntry(name string) (*MapEntry, error)
	CreateDraft(name string, config *engine.GameConfig, ownerID string) (string, error)
	SaveOwned(name string, config *engine.GameConfig, actor MapActor) (*MapEntry, error)
	Publish(name string, actor MapActor) (*MapEntry, error)
	DeleteOwned(name string, actor MapActor) error
	Claim(name, ownerID, token string) (*MapEntry, error)
}

// Session represents an active game session
type Session struct {
	ID          string
	DisplayName string
	// OwnerAccountID is the account that created the session ("" = guest).
	// It keeps the session private when its map is deleted.
	OwnerAccountID string
	Engine         *engine.GameEngine
	Config         *engine.GameConfig
	CreatedAt      time.Time
	LastActionAt   time.Time
	FogEnabled     bool
	FogRadius      int
	GridPassword   string
	MoveDelayMs    int
}
