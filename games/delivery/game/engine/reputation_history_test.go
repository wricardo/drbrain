package engine

import (
	"math"
	"testing"
)

// A fresh world starts with an opening standing row, so a reputation chart has
// a tick-0 anchor instead of starting at the first expiry.
func TestReputationHistoryStartsWithOpeningEntry(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Ledger.ReputationEntries) != 1 {
		t.Fatalf("entries = %d, want one opening row", len(w.Ledger.ReputationEntries))
	}
	entry := w.Ledger.ReputationEntries[0]
	if entry.Seq != 0 || entry.Tick != 0 || entry.Reason != ReputationOpening {
		t.Fatalf("opening = %+v, want seq 0 tick 0 reason opening", entry)
	}
	if entry.Balance != cfg.MaxReputation || entry.Delta != cfg.MaxReputation {
		t.Fatalf("opening balance/delta = %v/%v, want %v", entry.Balance, entry.Delta, cfg.MaxReputation)
	}
}

// The stored delta is the actual change after clamping, so the series still
// sums to the current reputation.
func TestReputationHistoryStoresActualDeltas(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Tick = 42
	applied := w.recordReputation(0.02, ReputationDelivery, "ord_1", "pizza delivered")
	if applied != 0 {
		t.Fatalf("applied at max = %v, want 0 (clamped)", applied)
	}
	if len(w.Ledger.ReputationEntries) != 1 {
		t.Fatalf("clamped change recorded an entry: %+v", w.Ledger.ReputationEntries)
	}

	w.Ledger.Reputation = 0.05
	applied = w.recordReputation(-0.10, ReputationExpired, "ord_2", "pizza expired")
	if math.Abs(applied-(-0.05)) > 1e-9 {
		t.Fatalf("applied = %v, want -0.05 after clamping", applied)
	}
	last := w.Ledger.ReputationEntries[len(w.Ledger.ReputationEntries)-1]
	if last.Seq != 1 || last.Tick != 42 || last.Reason != ReputationExpired {
		t.Fatalf("last = %+v, want seq 1 tick 42 expired", last)
	}
	if math.Abs(last.Balance) > 1e-9 || math.Abs(last.Delta-(-0.05)) > 1e-9 {
		t.Fatalf("last balance/delta = %v/%v, want 0/-0.05", last.Balance, last.Delta)
	}
}
