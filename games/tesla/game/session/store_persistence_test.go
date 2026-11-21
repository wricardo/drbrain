package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/tesla/game/config"
	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// newMemoryPersistence returns a StorePersistence over an in-memory store for
// tests that exercise manager or rehydration logic but not durability.
func newMemoryPersistence(t *testing.T, configManager service.ConfigManager) *StorePersistence {
	t.Helper()
	return newStorePersistence(sessionstore.NewMemory(sessionstore.Options{}), configManager)
}

func TestStorePersistence(t *testing.T) {
	pool := pgtest.Pool(t)

	configManager, err := config.NewManager(config.Options{ConfigDir: "../../maps", CustomDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	persistence, err := NewPostgresPersistence(pool, configManager, 0)
	if err != nil {
		t.Fatalf("Failed to create store persistence: %v", err)
	}

	gameConfig := configManager.GetDefault()
	gameEngine, err := engine.NewEngine(gameConfig)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	session := &service.Session{
		ID:           "test1",
		Engine:       gameEngine,
		Config:       gameConfig,
		CreatedAt:    time.Now(),
		LastActionAt: time.Now(),
		FogEnabled:   true,
		FogRadius:    2,
		GridPassword: "secret",
		MoveDelayMs:  300,
	}

	t.Run("Save and Load Session", func(t *testing.T) {
		if err := persistence.Save(session); err != nil {
			t.Fatalf("Failed to save session: %v", err)
		}

		if !persistence.Exists("test1") {
			t.Error("Session should exist after save")
		}

		loadedSession, err := persistence.Load("test1")
		if err != nil {
			t.Fatalf("Failed to load session: %v", err)
		}

		if loadedSession.ID != session.ID {
			t.Errorf("Expected ID %s, got %s", session.ID, loadedSession.ID)
		}
		if loadedSession.Config.Name != session.Config.Name {
			t.Errorf("Expected config name %s, got %s", session.Config.Name, loadedSession.Config.Name)
		}
		if loadedSession.Engine.GetState().Battery != session.Engine.GetState().Battery {
			t.Errorf("Expected battery %d, got %d", session.Engine.GetState().Battery, loadedSession.Engine.GetState().Battery)
		}
		if !loadedSession.FogEnabled {
			t.Errorf("Expected fog enabled to persist")
		}
		if loadedSession.FogRadius != 2 {
			t.Errorf("Expected fog radius 2, got %d", loadedSession.FogRadius)
		}
		if loadedSession.GridPassword != "secret" {
			t.Errorf("Expected grid password to persist")
		}
		if loadedSession.MoveDelayMs != 300 {
			t.Errorf("Expected move delay 300, got %d", loadedSession.MoveDelayMs)
		}
	})

	t.Run("Survives a restart", func(t *testing.T) {
		// A second persistence over the same database sees sessions written
		// by the process that saved them.
		restarted, err := NewPostgresPersistence(pool, configManager, 0)
		if err != nil {
			t.Fatalf("Failed to reopen store persistence: %v", err)
		}
		loadedSession, err := restarted.Load("test1")
		if err != nil {
			t.Fatalf("Failed to load session after restart: %v", err)
		}
		if loadedSession.ID != "test1" {
			t.Errorf("Expected ID test1, got %s", loadedSession.ID)
		}
	})

	t.Run("Save State Changes", func(t *testing.T) {
		if !session.Engine.Move("right") {
			t.Skip("Cannot test state persistence without successful move")
		}

		if err := persistence.Save(session); err != nil {
			t.Fatalf("Failed to save updated session: %v", err)
		}

		loadedSession, err := persistence.Load("test1")
		if err != nil {
			t.Fatalf("Failed to load updated session: %v", err)
		}

		if loadedSession.Engine.GetState().PlayerPos != session.Engine.GetState().PlayerPos {
			t.Errorf("Player position not persisted correctly")
		}
		if len(loadedSession.Engine.GetMoveHistory()) != len(session.Engine.GetMoveHistory()) {
			t.Errorf("Move history not persisted correctly")
		}
	})

	t.Run("List All Sessions", func(t *testing.T) {
		session2 := &service.Session{
			ID:           "test2",
			Engine:       gameEngine,
			Config:       gameConfig,
			CreatedAt:    time.Now(),
			LastActionAt: time.Now(),
		}
		if err := persistence.Save(session2); err != nil {
			t.Fatalf("Failed to save second session: %v", err)
		}

		sessionIDs, err := persistence.ListAll()
		if err != nil {
			t.Fatalf("Failed to list sessions: %v", err)
		}

		if len(sessionIDs) < 2 {
			t.Errorf("Expected at least 2 sessions, got %d", len(sessionIDs))
		}

		found := make(map[string]bool)
		for _, id := range sessionIDs {
			found[id] = true
		}
		if !found["test1"] || !found["test2"] {
			t.Error("Expected sessions not found in list")
		}
	})

	t.Run("Delete Session", func(t *testing.T) {
		if err := persistence.Delete("test2"); err != nil {
			t.Fatalf("Failed to delete session: %v", err)
		}

		if persistence.Exists("test2") {
			t.Error("Session should not exist after delete")
		}

		if _, err := persistence.Load("test2"); err == nil {
			t.Error("Should not be able to load deleted session")
		}
	})

	t.Run("Error Cases", func(t *testing.T) {
		if _, err := persistence.Load("nonexistent"); err == nil {
			t.Error("Should get error when loading non-existent session")
		}

		if err := persistence.Delete("nonexistent"); err == nil {
			t.Error("Should get error when deleting non-existent session")
		}

		if err := persistence.Save(nil); err == nil {
			t.Error("Should get error when saving nil session")
		}
	})
}

// A session owner is durable, and a save written before ownership existed
// loads with an empty owner (hidden if its map is later deleted).
func TestStorePersistenceOwnerAndLegacySave(t *testing.T) {
	configManager, err := config.NewManager(config.Options{ConfigDir: "../../maps", CustomDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	fp := newMemoryPersistence(t, configManager)
	cfg := configManager.GetDefault()
	eng, err := engine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	owned := &service.Session{
		ID: "owned", Engine: eng, Config: cfg, CreatedAt: now, LastActionAt: now,
		OwnerAccountID: "acct-1", MoveDelayMs: service.DefaultMoveDelayMs,
	}
	if err := fp.Save(owned); err != nil {
		t.Fatal(err)
	}
	loaded, err := fp.Load("owned")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.OwnerAccountID != "acct-1" {
		t.Fatalf("loaded owner = %q, want acct-1", loaded.OwnerAccountID)
	}

	// A pre-ownership save: no owner_account_id key at all.
	legacyJSON, err := json.Marshal(PersistedSessionData{
		ID: "legacy", MapName: "classic", CreatedAt: now, LastActionAt: now,
		GameState: eng.GetState(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fp.store.Persist(sessionstore.Meta{
		ID: "legacy", MapID: "classic", CreatedAt: now, LastActionAt: now,
	}, legacyJSON); err != nil {
		t.Fatal(err)
	}
	legacy, err := fp.Load("legacy")
	if err != nil {
		t.Fatalf("legacy save must still load: %v", err)
	}
	if legacy.OwnerAccountID != "" {
		t.Fatalf("legacy owner = %q, want empty", legacy.OwnerAccountID)
	}
}
