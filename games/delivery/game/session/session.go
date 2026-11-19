// Package session owns everything between a World and the network: the runtime
// clock, save files, and the view types the API serialises.
//
// The layering matters when you are deciding where a change belongs:
//
//	engine   pure simulation. No I/O, no wall clock, no knowledge of HTTP.
//	session  owns time and storage. Mutex-protected, one goroutine per live
//	         session, and the only place that reads or writes save files.
//	graph    transport. Resolvers translate GraphQL into session calls and
//	         view structs into generated model types. No game rules here.
//
// The view types in this package exist so the wire format is a deliberate
// decision rather than whatever the engine happens to export. That is why
// OrderView carries Name fields the engine stores as ids, and why Session's
// snapshot is built under one lock rather than read field by field by the
// resolver — a client must never observe a half-advanced world.
//
// A session is either loaded or not. Manager keeps only the loaded ones in
// memory and lists everything else straight off disk, which is what makes
// `loaded` meaningful in the API.
package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// Session is one playthrough with its own clock.
//
// All access to World must go through Do, which holds mu. The ticker goroutine
// and HTTP handlers contend for the same lock; because a tick is a fixed,
// short amount of work, a plain mutex is simpler and more predictable than
// queueing commands through a channel.
type Session struct {
	ID        string
	CreatedAt time.Time

	mu    sync.Mutex
	world *engine.World

	// speed is the tick multiplier. 0 means paused.
	speed int

	// stop closes the ticker goroutine.
	stop chan struct{}
	// done is closed when the goroutine has exited, so Close can wait.
	done chan struct{}
	// running guards against starting the ticker twice.
	running bool

	// dir is where this session is persisted.
	store sessionstore.Store

	// hub fans state changes out to live subscribers. It has its own lock and
	// is never held together with mu, so publishing cannot stall the tick loop
	// behind a slow reader.
	hub *watchHub
}

// Speed values accepted by SetSpeed.
const (
	SpeedPaused = 0
	Speed1x     = 1
	Speed2x     = 2
	Speed4x     = 4
)

// validID rejects anything that could escape the sessions directory.
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidID reports whether an id is safe to use as a filename.
func ValidID(id string) bool { return validID.MatchString(id) }

// Do runs fn with exclusive access to the world.
//
// Everything that reads or mutates game state goes through here. It is a
// function rather than Lock/Unlock so a caller cannot forget to unlock, and so
// the critical section is obvious in review.
func (s *Session) Do(fn func(w *engine.World)) {
	// The unlock is deferred so a panicking callback cannot wedge the session
	// forever; the notify runs after the lock is released by unwrapping into a
	// closure rather than returning early.
	s.mu.Lock()
	func() {
		defer s.mu.Unlock()
		fn(s.world)
	}()
	// Every mutating entry point goes through here (the resolvers all do), so
	// this is the one place that guarantees a subscriber hears about a manual
	// step, a queued move or a purchase. It is outside the lock so a reader
	// woken by it can immediately take the lock to build its snapshot.
	s.notify()
}

// New creates a session from a city config. The ticker is not started; call
// Start.
func New(id string, city *engine.City, cfg *engine.CityConfig, seed uint64, dir string) (*Session, error) {
	var st sessionstore.Store
	if dir != "" {
		var err error
		st, err = sessionstore.Open(sessionstore.Options{Dir: dir, Summary: summarizePayload})
		if err != nil {
			return nil, err
		}
	}
	return newWithStore(id, city, cfg, seed, st)
}

// newWithStore builds a session sharing the manager's store.
func newWithStore(id string, city *engine.City, cfg *engine.CityConfig, seed uint64, st sessionstore.Store) (*Session, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	w, err := engine.NewWorldSeeded(city, cfg, seed)
	if err != nil {
		return nil, err
	}

	return &Session{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		world:     w,
		// Sessions start PAUSED. This game is manual driving: the player must
		// choose every step, so a live clock would expire orders while a human
		// is still reading the screen. A paused clock makes the loop turn-based,
		// which is what "drive it yourself" actually means. Agents and
		// spectators resume with SetSpeed; nothing is lost by defaulting to 0.
		speed: SpeedPaused,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		store: st,
		hub:   newWatchHub(),
	}, nil
}

// TickInterval is the wall-clock gap between ticks at 1x.
func (s *Session) TickInterval() time.Duration {
	ms := s.world.City.TickMS
	if ms <= 0 {
		ms = engine.DefaultTickMS
	}
	return time.Duration(ms) * time.Millisecond
}

// Start launches the ticker goroutine. It is safe to call once; further calls
// are no-ops.
func (s *Session) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go s.loop()
}

// loop advances the world on a wall clock, honouring pause and speed.
//
// The tick rate is recomputed every iteration rather than using a fixed
// ticker, because speed changes must take effect immediately and a paused
// session must not accumulate ticks to fire in a burst on resume.
func (s *Session) loop() {
	defer close(s.done)

	for {
		s.mu.Lock()
		speed := s.speed
		s.mu.Unlock()

		if speed == SpeedPaused {
			select {
			case <-s.stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}

		interval := s.TickInterval() / time.Duration(speed)
		select {
		case <-s.stop:
			return
		case <-time.After(interval):
			s.mu.Lock()
			advanced := false
			if s.speed != SpeedPaused {
				s.world.Advance()
				advanced = true
			}
			s.mu.Unlock()
			// Publish outside the lock: a subscriber that reads the snapshot
			// immediately must not deadlock against the world lock.
			if advanced {
				s.notify()
			}
		}
	}
}

// Close stops the ticker and waits for the goroutine to exit.
//
// It also ends every live subscription. Without that, a client watching a
// session that gets evicted or deleted would sit blocked on a watch nobody will
// ever publish to, holding its connection open until the browser gives up.
func (s *Session) Close() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		s.hub.closeAll()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stop)
	<-s.done
	s.hub.closeAll()
}

// Speed returns the current multiplier (0 when paused).
func (s *Session) Speed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.speed
}

// SetSpeed changes the multiplier. It rejects values other than the known
// speeds so a client cannot accidentally request a 100x server melt.
func (s *Session) SetSpeed(speed int) error {
	switch speed {
	case SpeedPaused, Speed1x, Speed2x, Speed4x:
	default:
		return fmt.Errorf("unsupported speed %d (use 0, 1, 2 or 4)", speed)
	}
	s.mu.Lock()
	s.speed = speed
	s.mu.Unlock()
	// A speed change is a state change: subscribers must see `paused` flip even
	// though no tick occurred.
	s.notify()
	return nil
}

// Paused reports whether the clock is stopped.
func (s *Session) Paused() bool { return s.Speed() == SpeedPaused }

// persisted is the on-disk shape of a session.
//
// It stores the city file name rather than the whole city, so fixing map
// content applies to existing saves instead of freezing old geography into
// them: the save keeps only what the session changed, and completed builds are
// replayed from the (current) city config on load.
type persisted struct {
	ID        string            `json:"id"`
	CityFile  string            `json:"city_file"`
	CreatedAt time.Time         `json:"created_at"`
	Speed     int               `json:"speed"`
	World     engine.SavedState `json:"world"`
}

// Save writes the session to disk atomically.
//
// The state is deep-copied under the session lock and marshalled and written
// outside it. Marshalling in place was a data race: the ticker goroutine keeps
// mutating cars and the event log while the filesystem write is in progress.
//
// The write goes to a temp file and is renamed into place, so a crash mid-write
// cannot leave a half-serialised save that fails to load later.
func (s *Session) Save(cityFile string) error {
	if s.store == nil {
		return nil
	}

	s.mu.Lock()
	data := persisted{
		ID:        s.ID,
		CityFile:  cityFile,
		CreatedAt: s.CreatedAt,
		Speed:     s.speed,
		World:     s.world.ExportState(),
	}
	s.mu.Unlock()

	blob, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return s.store.Persist(sessionstore.Meta{
		ID: s.ID, MapID: cityFile, CreatedAt: s.CreatedAt, LastActionAt: s.CreatedAt,
		Seq: uint64(data.World.Tick),
	}, blob)
}

// summarizePayload derives the platform Meta from a delivery save.
func summarizePayload(raw []byte) (sessionstore.Meta, error) {
	var p persisted
	if err := json.Unmarshal(raw, &p); err != nil {
		return sessionstore.Meta{}, err
	}
	return sessionstore.Meta{
		ID: p.ID, MapID: p.CityFile, CreatedAt: p.CreatedAt, LastActionAt: p.CreatedAt,
		Seq: uint64(p.World.Tick),
	}, nil
}
