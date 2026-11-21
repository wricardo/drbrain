// Package session owns everything between a World and the network: the runtime
// clock, durable saves, and the view types the API serialises.
//
// The layering matters when you are deciding where a change belongs:
//
//	engine   pure simulation. No I/O, no wall clock, no knowledge of HTTP.
//	session  owns time and storage. Mutex-protected, one goroutine per live
//	         session, and the only place that reads or writes durable saves.
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
// memory and lists everything else straight from the store, which is what makes
// `loaded` meaningful in the API.
package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// A session is otherwise only persisted on an explicit saveSession mutation and
// on graceful shutdown, so a crash or a kill -9 lost every tick since the last
// one. The interval is the map's autosave_ticks: sixty ticks is 18s at 1x and
// 4.5s at 4x on the shipped map.

// Session is one playthrough with its own clock.
//
// All access to World must go through Do, which holds mu. The ticker goroutine
// and HTTP handlers contend for the same lock; because a tick is a fixed,
// short amount of work, a plain mutex is simpler and more predictable than
// queueing commands through a channel.
type Session struct {
	ID        string
	CreatedAt time.Time
	// cityID is the catalogue id this session snapshotted at creation. The
	// city itself lives in world.City; saving records the id so a reload can
	// re-read current map content instead of freezing geography in the save.
	cityID string

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

	// accountID is the signed-in player this session belongs to ("" = guest).
	// ownerHash is the SHA-256 (hex) of the secret owner token returned once at
	// creation ("" = a save made before owner tokens, which stays open). Neither
	// is ever part of Snapshot: writes are gated on them, reads are not.
	accountID string
	ownerHash string

	// lastSavedTick is the world tick of the newest autosave. Storing it before
	// the write means a failing store costs one interval per attempt instead of
	// retrying on every tick.
	lastSavedTick atomic.Int64
	// saveMu keeps autosaves from overlapping. TryLock means a store slower
	// than the interval skips a save instead of building a backlog behind the
	// tick loop.
	saveMu sync.Mutex
	// saveRequested asks the loop to persist once on its next pass. The paused
	// branch needs it because a paused world produces no ticks to count.
	saveRequested atomic.Bool
}

// Speed values accepted by SetSpeed.
const (
	SpeedPaused = 0
	Speed1x     = 1
	Speed2x     = 2
	Speed4x     = 4
)

// validID rejects anything that could not be a store key.
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidID reports whether id can name a session and be used as a store key:
// 1-64 characters from [A-Za-z0-9_-]. Unlike city ids it allows uppercase; it
// is a shape check, not proof of ownership.
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
	before := s.world.Tick
	changed := func() bool {
		defer s.mu.Unlock()
		fn(s.world)
		return s.world.Tick != before
	}()
	if changed {
		// A paused clock never reaches the loop's autosave interval, so record
		// that there is unsaved progress for the loop to pick up.
		s.saveRequested.Store(true)
	}
	// Every mutating entry point goes through here (the resolvers all do), so
	// this is the one place that guarantees a subscriber hears about a manual
	// step, a queued move or a purchase. It is outside the lock so a reader
	// woken by it can immediately take the lock to build its snapshot.
	s.notify()
}

// New builds an unpersisted, ownerless session from an already-validated city
// and the config that produced it. The city must already include its initial
// fog; city and cfg must belong together. The ticker is not started; call
// Start. Production creation goes through Manager.Create, which adds a store
// and mints an owner token.
func New(id string, city *engine.City, cfg *engine.CityConfig, seed uint64) (*Session, error) {
	return newWithStore(id, city, cfg, "", seed, nil, "", "")
}

// newWithStore builds a session sharing the manager's store. accountID and
// ownerHash may be empty (guest session, or one created before owner tokens).
// cityID is the catalogue id persisted with the save ("" for the legacy New
// constructor, which never persists).
func newWithStore(id string, city *engine.City, cfg *engine.CityConfig, cityID string, seed uint64, st sessionstore.Store, accountID, ownerHash string) (*Session, error) {
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
		cityID:    cityID,
		world:     w,
		accountID: accountID,
		ownerHash: ownerHash,
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

// CityID returns the catalogue id this session snapshotted at creation. It is
// immutable after construction, so readers do not need the world lock.
func (s *Session) CityID() string { return s.cityID }

// TickInterval is the wall-clock gap between ticks at 1x, derived from the
// city's tick_ms. At 2x or 4x the ticker fires proportionally faster; at paused
// it does not tick at all.
func (s *Session) TickInterval() time.Duration {
	return time.Duration(s.world.City.TickMS) * time.Millisecond
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
			// Manual play advances the world through Do, which marks the session
			// dirty; a paused clock produces no tick cadence, so this is where a
			// hand-played session gets its periodic save.
			if s.saveRequested.Load() {
				tick := s.currentTick()
				if tick-s.lastSavedTick.Load() >= s.world.AutosaveTicks() {
					s.lastSavedTick.Store(tick)
					s.saveRequested.Store(false)
					s.autosave()
				}
			}
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
			var tick int64
			if s.speed != SpeedPaused {
				s.world.Advance()
				advanced = true
				tick = s.world.Tick
			}
			s.mu.Unlock()
			// Publish outside the lock: a subscriber that reads the snapshot
			// immediately must not deadlock against the world lock.
			if advanced {
				s.notify()
				if tick-s.lastSavedTick.Load() >= s.world.AutosaveTicks() {
					s.lastSavedTick.Store(tick)
					s.autosave()
				}
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
	if speed == SpeedPaused {
		// Pausing is the natural end of a session, and a paused world produces
		// no ticks for the autosave interval to count: flush here instead.
		s.lastSavedTick.Store(s.currentTick())
		s.autosave()
	}
	return nil
}

// currentTick is the world tick under the session lock.
func (s *Session) currentTick() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.world.Tick
}

// Paused reports whether the clock is stopped; it is exactly Speed() ==
// SpeedPaused. Commands are still accepted while paused.
func (s *Session) Paused() bool { return s.Speed() == SpeedPaused }

// persisted is the stored shape of a session.
//
// It stores the city id rather than the whole city, so fixing map content
// applies to existing saves instead of freezing old geography into them: the
// save keeps only what the session changed, and completed growth stages are
// replayed from the (current) city config on load.
//
// CityFile is the pre-catalogue field. It is still read on load (its base is
// tried as a city id, then the default applies) but no longer written. A new
// save has an empty city_file, which keeps old and new payloads unambiguous.
type persisted struct {
	ID        string            `json:"id"`
	CityID    string            `json:"city_id,omitempty"`
	CityFile  string            `json:"city_file,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Speed     int               `json:"speed"`
	World     engine.SavedState `json:"world"`
	// AccountID links the session to a signed-in player; OwnerHash is the
	// SHA-256 of its secret owner token. Both are absent for guest sessions and
	// for saves written before owner tokens, so old and new guest files are
	// byte-identical.
	AccountID string `json:"account_id,omitempty"`
	OwnerHash string `json:"owner_hash,omitempty"`
}

// autosave persists the session without ever queueing behind another save.
//
// It runs from the tick loop, so a store slower than the autosave interval
// skips a save (the next interval retries) rather than building a backlog that
// stalls the clock. Errors are ignored here on purpose: the only thing a player
// could do about a failed background write is nothing, and Close/SaveAll still
// reports the real error.
func (s *Session) autosave() {
	if s.store == nil {
		return
	}
	if !s.saveMu.TryLock() {
		return
	}
	defer s.saveMu.Unlock()
	_ = s.Save()
}

// Save persists the session through the manager's store.
//
// The state is deep-copied under the session lock and marshalled outside it.
// Marshalling in place was a data race: the ticker goroutine keeps mutating cars
// and the event log while the encode is in progress.
func (s *Session) Save() error {
	if s.store == nil {
		return nil
	}

	s.mu.Lock()
	data := persisted{
		ID:        s.ID,
		CityID:    s.cityID,
		CreatedAt: s.CreatedAt,
		Speed:     s.speed,
		World:     s.world.ExportState(),
		AccountID: s.accountID,
		OwnerHash: s.ownerHash,
	}
	s.mu.Unlock()

	blob, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return s.store.Persist(sessionstore.Meta{
		ID: s.ID, MapID: s.cityID, CreatedAt: s.CreatedAt, LastActionAt: s.CreatedAt,
		Seq: uint64(data.World.Tick),
	}, blob)
}
