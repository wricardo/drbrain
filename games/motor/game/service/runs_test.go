package service_test

import (
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/platform/account"
)

var guestShape = regexp.MustCompile(`^[A-Z][a-z]+[A-Z][a-z]+$`)

func TestGuestName(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		n := account.GuestName(0)
		if !guestShape.MatchString(n) {
			t.Fatalf("guest name %q is not <Adjective><Noun>", n)
		}
		if utf8.RuneCountInString(n) > service.DefaultMaxDisplayName {
			t.Fatalf("guest name %q is longer than the display-name limit", n)
		}
		seen[n] = true
	}
	if len(seen) < 100 {
		t.Fatalf("only %d distinct names in 500 draws: the generator is not random enough", len(seen))
	}
	if n := account.GuestName(5); utf8.RuneCountInString(n) > 5 {
		t.Fatalf("GuestName(5) = %q, want at most 5 characters", n)
	}
}

func runsByMap(t *testing.T, e *env, q service.RunsQuery) map[string]service.PlayerRun {
	t.Helper()
	runs, err := e.svc.GetPlayerRuns(q)
	if err != nil {
		t.Fatalf("GetPlayerRuns(%+v): %v", q, err)
	}
	out := map[string]service.PlayerRun{}
	for _, r := range runs {
		out[r.MapID] = r
	}
	return out
}

func TestPlayerRuns(t *testing.T) {
	e := newEnv(t, envOpts{}, corridorCfg, wideCfg)
	// Two sessions on wide, the better one (6/9) counts.
	adaWide1 := e.wonBy(t, "wide", wideDirect())      // 7/7
	adaWide2 := e.wonBy(t, "wide", wideViaSub())      // 6/9
	adaCorr := e.wonBy(t, "corridor", corridorFast()) // 4/4
	bob := e.wonBy(t, "wide", wideWasteful())         // 8/11
	e.create(t, "wide")                               // a session that never won

	t.Run("by session ids: best session per map with its rank", func(t *testing.T) {
		runs := runsByMap(t, e, service.RunsQuery{SessionIDs: []string{adaWide1, adaWide2, adaCorr}})
		if len(runs) != 2 {
			t.Fatalf("runs = %+v, want wide and corridor", runs)
		}
		w := runs["wide"]
		if w.Entry.SessionID != adaWide2 || w.Entry.Slots != 6 || w.Entry.Steps != 9 || w.Entry.Rank != 1 || w.Total != 3 {
			t.Fatalf("wide = %+v, want session %s 6/9 rank 1 of 3", w, adaWide2)
		}
		if c := runs["corridor"]; c.Entry.SessionID != adaCorr || c.Entry.Rank != 1 || c.Total != 1 {
			t.Fatalf("corridor = %+v", c)
		}
	})

	t.Run("ids that never won, are unknown or belong to someone else add nothing", func(t *testing.T) {
		runs := runsByMap(t, e, service.RunsQuery{SessionIDs: []string{"0123456789abcdef", bob}})
		if len(runs) != 1 || runs["wide"].Entry.SessionID != bob || runs["wide"].Entry.Rank != 3 {
			t.Fatalf("runs = %+v, want only Bob on wide at rank 3", runs)
		}
		if runs := runsByMap(t, e, service.RunsQuery{SessionIDs: []string{"0123456789abcdef"}}); len(runs) != 0 {
			t.Fatalf("runs = %+v, want none", runs)
		}
	})

	t.Run("runs come back ordered by map id", func(t *testing.T) {
		list, err := e.svc.GetPlayerRuns(service.RunsQuery{SessionIDs: []string{adaWide2, adaCorr}})
		if err != nil || len(list) != 2 || list[0].MapID != "corridor" || list[1].MapID != "wide" {
			t.Fatalf("list = %+v, %v", list, err)
		}
	})

	t.Run("a rules change retires the run", func(t *testing.T) {
		cfg := wideCfg
		cfg.MaxSteps = 20
		if _, err := e.svc.SaveMap(cfg, service.MapActor{AccountID: "tester"}); err != nil {
			t.Fatal(err)
		}
		runs := runsByMap(t, e, service.RunsQuery{SessionIDs: []string{adaWide1, adaWide2, adaCorr}})
		if _, ok := runs["wide"]; ok || len(runs) != 1 {
			t.Fatalf("runs = %+v, want only corridor", runs)
		}
	})
}

func TestPlayerRunsArguments(t *testing.T) {
	e := newEnv(t, envOpts{}, corridorCfg)
	tooMany := make([]string, service.MaxRunSessionIDs+1)
	exactly := make([]string, service.MaxRunSessionIDs)
	tests := []struct {
		name string
		q    service.RunsQuery
		code string // "" = ok
	}{
		{"neither", service.RunsQuery{}, service.CodeInvalidArgument},
		{"too many ids", service.RunsQuery{SessionIDs: tooMany}, service.CodeInvalidArgument},
		{"the maximum number of ids", service.RunsQuery{SessionIDs: exactly}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.GetPlayerRuns(tc.q)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			wantCode(t, err, tc.code)
		})
	}
}
