package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/wricardo/drbrain-delivery/game/engine"
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
	dir string
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
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.world)
}

// New creates a session from a city config. The ticker is not started; call
// Start.
func New(id string, city *engine.City, cfg *engine.CityConfig, seed uint64, dir string) (*Session, error) {
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
		dir:   dir,
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
			if s.speed != SpeedPaused {
				s.world.Advance()
			}
			s.mu.Unlock()
		}
	}
}

// Close stops the ticker and waits for the goroutine to exit.
func (s *Session) Close() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stop)
	<-s.done
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
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
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

	final := filepath.Join(s.dir, s.ID+".json")
	tmp, err := os.CreateTemp(s.dir, s.ID+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp save: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp save: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp save: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("commit save: %w", err)
	}
	return nil
}
