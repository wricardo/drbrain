package graph

import (
	"math"

	mazesservice "github.com/wricardo/drbrain/games/mazes/game/service"
	mazesmodel "github.com/wricardo/drbrain/games/mazes/graph/model"
	motorservice "github.com/wricardo/drbrain/games/motor/game/service"
	motormodel "github.com/wricardo/drbrain/games/motor/graph/model"
	"github.com/wricardo/drbrain/graph/generated"
)

// ComplexityLimit bounds the cost of one GraphQL operation. It matches the
// value the per-game servers used.
const ComplexityLimit = 1000

const (
	// sessionsPerCostUnit: a sessions list of N rows costs ceil(N/10) times
	// the per-row cost, so aliasing cannot multiply an unbounded list cheaply.
	sessionsPerCostUnit = 10
	// leaderboardBaseCost is the flat cost of one leaderboard query.
	leaderboardBaseCost = 20
	// playerRunsRowCost approximates the rows a playerRuns call can return.
	playerRunsRowCost = 10
)

// Complexity ports the mazes and motor field weights onto the namespaced
// schema. Delivery and Tesla have no fan-out fields worth weighting.
func Complexity() generated.ComplexityRoot {
	var c generated.ComplexityRoot
	c.Mazes_Query.Sessions = func(child int, _ *mazesmodel.SessionSort, limit *int, _ *string) int {
		n := mazesservice.DefaultListLimit
		if limit != nil && *limit > 0 {
			n = min(*limit, mazesservice.MaxListLimit)
		}
		return safeMul(child, (n+sessionsPerCostUnit-1)/sessionsPerCostUnit) + 1
	}

	c.Motor_Query.Sessions = func(child int, _ *motormodel.SessionSort, limit *int, _ *string) int {
		n := motorservice.DefaultListLimit
		if limit != nil && *limit > 0 {
			n = min(*limit, motorservice.MaxListLimit)
		}
		return safeMul(child, (n+sessionsPerCostUnit-1)/sessionsPerCostUnit) + 1
	}
	c.Motor_Query.Leaderboard = func(child int, _ string, _ *motormodel.LeaderboardOrder, limit *int, _ *string) int {
		n := motorservice.DefaultLeaderboardLimit
		if limit != nil && *limit > 0 {
			n = min(*limit, motorservice.MaxLeaderboardLimit)
		}
		return safeMul(child, (n+sessionsPerCostUnit-1)/sessionsPerCostUnit) + leaderboardBaseCost
	}
	c.Motor_Query.PlayerRuns = func(child int, _ []string, _ *string) int {
		return safeMul(child, playerRunsRowCost) + leaderboardBaseCost
	}
	c.Motor_Query.Profile = func(child int, _ string) int {
		return safeMul(child, playerRunsRowCost) + leaderboardBaseCost
	}
	c.Motor_Mutation.ClaimSessions = func(child int, claims []*motormodel.SessionClaimInput) int {
		return safeMul(child, len(claims)) + 1
	}
	return c
}

// safeMul multiplies non-negative a by b without overflowing.
func safeMul(a, b int) int {
	if a != 0 && b > math.MaxInt/a {
		return math.MaxInt
	}
	return a * b
}
