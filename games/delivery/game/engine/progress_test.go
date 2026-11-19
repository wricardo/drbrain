package engine

import "testing"

// TestEveryUnlockStatesItsGate is the guard against a locked upgrade card that
// says nothing. A card the player cannot act on is a dead end: every shipped
// unlock must carry the criteria text and a target the UI can show progress
// against, so "locked" always comes with the reason.
func TestEveryUnlockStatesItsGate(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 7)
	if err != nil {
		t.Fatal(err)
	}

	unlocks := w.Unlocks()
	if len(unlocks) == 0 {
		t.Fatal("shipped city has no unlocks")
	}
	for _, u := range unlocks {
		if u.Requires == "" || u.Requires == "no requirement" {
			t.Errorf("unlock %s (%q) has no criteria text", u.ID, u.Title)
		}
		if u.Target <= 0 {
			t.Errorf("unlock %s has target %d; progress cannot be shown", u.ID, u.Target)
		}
	}
}

// TestUnlockProgressTracksTheGate pins the units: the progress bar for an
// unlock must count the same thing its criteria text names. The shipped East
// Bridge unlocks at 5 deliveries, so its target is 5 and progress is the
// delivery counter, not money or builds.
func TestUnlockProgressTracksTheGate(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 7)
	if err != nil {
		t.Fatal(err)
	}

	var east UnlockStatus
	for _, u := range w.Unlocks() {
		if u.ID == "u02_east_bridge" {
			east = u
		}
	}
	if east.ID == "" {
		t.Fatal("shipped city has no u02_east_bridge")
	}
	if want := "5 deliveries"; east.Requires != want {
		t.Errorf("criteria = %q, want %q", east.Requires, want)
	}
	if east.Target != 5 {
		t.Errorf("target = %d, want 5", east.Target)
	}

	w.Ledger.Deliveries = 3
	for _, u := range w.Unlocks() {
		if u.ID == east.ID {
			if u.Progress != 3 {
				t.Errorf("progress = %d after 3 deliveries, want 3", u.Progress)
			}
			if u.Earned {
				t.Error("unlock earned at 3/5 deliveries")
			}
		}
	}
}

// TestDescribeRequiresNamesEveryone pins the wording and the fact that a gate
// with two conditions names both. Showing only the strictest one hides half
// the requirement, and the strictest-progress bar cannot reveal the other.
func TestDescribeRequiresNamesEveryone(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 7)
	if err != nil {
		t.Fatal(err)
	}

	one, five, cash := 1, 5, 2000
	rep := 4.0
	delivered := 5

	cases := []struct {
		name string
		req  Requirement
		want string
	}{
		{"singular", Requirement{Deliveries: &one}, "1 delivery"},
		{"plural", Requirement{Deliveries: &five}, "5 deliveries"},
		{"money", Requirement{MoneyEarned: &cash}, "earn $2000"},
		{"reputation", Requirement{Reputation: &rep}, "reputation 4.0"},
		{"item", Requirement{ItemDelivered: &ItemReq{Item: "sushi", Delivered: &delivered}}, "5 Sushi delivered"},
		{"two parts", Requirement{MoneyEarned: &cash, Reputation: &rep}, "earn $2000 and reputation 4.0"},
	}
	for _, c := range cases {
		if got := w.describeRequires(c.req); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
