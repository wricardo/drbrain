package engine

import (
	"math"
	"testing"
)

// TestPayMultiplierBands pins the reputation ladder: full pay at 4.90, one
// tenth less per half point, zero below 0.50. The exact-boundary rows matter
// because reputation accumulates in 0.02 steps on a float.
func TestPayMultiplierBands(t *testing.T) {
	w := orderWorld(t)

	cases := []struct {
		rep  float64
		want float64
	}{
		{5.00, 1.00},
		{4.90, 1.00},
		{4.8999, 0.90}, // just below the top band
		{4.50, 0.90},
		{4.49, 0.80},
		{4.00, 0.80},
		{3.50, 0.70},
		{3.00, 0.60}, // mid-ladder
		{2.50, 0.50},
		{2.00, 0.40},
		{1.50, 0.30},
		{1.00, 0.20},
		{0.50, 0.10},
		{0.49, 0.10}, // floor: any standing above zero still earns a tenth
		{0.02, 0.10},
		{0.00, 0.00},
	}
	for _, tc := range cases {
		w.Ledger.Reputation = tc.rep
		if got := w.PayMultiplier(); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("reputation %.2f: multiplier = %.2f, want %.2f", tc.rep, got, tc.want)
		}
	}
}

// TestDeliveryPayScalesWithReputation proves the multiplier reaches the money:
// the same order pays the full reward at 5.0, 60% at the neutral 3.0, and zero
// at rock bottom while still counting as a delivery that earns reputation back.
func TestDeliveryPayScalesWithReputation(t *testing.T) {
	cases := []struct {
		name    string
		rep     float64
		wantPay func(reward int) int
	}{
		{"top standing pays in full", 5.0, func(r int) int { return r }},
		{"neutral standing pays 60%", 3.0, func(r int) int { return int(math.Round(float64(r) * 0.6)) }},
		{"bottom standing still earns the floor", 0.2, func(r int) int { return int(math.Round(float64(r) * 0.10)) }},
		{"exactly zero standing pays nothing", 0.0, func(int) int { return 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := orderWorld(t)
			w.Ledger.Reputation = tc.rep
			w.Advance()
			o := firstOrder(t, w)
			car := w.Cars[0]
			reward := o.Reward

			if err := w.AssignOrder(o.ID, car.ID); err != nil {
				t.Fatalf("assign: %v", err)
			}
			driveTo(t, w, car, Position{2, 1})
			driveTo(t, w, car, Position{2, 3})

			want := tc.wantPay(reward)
			if o.Paid != want {
				t.Fatalf("paid = %d, want %d (reward %d)", o.Paid, want, reward)
			}
			if w.Ledger.MoneyEarned != want {
				t.Fatalf("moneyEarned = %d, want %d", w.Ledger.MoneyEarned, want)
			}

			// The delivery counts regardless of pay, and raises standing, so a
			// broke fleet can climb back into a paid band by running for free.
			if w.Ledger.Deliveries != 1 {
				t.Fatalf("deliveries = %d, want 1", w.Ledger.Deliveries)
			}
			if w.Ledger.Reputation < tc.rep {
				t.Fatalf("reputation = %.2f, want at least the starting %.2f", w.Ledger.Reputation, tc.rep)
			}

			// A zero payout records no statement row: a movement of nothing is
			// not a line in a checking account.
			wantRows := 1
			if want == 0 {
				wantRows = 0
			}
			if len(w.Ledger.Cash) != wantRows {
				t.Fatalf("cash rows = %d, want %d", len(w.Ledger.Cash), wantRows)
			}
		})
	}
}

// TestExpiryDropsReputationTenPoints pins the steeper expiry penalty: one
// order left to rot costs five on-time deliveries.
func TestExpiryDropsReputationTenPoints(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	before := w.Ledger.Reputation

	// Push the world past the deadline; the sweep charges the fine.
	w.Tick = o.DeadlineTick + 1
	w.expireOrders()

	if o.State != OrderExpired {
		t.Fatalf("state = %s, want expired", o.State)
	}
	want := before - w.ordersCfg.ExpirePenaltyReputation
	if math.Abs(w.Ledger.Reputation-want) > 1e-9 {
		t.Fatalf("reputation = %.2f, want %.2f (penalty %.2f)",
			w.Ledger.Reputation, want, w.ordersCfg.ExpirePenaltyReputation)
	}
	// The shipped city must carry the steeper penalty, or a reloaded session
	// would quietly bill the old 0.05.
	if _, cfg, err := CityFromFile(repoCityPath(t)); err != nil {
		t.Fatalf("load shipped city: %v", err)
	} else if cfg.Orders.ExpirePenaltyReputation != 0.10 {
		t.Fatalf("shipped expire_penalty_reputation = %.2f, want 0.10", cfg.Orders.ExpirePenaltyReputation)
	}
}

// TestNewWorldStartsAtFullReputation pins the opening standing: a fresh session
// is paid in full from its first delivery, and the ladder is spent downward.
func TestNewWorldStartsAtFullReputation(t *testing.T) {
	w := orderWorld(t)
	if w.Ledger.Reputation != w.maxReputation {
		t.Fatalf("starting reputation = %.2f, want max %.2f", w.Ledger.Reputation, w.maxReputation)
	}
	if got := w.PayMultiplier(); got != 1.0 {
		t.Fatalf("starting multiplier = %.2f, want 1.0", got)
	}
}

// TestExpirePenaltyScalesWithStanding pins the recovery rule: the full penalty
// only lands on a fleet with standing to lose. A collapsed fleet pays a
// fraction, so out-delivering the misses always climbs.
func TestExpirePenaltyScalesWithStanding(t *testing.T) {
	w := orderWorld(t)
	max := w.ordersCfg.ExpirePenaltyReputation // 0.10 in this fixture

	cases := []struct {
		rep  float64
		want float64
	}{
		{5.00, max},
		{1.00, max},
		{0.50, max * 0.5},
		{0.20, max * 0.2},
		{0.02, max * 0.02},
		{0.00, 0},
	}
	for _, tc := range cases {
		w.Ledger.Reputation = tc.rep
		if got := w.ExpirePenalty(); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("standing %.2f: penalty = %.4f, want %.4f", tc.rep, got, tc.want)
		}
	}
}

// TestCollapsedFleetClimbs proves the point of the scale: at 0.2 a missed order
// costs one delivery, so any delivery advantage raises standing.
func TestCollapsedFleetClimbs(t *testing.T) {
	w := orderWorld(t)
	w.Ledger.Reputation = 0.2
	before := w.Ledger.Reputation

	w.Advance()
	o := firstOrder(t, w)
	w.Tick = o.DeadlineTick + 1
	w.expireOrders()
	if o.State != OrderExpired {
		t.Fatalf("state = %s, want expired", o.State)
	}
	// One miss at 0.2 is 0.02, exactly one delivery's worth of standing.
	want := before - 0.10*0.2
	if math.Abs(w.Ledger.Reputation-want) > 1e-9 {
		t.Fatalf("reputation = %.4f, want %.4f", w.Ledger.Reputation, want)
	}
}
