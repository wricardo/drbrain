package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// StorePersistence adapts platform/sessionstore to this game. The store owns
// files, limits and listing; tesla keeps its JSON shape and map rehydration.
type StorePersistence struct {
	store         sessionstore.Store
	configManager service.ConfigManager
}

// FilePersistence is the pre-monorepo name, kept so existing call sites and
// tests keep compiling.
type FilePersistence = StorePersistence

// NewStorePersistence opens the file-backed store and keeps configManager for
// rehydrating the engine on Load.
func NewStorePersistence(sessionsDir string, configManager service.ConfigManager, maxSessions int) (*StorePersistence, error) {
	store, err := sessionstore.Open(sessionstore.Options{Dir: sessionsDir, MaxSessions: maxSessions, Summary: summarize})
	if err != nil {
		return nil, err
	}
	return &StorePersistence{store: store, configManager: configManager}, nil
}

// NewFilePersistence keeps the old constructor signature.
func NewFilePersistence(sessionsDir string, configManager service.ConfigManager) (*StorePersistence, error) {
	return NewStorePersistence(sessionsDir, configManager, 0)
}

// summarize derives the platform Meta from a saved tesla payload.
func summarize(raw []byte) (sessionstore.Meta, error) {
	var data PersistedSessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return sessionstore.Meta{}, err
	}
	last := data.LastActionAt
	if last.IsZero() {
		last = data.CreatedAt
	}
	return sessionstore.Meta{
		ID: data.ID, DisplayName: data.DisplayName, MapID: data.MapName,
		CreatedAt: data.CreatedAt, LastActionAt: last,
	}, nil
}

// Save persists a session to a JSON file
func (fp *FilePersistence) Save(session *service.Session) error {
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
		ID:           session.ID,
		DisplayName:  session.DisplayName,
		MapName:      configID,
		CreatedAt:    session.CreatedAt,
		LastActionAt: session.LastActionAt,
		FogEnabled:   session.FogEnabled,
		FogRadius:    session.FogRadius,
		GridPassword: session.GridPassword,
		MoveDelayMs:  intPtr(session.MoveDelayMs),
		GameState:    session.Engine.GetState(),
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

// Load retrieves a session from a JSON file
func (fp *FilePersistence) Load(id string) (*service.Session, error) {
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	_, jsonData, err := fp.store.Load(id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
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
		ID:           data.ID,
		DisplayName:  data.DisplayName,
		Engine:       gameEngine,
		Config:       gameConfig,
		CreatedAt:    data.CreatedAt,
		LastActionAt: lastActionAt,
		FogEnabled:   data.FogEnabled,
		FogRadius:    fogRadius,
		GridPassword: data.GridPassword,
		MoveDelayMs:  loadMoveDelay(data.MoveDelayMs, data.BulkMoveDelayMs),
	}

	return session, nil
}

// Delete removes a session file
func (fp *FilePersistence) Delete(id string) error {
	if err := validateSessionID(id); err != nil {
		return err
	}
	if err := fp.store.Delete(id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("failed to remove session file: %w", err)
	}
	return nil
}

// ListAll returns all persisted session IDs
func (fp *FilePersistence) ListAll() ([]string, error) {
	metas, err := fp.store.List()
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}
	var sessionIDs []string
	for _, m := range metas {
		if validateSessionID(m.ID) == nil {
			sessionIDs = append(sessionIDs, m.ID)
		}
	}
	return sessionIDs, nil
}

// Exists checks if a session file exists
func (fp *FilePersistence) Exists(id string) bool {
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
func (fp *FilePersistence) getConfigIDFromName(displayName string) (string, error) {
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
