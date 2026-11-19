package session

import (
	"reflect"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
)

var (
	// winAt is what the fixture session records as LastActionAt.
	winAt = time.Date(2026, 1, 2, 4, 4, 5, 6, time.UTC)
	// recorded is a plausible stored win on the corridor map.
	recorded = &service.Win{Slots: 4, Steps: 4, At: time.Date(2026, 1, 2, 3, 30, 0, 0, time.UTC)}
)

// wonVM runs the fixture program to the end: 4 forward moves, 4 slots, 4 steps.
func wonVM(t *testing.T, ps *PersistedSession) {
	t.Helper()
	ps.VM = engine.Run(mustMap(t, corridorCfg), ps.Program)
	if ps.VM.Status != engine.StatusWon || ps.VM.Steps != 4 {
		t.Fatalf("fixture run = %s in %d steps, want WON in 4", ps.VM.Status, ps.VM.Steps)
	}
}

func TestFileRoundTripKeepsLeaderboardWins(t *testing.T) {
	fp, _ := newFile(t)
	s := fixture(t, idA)
	s.FewestSlots = &service.Win{Slots: 3, Steps: 9, At: recorded.At}
	s.FewestSteps = &service.Win{Slots: 4, Steps: 4, At: recorded.At}
	if err := fp.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := fp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.FewestSlots, s.FewestSlots) || !reflect.DeepEqual(got.FewestSteps, s.FewestSteps) {
		t.Fatalf("wins differ:\n got %+v %+v\nwant %+v %+v", got.FewestSlots, got.FewestSteps, s.FewestSlots, s.FewestSteps)
	}
}

func TestLoadRecoversWinsOfFilesWrittenBeforeLeaderboards(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PersistedSession)
		slots  *service.Win // expected FewestSlots (nil = none)
		steps  *service.Win // expected FewestSteps (nil = none)
	}{
		{
			// A WON VM always belongs to the stored program, so the win is exact.
			name:   "a won session is backfilled from its program and VM",
			mutate: func(ps *PersistedSession) { wonVM(t, ps) },
			slots:  &service.Win{Slots: 4, Steps: 4, At: winAt},
			steps:  &service.Win{Slots: 4, Steps: 4, At: winAt},
		},
		{
			name:   "a session that never won has no entry",
			mutate: nil,
		},
		{
			name: "recorded wins are kept as they are",
			mutate: func(ps *PersistedSession) {
				wonVM(t, ps)
				ps.FewestSlots = &service.Win{Slots: 3, Steps: 9, At: recorded.At}
				ps.FewestSteps = recorded
			},
			slots: &service.Win{Slots: 3, Steps: 9, At: recorded.At},
			steps: recorded,
		},
		{
			name: "an impossible recorded win is dropped, not ranked",
			mutate: func(ps *PersistedSession) {
				ps.FewestSlots = &service.Win{Slots: 99, Steps: 4, At: recorded.At} // map has 8 slots
				ps.FewestSteps = &service.Win{Slots: 4, Steps: 31, At: recorded.At} // limit is 30 steps
			},
		},
		{
			name: "a zero win is dropped",
			mutate: func(ps *PersistedSession) {
				ps.FewestSlots = &service.Win{}
				ps.FewestSteps = &service.Win{Slots: 0, Steps: 4}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp, dir := newFile(t)
			writeFile(t, dir, idA, tc.mutate)
			got, err := fp.Load(idA)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.FewestSlots, tc.slots) {
				t.Fatalf("FewestSlots = %+v, want %+v", got.FewestSlots, tc.slots)
			}
			if !reflect.DeepEqual(got.FewestSteps, tc.steps) {
				t.Fatalf("FewestSteps = %+v, want %+v", got.FewestSteps, tc.steps)
			}
		})
	}
}
