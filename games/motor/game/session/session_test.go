package session

import (
	"errors"
	"io"
	"log"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

const (
	idA = "00112233445566aa"
	idB = "00112233445566bb"
	idC = "00112233445566cc"
)

var corridorCfg = engine.MapConfig{
	ID: "corridor", Name: "Corridor", Layout: []string{".....", ">...*", "....."},
	MainTapeLength: 6, SubTapeLengths: []int{2}, MaxSteps: 30, MaxCallDepth: 8,
}

func init() {
	// Corrupt-row tests log on purpose.
	log.SetOutput(io.Discard)
}

func newPersistence(t testing.TB) *PostgresPersistence {
	t.Helper()
	return NewPostgresPersistence(pgtest.Pool(t))
}

func mustMap(t testing.TB, cfg engine.MapConfig) *engine.Map {
	t.Helper()
	m, err := engine.NewMap(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// fixture builds a session two steps into a winning-bound run, with every
// persisted field set to a distinguishable value.
func fixture(t testing.TB, id string) *service.Session {
	t.Helper()
	m := mustMap(t, corridorCfg)
	prog := engine.EmptyProgram(m)
	for i := range 4 {
		prog.Main[i] = engine.MoveForward
	}
	vm := engine.NewVMState(m)
	vm.Settle(&prog)
	var last engine.StepEvent
	for range 2 {
		ev, err := vm.Step(m, &prog)
		if err != nil {
			t.Fatal(err)
		}
		last = ev
	}
	return &service.Session{
		ID: id, MapID: m.ID, Map: m, Program: prog, VM: vm,
		Playing: true, SpeedMs: 120, Seq: 17,
		CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC),
		LastActionAt: time.Date(2026, 1, 2, 4, 4, 5, 6, time.UTC),
		Attempts:     3, BestSteps: 5, LastEvent: &last,
	}
}

func TestValidID(t *testing.T) {
	good := []string{idA, "0123456789abcdef", "ffffffffffffffff"}
	bad := []string{
		"", "0123", "0123456789abcde", "0123456789abcdef0", "0123456789ABCDEF", "0123456789abcdeg",
		"../0123456789abcd", "..", ".", "0123456789abcd/.", "0123456789abc\\..", " 123456789abcdef", "0123456789abcdef\n",
		"/etc/passwd", "0123456789abcdef.json",
	}
	for _, id := range good {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false", id)
		}
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
}

func TestPostgresRoundTripTileMovedRun(t *testing.T) {
	// A conveyor push and a portal teleport move the robot twice in one step, so
	// such a run has more visited positions than steps+1. It is a normal run and
	// must survive a save/load (a restart) instead of being skipped as corrupt.
	for name, tc := range map[string]struct {
		layout []string
		want   engine.Position
	}{
		"conveyor": {layout: []string{">RRR*", "#####", "#####"}, want: engine.Position{X: 2, Y: 0}},
		"portal":   {layout: []string{">1..*", "....1", "#####"}, want: engine.Position{X: 4, Y: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			fp := newPersistence(t)
			m := mustMap(t, engine.MapConfig{
				ID: name, Name: name, Layout: tc.layout,
				MainTapeLength: 4, SubTapeLengths: []int{}, MaxSteps: 20,
			})
			prog := engine.EmptyProgram(m)
			prog.Main[0] = engine.MoveForward
			vm := engine.NewVMState(m)
			vm.Settle(&prog)
			if _, err := vm.Step(m, &prog); err != nil {
				t.Fatal(err)
			}
			if vm.Pos != tc.want || len(vm.Visited) != vm.Steps+2 {
				t.Fatalf("step ended at %+v with %d visits for %d steps", vm.Pos, len(vm.Visited), vm.Steps)
			}
			s := &service.Session{
				ID: idA, MapID: m.ID, Map: m, Program: prog, VM: vm,
				SpeedMs: 500, Seq: 1, CreatedAt: time.Now(),
			}
			if err := fp.Save(s); err != nil {
				t.Fatal(err)
			}
			got, err := fp.Load(idA)
			if err != nil {
				t.Fatalf("a run that used a %s must load: %v", name, err)
			}
			if !reflect.DeepEqual(got.VM, s.VM) {
				t.Fatalf("vm differs:\n got %+v\nwant %+v", got.VM, s.VM)
			}
		})
	}
}

func TestPostgresRoundTripZeroSubMap(t *testing.T) {
	fp := newPersistence(t)
	m := mustMap(t, engine.MapConfig{
		ID: "nosubs", Name: "n", Layout: []string{".....", ">...*", "....."},
		MainTapeLength: 4, SubTapeLengths: []int{}, MaxSteps: 30,
	})
	prog := engine.EmptyProgram(m)
	s := &service.Session{ID: idA, MapID: m.ID, Map: m, Program: prog, VM: engine.NewVMState(m), SpeedMs: 500, Seq: 1}
	if err := fp.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := fp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Map.SubTapeLengths) != 0 || len(got.Program.Subs) != 0 {
		t.Fatalf("zero-sub map came back with subs %v / %d", got.Map.SubTapeLengths, len(got.Program.Subs))
	}
}

func TestPathTraversalRejected(t *testing.T) {
	// No database is needed: an invalid id is rejected before any query.
	fp := NewPostgresPersistence(nil)
	mgr := NewManager(nil, 0)

	for _, id := range []string{"../secret", "../../etc/passwd", "..", ".", "", "/abs", `..\secret`, idA + "/../" + idB} {
		bad := fixture(t, idA)
		bad.ID = id
		if err := fp.Save(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Save(%q) = %v, want ErrInvalidID", id, err)
		}
		if _, err := fp.Load(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Load(%q) = %v, want ErrInvalidID", id, err)
		}
		if err := fp.Delete(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Delete(%q) = %v, want ErrInvalidID", id, err)
		}
		if err := mgr.Put(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Put(%q) = %v, want ErrInvalidID", id, err)
		}
		var se *service.Error
		if err := mgr.Delete(id); !errors.As(err, &se) || se.Code != service.CodeNotFound {
			t.Errorf("Manager.Delete(%q) = %v, want NOT_FOUND", id, err)
		}
	}
}

// stubPersistence returns a Playing session to prove LoadAll normalizes it.
type stubPersistence struct{ s *service.Session }

func (p stubPersistence) Save(*service.Session) error { return nil }
func (p stubPersistence) Load(string) (*service.Session, error) {
	return p.s, nil
}
func (p stubPersistence) Delete(string) error { return ErrNotFound }
func (p stubPersistence) ListAll() ([]string, error) {
	return []string{p.s.ID}, nil
}

func TestLoadAllForcesPlayingFalse(t *testing.T) {
	s := fixture(t, idA)
	s.Playing = true
	m := NewManager(stubPersistence{s}, 10)
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(idA); got == nil || got.Playing {
		t.Fatal("LoadAll left Playing=true")
	}
}

func TestManagerPutGetListLimit(t *testing.T) {
	m := NewManager(nil, 2)
	a, b, c := fixture(t, idA), fixture(t, idB), fixture(t, idC)
	if err := m.Put(a); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(a); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate Put = %v, want ErrExists", err)
	}
	if err := m.Put(b); err != nil {
		t.Fatal(err)
	}
	err := m.Put(c)
	var se *service.Error
	if !errors.As(err, &se) || se.Code != service.CodeLimit {
		t.Fatalf("third Put = %v, want LIMIT_REACHED", err)
	}
	if got, ok := m.Get(idA); !ok || got != a {
		t.Fatal("Get must return the live pointer")
	}
	if _, ok := m.Get(idC); ok {
		t.Fatal("rejected session was stored")
	}
	if len(m.List()) != 2 || m.Count() != 2 {
		t.Fatalf("List/Count = %d/%d", len(m.List()), m.Count())
	}
	if err := m.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(c); err != nil {
		t.Fatalf("Put after Delete: %v", err)
	}
	if err := m.Delete(idA); !errors.As(err, &se) || se.Code != service.CodeNotFound {
		t.Fatalf("Delete of missing = %v, want NOT_FOUND", err)
	}
}

func TestManagerDefaultLimit(t *testing.T) {
	if m := NewManager(nil, 0); m.maxSessions != DefaultMaxSessions {
		t.Fatalf("maxSessions = %d, want default %d", m.maxSessions, DefaultMaxSessions)
	}
}

func TestManagerPersistAndDelete(t *testing.T) {
	fp := newPersistence(t)
	m := NewManager(fp, 10)
	s := fixture(t, idA)
	if err := m.Put(s); err != nil {
		t.Fatal(err)
	}
	if _, err := fp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("Put must not write; the service persists explicitly")
	}
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if _, err := fp.Load(idA); err != nil {
		t.Fatalf("Persist wrote nothing: %v", err)
	}
	// Persist snapshots under the session lock.
	s.Lock()
	s.Seq = 99
	s.Unlock()
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if got, err := fp.Load(idA); err != nil || got.Seq != 99 {
		t.Fatalf("persisted seq = %d, err %v, want 99", got.Seq, err)
	}

	if err := m.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if _, err := fp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("Delete left the row")
	}
	// A late Persist (e.g. from a runner finishing) must not resurrect it.
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if _, err := fp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("Persist resurrected a deleted session")
	}
}

func TestManagerDeleteRemovesRowOfUnloadedSession(t *testing.T) {
	fp := newPersistence(t)
	if err := fp.Save(fixture(t, idA)); err != nil {
		t.Fatal(err)
	}
	m := NewManager(fp, 10) // LoadAll not called: only in the database
	if err := m.Delete(idA); err != nil {
		t.Fatalf("Delete of unloaded session: %v", err)
	}
	if _, err := fp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row not removed: %v", err)
	}
}

func TestManagerMemoryOnly(t *testing.T) {
	m := NewManager(nil, 5)
	s := fixture(t, idA)
	if err := m.Put(s); err != nil {
		t.Fatal(err)
	}
	if err := m.Persist(s); err != nil {
		t.Fatalf("Persist without persistence: %v", err)
	}
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll without persistence: %v", err)
	}
	if err := m.Delete(idA); err != nil {
		t.Fatal(err)
	}
}

func TestManagerLoadAllRoundTripKeepsLimitOnPut(t *testing.T) {
	fp := newPersistence(t)
	m1 := NewManager(fp, 10)
	for _, id := range []string{idA, idB, idC} {
		s := fixture(t, id)
		if err := m1.Put(s); err != nil {
			t.Fatal(err)
		}
		if err := m1.Persist(s); err != nil {
			t.Fatal(err)
		}
	}
	m2 := NewManager(fp, 2) // fewer slots than persisted sessions
	if err := m2.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if m2.Count() != 3 {
		t.Fatalf("persisted sessions must all load, got %d", m2.Count())
	}
	var se *service.Error
	if err := m2.Put(fixture(t, "00112233445566dd")); !errors.As(err, &se) || se.Code != service.CodeLimit {
		t.Fatalf("Put over limit = %v", err)
	}
}

// TestManagerConcurrentPersist races mutation, Persist and Delete (run with -race).
func TestManagerConcurrentPersist(t *testing.T) {
	fp := newPersistence(t)
	m := NewManager(fp, 10)
	s := fixture(t, idA)
	if err := m.Put(s); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				s.Lock()
				s.Seq++
				s.Unlock()
				if err := m.Persist(s); err != nil {
					t.Errorf("Persist: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got, err := fp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	want := s.Seq
	s.Unlock()
	if got.Seq != want {
		t.Fatalf("last write wins: row has seq %d, live has %d", got.Seq, want)
	}
}
