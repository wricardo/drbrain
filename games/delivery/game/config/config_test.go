package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/mapstore"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func testCity(name string, withDepot bool) *engine.CityConfig {
	cfg := &engine.CityConfig{
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
	if withDepot {
		cfg.Depots = []engine.DepotConfig{
			{ID: "home", Pos: engine.Position{X: 2, Y: 2}},
		}
	}
	return cfg
}

func writeBuiltin(t *testing.T, dir, id string, cfg *engine.CityConfig) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(cityJSON(t, cfg)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cityJSON marshals a complete city document: DecodeCityConfig rejects rows
// missing any required knob, so tests must not hand-roll partial payloads.
func cityJSON(t *testing.T, cfg *engine.CityConfig) string {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func openCatalog(t *testing.T, dir, defaultID string, pool *pgxpool.Pool, max int) *Manager {
	t.Helper()
	m, err := Open(Options{
		BuiltinDir:    dir,
		DefaultCityID: defaultID,
		Pool:          pool,
		MaxUserCities: max,
	})
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	return m
}

func TestBuiltinListAndDefault(t *testing.T) {
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	writeBuiltin(t, dir, "alpha", testCity("Alpha", true))

	m := openCatalog(t, dir, "city", nil, 0)
	if m.DefaultID() != "city" {
		t.Fatalf("default = %q, want city", m.DefaultID())
	}
	list, err := m.ListFor("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "city" {
		t.Fatalf("list = %+v", list)
	}
	for _, e := range list {
		if !e.Builtin || e.Status != mapstore.StatusPublished || e.OwnerID != "" {
			t.Errorf("builtin %q = %+v", e.ID, e)
		}
	}
	entry, err := m.Get("city", "")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "Default City" || entry.Config.GridSize != 5 {
		t.Fatalf("get city = %+v", entry)
	}
	if _, err := m.Get("Alpha", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uppercase id accepted: %v", err)
	}
	if _, err := m.SaveOwned("city", testCity("Edit", true), Actor{}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("builtin save = %v, want ErrReadOnly", err)
	}
	if _, err := m.Publish("city", Actor{}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("builtin publish = %v, want ErrReadOnly", err)
	}
	if err := m.DeleteOwned("city", Actor{}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("builtin delete = %v, want ErrReadOnly", err)
	}
}

func TestInvalidBuiltinIsFatalAndNamesItsFile(t *testing.T) {
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte(`{"name":"broken","layout":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Options{BuiltinDir: dir, DefaultCityID: "city"})
	if err == nil {
		t.Fatal("corrupt builtin was accepted")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("error %q does not name the file", err)
	}
}

func TestInvalidBuiltinIDMismatchIsFatal(t *testing.T) {
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	writeBuiltin(t, dir, "Bad Name", testCity("Bad", true))
	_, err := Open(Options{BuiltinDir: dir, DefaultCityID: "city"})
	if err == nil {
		t.Fatal("invalid file-base id was accepted")
	}
	if !strings.Contains(err.Error(), "Bad Name.json") {
		t.Fatalf("error %q does not name the file", err)
	}
}

func TestMissingDefaultIsFatal(t *testing.T) {
	dir := t.TempDir()
	writeBuiltin(t, dir, "alpha", testCity("Alpha", true))
	_, err := Open(Options{BuiltinDir: dir, DefaultCityID: "city"})
	if err == nil || !strings.Contains(err.Error(), `default city "city"`) {
		t.Fatalf("error = %v, want a missing-default message", err)
	}
}

func TestCustomDraftLifecycleAndVisibility(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	m := openCatalog(t, dir, "city", pool, 0)

	if err := m.CreateDraft("mine", testCity("Mine", true), "owner-a"); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get("mine", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Builtin || got.OwnerID != "owner-a" || got.Status != mapstore.StatusDraft {
		t.Fatalf("draft = %+v", got)
	}
	if _, err := m.Get("mine", "owner-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another player saw the draft: %v", err)
	}
	other, err := m.ListFor("owner-b")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range other {
		if e.ID == "mine" {
			t.Fatal("another player's draft was listed")
		}
	}
	own, err := m.ListFor("owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(own, "mine") {
		t.Fatal("owner's own draft was not listed")
	}

	// A second catalogue over the same pool is the restart path.
	reloaded := openCatalog(t, dir, "city", pool, 0)
	if got, err := reloaded.Get("mine", "owner-a"); err != nil || got.Name != "Mine" {
		t.Fatalf("reload = %+v, %v", got, err)
	}

	if _, err := m.SaveOwned("mine", testCity("Mine v2", true), Actor{AccountID: "owner-b"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner save = %v", err)
	}
	if got, err := m.SaveOwned("mine", testCity("Mine v2", true), Actor{AccountID: "owner-a"}); err != nil || got.Status != mapstore.StatusDraft {
		t.Fatalf("owner save = %+v, %v", got, err)
	}
	if err := m.CreateDraft("city", testCity("Shadow", true), "owner-a"); !errors.Is(err, ErrExists) {
		t.Fatalf("builtin shadow = %v", err)
	}
}

func TestStructuralSaveAllowsNonPublishableDraft(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	m := openCatalog(t, dir, "city", pool, 0)

	if err := m.CreateDraft("draft", testCity("No Depot", false), "owner-a"); err != nil {
		t.Fatalf("structural draft save refused: %v", err)
	}
	// Community edits are structural too; only shipping (publish) requires a
	// fully simulatable city.
	if _, err := m.SaveOwned("draft", testCity("Still No Depot", false), Actor{AccountID: "owner-a"}); err != nil {
		t.Fatalf("structural owner save refused: %v", err)
	}
	if _, err := m.Publish("draft", Actor{AccountID: "owner-a"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("publish of an unplayable city = %v, want ErrInvalid", err)
	}
	if _, err := m.SaveOwned("draft", testCity("Fixed", true), Actor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Publish("draft", Actor{AccountID: "owner-a"})
	if err != nil {
		t.Fatalf("publish fixed city: %v", err)
	}
	if got.Status != mapstore.StatusPublished {
		t.Fatalf("status = %q", got.Status)
	}
	if _, err := m.Get("draft", "someone-else"); err != nil {
		t.Fatalf("published city is not visible: %v", err)
	}
}

func TestOwnerDeleteAndAccountHook(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	m := openCatalog(t, dir, "city", pool, 0)

	if err := m.CreateDraft("gone", testCity("Gone", true), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteOwned("gone", Actor{AccountID: "owner-b"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner delete = %v", err)
	}
	if err := m.DeleteOwned("gone", Actor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("gone", "owner-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted city still found: %v", err)
	}

	// The account hook drops drafts and orphans published rows.
	if err := m.CreateDraft("draft", testCity("Draft", true), "owner-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveOwned("draft", testCity("Published", true), Actor{AccountID: "owner-b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("draft", Actor{AccountID: "owner-b"}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateDraft("private", testCity("Private", true), "owner-b"); err != nil {
		t.Fatal(err)
	}
	if err := m.OnAccountDelete("owner-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("private", "owner-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft survived account deletion: %v", err)
	}
	orphan, err := m.Get("draft", "")
	if err != nil {
		t.Fatalf("published city was removed: %v", err)
	}
	if orphan.OwnerID != "" {
		t.Fatalf("orphan owner = %q, want empty", orphan.OwnerID)
	}
}

func TestQuota(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	m := openCatalog(t, dir, "city", pool, 1)

	if err := m.CreateDraft("one", testCity("One", true), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateDraft("two", testCity("Two", true), "owner-a"); !errors.Is(err, ErrLimit) {
		t.Fatalf("second city = %v, want ErrLimit", err)
	}
}

func TestCorruptCustomRowIsSkipped(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	insertRow(t, pool, "corrupt", `{"name":"corrupt","bogus":1}`, "owner-a", "DRAFT")
	insertRow(t, pool, "good", cityJSON(t, testCity("Good", true)), "owner-a", "PUBLISHED")

	m := openCatalog(t, dir, "city", pool, 0)
	list, err := m.ListFor("")
	if err != nil {
		t.Fatal(err)
	}
	if hasEntry(list, "corrupt") {
		t.Fatal("corrupt row was listed")
	}
	if !hasEntry(list, "good") {
		t.Fatal("valid custom row was not listed")
	}
	if _, err := m.Get("corrupt", "owner-a"); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt row get = %v", err)
	}
}

func TestCustomRowCannotShadowBuiltin(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	raw := cityJSON(t, testCity("Shadow", true))
	insertRow(t, pool, "city", raw, "", "PUBLISHED")

	m := openCatalog(t, dir, "city", pool, 0)
	got, err := m.Get("city", "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Builtin || got.Name != "Default City" {
		t.Fatalf("custom row shadowed the builtin: %+v", got)
	}
	list, err := m.ListFor("")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range list {
		if e.ID == "city" {
			n++
			if !e.Builtin {
				t.Fatal("list returned the custom shadow")
			}
		}
	}
	if n != 1 {
		t.Fatalf("city listed %d times", n)
	}
}

func TestAdminBuiltinSaveAndDelete(t *testing.T) {
	pool := pgtest.Pool(t)
	dir := t.TempDir()
	writeBuiltin(t, dir, "city", testCity("Default City", true))
	writeBuiltin(t, dir, "alpha", testCity("Alpha", true))
	m := openCatalog(t, dir, "city", pool, 0)

	if _, err := m.SaveOwned("alpha", testCity("Nope", true), Actor{AccountID: "owner-a"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("non-admin save = %v, want ErrReadOnly", err)
	}
	if err := m.DeleteOwned("alpha", Actor{AccountID: "owner-a"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("non-admin delete = %v, want ErrReadOnly", err)
	}

	// A builtin must stay fully valid: the structural community gate is not
	// enough to overwrite a shipped city.
	if _, err := m.SaveOwned("alpha", testCity("Unplayable", false), Actor{Admin: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("admin save of an unplayable builtin = %v, want ErrInvalid", err)
	}
	if got, err := m.Get("alpha", ""); err != nil || got.Name != "Alpha" {
		t.Fatalf("failed save changed the builtin: %+v, %v", got, err)
	}

	entry, err := m.SaveOwned("alpha", testCity("Alpha Edited", true), Actor{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if !entry.Builtin || entry.Name != "Alpha Edited" {
		t.Fatalf("admin save entry = %+v", entry)
	}
	got, err := m.Get("alpha", "")
	if err != nil || got.Name != "Alpha Edited" {
		t.Fatalf("Get after admin save = %+v, %v", got, err)
	}
	list, err := m.ListFor("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list {
		if e.ID == "alpha" {
			found = true
			if !e.Builtin || e.Name != "Alpha Edited" {
				t.Fatalf("ListFor after admin save = %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("alpha disappeared from ListFor after admin save")
	}
	// The refresh is not just in memory: a fresh catalogue reads the new file.
	reopened := openCatalog(t, dir, "city", pool, 0)
	if got, err := reopened.Get("alpha", ""); err != nil || got.Name != "Alpha Edited" {
		t.Fatalf("reopened catalog alpha = %+v, %v", got, err)
	}

	// Saving the default builtin keeps the configured default resolvable.
	if _, err := m.SaveOwned("city", testCity("Default Edited", true), Actor{Admin: true}); err != nil {
		t.Fatal(err)
	}
	if m.DefaultID() != "city" {
		t.Fatalf("default id = %q", m.DefaultID())
	}
	if got, err := m.Get("city", ""); err != nil || got.Name != "Default Edited" {
		t.Fatalf("Get default after admin save = %+v, %v", got, err)
	}

	// Removing the default would break every implicit city selection.
	if err := m.DeleteOwned("city", Actor{Admin: true}); !errors.Is(err, ErrDefaultCity) {
		t.Fatalf("delete default = %v, want ErrDefaultCity", err)
	}
	if _, err := m.Get("city", ""); err != nil {
		t.Fatalf("rejected default delete removed the city: %v", err)
	}

	if err := m.DeleteOwned("alpha", Actor{Admin: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("alpha", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin delete left alpha behind: %v", err)
	}
	list, err = m.ListFor("")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if e.ID == "alpha" {
			t.Fatal("alpha still listed after admin delete")
		}
	}
	reopened = openCatalog(t, dir, "city", pool, 0)
	if _, err := reopened.Get("alpha", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reopened catalog still has alpha: %v", err)
	}
	if got, err := reopened.Get("city", ""); err != nil || got.Name != "Default Edited" {
		t.Fatalf("reopened catalog default = %+v, %v", got, err)
	}
}

func insertRow(t *testing.T, pool *pgxpool.Pool, id, config, owner, status string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
INSERT INTO platform_maps (game, id, config, owner_id, status)
VALUES ('delivery', $1, $2::jsonb, $3, $4)`, id, config, owner, status)
	if err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func hasEntry(list []Entry, id string) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
}
