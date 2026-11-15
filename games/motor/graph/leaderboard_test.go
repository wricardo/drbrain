package graph

import (
	"context"
	"testing"
	"time"

	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/graph/model"
)

// lbService answers GetLeaderboard only; anything else panics on the nil
// embedded interface.
type lbService struct {
	service.GameService
	got  service.LeaderboardQuery
	lb   *service.Leaderboard
	runs []service.PlayerRun
	gotR service.RunsQuery
	err  error
}

func (f *lbService) GetPlayerRuns(q service.RunsQuery) ([]service.PlayerRun, error) {
	f.gotR = q
	return f.runs, f.err
}

func (f *lbService) GetLeaderboard(q service.LeaderboardQuery) (*service.Leaderboard, error) {
	f.got = q
	return f.lb, f.err
}

func ptr[T any](v T) *T { return &v }

func TestLeaderboardResolver(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	entry := service.LeaderboardEntry{Rank: 2, SessionID: "00112233445566aa", DisplayName: "Ada", Slots: 6, Steps: 9, At: at}

	t.Run("defaults to slots order and converts the board", func(t *testing.T) {
		f := &lbService{lb: &service.Leaderboard{
			MapID: "m", Order: service.OrderSlots, Total: 5,
			Entries: []service.LeaderboardEntry{entry},
			Yours:   &entry,
		}}
		q := &queryResolver{&Resolver{Service: f}}
		got, err := q.Leaderboard(context.Background(), "m", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if f.got != (service.LeaderboardQuery{MapID: "m", Order: service.OrderSlots}) {
			t.Fatalf("service query = %+v", f.got)
		}
		if got.MapID != "m" || got.Order != model.LeaderboardOrderSlots || got.Total != 5 || len(got.Entries) != 1 {
			t.Fatalf("board = %+v", got)
		}
		e := got.Entries[0]
		if e.Rank != 2 || e.SessionID != entry.SessionID || e.DisplayName != "Ada" || e.Slots != 6 || e.Steps != 9 || !e.AchievedAt.Equal(at) {
			t.Fatalf("entry = %+v", e)
		}
		if got.Yours == nil || got.Yours.SessionID != entry.SessionID {
			t.Fatalf("yours = %+v", got.Yours)
		}
	})

	t.Run("passes order, limit and session id through", func(t *testing.T) {
		f := &lbService{lb: &service.Leaderboard{MapID: "m", Order: service.OrderSteps, Entries: []service.LeaderboardEntry{}}}
		q := &queryResolver{&Resolver{Service: f}}
		got, err := q.Leaderboard(context.Background(), "m", ptr(model.LeaderboardOrderSteps), ptr(7), ptr("00112233445566bb"))
		if err != nil {
			t.Fatal(err)
		}
		want := service.LeaderboardQuery{MapID: "m", Order: service.OrderSteps, Limit: 7, SessionID: "00112233445566bb"}
		if f.got != want {
			t.Fatalf("service query = %+v, want %+v", f.got, want)
		}
		if got.Entries == nil || len(got.Entries) != 0 || got.Yours != nil {
			t.Fatalf("empty board must have entries [] and yours null: %+v", got)
		}
	})

	t.Run("a missing map is null, other errors keep their code", func(t *testing.T) {
		q := &queryResolver{&Resolver{Service: &lbService{err: service.Errorf(service.CodeNotFound, "map %q not found", "x")}}}
		if got, err := q.Leaderboard(context.Background(), "x", nil, nil, nil); got != nil || err != nil {
			t.Fatalf("got %v, %v; want nil, nil", got, err)
		}
		q = &queryResolver{&Resolver{Service: &lbService{err: service.Errorf(service.CodeInvalidArgument, "limit must be >= 0")}}}
		_, err := q.Leaderboard(context.Background(), "m", nil, ptr(-1), nil)
		if code := codeOf(t, err); code != service.CodeInvalidArgument {
			t.Fatalf("code = %s, want %s", code, service.CodeInvalidArgument)
		}
	})
}

func TestPlayerRunsResolver(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	f := &lbService{runs: []service.PlayerRun{{
		MapID: "m", Total: 9,
		Entry: service.LeaderboardEntry{Rank: 3, SessionID: "00112233445566aa", DisplayName: "BraveOtter", Slots: 5, Steps: 8, At: at},
	}}}
	q := &queryResolver{&Resolver{Service: f}}

	t.Run("by session ids", func(t *testing.T) {
		got, err := q.PlayerRuns(context.Background(), []string{"a", "b"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.gotR.SessionIDs) != 2 || f.gotR.Name != "" {
			t.Fatalf("service query = %+v", f.gotR)
		}
		if len(got) != 1 || got[0].MapID != "m" || got[0].Total != 9 || got[0].Entry.Rank != 3 || got[0].Entry.DisplayName != "BraveOtter" || !got[0].Entry.AchievedAt.Equal(at) {
			t.Fatalf("runs = %+v", got[0])
		}
	})
	t.Run("by name", func(t *testing.T) {
		if _, err := q.PlayerRuns(context.Background(), nil, ptr("BraveOtter")); err != nil {
			t.Fatal(err)
		}
		if f.gotR.Name != "BraveOtter" || len(f.gotR.SessionIDs) != 0 {
			t.Fatalf("service query = %+v", f.gotR)
		}
	})
	t.Run("no runs is an empty list, errors keep their code", func(t *testing.T) {
		empty := &queryResolver{&Resolver{Service: &lbService{}}}
		got, err := empty.PlayerRuns(context.Background(), []string{"a"}, nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %v, %v; want [] and no error", got, err)
		}
		bad := &queryResolver{&Resolver{Service: &lbService{err: service.Errorf(service.CodeInvalidArgument, "give either sessionIds or name")}}}
		_, err = bad.PlayerRuns(context.Background(), nil, nil)
		if code := codeOf(t, err); code != service.CodeInvalidArgument {
			t.Fatalf("code = %s", code)
		}
	})
}
