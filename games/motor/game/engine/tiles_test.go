package engine

import (
	"strings"
	"testing"
)

// tileMap builds a map from layout rows with sensible defaults for tests.
func tileMap(t *testing.T, rows ...string) *Map {
	t.Helper()
	return mustMap(t, MapConfig{
		ID: "tiles", Name: "Tiles", Layout: rows,
		MainTapeLength: 8, SubTapeLengths: []int{4}, MaxSteps: 50, MaxCallDepth: 4,
	})
}

func TestConveyorPushesOneCell(t *testing.T) {
	m := tileMap(t,
		">R*",
		"...",
		"...",
	)
	st := NewVMState(m)
	p := prog(m, "F")
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Pushed || ev.PushBlocked || ev.Blocked {
		t.Fatalf("pushed=%v pushBlocked=%v blocked=%v, want a successful push", ev.Pushed, ev.PushBlocked, ev.Blocked)
	}
	if st.Pos != (Position{X: 2, Y: 0}) {
		t.Fatalf("pos %v, want (2,0): one MOVE takes two cells over a conveyor", st.Pos)
	}
	if st.Status != StatusWon || st.Steps != 1 {
		t.Fatalf("status %s steps %d, want WON in 1: the push is not a second step", st.Status, st.Steps)
	}
	if len(st.Visited) != 3 {
		t.Fatalf("visited %v, want start + conveyor + landing", st.Visited)
	}
}

func TestConveyorPushBlockedByRock(t *testing.T) {
	m := tileMap(t,
		">R#",
		"..*",
		"...",
	)
	st := NewVMState(m)
	p := prog(m, "F")
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.PushBlocked || ev.Pushed || ev.Blocked {
		t.Fatalf("pushed=%v pushBlocked=%v blocked=%v, want only pushBlocked", ev.Pushed, ev.PushBlocked, ev.Blocked)
	}
	if st.Pos != (Position{X: 1, Y: 0}) {
		t.Fatalf("pos %v, want the conveyor cell (1,0)", st.Pos)
	}
}

func TestMirrorReflectsFacing(t *testing.T) {
	m := tileMap(t,
		">/.",
		"..*",
		"...",
	)
	st := NewVMState(m)
	p := prog(m, "F")
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Mirrored || ev.FacingAfter != FacingUp {
		t.Fatalf("mirrored=%v facingAfter=%v, want a right-to-up reflection", ev.Mirrored, ev.FacingAfter)
	}
	if ev.Pushed || ev.PushBlocked {
		t.Fatal("a mirror must not push")
	}

	back := tileMap(t,
		">\\.",
		"..*",
		"...",
	)
	st = NewVMState(back)
	p = prog(back, "F")
	ev, err = st.Step(back, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Mirrored || ev.FacingAfter != FacingDown {
		t.Fatalf("mirrored=%v facingAfter=%v, want a right-to-down reflection", ev.Mirrored, ev.FacingAfter)
	}
}

func TestPortalTeleportsOnce(t *testing.T) {
	m := tileMap(t,
		">1.",
		"...",
		".1*",
	)
	st := NewVMState(m)
	p := prog(m, "FF")
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Teleported == nil || *ev.Teleported != (Position{X: 1, Y: 2}) {
		t.Fatalf("teleported=%v, want (1,2)", ev.Teleported)
	}
	if st.Pos != (Position{X: 1, Y: 2}) {
		t.Fatalf("pos %v, want the twin portal", st.Pos)
	}
	if ev.Status != StatusRunning || st.Steps != 1 {
		t.Fatalf("status %s steps %d, want still running after 1 step", ev.Status, st.Steps)
	}
	ev, err = st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusWon || st.Steps != 2 {
		t.Fatalf("status %s steps %d, want WON in 2", st.Status, st.Steps)
	}
	if ev.Teleported != nil {
		t.Fatal("the second move is a plain step, not another teleport")
	}
}

func TestKeyOpensDoor(t *testing.T) {
	m := tileMap(t,
		">aA*",
		"....",
		"....",
	)
	if got, want := string(m.DoorLetters), "a"; got != want {
		t.Fatalf("DoorLetters %q, want %q", got, want)
	}
	st := NewVMState(m)
	if len(st.DoorsOpen) != 1 || st.DoorsOpen[0] {
		t.Fatalf("doorsOpen %v, want one closed door", st.DoorsOpen)
	}
	p := prog(m, "FFF")

	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if ev.CollectedKey != "a" || !st.DoorsOpen[0] {
		t.Fatalf("collectedKey=%q doorsOpen=%v, want key a collected", ev.CollectedKey, st.DoorsOpen)
	}
	ev, err = st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Blocked || st.Pos != (Position{X: 2, Y: 0}) {
		t.Fatalf("blocked=%v pos=%v, want the open door passable", ev.Blocked, st.Pos)
	}
	if _, err := st.Step(m, &p); err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusWon || st.Steps != 3 {
		t.Fatalf("status %s steps %d, want WON in 3", st.Status, st.Steps)
	}
}

func TestClosedDoorBlocksLikeRock(t *testing.T) {
	m := tileMap(t,
		">A..",
		"..a*",
		"....",
	)
	st := NewVMState(m)
	p := prog(m, "F")
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Blocked || st.Pos != (Position{X: 0, Y: 0}) {
		t.Fatalf("blocked=%v pos=%v, want the door to block the move", ev.Blocked, st.Pos)
	}
}

func TestTileEffectsDoNotChain(t *testing.T) {
	// A conveyor pushes the robot onto a portal; the portal must not fire.
	m := tileMap(t,
		">R1",
		".1.",
		"..*",
	)
	st := NewVMState(m)
	p := prog(m, "F")
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Pushed || ev.Teleported != nil {
		t.Fatalf("pushed=%v teleported=%v, want a push and no chained teleport", ev.Pushed, ev.Teleported)
	}
	if st.Pos != (Position{X: 2, Y: 0}) {
		t.Fatalf("pos %v, want (2,0)", st.Pos)
	}
}

func TestTileMapValidation(t *testing.T) {
	cases := []struct {
		name string
		rows []string
		want string
	}{
		{"door without key", []string{">A*", "...", "..."}, `door "A" has no matching key "a"`},
		{"key without door", []string{">a*", "...", "..."}, `key "a" has no matching door "A"`},
		{"odd portal", []string{">1*", "...", "..."}, `portal "1" appears 1 times`},
		{"unknown glyph", []string{">?*", "...", "..."}, "unknown glyph '?'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewMap(MapConfig{ID: "bad", Name: "Bad", Layout: c.rows})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestTileStateSurvivesClone(t *testing.T) {
	m := tileMap(t,
		">aA*",
		"....",
		"....",
	)
	st := NewVMState(m)
	p := prog(m, "F")
	if _, err := st.Step(m, &p); err != nil {
		t.Fatal(err)
	}
	c := st.Clone()
	c.DoorsOpen[0] = false
	if !st.DoorsOpen[0] {
		t.Fatal("Clone shares the DoorsOpen slice")
	}
}

func TestOldMapHasNoDoorState(t *testing.T) {
	m := tileMap(t,
		">**",
		"...",
		"...",
	)
	st := NewVMState(m)
	if len(st.DoorsOpen) != 0 {
		t.Fatalf("doorsOpen %v, want none on a map without doors", st.DoorsOpen)
	}
}
