package jsonmigrate

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

const (
	sessA   = "00112233445566aa"
	sessB   = "00112233445566bb"
	acctOne = "0123456789abcdef01234567"
	acctTwo = "fedcba9876543210fedcba98"
	ghost   = "aaaaaaaaaaaaaaaaaaaaaaaa" // an account that was never saved
)

func init() { log.SetOutput(io.Discard) }

func mapCfg(id string) engine.MapConfig {
	return engine.MapConfig{
		ID: id, Name: "Corridor " + id, Layout: []string{".....", ">...*", "....."},
		MainTapeLength: 6, SubTapeLengths: []int{2}, MaxSteps: 30, MaxCallDepth: 8,
	}
}

func newSession(t *testing.T, id, accountID string) *service.Session {
	t.Helper()
	m, err := engine.NewMap(mapCfg("corridor"))
	if err != nil {
		t.Fatal(err)
	}
	prog := engine.EmptyProgram(m)
	for i := range 4 {
		prog.Main[i] = engine.MoveForward
	}
	vm := engine.NewVMState(m)
	vm.Settle(&prog)
	for range 2 {
		if _, err := vm.Step(m, &prog); err != nil {
			t.Fatal(err)
		}
	}
	return &service.Session{
		ID: id, MapID: m.ID, Map: m, Program: prog, VM: vm, SpeedMs: 200, Seq: 7,
		CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		LastActionAt: time.Date(2026, 1, 2, 4, 4, 5, 0, time.UTC),
		Attempts:     2, BestSteps: 5, AccountID: accountID, OwnerHash: strings.Repeat("c", 64),
	}
}

// writeFiles lays out a JSON-file world: one shipped map, one player map, two
// accounts with a login each, and two sessions (one linked to a deleted account).
func writeFiles(t *testing.T) Dirs {
	t.Helper()
	root := t.TempDir()
	d := Dirs{
		Maps: filepath.Join(root, "maps"), UserMaps: filepath.Join(root, "maps-user"),
		Sessions: filepath.Join(root, "sessions"), Accounts: filepath.Join(root, "accounts"),
	}
	if err := os.MkdirAll(d.Maps, 0o755); err != nil {
		t.Fatal(err)
	}
	shipped, err := json.Marshal(mapCfg("shipped"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Maps, "shipped.json"), shipped, 0o644); err != nil {
		t.Fatal(err)
	}
	mm, err := config.NewManager(config.Options{Dir: d.Maps, UserDir: d.UserMaps})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mm.Create(mapCfg("mine")); err != nil {
		t.Fatal(err)
	}

	store, err := account.Open(d.Accounts, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{acctOne, acctTwo} {
		a := service.Account{
			ID: id, Provider: "password", ProviderUserID: id, Handle: []string{"otter", "fox"}[i], DisplayName: "Name " + id[:4],
			ProfilePublic: true, CreatedAt: time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC), PasswordHash: "$argon2id$hash" + id[:2],
		}
		if err := store.Create(a); err != nil {
			t.Fatal(err)
		}
	}

	fp, err := session.NewFilePersistence(d.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	if err := fp.Save(newSession(t, sessA, acctOne)); err != nil {
		t.Fatal(err)
	}
	if err := fp.Save(newSession(t, sessB, ghost)); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestImportMovesEverythingAndIsSafeToRerun(t *testing.T) {
	d := writeFiles(t)
	pool := pgtest.Pool(t)
	ctx := context.Background()

	dry, err := Import(ctx, pool, d, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Maps != 1 || dry.Accounts != 2 || dry.Sessions != 2 {
		t.Fatalf("dry run counted %v", dry)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions) + (SELECT count(*) FROM accounts) + (SELECT count(*) FROM maps)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("dry run wrote %d rows, %v", rows, err)
	}

	got, err := Import(ctx, pool, d, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Maps != 1 || got.Accounts != 2 || got.Sessions != 2 || got.Guests != 1 {
		t.Fatalf("first import = %v", got)
	}

	// Everything is readable back through the stores the server uses.
	pga := account.NewPostgresStore(pool)
	if a, ok := pga.ByHandle("otter"); !ok || a.ID != acctOne || a.PasswordHash != "$argon2id$hash01" {
		t.Fatalf("account not imported intact: %+v %v", a, ok)
	}
	pgs := session.NewPostgresPersistence(pool)
	a, err := pgs.Load(sessA)
	if err != nil || a.AccountID != acctOne || a.Seq != 7 || a.Attempts != 2 || a.OwnerHash != strings.Repeat("c", 64) {
		t.Fatalf("session A = %+v, %v", a, err)
	}
	if b, err := pgs.Load(sessB); err != nil || b.AccountID != "" {
		t.Fatalf("session B (dead account link) = %+v, %v", b, err)
	}
	mm, err := config.NewManager(config.Options{Dir: d.Maps, UserStore: config.NewPostgresUserMaps(pool)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mm.Get("mine"); err != nil || mm.Builtin("mine") {
		t.Fatalf("player map: %v builtin=%v", err, mm.Builtin("mine"))
	}
	if cfgs, _ := config.NewPostgresUserMaps(pool).List(); len(cfgs) != 1 {
		t.Fatalf("the shipped map was imported too: %d player maps", len(cfgs))
	}

	// A second run adds nothing, and does not overwrite what changed in the database since.
	if _, err := pool.Exec(ctx, `UPDATE sessions SET attempts = 99 WHERE id = $1`, sessA); err != nil {
		t.Fatal(err)
	}
	again, err := Import(ctx, pool, d, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Maps+again.Accounts+again.Sessions != 0 || again.Skipped == 0 {
		t.Fatalf("re-run = %v, want nothing inserted", again)
	}
	if a, _ := pgs.Load(sessA); a.Attempts != 99 {
		t.Fatalf("re-run overwrote newer database state: attempts=%d", a.Attempts)
	}
}

func TestImportWithMissingDirectoriesDoesNothing(t *testing.T) {
	pool := pgtest.Pool(t)
	root := t.TempDir()
	got, err := Import(context.Background(), pool, Dirs{
		Maps: filepath.Join(root, "a"), UserMaps: filepath.Join(root, "b"),
		Sessions: filepath.Join(root, "c"), Accounts: filepath.Join(root, "d"),
	}, false)
	if err != nil || got != (Counts{}) {
		t.Fatalf("Import = %v, %v", got, err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("import created %d directories", len(entries))
	}
}

func TestExportRoundTripsAndRefusesToOverwrite(t *testing.T) {
	src := writeFiles(t)
	pool := pgtest.Pool(t)
	ctx := context.Background()
	if _, err := Import(ctx, pool, src, false); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	out := Dirs{
		UserMaps: filepath.Join(root, "maps-user"), Sessions: filepath.Join(root, "sessions"), Accounts: filepath.Join(root, "accounts"),
	}
	got, err := Export(ctx, pool, out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Maps != 1 || got.Accounts != 2 || got.Sessions != 2 {
		t.Fatalf("export = %v", got)
	}

	// The exported files are a working JSON-file world: the file stores read them back.
	files, err := account.Open(out.Accounts, nil)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := account.Open(src.Accounts, nil)
	if !reflect.DeepEqual(files.Accounts(), orig.Accounts()) {
		t.Fatalf("accounts differ after the round trip:\n got %+v\nwant %+v", files.Accounts(), orig.Accounts())
	}
	fp, _ := session.NewFilePersistence(out.Sessions)
	s, err := fp.Load(sessA)
	if err != nil || s.AccountID != acctOne || s.Attempts != 2 {
		t.Fatalf("exported session = %+v, %v", s, err)
	}
	mm, err := config.NewManager(config.Options{Dir: src.Maps, UserDir: out.UserMaps})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mm.Get("mine"); err != nil {
		t.Fatalf("exported player map: %v", err)
	}

	// Running it again into the same directories must not clobber them.
	if _, err := Export(ctx, pool, out); err == nil || !strings.Contains(err.Error(), "empty directories") {
		t.Fatalf("second export = %v, want a refusal", err)
	}
}
