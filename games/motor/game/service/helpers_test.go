package service_test

import (
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// ---- maps ------------------------------------------------------------------

var (
	// corridor: 5x3, start (0,1) facing right, one treat at (4,1): 4 forward moves win.
	corridorCfg = engine.MapConfig{
		ID: "corridor", Name: "Corridor", Layout: []string{".....", ">...*", "....."},
		MainTapeLength: 6, SubTapeLengths: []int{2}, MaxSteps: 30, MaxCallDepth: 8,
	}
	// longCfg: 20x3, 19 forward moves to win (slow enough to overflow a subscriber buffer).
	longCfg = engine.MapConfig{
		ID: "long", Name: "Long", Layout: []string{"....................", ">..................*", "...................."},
		MainTapeLength: 20, SubTapeLengths: []int{1}, MaxSteps: 100, MaxCallDepth: 8,
	}
	// spinCfg: a recursion map whose spin program never wins and ends by STEP_LIMIT
	// after 100 steps, so a 50 ms run lasts about 5 s unless stopped.
	spinCfg = engine.MapConfig{
		ID: "spin", Name: "Spin", Layout: []string{".....", ">...*", "....."},
		MainTapeLength: 3, SubTapeLengths: []int{2}, MaxSteps: 100, MaxCallDepth: 64, AllowRecursion: true,
	}
	// noSubsCfg has zero subs (sub_tape_lengths: []).
	noSubsCfg = engine.MapConfig{
		ID: "nosubs", Name: "No subs", Layout: []string{".....", ">...*", "....."},
		MainTapeLength: 4, SubTapeLengths: []int{}, MaxSteps: 30,
	}
)

func mustMap(t testing.TB, cfg engine.MapConfig) *engine.Map {
	t.Helper()
	m, err := engine.NewMap(cfg)
	if err != nil {
		t.Fatalf("NewMap(%s): %v", cfg.ID, err)
	}
	return m
}

// fakeMaps is an in-memory MapStore built from engine.NewMap.
type fakeMaps struct {
	mu   sync.Mutex
	m    map[string]*engine.Map
	meta map[string]service.MapEntry
}

func newFakeMaps(t testing.TB, cfgs ...engine.MapConfig) *fakeMaps {
	fm := &fakeMaps{m: make(map[string]*engine.Map), meta: make(map[string]service.MapEntry)}
	for _, c := range cfgs {
		mp := mustMap(t, c)
		fm.m[c.ID] = mp
		fm.meta[c.ID] = service.MapEntry{Map: mp, Status: engine.MapStatusPublished}
	}
	return fm
}

func (f *fakeMaps) List() []*engine.Map {
	entries := f.ListEntries()
	out := make([]*engine.Map, len(entries))
	for i, e := range entries {
		out[i] = e.Map
	}
	return out
}

func (f *fakeMaps) ListEntries() []service.MapEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]service.MapEntry, 0, len(f.m))
	for id, m := range f.m {
		e := f.meta[id]
		e.Map = m
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Map.ID < out[j].Map.ID })
	return out
}

func (f *fakeMaps) Get(id string) (*engine.Map, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.m[id]
	if !ok {
		return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	return m, nil
}

func (f *fakeMaps) Entry(id string) (service.MapEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.m[id]
	if !ok {
		return service.MapEntry{}, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	e := f.meta[id]
	e.Map = m
	return e, nil
}

// Builtin reports false: every fake map is player-created, so writes are open.
func (f *fakeMaps) Builtin(string) bool { return false }

func (f *fakeMaps) Save(cfg engine.MapConfig, actor service.MapActor) (*engine.Map, error) {
	return f.put(cfg, actor.AccountID, false)
}

func (f *fakeMaps) Create(cfg engine.MapConfig, ownerID string) (*engine.Map, error) {
	return f.put(cfg, ownerID, true)
}

func (f *fakeMaps) put(cfg engine.MapConfig, ownerID string, createOnly bool) (*engine.Map, error) {
	m, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old, exists := f.meta[m.ID]
	if createOnly && exists {
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", m.ID)
	}
	owner := ownerID
	if !createOnly && exists && old.OwnerID != "" {
		owner = old.OwnerID
	}
	f.m[m.ID] = m
	f.meta[m.ID] = service.MapEntry{Map: m, OwnerID: owner, Status: engine.MapStatusDraft}
	return m, nil
}

func (f *fakeMaps) Publish(id string, actor service.MapActor) (*engine.Map, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.m[id]
	if !ok {
		return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	e := f.meta[id]
	e.Status = engine.MapStatusPublished
	f.meta[id] = e
	return m, nil
}

func (f *fakeMaps) Delete(id string, actor service.MapActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[id]; !ok {
		return service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	delete(f.m, id)
	delete(f.meta, id)
	return nil
}

// memAccounts is a permissive account Repository for service tests that need
// CreateOwnedSession without PostgreSQL: any non-empty id resolves.
type memAccounts struct{}

func (memAccounts) Create(account.Account) error { return nil }
func (memAccounts) Get(id string) (account.Account, bool) {
	if id == "" {
		return account.Account{}, false
	}
	return account.Account{ID: id, Handle: id, DisplayName: id, ProfilePublic: true}, true
}
func (memAccounts) ByIdentity(string, string) (account.Account, bool) {
	return account.Account{}, false
}
func (memAccounts) ByHandle(string) (account.Account, bool) { return account.Account{}, false }
func (memAccounts) Update(string, func(*account.Account) error) (account.Account, error) {
	return account.Account{}, nil
}
func (memAccounts) Delete(string) error { return nil }
func (memAccounts) Count() int          { return 0 }

// ---- persistence spy -------------------------------------------------------

// spyPersistence is an in-memory SessionPersistence that counts saves.
type spyPersistence struct {
	mu    sync.Mutex
	saved map[string]*service.Session
	saves map[string]int
}

func newSpyPersistence() *spyPersistence {
	return &spyPersistence{saved: make(map[string]*service.Session), saves: make(map[string]int)}
}

func (p *spyPersistence) Save(s *service.Session) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saved[s.ID] = s
	p.saves[s.ID]++
	return nil
}

func (p *spyPersistence) Load(id string) (*service.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.saved[id]
	if !ok {
		return nil, session.ErrNotFound
	}
	return s.Clone(), nil
}

func (p *spyPersistence) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.saved[id]; !ok {
		return session.ErrNotFound
	}
	delete(p.saved, id)
	return nil
}

func (p *spyPersistence) ListAll() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.saved))
	for id := range p.saved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (p *spyPersistence) saveCount(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saves[id]
}

// latest returns the most recently saved snapshot for id (nil if none).
func (p *spyPersistence) latest(id string) *service.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saved[id]
}

// ---- broadcaster recorder --------------------------------------------------

type recorder struct {
	mu    sync.Mutex
	snaps []*service.Session
}

func (r *recorder) Broadcast(s *service.Session) {
	r.mu.Lock()
	r.snaps = append(r.snaps, s)
	r.mu.Unlock()
}

func (r *recorder) forSession(id string) []*service.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*service.Session
	for _, s := range r.snaps {
		if s.ID == id {
			out = append(out, s)
		}
	}
	return out
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.snaps)
}

// ---- environment -----------------------------------------------------------

type env struct {
	svc  service.GameService
	mgr  *session.Manager
	pers *spyPersistence
	maps *fakeMaps
	rec  *recorder
}

// acctEnv adds a real PostgreSQL-backed account service to env.
type acctEnv struct {
	*env
	store    *account.PostgresStore
	accounts *account.Service
}

func newAcctEnv(t *testing.T, opts service.Options) *acctEnv {
	t.Helper()
	store := account.NewPostgresStore(pgtest.Pool(t))
	accounts := account.New(account.Options{Store: store})
	opts.Accounts = accounts
	e := newEnv(t, envOpts{svcOpts: opts}, wideCfg, corridorCfg, longCfg, spinCfg, noSubsCfg)
	return &acctEnv{env: e, store: store, accounts: accounts}
}

func (e *acctEnv) signIn(t *testing.T, handle string) *account.Account {
	t.Helper()
	a, _, err := e.accounts.SignIn("dev", handle)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type envOpts struct {
	maxSessions int
	svcOpts     service.Options
	bc          service.Broadcaster // nil = recorder
}

// newEnv wires a service over fake maps, a spy persistence and a recorder.
// The service is shut down when the test ends.
func newEnv(t *testing.T, o envOpts, cfgs ...engine.MapConfig) *env {
	t.Helper()
	if len(cfgs) == 0 {
		cfgs = []engine.MapConfig{corridorCfg, longCfg, spinCfg, noSubsCfg}
	}
	e := &env{maps: newFakeMaps(t, cfgs...), pers: newSpyPersistence(), rec: &recorder{}}
	e.mgr = session.NewManager(e.pers, o.maxSessions)
	var bc service.Broadcaster = e.rec
	if o.bc != nil {
		bc = o.bc
	}
	if o.svcOpts.Accounts == nil {
		o.svcOpts.Accounts = account.New(account.Options{Store: memAccounts{}})
	}
	e.svc = service.New(e.maps, e.mgr, bc, o.svcOpts)
	t.Cleanup(e.svc.Shutdown)
	return e
}

// ---- helpers ---------------------------------------------------------------

func pad(n int, ins ...engine.Instruction) []engine.Instruction {
	out := make([]engine.Instruction, n)
	for i := range out {
		out[i] = engine.Empty
	}
	copy(out, ins)
	return out
}

// moves returns n MOVE_FORWARD instructions.
func moves(n int) []engine.Instruction {
	out := make([]engine.Instruction, n)
	for i := range out {
		out[i] = engine.MoveForward
	}
	return out
}

// corridorFast wins corridor in 4 steps; corridorSlow wins it in 6.
func corridorFast() engine.Program {
	return engine.Program{Main: pad(6, moves(4)...), Subs: [][]engine.Instruction{pad(2)}}
}

func corridorSlow() engine.Program {
	main := append([]engine.Instruction{engine.TurnLeft, engine.TurnRight}, moves(4)...)
	return engine.Program{Main: main, Subs: [][]engine.Instruction{pad(2)}}
}

// corridorLose ends after one step without reaching the treat.
func corridorLose() engine.Program {
	return engine.Program{Main: pad(6, engine.TurnLeft), Subs: [][]engine.Instruction{pad(2)}}
}

func longWin() engine.Program {
	return engine.Program{Main: pad(20, moves(19)...), Subs: [][]engine.Instruction{pad(1)}}
}

// spinProgram never wins: sub1 turns and calls itself.
func spinProgram() engine.Program {
	return engine.Program{
		Main: pad(3, engine.CallSub1),
		Subs: [][]engine.Instruction{{engine.TurnLeft, engine.CallSub1}},
	}
}

func wantCode(t testing.TB, err error, code string) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) {
		t.Fatalf("error = %v (%T), want *service.Error with code %s", err, err, code)
	}
	if se.Code != code {
		t.Fatalf("error code = %s (%v), want %s", se.Code, err, code)
	}
}

func waitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (e *env) get(t testing.TB, id string) *service.Session {
	t.Helper()
	s, err := e.svc.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession(%s): %v", id, err)
	}
	return s
}

func (e *env) create(t testing.TB, mapID string) *service.Session {
	t.Helper()
	s, err := e.svc.CreateSession(mapID)
	if err != nil {
		t.Fatalf("CreateSession(%s): %v", mapID, err)
	}
	return s
}

func (e *env) setProgram(t testing.TB, id string, p engine.Program) *service.Session {
	t.Helper()
	s, err := e.svc.SetProgram(id, p)
	if err != nil {
		t.Fatalf("SetProgram: %v", err)
	}
	return s
}

func (e *env) run(t testing.TB, id string, speed int) {
	t.Helper()
	if _, err := e.svc.Run(id, speed); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// waitTerminal waits until the session's VM is terminal and playback stopped.
func (e *env) waitTerminal(t testing.TB, id string) *service.Session {
	t.Helper()
	var s *service.Session
	waitFor(t, "terminal VM", func() bool {
		s = e.get(t, id)
		return s.VM.Status.Terminal() && !s.Playing
	})
	return s
}

// waitSteps waits until the VM has executed at least n steps.
func (e *env) waitSteps(t testing.TB, id string, n int) {
	t.Helper()
	waitFor(t, "steps", func() bool { return e.get(t, id).VM.Steps >= n })
}
