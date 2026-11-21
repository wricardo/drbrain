package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// pgManager builds a Manager whose shipped maps are files in a temp dir and
// whose player maps are in PostgreSQL.
func pgManager(t *testing.T, max int) (*Manager, string) {
	t.Helper()
	return pgManagerOn(t, pgtest.Pool(t), max)
}

func pgManagerOn(t *testing.T, pool *pgxpool.Pool, max int) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	writeConfigFile(t, dir, "classic", createValidConfig())
	m, err := NewManager(Options{ConfigDir: dir, Pool: pool, MaxUserMaps: max})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, dir
}

// unwinnableConfig is structurally valid (home, park, legend, bounds) but the
// park is walled in, so the publication proof can never reach it.
func unwinnableConfig() *engine.GameConfig {
	cfg := createValidConfig()
	cfg.Name = "Trapped park"
	cfg.MaxBattery = 10
	cfg.StartingBattery = 10
	cfg.Layout = []string{
		"BBBBB",
		"BHBBB",
		"BBPBB",
		"BBBBB",
		"BBBBB",
	}
	return cfg
}

func TestPGDraftCreateReloadAndStructuralSave(t *testing.T) {
	pool := pgtest.Pool(t)
	m, dir := pgManagerOn(t, pool, 0)

	cfg := createValidConfig()
	cfg.Name = "mine"
	token, err := m.CreateDraft("mine", cfg, "owner-a")
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if token == "" {
		t.Fatal("CreateDraft returned no edit token")
	}

	// A fresh manager (same pool, same shipped dir) sees the durable row.
	m2, err := NewManager(Options{ConfigDir: dir, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := m2.GetEntry("mine")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.Builtin || entry.OwnerID != "owner-a" || entry.Status != service.MapStatusDraft {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Config.Name != "mine" {
		t.Errorf("reloaded name = %q", entry.Config.Name)
	}

	// Drafts only need to be structurally sound: winnability is the
	// publication gate.
	if _, err := m2.CreateDraft("trapped", unwinnableConfig(), "owner-a"); err != nil {
		t.Fatalf("structurally valid draft rejected: %v", err)
	}
	if _, err := m2.GetEntry("trapped"); err != nil {
		t.Fatalf("draft with an unreachable park must load: %v", err)
	}

	// A structurally broken draft is refused.
	broken := createValidConfig()
	broken.GridSize = 2
	if _, err := m2.CreateDraft("broken", broken, "owner-a"); !errors.Is(err, service.ErrMapInvalid) {
		t.Fatalf("structurally invalid draft = %v, want service.ErrMapInvalid", err)
	}
}

func TestPGPublishGate(t *testing.T) {
	m, _ := pgManager(t, 0)

	if _, err := m.CreateDraft("trapped", unwinnableConfig(), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("trapped", service.MapActor{AccountID: "owner-a"}); !errors.Is(err, service.ErrMapInvalid) {
		t.Fatalf("publishing an unwinnable map = %v, want ErrInvalidConfig", err)
	}

	if _, err := m.CreateDraft("good", createValidConfig(), "owner-a"); err != nil {
		t.Fatal(err)
	}
	entry, err := m.Publish("good", service.MapActor{AccountID: "owner-a"})
	if err != nil {
		t.Fatalf("publishing a winnable map: %v", err)
	}
	if entry.Status != service.MapStatusPublished {
		t.Fatalf("status = %q, want PUBLISHED", entry.Status)
	}
	// Publishing again is a no-op for the owner.
	if _, err := m.Publish("good", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Fatalf("republish: %v", err)
	}
}

func TestPGOwnerGates(t *testing.T) {
	m, _ := pgManager(t, 0)
	if _, err := m.CreateDraft("good", createValidConfig(), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("good", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}

	other := service.MapActor{AccountID: "owner-b"}
	if _, err := m.SaveOwned("good", createValidConfig(), other); !errors.Is(err, service.ErrMapForbidden) {
		t.Errorf("other owner save = %v, want ErrMapForbidden", err)
	}
	if _, err := m.Publish("good", other); !errors.Is(err, service.ErrMapForbidden) {
		t.Errorf("other owner publish = %v, want ErrMapForbidden", err)
	}
	if err := m.DeleteOwned("good", other); !errors.Is(err, service.ErrMapForbidden) {
		t.Errorf("other owner delete = %v, want ErrMapForbidden", err)
	}

	// The owner editing a published map puts it back into review.
	updated := createValidConfig()
	updated.Name = "renamed"
	entry, err := m.SaveOwned("good", updated, service.MapActor{AccountID: "owner-a"})
	if err != nil {
		t.Fatalf("owner save: %v", err)
	}
	if entry.Status != service.MapStatusDraft {
		t.Errorf("owner save status = %q, want DRAFT", entry.Status)
	}

	// Shipped maps stay read-only for players; admins may edit them.
	if _, err := m.SaveOwned("classic", createValidConfig(), service.MapActor{AccountID: "owner-a"}); !errors.Is(err, service.ErrMapReadOnly) {
		t.Errorf("player builtin save = %v, want ErrReadOnlyMap", err)
	}
	if _, err := m.SaveOwned("classic", createValidConfig(), service.MapActor{Admin: true}); err != nil {
		t.Errorf("admin builtin save: %v", err)
	}
}

func TestPGQuota(t *testing.T) {
	m, _ := pgManager(t, 2)
	cfg := createValidConfig()
	if _, err := m.CreateDraft("one", cfg, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateDraft("two", cfg, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateDraft("three", cfg, "o"); !errors.Is(err, service.ErrMapLimit) {
		t.Fatalf("third draft = %v, want ErrMapLimit", err)
	}
}

func TestPGLegacyImportClaimAndTokenHash(t *testing.T) {
	pool := pgtest.Pool(t)
	m, _ := pgManagerOn(t, pool, 0)

	// The legacy wrapper creates an ownerless PUBLISHED row, like an import.
	token, err := m.CreateCustom("legacy", createValidConfig())
	if err != nil {
		t.Fatalf("CreateCustom: %v", err)
	}
	if token == "" {
		t.Fatal("legacy map has no edit token")
	}
	entry, err := m.GetEntry("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if entry.OwnerID != "" || entry.Status != service.MapStatusPublished {
		t.Fatalf("legacy entry = %+v", entry)
	}

	// The raw column holds the SHA-256 hash, never the token.
	var hash string
	if err := pool.QueryRow(t.Context(),
		`SELECT edit_token_hash FROM platform_maps WHERE game = 'tesla' AND id = 'legacy'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == token || len(hash) != 64 {
		t.Fatalf("stored token hash %q is not a SHA-256 of the token", hash)
	}
	if err := m.VerifyEditToken("legacy", token); err != nil {
		t.Errorf("VerifyEditToken with the issued token: %v", err)
	}
	if err := m.VerifyEditToken("legacy", "wrong"); !errors.Is(err, ErrInvalidEditToken) {
		t.Errorf("VerifyEditToken with a wrong token = %v, want ErrInvalidEditToken", err)
	}

	// An ownerless row cannot be edited without admin, but its token claims it.
	if err := m.SaveConfig("legacy", createValidConfig()); !errors.Is(err, ErrMapForbidden) {
		t.Errorf("editing an ownerless row without admin = %v, want ErrMapForbidden", err)
	}
	if _, err := m.Claim("legacy", "owner-a", "wrong"); !errors.Is(err, service.ErrInvalidEditToken) {
		t.Errorf("claim with a wrong token = %v, want service.ErrInvalidEditToken", err)
	}
	claimed, err := m.Claim("legacy", "owner-a", token)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.OwnerID != "owner-a" {
		t.Fatalf("claimed owner = %q", claimed.OwnerID)
	}
	// Claiming again as the same owner is a no-op; another owner is refused.
	if _, err := m.Claim("legacy", "owner-a", token); err != nil {
		t.Errorf("reclaim by the owner: %v", err)
	}
	if _, err := m.Claim("legacy", "owner-b", token); !errors.Is(err, service.ErrMapForbidden) {
		t.Errorf("claim by another owner = %v, want ErrMapForbidden", err)
	}
}

func TestPGAccountDeletionHooks(t *testing.T) {
	m, _ := pgManager(t, 0)

	if _, err := m.CreateDraft("draft-a", createValidConfig(), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateDraft("pub-a", createValidConfig(), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("pub-a", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateDraft("pub-b", createValidConfig(), "owner-b"); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteDraftsByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.OrphanPublishedByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetEntry("draft-a"); !errors.Is(err, service.ErrMapNotFound) {
		t.Errorf("draft after account deletion = %v, want ErrConfigNotFound", err)
	}
	orphaned, err := m.GetEntry("pub-a")
	if err != nil {
		t.Fatalf("published map must survive: %v", err)
	}
	if orphaned.OwnerID != "" || orphaned.Status != service.MapStatusPublished {
		t.Errorf("orphaned entry = %+v", orphaned)
	}
	if kept, err := m.GetEntry("pub-b"); err != nil || kept.OwnerID != "owner-b" {
		t.Errorf("other owner's map changed: %+v, %v", kept, err)
	}
}

// No custom files are written in PostgreSQL mode: the shipped directory keeps
// exactly the map that was there at startup.
func TestPGWritesNoFiles(t *testing.T) {
	m, dir := pgManager(t, 0)
	if _, err := m.CreateDraft("mine", createValidConfig(), "owner-a"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("shipped dir after draft = %v", entries)
	}
}
