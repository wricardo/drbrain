package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// StorePersistence adapts platform/sessionstore to this game. The store owns
// durability, limits and listing; tesla keeps its JSON shape and map rehydration.
type StorePersistence struct {
	store         sessionstore.Store
	configManager service.ConfigManager
}

// NewPostgresPersistence opens the PostgreSQL-backed store and keeps
// configManager for rehydrating the engine on Load.
func NewPostgresPersistence(pool *pgxpool.Pool, configManager service.ConfigManager, maxSessions int) (*StorePersistence, error) {
	store, err := sessionstore.Open(sessionstore.Options{Pool: pool, Game: "tesla", MaxSessions: maxSessions})
	if err != nil {
		return nil, err
	}
	return newStorePersistence(store, configManager), nil
}

// newStorePersistence wraps an already-open store. Production uses
// NewPostgresPersistence; tests can pass sessionstore.NewMemory.
func newStorePersistence(store sessionstore.Store, configManager service.ConfigManager) *StorePersistence {
	return &StorePersistence{store: store, configManager: configManager}
}

// Save persists a session to PostgreSQL.
func (fp *StorePersistence) Save(session *service.Session) error {
	if session == nil {
		return fmt.Errorf("session cannot be nil")
	}
	if err := validateSessionID(session.ID); err != nil {
		return err
	}

	// Get config ID from display name
	configID, err := fp.getConfigIDFromName(session.Config.Name)
	if err != nil {
		return fmt.Errorf("failed to get config ID: %w", err)
	}

	data := PersistedSessionData{
		ID:             session.ID,
		DisplayName:    session.DisplayName,
		MapName:        configID,
		CreatedAt:      session.CreatedAt,
		LastActionAt:   session.LastActionAt,
		OwnerAccountID: session.OwnerAccountID,
		FogEnabled:     session.FogEnabled,
		FogRadius:      session.FogRadius,
		GridPassword:   session.GridPassword,
		MoveDelayMs:    intPtr(session.MoveDelayMs),
		GameState:      session.Engine.GetState(),
	}

	// Marshal to JSON with indentation for readability
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	return fp.store.Persist(sessionstore.Meta{
		ID: session.ID, DisplayName: session.DisplayName, MapID: configID,
		CreatedAt: session.CreatedAt, LastActionAt: session.LastActionAt,
	}, jsonData)
}

// Load retrieves a session from PostgreSQL.
func (fp *StorePersistence) Load(id string) (*service.Session, error) {
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	_, jsonData, err := fp.store.Load(id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to read session: %w", err)
	}

	// Unmarshal JSON
	var data PersistedSessionData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session data: %w", err)
	}

	gameConfig, err := fp.configManager.LoadConfig(data.MapName)
	if err != nil {
		return nil, fmt.Errorf("failed to load map '%s': %w", data.MapName, err)
	}

	// Create the engine without re-proving winnability: the map was validated
	// when it was saved, and the proof is too expensive to repeat per session.
	gameEngine, err := engine.NewEngineForLoad(gameConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create game engine: %w", err)
	}

	// Restore game state
	gameStateJSON, err := json.Marshal(data.GameState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal game state: %w", err)
	}

	var gameState engine.GameState
	if err := json.Unmarshal(gameStateJSON, &gameState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal game state: %w", err)
	}

	// Set the restored state to the engine
	if err := gameEngine.SetState(&gameState); err != nil {
		return nil, fmt.Errorf("failed to set game state: %w", err)
	}

	// Create session
	fogRadius := data.FogRadius
	if fogRadius <= 0 {
		fogRadius = 1
	}

	lastActionAt := data.LastActionAt
	if lastActionAt.IsZero() {
		lastActionAt = data.LastAccessedAtLegacy
	}
	if lastActionAt.IsZero() {
		lastActionAt = data.CreatedAt
	}

	session := &service.Session{
		ID:             data.ID,
		DisplayName:    data.DisplayName,
		Engine:         gameEngine,
		Config:         gameConfig,
		CreatedAt:      data.CreatedAt,
		LastActionAt:   lastActionAt,
		OwnerAccountID: data.OwnerAccountID,
		FogEnabled:     data.FogEnabled,
		FogRadius:      fogRadius,
		GridPassword:   data.GridPassword,
		MoveDelayMs:    loadMoveDelay(data.MoveDelayMs, data.BulkMoveDelayMs),
	}

	return session, nil
}

// Delete removes a stored session.
func (fp *StorePersistence) Delete(id string) error {
	if err := validateSessionID(id); err != nil {
		return err
	}
	if err := fp.store.Delete(id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("failed to remove session: %w", err)
	}
	return nil
}

// ListAll returns all persisted session IDs.
func (fp *StorePersistence) ListAll() ([]string, error) {
	metas, err := fp.store.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	var sessionIDs []string
	for _, m := range metas {
		if validateSessionID(m.ID) == nil {
			sessionIDs = append(sessionIDs, m.ID)
		}
	}
	return sessionIDs, nil
}

// Exists checks if a session is stored.
func (fp *StorePersistence) Exists(id string) bool {
	if err := validateSessionID(id); err != nil {
		return false
	}
	metas, err := fp.store.List()
	if err != nil {
		return false
	}
	for _, m := range metas {
		if m.ID == id {
			return true
		}
	}
	return false
}

// getConfigIDFromName returns the map ID (filename without extension) from display name
func (fp *StorePersistence) getConfigIDFromName(displayName string) (string, error) {
	maps, err := fp.configManager.ListConfigs()
	if err != nil {
		return "", fmt.Errorf("failed to list maps: %w", err)
	}

	for _, m := range maps {
		if m.Name == displayName {
			return m.MapID, nil
		}
	}

	// If not found, assume displayName is already the map ID
	return displayName, nil
}

func intPtr(v int) *int {
	return &v
}

func loadMoveDelay(primary *int, legacy *int) int {
	v := primary
	if v == nil {
		v = legacy
	}
	if v == nil {
		return service.DefaultMoveDelayMs
	}
	if *v < 0 {
		return 0
	}
	return *v
}
