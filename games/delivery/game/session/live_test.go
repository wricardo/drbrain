package session

import (
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// LiveSummaries is the scrape view: memory only, no store reads. A save that
// is not loaded must not appear even though List would report it.
func TestLiveSummariesExcludesStoredSessions(t *testing.T) {
	mgr := newMemoryManager(t)

	live, _, err := mgr.Create("live", 1, "", "city")
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	live.Do(func(w *engine.World) { w.Ledger.Money = 321 })

	saved, _, err := mgr.Create("saved", 1, "", "city")
	if err != nil {
		t.Fatalf("create saved: %v", err)
	}
	saved.Do(func(w *engine.World) { w.Ledger.Money = 99 })
	if err := saved.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	mgr.Evict("saved")

	got := mgr.LiveSummaries()
	if len(got) != 1 {
		t.Fatalf("live summaries = %d, want 1", len(got))
	}
	if got[0].ID != "live" || got[0].CityID != "city" || got[0].Money != 321 {
		t.Errorf("summary = %+v, want live in city with 321", got[0])
	}
}
