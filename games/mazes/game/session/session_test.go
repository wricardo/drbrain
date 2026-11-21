package session

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

const (
	idA = "00112233445566aa"
	idB = "00112233445566bb"
	idC = "00112233445566cc"
)

// towerCfg: two levels, paired stairs and a portal pair on level 1.
// RIGHT, RIGHT (stairs), RIGHT (portal), DOWN wins in 4 moves.
var towerCfg = engine.MapConfig{
	ID: "tower", Name: "Tower", MaxMoves: 6,
	Levels: [][]string{
		{"SoU.", "....", "...."},
		{"..D1", "...1", "...F"},
	},
}

func init() {
	// Skipped/corrupt-payload tests log on purpose.
	log.SetOutput(io.Discard)
}

func mustMap(t testing.TB, cfg engine.MapConfig) *engine.Map {
	t.Helper()
	m, err := engine.NewMap(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// play applies dirs to a fresh state of m.
func play(t testing.TB, m *engine.Map, dirs ...engine.Direction) (engine.State, engine.MoveEvent) {
	t.Helper()
	st := engine.NewState(m)
	var last engine.MoveEvent
	for _, d := range dirs {
		ev, err := st.Move(m, d)
		if err != nil {
			t.Fatal(err)
		}
		last = ev
	}
	return st, last
}

// fixture builds a session three moves into the tower (stairs, then a portal
// hop), with every persisted field set to a distinguishable value.
func fixture(t testing.TB, id string) *service.Session {
	t.Helper()
	m := mustMap(t, towerCfg)
	st, last := play(t, m, engine.DirRight, engine.DirRight, engine.DirRight)
	best := 5
	return &service.Session{
		ID: id, DisplayName: "fixture", MapID: m.ID, Map: m, State: st, Seq: 17,
		CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC),
		LastActionAt: time.Date(2026, 1, 2, 4, 4, 5, 6, time.UTC),
		Attempts:     3, BestMoves: &best, LastEvent: &last,
	}
}

// newMemory returns a persistence backed by the in-memory store, for tests
// that do not exercise durability.
func newMemory(t testing.TB) *StorePersistence {
	t.Helper()
	return &StorePersistence{store: sessionstore.NewMemory(sessionstore.Options{})}
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

func TestRoundTrip(t *testing.T) {
	sp := newMemory(t)
	want := fixture(t, idA)
	if err := sp.Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := sp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastEvent != nil {
		t.Fatal("LastEvent is transient and must not be restored")
	}
	if got.ID != idA || got.DisplayName != "fixture" || got.MapID != "tower" || got.Seq != 17 || got.Attempts != 3 ||
		got.BestMoves == nil || *got.BestMoves != 5 {
		t.Fatalf("scalar fields differ: %+v", got)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.LastActionAt.Equal(want.LastActionAt) {
		t.Fatalf("timestamps differ: %v %v", got.CreatedAt, got.LastActionAt)
	}
	if !reflect.DeepEqual(got.State, want.State) {
		t.Fatalf("state differs:\n got %+v\nwant %+v", got.State, want.State)
	}
	if !reflect.DeepEqual(got.Map.Config(), want.Map.Config()) {
		t.Fatalf("map snapshot differs: %+v", got.Map.Config())
	}

	// The restored state must play exactly like the original.
	for _, d := range []engine.Direction{engine.DirUp, engine.DirDown} {
		w, werr := want.State.Move(want.Map, d)
		g, gerr := got.State.Move(got.Map, d)
		if werr != nil || gerr != nil || !reflect.DeepEqual(w, g) || !reflect.DeepEqual(want.State, got.State) {
			t.Fatalf("restored state diverged: %+v vs %+v (%v/%v)", g, w, gerr, werr)
		}
	}
	if got.State.Status != engine.StatusWon {
		t.Fatalf("restored run ended %s, want WON", got.State.Status)
	}
}

// A session's owner (account and token hash) survives the round trip; a
// payload written before ownership existed loads without an owner and stays
// open.
func TestRoundTripOwnership(t *testing.T) {
	sp := newMemory(t)
	token, hash, err := service.NewOwnerToken()
	if err != nil {
		t.Fatal(err)
	}
	want := fixture(t, idA)
	want.OwnerAccountID = "acct-1"
	want.OwnerTokenHash = hash
	if err := sp.Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := sp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerAccountID != "acct-1" || got.OwnerTokenHash != hash {
		t.Fatalf("restored ownership = %q/%q", got.OwnerAccountID, got.OwnerTokenHash)
	}
	if !got.CanPlay(service.Actor{OwnerToken: token}) || !got.CanPlay(service.Actor{AccountID: "acct-1"}) {
		t.Fatal("the restored owner must be able to play")
	}
	if got.CanPlay(service.Actor{OwnerToken: "wrong"}) || got.CanPlay(service.Actor{AccountID: "acct-2"}) {
		t.Fatal("a stranger must not play the restored session")
	}

	// A payload without ownership fields is open to anyone.
	putPayload(t, sp, idB, nil)
	legacy, err := sp.Load(idB)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.OwnerAccountID != "" || legacy.OwnerTokenHash != "" || !legacy.CanPlay(service.Actor{}) {
		t.Fatalf("legacy ownership = %q/%q open %v", legacy.OwnerAccountID, legacy.OwnerTokenHash, legacy.CanPlay(service.Actor{}))
	}
}

// A BestMoves of nil (never won) must survive the round trip as nil, not 0.
func TestRoundTripWithoutBest(t *testing.T) {
	sp := newMemory(t)
	m := mustMap(t, towerCfg)
	s := &service.Session{ID: idA, MapID: m.ID, Map: m, State: engine.NewState(m), Seq: 1}
	if err := sp.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := sp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.BestMoves != nil || got.State.Status != engine.StatusReady || !reflect.DeepEqual(got.State.Path, []engine.Position{m.Start}) {
		t.Fatalf("fresh session came back as %+v best %v", got.State, got.BestMoves)
	}
}

// Terminal states (won on the last allowed move, lost at the limit) load back.
func TestRoundTripTerminalStates(t *testing.T) {
	sp := newMemory(t)
	m := mustMap(t, towerCfg)
	won, _ := play(t, m, engine.DirRight, engine.DirRight, engine.DirRight, engine.DirUp, engine.DirDown)
	limit := mustMap(t, engine.MapConfig{ID: "limit", Name: "L", MaxMoves: 2, Levels: [][]string{{"...", "So.", "..F"}}})
	lost, _ := play(t, limit, engine.DirRight, engine.DirLeft)
	if won.Status != engine.StatusWon || lost.Status != engine.StatusLost || lost.LossReason != engine.LossMoveLimit {
		t.Fatalf("setup: %s / %s/%s", won.Status, lost.Status, lost.LossReason)
	}
	for id, c := range map[string]struct {
		m  *engine.Map
		st engine.State
	}{idA: {m, won}, idB: {limit, lost}} {
		if err := sp.Save(&service.Session{ID: id, MapID: c.m.ID, Map: c.m, State: c.st, Seq: 1}); err != nil {
			t.Fatal(err)
		}
		got, err := sp.Load(id)
		if err != nil {
			t.Fatalf("%s state %s did not load: %v", id, c.st.Status, err)
		}
		if !reflect.DeepEqual(got.State, c.st) {
			t.Fatalf("%s: state differs: %+v vs %+v", id, got.State, c.st)
		}
	}
}

func TestDelete(t *testing.T) {
	sp := newMemory(t)
	if err := sp.Save(fixture(t, idA)); err != nil {
		t.Fatal(err)
	}
	if err := sp.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if err := sp.Delete(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}
	if _, err := sp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after Delete = %v, want ErrNotFound", err)
	}
}

func TestInvalidIDRejected(t *testing.T) {
	sp := newMemory(t)
	for _, id := range []string{"../secret", "../../etc/passwd", "..", ".", "", "/abs", `..\secret`, idA + "/../" + idB} {
		bad := fixture(t, idA)
		bad.ID = id
		if err := sp.Save(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Save(%q) = %v, want ErrInvalidID", id, err)
		}
		if _, err := sp.Load(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Load(%q) = %v, want ErrInvalidID", id, err)
		}
		if err := sp.Delete(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Delete(%q) = %v, want ErrInvalidID", id, err)
		}
		mgr := NewManager(sp, 10)
		if err := mgr.Put(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Put(%q) = %v, want ErrInvalidID", id, err)
		}
		var se *service.Error
		if err := mgr.Delete(id); !errors.As(err, &se) || se.Code != service.CodeNotFound {
			t.Errorf("Manager.Delete(%q) = %v, want NOT_FOUND", id, err)
		}
	}
	if ids, _ := sp.ListAll(); len(ids) != 0 {
		t.Fatalf("rejected ids produced sessions: %v", ids)
	}
}

func persistedFrom(s *service.Session) PersistedSession {
	return PersistedSession{
		SchemaVersion: SchemaVersion, ID: s.ID, DisplayName: s.DisplayName, Map: s.Map.Config(),
		State: s.State.Clone(), Seq: s.Seq,
		CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt, Attempts: s.Attempts, BestMoves: s.BestMoves,
		OwnerAccountID: s.OwnerAccountID, OwnerTokenHash: s.OwnerTokenHash,
	}
}

// putPayload stores a persisted session after letting mutate edit it,
// bypassing Save so invalid payloads can be written.
func putPayload(t *testing.T, sp *StorePersistence, id string, mutate func(*PersistedSession)) {
	t.Helper()
	ps := persistedFrom(fixture(t, id))
	if mutate != nil {
		mutate(&ps)
	}
	data, err := json.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.store.Persist(sessionstore.Meta{ID: id, DisplayName: ps.DisplayName, MapID: ps.Map.ID}, data); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsInvalidPayloads(t *testing.T) {
	pos := func(l, x, y int) engine.Position { return engine.Position{Level: l, X: x, Y: y} }
	zero := 0
	huge := 7
	neg := -1
	cases := map[string]func(*PersistedSession){
		"future schema":       func(p *PersistedSession) { p.SchemaVersion = SchemaVersion + 1 },
		"missing schema":      func(p *PersistedSession) { p.SchemaVersion = 0 },
		"id mismatch":         func(p *PersistedSession) { p.ID = idB },
		"map without finish":  func(p *PersistedSession) { p.Map.Levels[1][2] = "...." },
		"unknown status":      func(p *PersistedSession) { p.State.Status = "PAUSED" },
		"unknown loss reason": func(p *PersistedSession) { p.State.LossReason = "STEP_LIMIT" },
		"empty path":          func(p *PersistedSession) { p.State.Path = nil },
		"path not at start":   func(p *PersistedSession) { p.State.Path[0] = pos(0, 1, 0) },
		"path teleports":      func(p *PersistedSession) { p.State.Path[1] = pos(0, 1, 2) },
		"path through void":   func(p *PersistedSession) { p.State.Path[1] = pos(0, 0, 1) },
		"path off map":        func(p *PersistedSession) { p.State.Path[1] = pos(0, -1, 0) },
		"stairs landing gone": func(p *PersistedSession) { p.State.Path = append(p.State.Path[:3:3], p.State.Path[4:]...) },
		"wrong landing":       func(p *PersistedSession) { p.State.Path[3] = pos(1, 1, 0) },
		"position mismatch":   func(p *PersistedSession) { p.State.Pos = pos(0, 0, 0) },
		"moves lie":           func(p *PersistedSession) { p.State.Moves = 2 },
		"negative moves":      func(p *PersistedSession) { p.State.Moves = -3 },
		"status WON lie":      func(p *PersistedSession) { p.State.Status = engine.StatusWon },
		"status READY lie":    func(p *PersistedSession) { p.State.Status = engine.StatusReady },
		"status LOST lie": func(p *PersistedSession) {
			p.State.Status, p.State.LossReason = engine.StatusLost, engine.LossMoveLimit
		},
		"loss reason on PLAYING": func(p *PersistedSession) { p.State.LossReason = engine.LossMoveLimit },
		"path longer than limit": func(p *PersistedSession) {
			for range 20 {
				p.State.Path = append(p.State.Path, pos(0, 3, 2))
			}
		},
		"negative attempts": func(p *PersistedSession) { p.Attempts = -1 },
		"best zero":         func(p *PersistedSession) { p.BestMoves = &zero },
		"best negative":     func(p *PersistedSession) { p.BestMoves = &neg },
		"best over limit":   func(p *PersistedSession) { p.BestMoves = &huge },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sp := newMemory(t)
			putPayload(t, sp, idA, mutate)
			if _, err := sp.Load(idA); err == nil {
				t.Fatal("invalid payload loaded")
			} else if errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid payload reported as not found: %v", err)
			}
		})
	}
}

func TestLoadAllSkipsCorruptPayloads(t *testing.T) {
	sp := newMemory(t)
	putPayload(t, sp, idA, nil)
	putPayload(t, sp, idB, func(p *PersistedSession) { p.SchemaVersion = 99 })
	if err := sp.store.Persist(sessionstore.Meta{ID: idC}, []byte(`{"schema_version":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := sp.store.Persist(sessionstore.Meta{ID: "0000000000000000"}, []byte("null")); err != nil {
		t.Fatal(err)
	}

	m := NewManager(sp, 10)
	if err := m.LoadAll(); err != nil {
		t.Fatalf("corrupt payloads must not be fatal: %v", err)
	}
	if m.Count() != 1 {
		t.Fatalf("loaded %d sessions, want 1", m.Count())
	}
	if _, ok := m.Get(idA); !ok {
		t.Fatal("valid session A was not loaded")
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
	sp := newMemory(t)
	m := NewManager(sp, 10)
	s := fixture(t, idA)
	if err := m.Put(s); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("Put must not persist; the service persists explicitly")
	}
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if got, err := sp.Load(idA); err != nil || got.Seq != s.Seq {
		t.Fatalf("Persist wrote %v: %v", got, err)
	}
	// Persist snapshots under the session lock.
	s.Lock()
	s.Seq = 99
	s.Unlock()
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if got, _ := sp.Load(idA); got.Seq != 99 {
		t.Fatalf("persisted seq = %d, want 99", got.Seq)
	}

	if err := m.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("Delete left the session stored")
	}
	// A late Persist (e.g. from a move racing the delete) must not resurrect it.
	if err := m.Persist(s); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("Persist resurrected a deleted session")
	}
}

func TestManagerDeleteRemovesUnloadedSession(t *testing.T) {
	sp := newMemory(t)
	putPayload(t, sp, idA, nil)
	m := NewManager(sp, 10) // LoadAll not called: only in the store
	if err := m.Delete(idA); err != nil {
		t.Fatalf("Delete of store-only session: %v", err)
	}
	if _, err := sp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatal("stored session not removed")
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
	sp := newMemory(t)
	m1 := NewManager(sp, 10)
	for _, id := range []string{idA, idB, idC} {
		s := fixture(t, id)
		if err := m1.Put(s); err != nil {
			t.Fatal(err)
		}
		if err := m1.Persist(s); err != nil {
			t.Fatal(err)
		}
	}
	m2 := NewManager(sp, 2) // fewer slots than persisted sessions
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
	sp := newMemory(t)
	m := NewManager(sp, 10)
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
	got, err := sp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	want := s.Seq
	s.Unlock()
	if got.Seq != want {
		t.Fatalf("last write wins: stored seq %d, live has %d", got.Seq, want)
	}
}

// TestPostgresPersistenceRoundTrip exercises the real durable backend.
func TestPostgresPersistenceRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	sp, err := NewPostgresPersistence(pool, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := fixture(t, idA)
	if err := sp.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := sp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != want.Seq || got.DisplayName != want.DisplayName || !reflect.DeepEqual(got.State, want.State) {
		t.Fatalf("restored %+v, want %+v", got, want)
	}
	ids, err := sp.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{idA}) {
		t.Fatalf("ListAll = %v", ids)
	}
	if err := sp.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Load(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after Delete = %v, want ErrNotFound", err)
	}
}
