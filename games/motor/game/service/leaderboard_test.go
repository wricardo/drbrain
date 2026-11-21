package service_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
)

// wideCfg: 8x3, start (0,1) facing right, treat at (7,1): 7 forward moves win.
// Main has 8 slots and sub 1 has 4, so a win can trade slots for steps.
var wideCfg = engine.MapConfig{
	ID: "wide", Name: "Wide", Layout: []string{"........", ">......*", "........"},
	MainTapeLength: 8, SubTapeLengths: []int{4}, MaxSteps: 40, MaxCallDepth: 8,
}

// Winning programs on wideCfg and what they cost.

// wideDirect: 7 slots, 7 steps.
func wideDirect() engine.Program {
	return engine.Program{Main: pad(8, moves(7)...), Subs: [][]engine.Instruction{pad(4)}}
}

// wideViaSub: 6 slots, 9 steps (2 calls + 3+3 moves + 1 move).
func wideViaSub() engine.Program {
	return engine.Program{
		Main: pad(8, engine.CallSub1, engine.CallSub1, engine.MoveForward),
		Subs: [][]engine.Instruction{pad(4, moves(3)...)},
	}
}

// wideWasteful: 8 slots, 11 steps (a pointless turn pair first).
func wideWasteful() engine.Program {
	return engine.Program{
		Main: pad(8, engine.TurnLeft, engine.TurnRight, engine.CallSub1, engine.CallSub1, engine.MoveForward),
		Subs: [][]engine.Instruction{pad(4, moves(3)...)},
	}
}

// winWith plays p to the end one manual step at a time (no runner, no timing).
func (e *env) winWith(t *testing.T, id string, p engine.Program) *service.Session {
	t.Helper()
	e.setProgram(t, id, p)
	for {
		s, err := e.svc.Step(id)
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if s.VM.Status.Terminal() {
			return s
		}
	}
}

// wonBy creates a session on mapID and wins it with p.
func (e *env) wonBy(t *testing.T, mapID string, p engine.Program) string {
	t.Helper()
	s, err := e.svc.CreateSession(mapID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := e.winWith(t, s.ID, p); got.VM.Status != engine.StatusWon {
		t.Fatalf("program did not win: %s/%s", got.VM.Status, got.VM.LossReason)
	}
	return s.ID
}

// waitClockAdvance makes sure time.Now() is later than t0, so two wins never
// share a timestamp and "earliest first" is deterministic.
func waitClockAdvance(t *testing.T, t0 time.Time) {
	t.Helper()
	waitFor(t, "clock to advance", func() bool { return time.Now().After(t0) })
}

func board(t *testing.T, e *env, q service.LeaderboardQuery) *service.Leaderboard {
	t.Helper()
	lb, err := e.svc.GetLeaderboard(q)
	if err != nil {
		t.Fatalf("GetLeaderboard(%+v): %v", q, err)
	}
	return lb
}

// rows flattens entries to "#rank slots/steps" for compact comparison; entry
// identity is asserted separately through SessionID.
func rows(lb *service.Leaderboard) []string {
	out := make([]string, len(lb.Entries))
	for i, en := range lb.Entries {
		out[i] = "#" + strconv.Itoa(en.Rank) + " " + strconv.Itoa(en.Slots) + "/" + strconv.Itoa(en.Steps)
	}
	return out
}

func TestWinRecordsSlotsAndSteps(t *testing.T) {
	e := newEnv(t, envOpts{}, corridorCfg, wideCfg)

	t.Run("a lost run records nothing", func(t *testing.T) {
		s := e.create(t, "corridor")
		e.setProgram(t, s.ID, corridorLose())
		got, err := e.svc.Step(s.ID)
		if err != nil || got.VM.Status != engine.StatusLost {
			t.Fatalf("step = %v, %v; want LOST", got, err)
		}
		if got.FewestSlots != nil || got.FewestSteps != nil {
			t.Fatalf("recorded a win for a lost run: %+v %+v", got.FewestSlots, got.FewestSteps)
		}
	})

	t.Run("a win records the server-side numbers", func(t *testing.T) {
		before := time.Now()
		id := e.wonBy(t, "wide", wideViaSub())
		s := e.get(t, id)
		for name, w := range map[string]*service.Win{"fewest slots": s.FewestSlots, "fewest steps": s.FewestSteps} {
			if w == nil || w.Slots != 6 || w.Steps != 9 {
				t.Fatalf("%s = %+v, want slots 6 steps 9", name, w)
			}
			if w.At.Before(before) {
				t.Fatalf("%s timestamp %v is before the win", name, w.At)
			}
		}
		if s.BestSteps != 9 {
			t.Fatalf("BestSteps = %d, want 9", s.BestSteps)
		}
		if p := e.pers.latest(id); p == nil || p.FewestSlots == nil {
			t.Fatal("the win was not persisted")
		}
	})

	t.Run("a run started with Run is recorded too", func(t *testing.T) {
		s := e.create(t, "corridor")
		e.setProgram(t, s.ID, corridorFast())
		e.run(t, s.ID, service.MinSpeedMs)
		got := e.waitTerminal(t, s.ID)
		if got.FewestSlots == nil || got.FewestSlots.Slots != 4 || got.FewestSlots.Steps != 4 {
			t.Fatalf("FewestSlots = %+v, want 4/4", got.FewestSlots)
		}
	})
}

func TestLeaderboardRanking(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	via := e.wonBy(t, "wide", wideViaSub())
	direct1 := e.wonBy(t, "wide", wideDirect())
	waitClockAdvance(t, e.get(t, direct1).FewestSlots.At)
	direct2 := e.wonBy(t, "wide", wideDirect())
	waste := e.wonBy(t, "wide", wideWasteful())
	e.create(t, "wide") // never won: not on the board

	tests := []struct {
		name  string
		q     service.LeaderboardQuery
		order string
		want  []string
	}{
		{"default is slots then steps", service.LeaderboardQuery{MapID: "wide"}, service.OrderSlots,
			[]string{"#1 6/9", "#2 7/7", "#2 7/7", "#4 8/11"}},
		{"explicit slots, lowercase", service.LeaderboardQuery{MapID: "wide", Order: "slots"}, service.OrderSlots,
			[]string{"#1 6/9", "#2 7/7", "#2 7/7", "#4 8/11"}},
		{"steps then slots", service.LeaderboardQuery{MapID: "wide", Order: service.OrderSteps}, service.OrderSteps,
			[]string{"#1 7/7", "#1 7/7", "#3 6/9", "#4 8/11"}},
		{"limit", service.LeaderboardQuery{MapID: "wide", Limit: 2}, service.OrderSlots,
			[]string{"#1 6/9", "#2 7/7"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lb := board(t, e, tc.q)
			if lb.Order != tc.order || lb.MapID != "wide" {
				t.Fatalf("order/map = %s/%s, want %s/wide", lb.Order, lb.MapID, tc.order)
			}
			if lb.Total != 4 {
				t.Fatalf("Total = %d, want 4 (never-won sessions are not ranked)", lb.Total)
			}
			if got := rows(lb); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rows = %v\nwant   %v", got, tc.want)
			}
		})
	}

	t.Run("yours is reported beyond the limit", func(t *testing.T) {
		lb := board(t, e, service.LeaderboardQuery{MapID: "wide", Limit: 1, SessionID: waste})
		if len(lb.Entries) != 1 || lb.Total != 4 {
			t.Fatalf("entries/total = %d/%d, want 1/4", len(lb.Entries), lb.Total)
		}
		if lb.Yours == nil || lb.Yours.Rank != 4 || lb.Yours.SessionID != waste {
			t.Fatalf("Yours = %+v, want rank 4 for %s", lb.Yours, waste)
		}
	})

	t.Run("yours is nil for a session that has not won or does not exist", func(t *testing.T) {
		for _, id := range []string{"0123456789abcdef", ""} {
			if lb := board(t, e, service.LeaderboardQuery{MapID: "wide", SessionID: id}); lb.Yours != nil {
				t.Fatalf("Yours for %q = %+v, want nil", id, lb.Yours)
			}
		}
	})

	t.Run("an entry carries the session id and time, ties go to the earlier win", func(t *testing.T) {
		lb := board(t, e, service.LeaderboardQuery{MapID: "wide"})
		if lb.Entries[0].SessionID != via || lb.Entries[0].At.IsZero() {
			t.Fatalf("first entry = %+v, want session %s with a time", lb.Entries[0], via)
		}
		if lb.Entries[1].SessionID != direct1 || lb.Entries[2].SessionID != direct2 {
			t.Fatalf("tie order = %s, %s; want %s, %s", lb.Entries[1].SessionID, lb.Entries[2].SessionID, direct1, direct2)
		}
	})
}

func TestLeaderboardIsPerMap(t *testing.T) {
	e := newEnv(t, envOpts{}, corridorCfg, wideCfg)
	e.wonBy(t, "wide", wideDirect())
	e.wonBy(t, "corridor", corridorFast())

	if got := rows(board(t, e, service.LeaderboardQuery{MapID: "wide"})); !reflect.DeepEqual(got, []string{"#1 7/7"}) {
		t.Fatalf("wide = %v", got)
	}
	if got := rows(board(t, e, service.LeaderboardQuery{MapID: "corridor"})); !reflect.DeepEqual(got, []string{"#1 4/4"}) {
		t.Fatalf("corridor = %v", got)
	}
}

func TestLeaderboardOneEntryPerSession(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	id := e.wonBy(t, "wide", wideDirect()) // 7 slots, 7 steps

	t.Run("a worse re-win does not replace the entry", func(t *testing.T) {
		if _, err := e.svc.Reset(id); err != nil {
			t.Fatal(err)
		}
		e.winWith(t, id, wideWasteful()) // 8/11
		lb := board(t, e, service.LeaderboardQuery{MapID: "wide"})
		if lb.Total != 1 || !reflect.DeepEqual(rows(lb), []string{"#1 7/7"}) {
			t.Fatalf("board = %v (total %d), want only Ada 7/7", rows(lb), lb.Total)
		}
	})

	t.Run("each ordering keeps its own best", func(t *testing.T) {
		e.winWith(t, id, wideViaSub()) // 6/9: fewer slots, more steps
		s := e.get(t, id)
		if s.FewestSlots.Slots != 6 || s.FewestSlots.Steps != 9 {
			t.Fatalf("FewestSlots = %+v, want 6/9", s.FewestSlots)
		}
		if s.FewestSteps.Slots != 7 || s.FewestSteps.Steps != 7 {
			t.Fatalf("FewestSteps = %+v, want 7/7", s.FewestSteps)
		}
		if got := rows(board(t, e, service.LeaderboardQuery{MapID: "wide"})); !reflect.DeepEqual(got, []string{"#1 6/9"}) {
			t.Fatalf("slots board = %v", got)
		}
		if got := rows(board(t, e, service.LeaderboardQuery{MapID: "wide", Order: service.OrderSteps})); !reflect.DeepEqual(got, []string{"#1 7/7"}) {
			t.Fatalf("steps board = %v", got)
		}
		if s.BestSteps != 7 {
			t.Fatalf("BestSteps = %d, want 7 (its meaning is unchanged)", s.BestSteps)
		}
	})

	t.Run("editing the program afterwards does not take the entry away", func(t *testing.T) {
		e.setProgram(t, id, engine.EmptyProgram(e.get(t, id).Map))
		if lb := board(t, e, service.LeaderboardQuery{MapID: "wide"}); lb.Total != 1 {
			t.Fatalf("Total = %d after clearing the program, want 1", lb.Total)
		}
	})

	t.Run("an identical re-win keeps the original time", func(t *testing.T) {
		first := e.get(t, id).FewestSteps.At
		if _, err := e.svc.Reset(id); err != nil {
			t.Fatal(err)
		}
		waitClockAdvance(t, first)
		e.winWith(t, id, wideDirect())
		if got := e.get(t, id).FewestSteps.At; !got.Equal(first) {
			t.Fatalf("At moved from %v to %v on a tie", first, got)
		}
	})
}

func TestLeaderboardDeletedSessionDisappears(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	a := e.wonBy(t, "wide", wideDirect())
	e.wonBy(t, "wide", wideWasteful())
	if err := e.svc.DeleteSession(a); err != nil {
		t.Fatal(err)
	}
	if got := rows(board(t, e, service.LeaderboardQuery{MapID: "wide"})); !reflect.DeepEqual(got, []string{"#1 8/11"}) {
		t.Fatalf("rows = %v, want only B", got)
	}
}

func TestLeaderboardFollowsMapRules(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	e.wonBy(t, "wide", wideDirect())

	t.Run("a cosmetic edit keeps the board", func(t *testing.T) {
		cfg := wideCfg
		cfg.Name, cfg.Description = "Wide, renamed", "now with a description"
		if _, err := e.svc.SaveMap(cfg); err != nil {
			t.Fatal(err)
		}
		if lb := board(t, e, service.LeaderboardQuery{MapID: "wide"}); lb.Total != 1 {
			t.Fatalf("Total = %d after a rename, want 1", lb.Total)
		}
	})

	t.Run("a rules edit retires results from the old rules", func(t *testing.T) {
		cfg := wideCfg
		cfg.MaxSteps = 20 // a different puzzle
		if _, err := e.svc.SaveMap(cfg); err != nil {
			t.Fatal(err)
		}
		if lb := board(t, e, service.LeaderboardQuery{MapID: "wide"}); lb.Total != 0 || len(lb.Entries) != 0 {
			t.Fatalf("board = %v (total %d), want empty", rows(lb), lb.Total)
		}
		// A session started on the new rules ranks again.
		e.wonBy(t, "wide", wideDirect())
		if got := rows(board(t, e, service.LeaderboardQuery{MapID: "wide"})); !reflect.DeepEqual(got, []string{"#1 7/7"}) {
			t.Fatalf("rows = %v, want only New", got)
		}
	})
}

func TestLeaderboardErrors(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	tests := []struct {
		name string
		q    service.LeaderboardQuery
		code string
	}{
		{"unknown map", service.LeaderboardQuery{MapID: "nope"}, service.CodeNotFound},
		{"unknown order", service.LeaderboardQuery{MapID: "wide", Order: "FASTEST"}, service.CodeInvalidArgument},
		{"negative limit", service.LeaderboardQuery{MapID: "wide", Limit: -1}, service.CodeInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.GetLeaderboard(tc.q)
			wantCode(t, err, tc.code)
		})
	}
}

func TestLeaderboardLimitIsClamped(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	const n = service.MaxLeaderboardLimit + 3
	for i := 0; i < n; i++ {
		e.wonBy(t, "wide", wideDirect())
	}
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"zero is the default", 0, service.DefaultLeaderboardLimit},
		{"one", 1, 1},
		{"above the maximum", 10000, service.MaxLeaderboardLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lb := board(t, e, service.LeaderboardQuery{MapID: "wide", Limit: tc.limit})
			if len(lb.Entries) != tc.want || lb.Total != n {
				t.Fatalf("rows/total = %d/%d, want %d/%d", len(lb.Entries), lb.Total, tc.want, n)
			}
		})
	}
}

func TestPublicName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain", "Ada", 40, "Ada"},
		{"empty is a guest", "", 40, "Guest"},
		{"blank is a guest", " \t ", 40, "Guest"},
		{"control characters become spaces", "A\x00d\x1b[31ma\nLovelace", 40, "A d [31ma Lovelace"},
		{"whitespace collapses", "  Ada    Lovelace  ", 40, "Ada Lovelace"},
		{"zero-width and bidi marks collapse to spaces", "Ad​a‮", 40, "Ad a"},
		{"only invisible is a guest", "​‮", 40, "Guest"},
		{"cut to max characters, not bytes", strings.Repeat("é", 10), 4, "éééé"},
		{"markup is left to the client to escape", "<b>x</b>", 40, "<b>x</b>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.PublicName(tc.in, tc.max); got != tc.want {
				t.Fatalf("PublicName(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestSlotsUsedAndFingerprint(t *testing.T) {
	m := mustMap(t, wideCfg)
	if got := engine.EmptyProgram(m).SlotsUsed(); got != 0 {
		t.Fatalf("empty program uses %d slots", got)
	}
	if got := wideViaSub().SlotsUsed(); got != 6 {
		t.Fatalf("via-sub program uses %d slots, want 6 (main and sub both count)", got)
	}
	if got := m.TotalSlots(); got != 12 {
		t.Fatalf("TotalSlots = %d, want 12", got)
	}

	cosmetic := wideCfg
	cosmetic.Name, cosmetic.Description, cosmetic.Difficulty = "Other", "d", "hard"
	if mustMap(t, cosmetic).Fingerprint() != m.Fingerprint() {
		t.Fatal("name, description and difficulty must not change the fingerprint")
	}
	for name, mutate := range map[string]func(*engine.MapConfig){
		"layout":    func(c *engine.MapConfig) { c.Layout = []string{"........", ">.#....*", "........"} },
		"main len":  func(c *engine.MapConfig) { c.MainTapeLength = 9 },
		"sub lens":  func(c *engine.MapConfig) { c.SubTapeLengths = []int{4, 1} },
		"no subs":   func(c *engine.MapConfig) { c.SubTapeLengths = []int{} },
		"max steps": func(c *engine.MapConfig) { c.MaxSteps = 41 },
		"depth":     func(c *engine.MapConfig) { c.MaxCallDepth = 9 },
		"recursion": func(c *engine.MapConfig) { c.AllowRecursion = true },
	} {
		t.Run(name, func(t *testing.T) {
			c := wideCfg
			mutate(&c)
			if mustMap(t, c).Fingerprint() == m.Fingerprint() {
				t.Fatalf("changing %s did not change the fingerprint", name)
			}
		})
	}
}
