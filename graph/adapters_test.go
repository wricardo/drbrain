package graph

import (
	"context"
	"testing"

	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
)

func TestNamespaceRootsReturnTheirGameResolver(t *testing.T) {
	d := &deliverygraph.Resolver{}
	m := &mazesgraph.Resolver{}
	p := &motorgraph.Resolver{}
	x := &teslagraph.Resolver{}
	r := New(d, m, p, x)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		got  func(context.Context) (any, error)
		want any
	}{
		"delivery":         {func(ctx context.Context) (any, error) { return r.Query().Delivery(ctx) }, d},
		"mazes":            {func(ctx context.Context) (any, error) { return r.Query().Mazes(ctx) }, m},
		"motorProgramming": {func(ctx context.Context) (any, error) { return r.Query().MotorProgramming(ctx) }, p},
		"tesla":            {func(ctx context.Context) (any, error) { return r.Query().Tesla(ctx) }, x},
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

func TestComplexityUsesNamespacedRoots(t *testing.T) {
	c := Complexity()
	if c.Mazes_Query.Sessions == nil {
		t.Fatal("mazes sessions complexity is not wired")
	}
	if c.Motor_Query.Sessions == nil || c.Motor_Query.Leaderboard == nil ||
		c.Motor_Query.PlayerRuns == nil || c.Motor_Query.Profile == nil ||
		c.Motor_Mutation.ClaimSessions == nil {
		t.Fatal("motor complexity is not fully wired")
	}
}
