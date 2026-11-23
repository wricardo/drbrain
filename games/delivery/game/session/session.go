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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
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

	// displayName is the player's name for this save; "" falls back to the
	// city name. Guarded by mu because rename may race a summary read.
	displayName string

	// lastSavedTick is the world tick of the newest autosave. Storing it before
	// the write means a failing store costs one interval per attempt instead of
	// retrying on every tick.
	lastSavedTick atomic.Int64
	// saveMu serialises every save, explicit or autosave, so the store and the
	// history projection always reflect snapshots in order. Autosave uses
	// TryLock: a slow store skips a background save rather than queueing behind
	// the tick loop.
	saveMu sync.Mutex
	// saveRequested asks the loop to persist once on its next pass. The paused
	// branch needs it because a paused world produces no ticks to count.
	saveRequested atomic.Bool
	// dirtyAt is when the last mutation ran, in Unix nanoseconds (0 = nothing
	// unsaved). It lets a paused session flush same-tick work (a purchase, a
	// tow) that never advances the tick autosave counts.
	dirtyAt atomic.Int64

	// history projects the ledger and orders into indexed tables after a save.
	// It is nil for sessions built without a manager (unit tests). All fields
	// below are guarded by saveMu, which serialises every save.
	history      *historyWriter
	historyReady bool
	// historyCashSeq is the highest cash seq already projected (-1 = none).
	historyCashSeq int
	// historyReputationSeq is the highest reputation seq already projected.
	historyReputationSeq int
	// historyOrders is the last projected signature per order id.
	historyOrders map[string]orderSignature
}

// Speed values accepted by SetSpeed.
const (
	SpeedPaused = 0
	Speed1x     = 1
	Speed2x     = 2
	Speed4x     = 4
)

// ErrPaused reports that a wait cannot finish because the clock is stopped.
// A paused session never advances, so waiting for a future tick would hang.
var ErrPaused = errors.New("session is paused")

// ErrClosed reports that the session ended while a caller was waiting on it.
var ErrClosed = errors.New("session closed")

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
	// A paused clock never reaches the loop's autosave interval, so record that
	// there is unsaved progress for the loop to pick up. Same-tick work (a
	// purchase, a tow, a queued move) never advances the tick that interval
	// counts, so it also records when it happened.
	s.saveRequested.Store(true)
	if !changed {
		s.dirtyAt.Store(time.Now().UnixNano())
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
		// -1 means "nothing projected yet"; the first save loads the real
		// watermark from the history table.
		historyCashSeq:       -1,
		historyReputationSeq: -1,
	}, nil
}

// CityID returns the catalogue id this session snapshotted at creation. It is
// immutable after construction, so readers do not need the world lock.
func (s *Session) CityID() string { return s.cityID }

// DisplayName returns the player's name for this save ("" when unnamed).
func (s *Session) DisplayName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.displayName
}

// Rename sets the save's player-facing name. An empty name clears it, which
// every view renders as the city name. The caller persists with Save; this only
// mutates memory so a batch of renames costs one write.
func (s *Session) Rename(name string) {
	name = strings.TrimSpace(name)
	if len(name) > MaxDisplayNameLen {
		name = name[:MaxDisplayNameLen]
	}
	s.mu.Lock()
	s.displayName = name
	s.mu.Unlock()
}

// MaxDisplayNameLen bounds a session name. It matches the city name bound so a
// picker never has to truncate two different kinds of title.
const MaxDisplayNameLen = 80

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
			// hand-played session gets its periodic save. Same-tick work that
			// never advances the tick is flushed after a short debounce instead,
			// or it would wait for 60 further ticks that a paused session may
			// never see.
			if s.saveRequested.Load() {
				tick := s.CurrentTick()
				due := tick-s.lastSavedTick.Load() >= s.world.AutosaveTicks()
				if at := s.dirtyAt.Load(); !due && at != 0 {
					due = time.Since(time.Unix(0, at)) >= pausedSaveDebounce
				}
				if due {
					s.saveRequested.Store(false)
					s.lastSavedTick.Store(tick)
					s.dirtyAt.Store(0)
					// Blocking: while the session is paused there is no clock to
					// stall, and a TryLock skip here silently dropped the flush
					// when an explicit save already held the lock.
					s.flush()
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
		// no ticks for the autosave interval to count: flush here instead. Use
		// the blocking flush so an explicit save in flight cannot make the
		// pause drop its write.
		s.saveRequested.Store(false)
		tick := s.CurrentTick()
		s.flush()
		s.lastSavedTick.Store(tick)
		s.dirtyAt.Store(0)
	}
	return nil
}

// CurrentTick is the world tick under the session lock.
func (s *Session) CurrentTick() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.world.Tick
}

// WaitUntil blocks until the world reaches target, or ctx ends.
//
// It waits on the session's live clock rather than advancing it: callers that
// want immediate progress use Do/tick. A paused session never reaches a future
// tick, so this returns ErrPaused immediately (and if the caller pauses while
// waiting) instead of blocking forever. Closing the session returns ErrClosed.
// A target at or below the current tick returns at once.
func (s *Session) WaitUntil(ctx context.Context, target int64) error {
	watch := s.Watch()
	defer s.Unwatch(watch)

	for {
		s.mu.Lock()
		tick := s.world.Tick
		paused := s.speed == SpeedPaused
		s.mu.Unlock()

		if tick >= target {
			return nil
		}
		if paused {
			return ErrPaused
		}
		if !watch.NextContext(ctx) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return ErrClosed
		}
	}
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
	// DisplayName is the player's name for this save. "" falls back to the
	// city name in every view, so old payloads keep working unchanged.
	DisplayName string `json:"display_name,omitempty"`
}

// pausedSaveDebounce is how long a same-tick mutation waits before the paused
// loop writes it. Long enough to coalesce a burst of clicks (a purchase and an
// assignment), short enough that a crash costs seconds of manual work.
const pausedSaveDebounce = time.Second

// flush saves the session, waiting for any save already in flight.
//
// autosave must not queue behind an explicit save because it runs next to a
// running tick clock; a paused session has no clock to stall, so dropping the
// write there is never acceptable.
func (s *Session) flush() {
	if s.store == nil {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	_ = s.save()
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
	_ = s.save()
}

// Save persists the session through the manager's store and then projects the
// derived history tables.
//
// Saves are serialised: an explicit save must not interleave with an autosave
// and leave the projection reflecting an older snapshot. Autosave skips when a
// save is already running; explicit saves wait for it.
func (s *Session) Save() error {
	if s.store == nil {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.save()
}

// save is the body of Save. Callers must hold saveMu.
func (s *Session) save() error {
	if s.store == nil {
		return nil
	}

	s.mu.Lock()
	data := persisted{
		ID:          s.ID,
		CityID:      s.cityID,
		CreatedAt:   s.CreatedAt,
		Speed:       s.speed,
		World:       s.world.ExportState(),
		AccountID:   s.accountID,
		OwnerHash:   s.ownerHash,
		DisplayName: s.displayName,
	}
	counters := &sessionstore.Counters{
		Tick: data.World.Tick,
	}
	if data.World.Ledger != nil {
		counters.Money = data.World.Ledger.Money
		counters.Reputation = data.World.Ledger.Reputation
		counters.Deliveries = data.World.Ledger.Deliveries
	}
	s.mu.Unlock()

	blob, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := s.store.Persist(sessionstore.Meta{
		ID: s.ID, MapID: s.cityID, CreatedAt: s.CreatedAt, LastActionAt: time.Now().UTC(),
		Seq: uint64(data.World.Tick), DisplayName: data.DisplayName,
		AccountID: data.AccountID, Counters: counters,
	}, blob); err != nil {
		return err
	}
	s.projectHistory(data)
	return nil
}
