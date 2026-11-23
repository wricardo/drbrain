package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// Account ids are 24 lower-case hex in production; the manager sanitizes
// anything else to the guest id, so tests must use the real shape when they
// mean to be a distinct owner.
const (
	ownerA = "aaaaaaaaaaaaaaaaaaaaaaaa"
	ownerB = "bbbbbbbbbbbbbbbbbbbbbbbb"
)

// multiCityConfig is a minimal playable 5x5 city with a display name. The name
// is the observable snapshot marker in these tests.
func multiCityConfig(name string) *engine.CityConfig {
	return &engine.CityConfig{
		Name:                name,
		GridSize:            5,
		TickMS:              300,
		GroundExtraStepCost: 1,
		TicksPerMonth:       600,
		MaxEvents:           500,
		MaxReputation:       5,
		MaxChapter:          5,
		AutosaveTicks:       60,
		Layout: []string{
			".....",
			"#####",
			"##D##",
			"#####",
			".....",
		},
		Legend: map[string]string{
			".": "road",
			"#": "building",
			"D": "depot",
		},
		Depots: []engine.DepotConfig{
			{ID: "home", Pos: engine.Position{X: 2, Y: 2}},
		},
		Orders: engine.OrdersConfig{
			MaxActiveOrders:          6,
			TargetOrdersPerCustomer:  0.75,
			MinSpawnGapTicks:         12,
			DeadlineSlack:            2,
			RewardBulkBase:           0.6,
			RewardBulkPerBox:         0.4,
			ExpireFineDivisor:        4,
			MinRouteTicks:            5,
			MinDeadlineTicks:         10,
			UnreachableLegTicks:      20,
			LatePayMultiplier:        0.6,
			RampTicks:                2400,
			CustomerCooldownMinScale: 0.5,
		},
		Charging: engine.ChargingConfig{DepotRate: 6, ChargerRate: 2},
		Fleet:    &engine.FleetConfig{UpkeepBoxDivisor: 4, UpkeepPerBoxPerInterval: 1, DefaultUpkeepPerInterval: 1},
		Closures: &engine.ClosureConfig{},
	}
}

func writeCityFile(t *testing.T, dir, id string, cfg *engine.CityConfig) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func openCityCatalog(t *testing.T, dir string, pool *pgxpool.Pool) *cityconfig.Manager {
	t.Helper()
	cat, err := cityconfig.Open(cityconfig.Options{
		BuiltinDir:    dir,
		DefaultCityID: "city",
		Pool:          pool,
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}

func memoryManager(t *testing.T, cat CityCatalog) *Manager {
	t.Helper()
	mgr := &Manager{
		catalog:  cat,
		store:    sessionstore.NewMemory(sessionstore.Options{}),
		sessions: map[string]*Session{},
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func TestBuiltinCitiesCoexist(t *testing.T) {
	dir := t.TempDir()
	writeCityFile(t, dir, "city", multiCityConfig("Default City"))
	writeCityFile(t, dir, "alpha", multiCityConfig("Alpha"))
	mgr := memoryManager(t, openCityCatalog(t, dir, nil))

	alpha, _, err := mgr.Create("alpha-session", 1, "", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	def, _, err := mgr.Create("default-session", 2, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := alpha.Snapshot().City.Name; got != "Alpha" {
		t.Fatalf("alpha city = %q", got)
	}
	if got := def.Snapshot().City.Name; got != "Default City" {
		t.Fatalf("default city = %q", got)
	}
	if got := def.Summary().CityID; got != "city" {
		t.Fatalf("default summary city = %q, want city", got)
	}
	if got := alpha.Summary().CityID; got != "alpha" {
		t.Fatalf("alpha summary city = %q", got)
	}
}

func TestUnknownAndHiddenCityRejectedAtCreate(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeCityFile(t, dir, "city", multiCityConfig("Default City"))
	cat := openCityCatalog(t, dir, pool)
	mgr := memoryManager(t, cat)

	if _, _, err := mgr.Create("nope", 1, "", "missing"); !errors.Is(err, ErrCityNotFound) {
		t.Fatalf("unknown city = %v, want ErrCityNotFound", err)
	}
	if err := cat.CreateDraft("draft", multiCityConfig("Draft"), ownerA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Create("hidden", 1, ownerB, "draft"); !errors.Is(err, ErrCityNotFound) {
		t.Fatalf("other player's draft = %v, want ErrCityNotFound", err)
	}
	if _, _, err := mgr.Create("mine", 1, ownerA, "draft"); err != nil {
		t.Fatalf("owner could not play their draft: %v", err)
	}
}

func TestCustomCitySessionSnapshotAndPersistence(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeCityFile(t, dir, "city", multiCityConfig("Default City"))
	cat := openCityCatalog(t, dir, pool)

	if err := cat.CreateDraft("bravo", multiCityConfig("Bravo"), ownerA); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Publish("bravo", cityconfig.Actor{AccountID: ownerA}); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cat, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	b, _, err := mgr.Create("on-b", 7, ownerA, "bravo")
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := mgr.Create("on-a", 8, ownerA, "city")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Snapshot().City.Name; got != "Bravo" {
		t.Fatalf("b city = %q", got)
	}
	if got := a.Snapshot().City.Name; got != "Default City" {
		t.Fatalf("a city = %q", got)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	var p persisted
	if err := json.Unmarshal(storedPayload(t, mgr, "on-b"), &p); err != nil {
		t.Fatal(err)
	}
	if p.CityID != "bravo" || p.CityFile != "" {
		t.Fatalf("persisted city id/file = %q/%q", p.CityID, p.CityFile)
	}

	mgr.Evict("on-b")
	reloaded, err := mgr.GetOrLoad("on-b")
	if err != nil {
		t.Fatalf("reload b: %v", err)
	}
	if got := reloaded.Snapshot().City.Name; got != "Bravo" {
		t.Fatalf("reloaded b city = %q", got)
	}

	// Editing b changes what a NEW session gets, never a snapshot.
	if _, err := cat.SaveOwned("bravo", multiCityConfig("Bravo Edited"), cityconfig.Actor{AccountID: ownerA}); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Snapshot().City.Name; got != "Bravo" {
		t.Fatalf("live session changed with its city: %q", got)
	}
	// Saving a published city as its owner puts it back in DRAFT and changes
	// future loads: a reload re-reads current content, while a live session
	// keeps its own snapshot. The owner must still see their draft city.
	mgr.Evict("on-b")
	reloaded, err = mgr.GetOrLoad("on-b")
	if err != nil {
		t.Fatalf("owner could not reload a session on their draft city: %v", err)
	}
	if got := reloaded.Snapshot().City.Name; got != "Bravo Edited" {
		t.Fatalf("reloaded draft-city session = %q, want the edited content", got)
	}
	newB, _, err := mgr.Create("new-b", 9, ownerA, "bravo")
	if err != nil {
		t.Fatal(err)
	}
	if got := newB.Snapshot().City.Name; got != "Bravo Edited" {
		t.Fatalf("new session city = %q", got)
	}
	if got := a.Snapshot().City.Name; got != "Default City" {
		t.Fatalf("editing b changed a: %q", got)
	}

	// Deleting b fails a reload with a clear city error, but leaves the live
	// session and a's (builtin) city untouched.
	if err := cat.DeleteOwned("bravo", cityconfig.Actor{AccountID: ownerA}); err != nil {
		t.Fatal(err)
	}
	reloaded.Do(func(w *engine.World) { w.Advance() })
	if got := reloaded.Snapshot().City.Name; got != "Bravo Edited" {
		t.Fatalf("live session broke on city deletion: %q", got)
	}
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("on-b")
	if _, err := mgr.GetOrLoad("on-b"); !errors.Is(err, ErrCityNotFound) {
		t.Fatalf("reload of deleted city = %v, want ErrCityNotFound", err)
	}
	// Listing a save whose city was deleted must still work for a save picker.
	var listed bool
	for _, sum := range mgr.List(ListOptions{}) {
		if sum.ID == "on-b" {
			listed = true
			if sum.CityID != "bravo" {
				t.Fatalf("listed city id = %q, want bravo", sum.CityID)
			}
		}
	}
	if !listed {
		t.Fatal("save with a deleted city disappeared from List")
	}
	if got := a.Snapshot().City.Name; got != "Default City" {
		t.Fatalf("deleting b changed a: %q", got)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("on-a")
	if _, err := mgr.GetOrLoad("on-a"); err != nil {
		t.Fatalf("reload a after b deletion: %v", err)
	}
}

func TestLegacyPayloadWithoutCityIDLoadsDefault(t *testing.T) {
	dir := t.TempDir()
	writeCityFile(t, dir, "city", multiCityConfig("Default City"))
	mgr := memoryManager(t, openCityCatalog(t, dir, nil))

	sess, _, err := mgr.Create("legacy", 3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("legacy")

	rewritePayload(t, mgr, "legacy", func(doc map[string]any) {
		delete(doc, "city_id")
		doc["city_file"] = "city.json"
	})
	reloaded, err := mgr.GetOrLoad("legacy")
	if err != nil {
		t.Fatalf("legacy city_file payload: %v", err)
	}
	if got := reloaded.Summary().CityID; got != "city" {
		t.Fatalf("city id = %q, want city", got)
	}

	// A legacy file base that no longer resolves falls back to the default.
	mgr.Evict("legacy")
	rewritePayload(t, mgr, "legacy", func(doc map[string]any) {
		delete(doc, "city_id")
		doc["city_file"] = "retired.json"
	})
	reloaded, err = mgr.GetOrLoad("legacy")
	if err != nil {
		t.Fatalf("legacy fallback: %v", err)
	}
	if got := reloaded.Snapshot().City.Name; got != "Default City" {
		t.Fatalf("fallback city = %q", got)
	}

	// No city information at all is the oldest shape and means the default.
	mgr.Evict("legacy")
	rewritePayload(t, mgr, "legacy", func(doc map[string]any) {
		delete(doc, "city_id")
		delete(doc, "city_file")
	})
	reloaded, err = mgr.GetOrLoad("legacy")
	if err != nil {
		t.Fatalf("legacy empty city: %v", err)
	}
	if got := reloaded.Summary().CityID; got != "city" {
		t.Fatalf("city id = %q, want city", got)
	}
}
