package session

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

const acctID = "0123456789abcdef01234567"

func newPG(t *testing.T) (*PostgresPersistence, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Pool(t)
	return NewPostgresPersistence(pool), pool
}

func addAccount(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO accounts (id, provider, provider_user_id, handle, display_name, created_at)
		 VALUES ($1, 'password', $1, $1, $1, now())`, id); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRoundTrip(t *testing.T) {
	p, _ := newPG(t)
	want := fixture(t, idA)
	want.OwnerHash = strings.Repeat("ab", 32)
	if err := p.Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := p.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Playing || got.LastEvent != nil {
		t.Fatal("transient fields were restored")
	}
	if got.ID != idA || got.DisplayName != "fixture" || got.MapID != "corridor" || got.SpeedMs != 120 ||
		got.Seq != 17 || got.Attempts != 3 || got.BestSteps != 5 || got.OwnerHash != want.OwnerHash {
		t.Fatalf("scalar fields differ: %+v", got)
	}
	// PostgreSQL keeps microseconds; the fixture's nanoseconds are cut.
	if !got.CreatedAt.Equal(want.CreatedAt.Truncate(time.Microsecond)) || !got.LastActionAt.Equal(want.LastActionAt.Truncate(time.Microsecond)) {
		t.Fatalf("timestamps differ: %v %v", got.CreatedAt, got.LastActionAt)
	}
	if !reflect.DeepEqual(got.Program, want.Program) {
		t.Fatalf("program differs:\n got %+v\nwant %+v", got.Program, want.Program)
	}
	if !reflect.DeepEqual(got.VM, want.VM) {
		t.Fatalf("vm differs:\n got %+v\nwant %+v", got.VM, want.VM)
	}
	if !reflect.DeepEqual(got.Map.Config(), want.Map.Config()) {
		t.Fatalf("map snapshot differs: %+v", got.Map.Config())
	}
	// The restored VM steps exactly like the original.
	for !want.VM.Status.Terminal() {
		w, werr := want.VM.Step(want.Map, &want.Program)
		g, gerr := got.VM.Step(got.Map, &got.Program)
		if werr != nil || gerr != nil || !reflect.DeepEqual(w, g) {
			t.Fatalf("restored VM diverged: %+v vs %+v (%v/%v)", g, w, gerr, werr)
		}
	}
}

func TestPostgresBestWinsSurviveAndAreSanitised(t *testing.T) {
	p, _ := newPG(t)
	s := fixture(t, idA)
	s.FewestSlots = &service.Win{Slots: 3, Steps: 12}
	s.FewestSteps = &service.Win{Slots: 4, Steps: 9}
	if err := p.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := p.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.FewestSlots == nil || *got.FewestSlots != *s.FewestSlots || got.FewestSteps == nil || *got.FewestSteps != *s.FewestSteps {
		t.Fatalf("best wins lost: %+v %+v", got.FewestSlots, got.FewestSteps)
	}

	// A win that cannot have happened on the map is dropped on load, as for files.
	s.FewestSlots = &service.Win{Slots: 1, Steps: 99999}
	if err := p.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err = p.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.FewestSlots != nil {
		t.Fatalf("impossible win survived: %+v", got.FewestSlots)
	}
}

func TestPostgresZeroSubMapStaysZeroSubs(t *testing.T) {
	p, _ := newPG(t)
	m := mustMap(t, engine.MapConfig{
		ID: "nosubs", Name: "n", Layout: []string{".....", ">...*", "....."},
		MainTapeLength: 4, SubTapeLengths: []int{}, MaxSteps: 30,
	})
	s := &service.Session{ID: idA, MapID: m.ID, Map: m, Program: engine.EmptyProgram(m), VM: engine.NewVMState(m), SpeedMs: 500, Seq: 1}
	if err := p.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := p.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Map.SubTapeLengths == nil || len(got.Map.SubTapeLengths) != 0 || len(got.Program.Subs) != 0 {
		t.Fatalf("zero-sub map came back with subs %#v / %d", got.Map.SubTapeLengths, len(got.Program.Subs))
	}
}

func TestPostgresSaveReplacesAndKeepsCreatedAt(t *testing.T) {
	p, pool := newPG(t)
	s := fixture(t, idA)
	if err := p.Save(s); err != nil {
		t.Fatal(err)
	}
	s.Seq, s.Attempts, s.DisplayName = 99, 8, "renamed"
	s.CreatedAt = s.CreatedAt.AddDate(1, 0, 0) // a later Save must not rewrite created_at
	if err := p.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := p.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 99 || got.Attempts != 8 || got.DisplayName != "renamed" {
		t.Fatalf("replace lost: %+v", got)
	}
	if !got.CreatedAt.Equal(fixture(t, idA).CreatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("created_at changed: %v", got.CreatedAt)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, err %v", n, err)
	}
}

func TestPostgresDeleteLoadAndList(t *testing.T) {
	p, _ := newPG(t)
	for _, id := range []string{idB, idA} {
		if err := p.Save(fixture(t, id)); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := p.ListAll()
	if err != nil || len(ids) != 2 {
		t.Fatalf("ListAll = %v, %v", ids, err)
	}
	if err := p.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	if _, err := p.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after delete = %v", err)
	}
	if _, err := p.Load("../etc"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("bad id = %v", err)
	}
	if err := p.Save(&service.Session{ID: "nope"}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Save bad id = %v", err)
	}
}

func TestPostgresAccountLink(t *testing.T) {
	p, pool := newPG(t)
	addAccount(t, pool, acctID)

	s := fixture(t, idA)
	s.AccountID = acctID
	if err := p.Save(s); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Load(idA); got == nil || got.AccountID != acctID {
		t.Fatalf("account link lost: %+v", got)
	}

	// Deleting the account leaves the session behind as a guest row.
	if _, err := pool.Exec(context.Background(), `DELETE FROM accounts WHERE id = $1`, acctID); err != nil {
		t.Fatal(err)
	}
	if got, err := p.Load(idA); err != nil || got.AccountID != "" {
		t.Fatalf("after account delete: %+v, %v", got, err)
	}

	// A late persist of a session whose account is gone must not fail.
	if err := p.Save(s); err != nil {
		t.Fatalf("Save with a deleted account's id: %v", err)
	}
	if got, _ := p.Load(idA); got == nil || got.AccountID != "" {
		t.Fatalf("stale account id was stored: %+v", got)
	}
}

func TestPostgresCorruptRowIsSkippedByLoadAll(t *testing.T) {
	p, pool := newPG(t)
	for _, id := range []string{idA, idB} {
		if err := p.Save(fixture(t, id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(context.Background(), `UPDATE sessions SET vm = jsonb_set(vm, '{status}', '"PAUSED"') WHERE id = $1`, idB); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(idB); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt row loaded: %v", err)
	}

	m := NewManager(p, 0)
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if m.Count() != 1 {
		t.Fatalf("loaded %d sessions, want only the good one", m.Count())
	}
	if _, ok := m.Get(idA); !ok {
		t.Fatal("good session missing")
	}
}

func TestPostgresManagerPersistAndDelete(t *testing.T) {
	p, _ := newPG(t)
	m := NewManager(p, 0)
	s := fixture(t, idA)
	if err := m.Put(s); err != nil {
		t.Fatal(err)
	}
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(idA); err != nil {
		t.Fatalf("not stored: %v", err)
	}
	if err := m.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still stored: %v", err)
	}
	// A late persist cannot bring a deleted session back.
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resurrected: %v", err)
	}
}
