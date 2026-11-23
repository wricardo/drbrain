package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// SchemaVersion is the current payload format version of a stored session.
const SchemaVersion = 1

// PersistedSlot is one measure placement in the player's arrangement.
type PersistedSlot struct {
	MeasureID string `json:"measure_id"`
	FlipH     bool   `json:"flip_h"`
	FlipV     bool   `json:"flip_v"`
}

// PersistedSession is the JSON payload of a session.
//
// LastChange is transient and deliberately not stored: after a restart a
// session's last change is unknown, not stale.
type PersistedSession struct {
	Version      int             `json:"version"`
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name"`
	Difficulty   string          `json:"difficulty"`
	Seed         int64           `json:"seed"`
	Piece        score.Piece     `json:"piece"` // frozen snapshot taken at creation
	Slots        []PersistedSlot `json:"slots"`
	Current      int             `json:"current"`
	Seq          uint64          `json:"seq"`
	CreatedAt    time.Time       `json:"created_at"`
	LastActionAt time.Time       `json:"last_action_at"`
	Wins         int             `json:"wins"`
	Solved       bool            `json:"solved"`
}

// StorePersistence adapts platform/sessionstore to this game: the store owns
// PostgreSQL rows, limits and listing; music keeps its JSON shape and
// validation.
type StorePersistence struct {
	store sessionstore.Store
}

var _ SessionPersistence = (*StorePersistence)(nil)

// NewPostgresPersistence opens the PostgreSQL-backed store.
func NewPostgresPersistence(pool *pgxpool.Pool, maxSessions int) (*StorePersistence, error) {
	store, err := sessionstore.Open(sessionstore.Options{Pool: pool, Game: "music", MaxSessions: maxSessions})
	if err != nil {
		return nil, err
	}
	return &StorePersistence{store: store}, nil
}

// NewMemoryPersistence returns an in-memory store for tests that exercise the
// manager and service without a database. The server never selects it.
func NewMemoryPersistence(maxSessions int) *StorePersistence {
	return &StorePersistence{store: sessionstore.NewMemory(sessionstore.Options{MaxSessions: maxSessions})}
}

// Save writes the snapshot to the store. s must not be mutated concurrently;
// the Manager passes a private clone.
func (sp *StorePersistence) Save(s *service.Session) error {
	if s == nil {
		return fmt.Errorf("save session: nil session")
	}
	if !ValidID(s.ID) {
		return ErrInvalidID
	}
	if s.Piece == nil {
		return fmt.Errorf("save session %s: missing piece snapshot", s.ID)
	}
	slots := make([]PersistedSlot, len(s.Slots))
	for i, p := range s.Slots {
		slots[i] = PersistedSlot{MeasureID: p.MeasureID, FlipH: p.Transform.FlipH, FlipV: p.Transform.FlipV}
	}
	data, err := json.Marshal(PersistedSession{
		Version:      SchemaVersion,
		ID:           s.ID,
		DisplayName:  s.DisplayName,
		Difficulty:   s.Difficulty.String(),
		Seed:         s.Seed,
		Piece:        *s.Piece,
		Slots:        slots,
		Current:      s.Current,
		Seq:          s.Seq,
		CreatedAt:    s.CreatedAt,
		LastActionAt: s.LastActionAt,
		Wins:         s.Wins,
		Solved:       s.Solved,
	})
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	return sp.store.Persist(sessionstore.Meta{
		ID: s.ID, DisplayName: s.DisplayName, MapID: s.Piece.ID,
		CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt, Seq: s.Seq,
	}, data)
}

// Load reads and fully validates one stored session. The frozen piece is
// re-canonicalised (positional measure ids, axis) and every slot must name a
// measure that exists in it. A legacy arrangement that does not cover the whole
// piece is repaired to the deterministic whole-piece scramble for its
// (piece, difficulty, seed) and logged.
func (sp *StorePersistence) Load(id string) (*service.Session, error) {
	if !ValidID(id) {
		return nil, ErrInvalidID
	}
	_, raw, err := sp.store.Load(id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	var ps PersistedSession
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", id, err)
	}
	if ps.Version != SchemaVersion {
		return nil, fmt.Errorf("session %s: unsupported version %d (want %d)", id, ps.Version, SchemaVersion)
	}
	if ps.ID != id {
		return nil, fmt.Errorf("session %s payload contains id %q", id, ps.ID)
	}
	piece, err := canonicalPiece(&ps.Piece)
	if err != nil {
		return nil, fmt.Errorf("session %s: invalid piece snapshot: %w", id, err)
	}
	difficulty, err := engine.ParseDifficulty(ps.Difficulty)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	slots, err := placements(piece, ps.Slots)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	// Legacy files written before the whole-piece rule store the truncated
	// window arrangement for a piece whose snapshot now has all of its bars. The
	// snapshot may also predate the 6-bar content cap, which is why canonicalPiece
	// does not re-cap it. The old slots cannot be carried over: the new challenge
	// has different slots and the old target was a prefix. Rebuild the scramble
	// deterministically from the frozen piece, difficulty and seed, preserving
	// identity and progress counters.
	repaired := len(slots) != len(piece.Measures)
	if repaired {
		puzzle, gerr := engine.Generate(piece, difficulty, ps.Seed)
		if gerr != nil {
			return nil, fmt.Errorf("session %s: legacy %d-slot arrangement on a %d-measure piece cannot be repaired: %w", id, len(slots), len(piece.Measures), gerr)
		}
		log.Printf("session %s: repairing legacy %d-slot arrangement to the whole %d-measure piece", id, len(slots), len(piece.Measures))
		slots = puzzle.Slots
	}
	if ps.Current < 0 || ps.Current >= len(slots) {
		return nil, fmt.Errorf("session %s: current %d outside 0..%d", id, ps.Current, len(slots)-1)
	}
	if ps.Wins < 0 {
		return nil, fmt.Errorf("session %s: wins %d is negative", id, ps.Wins)
	}
	solved := (&engine.Puzzle{Piece: piece, Slots: slots}).Solved()
	if !repaired && solved != ps.Solved {
		return nil, fmt.Errorf("session %s: solved flag %v disagrees with the arrangement", id, ps.Solved)
	}
	last := ps.LastActionAt
	if last.IsZero() {
		last = ps.CreatedAt
	}
	return &service.Session{
		ID: ps.ID, DisplayName: ps.DisplayName, Difficulty: difficulty, Seed: ps.Seed,
		Piece: piece, Slots: slots, Current: ps.Current, Seq: ps.Seq,
		CreatedAt: ps.CreatedAt, LastActionAt: last, Wins: ps.Wins, Solved: solved,
	}, nil
}

// Delete removes one session from the store. It returns ErrNotFound if there
// is none.
func (sp *StorePersistence) Delete(id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	if err := sp.store.Delete(id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("remove session %s: %w", id, err)
	}
	return nil
}

// ListAll returns the ids of every persisted session with a valid id; entries
// with an unusable id are ignored.
func (sp *StorePersistence) ListAll() ([]string, error) {
	metas, err := sp.store.List()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(metas))
	for _, m := range metas {
		if ValidID(m.ID) {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// canonicalPiece reinstates what score files derive at load: the fixed axis,
// canonical note order and positional measure ids. It revalidates the snapshot
// without the MaxMeasures content cap, so a session frozen before the 6-bar rule
// keeps loading with its longer piece.
func canonicalPiece(p *score.Piece) (*score.Piece, error) {
	if p == nil || p.ID == "" {
		return nil, errors.New("piece id is empty")
	}
	raw, err := score.Encode(p)
	if err != nil {
		return nil, err
	}
	return score.ParseSnapshot(p.ID, raw)
}

// placements converts persisted slots and checks they are a subset of the
// frozen piece, each named exactly once.
func placements(piece *score.Piece, slots []PersistedSlot) ([]engine.Placement, error) {
	if len(slots) == 0 {
		return nil, errors.New("arrangement is empty")
	}
	if len(slots) > len(piece.Measures) {
		return nil, fmt.Errorf("arrangement has %d slots but the piece has %d measures", len(slots), len(piece.Measures))
	}
	ids := make(map[string]bool, len(piece.Measures))
	for _, m := range piece.Measures {
		ids[m.ID] = true
	}
	out := make([]engine.Placement, len(slots))
	seen := make(map[string]bool, len(slots))
	for i, p := range slots {
		if !ids[p.MeasureID] {
			return nil, fmt.Errorf("slot %d names measure %q, which is not in the piece", i, p.MeasureID)
		}
		if seen[p.MeasureID] {
			return nil, fmt.Errorf("measure %q appears twice", p.MeasureID)
		}
		seen[p.MeasureID] = true
		out[i] = engine.Placement{MeasureID: p.MeasureID, Transform: engine.Transform{FlipH: p.FlipH, FlipV: p.FlipV}}
	}
	return out, nil
}
