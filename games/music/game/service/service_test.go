package service_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/game/session"
	"github.com/wricardo/drbrain/platform/mapstore"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

type fakePieces struct {
	pieces map[string]*score.Piece
}

func (f *fakePieces) List() ([]score.Entry, error) {
	out := make([]score.Entry, 0, len(f.pieces))
	for id, p := range f.pieces {
		out = append(out, score.Entry{ID: id, Piece: p.Clone()})
	}
	return out, nil
}

func (f *fakePieces) Get(id string) (*score.Piece, error) {
	p, ok := f.pieces[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", mapstore.ErrNotFound, id)
	}
	return p.Clone(), nil
}

// testPiece builds n distinct bars with the positional ids a loaded score
// would have.
func testPiece(n int) *score.Piece {
	measures := make([]score.Measure, n)
	for i := range measures {
		measures[i] = score.Measure{
			ID:  fmt.Sprintf("m%03d", i+1),
			Len: 480,
			Notes: []score.Note{
				{Start: 0, Dur: 120, Pitch: 60 + i%12},
				{Start: 240, Dur: 180, Pitch: 72 + i%5},
			},
		}
	}
	return &score.Piece{
		Version: score.Version, ID: "scale", Title: "Scale", Composer: "Anonymous",
		TempoBPM: 80, TimeSig: "4/4", Key: "C major", Clef: score.ClefTreble,
		Axis: score.Axis, Measures: measures,
	}
}

type recorder struct {
	mu    sync.Mutex
	snaps []*service.Session
}

func (r *recorder) Broadcast(s *service.Session) {
	r.mu.Lock()
	r.snaps = append(r.snaps, s)
	r.mu.Unlock()
}

func (r *recorder) last() *service.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.snaps) == 0 {
		return nil
	}
	return r.snaps[len(r.snaps)-1]
}

func newServiceWithPersistence(t *testing.T, persistence session.SessionPersistence) (service.GameService, *fakePieces, *recorder) {
	t.Helper()
	pieces := &fakePieces{pieces: map[string]*score.Piece{
		"scale": testPiece(score.MaxMeasures),
		"tiny":  testPiece(1),
	}}
	manager := session.NewManager(persistence, 100)
	if err := manager.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	rec := &recorder{}
	return service.New(pieces, manager, rec, service.Options{}), pieces, rec
}

// newServiceAt returns a service backed by the PostgreSQL store in pool.
func newServiceAt(t *testing.T, pool *pgxpool.Pool) (service.GameService, *fakePieces, *recorder) {
	t.Helper()
	persistence, err := session.NewPostgresPersistence(pool, 100)
	if err != nil {
		t.Fatalf("NewPostgresPersistence: %v", err)
	}
	return newServiceWithPersistence(t, persistence)
}

// newService returns a service backed by an in-memory store; it covers logic
// that does not need durability.
func newService(t *testing.T) (service.GameService, *fakePieces, *recorder) {
	t.Helper()
	return newServiceWithPersistence(t, session.NewMemoryPersistence(100))
}

func mustCreate(t *testing.T, svc service.GameService, piece string, d engine.Difficulty, seed int64) *service.Session {
	t.Helper()
	s, err := svc.CreateSession(piece, d, "Ada", seed)
	if err != nil {
		t.Fatalf("CreateSession(%s, %s): %v", piece, d, err)
	}
	return s
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *service.Error", err)
	}
	if se.Code != code {
		t.Fatalf("code = %q, want %q (%v)", se.Code, code, err)
	}
}

func targetArrangement(piece *score.Piece, n int) []engine.Placement {
	out := make([]engine.Placement, n)
	for i := range out {
		out[i] = engine.Placement{MeasureID: piece.Measures[i].ID}
	}
	return out
}

func TestCreateSession_SeedAndDefaults(t *testing.T) {
	svc, _, _ := newService(t)

	random := mustCreate(t, svc, "scale", engine.Novice, 0)
	if random.DisplayName != "Ada" {
		t.Fatalf("display name = %q", random.DisplayName)
	}
	if random.Seed <= 0 {
		t.Fatalf("seed = %d, want a drawn positive seed", random.Seed)
	}
	if random.Seq != 1 || random.Solved || random.Wins != 0 {
		t.Fatalf("session = %+v", random)
	}
	if len(random.Slots) != score.MaxMeasures {
		t.Fatalf("slots = %d, want %d", len(random.Slots), score.MaxMeasures)
	}
	if random.Piece == nil || random.Piece.ID != "scale" {
		t.Fatalf("piece = %+v", random.Piece)
	}

	first := mustCreate(t, svc, "scale", engine.Expert, 42)
	second := mustCreate(t, svc, "scale", engine.Expert, 42)
	if !reflect.DeepEqual(first.Slots, second.Slots) {
		t.Fatalf("same seed produced different slots:\n%+v\n%+v", first.Slots, second.Slots)
	}
}

func TestCreateSession_Errors(t *testing.T) {
	svc, _, _ := newService(t)

	_, err := svc.CreateSession("missing", engine.Novice, "", 1)
	wantCode(t, err, service.CodeNotFound)

	_, err = svc.CreateSession("scale", engine.Difficulty(0), "", 1)
	wantCode(t, err, service.CodeInvalidArgument)

	_, err = svc.CreateSession("tiny", engine.Genius, "", 1)
	wantCode(t, err, service.CodeInvalidArgument)

	_, err = svc.CreateSession("scale", engine.Novice, strings.Repeat("a", 41), 1)
	wantCode(t, err, service.CodeInvalidArgument)
}

func TestSetArrangement_SolvesWholePiece(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Genius, 37)
	if len(s.Slots) != score.MaxMeasures {
		t.Fatalf("slots = %d, want all %d bars", len(s.Slots), score.MaxMeasures)
	}
	if s.Solved {
		t.Fatal("new session is already solved")
	}

	target := targetArrangement(s.Piece, len(s.Piece.Measures))
	got, err := svc.SetArrangement(s.ID, target)
	if err != nil {
		t.Fatalf("SetArrangement: %v", err)
	}
	if !got.Solved || got.Wins != 1 {
		t.Fatalf("solved = %v, wins = %d", got.Solved, got.Wins)
	}
}

func TestSwitchMeasures_SwapsAndBumpsSeq(t *testing.T) {
	svc, _, rec := newService(t)
	s := mustCreate(t, svc, "scale", engine.Expert, 7)
	before := append([]engine.Placement(nil), s.Slots...)

	got, err := svc.SwitchMeasures(s.ID, 0, 3)
	if err != nil {
		t.Fatalf("SwitchMeasures: %v", err)
	}
	if got.Seq != s.Seq+1 {
		t.Fatalf("seq = %d, want %d", got.Seq, s.Seq+1)
	}
	if got.Slots[0] != before[3] || got.Slots[3] != before[0] {
		t.Fatalf("slots not swapped: %+v", got.Slots)
	}
	if got.LastChange == nil || got.LastChange.Kind != service.KindSwitch || got.LastChange.A != 0 || got.LastChange.B != 3 {
		t.Fatalf("last change = %+v", got.LastChange)
	}
	if rec.last() == nil || rec.last().Seq != got.Seq {
		t.Fatal("mutation was not broadcast")
	}

	_, err = svc.SwitchMeasures(s.ID, -1, 0)
	wantCode(t, err, service.CodeInvalidArgument)
	unchanged, err := svc.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if unchanged.Seq != got.Seq {
		t.Fatalf("rejected mutation bumped seq to %d", unchanged.Seq)
	}
}

func TestSwitchMeasures_NoOpStillBumpsSeq(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Novice, 3)

	got, err := svc.SwitchMeasures(s.ID, 2, 2)
	if err != nil {
		t.Fatalf("SwitchMeasures: %v", err)
	}
	if got.Seq != s.Seq+1 {
		t.Fatalf("seq = %d, want %d", got.Seq, s.Seq+1)
	}
}

func TestTransformMeasure_SetsFlips(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Expert, 11)

	got, err := svc.TransformMeasure(s.ID, 2, engine.Transform{FlipH: true, FlipV: true})
	if err != nil {
		t.Fatalf("TransformMeasure: %v", err)
	}
	want := engine.Transform{FlipH: true, FlipV: true}
	if got.Slots[2].Transform != want {
		t.Fatalf("transform = %+v, want %+v", got.Slots[2].Transform, want)
	}
	if got.LastChange == nil || got.LastChange.Kind != service.KindTransform || got.LastChange.A != 2 {
		t.Fatalf("last change = %+v", got.LastChange)
	}

	_, err = svc.TransformMeasure(s.ID, len(s.Slots), engine.Transform{})
	wantCode(t, err, service.CodeInvalidArgument)
}

func TestSetArrangement_RejectsNonPermutation(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Novice, 5)
	valid := append([]engine.Placement(nil), s.Slots...)

	duplicate := append([]engine.Placement(nil), valid...)
	duplicate[1].MeasureID = duplicate[0].MeasureID
	foreign := append([]engine.Placement(nil), valid...)
	foreign[0].MeasureID = "m999"

	for _, slots := range map[string][]engine.Placement{
		"duplicate": duplicate,
		"foreign":   foreign,
		"short":     valid[:len(valid)-1],
	} {
		_, err := svc.SetArrangement(s.ID, slots)
		wantCode(t, err, service.CodeInvalidArgument)
	}

	unchanged, err := svc.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if unchanged.Seq != s.Seq {
		t.Fatalf("rejected arrangements bumped seq to %d", unchanged.Seq)
	}
}

func TestSetArrangement_ReplacesOrderAndTransforms(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Novice, 6)

	reversed := make([]engine.Placement, len(s.Slots))
	for i, p := range s.Slots {
		reversed[len(s.Slots)-1-i] = engine.Placement{
			MeasureID: p.MeasureID,
			Transform: engine.Transform{FlipH: i%2 == 0},
		}
	}
	got, err := svc.SetArrangement(s.ID, reversed)
	if err != nil {
		t.Fatalf("SetArrangement: %v", err)
	}
	if !reflect.DeepEqual(got.Slots, reversed) {
		t.Fatalf("slots = %+v, want %+v", got.Slots, reversed)
	}
	if got.LastChange == nil || got.LastChange.Kind != service.KindArrange {
		t.Fatalf("last change = %+v", got.LastChange)
	}
}

func TestResetArrangement_RestoresStart(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Expert, 9)
	start := append([]engine.Placement(nil), s.Slots...)

	if _, err := svc.SwitchMeasures(s.ID, 0, 1); err != nil {
		t.Fatalf("SwitchMeasures: %v", err)
	}
	if _, err := svc.TransformMeasure(s.ID, 2, engine.Transform{FlipH: true}); err != nil {
		t.Fatalf("TransformMeasure: %v", err)
	}
	got, err := svc.ResetArrangement(s.ID)
	if err != nil {
		t.Fatalf("ResetArrangement: %v", err)
	}
	if !reflect.DeepEqual(got.Slots, start) {
		t.Fatalf("reset left %+v, want %+v", got.Slots, start)
	}
	if got.LastChange == nil || got.LastChange.Kind != service.KindReset {
		t.Fatalf("last change = %+v", got.LastChange)
	}
	if got.Solved {
		t.Fatal("reset arrangement reported solved")
	}
}

func TestWins_IncrementOnceOnSolve(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Expert, 13)
	target := targetArrangement(s.Piece, len(s.Slots))

	solved, err := svc.SetArrangement(s.ID, target)
	if err != nil {
		t.Fatalf("SetArrangement: %v", err)
	}
	if !solved.Solved || solved.Wins != 1 {
		t.Fatalf("solved = %v, wins = %d", solved.Solved, solved.Wins)
	}
	if solved.LastChange == nil || solved.LastChange.Kind != service.KindSolve {
		t.Fatalf("last change = %+v", solved.LastChange)
	}

	again, err := svc.SetArrangement(s.ID, target)
	if err != nil {
		t.Fatalf("SetArrangement again: %v", err)
	}
	if again.Wins != 1 {
		t.Fatalf("wins = %d after repeating the solved arrangement", again.Wins)
	}

	scrambled := append([]engine.Placement(nil), target...)
	if _, err := svc.SwitchMeasures(s.ID, 0, 1); err != nil {
		t.Fatalf("SwitchMeasures: %v", err)
	}
	if _, err := svc.SetArrangement(s.ID, scrambled); err != nil {
		t.Fatalf("SetArrangement: %v", err)
	}
	second, err := svc.SetArrangement(s.ID, target)
	if err != nil {
		t.Fatalf("SetArrangement: %v", err)
	}
	if second.Wins != 2 {
		t.Fatalf("wins = %d after a second solve, want 2", second.Wins)
	}
}

func TestMutations_ConcurrentSeqMonotonic(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Expert, 17)

	const workers = 40
	var wg sync.WaitGroup
	seqs := make(chan uint64, workers)
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := svc.SwitchMeasures(s.ID, 0, 1)
			if err != nil {
				errs <- err
				return
			}
			seqs <- got.Seq
		}()
	}
	wg.Wait()
	close(seqs)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent mutation: %v", err)
	}

	seen := make(map[uint64]bool, workers)
	var max uint64
	for seq := range seqs {
		if seen[seq] {
			t.Fatalf("seq %d was returned twice", seq)
		}
		seen[seq] = true
		if seq > max {
			max = seq
		}
	}
	if max != s.Seq+workers {
		t.Fatalf("max seq = %d, want %d", max, s.Seq+workers)
	}
}

func TestPersistence_RoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	svc, _, _ := newServiceAt(t, pool)
	s := mustCreate(t, svc, "scale", engine.Expert, 21)
	if _, err := svc.SwitchMeasures(s.ID, 0, 2); err != nil {
		t.Fatalf("SwitchMeasures: %v", err)
	}
	if _, err := svc.TransformMeasure(s.ID, 1, engine.Transform{FlipV: true}); err != nil {
		t.Fatalf("TransformMeasure: %v", err)
	}
	before, err := svc.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	reloaded, _, _ := newServiceAt(t, pool)
	after, err := reloaded.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession after reload: %v", err)
	}
	if after.Seq != before.Seq || after.Wins != before.Wins || after.Difficulty != before.Difficulty {
		t.Fatalf("reloaded = %+v, want %+v", after, before)
	}
	if after.Piece == nil || after.Piece.ID != "scale" {
		t.Fatalf("piece = %+v", after.Piece)
	}
	if !reflect.DeepEqual(after.Slots, before.Slots) {
		t.Fatalf("slots = %+v, want %+v", after.Slots, before.Slots)
	}
	if after.Solved != before.Solved {
		t.Fatalf("solved = %v, want %v", after.Solved, before.Solved)
	}
}

func TestShutdown_IsIdempotentAndPersists(t *testing.T) {
	pool := pgtest.Pool(t)
	svc, _, _ := newServiceAt(t, pool)
	s := mustCreate(t, svc, "scale", engine.Novice, 23)

	svc.Shutdown()
	svc.Shutdown()

	reloaded, _, _ := newServiceAt(t, pool)
	got, err := reloaded.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession after shutdown: %v", err)
	}
	if got.Seq != s.Seq {
		t.Fatalf("seq = %d, want %d", got.Seq, s.Seq)
	}
}

func TestDeleteSession_RemovesFromStore(t *testing.T) {
	pool := pgtest.Pool(t)
	svc, _, _ := newServiceAt(t, pool)
	s := mustCreate(t, svc, "scale", engine.Novice, 29)

	if err := svc.DeleteSession(s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, err := svc.GetSession(s.ID)
	wantCode(t, err, service.CodeNotFound)

	reloaded, _, _ := newServiceAt(t, pool)
	if _, err := reloaded.GetSession(s.ID); err == nil {
		t.Fatal("deleted session was reloaded from the store")
	}
}

func TestRenameSession_UpdatesNameAndSeq(t *testing.T) {
	svc, _, _ := newService(t)
	s := mustCreate(t, svc, "scale", engine.Novice, 31)

	got, err := svc.RenameSession(s.ID, "Grace")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if got.DisplayName != "Grace" || got.Seq != s.Seq+1 {
		t.Fatalf("session = %+v", got)
	}
}

func TestListSessions_LimitAndSort(t *testing.T) {
	svc, _, _ := newService(t)
	first := mustCreate(t, svc, "scale", engine.Novice, 2)
	second := mustCreate(t, svc, "scale", engine.Novice, 3)
	third := mustCreate(t, svc, "scale", engine.Novice, 4)

	list, err := svc.ListSessions(service.SessionFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %d sessions, want 2", len(list))
	}
	known := map[string]bool{first.ID: true, second.ID: true, third.ID: true}
	for _, got := range list {
		if !known[got.ID] {
			t.Fatalf("unexpected session %q", got.ID)
		}
	}

	all, err := svc.ListSessions(service.SessionFilter{Sort: "created"})
	if err != nil {
		t.Fatalf("ListSessions(created): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list = %d sessions, want 3", len(all))
	}
	if _, err := svc.ListSessions(service.SessionFilter{Sort: "bogus"}); err == nil {
		t.Fatal("unknown sort was accepted")
	}
	if _, err := svc.ListSessions(service.SessionFilter{Limit: -1}); err == nil {
		t.Fatal("negative limit was accepted")
	}
}
