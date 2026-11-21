package graph

import (
	"context"
	"testing"

	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	musicgraph "github.com/wricardo/drbrain/games/music/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
)

func TestNamespaceRootsReturnTheirGameResolver(t *testing.T) {
	d := &deliverygraph.Resolver{}
	m := &mazesgraph.Resolver{}
	p := &motorgraph.Resolver{}
	x := &teslagraph.Resolver{}
	mu := &musicgraph.Resolver{}
	r := New(nil, d, m, p, x, mu)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		got  func(context.Context) (any, error)
		want any
	}{
		"delivery":         {func(ctx context.Context) (any, error) { return r.Query().Delivery(ctx) }, d},
		"mazes":            {func(ctx context.Context) (any, error) { return r.Query().Mazes(ctx) }, m},
		"motorProgramming": {func(ctx context.Context) (any, error) { return r.Query().MotorProgramming(ctx) }, p},
		"tesla":            {func(ctx context.Context) (any, error) { return r.Query().Tesla(ctx) }, x},
		"music":            {func(ctx context.Context) (any, error) { return r.Query().Music(ctx) }, mu},
	} {
		got, err := tc.got(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %p, want %p", name, got, tc.want)
		}
	}

	// One delegation path through a generated child interface: delivery
	// Health is a pure function, so no session manager is needed.
	h, err := r.Delivery_Query().Health(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" {
		t.Fatalf("health status = %q, want ok", h.Status)
	}
}

func TestDisabledGameReportsNotEnabled(t *testing.T) {
	r := New(nil, nil, nil, nil, nil, nil)
	if _, err := r.Query().Music(context.Background()); err == nil || err.Error() != "game is not enabled" {
		t.Fatalf("disabled music error = %v, want %q", err, "game is not enabled")
	}
}

func TestComplexityUsesNamespacedRoots(t *testing.T) {
	c := Complexity()
	if c.Mazes_Query.Sessions == nil {
		t.Fatal("mazes sessions complexity is not wired")
	}
	if c.Motor_Query.Sessions == nil || c.Motor_Query.Leaderboard == nil ||
		c.Motor_Query.PlayerRuns == nil || c.Motor_Query.PlayerRunsFor == nil ||
		c.Motor_Mutation.ClaimSessions == nil {
		t.Fatal("motor complexity is not fully wired")
	}
}
