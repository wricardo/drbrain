package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func player(id string) service.MapActor { return service.MapActor{AccountID: id} }

func admin() service.MapActor { return service.MapActor{Admin: true} }

func cfg(id string) engine.MapConfig {
	return engine.MapConfig{
		ID: id, Name: "T",
		Layout:         []string{"....", ".>.*", "...."},
		MainTapeLength: 4, SubTapeLengths: []int{2},
	}
}

func TestSaveReloadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.Save(cfg("b_map"), player("tester"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save(cfg("a_map"), player("tester")); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m2.Get("b_map")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Config(), saved.Config()) {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got.Config(), saved.Config())
	}
	list := m2.List()
	if len(list) != 2 || list[0].ID != "a_map" || list[1].ID != "b_map" {
		t.Fatalf("List not sorted by id: %v", list)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Fatalf("expected only 2 files (no temp leftovers), got %d", len(ents))
	}
}

func TestGetNotFoundAndDelete(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(Options{Dir: dir})
	var se *service.Error
	if _, err := m.Get("nope"); !errors.As(err, &se) || se.Code != service.CodeNotFound {
		t.Fatalf("want NOT_FOUND, got %v", err)
	}
	if _, err := m.Save(cfg("x"), player("tester")); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("x", player("tester")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.json")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone: %v", err)
	}
	if err := m.Delete("x", player("tester")); !errors.As(err, &se) || se.Code != service.CodeNotFound {
		t.Fatalf("want NOT_FOUND on second delete, got %v", err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "maps")
	m, err := NewManager(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../evil", "a/b", "..", "", "UPPER", strings.Repeat("a", 65)} {
		if _, err := m.Save(cfg(id), player("tester")); err == nil {
			t.Errorf("Save(%q) succeeded", id)
		}
		if err := m.Delete(id, player("tester")); err == nil {
			t.Errorf("Delete(%q) succeeded", id)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.json")); !os.IsNotExist(err) {
		t.Fatal("file escaped maps dir")
	}
}

func TestSaveInvalidMapRejected(t *testing.T) {
	m, _ := NewManager(Options{Dir: t.TempDir()})
	bad := cfg("bad")
	bad.Layout = []string{"...", "...", "..."} // no start, no treat
	var se *service.Error
	if _, err := m.Save(bad, player("tester")); !errors.As(err, &se) || se.Code != service.CodeInvalidArgument {
		t.Fatalf("want INVALID_ARGUMENT, got %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatal("invalid map stored")
	}
}

func TestInvalidFileFailsFast(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"id":"broken","name":"x","layout":["...","...","..."]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewManager(Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("want error naming broken.json, got %v", err)
	}
}

func TestIDFilenameMismatchFailsFast(t *testing.T) {
	dir := t.TempDir()
	data, _ := os.ReadFile("../../maps/straight_line.json")
	if err := os.WriteFile(filepath.Join(dir, "other.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(Options{Dir: dir}); err == nil || !strings.Contains(err.Error(), "other.json") {
		t.Fatalf("want mismatch error, got %v", err)
	}
}

func TestReturnedMapsAreCopies(t *testing.T) {
	m, _ := NewManager(Options{Dir: t.TempDir()})
	if _, err := m.Save(cfg("c"), player("tester")); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Get("c")
	a.Name = "mutated"
	a.SubTapeLengths[0] = 99
	b, _ := m.Get("c")
	if b.Name != "T" || b.SubTapeLengths[0] != 2 {
		t.Fatal("store aliased returned map")
	}
}

func TestRepoMapsLoad(t *testing.T) {
	m, err := NewManager(Options{Dir: "../../maps"})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(m.List()); n != 30 {
		t.Fatalf("want 30 maps, got %d", n)
	}
}

func TestCreateFailsIfExistsAndIsAtomic(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(Options{Dir: dir})

	const n = 16
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		c := cfg("race")
		c.Name = fmt.Sprintf("N%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Create(c, "tester")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
			continue
		}
		var se *service.Error
		if !errors.As(err, &se) || se.Code != service.CodeInvalidArgument {
			t.Fatalf("want INVALID_ARGUMENT conflict, got %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d creates succeeded, want exactly 1", ok)
	}
	// Winner's content is what is stored and on disk.
	got, _ := m.Get("race")
	m2, err := NewManager(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	disk, _ := m2.Get("race")
	if got.Name != disk.Name {
		t.Fatalf("memory %q != disk %q", got.Name, disk.Name)
	}
	// Save still overwrites.
	if _, err := m.Save(cfg("race"), player("tester")); err != nil {
		t.Fatal(err)
	}
}

// splitStore builds a store with shipped maps in shipped/ and player maps in
// user/, mirroring production wiring.
func splitStore(t *testing.T, maxUser int) *Manager {
	t.Helper()
	root := t.TempDir()
	shipped, user := filepath.Join(root, "maps"), filepath.Join(root, "maps-user")
	if err := os.MkdirAll(shipped, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg("shipped"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shipped, "shipped.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(Options{Dir: shipped, UserDir: user, MaxUserMaps: maxUser})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSplitStoreShippedMapsAreBuiltin(t *testing.T) {
	m := splitStore(t, 0)
	if !m.Builtin("shipped") {
		t.Fatal("shipped map not reported as builtin")
	}
	created, err := m.Create(cfg("player"), "tester")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "player" || m.Builtin("player") {
		t.Fatalf("player map: builtin=%v", m.Builtin("player"))
	}
	if _, err := m.Create(cfg("shipped"), "tester"); err == nil {
		t.Fatal("create over a shipped id succeeded")
	}
}

// A new map is written to UserDir and reloads from there; editing a shipped
// map keeps it in Dir.
func TestSplitStoreWritesToUserDir(t *testing.T) {
	m := splitStore(t, 0)
	dir, userDir := m.opts.Dir, m.opts.UserDir
	if _, err := m.Create(cfg("player"), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "player.json")); err != nil {
		t.Fatalf("player map not in user dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "player.json")); !os.IsNotExist(err) {
		t.Fatal("player map written into the shipped dir")
	}
	// Reload: the player map must still be there and still not builtin.
	m2, err := NewManager(Options{Dir: dir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Get("player"); err != nil {
		t.Fatalf("player map lost on reload: %v", err)
	}
	if m2.Builtin("player") {
		t.Fatal("player map became builtin after reload")
	}
	// Editing a shipped map writes to Dir.
	edited := cfg("shipped")
	edited.Name = "Edited"
	if _, err := m2.Save(edited, admin()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shipped.json")); err != nil {
		t.Fatalf("shipped map not in shipped dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "shipped.json")); !os.IsNotExist(err) {
		t.Fatal("shipped map copied into the user dir")
	}
	if got, _ := m2.Get("shipped"); got.Name != "Edited" {
		t.Fatalf("shipped map not updated: %+v", got)
	}
}

// Delete removes the file from the directory the map actually lives in.
func TestSplitStoreDeletePicksTheRightDir(t *testing.T) {
	m := splitStore(t, 0)
	if _, err := m.Create(cfg("player"), "tester"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("player", player("tester")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.opts.UserDir, "player.json")); !os.IsNotExist(err) {
		t.Fatal("player map file not removed")
	}
	if err := m.Delete("shipped", admin()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.opts.Dir, "shipped.json")); !os.IsNotExist(err) {
		t.Fatal("shipped map file not removed")
	}
	if m.Builtin("shipped") {
		t.Fatal("deleted shipped map still reported as builtin")
	}
}

func TestSplitStoreMaxUserMaps(t *testing.T) {
	m := splitStore(t, 2)
	for _, id := range []string{"a", "b"} {
		if _, err := m.Create(cfg(id), "tester"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	_, err := m.Create(cfg("c"), "tester")
	var se *service.Error
	if !errors.As(err, &se) || se.Code != service.CodeLimit {
		t.Fatalf("want LIMIT_REACHED at the cap, got %v", err)
	}
	// Editing an existing map is not blocked by the cap.
	if _, err := m.Save(cfg("a"), player("tester")); err != nil {
		t.Fatalf("save at cap: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.opts.UserDir, "c.json")); !os.IsNotExist(err) {
		t.Fatal("rejected map was written")
	}
}

// One corrupt player file must not stop the server; a corrupt shipped map must.
func TestSplitStoreCorruptUserMapIsSkipped(t *testing.T) {
	m := splitStore(t, 0)
	bad := filepath.Join(m.opts.UserDir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"id":"bad"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: m.opts.Dir, UserDir: m.opts.UserDir})
	if err != nil {
		t.Fatalf("corrupt user map was fatal: %v", err)
	}
	if _, err := m2.Get("bad"); err == nil {
		t.Fatal("corrupt user map was loaded")
	}
	if _, err := m2.Get("shipped"); err != nil {
		t.Fatalf("shipped map lost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(m.opts.Dir, "bad.json"), []byte(`{"id":"bad"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(Options{Dir: m.opts.Dir, UserDir: m.opts.UserDir}); err == nil {
		t.Fatal("corrupt shipped map was tolerated")
	}
}

// A player file may not shadow a shipped map id.
func TestSplitStoreUserMapCannotShadowShipped(t *testing.T) {
	m := splitStore(t, 0)
	shadow := cfg("shipped")
	shadow.Name = "Impostor"
	data, err := json.Marshal(shadow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.opts.UserDir, "shipped.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: m.opts.Dir, UserDir: m.opts.UserDir})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m2.Get("shipped"); got.Name != "T" {
		t.Fatalf("player file shadowed a shipped map: %+v", got)
	}
	if !m2.Builtin("shipped") {
		t.Fatal("shadowed map lost its builtin flag")
	}
}

// A community map is born as the creator's draft, can be saved even when it
// cannot be published yet, and becomes public only after a successful publish.
func TestCommunityMapDraftLifecycle(t *testing.T) {
	pool := pgtest.Pool(t)
	shipped := t.TempDir()
	m, err := NewManager(Options{Dir: shipped, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	alice, bob := player("alice"), player("bob")

	invalid := cfg("my_map")
	invalid.Layout = []string{"#####", ">#*##", "#####"} // treat walled off from the start
	if _, err := m.Create(invalid, "alice"); err != nil {
		t.Fatalf("create invalid draft: %v", err)
	}
	e, err := m.Entry("my_map")
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != engine.MapStatusDraft || e.OwnerID != "alice" {
		t.Fatalf("new map = status %q owner %q, want DRAFT/alice", e.Status, e.OwnerID)
	}
	if !e.VisibleTo("alice") || e.VisibleTo("bob") || e.VisibleTo("") {
		t.Fatal("draft visibility is wrong")
	}
	if _, err := m.Save(invalid, bob); err == nil {
		t.Fatal("another player saved the draft")
	}
	if _, err := m.Publish("my_map", bob); err == nil {
		t.Fatal("another player published the draft")
	}
	if _, err := m.Publish("my_map", alice); err == nil {
		t.Fatal("an invalid draft was published")
	}

	valid := cfg("my_map")
	valid.Layout = []string{"....", ".>.*", "...."}
	if _, err := m.Save(valid, alice); err != nil {
		t.Fatalf("owner save: %v", err)
	}
	if _, err := m.Publish("my_map", alice); err != nil {
		t.Fatalf("publish valid draft: %v", err)
	}
	if e, _ = m.Entry("my_map"); e.Status != engine.MapStatusPublished || !e.VisibleTo("bob") {
		t.Fatalf("published map = %+v", e)
	}
	if _, err := m.Publish("my_map", bob); err == nil {
		t.Fatal("another player published an already published map")
	}

	// Editing a published map hides it again until the owner publishes.
	valid.Name = "Renamed"
	if _, err := m.Save(valid, alice); err != nil {
		t.Fatal(err)
	}
	if e, _ = m.Entry("my_map"); e.Status != engine.MapStatusDraft {
		t.Fatalf("edit left status %q, want DRAFT", e.Status)
	}

	// Ownership and status survive a restart.
	m2, err := NewManager(Options{Dir: shipped, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	if e, _ = m2.Entry("my_map"); e.Status != engine.MapStatusDraft || e.OwnerID != "alice" {
		t.Fatalf("reloaded map = %+v", e)
	}
	if _, err := m2.Publish("my_map", alice); err != nil {
		t.Fatal(err)
	}
	m3, err := NewManager(Options{Dir: shipped, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	if e, _ = m3.Entry("my_map"); e.Status != engine.MapStatusPublished || !e.VisibleTo("bob") {
		t.Fatalf("published map did not survive restart: %+v", e)
	}
}

// A map written before ownership existed has no owner and stays published; it
// is changeable only with the admin key, so nobody can claim it silently.
func TestLegacyUserMapIsPublishedAndAdminOnly(t *testing.T) {
	pool := pgtest.Pool(t)
	m, err := NewManager(Options{Dir: t.TempDir(), Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	// The legacy import path and the migration both produce ownerless rows.
	if _, err := m.store.Create("legacy", cfg("legacy")); err != nil {
		t.Fatal(err)
	}
	e, err := m.Entry("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != engine.MapStatusPublished || e.OwnerID != "" || !e.VisibleTo("bob") {
		t.Fatalf("legacy map = %+v", e)
	}
	if _, err := m.Save(cfg("legacy"), player("bob")); err == nil {
		t.Fatal("a player claimed a legacy map")
	}
	if _, err := m.Save(cfg("legacy"), admin()); err != nil {
		t.Fatalf("admin save: %v", err)
	}
}
