package graph

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/graph/model"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
)

// fakeService overrides only what the tests exercise; any other call panics
// on the nil embedded interface, which flags an unexpected dependency.
type fakeService struct {
	service.GameService
	maps     map[string]*engine.Map
	meta     map[string]service.MapEntry
	builtin  map[string]bool
	sessions map[string]*service.Session
	saved    []engine.MapConfig
	deleted  []string
	err      error
	// editErr is returned by AuthorizeSessionEdit; edited records the actors
	// that were let through.
	editErr error
	edited  []service.SessionActor
}

func (f *fakeService) metaOf(id string) service.MapEntry {
	e := f.meta[id]
	if e.Status == "" {
		e.Status = engine.MapStatusPublished
	}
	e.Map = f.maps[id]
	return e
}

func (f *fakeService) GetMap(id string) (*engine.Map, error) {
	if f.err != nil {
		return nil, f.err
	}
	if m, ok := f.maps[id]; ok {
		return m, nil
	}
	return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
}

func (f *fakeService) GetMapFor(id, viewerID string) (*engine.Map, error) {
	e := f.metaOf(id)
	if e.Map == nil {
		return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	if !e.VisibleTo(viewerID) {
		return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	return e.Map, nil
}

func (f *fakeService) MapEntry(id string) (service.MapEntry, error) {
	e := f.metaOf(id)
	if e.Map == nil {
		return service.MapEntry{}, service.Errorf(service.CodeNotFound, "map %q not found", id)
	}
	return e, nil
}

func (f *fakeService) ListMaps(viewerID string) []*engine.Map {
	out := []*engine.Map{}
	for id, m := range f.maps {
		if f.metaOf(id).VisibleTo(viewerID) {
			out = append(out, m)
		}
	}
	return out
}

// IsBuiltinMap reports whether id is shipped; shipped maps need the admin key.
func (f *fakeService) IsBuiltinMap(id string) bool { return f.builtin[id] }

func (f *fakeService) SaveMap(cfg engine.MapConfig, actor service.MapActor) (*engine.Map, error) {
	m, err := engine.NewMap(cfg)
	if err != nil {
		return nil, err
	}
	f.saved = append(f.saved, cfg)
	if f.maps == nil {
		f.maps = map[string]*engine.Map{}
	}
	if f.meta == nil {
		f.meta = map[string]service.MapEntry{}
	}
	owner := actor.AccountID
	if old, ok := f.meta[cfg.ID]; ok && old.OwnerID != "" {
		owner = old.OwnerID
	}
	f.maps[cfg.ID] = m
	f.meta[cfg.ID] = service.MapEntry{Map: m, OwnerID: owner, Status: engine.MapStatusDraft}
	return m, nil
}

func (f *fakeService) CreateMap(cfg engine.MapConfig, ownerID string) (*engine.Map, error) {
	if f.err != nil {
		return nil, f.err
	}
	if _, ok := f.maps[cfg.ID]; ok {
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", cfg.ID)
	}
	return f.SaveMap(cfg, service.MapActor{AccountID: ownerID})
}

func (f *fakeService) PublishMap(id string, actor service.MapActor) (*engine.Map, error) {
	e, err := f.MapEntry(id)
	if err != nil {
		return nil, err
	}
	e.Status = engine.MapStatusPublished
	f.meta[id] = e
	return e.Map, nil
}

func (f *fakeService) DeleteMap(id string, actor service.MapActor) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeService) GetSession(id string) (*service.Session, error) {
	if s, ok := f.sessions[id]; ok {
		return s.Clone(), nil
	}
	return nil, service.Errorf(service.CodeNotFound, "session %q not found", id)
}

func (f *fakeService) SessionVisibleTo(s *service.Session, viewerID string) bool {
	entry, err := f.MapEntry(s.MapID)
	if err != nil {
		return true
	}
	return entry.VisibleTo(viewerID)
}

func (f *fakeService) SetProgram(id string, p engine.Program) (*service.Session, error) {
	return nil, f.err
}

// AuthorizeSessionEdit allows everything unless the test sets editErr, so the
// resolver tests that are not about edit permissions stay unaffected.
func (f *fakeService) AuthorizeSessionEdit(id string, actor service.SessionActor) error {
	if f.editErr != nil {
		return f.editErr
	}
	f.edited = append(f.edited, actor)
	return nil
}

func testMapConfig(id string) engine.MapConfig {
	return engine.MapConfig{
		ID: id, Name: id,
		Layout:         []string{"...", ">.*", "..."},
		MainTapeLength: 2, SubTapeLengths: []int{2},
	}
}

func mustMap(t *testing.T, id string) *engine.Map {
	t.Helper()
	m, err := engine.NewMap(testMapConfig(id))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var ge *gqlerror.Error
	if !errors.As(err, &ge) {
		t.Fatalf("want *gqlerror.Error, got %T (%v)", err, err)
	}
	code, _ := ge.Extensions["code"].(string)
	return code
}

// withViewer marks the context as a request from a signed-in player.
func withViewer(ctx context.Context) context.Context {
	return auth.WithViewer(ctx, &auth.Viewer{Account: &account.Account{ID: "acct1", Handle: "player"}})
}

func ctxWithKey(key string) context.Context {
	r, _ := http.NewRequest(http.MethodPost, "/graphql", nil)
	if key != "" {
		r.Header.Set("X-Admin-Key", key)
	}
	return context.WithValue(context.Background(), HTTPRequestKey{}, r)
}

func TestAdminGate(t *testing.T) {
	input := model.MapInput{ID: "new_map", Name: "n", Layout: testMapConfig("x").Layout}
	cases := []struct {
		name      string
		adminKey  string
		allowOpen bool
		ctx       context.Context
		wantCode  string // code for writes to a shipped map
	}{
		{"no key configured, not opted out", "", false, ctxWithKey(""), service.CodeForbidden},
		{"no key configured, opted out", "", true, ctxWithKey(""), ""},
		{"key configured, header missing", "secret", false, ctxWithKey(""), service.CodeForbidden},
		{"key configured, wrong header", "secret", false, ctxWithKey("nope"), service.CodeForbidden},
		{"key configured, correct header", "secret", false, ctxWithKey("secret"), ""},
		{"key configured, no request in ctx", "secret", false, context.Background(), service.CodeForbidden},
		{"opt-out ignored when key configured", "secret", true, ctxWithKey("nope"), service.CodeForbidden},
		{"key is prefix of header", "secret", false, ctxWithKey("secret-and-more"), service.CodeForbidden},
		{"header is prefix of key", "secret", false, ctxWithKey("secre"), service.CodeForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeService{
				maps:    map[string]*engine.Map{"existing": mustMap(t, "existing"), "mine": mustMap(t, "mine")},
				meta:    map[string]service.MapEntry{"mine": {OwnerID: "acct1", Status: engine.MapStatusPublished}},
				builtin: map[string]bool{"existing": true},
			}
			ctx := withViewer(tc.ctx)
			m := &mutationResolver{&Resolver{Service: fs, AdminPolicy: Admin{Key: tc.adminKey, AllowUnauthenticated: tc.allowOpen}}}

			check := func(op string, err error) {
				t.Helper()
				if tc.wantCode == "" {
					if err != nil {
						t.Fatalf("%s: unexpected error %v", op, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("%s: expected %s error, got none", op, tc.wantCode)
				}
				if got := codeOf(t, err); got != tc.wantCode {
					t.Fatalf("%s: code = %q, want %q", op, got, tc.wantCode)
				}
			}
			// Creating and validating need a signed-in player but no key.
			_, err := m.CreateMap(ctx, input)
			if err != nil {
				t.Fatalf("createMap: unexpected error %v", err)
			}
			_, err = m.ValidateMap(ctx, input)
			if err != nil {
				t.Fatalf("validateMap: unexpected error %v", err)
			}
			// Writes to a shipped map are gated.
			upd := input
			upd.ID = "existing"
			_, err = m.UpdateMap(ctx, upd)
			check("updateMap(builtin)", err)
			_, err = m.DeleteMap(ctx, "existing")
			check("deleteMap(builtin)", err)
			// The owner may change their own map without the key.
			upd.ID = "mine"
			if _, err := m.UpdateMap(ctx, upd); err != nil {
				t.Fatalf("updateMap(player map): unexpected error %v", err)
			}
			if _, err := m.DeleteMap(ctx, "mine"); err != nil {
				t.Fatalf("deleteMap(player map): unexpected error %v", err)
			}
			if tc.wantCode != "" {
				for _, cfg := range fs.saved {
					if cfg.ID == "existing" {
						t.Fatalf("shipped map saved despite gate: %v", fs.saved)
					}
				}
				for _, id := range fs.deleted {
					if id == "existing" {
						t.Fatalf("shipped map deleted despite gate: %v", fs.deleted)
					}
				}
			}
		})
	}
}

// Creating a map needs a signed-in player: a guest is refused and nothing is
// stored. An existing id (including a shipped one) is a conflict, so creation
// cannot overwrite anything.
func TestCreateMapNeedsSignIn(t *testing.T) {
	layout := testMapConfig("x").Layout
	fs := &fakeService{maps: map[string]*engine.Map{"shipped": mustMap(t, "shipped")}, builtin: map[string]bool{"shipped": true}}
	m := &mutationResolver{&Resolver{Service: fs}}
	guest := context.Background()
	signedIn := withViewer(guest)

	if _, err := m.CreateMap(guest, model.MapInput{ID: "player_map", Name: "n", Layout: layout}); codeOf(t, err) != service.CodeUnauthenticated {
		t.Fatalf("guest create: err = %v", err)
	}
	if len(fs.saved) != 0 {
		t.Fatalf("guest create reached the store: %v", fs.saved)
	}
	if _, err := m.CreateMap(signedIn, model.MapInput{ID: "shipped", Name: "n", Layout: layout}); codeOf(t, err) != service.CodeInvalidArgument {
		t.Fatalf("create over shipped map: err = %v", err)
	}
	got, err := m.CreateMap(signedIn, model.MapInput{ID: "player_map", Name: "n", Layout: layout})
	if err != nil || got.ID != "player_map" {
		t.Fatalf("signed-in create = %+v, %v", got, err)
	}
	// Validation stores nothing, but it is still a map operation for players.
	if _, err := m.ValidateMap(guest, model.MapInput{ID: "player_map", Name: "n", Layout: layout}); codeOf(t, err) != service.CodeUnauthenticated {
		t.Fatalf("guest validate: err = %v", err)
	}
	if _, err := m.ValidateMap(signedIn, model.MapInput{ID: "player_map", Name: "n", Layout: layout}); err != nil {
		t.Fatalf("validateMap: %v", err)
	}
}

func TestCreateUpdateMapSemantics(t *testing.T) {
	fs := &fakeService{maps: map[string]*engine.Map{"existing": mustMap(t, "existing")}}
	m := &mutationResolver{&Resolver{Service: fs}}
	ctx := withViewer(context.Background())
	layout := testMapConfig("x").Layout

	if _, err := m.CreateMap(ctx, model.MapInput{ID: "existing", Name: "n", Layout: layout}); codeOf(t, err) != service.CodeInvalidArgument {
		t.Fatalf("create existing: err = %v", err)
	}
	if _, err := m.UpdateMap(ctx, model.MapInput{ID: "missing", Name: "n", Layout: layout}); codeOf(t, err) != service.CodeNotFound {
		t.Fatalf("update missing: err = %v", err)
	}
	// Invalid layout from the store surfaces as INVALID_ARGUMENT.
	if _, err := m.CreateMap(ctx, model.MapInput{ID: "bad", Name: "n", Layout: []string{"."}}); codeOf(t, err) != service.CodeInvalidArgument {
		t.Fatalf("create invalid: err = %v", err)
	}
	got, err := m.CreateMap(ctx, model.MapInput{ID: "fresh", Name: "n", Layout: layout})
	if err != nil || got.ID != "fresh" || got.Start.X != 0 || got.Start.Y != 1 || got.StartFacing != model.FacingRight {
		t.Fatalf("create fresh = %+v, %v", got, err)
	}
}

// An explicit empty subTapeLengths means zero subs; omitted means the engine
// default. The distinction must survive the input conversion.
func TestCreateMapSubTapeLengthsEmptyVsOmitted(t *testing.T) {
	layout := testMapConfig("x").Layout
	cases := []struct {
		name string
		in   []int
		want []int
	}{
		{"omitted", nil, []int{engine.DefaultSubTape}},
		{"empty", []int{}, []int{}},
		{"explicit", []int{4, 5}, []int{4, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &mutationResolver{&Resolver{Service: &fakeService{}}}
			got, err := m.CreateMap(withViewer(context.Background()), model.MapInput{ID: "fresh", Name: "n", Layout: layout, SubTapeLengths: tc.in})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.SubTapeLengths) != len(tc.want) {
				t.Fatalf("subTapeLengths = %v, want %v", got.SubTapeLengths, tc.want)
			}
			for i := range tc.want {
				if got.SubTapeLengths[i] != tc.want[i] {
					t.Fatalf("subTapeLengths = %v, want %v", got.SubTapeLengths, tc.want)
				}
			}
		})
	}
}

func TestToGQLErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"coded", service.Errorf(service.CodeSessionPlaying, "playing"), service.CodeSessionPlaying},
		{"wrapped coded", errors.Join(errors.New("ctx"), service.Errorf(service.CodeNotFound, "x")), service.CodeNotFound},
		{"map validation", &engine.ValidationError{Issues: []string{"bad"}}, service.CodeInvalidArgument},
		{"program error", &engine.ProgramError{}, service.CodeInvalidProgram},
		{"unknown", errors.New("disk on fire"), CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codeOf(t, toGQLError(tc.err)); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
	if err := toGQLError(errors.New("secret detail")); err.Error() == "secret detail" {
		t.Fatal("internal error message leaked to client")
	}
	if toGQLError(nil) != nil {
		t.Fatal("nil must map to nil")
	}
}

func TestSetProgramPropagatesCode(t *testing.T) {
	m := mustMap(t, "visible")
	fs := &fakeService{
		err:      service.Errorf(service.CodeSessionPlaying, "session is playing"),
		maps:     map[string]*engine.Map{"visible": m},
		meta:     map[string]service.MapEntry{"visible": {Status: engine.MapStatusPublished}},
		sessions: map[string]*service.Session{"s": {ID: "s", MapID: "visible", Map: m}},
	}
	r := &mutationResolver{&Resolver{Service: fs}}
	_, err := r.SetProgram(context.Background(), "s", model.ProgramInput{}, nil)
	if codeOf(t, err) != service.CodeSessionPlaying {
		t.Fatalf("err = %v", err)
	}
}

func TestQueryNotFoundIsNull(t *testing.T) {
	q := &queryResolver{&Resolver{Service: &fakeService{}}}
	if s, err := q.Session(context.Background(), "nope", nil); s != nil || err != nil {
		t.Fatalf("session = %v, %v; want nil,nil", s, err)
	}
	if m, err := q.Map(context.Background(), "nope"); m != nil || err != nil {
		t.Fatalf("map = %v, %v; want nil,nil", m, err)
	}
}

func TestSessionConversionDerivesTreatsRemaining(t *testing.T) {
	m := mustMap(t, "m")
	vm := engine.NewVMState(m)
	s := &service.Session{ID: "s", MapID: "m", Map: m, VM: vm, Seq: 3, BestSteps: 0}
	got := toSession(s, true)
	if len(got.VM.TreatsRemaining) != 1 || got.VM.TreatsRemaining[0].X != 2 || got.VM.TreatsRemaining[0].Y != 1 {
		t.Fatalf("treatsRemaining = %+v", got.VM.TreatsRemaining)
	}
	if got.BestSteps != nil {
		t.Fatal("bestSteps must be null before first win")
	}
	if got.VM.LossReason != nil || got.LastEvent != nil {
		t.Fatal("lossReason/lastEvent must be null")
	}
	if got.Seq != 3 || got.VM.Facing != model.FacingRight {
		t.Fatalf("seq/facing = %d/%s", got.Seq, got.VM.Facing)
	}
}

func TestStepEventConversionKeepsBlockedFlags(t *testing.T) {
	// A move into a rock and an ignored recursive call are different no-ops;
	// clients rely on the distinction to explain the step they just spent.
	got := toStepEvent(&engine.StepEvent{Step: 2, Instruction: engine.CallSub1, CallBlocked: true})
	if !got.CallBlocked || got.Blocked {
		t.Fatalf("callBlocked=%v blocked=%v, want true/false", got.CallBlocked, got.Blocked)
	}
	got = toStepEvent(&engine.StepEvent{Step: 3, Instruction: engine.MoveForward, Blocked: true})
	if got.CallBlocked || !got.Blocked {
		t.Fatalf("callBlocked=%v blocked=%v, want false/true", got.CallBlocked, got.Blocked)
	}
}

func liveSession(t *testing.T, seq uint64) *service.Session {
	t.Helper()
	m := mustMap(t, "m")
	return &service.Session{ID: "s1", MapID: "m", Map: m, VM: engine.NewVMState(m), Seq: seq}
}

func TestForwardInitialThenUpdatesAndSeqFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan *service.Session, 4)
	out := forwardSessionUpdates(ctx, liveSession(t, 5), updates, 4, true)

	recv := func() *model.SessionUpdate {
		t.Helper()
		select {
		case u, ok := <-out:
			if !ok {
				t.Fatal("channel closed early")
			}
			return u
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
			return nil
		}
	}
	if u := recv(); u.Seq != 5 {
		t.Fatalf("initial seq = %d, want 5", u.Seq)
	}
	updates <- liveSession(t, 5) // duplicate of initial: dropped
	updates <- liveSession(t, 4) // stale: dropped
	updates <- liveSession(t, 6)
	if u := recv(); u.Seq != 6 || u.Session.Seq != 6 {
		t.Fatalf("next seq = %d, want 6", u.Seq)
	}
}

func goroutinesSettle(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: %d running, baseline %d", runtime.NumGoroutine(), base)
}

func TestForwardEndsOnCancelWithoutReader(t *testing.T) {
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan *service.Session)
	// Buffer 1 and nobody reading: the initial send fits, the next blocks.
	out := forwardSessionUpdates(ctx, liveSession(t, 1), updates, 1, true)
	go func() { updates <- liveSession(t, 2) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	for range out { // drains until close; close proves the goroutine exited
	}
	goroutinesSettle(t, base)
}

func TestSubscriptionResolverEmitsSnapshotAndStopsOnCancel(t *testing.T) {
	hub := websocket.NewHub()
	fs := &fakeService{sessions: map[string]*service.Session{"s1": liveSession(t, 2)}}
	sub := &subscriptionResolver{&Resolver{Service: fs, Hub: hub}}

	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := sub.SessionUpdated(ctx, "s1", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case u := <-ch:
		if u.Seq != 2 || u.Session.ID != "s1" {
			t.Fatalf("initial = %+v", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no initial snapshot")
	}
	next := liveSession(t, 3)
	hub.Broadcast(next)
	select {
	case u := <-ch:
		if u.Seq != 3 {
			t.Fatalf("update seq = %d", u.Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no hub update forwarded")
	}
	cancel()
	for range ch {
	}
	goroutinesSettle(t, base)

	if _, err := sub.SessionUpdated(context.Background(), "missing", nil); codeOf(t, err) != service.CodeNotFound {
		t.Fatalf("missing session err = %v", err)
	}
}
