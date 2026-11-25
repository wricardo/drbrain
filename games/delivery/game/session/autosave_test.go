package session

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// TestPausedSessionFlushesSameTickWork covers mutations that never advance the
// tick (buyCar, towCar, moveCar): the paused autosave interval counts ticks, so
// without a wall-clock debounce that work could stay unsaved forever.
func TestPausedSessionFlushesSameTickWork(t *testing.T) {
	city, cfg := testCity(t)
	store := sessionstore.NewMemory(sessionstore.Options{})
	s, err := newWithStore("dirty1", city, cfg, "city", 9, store, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()

	s.Do(func(w *engine.World) { w.Ledger.Money = 12345 })

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_, blob, err := store.Load("dirty1")
		if err == nil {
			var p persisted
			if json.Unmarshal(blob, &p) == nil && p.World.Ledger != nil && p.World.Ledger.Money == 12345 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("same-tick mutation was never autosaved by the paused loop")
}

// gatedStore blocks the first Persist until release closes, so a test can hold
// saveMu and prove the pause flush waits instead of being skipped.
type gatedStore struct {
	sessionstore.Store
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	writes  int
}

func newGatedStore(inner sessionstore.Store) *gatedStore {
	return &gatedStore{Store: inner, entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g *gatedStore) Persist(meta sessionstore.Meta, payload []byte) error {
	g.mu.Lock()
	g.writes++
	g.mu.Unlock()
	g.once.Do(func() {
		g.entered <- struct{}{}
		<-g.release
	})
	return g.Store.Persist(meta, payload)
}

func (g *gatedStore) writeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writes
}

// TestPauseFlushWaitsForInflightSave pins the fix for a pause that cleared its
// dirty flag and then skipped the write because an explicit save held saveMu.
func TestPauseFlushWaitsForInflightSave(t *testing.T) {
	city, cfg := testCity(t)
	store := newGatedStore(sessionstore.NewMemory(sessionstore.Options{}))
	s, err := newWithStore("gated1", city, cfg, "city", 11, store, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	saved := make(chan error, 1)
	go func() { saved <- s.Save() }()
	<-store.entered

	paused := make(chan error, 1)
	go func() { paused <- s.SetSpeed(SpeedPaused) }()

	// Give SetSpeed time to reach the flush before releasing the first write.
	time.Sleep(50 * time.Millisecond)
	close(store.release)

	if err := <-saved; err != nil {
		t.Fatalf("explicit save: %v", err)
	}
	if err := <-paused; err != nil {
		t.Fatalf("pause: %v", err)
	}
	if got := store.writeCount(); got != 2 {
		t.Fatalf("persist calls = %d, want 2 (pause must not skip an in-flight save)", got)
	}
}

// TestStreamSnapshotCarriesCityID pins the stream view to the query view: a
// subscription consumer keying maps by city id must not receive "".
func TestStreamSnapshotCarriesCityID(t *testing.T) {
	city, cfg := testCity(t)
	s, err := newWithStore("stream1", city, cfg, "city", 3, nil, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.StreamSnapshot().City.ID; got != "city" {
		t.Fatalf("StreamSnapshot city id = %q, want %q", got, "city")
	}
}
